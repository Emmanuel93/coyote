# Contexto para agentes

Lo que el equipo sabe de un proyecto vive en dos archivos versionados: `README.coyote.md` (qué es y cómo correrlo) y `CONTEXT.coyote.md` (lo aprendido). `coyote generate agents` los resume en `AGENTS.md`, que leen todos los IDEs.

## Dejar una nota

```sh
coyote note "los pedidos se confirman al capturar el pago" --type inv --scope pedidos --ref docs/pedidos.md
```

| Tipo | Para qué |
| --- | --- |
| `inv` | Una invariante: lo que no se rompe |
| `dec` | Una decisión vigente |
| `gap` | Algo que falta o que no funciona del todo |
| `risk` | Un riesgo conocido |
| `how` | Cómo se hace algo |
| `term` | Un término del dominio |
| `todo` | Un pendiente |

- Cada nota tiene hasta 20 palabras y el archivo, hasta 1 500 tokens. Cuando se llena, archiva las entradas viejas en el ADR o el workstream que les dio origen.
- Una nota con forma de orden para un agente se rechaza, porque `AGENTS.md` es una instrucción (ADR-0023). Escríbela como un hecho: no "corre X", sino "X se hace con Y".

## Pedir contexto

```sh
coyote get context --scope pedidos                       # el paquete del ámbito
coyote get context --query "cómo se cobra" --budget 2000 # lo más relevante para una pregunta
coyote ask "cuándo se confirma un pedido"                # resultados con su referencia
```

Ninguno llama a un modelo. El paquete abre con las invariantes y, si hay consulta, con lo más relevante y lo que eso cita. Lo citado de los repos sale marcado como dato, y lo que tiene forma de orden lleva ⚠.

## Medir si el contexto sirve

Escribe preguntas reales con las fuentes que deberían salir en `evals/golden.v1.jsonl`:

```json
{"id": "g01", "q": "¿quién aprueba un reembolso?", "expect": ["ctx:reembolsos", "doc:docs/reembolsos.md"]}
```

```sh
coyote eval --update-baseline     # la primera vez; haz commit de evals/baseline.json
coyote eval -v                    # en cada cambio; falla si algo baja más de 2 puntos
```

El formato y las métricas están en [eval-v1](../specs/eval-v1.md).
