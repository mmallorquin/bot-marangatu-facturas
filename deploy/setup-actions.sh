#!/bin/sh
# Ejecutar con sudo sobre una copia verificada de deploy/, con una clave pública.
# No detiene ni reemplaza el bot en ejecución.
set -eu
if [ "$(id -u)" != 0 ] || [ "$#" != 1 ]; then
    echo 'Uso: sudo setup-actions.sh clave-publica' >&2
    exit 1
fi
deploy_source=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
deploy_account=bot-marangatu-deploy
deploy_home=/var/lib/bot-marangatu-deploy
public_key=$(/usr/bin/python3 -c '
import re,sys
key=open(sys.argv[1]).read().strip()
if not re.fullmatch(r"ssh-ed25519 [A-Za-z0-9+/]+={0,2}( [A-Za-z0-9@._-]+)?",key):
    sys.exit("clave pública inválida")
print(key)
' "$1")
if ! id "$deploy_account" >/dev/null 2>&1; then
    /usr/sbin/useradd --system --create-home --home-dir "$deploy_home" --shell /bin/sh "$deploy_account"
fi
/usr/bin/install -o root -g root -m 0755 "$deploy_source/oracle_deploy.py" /usr/local/sbin/bot-marangatu-deploy
/usr/bin/install -o root -g root -m 0755 "$deploy_source/receive.sh" /usr/local/bin/bot-marangatu-receive
/usr/bin/install -d -o root -g root -m 0755 "$deploy_home"
/usr/bin/install -d -o root -g root -m 0755 "$deploy_home/.ssh"
printf 'restrict,command="/usr/local/bin/bot-marangatu-receive" %s\n' "$public_key" > "$deploy_home/.ssh/authorized_keys"
chown root:root "$deploy_home/.ssh/authorized_keys"
chmod 0644 "$deploy_home/.ssh/authorized_keys"
sudoers_file=$(mktemp /etc/sudoers.d/.bot-marangatu-deploy.XXXXXX)
trap 'rm -f "$sudoers_file"' EXIT
printf '%s ALL=(root) NOPASSWD: /usr/local/sbin/bot-marangatu-deploy --apply, /usr/local/sbin/bot-marangatu-deploy --status\n' "$deploy_account" > "$sudoers_file"
chmod 0440 "$sudoers_file"
/usr/sbin/visudo -cf "$sudoers_file"
/usr/bin/install -o root -g root -m 0440 "$sudoers_file" /etc/sudoers.d/bot-marangatu-deploy
echo 'Receptor y cuenta de despliegue configurados; servicio sin cambios.'
