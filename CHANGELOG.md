# CHANGELOG

Los cambios de cada versión de coyote. Desde 1.0 sigue versionado semántico, con la promesa de [compatibilidad](docs/reference/compatibilidad.md). La evidencia de cada versión, con sus pruebas y revisiones adversariales, está en [docs/releases](docs/releases/).

## [1.0.0] — sin publicar

### Agregado

- `coyote audit`: el reporte del mes desde el ledger, las aprobaciones y git, con eventos, costo, integridad y los controles de ISO/IEC 42001 y del NIST AI RMF que apoya. `--write` lo deja en `coyote/audit/` (ADR-0024).
- `coyote eval`: mide el contexto con un golden de preguntas, sin modelos (recall@5, MRR y cobertura del paquete), y falla si algo baja más de 2 puntos contra `evals/baseline.json` (ADR-0025).
- `requires` en `project.yaml` y `hub.yaml`: la versión mínima de coyote que entiende el archivo. `init` lo escribe y `doctor` muestra la versión (ADR-0026).
- `make dist` reproducible, con un SBOM CycloneDX 1.7 y la procedencia SLSA v1.2 Build L1 de cada binario. `make verify-dist` construye dos veces con la caché vacía y compara (ADR-0027).
- Documentación en cuatro formas: un tutorial que corre una prueba, guías por tarea, la referencia de comandos generada desde la CLI y la explicación del diseño.
- Modelo de amenazas, con OWASP Top 10 for LLM Applications 2025 y for Agentic Applications 2026.

### Cambiado

- `coyote web` pide una clave por corrida en la ruta (`/s/<clave>/`) y responde 404 sin ella (ADR-0022).
- Los paquetes de contexto abren con lo más relevante para la consulta y lo que eso cita: la cobertura del golden pasó de 1.7 % a 93.3 %.
- Los paquetes de contexto y `ask` enmarcan lo citado de los repos como dato y marcan con ⚠ lo que tiene forma de orden para un agente (ADR-0023).
- `coyote note` y `coyote generate agents` rechazan entradas con forma de orden para un agente.
- `gate pr`: cambiar las instrucciones de los agentes (`AGENTS.md`, `CLAUDE.md`, `CONTEXT.coyote.md`…) o agregar texto con forma de orden en un documento es R2; reescribir el ledger o meter controles de dirección del texto en el código (Trojan Source) es R3.
- El estándar default pasa a 1.0, con la sección de estabilidad.

### Al actualizar

- Lo que sigue a `--` en un comando de coyote es siempre un argumento, nunca una bandera.
- Regenera el workflow de cada repo del producto con `coyote install --ci github`: ahora compila coyote con su versión.
- Si ya tenías una línea base de `coyote eval`, vuelve a fijarla con `--update-baseline`: ahora guarda las preguntas medidas y se mide sin los eventos del ledger. El golden y la línea base viven en `evals/`.
- `make dist` necesita el árbol limpio y empieza con la carpeta vacía.
- Los enlaces guardados de `coyote web` dejan de servir: la URL nueva sale al arrancar.
- Si una entrada de `CONTEXT.coyote.md` tiene forma de orden, `generate agents` falla hasta que la reescribas como un hecho.
- Los PRs que tocan las instrucciones de los agentes ahora piden la aprobación de un dueño.
- coyote 0.x no conoce `requires`: en `hub.yaml` falla con "clave desconocida" y en `project.yaml` la ignora. Actualiza coyote en cada máquina antes de abrir un proyecto creado con 1.x.

### Seguridad

- Revisión adversarial con tres revisores independientes. Sus 30 hallazgos se corrigieron, cada uno con su prueba:
  - el gate cuenta una bandera que niega también después de `--`, limpia las rutas (`//`, `/./`, `x/..`), las resuelve después de un `cd` y bloquea escribir el ledger por el shell;
  - `gate pr` compara el ledger byte por byte (un archivo recodificado en UTF-16 ya no pasa) y marca R3 un archivo del ledger que no es un archivo; suma más archivos de instrucciones de agentes y marca R2 el golden y la línea base;
  - el clasificador y la salida de coyote ven las marcas invisibles que no son de formato (U+034F, selectores de variación, los del mongol), y `generate agents` pasa todo `AGENTS.md` por el clasificador;
  - `coyote audit` ve un archivo del ledger cambiado por un symlink o un submódulo, las rutas con acentos, un renombre preparado, skip-worktree y un clon superficial; cada registro de un gate respalda un solo evento; avisa de eventos con fecha pasada, escapa el reporte y anota el commit y la huella del ledger;
  - si `project.yaml` no se puede leer, `secrets scan`, el hook `commit-msg` y `gate pr` fallan en vez de seguir sin `secrets.files`;
  - `make dist` construye desde git con el árbol limpio, la carpeta vacía y el entorno de Go fijo, y `distmeta` rechaza un binario que no sale de ese commit; la CI corre `coyote eval` y `make verify-dist`.
- Fuzzing de ocho lectores: los comandos y la entrada de los hooks del gate, las líneas CCF, los documentos CCF-doc, el escape de la salida, el clasificador de órdenes y el archivo y las consultas de SLOs. 4.9 millones de entradas, sin fallas.
- Prueba de escape del gate: 15 intentos, ninguno pasa.

## [0.7.0] — 2026-09-29

### Agregado

- Hub de la organización: `coyote hub init|status`. Un proyecto lo sigue con `hub: { path, ref }` y `extends: hub`, y se lee en el commit de la ref, nunca del árbol de trabajo (ADR-0018).
- Web v1: costos, presupuesto, workstreams, gate, SLOs, organización y `/api/usage`. Cada persona ve su gasto y los totales; el desglose lo ven los admins (ADR-0019).
- SLOs como código: `coyote/slo/<servicio>.yaml`, `coyote slo check|rules` y la regla R19. Genera las alertas page y ticket con varias ventanas y tasas de consumo (ADR-0020).
- gitleaks opcional en el pipeline (`features.gitleaks`), fijado por hash y con la configuración de la rama base (ADR-0021).

### Cambiado

- `gate pr` compara cada SLO con el de la rama base: relajarlo es R3.
- Cargar reglas de alertas o silenciarlas pasa por el gate como un cambio de infraestructura.
- La salida de coyote, en la terminal y en la web, escapa los caracteres de control y los invisibles.
- El gate protege toda carpeta de git, también la de un worktree, y su configuración.
- Una herramienta de apply cuyo subcomando llega al correr (`xargs`, `"$@"`, un alias) se bloquea.
- Los registros de aprobación ya no guardan contraseñas ni tokens que reconoce el escáner.

## [0.6.0] — 2026-09-29

### Agregado

- El gate en Codex CLI, Gemini CLI, Copilot, Windsurf y Devin CLI, con un lanzador que falla cerrado. `coyote doctor --ide X --canary` mide el nivel de cada IDE (ADR-0015).
- Secretos: archivos de secretos por nombre, `secrets.files` y `secrets.allow`, `coyote secrets list|scan` y el escáner en `coyote commit`, en el hook `commit-msg`, en la regla R18 y en `gate pr` (ADR-0016).
- Infraestructura: el inventario `coyote/infra.yaml`, `coyote infra propose|check|plan`, la regla R13, `gate pr --plan` y el gate por ambiente (ADR-0017).

### Cambiado

- El gate lee cada comando como lo correría el shell. Las comillas no esconden un subcomando; `bash -c`, `ssh`, `sudo` y `eval` se leen como otro comando; un mensaje de commit, lo que imprime `echo` y un patrón de `grep` son datos.

## [0.5.0] — 2026-09-28

### Agregado

- Pipeline de PRs: `coyote install --ci github` y `coyote gate pr`, con el riesgo por rutas y por impacto en los otros repos, los dueños del CODEOWNERS de la rama base y un solo comentario por PR. `coyote ci impact` da el impacto solo (ADR-0013).
- Motor de workstreams: `coyote ws check|status|run|continue`, con los modos `manual`, `supervised` y `autonomous`, y el estado sacado del ledger (ADR-0014).
- `coyote close` agrega la tabla de pasos del plan.
- La bandera `features.pr_enforcement`.

### Cambiado

- Un agente no puede correr `coyote run`, `ws run` ni `ws continue`, y leer `.git/config` se bloquea como una credencial.

## [0.4.0] — 2026-09-28

### Agregado

- Producto multi-repo: `type: product`, `coyote map` (Spring, Dart y TypeScript), `coyote impact` sobre un endpoint, un tópico, un texto o un diff, y `coyote extract` (ADR-0011).
- `coyote run`: corre Claude Code headless con el gate, topes de turnos y de dólares y un evento `run` en el ledger (ADR-0012).
- Router v1: modelo por agente, piso por riesgo y degradación por presupuesto. `coyote router` muestra la decisión.
- `coyote close`: el consumo real de un workstream desde el ledger.

## [0.3.0] — 2026-09-28

### Agregado

- Gate por hash: `coyote gate check` como hook previo de Claude Code y Cursor, y la cola `approvals`, `review`, `approve`, `reject` y `revoke`, con aprobaciones firmadas en cada máquina (ADR-0009).
- 14 agentes y 8 skills `coyote-*`, y `coyote install --ide` (ADR-0010).
- Identidad de los agentes: `--agent` solo acepta agentes conocidos y lo que registra una sesión de agente queda a nombre del IDE.

## [0.2.0] — 2026-09-28

### Agregado

- Índice local con BM25, `coyote get context` con presupuesto de tokens y `coyote ask` con referencias (ADR-0007).
- Repos del proyecto: `coyote repo add|list|fetch` trae solo sus documentos.
- `coyote push` y `coyote pull`, con autoría, atribución y estándar revisados y a ritmo humano.
- `coyote auth`: el token de GitHub en el llavero, nunca en archivos (ADR-0008).
- `coyote web`: el costo por proyecto, persona, modelo y agente.

## [0.1.0] — 2026-09-28

### Agregado

- Los formatos CCF v1, para el ledger, y CCF-doc v1, para `README.coyote.md` y `CONTEXT.coyote.md` (ADR-0003).
- Un ledger que solo agrega, con totales de tokens y costo.
- El estándar por capas: el default R1–R17 y A1–A4, `extends`, overrides con motivo, y `coyote standards lint|show|diff|explain`.
- El filtro de atribución a IA en cinco capas (ADR-0005).
- La CLI: `init`, `status`, `note`, `record`, `log`, `commit`, `standards`, `attribution`, `gate`, `generate agents`, `hooks`, `doctor` y `version`.
