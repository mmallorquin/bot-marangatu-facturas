# Plan: RUC y CDC mal leídos, y volver a ver o releer la foto

Estado: **propuesta, sin implementar**. Fecha: 14 de octubre de 2026, sobre `main` en `0339f7f`.

## Diagnóstico

Los usuarios de la beta reportan dos bloqueos:

1. **RUC y CDC mal leídos.** El dígito verificador (DV) del RUC del emisor y algunos CDC de 44 dígitos salen mal de la lectura. Con cualquiera de los dos inválidos, Guardar queda bloqueado (`FixBeforeSavingAlert` en `internal/telegram/buttons.go`, `saveInvoice`).
2. **La foto desaparece del flujo.** El borrador guarda solo el JSON leído (`invoices` en `internal/store/store.go`: `invoice_json`, `original_json`). No se conserva la imagen, ni el `file_id` de Telegram, ni el `message_id` de la foto. Cuando aparece el aviso, el usuario tiene que buscar la factura física o mandar la foto otra vez, y la relectura cuesta una llamada completa nueva.

Lo que encontré al leer el código, y que agrava el problema:

- **El CDC no se puede corregir.** `EditableFields` (`internal/invoice/edit.go`) no incluye `cdc`; `fieldsKeyboard` (`internal/telegram/callbacks.go`) arma los botones a partir de esa lista. Un CDC con 43 o 45 dígitos deja la factura bloqueada sin ningún botón que la destrabe. Es el caso más grave y el más barato de arreglar.
- **El bot ya sabe cuál es el DV correcto, pero no lo ofrece.** `ValidateRUC` (`internal/invoice/ruc.go`) devuelve "el dígito verificador del RUC «80012345-9» debería ser 6", y el usuario tiene que entrar a Corregir → RUC y tipear todo.
- **El CDC solo se valida por largo** (`cdcPattern` en `internal/invoice/validate.go`), aunque trae adentro el RUC del emisor, su DV, el número y la fecha, y tiene un DV propio. Hoy no se usa nada de eso para cruzar datos.
- **El mensaje con la lectura no cita la foto.** `send` (`internal/telegram/handler.go`) manda el resultado como mensaje suelto, sin `ReplyParameters`. Telegram permite responder citando el mensaje de la foto, y el usuario podría tocar la cita para saltar a la imagen. No hace falta guardar nada para eso.
- **No se mide qué campo bloquea.** `metricas` cuenta campos corregidos en facturas guardadas (`FieldAccuracy`) y correcciones hechas, pero no qué campos dejan una lectura con avisos ni cuántos borradores quedan abandonados por eso.

## Qué se confirmó y qué no

| Afirmación | Estado | Dónde |
|---|---|---|
| Estructura del CDC (44 dígitos): tipo de documento (2), RUC emisor sin DV con ceros a la izquierda (8), DV del RUC (1), establecimiento (3), punto de expedición (3), número (7), tipo de contribuyente (1), fecha AAAAMMDD (8), tipo de emisión (1), código de seguridad (9), DV del CDC (1) | Coincide en varias fuentes de terceros; **no pude abrir el Manual Técnico oficial de la DNIT** (el sitio está bloqueado desde este entorno) | [sifende.com.py](https://www.sifende.com.py/docs/conceptos/cdc), [tiendli.com](https://tiendli.com/herramientas/verificador-factura-electronica), [NT 14 del MT v150 (DNIT)](https://www.dnit.gov.py/documents/20123/420595/NT_E_KUATIA_014_MT_V150.pdf/f09eda43-a3dc-2fde-de39-41d69bbfb2b1?t=1687266734471) |
| DV del CDC: módulo 11 sobre los 43 dígitos previos, pesos 2 a 11 desde la derecha, DV = 11 − resto (0 si el resto es 0 o 1) | Mismo algoritmo que el RUC (`CheckDigit`); lo describe el MT v150 §10.2 según terceros. **Verificar con el manual oficial antes de bloquear por esto**; mientras tanto, usarlo solo para avisar | [PR de facturacion-sifen](https://github.com/ferpiecel/facturacion-sifen/pull/58), [MT v141 en Scribd](https://es.scribd.com/document/531686411/Manual-Tecnico-Version-141) |
| Los `file_id` de Telegram se pueden reutilizar para reenviar o volver a descargar el archivo, solo con el mismo bot | Confirmado en la API oficial. **No hay garantía oficial de vigencia**: terceros dicen que suelen ser permanentes; la URL de `getFile` sí caduca (~1 hora) y hay que pedirla de nuevo cada vez | [core.telegram.org/bots/api](https://core.telegram.org/bots/api), [grammy.dev/guide/files](https://grammy.dev/guide/files) |
| Costo de una relectura | Igual a una lectura: ~USD 0,0009 por foto con el modelo por defecto (`docs/comparacion-modelos.md`). Con razonamiento más alto puede subir; no medido | `docs/comparacion-modelos.md` |

## Opciones evaluadas

| Opción | Qué resuelve | Esfuerzo | Riesgo | Veredicto |
|---|---|---|---|---|
| **(0) Hacer el CDC corregible** y agregar "No tiene CDC" | El bloqueo sin salida | Bajo (una línea en `EditableFields`, un parser y un botón) | Que el usuario borre un CDC real y la factura entre al ZIP cuando Marangatu ya la tiene; se mitiga con un aviso claro | **Hacer primero** |
| **(a) Botón "Usar 80012345-6"** con el DV calculado | El caso más común del RUC | Bajo | Tapar una base mal leída (8↔3, 1↔7, 0↔6) con un DV que cierra. Por eso **no corregir solo**: el botón muestra el RUC completo y el usuario lo compara con la foto | **Hacer** |
| (a') Sugerir candidatos por una sola sustitución de dígito en la base cuyo DV coincida con el leído | El caso en que la base está mal y el DV bien | Medio (función pura, bien testeable) | Varios candidatos posibles; mostrar como máximo 2 y solo si el DV leído no cierra con la base leída | Hacer si (a) no alcanza; dejar listo el diseño |
| **(b) Validar el CDC con su DV y cruzarlo con RUC, número y fecha** | Detecta CDC mal leídos y, a la vez, **confirma el RUC** cuando el CDC está bien: si el CDC contiene `80012345` + `6`, el RUC leído como `80012345-9` se corrige con confianza | Medio | El algoritmo del DV del CDC no está confirmado con el manual oficial; además, usar el CDC con cuidado: si el CDC está mal, no "arregla" el RUC | **Hacer**, con el DV como aviso hasta confirmarlo |
| **(c) Guardar `file_id`, mime y `message_id` en el borrador** (no la imagen) | Habilita "Ver foto" y "Releer" | Medio (migración con el patrón `addedColumns` que ya existe) | Privacidad: el `file_id` no es la imagen, pero permite volver a bajarla mientras el chat exista. Documentarlo en el README. Si el `file_id` caduca, los botones avisan y piden la foto de nuevo | **Hacer** |
| (c') Citar el mensaje de la foto al responder (`ReplyParameters`) | El usuario toca la cita y salta a la foto | Muy bajo, sin guardar nada | Ninguno | **Hacer primero**, sirve aunque (c) se postergue |
| **(d) "Releer": segunda pasada enfocada** en RUC y CDC, con el mismo modelo y razonamiento medio; mostrar las dos lecturas cuando difieren | Lecturas borrosas donde una segunda pasada enfocada suele acertar | Medio-alto (prompt nuevo, esquema reducido, tope de relecturas) | Costo: ~USD 0,001 por relectura; limitar a 2 por borrador. Puede volver a leer mal; por eso muestra ambas y deja elegir | Hacer después de (c); medir si vale |
| (e) Ajustar el prompt de lectura | Menos errores de origen | Bajo | Sin medición antes y después no se sabe si ayuda | Hacer junto con (f) |
| (f) Medir qué campos bloquean | Saber si RUC y CDC son el problema principal y cuánto mejora cada cambio | Bajo | Ninguno | **Hacer primero**, para tener la línea base |

## Enfoque elegido (en orden)

### Paso 1 — Línea base y destrabe inmediato (bajo esfuerzo)
1. **Medir:** en `showReadInvoice` (`internal/telegram/handler.go`), registrar junto con `EventRead` qué campos tienen problemas (`ruc_emisor`, `cdc`, `total`…), y en `saveInvoice`, cuáles bloquearon. `cmd/metricas/report.go` suma una línea "Campos que bloquean" con conteos.
2. **Citar la foto:** `processImage` recibe el `message_id` del mensaje con la foto y `showReadInvoice` responde con `ReplyParameters{MessageID}`. Cambio en `send` para aceptar un `replyTo` opcional.
3. **CDC corregible:** agregar `FieldCDC` a `EditableFields` y un caso en `Edit` que acepte 44 dígitos (quitando espacios). En `fieldsKeyboard`, el botón "CDC". En `askValueMessage` (`internal/telegram/replies.go`), la pista "44 dígitos, sin espacios".
4. **"No tiene CDC":** cuando el problema es el CDC, agregar al mensaje un botón que lo vacíe, con el aviso: "Si es una factura electrónica, Marangatu ya la tiene; si la cargás sin CDC puede quedar duplicada". Nuevo `action` en `callbacks.go`.
5. **Botón "Usar 80012345-6":** cuando el único problema del RUC es el DV y la base tiene formato válido, mostrar el botón con el RUC completo. Al tocarlo, aplica `Edit(FieldIssuerRUC)` y revalida. Nunca se aplica solo. Función `SuggestRUC(ruc string) (string, bool)` en `internal/invoice/ruc.go`.

### Paso 2 — Cruce con el CDC
6. **Parser del CDC:** `ParseCDC(cdc string) (CDC, error)` en un archivo nuevo `internal/invoice/cdc.go`, con los 11 campos y la verificación del DV. Hasta confirmar el algoritmo con el manual oficial, un DV que no cierra genera un aviso, no un bloqueo.
7. **Cruce en `validateIdentity`:** si hay CDC válido, comparar RUC base + DV, número (establecimiento-punto-correlativo) y fecha. Si el RUC leído difiere solo en el DV y el CDC lo confirma, ofrecer "Usar X-N (según el CDC)". Si difieren en la base, avisar cuál dice cada uno, sin elegir por el usuario.
8. **Prompt (`internal/openrouter/prompt.md`):** pedir que el RUC y el CDC se transcriban tal como están impresos, dígito por dígito, y que se marquen como dudosos si no se distinguen; aclarar que el CDC suele estar debajo del código QR en el KuDE.

### Paso 3 — Ver foto y releer
9. **Guardar la referencia a la foto:** columnas nuevas en `invoices` (`source_file_id`, `source_mime`, `source_message_id`), agregadas con el mecanismo `addedColumns` de `store.go`. `Draft` recibe esos tres datos desde `processImage`. Nunca se guarda la imagen.
10. **Botón "📷 Ver foto":** reenvía la foto por `file_id` con `SendPhoto`/`SendDocument`, citando el mensaje de la factura. Si Telegram rechaza el `file_id`, responde "Ya no tengo la foto; mandala de nuevo".
11. **Botón "🔁 Releer":** vuelve a bajar el archivo con `getFile` (URL fresca cada vez), llama a `Reader.Read` con un prompt enfocado en identidad (RUC, timbrado, número, CDC) y razonamiento medio, y muestra ambas lecturas con botones "Usar esta" por campo cuando difieren. Tope de 2 relecturas por borrador (columna `rereads`), y el costo se registra como evento `relectura`. Reutiliza `processImage` con el `file_id` guardado para no duplicar la descarga.
12. **README, sección Privacidad:** explicar que se guarda el identificador del archivo en Telegram, no la foto, y que se borra con la factura y con `/borrar_mis_datos`.

## Cambios por archivo

| Archivo | Cambio |
|---|---|
| `internal/invoice/ruc.go` | `SuggestRUC` (DV calculado si la base es válida). Más adelante, candidatos por una sustitución |
| `internal/invoice/cdc.go` (nuevo) | `ParseCDC`, `CDC` con sus campos, `CheckDigit` reutilizado |
| `internal/invoice/validate.go` | `validateIdentity`: DV del CDC como aviso; cruce CDC ↔ RUC, número y fecha |
| `internal/invoice/edit.go` | `FieldCDC` en `EditableFields`; caso `FieldCDC` en `Edit` |
| `internal/openrouter/prompt.md` | Instrucciones de transcripción para RUC y CDC |
| `internal/openrouter/client.go` | Opción para un prompt enfocado y razonamiento por llamada (relectura) |
| `internal/store/store.go` | Columnas `source_file_id`, `source_mime`, `source_message_id`, `rereads`; `Draft` y `Record` las cargan; `DeleteChat` ya las borra con la fila |
| `internal/telegram/handler.go` | `processImage` recibe `message_id`; `send` con `replyTo`; eventos con campos problemáticos; flujo de relectura |
| `internal/telegram/callbacks.go` | Acciones nuevas: usar RUC sugerido, quitar CDC, ver foto, releer, usar lectura alternativa |
| `internal/telegram/buttons.go` | Handlers de esas acciones |
| `internal/telegram/replies.go` | Textos: pista del CDC, aviso de "No tiene CDC", "ya no tengo la foto", tope de relecturas |
| `internal/telegram/format.go` | Mostrar el RUC sugerido junto al aviso |
| `cmd/metricas/report.go` + `internal/store/events.go` | "Campos que bloquean" y evento `relectura` |
| `README.md` | Privacidad y comandos |

## Tests a agregar

- `internal/invoice`: `SuggestRUC` (base válida, base con letra, DV ya correcto → sin sugerencia); `ParseCDC` con un CDC real anonimizado y con DV incorrecto; cruce CDC ↔ RUC en los tres casos (coincide, difiere solo el DV, difiere la base); `Edit(FieldCDC)` con espacios y con 43 dígitos.
- `internal/telegram`: botón "Usar X-N" corrige y revalida; "No tiene CDC" vacía el campo y muestra el aviso; la lectura cita el mensaje de la foto; "Ver foto" reenvía por `file_id` y avisa si Telegram lo rechaza; "Releer" respeta el tope de 2 y muestra la diferencia; eventos registrados.
- `internal/store`: migración de una base anterior sin las columnas nuevas (mismo patrón que `TestOpenMigratesADatabaseCreatedBeforeTheNewColumns`).
- `internal/telegram/journey_test.go`: agregar al recorrido una factura con el DV del RUC mal leído que se arregla con el botón, y una con CDC inválido que se corrige a mano.
- `docs/beta-validacion.md`: anotar en la planilla cuántas facturas necesitaron el botón de RUC, corrección de CDC o relectura.

## Preguntas abiertas

1. **¿Qué tan seguido falla cada campo en producción?** Con la salida de `metricas -dias 30` y 3 o 4 ejemplos de RUC y CDC mal leídos (sin datos de terceros) se puede confirmar si el DV es el problema principal o si la base también se lee mal, lo que cambia la prioridad de (a') y (d).
2. **¿Las facturas de la beta son mayormente electrónicas (KuDE)?** Si sí, el cruce con el CDC (paso 2) resuelve casi todos los RUC y conviene adelantarlo.
3. **¿Está bien guardar el `file_id`?** No es la imagen, pero permite volver a bajarla mientras el chat exista. Si no, el paso 3 se limita a citar la foto (c') y a pedirla de nuevo.
4. **¿Qué modelo y razonamiento usar en la relectura?** Conviene probar con `cmd/comparar` sobre fotos con RUC y CDC difíciles antes de fijarlo.
