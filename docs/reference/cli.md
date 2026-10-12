# Referencia de comandos

Generada desde la CLI: no se edita a mano. La regenera `COYOTE_UPDATE_DOCS=1 go test ./internal/cli -run TestReferenciaDeComandos`, y `make check` falla si quedó vieja.

`coyote [-C ruta] <comando> [opciones]`. `-C` corre el comando sobre otra carpeta. Los códigos de salida están en [compatibilidad](compatibilidad.md).

| Comando | Uso | Para qué |
| --- | --- | --- |
| [`init`](#coyote-init) | `[nombre] [--type T] [--purpose TEXTO] [--hub RUTA]` | crea o adopta un proyecto coyote |
| [`status`](#coyote-status) | `[--json]` | estado: documentos, estándar, ledger, git |
| [`note`](#coyote-note) | `<texto> --type T [--scope S] [--ref R] [--agent A]` | agrega una entrada a CONTEXT.coyote.md |
| [`record`](#coyote-record) | `<type> <qué> [--scope S] [--tokens e/c/s] [--cost e+s]` | escribe un evento en el ledger |
| [`log`](#coyote-log) | `[--limit N] [--type T] [--user U] [--since 7d]` | muestra el ledger con totales |
| [`audit`](#coyote-audit) | `[--month AAAA-MM] [--json] [--write]` | reporte de auditoría desde el ledger, las aprobaciones y git: eventos, costo, integridad y controles (ADR-0024) |
| [`commit`](#coyote-commit) | `-m <mensaje> [-a] [--ws W]` | commit con tu autoría, formato R2 y sin atribución de IA |
| [`standards`](#coyote-standards) | `lint \| show \| explain <id> \| diff` | estándar por capas y su validación |
| [`hub`](#coyote-hub) | `init [carpeta] [--org NOMBRE] \| status [--json]` | hub de la organización: estándar, admins, presupuesto y proyectos, leído en un commit (ADR-0018) |
| [`attribution`](#coyote-attribution) | `check [rutas] \| scrub [archivo]` | detecta o quita atribución a herramientas de IA |
| [`gate`](#coyote-gate) | `check [--ide IDE] < hook.json` | gate humano para los hooks previos de los IDEs (ADR-0009) |
| [`secrets`](#coyote-secrets) | `list \| scan [--staged \| --range base...head]` | archivos de secretos y sus nombres, sin valores; secretos escritos en el repo (ADR-0016) |
| [`infra`](#coyote-infra) | `check \| plan <plan.json> \| propose` | inventario de la infraestructura (R13), su revisión y el plan de Terraform (ADR-0017) |
| [`slo`](#coyote-slo) | `check [--json] \| rules [servicio] [--check] [--stdout]` | SLOs como código: valida coyote/slo y genera sus alertas de Prometheus (R19, ADR-0020) |
| [`approvals`](#coyote-approvals) | `[--all] [--json]` | cola de propuestas; con --all, también las aprobaciones y sus usos |
| [`review`](#coyote-review) | `[id...]` | muestra el comando o el diff que se aprobaría |
| [`approve`](#coyote-approve) | `<id>... \| --all \| --bash CMD [--uses N] [--for 1h]` | aprueba acciones exactas (solo una persona, en su terminal) |
| [`reject`](#coyote-reject) | `<id>... --reason TEXTO` | rechaza una propuesta; el agente verá el motivo |
| [`revoke`](#coyote-revoke) | `<id> --reason TEXTO` | revoca una aprobación vigente |
| [`propose`](#coyote-propose) | `--bash CMD [--cwd DIR]` | encola un comando para que la persona lo apruebe |
| [`get`](#coyote-get) | `context [repo] [--scope S] [--query Q] [--budget N]` | paquete de contexto acotado para un agente |
| [`ask`](#coyote-ask) | `"pregunta" [--scope S] [--repo R]` | busca en el contexto del proyecto con referencias |
| [`index`](#coyote-index) | `[--rebuild]` | arma el índice local y muestra su tamaño |
| [`eval`](#coyote-eval) | `[--golden G] [--baseline B] [--update-baseline] [-v]` | mide el contexto con el golden del proyecto, sin modelos; falla si empeora (ADR-0025) |
| [`repo`](#coyote-repo) | `add <nombre> [url] [--path P] \| list \| fetch [nombre]` | repos del proyecto y sus documentos; add escribe coyote/project.yaml y lo hace una persona, en su terminal |
| [`map`](#coyote-map) | `[--check] [--json]` | mapa de interfaces del producto: quién expone y quién usa cada endpoint y tópico |
| [`extract`](#coyote-extract) | `[repo...] [--stdout] [--check]` | propone README.coyote.md y CONTEXT.coyote.md de cada repo desde su código |
| [`impact`](#coyote-impact) | `<endpoint\|texto> \| --topic T \| --diff repo=RANGO [--format md]` | qué rompe un cambio en los repos del producto |
| [`run`](#coyote-run) | `--agent A "tarea" [--ws W] [--risk R] [--diff repo=RANGO] [--dry-run]` | corre un paso de un agente con Claude Code, con topes, gate y costo |
| [`ws`](#coyote-ws) | `check\|status\|run\|continue <W> [--redo TEXTO \| --retry]` | corre el plan de un workstream con puntos de control; continue lo hace una persona, en su terminal (ADR-0014) |
| [`ci`](#coyote-ci) | `impact --repo nombre=ruta... [--policy warn\|fail] [--comment]` | impacto de un PR en los repos del producto, dentro del pipeline |
| [`router`](#coyote-router) | `[--init] [--risk R]` | modelo y topes que el router da a cada agente |
| [`close`](#coyote-close) | `<W> [--dry-run]` | cierra un workstream con su consumo real desde el ledger |
| [`push`](#coyote-push) | `[--remote origin] [--agent A] [--dry-run]` | publica con autoría, estándar y ritmo humano revisados |
| [`pull`](#coyote-pull) | `[--remote origin]` | trae cambios y resume el contexto nuevo |
| [`auth`](#coyote-auth) | `login \| status [--check] \| logout` | token de GitHub en el llavero, nunca en archivos |
| [`web`](#coyote-web) | `[--addr 127.0.0.1:7410]` | costos, presupuesto, workstreams, gate y SLOs en el navegador, con la visibilidad de D10 (ADR-0019) |
| [`generate`](#coyote-generate) | `agents [--check]` | genera AGENTS.md desde los documentos coyote |
| [`install`](#coyote-install) | `--ide IDE\|all \| --ci github [--check]` | instala el gate, los agentes y las skills en el IDE, o el pipeline del producto; escribir lo hace una persona, en su terminal |
| [`hooks`](#coyote-hooks) | `install [--force]` | instala el hook commit-msg |
| [`doctor`](#coyote-doctor) | `[--ide IDE [--canary]]` | verifica herramienta, identidad, documentos, estándar, gate y el nivel medido de cada IDE |
| [`version`](#coyote-version) | `` | muestra la versión |

## coyote init

crea o adopta un proyecto coyote

```
uso: coyote init [nombre] [--type T] [--purpose TEXTO] [--hub RUTA]
  -hub string
    	ruta del clon del hub de la organización (ADR-0018)
  -no-claude
    	no crea .claude/settings.json
  -no-git
    	no inicializa git
  -no-hooks
    	no instala el hook commit-msg
  -purpose string
    	qué hace el repo, en una línea
  -type string
    	tipo: backend, mobile, web, infra, hub, product, tool, library, docs, data, other (default "other")
```

## coyote status

estado: documentos, estándar, ledger, git

```
uso: coyote status [--json]
  -json
    	salida JSON
```

## coyote note

agrega una entrada a CONTEXT.coyote.md

```
uso: coyote note <texto> --type T [--scope S] [--ref R] [--agent A]
  -agent string
    	agente que registra la nota a nombre de la persona
  -file string
    	archivo de contexto (default "CONTEXT.coyote.md")
  -ref string
    	referencia: ruta#L, ADR o workstream (default "-")
  -scope string
    	ámbito: módulo o tema, sin espacios (default "general")
  -type string
    	tipo: dec, gap, how, inv, risk, term, todo
  -ws string
    	workstream (default "-")
```

## coyote record

escribe un evento en el ledger

```
uso: coyote record <type> <qué> [opciones]
  -agent string
    	agente que actuó a nombre de la persona
  -cost string
    	USD entrada+salida, p. ej. 0.009+0.011
  -refs string
    	referencias clave:valor separadas por espacios
  -repo string
    	repo (por defecto, el nombre del proyecto)
  -scope string
    	ámbito (default "-")
  -status string
    	ok, pend, fail o skip (default "ok")
  -tokens string
    	tokens entrada/caché/salida, p. ej. 12.4k/8.7k/1.1k
  -ts string
    	marca de tiempo RFC 3339 (por defecto, ahora)
  -ws string
    	workstream (W-0001) o - (default "-")
```

## coyote log

muestra el ledger con totales

```
uso: coyote log [--limit N] [--type T] [--user U] [--ws W] [--since 7d] [--raw]
  -limit int
    	máximo de eventos (default 20)
  -raw
    	imprime las líneas CCF tal cual
  -since string
    	desde: 7d, 24h o 2026-09-01
  -type string
    	filtra por tipo
  -user string
    	filtra por persona (sin @)
  -ws string
    	filtra por workstream
```

## coyote audit

reporte de auditoría desde el ledger, las aprobaciones y git: eventos, costo, integridad y controles (ADR-0024)

```
uso: coyote audit [--month AAAA-MM] [--json] [--write]
  -json
    	salida en JSON
  -month string
    	mes a auditar, AAAA-MM; por defecto, el actual (UTC)
  -write
    	deja el reporte en coyote/audit/AAAA-MM.md para revisarlo y hacerle commit
```

## coyote commit

commit con tu autoría, formato R2 y sin atribución de IA

```
uso: coyote commit -m <mensaje> [-a] [--ws W] [--agent A]
  -a	incluye cambios de archivos versionados, como git commit -a
  -agent string
    	agente que preparó el cambio; queda en el ledger, no en el commit
  -dry-run
    	muestra el mensaje limpio y los eventos sin hacer commit
  -m value
    	mensaje; repetible como en git
  -no-verify
    	omite R2 y R14 (la limpieza de atribución y la revisión de autoría no se omiten)
  -ws string
    	workstream (default "-")
```

## coyote standards

estándar por capas y su validación

### coyote standards lint

```
uso: coyote standards lint [--strict] [--json] [--scripts]
  -json
    	salida JSON
  -scripts
    	corre los checks script de rules.yaml (ejecutan comandos del repo o del hub)
  -strict
    	también falla con hallazgos SHOULD
```

### coyote standards show

```
uso: coyote standards show
```

### coyote standards explain

```
uso: coyote standards explain <id>
```

### coyote standards diff

```
uso: coyote standards diff
```

## coyote hub

hub de la organización: estándar, admins, presupuesto y proyectos, leído en un commit (ADR-0018)

### coyote hub init

```
uso: coyote hub init [carpeta] [--org NOMBRE]
  -org string
    	nombre de la organización; por defecto, el de la carpeta
```

### coyote hub status

```
uso: coyote hub status [--json]
  -json
    	salida en JSON
```

## coyote attribution

detecta o quita atribución a herramientas de IA

### coyote attribution check

```
uso: coyote attribution check [rutas] [--commits N] [--stdin]
  -commits int
    	revisa también los últimos N commits desde que se adoptó coyote
  -stdin
    	revisa el texto de la entrada estándar
```

### coyote attribution scrub

```
uso: coyote attribution scrub [archivo] [--in-place] [--commit-msg]
  -commit-msg
    	modo hook commit-msg: respeta comentarios y tijeras de git, detiene el commit ante frases o autoría de herramientas
  -in-place
    	reescribe el archivo
```

## coyote gate

gate humano para los hooks previos de los IDEs (ADR-0009)

### coyote gate check

```
uso: coyote gate check [--ide claude-code|cursor|codex|copilot|gemini|windsurf|devin|junie] < hook.json
  -ide string
    	IDE para el que se instaló el hook (por defecto se deduce de la entrada)
```

### coyote gate pr

```
uso: coyote gate pr --repo nombre=ruta... [--self nombre] [--risk R3=patrón]... [--policy warn|fail] [--comment]
  -base string
    	commit base; manda sobre el evento
  -comment
    	crea o actualiza el comentario de coyote en el PR (usa GITHUB_TOKEN)
  -event string
    	evento del PR de GitHub Actions
  -gitleaks string
    	reporte JSON de gitleaks sobre los commits del PR (ADR-0021)
  -gitleaks-net string
    	reporte JSON de gitleaks sobre el diff neto del PR
  -head string
    	commit del PR; manda sobre el evento
  -plan string
    	plan de Terraform en JSON (terraform show -json) que generó el pipeline
  -policy string
    	warn: reporta el riesgo y a quién le toca revisar; fail: el chequeo falla hasta que aprueba un dueño (default "warn")
  -repo value
    	repo del producto: nombre=ruta; se repite, uno por repo
  -risk value
    	regla de riesgo por rutas del proyecto: R2=patrón o R3=patrón; se repite
  -self string
    	repo del PR (por defecto, el de GITHUB_REPOSITORY)
  -summary string
    	archivo del resumen del job
```

## coyote secrets

archivos de secretos y sus nombres, sin valores; secretos escritos en el repo (ADR-0016)

### coyote secrets list

```
uso: coyote secrets list [--json] [--no-names]
  -json
    	salida en JSON
  -no-names
    	solo rutas y tipos: no abre los archivos
```

### coyote secrets scan

```
uso: coyote secrets scan [--staged | --range base...head] [--json]
  -hook
    	modo del hook commit-msg: solo avisa si encuentra algo
  -json
    	salida en JSON
  -range string
    	revisa lo que agrega un rango de git (base...head)
  -staged
    	revisa lo preparado para el commit
```

## coyote infra

inventario de la infraestructura (R13), su revisión y el plan de Terraform (ADR-0017)

### coyote infra check

```
uso: coyote infra check [--file inventario] [--json]
  -file string
    	inventario a revisar (por defecto, coyote/infra.yaml del repo)
  -json
    	salida en JSON
```

### coyote infra plan

```
uso: coyote infra plan <plan.json> [--format text|md|json]
  -format string
    	text, md (para un PR) o json (default "text")
```

### coyote infra propose

```
uso: coyote infra propose
```

## coyote slo

SLOs como código: valida coyote/slo y genera sus alertas de Prometheus (R19, ADR-0020)

### coyote slo check

```
uso: coyote slo check [--json]
  -json
    	salida en JSON
```

### coyote slo rules

```
uso: coyote slo rules [servicio] [--check] [--stdout]
  -check
    	falla si las reglas no están al día, sin escribir (para CI)
  -stdout
    	imprime las reglas de un servicio en vez de escribirlas
```

## coyote approvals

cola de propuestas; con --all, también las aprobaciones y sus usos

```
uso: coyote approvals [--all] [--json]
  -all
    	incluye las aprobaciones vigentes, agotadas, vencidas y revocadas
  -json
    	salida en JSON
```

## coyote review

muestra el comando o el diff que se aprobaría

```
uso: coyote review [id...]
```

## coyote approve

aprueba acciones exactas (solo una persona, en su terminal)

```
uso: coyote approve <id>... | --all | --bash COMANDO [--cwd DIR] [--uses N] [--for 1h] [--ws W]
  -all
    	aprueba todas las propuestas pendientes
  -bash string
    	aprueba este comando antes de que el agente lo pida
  -cwd string
    	carpeta del comando, relativa al proyecto (con --bash) (default ".")
  -for duration
    	vigencia (máximo 24h) (default 1h0m0s)
  -uses int
    	veces que se puede usar (1 a 100) (default 1)
  -ws string
    	workstream al que pertenece
```

## coyote reject

rechaza una propuesta; el agente verá el motivo

```
uso: coyote reject <id>... --reason TEXTO
  -reason string
    	motivo que verá el agente (obligatorio)
```

## coyote revoke

revoca una aprobación vigente

```
uso: coyote revoke <id> --reason TEXTO
  -reason string
    	motivo (obligatorio)
```

## coyote propose

encola un comando para que la persona lo apruebe

```
uso: coyote propose --bash COMANDO [--cwd DIR]
  -agent string
    	agente que lo pide
  -bash string
    	comando que se quiere correr
  -cwd string
    	carpeta del comando, relativa al proyecto (default ".")
```

## coyote get

paquete de contexto acotado para un agente

### coyote get context

```
uso: coyote get context [repo] [--scope S] [--query Q] [--budget N] [--format md|ccf]
  -budget int
    	tope de tokens del paquete (default 2000)
  -events int
    	eventos recientes del ledger (default 8)
  -format string
    	md para personas y agentes, ccf compacto (default "md")
  -query string
    	consulta libre para ordenar lo relevante
  -scope string
    	ámbito: módulo o tema, p. ej. pagos
```

## coyote ask

busca en el contexto del proyecto con referencias

```
uso: coyote ask "pregunta" [--limit N] [--kind context,adr,doc,event] [--scope S] [--json] [--record]
  -agent string
    	agente que pregunta, con --record
  -json
    	salida JSON
  -kind string
    	clases separadas por coma: readme, context, adr, doc, workstream, event
  -limit int
    	máximo de resultados (default 5)
  -record
    	registra la pregunta en el ledger (tipo ask)
  -repo string
    	pregunta a otro repo del proyecto
  -scope string
    	solo este ámbito
  -ws string
    	workstream, con --record (default "-")
```

## coyote index

arma el índice local y muestra su tamaño

```
uso: coyote index [--rebuild] [--json]
  -json
    	salida JSON
  -rebuild
    	descarta la caché y vuelve a leer todo
```

## coyote eval

mide el contexto con el golden del proyecto, sin modelos; falla si empeora (ADR-0025)

```
uso: coyote eval [--golden evals/golden.v1.jsonl] [--baseline evals/baseline.json] [--update-baseline] [--budget N] [--json] [-v]
  -baseline string
    	línea base versionada (default "evals/baseline.json")
  -budget int
    	presupuesto del paquete de contexto, como en get context (default 2000)
  -golden string
    	preguntas con sus fuentes esperadas (JSONL) (default "evals/golden.v1.jsonl")
  -json
    	salida en JSON
  -update-baseline
    	reescribe la línea base con la medida actual; el cambio se revisa en el PR
  -v	muestra cada pregunta que no salió completa
```

## coyote repo

repos del proyecto y sus documentos; add escribe coyote/project.yaml y lo hace una persona, en su terminal

### coyote repo add

```
uso: coyote repo add <nombre> [url] [--path P] [--branch B]
  -branch string
    	rama de la que se leen los documentos (por defecto, la principal)
  -path string
    	copia local del repo, relativa al proyecto o absoluta; coyote solo la lee
```

### coyote repo list

```
uso: coyote repo list
```

### coyote repo fetch

```
uso: coyote repo fetch [nombre]
```

## coyote map

mapa de interfaces del producto: quién expone y quién usa cada endpoint y tópico

```
uso: coyote map [--check] [--json] [--top N]
  -ambiguous
    	lista las llamadas que coinciden igual con más de un servicio
  -check
    	falla si el mapa guardado no refleja el código (para la CI); no escribe
  -json
    	salida JSON
  -top int
    	grupos de llamadas sin proveedor a mostrar (default 10)
```

## coyote extract

propone README.coyote.md y CONTEXT.coyote.md de cada repo desde su código

```
uso: coyote extract [repo...] [--stdout] [--check] [--force]
  -check
    	falla si alguna propuesta no está al día; no escribe
  -force
    	reemplaza también las propuestas que la persona editó
  -stdout
    	muestra las propuestas sin escribirlas
```

## coyote impact

qué rompe un cambio en los repos del producto

```
uso: coyote impact <endpoint|texto> | --topic T | --diff [repo=]RANGO | --files repo:ruta [--format text|md|json] [--record]
  -agent string
    	agente, con --record
  -diff value
    	cambios de un repo: repo=RANGO (p. ej. servicios=main...HEAD); se repite para un cambio coordinado
  -files value
    	archivos cambiados: repo:ruta; se repite
  -format string
    	text, md (para un PR) o json (default "text")
  -fresh
    	lee los repos en lugar del mapa guardado
  -json
    	igual que --format json
  -record
    	registra el análisis en el ledger (tipo rev)
  -repo string
    	repo de un --diff o --files sin prefijo
  -topic string
    	tópico de eventos
  -ws string
    	workstream, con --record (default "-")
```

## coyote run

corre un paso de un agente con Claude Code, con topes, gate y costo

```
uso: coyote run --agent A "tarea" [--ws W] [--risk R1|R2|R3] [--scope S] [--diff repo=RANGO] [--model M] [--max-turns N] [--max-usd X] [--dry-run]
  -agent string
    	agente que corre el paso (coyote-*)
  -context int
    	tope de tokens del paquete de contexto (default 3000)
  -diff value
    	cambio que el agente revisa, con su impacto en el producto: repo=RANGO; se repite
  -dry-run
    	muestra la decisión del router, la línea de Claude Code y la entrada, sin correr
  -max-turns int
    	tope de turnos; por defecto el del agente o el del router
  -max-usd float
    	tope en dólares de la corrida; por defecto el del router
  -model string
    	modelo; manda sobre el router (el piso por riesgo se respeta)
  -risk string
    	riesgo de la tarea: R1, R2 o R3; sube el piso de modelo (default "R1")
  -scope string
    	ámbito del paquete de contexto
  -timeout duration
    	tiempo máximo de la corrida (default 30m0s)
  -ws string
    	workstream (W-0001): el artefacto queda en su carpeta
```

## coyote ws

corre el plan de un workstream con puntos de control; continue lo hace una persona, en su terminal (ADR-0014)

### coyote ws check

```
uso: coyote ws check <W> [--json]
  -json
    	salida en JSON
```

### coyote ws status

```
uso: coyote ws status [W] [--json]
  -json
    	salida en JSON
```

### coyote ws run

```
uso: coyote ws run <W> [--mode manual|supervised] [--dry-run]
  -dry-run
    	muestra el paso que correría, con la decisión del router y la entrada, sin correr
  -mode string
    	corre con menos autonomía que la del plan: manual o supervised
```

### coyote ws continue

```
uso: coyote ws continue <W> [--redo "qué cambiar" | --retry] [--step ID] [--note TEXTO] [--mode manual|supervised]
  -mode string
    	sigue con menos autonomía que la del plan: manual o supervised
  -note string
    	nota breve de tu revisión; queda en el ledger
  -redo string
    	pide rehacer el paso: lo que hay que cambiar
  -retry
    	repite un paso que falló o que dejó acciones en la cola, sin retomar su sesión
  -step string
    	paso al que se aplican --redo o --retry; por defecto, donde se detuvo el motor
```

## coyote ci

impacto de un PR en los repos del producto, dentro del pipeline

### coyote ci impact

```
uso: coyote ci impact --repo nombre=ruta... [--self nombre] [--base SHA --head SHA] [--policy warn|fail] [--comment]
  -base string
    	commit base; manda sobre el evento
  -comment
    	crea o actualiza el comentario de coyote en el PR (usa GITHUB_TOKEN)
  -event string
    	evento del PR de GitHub Actions
  -head string
    	commit del PR; manda sobre el evento
  -policy string
    	warn: solo reporta; fail: el chequeo falla si el cambio rompe a otro módulo (default "warn")
  -repo value
    	repo del producto: nombre=ruta; se repite, uno por repo
  -self string
    	repo del PR (por defecto, el de GITHUB_REPOSITORY)
  -summary string
    	archivo del resumen del job
```

## coyote router

modelo y topes que el router da a cada agente

```
uso: coyote router [--init] [--risk R1|R2|R3]
  -init
    	escribe coyote/router.yaml con los valores por defecto
  -risk string
    	riesgo con el que se muestran las decisiones (default "R1")
```

## coyote close

cierra un workstream con su consumo real desde el ledger

```
uso: coyote close <W> [--dry-run]
  -dry-run
    	muestra el resumen sin escribir close.md ni el ledger
```

## coyote push

publica con autoría, estándar y ritmo humano revisados

```
uso: coyote push [--remote origin] [--agent A] [--dry-run]
  -agent string
    	agente que publica a nombre de la persona
  -dry-run
    	revisa y muestra qué se publicaría, sin publicar
  -no-verify
    	omite el lint (solo una persona; queda en el ledger)
  -pace string
    	perfil de ritmo: human o batch (por defecto, el de project.yaml)
  -remote string
    	remoto de git (default "origin")
```

## coyote pull

trae cambios y resume el contexto nuevo

```
uso: coyote pull [--remote origin]
  -remote string
    	remoto de git (default "origin")
```

## coyote auth

token de GitHub en el llavero, nunca en archivos

### coyote auth login

```
uso: coyote auth login [--host github.com]
  -check
    	verifica el token contra la API (una lectura)
  -client-id string
    	client ID de la OAuth App (device flow)
  -device
    	OAuth device flow con la OAuth App de la organización
  -host string
    	host de GitHub (default "github.com")
  -with-token
    	lee el token de la entrada estándar y lo guarda en el llavero
```

### coyote auth status

```
uso: coyote auth status [--host github.com]
  -check
    	verifica el token contra la API (una lectura)
  -client-id string
    	client ID de la OAuth App (device flow)
  -device
    	OAuth device flow con la OAuth App de la organización
  -host string
    	host de GitHub (default "github.com")
  -with-token
    	lee el token de la entrada estándar y lo guarda en el llavero
```

### coyote auth logout

```
uso: coyote auth logout [--host github.com]
  -check
    	verifica el token contra la API (una lectura)
  -client-id string
    	client ID de la OAuth App (device flow)
  -device
    	OAuth device flow con la OAuth App de la organización
  -host string
    	host de GitHub (default "github.com")
  -with-token
    	lee el token de la entrada estándar y lo guarda en el llavero
```

## coyote web

costos, presupuesto, workstreams, gate y SLOs en el navegador, con la visibilidad de D10 (ADR-0019)

```
uso: coyote web [--addr 127.0.0.1:7410]
  -addr string
    	dirección de escucha; solo loopback (default "127.0.0.1:7410")
```

## coyote generate

genera AGENTS.md desde los documentos coyote

### coyote generate agents

```
uso: coyote generate agents [--check] [--force]
  -check
    	falla si AGENTS.md no está vigente, sin escribir
  -force
    	reemplaza un AGENTS.md que no generó coyote
```

## coyote install

instala el gate, los agentes y las skills en el IDE, o el pipeline del producto; escribir lo hace una persona, en su terminal

```
uso: coyote install --ide claude-code|cursor|codex|gemini|copilot|windsurf|all | --ci github [--check] [--dry-run]
  -check
    	falla si la configuración no está vigente, sin escribir (para CI)
  -ci string
    	pipeline de impacto del producto: github
  -coyote-ref string
    	con --ci: versión etiquetada de coyote que compila el pipeline (por defecto, la de este binario)
  -coyote-repo string
    	con --ci: owner/nombre del repo de coyote en GitHub
  -dry-run
    	muestra los cambios sin escribir
  -ide string
    	IDE a configurar: claude-code, cursor, codex, gemini, copilot, windsurf o all
  -policy string
    	con --ci: warn solo reporta; fail hace fallar el chequeo hasta que aprueba un dueño. Por defecto sale de features.pr_enforcement
```

## coyote hooks

instala el hook commit-msg

### coyote hooks install

```
uso: coyote hooks install [--force]
  -force
    	reemplaza un hook commit-msg ajeno
```

## coyote doctor

verifica herramienta, identidad, documentos, estándar, gate y el nivel medido de cada IDE

```
uso: coyote doctor [--ide IDE|all [--canary]] | canary <código>
  -canary
    	con --ide: pide un canario para medir si el IDE llama al gate y respeta su negación
  -ide string
    	prueba además el gate, la instalación y el nivel medido de ese IDE
```
