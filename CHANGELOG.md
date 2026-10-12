# CHANGELOG

Los cambios de cada versión de coyote. Desde 1.0 sigue versionado semántico, con la promesa de [compatibilidad](docs/reference/compatibilidad.md). La evidencia de cada versión, con sus pruebas y revisiones adversariales, está en [docs/releases](docs/releases/).

## [1.0.0] — sin publicar

### Agregado

- La fase del PRD (ADR-0028). La skill `coyote-prd` trae la plantilla de ocho secciones (problema, usuarios, metas y métricas, alcance, requisitos con ID, criterios en Gherkin, riesgos y preguntas abiertas) y `coyote-pm` la usa. El PRD del producto es un documento vivo en `coyote/prd/producto.md` y cada feature tiene el suyo, que cuelga de él. El plan declara `prd:` (un archivo `.md` o `step:ID`) y `coyote ws check` avisa cuando uno de riesgo R2 o R3 no parte de un PRD: es la regla R20, SHOULD. En `autonomous` el paso que escribe el PRD lleva `gate: human` y la autorización del plan cubre también el contenido del PRD y de las entradas de archivo. El índice de contexto lee `coyote/prd/`.
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

- Un plan de riesgo R2 o R3 sin `prd:` recibe un aviso de `coyote ws check` (R20, SHOULD); no impide correrlo. coyote 1.0.0-rc.1 no conoce `prd:` y falla nombrando el campo.
- Lo que sigue a `--` en un comando de coyote es siempre un argumento; una bandera del comando después de `--` es un error de uso.
- Regenera el workflow de cada repo del producto con `coyote install --ci github`: ahora compila coyote con su versión.
- Si ya tenías una línea base de `coyote eval`, vuelve a fijarla con `--update-baseline`: ahora guarda la medida de cada pregunta y se mide sin los eventos del ledger. El golden y la línea base viven en `evals/`.
- `make dist` necesita el árbol limpio, compila en un clon de HEAD hecho por `tools/distsrc.sh` sin el entorno de git de quien compila, y empieza con la carpeta vacía.
- `coyote install --ci github` (cuando escribe los workflows) y `coyote repo add` los corre una persona, en su terminal; el gate bloquea a un agente que los intente, también si la línea lleva `--check` como valor de otra bandera. `--check` y `--dry-run` siguen corriendo en cualquier parte.
- `coyote audit` cuenta las firmas de los commits sin verificarlas: ya no corre gpg ni el programa de firma que declare la configuración del repo. El conteo cambia frente a un reporte hecho con la verificación (el de septiembre de 2026 dice 0 de 117 con la verificación; contadas por su encabezado son 24); los reportes ya cerrados no se tocan.
- Un `coyote/project.yaml` que git sigue y no se puede leer (un enlace roto, un `sparse-checkout`) hace fallar `secrets scan`, el commit y `gate pr`, nombrando el archivo.
- En un monorepo con proyectos coyote anidados, el gate, `secrets scan`, `secrets list`, el commit y `gate pr` juzgan cada ruta con las reglas del proyecto más cercano y los `secrets.files` de los que lo contienen. Una dispensa (`secrets.allow`) del proyecto anidado vale para lo suyo, salvo para un archivo que el de afuera declara y no dispensa: ahí hace falta también en el de afuera. Un agente no escribe el `project.yaml` de un proyecto anidado ni mueve su carpeta `coyote/`.
- Un `plan.yaml` cuyo `id:` no es el de su carpeta no corre (`ws check` lo dice), y `coyote run --ws`, `close` y `web` atribuyen al workstream de la carpeta.
- El gate lee los comodines como bash y, cuando no sabe, de más: un patrón con corchetes o con una clase de POSIX que podría alcanzar un archivo de secretos se bloquea aunque el shell no lo alcanzaría, y `rm -rf /ruta/al/proyecto/coyote` se bloquea como `rm -rf coyote`.
- Si dos carpetas con plan comparten el número de un workstream, los comandos de `ws` fallan nombrándolas (`hay 2 carpetas para W-0004: …`); antes elegían la primera. `ws status --json` dice `next.run: false` cuando el plan tiene errores.
- Si ya usabas planes `autonomous` con un PRD o con entradas de archivo, `coyote review` ahora lista esos archivos con su huella y la autorización deja de valer si uno cambia: autoriza de nuevo.
- Los enlaces guardados de `coyote web` dejan de servir: la URL nueva sale al arrancar.
- Si una entrada de `CONTEXT.coyote.md` tiene forma de orden, `generate agents` falla hasta que la reescribas como un hecho.
- Los PRs que tocan las instrucciones de los agentes ahora piden la aprobación de un dueño.
- coyote 0.x no conoce `requires`: en `hub.yaml` falla con "clave desconocida" y en `project.yaml` la ignora. Actualiza coyote en cada máquina antes de abrir un proyecto creado con 1.x.

### Seguridad

- Revisión adversarial en siete pasadas: tres revisores independientes sobre v1.0, dos pasadas de dos revisores sobre los arreglos, una cuarta de tres revisores sobre las correcciones de la tercera ronda, el gate de PR y el código del PRD, y de la quinta a la séptima, de tres revisores cada una (secretos y gate; PRD y workstreams; auditoría y release), sobre las correcciones de la ronda anterior. Sus 160 hallazgos (32, 22, 13, 22, 25, 23 y 23 por pasada) se corrigieron con su prueba, salvo los límites declarados (el principal: un selector de variación después de un símbolo o un dígito, que es la forma de color de un emoji; el resto, en docs/releases/v1.0.0.md):
  - el gate cuenta una bandera que niega también después de `--`; resuelve las rutas limpias contra la carpeta del IDE, cada `cd`, `-C` o `--chdir`, los comodines y los enlaces; ve una redirección pegada; bloquea mover o borrar la carpeta que contiene lo protegido, y todo lo que nombra el ledger con efectos, salvo leerlo;
  - `gate pr` compara el ledger byte por byte (un archivo recodificado en UTF-16 ya no pasa) y marca R3 un archivo del ledger que no es un archivo y el gate de un proyecto en una subcarpeta; suma más archivos de instrucciones de agentes y marca R2 el golden y la línea base;
  - el clasificador y la salida de coyote ven las marcas invisibles que no son de formato (U+034F, selectores de variación fuera de un emoji, el blanco del braille), y `generate agents` pasa también el nombre del frontmatter por el clasificador;
  - `coyote audit` cuenta cada commit en el mes en que llegó a la cadena de primeros padres y lo compara contra cada padre (un merge `-s ours` o una rama con fechas viejas ya no esconden nada), sin injertos; ve un symlink o un submódulo en lugar de un archivo, las rutas con acentos, un renombre preparado, skip-worktree y un clon superficial; cada registro de un gate respalda un solo evento, solo un gate respalda a un agente y una persona no pierde su registro frente a un agente; avisa de eventos con fecha pasada, escapa el reporte y anota el commit y la huella de lo que leyó;
  - si un `project.yaml` no se puede leer, `secrets scan`, `secrets list`, el hook `commit-msg` y `gate pr` lo nombran y fallan en vez de seguir sin `secrets.files`, también desde la raíz de un monorepo; un proyecto anidado no tapa los secretos del de afuera;
  - `coyote eval` compara sobre las preguntas de la línea base, con la huella de cada una, y rechaza preguntas repetidas;
  - `make dist` compila en un clon limpio de HEAD sin la configuración de git de quien compila, con la carpeta vacía y el entorno de Go fijo, y `distmeta` rechaza un binario que no sale de ese commit; la CI corre `coyote eval` y `make verify-dist`;
  - la cuarta pasada (22 hallazgos: 4 altos, 8 medios y 10 bajos, 3 de ellos regresiones de la tercera ronda):
    - `coyote audit`: ningún commit llega en el futuro ni saca a los que le siguen del periodo, un commit que borró líneas del ledger antes del periodo se avisa con el mes en que llegó, los registros de aprobación se asignan por orden (primero las personas) y citar uno ya usado no se lo quita a su evento, las firmas se cuentan sin correr programas ajenos y un trailer `Assisted-by: <herramienta>` cuenta como atribución a IA;
    - secretos: los proyectos salen de lo versionado (un `project.yaml` que falta o no se lee falla cerrado) y uno dentro de `vendor`, `node_modules` o `third_party` suma sus archivos de secretos sin dispensar nada, así que un proyecto real llamado `apps/vendor` ya no pierde los suyos;
    - release: el clon de `make dist` no hereda atributos, plantillas ni variables de git de quien compila; `distmeta` rechaza `-gcflags`, `-tags` y similares y declara por plataforma las opciones propias de una arquitectura;
    - gate: `--check` o `--dry-run` como valor de otra bandera no libera `install`; `install --ci` (al escribir) y `repo add` son de la persona; `gate pr` lee completos los archivos que git lista sin hunks (`-diff`, binarios, vacíos) en busca de caracteres ocultos;
    - PRD y workstreams: `prd:` y las entradas solo aceptan rutas visibles (sin saltos de línea ni caracteres invisibles) y `step:` en minúsculas; en `autonomous` el paso del PRD pide a una persona; la autorización cubre el contenido del PRD y de los archivos que el plan lee, y el motor se detiene si uno cambia mientras corre; lo que un paso recibe de un archivo entra escapado y marcado; la plantilla del PRD tiene una prueba que compara la lista ordenada de secciones de cada fuente.
  - la quinta pasada (25 hallazgos: 2 altos, 7 medios y 16 bajos, 10 de ellos regresiones de la cuarta ronda):
    - `coyote audit`: la llegada de un commit con fecha futura no depende de la hora en que corre la auditoría (llega con el commit que le sigue, y ya no amontona el trabajo posterior en el mes en curso); el mensaje se revisa también como lo convierte git cuando el commit declara otra codificación; el autor de las líneas se lee sin correr el `textconv` de la configuración; la historia anterior al periodo se lee con prefijos fijos, aunque `diff.noprefix` diga otra cosa, y de una sola vez; el trailer `Assisted-by:` no marca a personas;
    - secretos y gate: el gate de un agente aplica los `secrets.files` de los proyectos anidados de un monorepo; `secrets list` ve los mismos proyectos que `scan`; `gate pr` compara con la base lo que git lista sin hunks, así que un PR que solo borra líneas o cambia el modo no acusa lo que ya había; `--staged` y `coyote commit` aplican un proyecto anidado que todavía no se versiona; un binario que empieza con un BOM falso no es texto; `repo add` es de la persona;
    - release: `distmeta` solo acepta en el buildinfo lo que `make dist` fija (`GOAMD64=v3`, `GOFIPS140` o `-buildmode=pie` se rechazan) y `make dist` no lee `GIT_DIR`, `GIT_WORK_TREE` ni `GIT_INDEX_FILE`;
    - PRD y workstreams: el `title`, el `does` y los demás textos del plan salen en una línea; el motor autónomo solo se detiene ante el archivo que recibe el paso, no ante cualquier archivo declarado; `./step:S1` no vale como archivo; un final de línea de Windows y el texto legítimo con marcas invisibles llegan bien al agente; `--mode supervised` acepta un plan cuyo PRD es un paso; los PRD de ejemplo se validan contra su vocabulario.
  - la sexta pasada (23 hallazgos: 2 altos, 9 medios y 12 bajos, 6 de ellos regresiones de la quinta ronda):
    - secretos y gate: un archivo en UTF-16 con secuencias ANSI se escanea (antes lo volvía binario una proporción de controles); una ruta con `[` en el nombre o en una carpeta no esquiva la lectura de un archivo de secretos; el gate aplica los secretos y protege el gobierno de cada proyecto anidado, también con el IDE abierto en uno de ellos, y bloquea mover o borrar su carpeta `coyote/` (y `git mv`, `git rm` o extraer con `tar` donde se está); `secrets list` incluye lo que git sigue en `vendor`, `node_modules` y `build`; `propose` y `approve --bash` juzgan como el gate; la dispensa de un proyecto anidado vale salvo para lo que el de afuera declara y no dispensa; con un `project.yaml` anidado ilegible, el motivo lo dice;
    - `coyote audit`: `log.diffMerges`, dos encabezados `encoding` y un filtro `clean` de nombre inusual ya no esconden un aviso; la historia se pide en UTF-8; lo que llegó en el periodo se lee con un `git log` y `git cat-file --batch` y no con un proceso por commit (3000 commits: de 29 s a 0,5 s); el aviso de fecha futura dice que el reporte puede repetirse el mes siguiente;
    - release y atribución: `make dist` lee `DIST` del entorno entre comillas y no corre nada que venga en él; el trailer `Assisted-by:` vuelve a ver Cursor, IDE, Composer y los correos de Anthropic, y `Helped-by: Claude` no marca a una persona;
    - PRD y workstreams: el workstream es el de su carpeta para todo (`run --ws`, `close`, `web`); un `autonomy:` inválido no forja líneas ni se compara con `--mode`; las pistas llevan el `--mode` con el que se corre; la evaluación de A4 solo es de lo que corrió en `autonomous`; un espacio al borde de un nombre no esconde `step:`; la detención de A4 queda en el ledger; el plan de ejemplo carga en 1.0.
  - la séptima pasada (23 hallazgos: 2 altos, 6 medios y 15 bajos, 4 de ellos regresiones de la sexta ronda):
    - secretos y gate: un comodín se lee como lo lee bash (clases de POSIX, `[!x]`, `[]x]`, equivalencias, corchetes citados o escapados) y, cuando no se sabe, de más; mover o borrar `coyote/`, `.git/` o `.claude/` se bloquea con la ruta absoluta, con otras mayúsculas y con `-t`; con el IDE en un worktree enlazado o en un proyecto anidado, el gate aplica las reglas de secretos de cada checkout y protege el gobierno de los proyectos de afuera y de los otros checkouts; una carpeta `pkg/coyote/ledger/` sin proyecto deja de bloquearse; `secrets list` y `secrets scan` corridos desde un proyecto anidado incluyen lo que declara el de afuera;
    - `coyote audit` y `attribution check`: un asunto con `(from <hash>)` no elige contra qué padre se mide un commit; `log.showRoot=false` y `log.showSignature=true` no cambian lo que se lee (la segunda dejaba pasar un `Co-Authored-By` en un commit firmado); unos bytes nulos o una salida inesperada de git fallan en vez de darse por limpios; la cadena de primeros padres se arma en tiempo lineal; `Helped-by:` ve un modelo con versión (`Claude 3.5`), `Assisted-by:` ve `cursor[bot]` y `[Cursor]`, y un espacio que no es ASCII se lee como un espacio;
    - PRD y workstreams: los nombres de las entradas solo llevan caracteres imprimibles y no empiezan con una marca; una carpeta sin plan no esconde la que lo tiene y dos con plan fallan nombrándolas; el cierre solo habla de A4 si algo corrió en `autonomous`; `--mode supervised` no calla el aviso del evaluador; `ws status --json` dice `next.run: false` con un plan con errores.
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
