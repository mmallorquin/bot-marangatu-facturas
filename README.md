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
- [ ] **Etapa 2 — Lectura:** extraer los datos de la factura desde la foto con IA
- [ ] **Etapa 3 — Confirmación:** mostrar los datos y permitir corregirlos desde el chat
- [ ] **Etapa 4 — Exportación:** generar el archivo de importación de la RG 90 para Marangatu
- [ ] **Etapa 5 — Beta:** probarlo con usuarios reales
- [ ] **Etapa 6 — WhatsApp:** sumar WhatsApp como segundo canal
- [ ] **Futuro:** carga automática en Marangatu

## Stack

- **[Go](https://go.dev)**: el proyecto también es mi excusa para aprender Go viniendo de Python.
- **[go-telegram/bot](https://github.com/go-telegram/bot)** para el bot de Telegram.

```
cmd/bot/            → punto de entrada: arma el bot y lo pone a escuchar
internal/config/    → lee y valida la configuración (.env)
internal/telegram/  → qué responde el bot y cómo lo envía
```

## Cómo correrlo

Necesitás [Go 1.27+](https://go.dev/dl/) y un bot de Telegram.

1. Creá tu bot hablándole a [@BotFather](https://t.me/BotFather) → `/newbot`, y copiá el token.
2. Configurá el token:
   ```bash
   cp .env.example .env
   # editá .env y pegá el token en TELEGRAM_BOT_TOKEN
   ```
3. Arrancá el bot:
   ```bash
   go run ./cmd/bot
   ```
4. Abrí tu bot en Telegram, mandá `/start` y después una foto.

## Tests

```bash
go test -race -cover ./...
```

## Privacidad

- Nunca subas tu `.env`, tokens ni fotos de facturas reales al repositorio (ya están en `.gitignore`).
- Para capturas y demos, usá facturas de prueba o tapá RUC, nombres y montos.

## Licencia

[MIT](LICENSE)
