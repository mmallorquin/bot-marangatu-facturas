# bot-marangatu-facturas

Olvidate de transcribir facturas a mano: mandá la foto y el bot prepara el archivo para Marangatu.

> 🚧 Proyecto en construcción, hecho **en público**. Seguí el avance en X con **#buildinpublic**.

## El problema

En Paraguay, quienes liquidan IVA o IRP tienen que registrar sus comprobantes de compra en
[Marangatu](https://marangatu.set.gov.py) (RG 90). Hacerlo factura por factura, copiando RUC,
timbrado, número y montos, lleva horas y genera errores.

## La idea

```
📸 Foto de la factura ──► 🤖 Bot de Telegram ──► 🧠 IA lee los datos ──► 📊 Planilla lista para importar en Marangatu
```

1. Le sacás una foto a la factura (o reenviás el PDF) y se la mandás al bot.
2. El bot extrae los datos: RUC y razón social del emisor, timbrado, número, fecha, condición,
   gravadas 10 % y 5 %, exentas, IVA y total.
3. Te los muestra para que confirmes o corrijas.
4. En **Exportar** elegís el período según tu registro, revisás CSV o Excel y confirmás el ZIP para Marangatu.

El bot **no se conecta a tu cuenta de Marangatu**: vos subís el archivo. Así no tiene que
manejar tus credenciales. Guardar o exportar en el bot no presenta tus registros ante la DNIT.

## Hoja de ruta

- [x] **Etapa 1 — Arranque:** estructura del proyecto y bot de Telegram que recibe fotos
- [x] **Etapa 2 — Lectura:** extraer los datos de la factura desde la foto con IA y validarlos
- [x] **Etapa 3 — Confirmación:** guardar, corregir o descartar cada factura desde el chat
- [x] **Etapa 4 — Exportación:** generar el archivo de importación de la RG 90 para Marangatu
- [ ] **Etapa 5 — Beta:** probarlo con usuarios reales (ya corre en Oracle como `bot-marangatu`; importación del ZIP validada por el administrador, falta probar con otros usuarios)
- [ ] **Etapa 6 — WhatsApp:** sumar WhatsApp como segundo canal
- [ ] **Futuro:** carga automática en Marangatu

### Próximos hitos de la beta

- [x] Validar una importación real del ZIP en Marangatu (confirmado por el administrador).
- [x] Corregir las instrucciones sobre presentación, electrónicas y conservación de comprobantes.
- [x] Configurar la obligación de registro 955 mensual o 956 anual según el RUC, separada de la imputación a impuestos.
- [x] Distinguir ZIP entregado de presentación marcada manualmente por el usuario (el bot no verifica DNIT).
- [ ] Revisar el calendario oficial antes de agregar avisos de vencimiento; los actuales son recordatorios de exportación, no vencimientos.
- [x] Aislar chats privados, proteger borrado durante lecturas y confirmar guardados con validación atómica.
- [x] Proteger confirmaciones de ZIP y pendientes; registrar entregas y recordatorios después de la respuesta de Telegram.
- [x] Automatizar pruebas y despliegue a Oracle desde GitHub Actions, con respaldo previo y recuperación del ejecutable.
- [x] Simplificar el menú, reunir pendientes y guardadas, recorrer el historial y corregir sin repetir OCR.
- [x] Elegir períodos con botones y retomar la exportación después de completar los ajustes.
- [x] Clasificar errores de OpenRouter y pedir lectura nativa de PDF sin OCR adicional implícito.
- [ ] Programar un respaldo fuera de Oracle y probar su restauración (postergado por el administrador).
- [ ] Probar con otros usuarios y medir lecturas, correcciones, errores, costo y tiempo.

El seguimiento normativo y sus límites están en [docs/dnit-vigencia.md](docs/dnit-vigencia.md).
La prueba propuesta de 50 facturas está preparada en [docs/beta-validacion.md](docs/beta-validacion.md); todavía requiere participantes y comprobantes reales.

## Stack

- **[Go](https://go.dev)**: el proyecto también es mi excusa para aprender Go viniendo de Python.
- **[go-telegram/bot](https://github.com/go-telegram/bot)** para el bot de Telegram.
- **[OpenRouter](https://openrouter.ai)** para leer la factura con cualquier modelo con visión.
  Por defecto `google/gemini-3.1-flash-lite` (~3 s y ~USD 0,0009 por factura); se cambia con `OPENROUTER_MODEL`.
  El modelo se eligió comparando con fotos reales: ver [docs/comparacion-modelos.md](docs/comparacion-modelos.md).

```
cmd/bot/              → punto de entrada: arma el bot y lo pone a escuchar
cmd/metricas/         → reporte de uso del bot, leyendo la base en solo lectura
cmd/comparar/         → compara modelos leyendo las fotos de facturas/ (costo, tiempo, resultados)
internal/config/      → lee y valida la configuración (.env)
internal/telegram/    → recibe la foto, la descarga y responde
internal/reader/      → contrato para leer facturas (independiente del proveedor de IA)
internal/openrouter/  → implementación con OpenRouter: prompt, esquema JSON y cliente HTTP
internal/invoice/     → la factura, sus validaciones (RUC módulo 11, IVA, totales) y las correcciones
internal/store/       → base SQLite local: facturas, duplicados, resumen, configuración por chat, eventos de uso
internal/marangatu/   → arma el ZIP para importar compras en Marangatu (RG 90)
internal/benchmark/   → lógica de la comparación de modelos
```

### Cómo se valida una factura

La IA lee la foto, pero los números los verifica el código:

- **RUC**: dígito verificador con el algoritmo oficial de la DNIT (módulo 11).
- **IVA**: IVA 10 % = gravada ÷ 11 e IVA 5 % = gravada ÷ 21, con tolerancia por redondeo.
- **Total**: exentas + gravadas = total.
- **Formatos**: timbrado de 8 dígitos, número `001-001-0000001`, fecha válida, CDC de 44 dígitos.

Si algo no cierra, el bot te avisa qué revisar.

## Cómo correrlo

Necesitás [Go 1.27+](https://go.dev/dl/) y un bot de Telegram.

1. Creá tu bot hablándole a [@BotFather](https://t.me/BotFather) → `/newbot`, y copiá el token.
2. Creá una key en [openrouter.ai/keys](https://openrouter.ai/keys), con un límite de crédito.
3. Configurá las dos:
   ```bash
   cp .env.example .env
   # editá .env: TELEGRAM_BOT_TOKEN y OPENROUTER_API_KEY
   ```
4. Arrancá el bot:
   ```bash
   go run ./cmd/bot
   ```
5. Abrí tu bot en un chat privado de Telegram, mandá `/start` y después una foto de una factura.

El bot no procesa facturas ni comandos en grupos. Puede descargar/leer hasta dos
archivos simultáneamente; el resto espera su turno. Borrar tus datos invalida las
lecturas anteriores, aunque el proveedor tarde en responder.

## Cómo se usa

En Telegram, tocá **Menú** junto al campo de mensaje para elegir un comando. El bot registra
esas opciones automáticamente al arrancar.

La primera vez, `/start` invita a mandar una foto: podés probar sin configurar impuestos.
En Menú hay cuatro entradas: **Mis facturas**, **Exportar**, **Ajustes** y **Ayuda**.
Al exportar por primera vez se pide el RUC (lo escribís tal cual), los impuestos y la obligación
de registro con botones; después se retoma el período solicitado. Elegí la obligación que figure activa en tu RUC en Marangatu.
El botón **No sé** explica dónde consultarla; el bot no la deduce de los impuestos ni consulta tu cuenta.

1. Mandás la foto de la factura o el PDF: una factura por archivo. Podés mandar varias fotos juntas, como álbum. Separá un PDF que reúna varias facturas.
2. El bot la lee y te la muestra con tres botones:

   ```
   [✅ Guardar]  [✏️ Corregir]  [🗑️ Descartar]
   ```

3. **✏️ Corregir**: elegís el campo, escribís el valor correcto (`150.000`, `20/09/2026`, `1-1-1234`…)
   y el bot vuelve a validar.
4. **✅ Guardar**: solo se puede si todos los datos cierran. Si ya habías guardado la misma factura, te avisa
   apenas la lee. Si es electrónica (tiene CDC), no va en el ZIP: obtenela en Marangatu y revisá su imputación.

Con `/autoguardar si`, las facturas que cierran se guardan solas, con un botón **↩️ Deshacer**.
En **Mis facturas** cambiás entre pendientes y guardadas, recorrés todas las páginas y
abrís el detalle. **Corregir** una guardada la devuelve a pendiente en el mismo registro,
sin leer de nuevo el archivo; vuelve a entrar en el ZIP cuando la guardás otra vez.

Al recibir el ZIP aparece **Ya presenté en Marangatu**. Tocá esa opción únicamente
después de confirmar el período en Marangatu y obtener el Talón. La marca es manual;
si cambian las facturas o los ajustes, la previa advierte que esa confirmación corresponde a datos anteriores.

| Comando | Qué hace |
|---|---|
| `/resumen` | Facturas guardadas este mes, con IVA y total |
| `/resumen 08/2026` | Lo mismo para otro mes |
| `/facturas` | Mis facturas: pendientes, guardadas, totales, páginas y detalle editable |
| `/facturas 08/2026` | Lo mismo para otro mes (o `/facturas 2026` para el año) |
| `/exportar` | Selector de mes para 955 o año para 956; previa, CSV, Excel y ZIP |
| `/ajustes` | Cambiar RUC, impuestos, registro y preferencias con botones |
| `/ayuda` | Guía y límites del bot |
| `/exportar 08/2026` | Lo mismo para otro mes |
| `/exportar 2026` | Revisión del año entero; genera ZIP solo con registro 956 |
| `/ruc 1234567-8` | Tu RUC (va en el nombre del archivo) |
| `/imputar iva irp` | A qué impuestos imputás tus compras: `iva`, `ire`, `irp` |
| `/registro` | Consulta o elige con botones la obligación 955 mensual o 956 anual |
| `/registro 955` / `956` | Configura la obligación que corresponda a tu RUC |
| `/pendientes` | Cuenta las facturas leídas sin guardar y guarda de una vez las que cierran |
| `/autoguardar si` / `no` | Guardar solas las facturas que cierran |
| `/recordatorios si` / `no` | Aviso de exportar según `/registro`: desde el día 3 por el mes anterior (955) o el 15 de enero por el año anterior (956); no es un vencimiento oficial |
| `/cancelar` | Cancela una corrección a medias |
| `/borrar_mis_datos` | Borra todo lo que guardaste en el bot, con confirmación |

### Exportar a Marangatu

1. La primera vez: `/ruc 1234567-8`, `/imputar iva` (o los impuestos que correspondan) y `/registro 955` o `/registro 956`, según tu RUC.
2. `/exportar` muestra cuántos comprobantes entrarán, el total, las imputaciones y las primeras 10 filas.
   Si hay excluidos, muestra los primeros 10 con su motivo y cuenta el resto; siguen guardados en el bot.
3. Si querés revisar todo, descargá el CSV o Excel. Estos archivos tienen encabezados y son solo de revisión.
4. Tocá **Generar ZIP** para recibir el archivo oficial, por ejemplo `1234567_REG_092026_V0001.zip`.
5. Subí únicamente ese ZIP en Marangatu, en la importación del Registro de Comprobantes.
6. Revisá los registros y su imputación y **confirmá el período en Marangatu** para obtener el **Talón de Presentación**.

Importar el ZIP no confirma la presentación. Conservá los comprobantes físicos por el plazo
de prescripción del impuesto. Que el bot no tenga facturas no prueba que no hubo operaciones:
si realmente no hubo movimiento y te corresponde presentar, confirmalo como «sin movimiento» en Marangatu.

La numeración `V0001`, `V0002`… avanza únicamente al confirmar la generación del ZIP; descargar
CSV o Excel no la consume.

Sin `/registro` elegido, o con un período incompatible, CSV y Excel siguen disponibles pero
el bot no genera el ZIP. Para 956, `/exportar` sugiere el año anterior durante enero y febrero,
y el actual durante el resto del año (hora de Paraguay); podés indicar otro año explícitamente.
Para 955, sin argumento muestra el mes actual. Cambiar `/imputar` no cambia tu registro;
cambiar el RUC lo deja sin configurar para que lo confirmes de nuevo.

Las bases existentes conservan sus facturas y exportaciones, pero comienzan sin registro elegido.
Usá `/registro` antes del primer ZIP y para habilitar los avisos de exportación correspondientes.

Las facturas electrónicas (con CDC) no van en el archivo: obtenelas en Marangatu y revisá
su imputación; si no se imputaron automáticamente, debés hacerlo allí.
El formato está documentado en [docs/rg90-compras.md](docs/rg90-compras.md).

Las facturas se guardan en `data/facturas.db` (SQLite, en `.gitignore`), separadas por chat.
También se guarda lo que leyó la IA antes de tus correcciones, para medir qué tan bien lee cada modelo.

## Dejarlo corriendo en tu Mac

Para alojarlo en Oracle como servicio Linux `bot-marangatu`, ver
[docs/deploy-oracle.md](docs/deploy-oracle.md). Ejecutar una sola instancia con el mismo token.

En vez de `go run`, el bot puede correr como servicio de macOS (launchd):
arranca solo al iniciar sesión, se reinicia si se cae y no necesita una terminal abierta.

```bash
scripts/servicio-mac.sh instalar     # compila e instala el servicio
scripts/servicio-mac.sh estado       # ¿está corriendo?
scripts/servicio-mac.sh logs         # log en vivo (Ctrl+C para salir)
scripts/servicio-mac.sh actualizar   # después de un git pull: recompila y reinicia
scripts/servicio-mac.sh desinstalar  # lo detiene y lo quita (la base queda intacta)
```

- No corras `go run ./cmd/bot` al mismo tiempo: dos bots con el mismo token chocan.
- Si la Mac se suspende, el bot deja de responder hasta que se despierte.
- El log queda en `~/Library/Logs/bot-marangatu-facturas.log`.

## Métricas de la beta

El bot registra cómo se usa, para medir la beta con usuarios reales: usuarios activos y que vuelven,
embudo de cada foto (leída, guardada, descartada, abandonada), campos que la IA lee mal según las
correcciones, costo y tiempos, y qué funciones se usan.

Se guarda en la misma base SQLite, **sin datos de las facturas** ni el texto de los mensajes, y solo
lo consulta quien administra el servidor. En Telegram no se ve nada:

```bash
go run ./cmd/metricas -dias 7 -excluir 123456789 -usuarios
```

En el servidor: ver [docs/deploy-oracle.md](docs/deploy-oracle.md#métricas-de-uso).

## Comparar modelos

Poné fotos de facturas en `facturas/` (está en `.gitignore`) y corré:

```bash
go run ./cmd/comparar -modelos google/gemini-3.1-flash-lite,deepseek/deepseek-v4.1-flash -razonamiento defecto,none
```

Muestra una tabla con cuántas facturas leyó bien cada configuración, cuánto costó y cuánto tardó.
No imprime datos de las facturas. Ojo: cada foto × configuración es una llamada paga a OpenRouter.

## Tests

```bash
go test -race -cover ./...
```

## Privacidad

- Nunca subas tu `.env`, tokens ni fotos de facturas reales al repositorio (ya están en `.gitignore`).
- Para capturas y demos, usá facturas de prueba o tapá RUC, nombres y montos.
- Las fotos no se guardan: se descargan en memoria, se envían al modelo y se descartan.
- Cada pedido a OpenRouter va con `data_collection: "deny"` y, por defecto, `zdr: true`:
  solo se usan proveedores que no guardan ni entrenan con tus facturas.

## Licencia

[MIT](LICENSE)
