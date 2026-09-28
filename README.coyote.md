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
entry|cmd/coyote/main.go|arranque; delega en internal/cli
mod|cli|comandos y salida|internal/cli|Main
mod|formatos|ledger CCF y documentos CCF-doc|internal/ccf, internal/ccfdoc, internal/ledger|Line, Doc
mod|standards|estándar por capas y lint|internal/standards|Load, Lint
mod|attribution|atribución a IA e identidades de bots|internal/attribution|Config
mod|project|init, repos y hook commit-msg|internal/project|Init, AddRepo
mod|index|índice, BM25 y paquetes de contexto|internal/index|Build, Search, Pack
mod|remoto|ritmo humano, token y API de GitHub|internal/pace, internal/auth, internal/github|Limiter, Token
mod|gate|gate por hash y aprobaciones firmadas|internal/gate, internal/approval|Evaluate, Store
mod|install|agentes, skills y configuración por IDE|agents, skills, internal/agents, internal/install|Plan
docs|docs/specs|especificaciones
docs|docs/plan/EXECUTION_PLAN.md|plan razonado por release
dep|go-yaml|YAML; copia en third_party
