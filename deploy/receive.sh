#!/bin/sh
# La clave SSH sólo permite estas dos operaciones fijas, sin shell ni rutas.
set -eu
case "${SSH_ORIGINAL_COMMAND:-}" in
    deploy) exec /usr/bin/sudo -n /usr/local/sbin/bot-marangatu-deploy --apply ;;
    status) exec /usr/bin/sudo -n /usr/local/sbin/bot-marangatu-deploy --status ;;
    *) echo 'Operación no permitida' >&2; exit 1 ;;
esac
