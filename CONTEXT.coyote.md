---
coyote: 1
repo: coyote
updated: 2026-09-28
---
# CONTEXT.coyote.md
how|coyote|registra lo aprendido con coyote note y valida con coyote doctor|-
inv|attribution|ningún commit, PR, doc o release lleva atribución a herramientas de IA; el ledger registra a los agentes|ADR-0005
inv|git|toda operación git usa el binario del sistema para respetar identidad, firma y credenciales de la persona|ADR-0002
inv|ledger|el ledger solo agrega líneas; un archivo por día y persona evita conflictos de merge|docs/specs/ccf-v1.md
inv|standards|relajar una regla exige reason, redefinir no baja de nivel y los scripts solo corren con --scripts|docs/specs/standards-v1.md
inv|code|código, pruebas, plantillas, estándar y ejemplos no nombran organizaciones ni repos reales (regla C1)|coyote/standards/rules.yaml
dec|cli|CLI solo con biblioteca estándar; la única dependencia es go-yaml, copiada en third_party|ADR-0004
dec|formats|CCF para el ledger y CCF-doc para README.coyote.md y CONTEXT.coyote.md|ADR-0003
gap|build|sin acceso al proxy de módulos se compila con GOFLAGS=-mod=mod y GOPROXY=off; el Makefile ya lo hace|Makefile
how|release|make dist genera binarios de macOS y Linux con la versión inyectada por ldflags|Makefile
term|ccf|Coyote Compact Format: una línea por evento, once campos separados por barra vertical|docs/specs/ccf-v1.md
risk|tokens|el estimador local de tokens puede desviarse del conteo real; v0.2 lo contrasta con count_tokens|ADR-0006
todo|remote|push, pull y get context con OAuth device flow y cuotas a ritmo humano llegan en v0.2|docs/plan/EXECUTION_PLAN.md
inv|index|el índice se reconstruye desde git; .coyote/ es caché y nunca se versiona|ADR-0007
