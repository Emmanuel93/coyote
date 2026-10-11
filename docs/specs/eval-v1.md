# coyote eval v1

Estado: nuevo en v1.0 · Implementación: `internal/golden`, `coyote eval` · Decisión: ADR-0025

`coyote eval` mide si el contexto que entrega coyote trae las fuentes correctas. Usa un golden: preguntas con las fuentes que deben aparecer en la respuesta. No llama a ningún modelo, así que no cuesta nada y puede correr en cada PR.

```
coyote eval [--golden evals/golden.v1.jsonl] [--baseline evals/baseline.json] [--update-baseline] [--budget 2000] [--json] [-v]
```

## Golden v1

Un archivo JSONL con una pregunta por línea. Las líneas vacías y las que empiezan con `#` se saltan.

```json
{"id": "g01", "q": "¿cómo aprueba una persona una acción de la cola?", "expect": ["doc:docs/specs/gate-v1.md", "adr:ADR-0009"]}
```

| Campo | Qué es |
| --- | --- |
| `id` | Identificador único de la pregunta |
| `q` | La pregunta, como la haría una persona o un agente |
| `scope` | Opcional: el ámbito, como en `ask --scope` |
| `expect` | Las fuentes esperadas: `doc:ruta` (la ruta exacta del repo), `adr:ADR-0000` (el número exacto del ADR) o `ctx:ámbito` (una entrada de `CONTEXT.coyote.md` de ese ámbito o de uno de sus subámbitos; una nota general no cuenta) |
| `k` | Opcional: cuántos resultados de `ask` cuentan; 5 por defecto |

Una clave desconocida, un `id` repetido, una pregunta que repite a otra (el mismo texto, ámbito y fuentes), una referencia de otra forma (`adr:-`, `doc:../x`) o un golden sin preguntas hacen fallar la lectura. El golden y la línea base viven en `evals/` del proyecto: `--golden` es un `.jsonl` y `--baseline` un `.json` de esa carpeta, de hasta 4 MiB.

## Métricas

Van en puntos porcentuales, promediadas sobre las preguntas. Se miden sobre el índice sin los eventos del ledger: crecen con cada commit y moverían la medida sin que cambie el contexto.

- **recall@k.** Qué parte de lo esperado sale entre los k primeros resultados de `ask`.
- **MRR.** El inverso del puesto del primer resultado esperado, entre los 10 primeros.
- **Cobertura del paquete.** Qué parte de lo esperado entra en `get context --query`, dentro del presupuesto tal como lo recibe el agente.

## Línea base

- `--update-baseline` escribe `evals/baseline.json` con las tres métricas, la medida de cada pregunta, el golden, el presupuesto y la fecha. Ese cambio se revisa en el PR: `gate pr` marca R2 el golden y la línea base.
- Sin `--update-baseline`, `coyote eval` compara sobre las preguntas de la línea base y sale con 1 si una métrica baja más de 2 puntos o si falta en el golden una de ellas. Una pregunta nueva no cuenta hasta que se fija de nuevo la línea base, así que agregar preguntas fáciles no tapa una regresión.
- Sin línea base, mide y sale con 1: no hay contra qué comparar.
- Medir con otro `--budget` que el de la línea base es un error de uso.
- En coyote, `make check` y la CI corren `coyote eval`.
- Para un agente, `eval` es lectura libre y `eval --update-baseline` pide aprobación.

## Límites

- Mide si la fuente llega al agente, no si el agente la usa bien. El juez de fidelidad, que lo mediría con un modelo, queda para v1.1 (ADR-0025).
- El golden de coyote mide coyote. Cada proyecto escribe el suyo con sus preguntas y sus fuentes.
