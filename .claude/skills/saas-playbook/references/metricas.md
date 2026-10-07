# Métricas y fórmulas

Usá estas definiciones para que los números sean comparables. Decí siempre qué supuestos usaste. Si falta un dato, dejá la fórmula con la variable y anotá qué hay que medir.

## Embudo de adquisición
- **Conversión de la landing** = clics en la llamada a la acción ÷ visitas únicas. Medila por canal: mezclar canales esconde el problema (paso 8b).
- **Tasa de registro** = registros ÷ visitas.
- **Atribución:** marcá cada link con UTM o un código por canal. Sin eso no hay CAC por canal.

## Activación y onboarding
- **AHA moment:** un evento concreto, por ejemplo "primera factura procesada y guardada" o "primer reporte exportado".
- **Activación** = usuarios que llegan al AHA ÷ usuarios que empezaron, dentro de una ventana fija (24 h, 7 días).
- **TTV (time to value):** tiempo desde el registro hasta el AHA. Reportá la mediana, que es menos sensible a los casos extremos que el promedio.
- **Completitud por paso:** el porcentaje que pasa de cada paso al siguiente. El paso con la mayor caída es el primero a revisar.

## Monetización
- **Conversión a pago** = clientes que pagan ÷ usuarios activados, en la misma ventana.
- **ARPU** (ingreso promedio por usuario) = ingreso del período ÷ clientes que pagan.

## Retención y churn
- **Retención por cohortes:** agrupá a los usuarios por mes (o semana) de alta y medí qué parte sigue activa en cada período siguiente. La señal buena es una curva que **se aplana**, no que llegue a cero.
- **Churn mensual** = clientes que cancelan en el mes ÷ clientes al inicio del mes.
- **Vida esperada** ≈ 1 ÷ churn mensual, en meses, como aproximación.
- **Frecuencia natural:** medí la retención en la unidad de tiempo en que ocurre el dolor. Si el dolor es mensual, la retención diaria no dice nada.

## Unit economics
- **Margen de contribución por cliente y mes** = ARPU − (IA + infraestructura + comisiones de pago + reembolsos prorrateados + soporte).
  - IA = costo por uso × usos por cliente.
  - Soporte = horas × valor de la hora, aunque la hora sea del fundador.
- **CAC** = gasto de adquisición del canal ÷ clientes nuevos que pagan de ese canal. Incluí pauta, herramientas y el tiempo dedicado si es relevante.
- **Payback** = CAC ÷ margen de contribución mensual, en meses. Cuanto más corto, menos caja hace falta para crecer.
- **LTV** ≈ margen de contribución mensual × vida esperada.
- **Heurística común:** LTV/CAC ≥ 3 y payback de 12 meses o menos en SaaS. Son referencias de la industria, no reglas. Un producto con poca caja necesita un payback más corto.

Ejemplo con variables: si ARPU = P, el costo variable por cliente = V, el soporte = S y el CAC = C, entonces el margen es M = P − V − S, el payback es C ÷ M y el LTV es M ÷ churn.

## Muestra mínima para un A/B (aproximación)
Para detectar una diferencia relativa *d* sobre una tasa base *p*, con 95 % de confianza y 80 % de potencia, cada variante necesita aproximadamente:

  n ≈ 16 · p · (1 − p) ÷ (p · d)²

Ejemplo: p = 5 % de conversión y una mejora del 20 % (de 5 % a 6 %) dan n ≈ 16 · 0,05 · 0,95 ÷ (0,01)² ≈ 7.600 visitas por variante.

Si el tráfico real no llega a esa cifra en unas semanas, no hay A/B posible: conviene hacer cambios grandes y medir antes y después, junto con entrevistas.

## Pregunta de Sean Ellis (PMF)
"¿Cómo te sentirías si ya no pudieras usar [producto]?" Opciones: muy decepcionado / algo decepcionado / no me importaría.

Se pregunta solo a usuarios activos y recientes. Que ~40 % responda "muy decepcionado" es la referencia habitual de PMF. Con muestras chicas (menos de 30 respuestas), tomalo como señal, no como medida.
