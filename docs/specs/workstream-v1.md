# Workstreams v1 — plan, contrato por paso y motor

Estado: nuevo en v0.5.0; `prd:` y la autorización que cubre los archivos que lee el plan, desde v1.0.0 · Implementación: `internal/workstream`, `coyote ws` · Decisiones: ADR-0014, ADR-0028

Un workstream es una unidad de trabajo con su plan (`coyote/workstreams/W-0001-algo/plan.yaml`), sus corridas (`runs/`) y su cierre (`close.md`). El motor corre el plan paso a paso con el núcleo de `coyote run` y se detiene donde el modo lo pide.

## El plan

```yaml
id: W-0007
title: cobros idempotentes
autonomy: supervised          # manual | supervised | autonomous; el proyecto es el techo
risk: R2                      # riesgo por defecto de los pasos
prd: coyote/prd/cobros.md     # de qué PRD parte el plan (R20); o step:S1 si el primer paso lo escribe
budget_usd: 3                 # tope del plan; autonomous lo exige
steps:
  - id: S1
    does: revisar el diseño de cobros
    agent: coyote-reviewer
    input: [coyote/prd/cobros.md, docs/pagos.md, diff:servicios=main...HEAD]
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
| `input` | `step:ID` (el artefacto de un paso anterior), `diff:repo=RANGO` (el impacto en el producto) o una ruta de un archivo de texto del proyecto |
| `output` | la salida que se espera; obligatoria en los pasos de agente |
| `sections` | títulos que la entrega tiene que traer: el esquema de salida verificable |
| `risk` | R1, R2 o R3; sube el piso de modelo del router |
| `max_usd`, `max_turns` | topes del paso; `max_usd` es obligatorio en los pasos de agente |
| `gate: human` | la persona revisa este paso también en `autonomous` |

## El PRD del plan

`prd:` dice de qué documento parte el plan (R20, ADR-0028). Vale una ruta `.md` del proyecto (`coyote/prd/cobros.md`) o `step:ID`, cuando el primer paso lo escribe y la persona lo acepta con `ws continue`. Es opcional: lo que `ws check` dice depende del riesgo.

| Caso | Hallazgo |
|------|----------|
| plan R2 o R3 (el suyo o el de un paso) sin `prd:` | aviso (R20) |
| `prd:` que no es una ruta `.md` del proyecto ni `step:ID` (el prefijo va en minúsculas), que es un `diff:` o que lleva un salto de línea, un control, un carácter invisible o un espacio al borde de un nombre | error |
| `step:ID` que no existe o que es un paso de la persona | error |
| en `autonomous`, el paso del PRD sin `gate: human` | error (R20); se mira el modo con el que corre, y `--mode supervised`, que se detiene tras cada paso, lo acepta |
| el archivo existe pero no es un archivo regular del proyecto: un enlace, una carpeta o más de 8 MB | error |
| el archivo no existe todavía, o no es texto | aviso |
| `step:ID` con un paso de agente antes: esos corren sin PRD | aviso |
| ningún paso lo recibe en `input` | aviso |

`ws check` muestra de dónde parte el plan en la línea `PRD:` y en el campo `prd` de `--json`; un `prd:` que no se entiende sale escapado y con «(inválido)», nunca como un paso. El motor no agrega el PRD a los pasos por su cuenta: lo recibe el paso que lo declara en `input`. El campo es parte del plan, así que cambiarlo cambia el hash que se autoriza en `autonomous`.

Un plan con errores no corre. La carpeta del workstream es `W-0001` o `W-0001-nombre`, sin espacios, porque sus rutas van al ledger. Si hay más de una carpeta con ese id, vale la que tiene `plan.yaml` (una carpeta huérfana, con corridas sueltas o un nombre viejo, no esconde el plan de otra); con dos que tienen plan, o dos sin él, coyote no elige a ciegas y pide dejar una. Esa carpeta es el workstream: el `id:` del plan tiene que ser el de su carpeta (si no, es un error), y `ws`, `run --ws`, `close` y `web` atribuyen todo al de la carpeta, no al de adentro. Lo que el plan escribe y coyote muestra (`id`, `title`, `does`, `autonomy`, `prd:`, las entradas) sale en una línea y escapado: un salto de línea o un carácter invisible no dibujan líneas que coyote no escribió, ni la que le dice a la persona qué sigue. Un `autonomy:` que no es `manual`, `supervised` ni `autonomous` es un error del plan, y `--mode` no se compara con él hasta que se corrija.

Las entradas de archivo son rutas relativas dentro del proyecto, nunca de `.git` ni de `.coyote` (en cualquier combinación de mayúsculas), sin saltos de línea, controles, marcas que no se ven, espacios especiales (el espacio sin corte, el ideográfico, los de ancho fijo), una marca combinante que abre un nombre ni espacios al borde de un nombre. `step:`, `diff:` y `file:` son prefijos reservados y se escriben en minúsculas: ni `STEP:S1`, ni `file:step:S1`, ni una ruta que los forma al limpiarla (`./step:S1`, `a/../step:S1`) valen como un archivo. Cada entrada de archivo cuenta con la misma revisión que el PRD: no existe todavía (aviso), no es texto (aviso) o no es un archivo regular del proyecto (error).

Lo que un paso recibe de un archivo es dato, no una instrucción (ADR-0023). La tarea entera sale escapada (controles y marcas invisibles), cada entrada entra entre cercas, con un tope de unos 3000 tokens, y el agente lee el resto si lo necesita. Si el texto tiene forma de orden para un agente, la entrada lleva una marca que lo dice, con las reglas que saltaron; si solo trae caracteres que no se ven (un guion suave, la marca de dirección del hebreo o del árabe), la marca dice eso y no habla de órdenes. Un final de línea de Windows (CRLF) llega como un salto de línea; los acentos, el CJK y los emojis con ZWJ o con selector de color llegan intactos. Un archivo en UTF-16 se lee como texto.

## Comandos

```sh
coyote ws check W-0007           # contrato, entradas y lo que exige el modo
coyote ws status [W-0007]        # avance, costo contra el tope y qué sigue (--json)
coyote ws run W-0007             # corre desde el primer paso pendiente
coyote ws continue W-0007        # acepta lo que corrió y sigue
coyote ws continue W-0007 --redo "usa una llave de idempotencia"   # repite el paso con tu revisión
coyote ws continue W-0007 --retry                                  # repite un paso que falló
```

`ws run` y `ws continue` los corre la persona:

- **Nunca desde un agente.** No corren dentro de una sesión de agente, y el gate los bloquea aunque vengan detrás de un envoltorio del shell (`env`, `script`, `bash -c`, `xargs`, `eval`, llaves, `if` o `for`…).
- **Con terminal.** `ws continue` pide además una terminal interactiva.
- **Un motor por workstream.** Lo asegura un lock del sistema (`flock`), que se suelta solo si el proceso muere.
- **Ctrl-C.** Detiene a Claude Code con todo lo que lanzó y la corrida queda en el ledger como interrumpida.
- **Ledger.** Si una corrida no se puede registrar, el motor se detiene en lugar de repetir el paso.

`coyote run --ws W` respeta el tope y el lock del plan, si lo hay.

## Modos

El modo del plan no pasa el del proyecto (`coyote/project.yaml`). `--mode` baja la autonomía de una corrida, nunca la sube. Ningún modo cambia lo que el gate deja pasar: lo que tiene efectos se aprueba acción por acción. Cuando `--mode` bajó el modo, las órdenes que coyote sugiere (`coyote ws continue W-0007 --mode supervised`) llevan el mismo `--mode`: copiarlas sin él correría con más autonomía de la que elegiste. Las reglas del plan se juzgan con el modo con el que corre, salvo que `autonomous` exija `budget_usd`, que mira el modo que el plan declara: un plan `autonomy: autonomous` sin tope es un error aunque se corra con `--mode supervised`.

| Modo | Cuándo se detiene el motor |
|------|-----------------------------|
| `manual` | después de cada paso; `ws continue` solo registra la revisión y el paso siguiente lo lanza `ws run` |
| `supervised` | después de cada paso; `ws continue` registra la revisión y corre el siguiente |
| `autonomous` | en un paso con `gate: human`, en un paso de la persona, al llegar al tope o ante una falla; al final, quien lo autorizó evalúa lo que corrió en `autonomous`; un paso que corrió en otro modo (con `--mode supervised`, por ejemplo) lo revisa antes de seguir |

`autonomous` exige lo que pide A4:

- la rama `ws/<W>` del proyecto;
- `budget_usd` en el plan;
- la autorización de ese plan exacto.

La primera vez, `ws run` deja en la cola del gate una propuesta con el plan completo y la lista de los archivos que lee (el PRD y las entradas de archivo), cada uno con las primeras cifras de su huella. La persona la revisa con `coyote review` y la aprueba con `coyote approve [--uses N] [--for 8h]`. La aprobación queda atada al hash del plan y del contenido de esos archivos: cambiar uno de los dos pide autorizar otra vez. Un archivo que falta o que no se puede leer cuenta como un estado más, así que aparecer o arreglarse también cambia el hash; por eso un archivo que un paso del plan crea y otro lee detiene el loop antes del segundo, y `ws check` lo avisa. Cada arranque del motor gasta un uso.

Antes de cada paso, el motor vuelve a revisar, sin gastar otro uso, que:

- la aprobación no se revocó ni venció;
- el plan en disco es el autorizado;
- la rama sigue siendo la del workstream.

Y al armar el paso, que el archivo que ese paso recibe tiene el contenido autorizado. El motor lee el archivo una sola vez, calcula su huella y entrega esos mismos bytes: no hay un momento entre revisar y entregar. Un paso del propio plan que edita un archivo que el siguiente no recibe no detiene el loop; si el siguiente lo recibe, sí, con «cambió desde que lo autorizaste».

Si algo cambió, el motor se detiene, dice qué (el plan, la rama, la aprobación o el archivo) y lo deja en el ledger como un evento `plan` con `stop:authorization` o `stop:input`: quien audita ve por qué hay dos corridas y un hueco.

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
| `apr` con `step:` | aceptado por la persona, si su `doc:` es el último artefacto del paso; en un paso de la persona, hecho |
| `rej` con `step:` y `doc:` | por rehacer, con la revisión guardada junto a las corridas |
| `plan` | el motor se detuvo: `mode:`, `stop:` y `at:`; si lo detuvo A4 antes de un paso (`stop:authorization` o `stop:input`), no cambia el estado de ningún paso |
| `gate` con `auth:ws` | un arranque de `autonomous` con su aprobación (`apr:`) |

Todas las corridas del workstream suman al tope del plan, también las de `coyote run --ws`. Cada paso corre con el menor tope entre el suyo, lo que queda del plan y lo que queda del mes.

## Acciones en la cola

Un paso que deja acciones en la cola del gate se detiene en `pend`. Cuando la persona las decide, `ws continue` retoma la sesión de Claude Code del paso (`--resume`, con la sesión que guarda el artefacto). Así el agente repite exactamente las llamadas aprobadas. `--retry` corre el paso desde cero.

## Cierre

`coyote close W` agrega a `close.md` la tabla de pasos con su estado, corridas y costo. Avisa si quedan pasos sin cerrar y, solo si alguno corrió en el loop autónomo y siguió de largo sin que nadie lo revisara (un paso con `gate: human` se detuvo a esperar a la persona y no cuenta), dice cuántos son y que los evalúa quien autorizó el plan (A4). Un plan `supervised` o `manual` no tiene nada que evaluar así.

## Límites

- `--resume` con `--agent` en modo headless no se probó contra Claude Code real: si falla, `--retry` corre el paso de nuevo.
- La revisión de un paso es la de quien corre `ws continue`: si acepta algo que corrió en el loop autónomo, coyote avisa una vez, sea cual sea el `--mode` con que lo acepta, que A4 pide que lo evalúe quien autorizó el loop, y no lo impide. Lo que se rehízo en `supervised` o `manual` no lleva el aviso.
- `ws status --json` dice en `next.run` si `ws run` y `ws continue` correrían: con errores en el plan que ningún modo menor salva, `next.run` es `false` y `next.reason` es `plan`, igual que el texto «corrige el plan»; `next.step` sigue siendo donde se detendría el motor en el modo del plan.
- `prd:` comprueba que el PRD exista y esté conectado al plan, no que sea bueno ni que el plan lo cumpla.
- La autorización cubre los archivos que el plan declara. Lo que un agente lee por su cuenta con `Read` no se puede conocer de antemano y no entra al hash; los pasos de agente solo reciben herramientas de lectura. Tampoco entran el contenido de `diff:repo=RANGO` ni el paquete de contexto, que son dinámicos por naturaleza.
- Cada arranque del motor (`ws run`, `ws continue`, `--retry`) calcula otra vez la huella de todos los archivos del plan, no solo de los que reciben los pasos que faltan. Si un paso del plan o la persona editó uno, pide autorizar otra vez, con otra propuesta y otro uso, aunque lo que queda no lo lea; `--uses N` no alcanza para un plan cuyos pasos editan sus entradas. Pasar lo que produce un paso a los siguientes con `step:ID` no tiene este costo.
- `ws continue` acepta lo que corrió («revisado») antes de pedir una autorización nueva: tras una detención de A4, los pasos que ya corrieron quedan revisados por quien continúa y la evaluación final solo lista los que corran después. Lee `coyote ws status` antes de continuar.
- Un plan `autonomous` sin `prd:` solo recibe el aviso R20. Si su paso de `coyote-pm` no lleva `gate: human`, el paso siguiente usa un PRD que nadie revisó antes; la persona lo ve en `coyote review` al autorizar.
- `plan.yaml` no tiene `requires`: cada campo nuevo de 1.x romperá a los binarios 1.0 que no lo conozcan (un coyote anterior falla por `prd:` con el mensaje de yaml.v3, en inglés).
- El marcado de una entrada con forma de orden es un aviso para quien lee, no un filtro: una orden disfrazada pasa, pero su efecto espera a una persona (gate).
- Los planes de v0.1 a v0.4 de este repo se escribieron antes del contrato y no pasan `ws check` (les falta `max_usd`).
