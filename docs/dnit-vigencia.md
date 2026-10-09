# Seguimiento normativo de la beta

Revisión: 1 de octubre de 2026. Esta lista no certifica cumplimiento tributario integral.
El bot prepara compras para importar; no verifica las obligaciones activas del RUC ni opera en Marangatu.

## Reglas consideradas

La [RG DNIT 12/2024](https://www.dnit.gov.py/en/web/portal-institucional/w/resolucion-general-dnit-n-12/2024)
modifica la RG 90:

- La presentación se confirma en Marangatu y genera el **Talón de Presentación**, no el antiguo Talón Resumen. Importar no equivale a confirmar; los datos registrados tienen carácter de declaración jurada aun sin confirmación.
- La imputación depende de las obligaciones activas del RUC. Las electrónicas pueden requerir imputación manual.
- Los comprobantes físicos se conservan durante el plazo de prescripción del impuesto.
- La obligación 955 es mensual y la 956 anual. Sus vencimientos siguen el calendario de declaraciones informativas.

El [aviso DNIT del 12/02/2026 sobre el ejercicio 2025](https://www.dnit.gov.py/en/web/portal-institucional/w/vencimiento-del-registro-anual-de-comprobantes-correspondiente-al-ejercicio-2025)
recuerda la confirmación anual incluso sin movimiento. No se puede inferir ausencia de operaciones de una base vacía en el bot.

## Estado del bot

- Implementado: instrucciones de confirmación, revisión de imputación electrónica, conservación y aviso condicional sin movimiento; pruebas de esas respuestas.
- Implementado: `/registro` elige 955/956 por chat, independiente de `/imputar`. Sin elección o con período incompatible no se genera ZIP; CSV/Excel siguen disponibles. Las sugerencias y el tipo de aviso siguen la elección, no los impuestos. El usuario debe verificarla en su RUC; el bot no certifica que corresponda legalmente.
- Migración: las bases existentes conservan los datos y quedan sin elección automática. Cambiar de RUC requiere volver a elegir; cambiar impuestos no modifica el registro.
- Implementado: seguimiento manual de presentación reportada por el usuario, vinculado a la versión entregada. El ZIP recibido ofrece marcar la presentación cuando se obtenga el Talón; el bot no verifica esa declaración en DNIT. Los recordatorios de exportación se detienen al registrar una entrega, no al generar o reservar el ZIP.
- Pendiente: calendario y excepciones. Los avisos actuales son de exportación, no vencimientos oficiales.
- Fuera de esta beta de compras IVA/IRP: reglas especiales de fideicomisos de garantía de la [RG 36/2025](https://www.dnit.gov.py/documents/20123/1374136/Res.%2BGeneral%2BDNIT%2BN%C2%B0%2B36_2025.pdf/40e71397-7b27-6aa5-e161-7349f0e02218?t=1757706846937).

Las prórrogas puntuales, como la [RG 55/2026](https://www.dnit.gov.py/documents/20123/2664257/RESOLUCION%2BGENERAL%2BDNIT%2BN%C2%B0%2B55-2026.pdf/91695cb6-ddf5-48b8-8ec3-9ba40e5e1d4c?t=1784823667417.pdf),
no deben convertirse en un vencimiento permanente. Antes de implementar fechas se debe verificar el calendario aplicable.

El formato del ZIP sigue documentado en [rg90-compras.md](rg90-compras.md).
Una importación aceptada valida ese archivo, no acredita todas las obligaciones del contribuyente.

Estado funcional contrastado con el código el 9 de octubre de 2026. La fecha de revisión normativa permanece sin cambios.
