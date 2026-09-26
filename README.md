# bot-marangatu-facturas

Olvidate de cargar facturas a mano: mandá la foto y el bot la registra para Marangatu.

> 🚧 Proyecto en construcción, hecho **en público**. Seguí el avance en X con **#buildinpublic**.

## El problema

En Paraguay, quienes liquidan IVA o IRP tienen que registrar sus comprobantes de compra en
[Marangatu](https://marangatu.set.gov.py) (RG 90). Hacerlo factura por factura, copiando RUC,
timbrado, número y montos, lleva horas y genera errores.

## La idea

```
📸 Foto de la factura ──► 🤖 Bot de Telegram ──► 🧠 IA lee los datos ──► 📊 Planilla lista para importar en Marangatu
```

1. Le sacás una foto a la factura y se la mandás al bot.
2. El bot extrae los datos: RUC y razón social del emisor, timbrado, número, fecha, condición,
   gravadas 10 % y 5 %, exentas, IVA y total.
3. Te los muestra para que confirmes o corrijas.
4. Con `/exportar` te genera el consolidado del mes en el formato de importación de Marangatu.

El bot **no se conecta a tu cuenta de Marangatu**: vos subís el archivo. Así no tiene que
manejar tus credenciales.

## Hoja de ruta

- [x] **Etapa 1 — Arranque:** estructura del proyecto y bot de Telegram que recibe fotos
- [x] **Etapa 2 — Lectura:** extraer los datos de la factura desde la foto con IA y validarlos
- [x] **Etapa 3 — Confirmación:** guardar, corregir o descartar cada factura desde el chat
- [ ] **Etapa 4 — Exportación:** generar el archivo de importación de la RG 90 para Marangatu
- [ ] **Etapa 5 — Beta:** probarlo con usuarios reales (ya corre como servicio en la Mac; falta un servidor)
- [ ] **Etapa 6 — WhatsApp:** sumar WhatsApp como segundo canal
- [ ] **Futuro:** carga automática en Marangatu

## Stack

- **[Go](https://go.dev)**: el proyecto también es mi excusa para aprender Go viniendo de Python.
- **[go-telegram/bot](https://github.com/go-telegram/bot)** para el bot de Telegram.
- **[OpenRouter](https://openrouter.ai)** para leer la factura con cualquier modelo con visión.
  Por defecto `google/gemini-3.1-flash-lite` (~3 s y ~USD 0,0009 por factura); se cambia con `OPENROUTER_MODEL`.
  El modelo se eligió comparando con fotos reales: ver [docs/comparacion-modelos.md](docs/comparacion-modelos.md).

```
cmd/bot/              → punto de entrada: arma el bot y lo pone a escuchar
cmd/comparar/         → compara modelos leyendo las fotos de facturas/ (costo, tiempo, resultados)
internal/config/      → lee y valida la configuración (.env)
internal/telegram/    → recibe la foto, la descarga y responde
internal/reader/      → contrato para leer facturas (independiente del proveedor de IA)
internal/openrouter/  → implementación con OpenRouter: prompt, esquema JSON y cliente HTTP
internal/invoice/     → la factura, sus validaciones (RUC módulo 11, IVA, totales) y las correcciones
internal/store/       → base SQLite local: borradores, facturas guardadas, duplicados y resumen mensual
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
5. Abrí tu bot en Telegram, mandá `/start` y después una foto de una factura.

## Cómo se usa

1. Mandás la foto de la factura.
2. El bot la lee y te la muestra con tres botones:

   ```
   [✅ Guardar]  [✏️ Corregir]  [🗑️ Descartar]
   ```

3. **✏️ Corregir**: elegís el campo, escribís el valor correcto (`150.000`, `20/09/2026`, `1-1-1234`…)
   y el bot vuelve a validar.
4. **✅ Guardar**: solo se puede si todos los datos cierran. Si ya habías guardado la misma factura, te avisa.

| Comando | Qué hace |
|---|---|
| `/resumen` | Facturas guardadas este mes, con IVA y total |
| `/resumen 08/2026` | Lo mismo para otro mes |
| `/cancelar` | Cancela una corrección a medias |

Las facturas se guardan en `data/facturas.db` (SQLite, en `.gitignore`), separadas por chat.
También se guarda lo que leyó la IA antes de tus correcciones, para medir qué tan bien lee cada modelo.

## Dejarlo corriendo en tu Mac

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
