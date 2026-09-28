---
status: proposed
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0014: Motor de workstreams con modos supervised y autonomous

## Contexto y problema
Un workstream tiene un plan (`plan.yaml`) con pasos, pero nada lo ejecuta: cada paso se corre a mano con `coyote run`. `supervised` y `autonomous` se aceptan en `project.yaml` sin efecto. La propuesta pide que todo paso declare contrato, tope y esquema de salida (A2). También pide que un loop autónomo corra en una rama `ws/`, dentro de sus topes, y que lo evalúe quien lo autorizó (A4).

## Opciones consideradas
- Un motor en coyote que corre cada paso con `coyote run`, encadena artefactos, se detiene en puntos de control y lleva el presupuesto del workstream.
- Un solo `coyote run` con un agente coordinador que llama subagentes: un tope para todo y el ledger no ve cada paso.

## Decisión
`coyote ws check|run|continue|status W`:

- **Contrato por paso** (A2): agente, qué hace, entradas (pasos anteriores, archivos, impacto de un diff), salida esperada, riesgo, tope de dólares y turnos, y `gate: human` si pide revisión.
- **`supervised`:** después de cada paso el motor se detiene. La persona revisa el artefacto y sigue con `coyote ws continue`.
- **`autonomous`:** sigue hasta un paso con `gate: human`, el tope del plan o una falla. Corre en la rama `ws/<W>`, y quien lo autorizó evalúa el resultado antes del merge (A4).
- **Gate:** en los dos modos, lo que tiene efectos pasa por el gate y ningún agente se aprueba. El modo cambia cuándo se detiene el motor, no lo que el gate deja pasar.
- **Presupuesto:** cada paso usa el router con el menor tope entre el paso, lo que queda del plan y lo que queda del mes. El estado vive en `.coyote/` y los pasos, en el ledger.

## Consecuencias
- Un plan se ejecuta con su costo por paso a la vista y puntos de control donde la persona decide.
- `autonomous` exige una rama `ws/` y topes; sin ellos el motor no arranca.
- El piloto usa `supervised` hasta tener evidencia de planes medidos (D25).
