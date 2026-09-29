# Coyote

Coyote es una CLI en Go para trabajar con agentes de IA en proyectos de software sin perder el control: el contexto del proyecto vive versionado junto al código, cada acción queda registrada con su costo, el estándar del equipo se valida solo y ningún entregable sale firmado por una herramienta de IA.

Estado: **v0.5.0** (aprobada en el gate G5, en la rama `v0.5`): pipeline de impacto y revisión en cada PR, y motor de workstreams. `main` sigue en v0.4.0 hasta que el ejercicio en ramas termine. Funciona sin red; para GitHub usa tus propias credenciales. El orden está en [docs/plan/EXECUTION_PLAN.md](docs/plan/EXECUTION_PLAN.md).

## Qué resuelve

- **Contexto que no se pierde.** Cada repo lleva `README.coyote.md` (qué es y cómo se corre) y `CONTEXT.coyote.md` (invariantes, decisiones, trampas) en un formato compacto con tope de tokens. De ahí se genera `AGENTS.md`, que leen Claude Code, Cursor y los demás IDEs.
- **Todo queda registrado.** El ledger (`coyote/ledger/`) guarda una línea por evento: quién, qué, tokens de entrada, caché y salida, y costo. Un archivo por día y persona, así git nunca choca.
- **Un estándar por capas.** El default de la herramienta, el de tu organización y el de cada proyecto se combinan con `extends`. Relajar una regla exige motivo y puede vencer.
- **Nada con efectos sin tu aprobación.** Un gate en el hook de Claude Code y de Cursor deja leer libremente y detiene todo lo demás hasta que apruebas esa acción exacta. Un agente no puede aprobarse, tocar el gate ni leer tus credenciales.
- **Un cambio se ve contra todo el producto.** El mapa de interfaces de todos los repos dice a quién afecta un cambio. En cada PR, el pipeline pide la revisión de un dueño cuando el riesgo lo amerita.
- **Autoría humana.** Los commits llevan tu identidad de git. Las firmas y trailers que agregan los asistentes se quitan en cinco capas: configuración del IDE, gate del IDE, `coyote commit`, hook `commit-msg` y lint en la CI.

## Instalar

Requiere Go 1.22 o superior y git. No descarga dependencias: la única (go-yaml) viene en `third_party/`.

```sh
make install        # instala coyote en $GOPATH/bin; make build lo deja en bin/coyote
coyote version
```

`make dist` genera binarios de macOS y Linux con sus sumas SHA-256.

## Empezar

```sh
coyote init pedidos-api --type backend --purpose "API de pedidos"
cd pedidos-api
coyote doctor                       # identidad, hook, documentos y estándar
coyote note "un pedido se confirma solo con pago capturado" --type inv --scope pedidos
git add -A && coyote commit -m "feat(pedidos): alta del proyecto"
coyote log                          # eventos con tokens y costo
coyote get context --scope pedidos  # lo que un agente necesita saber, acotado
coyote ask "cuándo se confirma un pedido"
coyote push                         # revisa autoría y estándar, y publica a ritmo humano
coyote web                          # costos en http://127.0.0.1:7410
```

`coyote init` nunca sobrescribe: en un repo existente solo agrega lo que falta.

## Trabajar con agentes

```sh
coyote install --ide claude-code    # gate, agentes, skills y atribución apagada (o cursor, codex, gemini, copilot, windsurf, all)
coyote doctor --ide codex --canary  # mide si el IDE pasa por el gate: nivel 1, 2 o 3
coyote doctor --ide codex           # prueba el gate en tu máquina y muestra el nivel medido
coyote approvals                    # lo que un agente dejó esperando
coyote review P-7q3k9d              # el comando o el diff exacto
coyote approve P-7q3k9d --uses 3    # el agente repite la llamada y pasa
coyote reject P-7q3k9d --reason "usa go mod tidy"          # el agente recibe el motivo
```

Leer, buscar y pedir contexto no piden aprobación. Una aprobación vale para esa acción exacta, en tu máquina, por 24 horas como máximo, y se da desde tu terminal, nunca desde el IDE.

Un IDE puede tener hooks y aun así no pasar por el gate: carpeta sin confianza, modo restringido o hooks apagados. Por eso el nivel de cada IDE se mide con un canario, un comando que el gate niega siempre (docs/specs/install-v1.md).

## Un producto en varios repos

```sh
coyote init mi-producto --type product
coyote repo add servicios --path ../servicios    # los repos se leen, nunca se escriben
coyote map                                       # qué expone y qué consume cada módulo
coyote impact --diff servicios=main...HEAD --format md   # a quién afecta la rama, en todos los repos
coyote extract                                   # propone README.coyote.md y CONTEXT.coyote.md de cada repo
coyote install --ci github --policy fail         # el pipeline de cada repo: impacto, riesgo y revisión
```

En cada PR, `coyote gate pr` calcula el riesgo por rutas y por impacto. Un cambio R2 o R3 espera la aprobación de un dueño, según el CODEOWNERS de la rama base.

## Correr agentes y planes con costo a la vista

```sh
coyote router                       # modelo y topes de cada agente con el gasto del mes
coyote run --agent coyote-architect --ws W-0005 --risk R2 "diseña el alta de convenios"
coyote ws check W-0007              # el contrato de cada paso del plan
coyote ws run W-0007                # corre el plan y se detiene donde el modo lo pide
coyote ws continue W-0007           # aceptas lo que corrió y sigue; --redo "qué cambiar" lo repite
coyote close W-0007                 # consumo real del workstream en close.md
```

Cada corrida tiene topes de turnos y de dólares. El router baja de modelo al 80 % del presupuesto mensual y no corre al 100 %. Cada paso queda en el ledger con tokens, modelo y costo.

## Comandos

| Comando | Para qué |
|---------|----------|
| `init`, `status`, `doctor [--ide IDE [--canary]]` | crea o adopta un proyecto, su estado y su diagnóstico, con el nivel medido de cada IDE |
| `note`, `record`, `log` | contexto (`inv`, `dec`, `gap`, `how`, `term`, `risk`, `todo`), eventos y el ledger con sus totales |
| `commit -m`, `hooks install` | commits con tu autoría, formato R2 y sin atribución a IA |
| `standards lint\|show\|diff\|explain`, `attribution check\|scrub` | el estándar por capas y la atribución a IA |
| `generate agents [--check]` | genera `AGENTS.md` o verifica que esté al día |
| `install --ide IDE \| --ci github` | el gate, los agentes y las skills en Claude Code, Cursor, Codex, Gemini CLI, Copilot o Windsurf; o el pipeline de cada repo del producto |
| `gate check`, `gate pr` | el gate de los hooks del IDE; el de los PR con la revisión de un dueño |
| `approvals`, `review`, `approve`, `reject`, `revoke`, `propose` | la cola y las decisiones humanas sobre acciones exactas |
| `get context`, `ask`, `index` | contexto acotado con referencias, sin llamar a ningún modelo |
| `repo add\|list\|fetch` | repos del proyecto; de otros repos se traen solo sus documentos |
| `map`, `impact`, `extract`, `ci impact` | mapa de interfaces del producto, impacto de un cambio y documentos propuestos |
| `run --agent A "tarea"`, `router` | un paso de un agente con Claude Code, con topes, gate y costo |
| `ws check\|status\|run\|continue` | el plan de un workstream con puntos de control |
| `close <W>` | consumo de un workstream desde el ledger, con sus pasos |
| `push`, `pull`, `auth` | publica y trae con autoría y ritmo humano; el token de GitHub vive en el llavero |
| `web` | costos por proyecto, persona, modelo y agente en `127.0.0.1` |

`coyote help <comando>` muestra las opciones. `-C <ruta>` corre cualquier comando sobre otro directorio.

## Cómo se organiza un proyecto

```
README.md, README.coyote.md, CONTEXT.coyote.md   para personas; identidad y contexto vivo (CCF-doc)
AGENTS.md, CLAUDE.md      generados; los leen los IDEs
.coyoteignore             exclusiones de indexado
coyote/
  project.yaml            nombre, tipo, autonomía, presupuesto, ritmo, repos y reglas de riesgo
  standards/rules.yaml    estándar del proyecto (extends)
  ledger/AAAA/MM/         eventos en CCF, un archivo por día y persona
  decisions/              ADRs
  workstreams/            planes, corridas (runs/) y cierres con su costo
  router.yaml             modelo por agente, pisos por riesgo y topes (opcional)
  map/, repos/, ci/       en un producto: mapa, documentos propuestos y workflows por repo
  approvals/              aprobaciones humanas, firmadas
.claude/, .cursor/, …     generados por coyote install, según el IDE (.codex/, .gemini/, .github/hooks/, .windsurf/)
.coyote/                  índice, cola y latido del gate, y locks; nunca se versiona
```

## Autonomía

`coyote/project.yaml` fija el techo: `manual`, `supervised` o `autonomous`. El modo decide cuándo se detiene el motor de workstreams, nunca lo que el gate deja pasar: toda acción con efectos se aprueba en los tres modos. `autonomous` corre en una rama `ws/<W>`, dentro de los topes del plan y con tu aprobación de ese plan exacto; al final lo evalúa quien lo autorizó.

## Documentación

- Especificaciones: [CCF](docs/specs/ccf-v1.md), [CCF-doc](docs/specs/ccf-doc-v1.md), [estándar](docs/specs/standards-v1.md), [atribución](docs/specs/attribution-v1.md), [contexto](docs/specs/context-v1.md), [remoto](docs/specs/remote-v1.md), [gate](docs/specs/gate-v1.md), [instalación](docs/specs/install-v1.md), [producto](docs/specs/product-v1.md), [corridas](docs/specs/run-v1.md), [workstreams](docs/specs/workstream-v1.md), [pipeline](docs/specs/ci-v1.md).
- Estándar default: [standards/default/STANDARD.md](standards/default/STANDARD.md). Agentes y skills: [agents/](agents/), [skills/](skills/).
- Decisiones: [coyote/decisions/](coyote/decisions/). Plan y releases: [docs/plan/](docs/plan/EXECUTION_PLAN.md), [docs/releases/](docs/releases/).
- Ejemplo completo: [examples/acme-shop](examples/acme-shop/) (proyecto sintético).

## Desarrollo

```sh
make check          # vet, pruebas, build y el lint del propio estándar
```

Este repo es un proyecto coyote: tiene sus documentos, su ledger y dos reglas propias (C1 y C2). Los cambios se registran con `coyote commit`.

## Licencia

Repositorio privado, sin licencia: todos los derechos reservados (decisión del gate G1).
