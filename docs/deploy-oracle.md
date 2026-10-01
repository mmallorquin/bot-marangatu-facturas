# Deploy de bot-marangatu en Oracle

El bot corre como servicio independiente de systemd en Ubuntu. El nombre del servicio es
`bot-marangatu`; mantiene el mismo bot de Telegram y la misma base de facturas.

## Archivos del servidor

| Archivo | Uso |
|---|---|
| `/opt/bot-marangatu/bot` | Ejecutable Linux |
| `/etc/bot-marangatu.env` | Token de Telegram y key de OpenRouter; permisos `0600`, propiedad de root |
| `/var/lib/bot-marangatu/facturas.db` | SQLite, propiedad del usuario exclusivo `bot-marangatu` |
| `/etc/systemd/system/bot-marangatu.service` | Servicio; plantilla en `deploy/bot-marangatu.service` |
| `/var/backups/bot-marangatu/` | Respaldo inicial de la migración; accesible por root |

La carpeta de datos tiene permisos `0700`. El servicio arranca con el servidor y se reinicia
si el proceso falla. `GOMEMLIMIT=192MiB` es un objetivo de memoria del runtime de Go, no un
límite estricto para el proceso completo.

## Estado y logs

Desde la Mac, usando el alias SSH configurado:

```bash
ssh hermes-vm 'sudo systemctl status bot-marangatu.service --no-pager'
ssh hermes-vm 'sudo journalctl -u bot-marangatu.service -n 50 --no-pager'
```

Para reiniciar:

```bash
ssh hermes-vm 'sudo systemctl restart bot-marangatu.service'
```

## Actualizar el ejecutable

### GitHub Actions

El workflow [tests.yml](../.github/workflows/tests.yml) verifica formato, `go vet`,
pruebas Go con `-race` y las pruebas del receptor de despliegues. En PR no accede al
servidor. Un push a `main` que pasa esas pruebas compila `bot` y `metricas` para Linux
amd64 y actualiza Oracle. También puede ejecutarse desde **Actions → tests → Run workflow**
seleccionando `main`.

El entorno GitHub **production** sólo admite la rama `main` y contiene:

| Configuración | Tipo | Uso |
|---|---|---|
| `ORACLE_HOST` | Variable | Dirección del servidor |
| `ORACLE_PORT` | Variable | Puerto SSH; 22 actualmente |
| `ORACLE_USER` | Variable | `bot-marangatu-deploy` |
| `ORACLE_SSH_KEY` | Secret | Clave exclusiva de despliegue |
| `ORACLE_KNOWN_HOSTS` | Secret | Clave del servidor verificada por un acceso administrativo previo |

Los tokens de Telegram y OpenRouter siguen sólo en `/etc/bot-marangatu.env`.
La clave de Actions no permite shell, SCP, PTY ni forwarding; únicamente `status`
y `deploy`. El usuario dedicado no pertenece al grupo del bot ni puede modificar
el receptor, las claves autorizadas o sus permisos sudo.

El receptor root [oracle_deploy.py](../deploy/oracle_deploy.py) acepta un tar sin
comprimir con sólo `bot`, `metricas`, `REVISION` y `SHA256SUMS`. Limita tamaños,
rechaza rutas/enlaces y verifica revisión, arquitectura y checksums antes de
detener el servicio. Mantiene un bloqueo local, además del bloqueo de Actions,
para evitar dos despliegues simultáneos. Actions omite un commit si `main` ya
apunta a otro más nuevo al comenzar la transferencia.

Después de detener el bot, crea un respaldo SQLite consistente en
`/var/backups/bot-marangatu/pre-update-<revision>-<fecha>-<id>/` junto con los
ejecutables anteriores. Reemplaza los binarios de forma atómica, arranca y
comprueba tanto el ejecutable del PID activo como el mensaje de inicio del bot.
Si falla, reinstala los binarios y la revisión anteriores. **No restaura SQLite
automáticamente:** una migración o nuevas facturas no deben sobrescribirse con
una copia vieja. Las migraciones de este proyecto deben seguir siendo compatibles
con la versión anterior para permitir esa recuperación.

Estos respaldos previos a despliegue permanecen en la VM; no sustituyen un backup
programado fuera del servidor.

La instalación inicial se hace con la cuenta administrativa: copiar los tres
archivos de `deploy/` y una clave pública exclusiva a un directorio temporal del
servidor, y ejecutar `sudo sh setup-actions.sh clave.pub`. El instalador no reinicia
el bot. La parte privada se guarda con `gh secret set ORACLE_SSH_KEY --env production`
y nunca se agrega a Git. Actualizaciones del receptor o rotaciones de clave requieren
el mismo acceso administrativo; el workflow sólo reemplaza los dos binarios y la revisión.

### Actualización manual de recuperación

Compilar el bot y el comando de métricas para la arquitectura del servidor (actualmente Linux x86-64)
y transferirlos:

```bash
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/bot-linux-amd64 ./cmd/bot
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/metricas-linux-amd64 ./cmd/metricas
scp bin/bot-linux-amd64 hermes-vm:bot-marangatu-next
scp bin/metricas-linux-amd64 hermes-vm:metricas-next
```

En el servidor, detener el servicio antes de reemplazar el ejecutable. La base y las
credenciales permanecen en sus propias rutas:

```bash
sudo systemctl stop bot-marangatu.service
sudo cp -p /opt/bot-marangatu/bot /opt/bot-marangatu/bot.previous
sudo install -m 0755 "$HOME/bot-marangatu-next" /opt/bot-marangatu/bot
sudo install -m 0755 "$HOME/metricas-next" /opt/bot-marangatu/metricas
sudo systemctl start bot-marangatu.service
sudo systemctl status bot-marangatu.service --no-pager
sudo journalctl -u bot-marangatu.service -n 20 --no-pager
```

Si el ejecutable nuevo falla, detener el servicio e instalar `bot.previous` en la ruta
`/opt/bot-marangatu/bot` antes de arrancarlo nuevamente.

## Métricas de uso

El bot registra eventos de uso en la misma base (tabla `events`): fotos, lecturas, correcciones,
guardadas, exportaciones, costo y tiempos. No guarda datos de las facturas ni el texto de los
mensajes, y los usuarios no ven nada de esto en Telegram. La tabla se crea sola cuando arranca
la versión nueva del bot.

El comando `/opt/bot-marangatu/metricas` se instala junto con el bot (ver "Actualizar el ejecutable").

Consultarlas desde la Mac (abre la base en solo lectura; el bot puede seguir corriendo):

```bash
ssh hermes-vm 'sudo -u bot-marangatu /opt/bot-marangatu/metricas -db /var/lib/bot-marangatu/facturas.db -dias 7'
```

| Opción | Uso |
|---|---|
| `-dias 7` | Período; `-dias 0` es todo el historial |
| `-excluir 123456789` | Chat_id que no cuentan, separados por coma (tus propias pruebas) |
| `-usuarios` | Agrega una fila por chat: primer y último uso, días, fotos, guardadas, ZIP y costo |

Tu chat_id aparece en la tabla de `-usuarios` y en los logs (`chat_id=`).

## Copia de la Mac

Después de migrar, el servicio de launchd queda detenido y deshabilitado para el inicio
automático; su configuración y su base local se conservan. El respaldo inicial también
queda en `exports/migration-20260929/`, fuera de Git.

No ejecutar ambas copias con el mismo token. Para volver a la Mac, primero detener el
servicio de Oracle y trasladar una copia coherente y actualizada de SQLite, incluyendo las
facturas creadas después de la migración. Luego habilitar launchd y reinstalar el servicio
con `scripts/servicio-mac.sh`.

La prueba operativa se hace desde Telegram con `/resumen`, `/exportar` y una foto. La
aceptación del ZIP por Marangatu se verifica por separado desde la cuenta del usuario.
