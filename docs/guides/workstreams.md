# Workstreams

Un workstream es un plan de varios pasos, cada uno a cargo de un agente o de una persona. Tiene topes en dólares y se detiene donde lo pide su modo. El estado sale del ledger, así que un paso nunca se pierde (ADR-0014).

## 1. Escribir el plan

`coyote/workstreams/W-0003-pagos/plan.yaml`:

```yaml
id: W-0003
title: pagos con revisión
autonomy: supervised           # manual, supervised o autonomous
risk: R2
budget_usd: 3                  # tope del plan
steps:
  - id: S1
    does: diseñar el cobro con tarjeta
    agent: coyote-architect
    input: [docs/pagos.md]
    output: diseño
    sections: [Contrato, Riesgos]    # lo que la entrega tiene que traer
    max_usd: 0.5
  - { id: S2, does: implementar, agent: coyote-dev, input: [step:S1], output: [parche], max_usd: 1.5 }
  - { id: S3, does: probar en staging, agent: persona }
```

Cada paso de agente declara su salida y su tope (A2). El campo `task` lleva la instrucción larga. Todos los campos están en [workstream-v1](../specs/workstream-v1.md).

```sh
coyote ws check W-0003      # valida el plan y muestra modelo, tope y revisión de cada paso
```

## 2. Correrlo

```sh
coyote ws run W-0003                          # corre hasta el siguiente punto de revisión
coyote ws status W-0003                       # dónde va y cuánto lleva
coyote ws continue W-0003                     # aceptas el paso y sigue
coyote ws continue W-0003 --redo "usa la llave de idempotencia"   # repite el paso con tu revisión
coyote ws continue W-0003 --retry             # repite un paso que falló
```

| Modo | Dónde se detiene |
| --- | --- |
| `manual` | Después de cada paso; el siguiente lo lanza `ws run` |
| `supervised` | Después de cada paso; `ws continue` corre el siguiente |
| `autonomous` | En un paso de persona o con `gate: human`, en el tope o ante una falla. Corre solo en su rama `ws/`, con el plan exacto aprobado por su hash |

El gate aplica igual en los tres modos: si un paso deja acciones en la cola, apruébalas y corre `ws continue`. El modo del proyecto es el techo. Un plan `autonomous` en un proyecto `supervised` corre como `supervised`.

## 3. Cerrarlo

```sh
coyote close W-0003        # el costo real de cada paso, desde el ledger
```
