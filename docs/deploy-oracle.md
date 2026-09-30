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

## Deploy automático desde GitHub

El código se sube a GitHub y el servidor lo trae de ahí; nunca se copia desde la Mac.

1. Cada merge a `main` corre el workflow `release` (`.github/workflows/release.yml`):
   tests, compilación de `bot` y `metricas` para Linux y un Release con `SHA256SUMS`.
2. En el servidor, el timer `bot-marangatu-update` corre cada 5 minutos
   `scripts/actualizar-servidor.sh`. Si hay un Release nuevo, lo baja, verifica los checksums,
   instala los dos ejecutables y reinicia el bot.
3. Si el bot no queda corriendo 20 segundos después, vuelve a la versión anterior y no reintenta
   ese Release. El próximo merge se instala normalmente.

GitHub no necesita ninguna llave del servidor, ni el servidor una de GitHub: el repositorio es
público y el servidor solo descarga. La versión instalada queda en `/opt/bot-marangatu/VERSION`.

### Instalación (una sola vez)

Desde la Mac, en la carpeta del repo, con la conexión de casa: algunas redes corporativas bloquean SSH.

```bash
git pull
scripts/instalar-deploy-automatico.sh            # usa el alias hermes-vm
```

Sin el alias: `scripts/instalar-deploy-automatico.sh ubuntu@144.22.129.166` (con la llave configurada en
`~/.ssh/config` o cargada con `ssh-add`). El script baja los archivos desde GitHub en el servidor,
activa el timer e instala en el momento el Release más reciente. Tiene que terminar con
`Versión instalada: v…` y `active`.

### Seguimiento

```bash
ssh hermes-vm 'cat /opt/bot-marangatu/VERSION'                                        # versión instalada
ssh hermes-vm 'sudo journalctl -u bot-marangatu-update.service -n 30 --no-pager'      # últimas actualizaciones
ssh hermes-vm 'systemctl list-timers bot-marangatu-update.timer --no-pager'           # próxima revisión
ssh hermes-vm 'sudo systemctl start bot-marangatu-update.service'                     # actualizar ya
```

Para pausar las actualizaciones: `sudo systemctl disable --now bot-marangatu-update.timer`.

## Actualizar el ejecutable a mano

Solo si el deploy automático no está disponible.

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

## Métricas de uso

El bot registra eventos de uso en la misma base (tabla `events`): fotos, lecturas, correcciones,
guardadas, exportaciones, costo y tiempos. No guarda datos de las facturas ni el texto de los
mensajes, y los usuarios no ven nada de esto en Telegram. La tabla se crea sola cuando arranca
la versión nueva del bot.

El deploy automático instala `/opt/bot-marangatu/metricas` junto con el bot.

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
