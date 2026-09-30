#!/usr/bin/env bash
# Activa el deploy automático en el servidor (se corre una sola vez, desde la Mac):
#
#   scripts/instalar-deploy-automatico.sh            # usa el alias SSH hermes-vm
#   scripts/instalar-deploy-automatico.sh ubuntu@IP  # u otro destino SSH
#
# El servidor baja de GitHub el script de actualización y el timer, los instala
# e instala en el momento el último Release. Ver docs/deploy-oracle.md.
set -euo pipefail

HOST="${1:-hermes-vm}"
RAW="https://raw.githubusercontent.com/mmallorquin/bot-marangatu-facturas/main"

echo "Instalando el deploy automático en $HOST…"
ssh -t "$HOST" "set -e
  cd /tmp
  curl -fsSLO $RAW/scripts/actualizar-servidor.sh
  curl -fsSLO $RAW/deploy/bot-marangatu-update.service
  curl -fsSLO $RAW/deploy/bot-marangatu-update.timer
  sudo install -m 0755 actualizar-servidor.sh /opt/bot-marangatu/actualizar-servidor.sh
  sudo install -m 0644 bot-marangatu-update.service bot-marangatu-update.timer /etc/systemd/system/
  sudo systemctl daemon-reload
  sudo systemctl enable --now bot-marangatu-update.timer
  sudo systemctl start bot-marangatu-update.service || true
  sudo journalctl -u bot-marangatu-update.service -n 20 --no-pager
  echo
  echo \"Versión instalada: \$(cat /opt/bot-marangatu/VERSION 2>/dev/null || echo ninguna)\"
  systemctl is-active bot-marangatu.service"
