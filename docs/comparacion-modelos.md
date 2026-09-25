# Comparación de modelos

Resultados de `go run ./cmd/comparar -razonamiento defecto,none` sobre 9 fotos reales
(8 facturas y 1 que no es factura), con retención cero de datos. Costo total de la prueba: ~USD 0,10.

**Cómo leer la tabla**

- **✅ Limpias**: la factura pasó todas las validaciones (RUC, IVA, total, formatos) sin campos dudosos.
- **Coinciden**: fotos donde RUC, número y total coinciden con la mayoría de los modelos.
  No hay "respuestas correctas" cargadas todavía, así que el acuerdo entre modelos es una aproximación a la precisión.
- **Tokens razon.**: tokens de "razonamiento" promedio. Se cobran como salida y hacen más lenta la respuesta.

## 2026-09-25

| Configuración | ✅ Limpias | ⚠️ Con avisos | 🤔 No factura | ❌ Errores | Coinciden | Costo total | Costo/foto | Seg. prom. | Seg. máx. | Tokens razon. |
|---|---|---|---|---|---|---|---|---|---|---|
| deepseek/deepseek-v4.1-flash (por defecto) | 3 | 4 | 1 | 1 | 5 | $0.0122 | $0.0014 | 34.8 | 90.0 | 1981 |
| deepseek/deepseek-v4.1-flash (none) | 2 | 6 | 1 | 0 | 3 | $0.0022 | $0.0002 | 3.6 | 4.5 | 0 |
| google/gemini-3.1-flash-lite (por defecto) | 5 | 3 | 1 | 0 | 6 | $0.0085 | $0.0009 | 3.2 | 3.8 | 0 |
| google/gemini-3.1-flash-lite (none) | 4 | 4 | 1 | 0 | 6 | $0.0086 | $0.0010 | 3.0 | 3.5 | 0 |
| anthropic/claude-haiku-4.5 (por defecto) | 3 | 4 | 2 | 0 | 2 | $0.0354 | $0.0039 | 4.4 | 4.9 | 0 |
| anthropic/claude-haiku-4.5 (none) | 2 | 5 | 2 | 0 | 2 | $0.0358 | $0.0040 | 4.5 | 5.3 | 0 |

**Conclusión:** `google/gemini-3.1-flash-lite` pasa a ser el modelo por defecto. Lee más facturas sin problemas,
es el que más coincide con el resto, tarda ~3 s y cuesta ~USD 0,0009 por foto.
DeepSeek con razonamiento por defecto "piensa" ~2.000 tokens por foto: tarda 35 s en promedio y una lectura superó los 90 s.
