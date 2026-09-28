# Corridas v1 — coyote run, router y cierre con costo

Estado: nuevo en v0.4.0; pasos de workstream en v0.5.0 · Implementación: `internal/runner`, `internal/router`, `coyote run`, `coyote router`, `coyote close` · Decisión: ADR-0012

## coyote run

```sh
coyote run --agent coyote-architect --ws W-0005 --risk R2 "diseña el alta de convenios"
coyote run --agent coyote-reviewer --diff servicios=main...HEAD "revisa el impacto de esta rama"
coyote run --agent coyote-dev --dry-run "…"     # muestra la decisión y la entrada sin correr
```

Corre un paso de un agente con Claude Code en modo headless, en la máquina de la persona y con su sesión de Claude Code:

1. **Router:** elige el modelo y los topes de turnos y de dólares.
2. **Entrada:** la tarea, el paquete de contexto (`get context` con la tarea como consulta) y, con `--diff`, el reporte de impacto en el producto. Va por la entrada estándar.
3. **Claude Code:**

   ```
   claude -p --output-format json --agent A --permission-mode dontAsk
          --model M --max-turns N --max-budget-usd X
          --allowedTools Read,Grep,Glob,Bash [--add-dir <repo>...]
   ```

   `dontAsk` niega lo que no está permitido en lugar de esperar una respuesta que en headless nadie da. Las herramientas permitidas son las de lectura del agente, y Bash pasa por el gate en cada llamada.
4. **Artefacto:** la respuesta final queda en `coyote/workstreams/<W>/runs/<fecha>-<agente>.md` (o `coyote/runs/` sin workstream), con agente, modelo, sesión, turnos, estado y costo.
5. **Ledger:** evento `run` con tokens de entrada, caché y salida, costo, `model:`, `session:`, `turns:`, `doc:` y `cost:estimado`. El estado es:
   - `ok`;
   - `pend`, si la corrida dejó acciones en la cola del gate;
   - `fail`: error, tope de turnos o de dólares, o tiempo vencido;
   - `skip`: no corrió por presupuesto, sin gate o sin el agente instalado.

### Pasos de un workstream

El motor de workstreams (docs/specs/workstream-v1.md) usa este mismo núcleo para cada paso:

- **Entrada:** la tarea lleva el paso y el plan, las entradas del paso entre cercas (artefactos de pasos anteriores, archivos e impacto) y la salida esperada.
- **Evento `run`:** lleva `step:` y `mode:`.
- **Esquema de salida:** si el paso declara `sections`, una entrega sin esos títulos queda en `fail` ("salida incompleta").
- **Acciones en la cola:** un paso que las dejó se retoma con `--resume` y la sesión del artefacto, para que el agente repita las llamadas aprobadas.

Dos corridas en el mismo segundo no pisan su artefacto: la segunda lleva `-2`.

### Qué no hace

- **No corre sin el gate de Claude Code instalado** (`coyote install --ide claude-code`). Sin él, un agente podría correr comandos sin aprobación.
- **No corre dentro de una sesión de agente.** En modo manual la persona lanza cada corrida, y el gate bloquea `coyote run` si lo intenta un agente, también con envoltorios como `env`, `nice`, `timeout` o `sh -c`.
- **No escribe código:** los agentes proponen, y lo que tenga efectos lo aprueba una persona en la cola del gate.

El costo es la estimación de Claude Code, no la factura. Sin tabla de precios, todo el costo queda como entrada (`split:no`).

## Router

`coyote/router.yaml` es opcional; sin él valen estos valores. `coyote router --init` lo escribe:

```yaml
version: 1
levels: [haiku, sonnet, opus]   # de menor a mayor capacidad y costo
default: sonnet
agents: {}                      # agente → modelo; manda sobre el del agente
risk_floor: { R2: sonnet, R3: opus }
budget: { warn_at: 0.8, stop_at: 1.0 }   # sobre budgets.monthly_usd de project.yaml
limits: { max_turns: 30, max_usd_per_run: 2.00 }
# prices: { sonnet: { input: …, output: …, cache_read: …, cache_write: … } }  # USD por millón
```

El orden de la decisión:

1. **Modelo:** `--model`, el de `agents` en el router, el que declara el agente o `default`, en ese orden.
2. **Piso por riesgo:** con `--risk R2` o `R3`, el modelo no queda por debajo de `risk_floor`.
3. **Presupuesto del mes:** es la suma del ledger del proyecto en el mes UTC, sin cierres.
   - Desde `warn_at`, baja un nivel sin cruzar el piso, salvo en R3.
   - Desde `stop_at`, no corre: la persona sube `budgets.monthly_usd`, un archivo que ningún agente puede editar.
4. **Topes:** `--max-turns` o los del agente o del router, y `--max-usd` o el del router, sin pasar de lo que queda del mes.

`coyote router [--risk R]` muestra la decisión para cada agente con el gasto del mes.

## coyote close

```sh
coyote close W-0005
```

Suma del ledger el consumo del workstream:

- totales y porcentaje de caché;
- tokens y costo por modelo y por agente;
- eventos por tipo;
- la lista de corridas con su artefacto.

Con eso llena la sección "Consumo" de `close.md` entre marcadores; lo que la persona escribió fuera de ellos se conserva. Registra un evento `close`, que resume y no se vuelve a sumar en FinOps.
