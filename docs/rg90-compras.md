# Importación de compras en Marangatu (RG 90)

Resumen de la [Especificación Técnica para Importación del Registro de Comprobantes](https://www.dnit.gov.py/documents/44828/0/Especificaciones+T%C3%A9cnicas+para+registro+de+comprobantes+en+Marangatu.pdf/fd50732c-4232-750a-b253-5b14843cdccb?t=1682430005384.pdf)
de la DNIT (versión 1, junio 2021), aplicado en `internal/marangatu`.

## Archivo

- `.txt` delimitado por **tabulaciones** (también se admite `.csv` delimitado por comas; usamos TXT para que las comas en razones sociales no rompan el archivo).
- UTF-8, **sin encabezado**, máximo **5.000 filas**.
- Se sube **comprimido en ZIP** con el mismo nombre que el archivo de adentro:
  `<RUC sin DV>_REG_<MMAAAA>_<ID de hasta 5 caracteres>.zip` → ej. `80024627_REG_092026_V0001.zip`.
- **No** se incluyen comprobantes electrónicos (e-Kuatia, con CDC) ni virtuales: Marangatu los obtiene solo.
- Los RUC van **sin dígito verificador**.

## Registro de compras (tipo 2): 20 campos en este orden

| # | Campo | Valor que genera el bot |
|---|---|---|
| 1 | Código tipo de registro | `2` (compras) |
| 2 | Tipo de identificación del proveedor | `11` (RUC) |
| 3 | Número de identificación | RUC del emisor sin DV |
| 4 | Nombre o razón social | Razón social (opcional para RUC) |
| 5 | Tipo de comprobante | `109` factura, `112` ticket máquina registradora |
| 6 | Fecha de emisión | `dd/mm/aaaa` |
| 7 | Timbrado | 8 dígitos |
| 8 | Número de comprobante | `###-###-#######` |
| 9 | Gravado 10 % (IVA incluido) | entero, sin decimales (`0` en tickets) |
| 10 | Gravado 5 % (IVA incluido) | entero (`0` en tickets) |
| 11 | No gravado o exento | entero (`0` en tickets) |
| 12 | Total | suma de 9 + 10 + 11 (en tickets, solo este campo) |
| 13 | Condición de compra | `1` contado, `2` crédito |
| 14 | Operación en moneda extranjera | `N` |
| 15 | Imputa al IVA | `S`/`N` según `/imputar` |
| 16 | Imputa al IRE | `S`/`N` según `/imputar` |
| 17 | Imputa al IRP-RSP | `S`/`N` según `/imputar` |
| 18 | No imputa | `N` |
| 19 | Número de comprobante asociado | vacío (solo notas de crédito/débito) |
| 20 | Timbrado del comprobante asociado | vacío (solo notas de crédito/débito) |

## Todavía no soportado

- Notas de crédito y débito (requieren el comprobante asociado, campos 19 y 20).
- Autofacturas y despachos de importación (otro tipo de identificación).
- Operaciones en moneda extranjera.
- Fechas anteriores al 01/01/2021 al contado (Marangatu no las acepta).

El bot deja estas facturas afuera del archivo y avisa cuáles fueron.
