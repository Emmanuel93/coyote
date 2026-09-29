---
coyote: 1
repo: coyote
updated: 2026-09-29
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
risk|tokens|el estimador local de tokens puede desviarse del real; los eventos run ya traen el uso que reporta Claude Code|ADR-0006
dec|ws|el motor saca el estado del ledger; el modo decide dónde se detiene y el gate aplica igual|ADR-0014
inv|index|el índice se reconstruye desde git; .coyote/ es caché y nunca se versiona|ADR-0007
inv|sync|push y pull usan el git del sistema; coyote nunca guarda credenciales de git|ADR-0008
inv|web|coyote web escucha solo en loopback, valida Host y es de solo lectura|internal/web/web.go
inv|gate|toda acción con efectos de un agente pasa por coyote gate check; leer es libre, lo demás se aprueba|ADR-0009
inv|gate|un agente nunca aprueba, edita el gate ni lee credenciales; el gate falla cerrado|docs/specs/gate-v1.md
inv|approvals|una aprobación vale en la máquina que la firmó, por 24 h y usos contados en el ledger|docs/specs/gate-v1.md
dec|agents|una definición por agente y skill; coyote install la traduce a Claude Code y Cursor sin pisar su configuración|ADR-0010
gap|gate|los bloqueos por texto pueden bloquear un commit que solo menciona rutas del gate o credenciales|docs/specs/gate-v1.md
inv|product|leer un repo del producto nunca lo modifica ni corre sus programas: git de plomería, sin filtros ni transportes|docs/specs/product-v1.md
dec|run|coyote run corre Claude Code headless con el gate, topes de turnos y dólares y evento run en el ledger|ADR-0012
gap|run|el costo de una corrida es la estimación de Claude Code, no la factura; sin precios queda todo como entrada|docs/specs/run-v1.md
gap|product|un tópico elegido por un mapa de configuración o un cliente generado quedan sin enlace en el mapa|docs/specs/product-v1.md
inv|ws|autonomous corre solo en una rama ws/, con tope de plan y la aprobación del plan exacto por su hash|docs/specs/workstream-v1.md
inv|ci|gate pr toma los dueños del CODEOWNERS de la rama base y nunca cuenta al autor del PR|docs/specs/ci-v1.md
gap|ws|retomar una sesión con --resume y --agent en headless no se probó contra Claude Code real|docs/specs/workstream-v1.md
inv|install|Codex, Gemini CLI y Windsurf dejan pasar la herramienta si el hook falta: el lanzador niega con salida 2|docs/specs/install-v1.md
dec|gate|nivel de IDE medido con un canario que el gate siempre niega; nivel 2 o 3 cae en R17|ADR-0015
inv|secrets|un agente no lee ni escribe archivos de secretos ni corre comandos que imprimen credenciales, aun con aprobación|ADR-0016
gap|secrets|el escáner solo reconoce formas conocidas; una contraseña de forma libre se protege declarando su archivo en secrets.files|docs/specs/secrets-v1.md
