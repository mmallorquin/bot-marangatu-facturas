# Diagnóstico: bot-marangatu-facturas — 7 de octubre de 2026

Hecho con la skill `saas-playbook` (`.claude/skills/saas-playbook/`) sobre el estado de `main`.

## Resumen
El producto está muy adelantado en **Construir** y **Activar**: el MVP funciona de punta a punta, la importación del ZIP en Marangatu está validada, el onboarding es guiado y el uso ya se mide. Está atrasado en **Descubrir**: no hay entrevistas registradas, ni ICP escrito, ni ninguna señal de disposición a pagar. El cuello de botella es el **paso 3, validar el dolor y la disposición a pagar con personas del ICP**, y la acción más importante es arrancar la beta con 5–10 usuarios reales mientras se los entrevista con un guion que incluya precio.

## Evidencia usada
- **README:**
  - el problema (registrar compras en Marangatu, RG 90, a mano);
  - el público (quienes liquidan IVA o IRP en Paraguay);
  - la hoja de ruta: "falta probar con otros usuarios".
- **`docs/beta-validacion.md`:** la prueba de 50 facturas está **preparada, no ejecutada**.
- **`docs/comparacion-modelos.md`:** costo de lectura de ~USD 0,0009 por factura y ~3 s por lectura.
- **Instrumentación de uso:** tabla `events` y comando `metricas`, con:
  - activos, nuevos y recurrentes;
  - embudo foto → guardada → ZIP;
  - campos corregidos;
  - costo y tiempos.
- **Producto:**
  - onboarding guiado (RUC y botones de impuestos);
  - `/registro` 955/956;
  - menú simplificado (facturas, exportar, ajustes, ayuda);
  - recordatorios;
  - borrado de datos.
- **Lo que no se pudo verificar:**
  - cuántas personas usan hoy el bot fuera del administrador;
  - si existen conversaciones o entrevistas no documentadas;
  - el canal por el que llegarían usuarios (hoy, el #buildinpublic en X).

## Los 17 pasos

Estados: ✅ hecho con evidencia · 🟡 en curso o hipótesis · ❌ sin hacer · ⏸️ todavía no corresponde

| # | Paso | Estado | Evidencia | Qué falta |
|---|---|---|---|---|
| 1 | Dolor de nicho | 🟡 | El problema está bien descrito en el README: transcribir facturas a Marangatu lleva horas y genera errores | Escucharlo en palabras de contribuyentes y contadores, no solo del fundador |
| 2 | ICP | 🟡 | "Quienes liquidan IVA o IRP" | Elegir un segmento: ¿persona con IRP, profesional independiente con IVA, PyME, contador con varios clientes? ¿Quién paga? |
| 3 | Validación con ~10 personas | ❌ | No hay entrevistas registradas | Entrevistas sobre cómo lo resuelven hoy, cuánto les cuesta y si pagan algo (contador, planilla) |
| 4 | Solución testeada / PSF | 🟡 | El administrador validó la importación real del ZIP | Que personas del ICP la usen un mes completo y se comprometan (vuelvan, pidan acceso, ofrezcan pagar) |
| 5 | MVP | ✅ | Flujo foto/PDF → lectura → corrección → ZIP aceptado por Marangatu; costo por lectura conocido | — |
| 6 | Landing | ❌ | No hay landing; el acceso es el link del bot | Una página simple: dolor, para quién, cómo funciona, privacidad y un botón para abrir el bot |
| 7 | Contenido | 🟡 | #buildinpublic en X | Contenido pensado para el ICP (guías de RG 90, 955/956, "cómo cargar compras en Marangatu"), no solo para colegas que programan |
| 8 | Conversión y CRO | ⏸️ | — | Necesita landing (paso 6) y tráfico |
| 9 | Onboarding / AHA / TTV | ✅ | Configuración guiada; avisos de duplicados y electrónicas; álbumes y pendientes | Escribir el AHA como evento: probablemente "primera factura guardada" o "primer ZIP descargado" |
| 10 | Medición del onboarding | 🟡 | `metricas` mide el embudo foto → guardada → ZIP, correcciones y tiempos | Datos de usuarios reales; medir el TTV hasta la primera factura guardada |
| 11 | A/B tests | ⏸️ | — | Sin tráfico no aplica; ver `metricas.md` (miles de visitas por variante) |
| 12 | Paywall | ❌ | Todo gratis, sin precio | Hipótesis de precio y la pregunta de disposición a pagar en las entrevistas |
| 13 | Soporte | 🟡 | `/ayuda`, mensajes de error claros, borrado de datos, registro de incidentes en logs | Canal de soporte explícito (¿Telegram del fundador?) y registro de por qué escriben |
| 14 | Retención / churn / PMF | 🟡 | Métrica de recurrentes y recordatorios mensuales ya existen | Usuarios reales durante 2–3 ciclos mensuales; el dolor es mensual (o anual con 956): medir retención mes a mes |
| 15 | Distribución | ❌ | — | Hipótesis fuerte: los contadores, que concentran clientes con el mismo dolor |
| 16 | CAC y unit economics | 🟡 | Costo variable de IA conocido (~USD 0,0009 por factura); servidor Oracle | No hay precio ni CAC; armar la cuenta con variables (abajo) |
| 17 | Escalar | ⏸️ | — | Depende de los pasos 12, 14 y 16 |

### La cuenta, con lo que se sabe
Con *F* facturas por cliente y por mes y una lectura de ~USD 0,0009, la IA cuesta ~0,0009 × F. Con F = 50, son unos 4–5 centavos de dólar por cliente y por mes. La IA no es el costo que importa. Los que importan son:
- el **soporte** (tiempo del fundador);
- las **comisiones de cobro** en Paraguay;
- el **CAC**.

Por eso el precio puede pensarse por el valor (horas ahorradas y errores evitados) y no por el costo de IA. Falta el dato clave: cuánto paga hoy el ICP por resolver esto (contador, horas propias).

## Cuello de botella
El **paso 3**. El producto avanzó mucho en confiabilidad y onboarding, pero todavía no hay evidencia de que personas del ICP:
- tengan este dolor lo bastante fuerte;
- prefieran esta solución a la actual (contador o planilla);
- paguen por ella.

Seguir puliendo funciones (pasos 5, 9 y 13), o pensar en WhatsApp o en carga automática, tiene un retorno incierto hasta tener esa respuesta. La beta de 50 facturas ya preparada es el vehículo ideal, siempre que se le sumen preguntas de negocio y no solo de exactitud del OCR.

## Próximas 3 acciones

### 1. Beta con entrevistas: 5–10 personas del ICP durante un ciclo mensual
- **Prueba:** reclutar de 5 a 10 contribuyentes (o 2–3 contadores) para la prueba de 50 facturas. Hacerle a cada uno una entrevista de 20 minutos antes de empezar:
  - ¿cómo cargás hoy tus compras?;
  - ¿cuánto tiempo te lleva?;
  - ¿pagás a alguien?;
  - ¿qué pasa si te equivocás?

  Y otra al terminar el mes:
  - ¿lo volverías a usar?;
  - ¿cuánto pagarías?;
  - pregunta de Sean Ellis.
- **Métrica:**
  - cuántos llegan al ZIP del mes;
  - cuántos vuelven al mes siguiente sin que se lo pidas;
  - lo que pagan hoy por la alternativa.
- **Criterio (heurística):** la mayoría describe el mismo dolor con costo concreto y al menos la mitad exporta el mes completo.
- **Decisión:**
  - **Si se cumple:** pasar al precio (acción 3).
  - **Si no:** revisar el ICP (¿contadores en lugar de contribuyentes?) antes de agregar funciones.

### 2. Landing mínima con un solo botón
- **Prueba:** una página con:
  - el título del dolor ("Cargá tus compras en Marangatu sin tipearlas");
  - para quién es;
  - cómo funciona en 3 pasos;
  - privacidad (el bot no pide la contraseña de Marangatu);
  - "gratis durante la beta";
  - un botón para abrir el bot con un parámetro de origen (`t.me/<bot>?start=landing`), para atribuir de dónde llega cada usuario. Hoy el bot no guarda ese parámetro: haría falta un cambio chico para registrarlo como detalle del evento `start`.
- **Métrica:** visitas, clics en el botón y `/start` atribuidos a la landing.
- **Criterio:** que 3–5 personas del ICP entiendan qué hace sin explicación ("¿qué entendiste?").
- **Decisión:** si se entiende, usarla como destino de todo el contenido del paso 7. Si no, ajustar el mensaje antes de traer tráfico.

### 3. Precio de prueba con los usuarios de la beta
- **Prueba:** al final del ciclo, presentar una oferta concreta: un plan mensual con un tope de facturas, otro anual para quienes registran con la 956, y la opción de pagar de la forma habitual en Paraguay. Preguntar quién se anota, idealmente con un compromiso real (preventa o seña simbólica).
- **Métrica:** cuántos aceptan y el motivo de quienes no.
- **Criterio:** al menos algunos aceptan pagar por adelantado.
- **Decisión:**
  - **Si nadie acepta:** separar si el problema es el valor percibido, el precio o la oferta (paso 12 en `pasos.md`).
  - **Si aceptan:** recién ahí definir el paywall y empezar a medir el CAC.

## Preguntas abiertas para el dueño
- ¿El cliente que paga es el contribuyente o el contador? Cambia el ICP, la landing y el canal.
- ¿Cuántas facturas por mes tiene un usuario típico del ICP? Define el precio y el valor del ahorro de tiempo.
- ¿Cuánto paga hoy esa persona por resolverlo (contador, horas propias)?
- ¿Ya hay personas que pidieron usar el bot? ¿De qué canal vinieron?
- ¿Cuánto tiempo semanal le podés dedicar al soporte durante la beta? Es el costo más grande de la cuenta.
