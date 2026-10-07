---
name: saas-playbook
description: Diagnostica en qué etapa está un SaaS, app o bot (dolor de nicho, ICP, validación, PSF, MVP, landing, contenido, CRO, onboarding y AHA moment, A/B, paywall, soporte, retención y PMF, distribución, CAC y unit economics) y propone las próximas acciones con una prueba chica y medible. Usala siempre que alguien pregunte qué hacer ahora con su producto, en qué etapa está, cómo validar una idea, conseguir los primeros clientes, armar o mejorar una landing, el onboarding o el paywall, ponerle precio, bajar el churn, saber si hay product-market fit o si el negocio es rentable (CAC, margen, payback), aunque no diga "SaaS" ni "playbook". No la uses para tareas puramente técnicas de código sin una decisión de producto o negocio detrás.
---

# SaaS Playbook

Guía para llevar un producto digital de "tengo una idea o un MVP" a "trae clientes con números que cierran", con un recorrido de 17 pasos. Sirve para diagnosticar un producto completo o para trabajar un solo paso a fondo.

Responder en el idioma del pedido. Los términos de la industria (ICP, PSF, PMF, CRO, CAC, TTV, AHA moment, churn, paywall) quedan en inglés, con una aclaración corta la primera vez que aparecen si el usuario no parece conocerlos.

## Por qué existe esta guía

El error más caro de un producto chico es optimizar un paso que todavía no importa: anuncios pagos sin saber si alguien quiere el producto, A/B tests con 40 visitas, rediseñar la landing cuando el problema es que el onboarding no llega al valor. Los 17 pasos están en orden porque cada uno se apoya en la evidencia del anterior. El trabajo de esta skill es mostrar **dónde está el cuello de botella real** y proponer **la prueba más chica que lo destrabe**.

## Los 17 pasos en 5 etapas

| Etapa | Pasos |
|---|---|
| **Descubrir** | 1. Dolor de nicho · 2. ICP · 3. Validar el dolor con ~10 personas · 4. Testear la solución y buscar señales de PSF |
| **Construir** | 5. MVP (con IA si ayuda) · 6. Landing clara sobre el dolor y el público |
| **Atraer y convertir** | 7. Contenido que lleve a la landing · 8. Medir la conversión y hacer CRO (8a: si funciona, potenciar con pauta; 8b: si no, ver si falla la landing o el público) · 11. A/B tests cuando haya tráfico suficiente |
| **Activar y monetizar** | 9. Onboarding simple, TTV corto, foco en el AHA moment · 10. Medir el onboarding (completitud, ¿percibieron el valor?) · 12. Paywall: ¿convierte? si no, ¿valor, precio u oferta? · 13. Soporte, reembolsos, bugs |
| **Retener y escalar** | 14. Retención y churn, señales de PMF · 15. Nuevas estrategias de distribución · 16. CAC y cuánto sobra después de adquisición, IA, infra, comisiones, reembolsos y soporte · 17. ¿Se pueden traer más clientes con esa cuenta positiva? Repetir |

El detalle de cada paso (objetivo, preguntas, qué evidencia cuenta, errores comunes y cuándo avanzar) está en `references/pasos.md`. Leelo cuando vayas a evaluar o trabajar un paso concreto. Las definiciones y fórmulas de las métricas están en `references/metricas.md`. Leelo antes de calcular algo o de decir si un número es bueno.

## Flujo para diagnosticar un producto

1. **Juntar evidencia antes de opinar.** Usá lo que haya:
   - lo que contó el usuario;
   - el repo (README, docs, roadmap, métricas instrumentadas, eventos que se registran);
   - landing, precios y números reales.

   Si hay código, buscá qué se mide de verdad (eventos, embudos, costos). Eso dice mucho de los pasos 10, 14 y 16. No inventes números ni testimonios. Si un dato cambia el diagnóstico y no está, anotalo como pregunta.

2. **Marcar cada paso con un estado:**
   - ✅ **Hecho con evidencia:** hay algo verificable, como entrevistas registradas, una métrica medida, clientes que pagan o una landing publicada.
   - 🟡 **En curso o hipótesis:** se pensó o se armó, pero falta medir o validar.
   - ❌ **Sin hacer.**
   - ⏸️ **Todavía no corresponde:** sería prematuro, por ejemplo un A/B sin tráfico o pauta sin PSF.

   Una afirmación sin evidencia es 🟡, no ✅. "La gente lo va a querer" no valida el paso 3, y que el código exista no prueba el PSF.

3. **Encontrar el cuello de botella.** Es el primer paso sin evidencia que frena a los siguientes. Normalmente es el más temprano en ❌ o 🟡 dentro de las etapas Descubrir y Activar. Explicá en dos o tres frases por qué destrabar ese paso vale más que mejorar los posteriores. Es habitual que un producto técnico esté adelantado en Construir y atrasado en Descubrir: decilo sin vueltas.

4. **Proponer 3 próximas acciones**, ordenadas por impacto/esfuerzo, con al menos una sobre el cuello de botella. Cada acción tiene que tener:
   - **Prueba:** la versión más chica que sirve para aprender, mejor si se hace en días que en meses.
   - **Métrica:** qué se observa.
   - **Criterio:** qué resultado cuenta como señal positiva, como heurística y no como verdad.
   - **Decisión:** qué se hace si sale bien y qué si sale mal.

5. **Cerrar con preguntas abiertas** para el dueño del producto. Son las que más cambiarían el diagnóstico.

Usá la plantilla `assets/plantilla-diagnostico.md` para el formato. Si el pedido es sobre un solo paso ("ayudame con el paywall"), no hagas el diagnóstico completo: andá directo a ese paso con `references/pasos.md`. Señalá un paso anterior solo si de verdad lo bloquea.

## Criterios que importan

- **PSF no es PMF.** El PSF (problem-solution fit) son señales tempranas: las personas reconocen el dolor, entienden la solución y muestran compromiso (prueban, piden acceso, ofrecen pagar). El PMF (product-market fit) es retención y pago sostenidos: vuelven solos, siguen pagando, lo recomiendan. No declares PMF con entusiasmo de entrevistas.
- **Los números de referencia son heurísticas.** Las ~10 entrevistas, la pregunta de Sean Ellis (≥40 % "muy decepcionado") o las tasas típicas de conversión orientan, pero no garantizan. Decí siempre de dónde sale el número y que depende del nicho.
- **Sin tráfico no hay A/B.** Con poco volumen, mejor hacer pruebas cualitativas (mirar sesiones, hablar con usuarios) y cambios grandes, no variantes chicas. `references/metricas.md` explica cómo estimar la muestra.
- **La cuenta tiene que cerrar con todos los costos.** El margen por cliente descuenta IA, infraestructura, comisiones de pago, reembolsos y el tiempo de soporte, no solo el servidor. Calculá el payback del CAC y decí qué supuestos usaste.
- **Cada recomendación respeta el contexto del producto:** regulación, privacidad de datos, capacidad del equipo y la experiencia actual de los usuarios. Una idea de crecimiento que rompe lo que ya funciona es un costo, no una mejora gratis.
- **No prometas resultados** ("vas a ganar mucho dinero"). La guía ordena el trabajo y reduce riesgo, nada más.
- **No ejecutes acciones externas por tu cuenta:** publicar, gastar en anuncios, escribirles a clientes. Proponelas. Crear archivos (landing, guiones de entrevista, planillas) está bien cuando el usuario lo pide o es parte natural de la tarea.

## Formatos rápidos

- **"¿En qué estoy?":** diagnóstico completo con la plantilla.
- **"¿Qué hago esta semana?":** el cuello de botella y una sola acción con su prueba.
- **Un paso puntual:** objetivo del paso, preguntas clave, qué medir, errores comunes y una prueba concreta.
- **Números (CAC, margen, conversión):** la fórmula, los supuestos, el cálculo y qué significa. Si faltan datos, armá la cuenta con variables y decí qué dato falta.
