#!/usr/bin/env bash
# Instala en el servidor el último Release de GitHub, si es más nuevo que el instalado.
# Lo corre el timer bot-marangatu-update (cada 5 minutos) como root. También se puede correr a mano:
#
#   sudo /opt/bot-marangatu/actualizar-servidor.sh
#
# Si el bot no queda corriendo con la versión nueva, vuelve a la anterior.
set -euo pipefail

REPO="mmallorquin/bot-marangatu-facturas"
INSTALL_DIR="/opt/bot-marangatu"
SERVICE="bot-marangatu.service"
VERSION_FILE="$INSTALL_DIR/VERSION"
FAILED_FILE="$INSTALL_DIR/VERSION.fallida"
HEALTH_WAIT_SECONDS=20

log() { echo "[actualizar] $*"; }

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) log "arquitectura no soportada: $(uname -m)"; exit 1 ;;
esac

# Un solo proceso a la vez.
exec 9>/run/bot-marangatu-update.lock
flock -n 9 || { log "ya hay una actualización en curso"; exit 0; }

# github.com/<repo>/releases/latest redirige a .../releases/tag/<tag>.
latest_url=$(curl -fsS -o /dev/null -w '%{redirect_url}' "https://github.com/$REPO/releases/latest")
tag=${latest_url##*/}
if [[ -z "$tag" || "$tag" == "latest" || "$tag" == "releases" ]]; then
  log "todavía no hay releases publicados"
  exit 0
fi

installed=$(cat "$VERSION_FILE" 2>/dev/null || echo "ninguna")
if [[ "$tag" == "$installed" ]]; then
  exit 0 # ya está al día
fi
if [[ "$tag" == "$(cat "$FAILED_FILE" 2>/dev/null)" ]]; then
  exit 0 # ya falló una vez: se espera al próximo release
fi
log "versión instalada: $installed → nueva: $tag"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
base="https://github.com/$REPO/releases/download/$tag"
for file in "bot-linux-$arch" "metricas-linux-$arch" SHA256SUMS; do
  curl -fsSL --retry 3 -o "$work/$file" "$base/$file"
done
(cd "$work" && sha256sum --check --ignore-missing --quiet SHA256SUMS) || { log "checksum inválido, no instalo nada"; exit 1; }

systemctl stop "$SERVICE"
cp -p "$INSTALL_DIR/bot" "$INSTALL_DIR/bot.previous" 2>/dev/null || true
cp -p "$INSTALL_DIR/metricas" "$INSTALL_DIR/metricas.previous" 2>/dev/null || true
install -m 0755 "$work/bot-linux-$arch" "$INSTALL_DIR/bot"
install -m 0755 "$work/metricas-linux-$arch" "$INSTALL_DIR/metricas"
systemctl start "$SERVICE"
restarts_before=$(systemctl show -p NRestarts --value "$SERVICE")

# El bot no tiene endpoint de salud: alcanza con que siga activo y sin reinicios nuevos.
sleep "$HEALTH_WAIT_SECONDS"
restarts_after=$(systemctl show -p NRestarts --value "$SERVICE")
if systemctl is-active --quiet "$SERVICE" && [[ "$restarts_after" == "$restarts_before" ]]; then
  echo "$tag" > "$VERSION_FILE"
  rm -f "$FAILED_FILE"
  log "instalada $tag"
  exit 0
fi

log "la versión $tag no quedó corriendo; vuelvo a la anterior ($installed)"
journalctl -u "$SERVICE" -n 30 --no-pager || true
systemctl stop "$SERVICE"
if [[ -f "$INSTALL_DIR/bot.previous" ]]; then
  install -m 0755 "$INSTALL_DIR/bot.previous" "$INSTALL_DIR/bot"
fi
if [[ -f "$INSTALL_DIR/metricas.previous" ]]; then
  install -m 0755 "$INSTALL_DIR/metricas.previous" "$INSTALL_DIR/metricas"
fi
systemctl start "$SERVICE"
# Se anota el tag fallido para no reintentarlo cada 5 minutos; el próximo release se instala normal.
echo "$tag" > "$FAILED_FILE"
exit 1
