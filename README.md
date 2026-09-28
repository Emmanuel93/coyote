# Coyote

Coyote es una CLI en Go para trabajar con agentes de IA en proyectos de software sin perder el control: el contexto del proyecto vive versionado junto al código, cada acción queda registrada con su costo, el estándar del equipo se valida solo y ningún entregable sale firmado por una herramienta de IA.

Estado: **v0.1.0** (aprobada en el gate G1). Funciona sin red y sin credenciales. Los remotos (GitHub), el índice de contexto, los agentes y la web de costos llegan en las siguientes versiones; el orden está en [docs/plan/EXECUTION_PLAN.md](docs/plan/EXECUTION_PLAN.md).

## Qué resuelve

- **Contexto que no se pierde.** Cada repo lleva `README.coyote.md` (qué es y cómo se corre) y `CONTEXT.coyote.md` (invariantes, decisiones, trampas) en un formato compacto con tope de tokens. De ahí se genera `AGENTS.md`, que leen Claude Code, Cursor y los demás IDEs.
- **Todo queda registrado.** El ledger (`coyote/ledger/`) guarda una línea por evento: quién, qué, tokens de entrada, caché y salida, y costo. Un archivo por día y persona, así git nunca choca.
- **Un estándar por capas.** El default de la herramienta, el de tu organización y el de cada proyecto se combinan con `extends`. Relajar una regla exige motivo y puede vencer.
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
```

`coyote init` nunca sobrescribe: en un repo existente solo agrega lo que falta.

## Comandos

| Comando | Para qué |
|---------|----------|
| `init [nombre]` | crea o adopta un proyecto: documentos, estándar, hook, `.claude/settings.json`, `AGENTS.md` |
| `status [--json]` | estado de documentos, estándar, ledger y aprobaciones |
| `note <texto> --type T` | agrega contexto (`inv`, `dec`, `gap`, `how`, `term`, `risk`, `todo`) y regenera `AGENTS.md` |
| `record <tipo> <qué>` | registra un evento con tokens (`12.4k/8.7k/1.1k`) y costo (`0.009+0.011`) |
| `log` | eventos con filtros por tipo, persona, workstream y fecha, y sus totales |
| `commit -m <mensaje>` | commit con tu autoría, formato R2, documentos válidos y sin atribución a IA |
| `standards lint\|show\|diff\|explain` | valida y explica el estándar por capas |
| `attribution check\|scrub` | busca o quita atribución a herramientas de IA en archivos y commits |
| `gate attribution` | gate para los hooks `PreToolUse` de los IDEs; sale con 2 para bloquear |
| `generate agents [--check]` | genera `AGENTS.md` o verifica que esté al día |
| `hooks install` | instala el hook `commit-msg` |
| `doctor` | diagnóstico completo del proyecto |

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
  approvals/              aprobaciones humanas
```

## Autonomía

`coyote/project.yaml` define el modo: `manual` (toda acción con efectos espera aprobación), `supervised` o `autonomous`. En modo autónomo el trabajo corre dentro de sus topes y la persona que lo autorizó responde por el resultado final. En v0.1 el modo queda registrado y se publica en `AGENTS.md`; el motor que lo aplica llega en v0.3.

## Documentación

- Especificaciones: [CCF v1](docs/specs/ccf-v1.md) (ledger), [CCF-doc v1](docs/specs/ccf-doc-v1.md) (documentos), [estándar v1](docs/specs/standards-v1.md), [atribución v1](docs/specs/attribution-v1.md).
- Estándar default: [standards/default/STANDARD.md](standards/default/STANDARD.md).
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
