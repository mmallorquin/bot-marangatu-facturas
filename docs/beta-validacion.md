# Prueba de la beta: 50 facturas

Estado: **preparada, no ejecutada**. Las pruebas automáticas usan SQLite real y APIs
simuladas; no demuestran exactitud del OCR real ni aceptación actual de Marangatu.

## Muestra y consentimiento

Invitar primero a 2–3 personas que acepten enviar sus comprobantes a Telegram y al
servicio de lectura configurado en OpenRouter. Cada una usa su chat privado; nunca
un grupo. No pedir contraseñas de Marangatu ni publicar fotos, RUC o chat_id en GitHub.

Propuesta de muestra: 30 facturas legibles, 15 con condiciones difíciles pero
recuperables (luz, ángulo, recibos largos) y 5 PDF de **una factura por archivo**.
Variar emisores, meses, contado/crédito y montos exentos/gravados. Incluir electrónicas
para comprobar que permanecen guardadas pero no entran en el ZIP.

Antes de leer, un revisor anota privadamente los valores reales de todos los campos
del comprobante: tipo, RUC y nombre del emisor, timbrado, número, fecha, condición,
moneda, exentas, gravadas, IVA, total y CDC cuando corresponda. La coherencia
aritmética no sustituye esa comparación. Una factura ilegible se identifica como
tal; no se inventa una respuesta correcta.

## Recorrido por caso

1. Registrar un alias y un ID B01–B50 en la [planilla vacía](templates/beta-50-facturas.csv).
2. Medir desde el envío hasta la primera revisión. Comparar la lectura original con
   el comprobante antes de corregir nada, aunque el bot diga que los datos cierran.
3. Corregir si hace falta, guardar y abrirla desde **Mis facturas**. Anotar los campos
   corregidos y los reintentos, no solamente si terminó guardada.
4. Elegir período en **Exportar**, completar los ajustes y verificar que retome el
   mismo período. Comparar CSV/Excel/ZIP con las facturas guardadas y sus exclusiones.
5. El usuario verifica personalmente la importación, imputación y presentación en
   Marangatu cuando corresponda. **Ya presenté** solo registra su afirmación; el bot
   no consulta DNIT. Evitar subir dos veces el mismo registro durante las pruebas.

Registrar también el tiempo hasta guardado y hasta ZIP recibido. La presentación
real puede ocurrir otro día: no mezclar ese plazo con la duración del bot.

## Controles adicionales

- Enviar un duplicado y comprobar que no se guarde dos veces en el mismo chat.
- Guardar/corregir desde una página antigua con más de 20 facturas del mismo mes,
  sin otra lectura OCR y sin perder el registro original.
- Una imagen que no es factura no debe quedar guardada; un dato inconsistente exige
  corrección. Probar rechazo de archivo pesado o formato no admitido.
- Cancelar configuración, borrar datos con confirmación y comprobar que botones
  anteriores no recreen facturas ni permitan confirmar exportaciones borradas.
- Comprobar que cada usuario ve únicamente sus facturas y ajustes.
- Probar 955 mensual y 956 anual **solo con usuarios cuya obligación corresponda**;
  no modificar obligaciones fiscales reales para fabricar un escenario.
- Los fallos de saldo, proveedor y entrega se ensayan primero en tests locales, no
  agotando crédito ni interrumpiendo producción para provocar errores.

## Métricas y cierre

Usar el reporte existente, de solo lectura:

```sh
sudo -u bot-marangatu /opt/bot-marangatu/metricas -db /var/lib/bot-marangatu/facturas.db -dias 7
```

Medir:

- Exactitud inicial: facturas con todos los campos verificados correctamente / 50.
- Exactitud por campo: valores correctos / valores verificables de ese campo.
- Intervención: facturas y campos que necesitaron corrección; fallos y reintentos.
- Tiempo: mediana y peor caso observado hasta revisión, guardado y ZIP.
- Costo: diferencia de consumo real en OpenRouter durante la prueba, dividida por
  las 50 facturas; registrar aparte la cantidad total de intentos. El reporte local
  suma el costo comunicado en lecturas exitosas; puede omitir cargos de respuestas
  fallidas. Las diferencias original/corregido tampoco son una verdad independiente.

No fijar una promesa de precisión antes de medir. Para cerrar la beta debe existir
un resultado trazable por caso, ningún incidente abierto de aislamiento/pérdida de
datos/duplicación, recuperación de errores comprobada y una verificación manual de
los ZIP de los tipos de registro realmente probados. Si un tipo no se probó, dejarlo
explícito. A partir de los resultados decidir si ampliar usuarios o corregir primero.

La planilla debe permanecer privada y sin datos fiscales: usar aliases e IDs. Al
abrirla en una hoja de cálculo, tratar las columnas de texto como texto y evitar
valores que empiecen con `=`, `+`, `-` o `@`. Guardar los valores reales y las fotos
en el lugar privado acordado con los participantes, no en esta plantilla pública.

El respaldo fuera de Oracle queda **postergado por decisión del administrador**.
El respaldo previo a despliegue en el mismo servidor no cubre la pérdida de la VM;
por eso esta sigue siendo una beta pequeña, no una garantía de recuperación completa.
