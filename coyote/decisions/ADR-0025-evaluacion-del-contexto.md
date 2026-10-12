---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0025: Evaluación del contexto con un golden (D40)

## Contexto y problema
`get context` y `ask` entregan lo que un agente sabe de un proyecto, y nada mide si entregan lo correcto. Un cambio al índice, al stemmer o al presupuesto de un paquete puede empeorar las respuestas sin que ninguna prueba falle. La propuesta pide un golden de 30 preguntas por proyecto, un juez de fidelidad mensual y un umbral de regresión de 2 puntos en la CI (C6.3). También pide expandir los paquetes un salto (C6.2).

## Opciones consideradas
- **Juez con modelo en cada CI.** Gasta en cada PR y no es determinista.
- **Métricas de recuperación sin modelo en la CI y un juez opcional.** La CI verifica que salgan las fuentes esperadas. El juez, que evalúa si la respuesta es fiel a esas fuentes, corre cuando la persona lo decide.
- **Sin golden.** Se sigue sin saber.

## Decisión
- **Golden v1.** Es un archivo JSONL con una pregunta por línea:
  - `id`;
  - `q`, la pregunta;
  - `scope`, opcional;
  - `expect`, las referencias que deben aparecer (`doc:ruta`, `adr:ADR-0009`, `ctx:ámbito`);
  - `k`, opcional, con 5 por defecto.

  coyote trae `evals/golden.v1.jsonl` con 30 preguntas sobre sí mismo. El mismo formato sirve para cualquier proyecto.
- **`coyote eval`.** Corre cada pregunta contra el índice local y mide:
  - recall@k de `ask`: qué parte de lo esperado sale entre los k primeros;
  - MRR: en qué puesto sale lo primero esperado;
  - cobertura del paquete: qué parte de lo esperado entra en `get context --query` dentro de su presupuesto.

  Compara contra la línea base versionada (`evals/baseline.json`) y falla si una métrica baja más de 2 puntos porcentuales. `--update-baseline` la reescribe, y ese cambio se revisa en el PR como cualquier otro.
- **En la CI de coyote.** `make check` corre `coyote eval`.
- **Expansión de un salto.** El paquete suma los fragmentos que citan sus mejores resultados (ADRs, documentos y specs enlazados), con menor prioridad y dentro del presupuesto. Se queda solo si mejora la cobertura del golden sin bajar las otras métricas.
- **Juez de fidelidad.** Un agente respondería cada pregunta con el paquete y otro calificaría si la respuesta se sostiene en las fuentes. Correría con `coyote run`, con los topes del proyecto y cuando la persona lo decida, nunca en la CI. No entró en v1.0: el plan lo cortaba primero si el tiempo apretaba, y medir antes la recuperación ya encontró el problema más grande (ver Consecuencias). v1.1 trae solo los pendientes de G5 a G7.

## Consecuencias
- La primera medida del golden de coyote encontró que el paquete con consulta casi nunca traía las fuentes esperadas: 1.7 % de cobertura. Las entradas de `CONTEXT.coyote.md` llenaban el presupuesto antes de los ADRs y los documentos. Con la sección "Lo más relevante para la consulta" después de las invariantes, la cobertura subió a 86.7 %. Con la expansión de un salto llegó a 93.3 %.
- Un cambio que empeora la recuperación falla en el PR sin gastar un dólar.
- El golden de coyote mide coyote. Cada proyecto necesita el suyo, y escribirlo es trabajo de su equipo.
- Las métricas sin modelo miden si la fuente llega al agente, no si el agente la usa bien. Eso lo mide el juez.
