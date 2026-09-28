# Coyote

Coyote es una CLI en Go para trabajar con agentes de IA en proyectos de software sin perder el control: el contexto del proyecto vive versionado junto al código, cada acción queda registrada con su costo, el estándar del equipo se valida solo y ningún entregable sale firmado por una herramienta de IA.

Estado: **v0.3.0** (aprobada en el gate G3). Funciona sin red; para GitHub usa tus propias credenciales. El router de modelos, los modos `supervised` y `autonomous` y el cierre con costo llegan en v0.4; el orden está en [docs/plan/EXECUTION_PLAN.md](docs/plan/EXECUTION_PLAN.md).

## Qué resuelve

- **Contexto que no se pierde.** Cada repo lleva `README.coyote.md` (qué es y cómo se corre) y `CONTEXT.coyote.md` (invariantes, decisiones, trampas) en un formato compacto con tope de tokens. De ahí se genera `AGENTS.md`, que leen Claude Code, Cursor y los demás IDEs.
- **Todo queda registrado.** El ledger (`coyote/ledger/`) guarda una línea por evento: quién, qué, tokens de entrada, caché y salida, y costo. Un archivo por día y persona, así git nunca choca.
- **Un estándar por capas.** El default de la herramienta, el de tu organización y el de cada proyecto se combinan con `extends`. Relajar una regla exige motivo y puede vencer.
- **Nada con efectos sin tu aprobación.** Un gate en el hook de Claude Code y de Cursor deja leer libremente y detiene todo lo demás hasta que apruebas esa acción exacta. Un agente no puede aprobarse, tocar el gate ni leer tus credenciales.
- **Autoría humana.** Los commits llevan tu identidad de git. Las firmas y trailers que agregan los asistentes se quitan en cinco capas: configuración del IDE, gate del IDE, `coyote commit`, hook `commit-msg` y lint en la CI.

## Instalar

Requiere Go 1.22 o superior y git. No descarga dependencias: la única (go-yaml) viene en `third_party/`.

```sh
make install        # instala coyote en $GOPATH/bin
coyote version
```

Para compilar sin instalar: `make build` deja el binario en `bin/coyote`. `make dist` genera binarios de macOS y Linux con sus sumas SHA-256.

## Empezar

```sh
coyote init pedidos-api --type backend --purpose "API de pedidos"
cd pedidos-api
coyote doctor                       # identidad, hook, documentos y estándar
coyote note "un pedido se confirma solo con pago capturado" --type inv --scope pedidos
git add -A
coyote commit -m "feat(pedidos): alta del proyecto"
coyote log                          # eventos con tokens y costo
coyote standards lint               # 0 si cumple las reglas MUST
coyote get context --scope pedidos  # lo que un agente necesita saber, acotado
coyote ask "cuándo se confirma un pedido"
coyote push                         # revisa autoría y estándar, y publica a ritmo humano
coyote web                          # costos en http://127.0.0.1:7410
```

## Trabajar con agentes

```sh
coyote install --ide claude-code    # gate, 14 agentes, skills y atribución apagada (o --ide cursor, all)
coyote doctor --ide claude-code     # prueba el gate en tu máquina
# el agente intenta algo con efectos → queda en la cola
coyote approvals                    # qué está esperando
coyote review P-7q3k9d              # el comando o el diff exacto
coyote approve P-7q3k9d --uses 3    # o --all; el agente repite la llamada y pasa
coyote approve --bash "go test ./..." --uses 20 --for 8h   # aprobar antes de que lo pida
coyote reject P-7q3k9d --reason "usa go mod tidy"          # el agente recibe el motivo
```

Leer, buscar y pedir contexto no piden aprobación. Las aprobaciones valen para esa acción exacta, en tu máquina, por 24 horas como máximo; se dan desde tu terminal, nunca desde el IDE.

`coyote init` nunca sobrescribe: en un repo existente solo agrega lo que falta.

## Comandos

| Comando | Para qué |
|---------|----------|
| `init [nombre]` | crea o adopta un proyecto: documentos, estándar, hook, `.claude/settings.json`, `AGENTS.md` |
| `status [--json]` | estado de documentos, estándar, ledger, gate y aprobaciones |
| `note <texto> --type T` | agrega contexto (`inv`, `dec`, `gap`, `how`, `term`, `risk`, `todo`) y regenera `AGENTS.md` |
| `record <tipo> <qué>` | registra un evento con tokens (`12.4k/8.7k/1.1k`) y costo (`0.009+0.011`) |
| `log` | eventos con filtros por tipo, persona, workstream y fecha, y sus totales |
| `commit -m <mensaje>` | commit con tu autoría, formato R2, documentos válidos y sin atribución a IA |
| `standards lint\|show\|diff\|explain` | valida y explica el estándar por capas |
| `attribution check\|scrub` | busca o quita atribución a herramientas de IA en archivos y commits |
| `gate check` | gate humano para los hooks previos de Claude Code, Cursor, Codex y Copilot; sale con 2 para bloquear |
| `approvals`, `review` | la cola de propuestas y el detalle de cada una |
| `approve`, `reject`, `revoke` | decisiones humanas sobre acciones exactas, desde tu terminal |
| `propose --bash CMD` | encola un comando para aprobarlo |
| `install --ide IDE` | gate, agentes, skills y atribución apagada en Claude Code o Cursor; `--check` para CI |
| `generate agents [--check]` | genera `AGENTS.md` o verifica que esté al día |
| `hooks install` | instala el hook `commit-msg` |
| `doctor [--ide IDE]` | diagnóstico completo del proyecto y, con `--ide`, del gate |
| `get context [repo]` | paquete de contexto acotado (`--scope`, `--query`, `--budget`) con referencias |
| `ask "pregunta"` | busca en el contexto del proyecto o de otro repo, sin llamar a ningún modelo |
| `index [--rebuild]` | arma el índice local y muestra su tamaño |
| `repo add\|list\|fetch` | repos del proyecto; de otros repos se traen solo sus documentos |
| `push` / `pull` | publica y trae con autoría, estándar y ritmo humano revisados |
| `auth login\|status\|logout` | token de GitHub desde el entorno, `gh` o el llavero; nunca en archivos |
| `web` | costos por proyecto, persona, modelo y agente en `127.0.0.1` |

`coyote help <comando>` muestra las opciones. `-C <ruta>` corre cualquier comando sobre otro directorio.

## Cómo se organiza un proyecto

```
README.md                 para personas
README.coyote.md          identidad del repo (CCF-doc)
CONTEXT.coyote.md         contexto vivo (CCF-doc)
AGENTS.md                 generado; lo leen los IDEs
CLAUDE.md                 @AGENTS.md
.coyoteignore             exclusiones de indexado
coyote/
  project.yaml            nombre, tipo, hub, autonomía, presupuesto, ritmo
  standards/rules.yaml    estándar del proyecto (extends)
  ledger/AAAA/MM/         eventos en CCF, un archivo por día y persona
  decisions/              ADRs
  workstreams/            planes y cierres con su costo
  approvals/              aprobaciones humanas, firmadas
  agents/, skills/        agentes y skills propios (opcional)
.claude/, .cursor/        generados por coyote install
.coyote/                  índice y cola del gate; nunca se versiona
```

## Autonomía

`coyote/project.yaml` define el modo: `manual` (toda acción con efectos espera aprobación), `supervised` o `autonomous`. En modo autónomo el trabajo corre dentro de sus topes y la persona que lo autorizó responde por el resultado final. Desde v0.3 el gate aplica `manual`; `supervised` y `autonomous` se aceptan, pero el gate no es más laxo hasta que v0.4 traiga su motor. Lo que hace un agente queda en el ledger con el nombre que reporta el IDE.

## Documentación

- Especificaciones: [CCF v1](docs/specs/ccf-v1.md) (ledger), [CCF-doc v1](docs/specs/ccf-doc-v1.md) (documentos), [estándar v1](docs/specs/standards-v1.md), [atribución v1](docs/specs/attribution-v1.md), [contexto v1](docs/specs/context-v1.md), [remoto v1](docs/specs/remote-v1.md), [gate v1](docs/specs/gate-v1.md), [instalación v1](docs/specs/install-v1.md).
- Estándar default: [standards/default/STANDARD.md](standards/default/STANDARD.md). Agentes y skills: [agents/](agents/), [skills/](skills/).
- Decisiones: [coyote/decisions/](coyote/decisions/).
- Plan y releases: [docs/plan/EXECUTION_PLAN.md](docs/plan/EXECUTION_PLAN.md), [docs/releases/](docs/releases/).
- Ejemplo completo: [examples/acme-shop](examples/acme-shop/) (proyecto sintético).

## Desarrollo

```sh
make check          # vet, pruebas, build y el lint del propio estándar
```

Este repo es un proyecto coyote: tiene sus documentos, su ledger y dos reglas propias (C1 y C2). Los cambios se registran con `coyote commit`.

## Licencia

Repositorio privado, sin licencia: todos los derechos reservados (decisión del gate G1).
