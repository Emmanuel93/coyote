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
gap|build|sin acceso al proxy de módulos se compila con GOFLAGS=-mod=mod y GOPROXY=off; el Makefile ya lo hace|Makefile
term|ccf|Coyote Compact Format: una línea por evento, once campos separados por barra vertical|docs/specs/ccf-v1.md
inv|index|el índice se reconstruye desde git; .coyote/ es caché y nunca se versiona|ADR-0007
inv|sync|push y pull usan el git del sistema; coyote nunca guarda credenciales de git|ADR-0008
inv|web|coyote web escucha en loopback, valida Host, es de solo lectura y pide la clave de la corrida|ADR-0022
inv|gate|toda acción con efectos de un agente pasa por coyote gate check; leer es libre, lo demás se aprueba|ADR-0009
inv|gate|un agente nunca aprueba, edita el gate ni lee credenciales; el gate falla cerrado|docs/specs/gate-v1.md
inv|approvals|una aprobación vale en la máquina que la firmó, por 24 h y usos contados en el ledger|docs/specs/gate-v1.md
gap|gate|un comando que arma palabras al correr con sustituciones o variables del entorno baja de bloqueo a aprobación|docs/specs/gate-v1.md
inv|product|leer un repo del producto nunca lo modifica ni corre sus programas: git de plomería, sin filtros ni transportes|docs/specs/product-v1.md
dec|run|coyote run corre Claude Code headless con el gate, topes de turnos y dólares y evento run en el ledger|ADR-0012
gap|run|el costo de una corrida es la estimación de Claude Code, no la factura; sin precios queda todo como entrada|docs/specs/run-v1.md
inv|ws|autonomous corre solo en una rama ws/, con tope de plan y la aprobación del plan exacto por su hash|docs/specs/workstream-v1.md
dec|ws|el motor saca el estado del ledger; el modo decide dónde se detiene y el gate aplica igual|ADR-0014
inv|ci|gate pr toma los dueños del CODEOWNERS de la rama base y nunca cuenta al autor del PR|docs/specs/ci-v1.md
inv|install|el lanzador corre el hook con sh y vuelve 2 toda salida distinta de 0: un hook roto niega|docs/specs/install-v1.md
inv|secrets|un agente no lee ni escribe archivos de secretos ni corre comandos que imprimen credenciales, aun con aprobación|ADR-0016
gap|secrets|el escáner solo reconoce formas conocidas; una contraseña de forma libre se protege declarando su archivo en secrets.files|docs/specs/secrets-v1.md
inv|infra|un agente nunca aplica infraestructura; un ambiente reviewed solo lo aplica la persona o un pipeline con revisor|ADR-0017
dec|infra|coyote infra plan lee el plan en JSON sin copiarlo y solo reporta direcciones, tipos y acciones|docs/specs/infra-v1.md
dec|gate|el gate lee cada comando como lo correría el shell; los mensajes, patrones y lo que imprime echo son datos|docs/specs/gate-v1.md
dec|hub|el estándar, los admins y el presupuesto de la organización salen del commit que rige en el clon del hub|ADR-0018
inv|gate|un agente no escribe en el clon del hub ni en una carpeta de git, ni lee su config|docs/specs/gate-v1.md
dec|web|cada persona ve su gasto y los totales; el desglose, los admins; la organización, solo los del hub|ADR-0019
dec|slo|las alertas salen de coyote/slo/<servicio>.yaml; relajar un SLO es R3 y un agente nunca las carga|ADR-0020
dec|ci|gitleaks es opcional en el pipeline, fijado por hash y con la configuración de la rama base|ADR-0021
inv|output|la salida de coyote en terminal y web escapa controles y caracteres invisibles|internal/safetext/safetext.go
inv|context|lo citado de los repos es dato: paquetes y ask lo enmarcan y marcan órdenes para agentes|ADR-0023
inv|agents|coyote note y generate agents rechazan entradas con forma de orden para un agente|ADR-0023
gap|inject|el clasificador marca formas conocidas; una orden disfrazada pasa, pero su efecto espera a una persona|docs/security/threat-model.md
dec|ci|gate pr: instrucciones de agentes y órdenes en documentos son R2; ledger reescrito y Trojan Source, R3|docs/specs/ci-v1.md
dec|audit|coyote audit arma el reporte del mes desde el ledger, las aprobaciones y git; es evidencia, no certificación|ADR-0024
dec|eval|coyote eval mide el contexto con un golden sin modelos; la CI falla si algo baja más de 2 puntos|ADR-0025
