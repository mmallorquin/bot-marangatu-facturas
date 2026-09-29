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

Compilar para la arquitectura del servidor (actualmente Linux x86-64) y transferirlo:

```bash
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -o bin/bot-linux-amd64 ./cmd/bot
scp bin/bot-linux-amd64 hermes-vm:bot-marangatu-next
```

En el servidor, detener el servicio antes de reemplazar el ejecutable. La base y las
credenciales permanecen en sus propias rutas:

```bash
sudo systemctl stop bot-marangatu.service
sudo cp -p /opt/bot-marangatu/bot /opt/bot-marangatu/bot.previous
sudo install -m 0755 "$HOME/bot-marangatu-next" /opt/bot-marangatu/bot
sudo systemctl start bot-marangatu.service
sudo systemctl status bot-marangatu.service --no-pager
sudo journalctl -u bot-marangatu.service -n 20 --no-pager
```

Si el ejecutable nuevo falla, detener el servicio e instalar `bot.previous` en la ruta
`/opt/bot-marangatu/bot` antes de arrancarlo nuevamente.

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
