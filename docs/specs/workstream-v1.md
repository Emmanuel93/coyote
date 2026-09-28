# Workstreams v1 — plan, contrato por paso y motor

Estado: nuevo en v0.5.0 · Implementación: `internal/workstream`, `coyote ws` · Decisión: ADR-0014

Un workstream es una unidad de trabajo con su plan (`coyote/workstreams/W-0001-algo/plan.yaml`), sus corridas (`runs/`) y su cierre (`close.md`). El motor corre el plan paso a paso con el núcleo de `coyote run` y se detiene donde el modo lo pide.

## El plan

```yaml
id: W-0007
title: cobros idempotentes
autonomy: supervised          # manual | supervised | autonomous; el proyecto es el techo
risk: R2                      # riesgo por defecto de los pasos
budget_usd: 3                 # tope del plan; autonomous lo exige
steps:
  - id: S1
    does: revisar el diseño de cobros
    agent: coyote-reviewer
    input: [docs/pagos.md, diff:servicios=main...HEAD]
    output: informe de revisión
    sections: [Hallazgos, Riesgos]   # la entrega los trae como títulos o el paso falla
    max_usd: 0.8
  - { id: S2, does: proponer el parche, agent: coyote-dev, input: [step:S1], output: parche como diff, max_usd: 0.8, gate: human }
  - { id: S3, does: probar en staging, agent: persona }
```

Contrato de cada paso (A2), que `coyote ws check` revisa:

| Campo | Qué es |
|-------|--------|
| `id`, `does` | identificador único y qué hace; `task` opcional da la instrucción larga |
| `agent` | un agente del proyecto o `persona`, si el paso lo hace la persona |
| `input` | `step:ID` (el artefacto de un paso anterior), `diff:repo=RANGO` (el impacto en el producto) o una ruta del proyecto |
| `output` | la salida que se espera; obligatoria en los pasos de agente |
| `sections` | títulos que la entrega tiene que traer: el esquema de salida verificable |
| `risk` | R1, R2 o R3; sube el piso de modelo del router |
| `max_usd`, `max_turns` | topes del paso; `max_usd` es obligatorio en los pasos de agente |
| `gate: human` | la persona revisa este paso también en `autonomous` |

Un plan con errores no corre. Las entradas de archivo son rutas relativas dentro del proyecto, nunca de `.git` ni de `.coyote`. Cada entrada entra en la tarea entre cercas, como dato, con un tope de unos 3000 tokens; el agente lee el resto si lo necesita.

## Comandos

```sh
coyote ws check W-0007           # contrato, entradas y lo que exige el modo
coyote ws status [W-0007]        # avance, costo contra el tope y qué sigue (--json)
coyote ws run W-0007             # corre desde el primer paso pendiente
coyote ws continue W-0007        # acepta lo que corrió y sigue
coyote ws continue W-0007 --redo "usa una llave de idempotencia"   # repite el paso con tu revisión
coyote ws continue W-0007 --retry                                  # repite un paso que falló
```

`ws run` y `ws continue` los corre la persona. No corren dentro de una sesión de agente, y el gate los bloquea si los intenta un agente. `ws continue` pide además una terminal interactiva. Un lock por workstream evita dos motores a la vez.

## Modos

El modo del plan no pasa el del proyecto (`coyote/project.yaml`). `--mode` baja la autonomía de una corrida, nunca la sube. Ningún modo cambia lo que el gate deja pasar: lo que tiene efectos se aprueba acción por acción.

| Modo | Cuándo se detiene el motor |
|------|-----------------------------|
| `manual` | después de cada paso; `ws continue` solo registra la revisión y el paso siguiente lo lanza `ws run` |
| `supervised` | después de cada paso; `ws continue` registra la revisión y corre el siguiente |
| `autonomous` | en un paso con `gate: human`, en un paso de la persona, al llegar al tope o ante una falla; al final, quien lo autorizó evalúa lo que corrió |

`autonomous` exige lo que pide A4:

- la rama `ws/<W>` del proyecto;
- `budget_usd` en el plan;
- la autorización de ese plan exacto.

La primera vez, `ws run` deja en la cola del gate una propuesta con el plan completo. La persona la revisa con `coyote review` y la aprueba con `coyote approve [--uses N] [--for 8h]`. La aprobación queda atada al hash del plan: cambiarlo pide autorizarlo otra vez. Cada arranque del motor gasta un uso.

Siempre se detiene cuando:

- un paso falla;
- la entrega no trae sus secciones;
- quedan acciones en la cola del gate;
- el plan llega a su tope.

## Estado

El estado sale del ledger, no de otro archivo. Así, después de un `pull`, el equipo ve lo mismo.

| Evento | Efecto en el paso |
|--------|-------------------|
| `run` con `step:` | `ok` lo deja por revisar, `pend` en la cola del gate, `fail` fallido; `skip` no lo cambia |
| `apr` con `step:` | aceptado por la persona; en un paso de la persona, hecho |
| `rej` con `step:` y `doc:` | por rehacer, con la revisión guardada junto a las corridas |
| `plan` | el motor se detuvo: `mode:`, `stop:` y `at:` |
| `gate` con `auth:ws` | un arranque de `autonomous` con su aprobación (`apr:`) |

Todas las corridas del workstream suman al tope del plan, también las de `coyote run --ws`. Cada paso corre con el menor tope entre el suyo, lo que queda del plan y lo que queda del mes.

## Acciones en la cola

Un paso que deja acciones en la cola del gate se detiene en `pend`. Cuando la persona las decide, `ws continue` retoma la sesión de Claude Code del paso (`--resume`, con la sesión que guarda el artefacto). Así el agente repite exactamente las llamadas aprobadas. `--retry` corre el paso desde cero.

## Cierre

`coyote close W` agrega a `close.md` la tabla de pasos con su estado, corridas y costo. Avisa si quedan pasos sin cerrar.

## Límites

- `--resume` con `--agent` en modo headless no se probó contra Claude Code real: si falla, `--retry` corre el paso de nuevo.
- La revisión de un paso es la de quien corre `ws continue`: en `autonomous`, coyote avisa si no es quien autorizó el loop, pero no lo impide.
- Los planes de v0.1 a v0.4 de este repo se escribieron antes del contrato y no pasan `ws check` (les falta `max_usd`).
