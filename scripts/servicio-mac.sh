#!/usr/bin/env bash
# Corre el bot como servicio de macOS (launchd): arranca al iniciar sesión,
# se reinicia si se cae y no necesita una terminal abierta.
#
#   scripts/servicio-mac.sh instalar     compila e instala el servicio
#   scripts/servicio-mac.sh actualizar   recompila y reinicia (después de un git pull)
#   scripts/servicio-mac.sh estado       muestra si está corriendo
#   scripts/servicio-mac.sh logs         sigue el log en vivo (Ctrl+C para salir)
#   scripts/servicio-mac.sh desinstalar  detiene el servicio y lo quita
set -euo pipefail

readonly LABEL="com.mmallorquin.bot-marangatu-facturas"
readonly REPO="$(cd "$(dirname "$0")/.." && pwd)"
readonly BIN="$REPO/bin/bot"
readonly PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
readonly LOG="$HOME/Library/Logs/bot-marangatu-facturas.log"
readonly DOMAIN="gui/$(id -u)"
# Si el bot se cae, launchd espera esto (segundos) antes de reiniciarlo.
readonly RESTART_DELAY=30

compilar() {
  echo "Compilando..."
  (cd "$REPO" && go build -o "$BIN" ./cmd/bot)
}

escribir_plist() {
  mkdir -p "$(dirname "$PLIST")" "$(dirname "$LOG")"
  cat > "$PLIST" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>             <string>$LABEL</string>
  <key>ProgramArguments</key>  <array><string>$BIN</string></array>
  <key>WorkingDirectory</key>  <string>$REPO</string>
  <key>RunAtLoad</key>         <true/>
  <key>KeepAlive</key>         <true/>
  <key>ThrottleInterval</key>  <integer>$RESTART_DELAY</integer>
  <key>StandardOutPath</key>   <string>$LOG</string>
  <key>StandardErrorPath</key> <string>$LOG</string>
</dict>
</plist>
PLIST
}

esta_instalado() {
  launchctl print "$DOMAIN/$LABEL" >/dev/null 2>&1
}

instalar() {
  if [[ ! -f "$REPO/.env" ]]; then
    echo "Falta $REPO/.env: copiá .env.example y completá los tokens antes de instalar." >&2
    exit 1
  fi
  if pgrep -f "$REPO/bin/bot|go-build.*/bot$" >/dev/null && ! esta_instalado; then
    echo "Hay otro bot corriendo (¿un 'go run ./cmd/bot'?). Detenelo con Ctrl+C: dos bots con el mismo token chocan." >&2
    exit 1
  fi
  compilar
  escribir_plist
  esta_instalado && launchctl bootout "$DOMAIN/$LABEL" 2>/dev/null || true
  launchctl bootstrap "$DOMAIN" "$PLIST"
  echo "✅ Servicio instalado. Log: $LOG"
}

actualizar() {
  esta_instalado || { echo "El servicio no está instalado: usá 'instalar'." >&2; exit 1; }
  compilar
  launchctl kickstart -k "$DOMAIN/$LABEL"
  echo "✅ Bot reiniciado con la versión nueva."
}

estado() {
  if ! esta_instalado; then
    echo "⏹️  Servicio no instalado."
    return
  fi
  launchctl print "$DOMAIN/$LABEL" | grep -E "^\s*(state|pid|last exit code) =" || true
  echo "Últimas líneas del log:"
  tail -n 5 "$LOG" 2>/dev/null || echo "(sin log todavía)"
}

desinstalar() {
  esta_instalado && launchctl bootout "$DOMAIN/$LABEL" || true
  rm -f "$PLIST"
  echo "⏹️  Servicio detenido y desinstalado. La base de datos y el log quedan intactos."
}

case "${1:-}" in
  instalar)    instalar ;;
  actualizar)  actualizar ;;
  estado)      estado ;;
  logs)        tail -n 20 -f "$LOG" ;;
  desinstalar) desinstalar ;;
  *) sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'; exit 1 ;;
esac
