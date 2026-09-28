---
coyote: 1
repo: coyote
type: tool
stack: [go]
owners: ["@eramirezhdez"]
standards: { profile: tool }
---
# coyote
purpose|CLI en Go para contexto versionado, gates humanos, estándar por capas y costo trazable de agentes de IA
run|make build && ./bin/coyote help
test|make check
build|make build
entry|cmd/coyote/main.go|arranque de la CLI; delega en internal/cli
mod|cli|comandos, flags y salida para personas|internal/cli|Main
mod|ccf|formato del ledger: parseo, validación y serialización|internal/ccf|Line
mod|ccfdoc|README.coyote.md y CONTEXT.coyote.md: gramática y topes|internal/ccfdoc|Doc
mod|ledger|archivos append-only por día y persona|internal/ledger|Ledger
mod|standards|estándar por capas, overrides y checks del lint|internal/standards|Load, Lint
mod|attribution|detecta y quita atribución a IA|internal/attribution|Config
mod|project|project.yaml, coyote init y hook commit-msg|internal/project|Init
docs|docs/specs|especificaciones CCF, CCF-doc, estándar y atribución
docs|docs/plan/EXECUTION_PLAN.md|plan de ejecución razonado por release y gates
dep|go-yaml|YAML del estándar y frontmatter; copia en third_party
