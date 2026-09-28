# Plan de ejecución — Coyote

Estado: v0.4.0 aprobada en G4 (P-0004) · v0.5.0 lista para G5 (docs/releases/v0.5.0.md) · actualizado 2026-09-28

Este plan ejecuta la propuesta aprobada ("Plan de construcción — Framework Coyote"). Cada release se razona con la plantilla de cinco partes que usarán los agentes de Coyote (problema, restricciones, opciones, decisión, riesgos) y cierra con un gate humano: nada se etiqueta, se publica ni toca otros proyectos sin autorización explícita.

## Reglas de ejecución

1. Aislamiento. Todo vive en `~/Documents/coyote`. Los repos de `~/Documents/projects` no se modifican; de ellos solo se leyó lo necesario para derivar el estándar default y la identidad de git.
2. Gates. Cada release cierra con un reporte en `docs/releases/` (qué se hizo, evidencia, riesgos, qué se autoriza). La decisión se registra en `coyote/approvals/` y en el ledger.
3. Autoría humana. Los commits llevan la identidad de git de la persona dueña del repo; ningún commit, documento o release lleva firmas de herramientas de IA (R15).
4. Sin efectos externos sin gate: nada de push, releases públicos, repos remotos ni llamadas pagadas a APIs.
5. Registro. Cada evento relevante queda en `coyote/ledger/` en CCF.

## Releases y gates

| Release | Alcance | Gate | Qué se autoriza en el gate |
| --- | --- | --- | --- |
| v0.1 init (local) | Formatos CCF, ledger, estándar por capas, filtro de atribución, CLI local, CI | G1 | Etiquetar v0.1.0, publicar el repo en GitHub, arrancar v0.2 |
| v0.2 remote + context | Remotos git con autoría humana y cuotas; índice local; `get context`; `ask`; web v0 | G2 | Uso del token de GitHub; costo del primer job de indexado |
| v0.3 gate | Aprobaciones por hash, hooks de IDE, agentes y skills, `install` para Claude Code y Cursor | G3 | Piloto en seco sobre un dominio real, en solo lectura |
| v0.4 run | Producto multi-repo: extracción de contexto, mapa entre repos e impacto de un cambio; `coyote run`, router v1 y cierres con costo | G4 | Presupuesto para correr agentes y llevar las propuestas del piloto a cada repo |
| v0.5 team | Pipeline de impacto en cada PR, motor de workstreams (`supervised` y `autonomous`), aprobación en equipo por PR y corridas medidas | G5 | Aplicar coyote a los repos de la organización: pipeline y `supervised` |
| v0.6 | Resto de IDEs, web v1, IaC, DevSecOps y SRE | G6 | Según lo que muestre v0.5 |
| v1.0 | Endurecimiento, auditoría, documentación | G7 | Release 1.0 |

## v0.1 init — razonamiento

**Problema.** Hace falta una base local que ya cumpla las reglas que Coyote va a exigir a otros: formatos compactos para registrar y contextualizar, un estándar por capas que se pueda validar, y la garantía de que nada sale con firmas de herramientas de IA. Sin eso, cada release posterior se construiría sobre acuerdos verbales.

**Restricciones.** Sin tocar otros proyectos; sin credenciales de GitHub ni API key en esta etapa; el entorno de construcción no llega al proxy de módulos de Go; la herramienta no puede contener datos de ninguna organización; cada entregable debe poder revisarse en minutos.

**Opciones.**
- A. Seguir el orden de la propuesta: v0.1 con `push` y `pull` contra GitHub y los seis repos inicializados. Choca con el aislamiento y con la falta de credenciales.
- B. Construir capas completas (todos los formatos, luego toda la CLI, luego remotos). Retrasa lo que se puede usar y probar.
- C. Corte vertical local: formatos, ledger, estándar, atribución y una CLI que los usa de punta a punta sobre un proyecto propio y uno sintético; remotos y repos reales pasan a gates posteriores.

**Decisión.** C. Es lo único que se puede verificar completo sin credenciales ni efectos externos, y deja listo lo que más cuesta rehacer: el formato del registro, el estándar y el filtro de atribución. `push` y `pull` pasan a v0.2 (requieren tu token); aplicar Coyote a los repos de la organización pasa a G5.

**Riesgos.** Que el estimador local de tokens se desvíe del conteo real (mitigación: es conservador y v0.2 lo contrasta con `count_tokens`); falsos positivos del filtro de atribución en documentación (mitigación: patrones de frase, lista de rutas permitidas y pruebas); que la CLI sin framework crezca desordenada (mitigación: un archivo por comando y pruebas de punta a punta).

### Tareas de v0.1

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T1 | Espacio aislado y entorno | El proyecto debe vivir fuera de `projects/` y compilar sin red | Carpeta propia, Go 1.24 en el entorno de construcción, go-yaml copiado con procedencia | Hecho |
| T2 | Plan y ADRs | Las decisiones que no tomaste explícitamente deben quedar razonadas y revisables | Este plan y ADR-0001 a ADR-0006 | Hecho |
| T3 | CCF v1 y CCF-doc v1 | Son la memoria del sistema; cambiar el formato después es caro | Specs, parsers con validación y pruebas de ida y vuelta | Hecho |
| T4 | Ledger | Append-only, un archivo por día y persona para no generar conflictos en git | `record` y `log` con filtros; pruebas | Hecho |
| T5 | Estándar por capas | El default sale de tus repos; cada organización y proyecto lo ajusta | `rules.yaml` default R1–R17 y A1–A4, `extends`, overrides con motivo y caducidad, lint con checks | Hecho |
| T6 | Atribución | Requisito explícito: ningún entregable firmado por herramientas de IA | Scrub en commit, hook `commit-msg` autónomo, `gate attribution` para PreToolUse, check en lint y CI | Hecho |
| T7 | CLI local | Los comandos son la interfaz de humanos, agentes e IDEs | `init`, `status`, `note`, `record`, `log`, `commit`, `standards`, `attribution`, `gate`, `generate agents`, `hooks`, `doctor` | Hecho |
| T8 | Dogfooding | La herramienta debe cumplir su propio estándar | `coyote/` propio, AGENTS.md generado, lint en verde, commits con `coyote commit` | Hecho |
| T9 | Ejemplo y CI | Probar sin datos reales y dejar la verificación automática lista para GitHub | `examples/acme-shop` y `.github/workflows/ci.yml` | Hecho |
| T10 | Verificación | Evidencia antes de pedir autorización | vet, pruebas, demo de punta a punta y revisión independiente | Hecho: dos revisiones adversariales, 13 + 9 hallazgos corregidos (ver docs/releases/v0.1.0.md) |
| T11 | Entrega y gate G1 | Pedir autorización con evidencia, no con promesas | Repo en `~/Documents/coyote` con historia y autoría tuya; reporte en `docs/releases/v0.1.0.md` | Hecho: G1 aprobado (P-0001) |

## v0.2 remote + context — razonamiento

**Problema.** Coyote ya registra y valida, pero no colabora. El contexto no viaja entre repos ni entre personas. Un agente no puede pedir "lo que necesito saber para tocar pagos" sin leer todo. Nadie ve el costo por proyecto o por persona. Y no hay manera segura de publicar en GitHub al ritmo de una persona.

**Restricciones.**
- Toda escritura remota va con la identidad y las credenciales de la persona: git del sistema para push y pull, y un token suyo para la API.
- Las cuotas de GitHub se consumen a ritmo humano (80 escrituras por minuto y 500 por hora como techo; perfil `human` muy por debajo), respetando `Retry-After` y los límites secundarios.
- El código de otros repos no se baja si no hace falta: del repo remoto solo se leen sus documentos coyote.
- El entorno de construcción no llega al proxy de módulos de Go ni a la API de GitHub. No se pueden agregar dependencias y las llamadas reales solo se prueban contra servidores locales.
- Ningún secreto en disco dentro del proyecto. Ningún servicio escucha fuera de `127.0.0.1`.

**Opciones para el índice local.**
- A. SQLite con `modernc.org/sqlite`: puro Go, pero decenas de módulos que no se pueden traer aquí y un repo mucho más pesado.
- B. SQLite con `mattn/go-sqlite3`: requiere cgo y rompe los binarios cruzados (`CGO_ENABLED=0`).
- C. Índice en memoria reconstruido desde los archivos (que ya son la fuente de verdad en git), con caché en `.coyote/` y una interfaz de almacenamiento para poner SQLite después.

**Opciones para credenciales.** Pedir un token personal a mano; reutilizar `gh auth token` y el llavero del sistema; OAuth device flow propio.

**Decisión.**
- Índice: **C** (ADR-0007). Con el volumen de un proyecto (miles de eventos y cientos de entradas de contexto) reconstruir cuesta milisegundos, git sigue siendo la fuente de verdad y no se agrega ninguna dependencia. SQLite entra cuando la medición lo pida y el entorno tenga el proxy de módulos; se decide en G3 o G4.
- Credenciales, en capas (ADR-0008): variable de entorno, luego `gh auth token`, luego el llavero del sistema vía su binario (`security` en macOS, `secret-tool` en Linux), y device flow cuando haya un client ID de OAuth App registrado. Git no necesita token: usa tu SSH o tu credential helper.
- Búsqueda: BM25 local sobre entradas, ADRs y documentos. Sin embeddings ni LLM en v0.2: costo cero y nada sale de la máquina.
- Web: `net/http` y `html/template` embebidos, solo lectura, solo `127.0.0.1` y con verificación de `Host` contra DNS rebinding.

**Riesgos.**
- Que el índice en memoria no escale (mitigación: interfaz de almacenamiento y medición en la demo).
- Que las llamadas reales a GitHub difieran de los servidores de prueba (mitigación: G2 autoriza una prueba real de solo lectura con tu token).
- Que `gh` o el llavero no estén (mitigación: mensajes claros y variable de entorno).

### Tareas de v0.2

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T12 | Plan y ADR-0007, ADR-0008 | Dos decisiones cambian supuestos de la propuesta; quedan razonadas antes del código | Esta sección y los ADRs | Hecho |
| T13 | Índice, `get context` y `ask` | Es lo que más ahorra tokens: un agente recibe un paquete acotado en vez de leer todo | Paquete con presupuesto de tokens y referencias; búsqueda con fuentes; pruebas | Hecho |
| T14 | Ritmo humano y credenciales | Evitar que GitHub trate la cuenta como bot y no dejar secretos en disco | Token bucket persistido, `auth login/status/logout`, cliente con `Retry-After`; pruebas con servidor local | Hecho |
| T15 | Repos del proyecto, `push` y `pull` | Colaborar como con git, con los gates antes de publicar | `repo add/list`, contexto remoto sin bajar código, push con lint y atribución, pull con resumen | Hecho |
| T16 | Web FinOps v0 | Ver consumo y costo por proyecto y por persona | `coyote web` con vistas proyecto>usuario y usuario>proyecto, modelos y tokens | Hecho |
| T17 | Verificación y G2 | Evidencia antes de autorizar el uso del token | Pruebas, demo, revisión adversarial, `docs/releases/v0.2.0.md` | Hecho: 14 hallazgos corregidos; G2 aprobado (P-0002) |

G2 autorizó (P-0002): etiquetar v0.2.0, validar tu token con una sola lectura a la API, seguir con `gh` y el llavero (la OAuth App se decide después), confirmar D2, D4, D15, D16 y D18, y arrancar v0.3. Los repos de la organización no se indexan hasta que los elijas.

## v0.3 gate — razonamiento

**Problema.** La regla A1 hoy es una promesa. Un agente en Claude Code o Cursor puede correr comandos, editar archivos o llamar herramientas MCP sin una aprobación registrada. El ledger guarda el agente que cada comando declara, sin verificarlo. No hay agentes ni skills que instalar, cada IDE se configura a mano y el modo de autonomía de `project.yaml` no tiene efecto.

**Restricciones.**
- El control vive donde el agente actúa: el hook previo del IDE. En Claude Code, `PreToolUse` corre antes de los permisos, dentro de los subagentes y aun con `bypassPermissions`. En Cursor, `preToolUse` cubre comandos, ediciones y MCP. Nada depende del prompt.
- Se aprueba la acción exacta por su hash, con caducidad de 24 horas como máximo y usos contados. Sin comodines.
- Aprueba una persona. Un agente no puede aprobar, editar el gate ni fabricar un registro.
- Falla cerrado: si coyote no está instalado o falla, la acción no corre.
- Leer y buscar no piden aprobación. Si cada `ls` pidiera una, la gente apagaría el gate; la fatiga de aprobaciones es el primer riesgo de la propuesta.
- Sin dependencias ni red nuevas, sin tocar otros proyectos, y con C1 y R15.

**Opciones para el gate.**
- A. Instrucciones en los prompts de los agentes. Un agente con una instrucción inyectada las ignora.
- B. Listas de comandos permitidos por patrón. Aprueban acciones que nadie vio, y la propuesta prohíbe los comodines.
- C. Hash de la acción exacta en el hook del IDE, con una cola de propuestas, aprobación desde la terminal de la persona, registros firmados con una clave local, usos contados en el ledger y una lista cerrada de comandos de solo lectura que no necesitan aprobación.

**Opciones para agentes e IDEs.** Escribir a mano los archivos de cada IDE, o mantener una sola definición por agente que `coyote install` traduce al formato de cada IDE.

**Decisión.**
- Gate: **C** (ADR-0009). El hash cubre lo que decide el efecto: en Bash, el comando y la carpeta; en Write, la ruta y el contenido; en Edit, la ruta y los textos viejo y nuevo; en lo demás, el nombre y la entrada completa. Si el agente cambia la descripción, el hash no cambia; si cambia un carácter del comando, sí.
- Siempre bloqueado, aun con aprobación: que un agente apruebe o revoque, que edite el gate (hooks y configuración del IDE, `.git/`, `coyote/approvals/`, `.coyote/`) y que lea o escriba credenciales.
- Agentes: una definición por agente en la herramienta, los 14 de la propuesta, más los del proyecto en `coyote/agents/`. Como pide la propuesta, exploran y proponen sin Bash, Write ni Edit; lo que cambia el código lo aplica la sesión principal y pasa por el gate (ADR-0010).
- IDEs: `coyote install --ide claude-code` y `--ide cursor`. Ambos son de nivel 1: su hook previo cubre comandos, ediciones y MCP. Los demás IDEs llegan en v0.5.
- Identidad: el ledger toma el agente que reporta el IDE (`agent_type` en Claude Code), no el que declara el comando. Un nombre fuera del roster se rechaza, y un comando que declara un agente distinto al que reporta el IDE se bloquea.
- Autonomía: solo `manual` en v0.3. `supervised` y `autonomous` se aceptan en `project.yaml`, pero el gate aplica `manual` hasta que v0.4 traiga su motor: nunca más laxo que lo construido.

**Riesgos.**
- Fatiga de aprobaciones. Mitigación: lectura libre, aprobar la cola completa con `--all`, aprobaciones con varios usos durante 24 horas como máximo, y el flujo de parche: el agente propone un diff y se aprueba una sola vez.
- Un comando "de solo lectura" que en realidad escribe o ejecuta. Mitigación: lista cerrada con banderas prohibidas por comando, sin redirecciones, sustituciones ni variables, y pruebas adversariales.
- Que un IDE cambie el formato de sus hooks. Mitigación: pruebas con entradas reales de cada IDE y `coyote doctor --ide`, que prueba el gate en tu máquina.
- Un proyecto con gate no se puede usar sin coyote instalado. Es la consecuencia buscada; el mensaje dice cómo instalarlo.
- Aprobar sin leer. Mitigación: `coyote review` muestra el comando o el diff, quién lo pidió y desde qué agente.

### Tareas de v0.3

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T18 | Plan y ADR-0009, ADR-0010 | El gate cambia cómo trabajan los agentes; se razona antes del código | Esta sección, los ADRs y el workstream W-0003 | Hecho |
| T19 | Gate por hash | Es el núcleo de A1: sin él, lo demás es declarativo | `gate check` con entradas de Claude Code, Cursor, Codex y Copilot; lectura libre; rutas protegidas; falla cerrado | Hecho |
| T20 | Cola de aprobaciones | La persona necesita ver y decidir rápido, con rastro | `approvals`, `review`, `approve`, `reject`, `revoke`; registros firmados; usos, caducidad y revocación en el ledger | Hecho |
| T21 | Agentes y skills | Una definición sirve a todos los IDEs | 14 agentes y skills `coyote-*` con esquema de salida y R15; los del proyecto en `coyote/agents` y `coyote/skills` | Hecho |
| T22 | `install` y `doctor --ide` | Configurar un IDE debe ser un comando, verificable en CI | Claude Code y Cursor; configuración fusionada sin pisar la tuya; `--check` | Hecho |
| T23 | Identidad y autonomía | El ledger debe decir qué agente actuó, sin depender de lo que declara | Roster, identidad del IDE en el ledger, sesiones de agente sin poder aprobar | Hecho |
| T24 | Verificación y G3 | Evidencia antes de activar el gate en proyectos reales | 24 pruebas de gate o más, demo de punta a punta, revisión adversarial y `docs/releases/v0.3.0.md` | Hecho: 138 casos de gate; 4 hallazgos y 4 notas corregidos; G3 aprobado (P-0003) |

G3 autorizó (P-0003): etiquetar v0.3.0, conservar la historia con las firmas del entorno, confirmar D6, D11, D19, D20, D21 y D22, y arrancar v0.4. El piloto en seco abarca los tres repos del producto juntos (servicios, app y backoffice), en solo lectura, porque un cambio en uno afecta a los otros. Activar el gate en tus proyectos lo haces tú con `coyote install`.

## v0.4 run — razonamiento

**Problema.** El producto no es un repo. Servicios (con sus dos BFF), la app móvil y el backoffice web viven en tres repos, y un cambio en uno puede romper a los otros sin que nadie lo vea antes del merge; así lo pediste en G3. Hoy coyote trata cada repo por separado, los agentes no se pueden correr desde coyote, ningún cierre sabe cuánto costó y el router de modelos no existe.

**Lo que mostró la lectura de los tres repos (solo lectura).**
- Servicios: 29 servicios Spring. Las rutas se declaran con anotaciones (`@RequestMapping` en la clase y `@GetMapping`, `@PostMapping`… en los métodos). Dos BFF exponen lo que consumen la app y el backoffice, y llaman a los servicios internos con WebClient (`.uri("/api/v1/...")`). Unos 40 tópicos Kafka aparecen como literales `dominio.evento`. Solo un servicio tiene OpenAPI.
- App: Flutter con Melos, 11 paquetes. Llama al BFF móvil con Dio: `.post('/credit/applications/$id/offer')`.
- Backoffice: Nx con 10 micro-frontends y una librería de cliente. Llama al BFF de backoffice con `request(`/staff/${id}`)`.
- Conclusión: los contratos reales viven en el código, no en archivos OpenAPI. El mapa entre repos se puede sacar de ahí sin modelo: cuesta cero y se puede repetir.

**Restricciones.**
- Los repos del producto se leen, no se modifican. Lo que coyote propone vive en un proyecto aparte y se revisa antes de llevarse a cada repo.
- C1: la herramienta no nombra a la organización. Los extractores son por stack (Spring, Dart, TypeScript), no por repo, y sus pruebas usan código sintético.
- Sin dependencias nuevas ni red desde el entorno de construcción. `coyote run` usa el Claude Code de la persona en su máquina, y su costo se autoriza en G4.
- El gate de v0.3 aplica a todo lo que corra `coyote run`: los hooks también corren en modo headless.
- Costo predecible: toda corrida lleva tope de turnos y de dólares, y el cierre suma lo gastado.

**Opciones para ver un cambio de manera holística.**
- A. Un agente que lea los tres repos por cada cambio. Es caro (millones de tokens), lento y no se puede repetir.
- B. Exigir contratos OpenAPI y AsyncAPI en cada repo antes de analizar. Es lo correcto a largo plazo (R11), pero hoy casi no existen y bloquearía el piloto.
- C. Un mapa del producto sacado del código por extractores deterministas: rutas que expone cada servicio, llamadas que hace cada cliente y tópicos que se publican y se consumen, con su archivo y línea. Encima, un análisis de impacto que cruza un cambio contra ese mapa, y agentes que reciben el mapa y el impacto como contexto acotado en lugar de leerlo todo.

**Opciones para correr agentes.**
- A. Un runner propio sobre el Agent SDK. Es una dependencia nueva y otro runtime que mantener.
- B. Claude Code en modo headless (`claude -p --output-format json` con `--agent`, `--max-turns` y `--max-budget-usd`). Ya aplica los hooks (el gate) y reporta tokens, caché, costo y modelo.

**Decisión.**
- **Producto multi-repo: C** (ADR-0011). Un proyecto coyote de tipo `product` registra sus repos por ruta local (solo lectura) o por URL.
  - `coyote extract` saca de cada repo su identidad (stack, comandos, módulos, documentos) y propone su README.coyote.md y su CONTEXT.coyote.md en `coyote/repos/<repo>/`.
  - `coyote map` arma el mapa de interfaces (qué expone y qué consume cada módulo) con referencias `repo@sha:ruta#L`.
  - `coyote impact` cruza un cambio contra el mapa y dice qué se afecta en cada repo, directo y a través del BFF. El cambio puede ser el diff de un repo, un endpoint, un tópico o un texto.
- **`coyote run`: B** (ADR-0012). Corre un paso de un agente en la máquina de la persona:
  - recibe como entrada el paquete de contexto y el impacto;
  - usa el modelo que decide el router, con topes de turnos y de dólares, y el gate activo;
  - guarda el artefacto en el workstream y registra un evento `run` con tokens, costo y modelo.
- **Router v1**: `coyote/router.yaml` define el modelo por agente, un piso por riesgo y la degradación por presupuesto: al 80 % del presupuesto mensual avisa y baja un nivel (salvo R3); al 100 % no corre.
- **`coyote close`**: suma tokens y costo por modelo y por agente, más aprobaciones y artefactos. Llena `close.md` y el evento `close`: los cierres dejan de decir "sin medir".
- **Autonomía**: `supervised` y `autonomous` pasan a v0.5, con el trabajo en equipo. Primero hay que medir corridas reales (G4) antes de dejar que un plan corra solo. Hasta entonces, el gate sigue aplicando `manual`.

**Riesgos.**
- Los extractores por patrones fallan con código atípico (rutas armadas en variables, clientes generados). Mitigación: cada entrada del mapa cita su archivo y línea, lo que no se resuelve se reporta como "sin resolver" en lugar de callarse, y el piloto mide la cobertura contra conteos crudos.
- Rutas genéricas (`/health`, `/{id}`) dan coincidencias falsas. Mitigación: se compara por segmentos, pesan más los literales y se descartan las rutas triviales.
- Costo de `coyote run`. Mitigación: topes obligatorios, `--max-budget-usd` y un presupuesto autorizado en G4.
- Que Claude Code cambie su salida JSON. Mitigación: pruebas con salidas grabadas y el runner detrás de una interfaz.
- Datos de la organización en el repo de la herramienta. Mitigación: fixtures sintéticos (C1); el piloto vive en un proyecto aparte, fuera de este repo.

### Tareas de v0.4

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T25 | Plan y ADR-0011, ADR-0012 | El producto multi-repo cambia la unidad de trabajo; se razona antes del código | Esta sección, los ADRs y W-0004 | Hecho |
| T26 | Producto y extracción | Sin contexto por repo no hay mapa ni agentes útiles | `type: product`, `repo add --path` de solo lectura, `coyote extract` para Spring/Gradle, Flutter/Melos y Nx/npm con propuestas en `coyote/repos/<repo>/` | Hecho |
| T27 | Mapa e impacto | Es lo que pediste: ver cada cambio contra los tres repos | `coyote map` (HTTP y eventos, con `repo@sha:ruta#L`) y `coyote impact` (diff, endpoint, tópico o texto), directo e indirecto; el mapa entra a `get context` | Hecho |
| T28 | `coyote run` | Correr agentes desde coyote, con el gate y el costo a la vista | Claude Code headless con agente, contexto, topes y gate; evento `run` con tokens, costo y modelo; pruebas con un `claude` simulado | Hecho |
| T29 | Router v1 y `coyote close` | Gasto predecible y cierres con consumo real | `router.yaml`, pisos por riesgo, degradación por presupuesto; `close.md` desde el ledger | Hecho |
| T30 | Piloto en seco | Evidencia sobre los tres repos reales antes de gastar en modelos | Extracción, mapa e impacto de un cambio real, en solo lectura y en un proyecto aparte; tú revisas cada propuesta | Hecho: `~/Documents/fintech-producto`; revisión en G4 |
| T31 | Verificación y G4 | Evidencia antes de autorizar presupuesto | Pruebas, revisión adversarial y `docs/releases/v0.4.0.md` | Hecho; aprobado en G4 (P-0004) |

**Lo que cambió al construir.** El piloto sobre los tres repos reales mostró patrones que el diseño no cubría, y cada uno quedó con su prueba:

- Constantes de tópicos con el mismo nombre en varias clases.
- Tópicos en `application.yml` y en clases `@ConfigurationProperties`.
- Métodos propios que publican (`send(TOPIC, …)`).
- Rutas concatenadas y verbos en envoltorios `get(…)`/`post(…)`.
- Clientes en paquetes `adapter/out`.
- Dos BFF con la misma ruta, resueltos por afinidad.

La revisión adversarial encontró que leer un repo podía correr sus filtros de git o traer objetos de un clon parcial, y que un temporal plantado desviaba escrituras; se cerró antes del gate. El detalle está en `docs/releases/v0.4.0.md`.

G4 autoriza: el presupuesto para correr agentes con `coyote run` en tu Mac, llevar las propuestas del piloto a cada repo por PR y arrancar v0.5.

## v0.5 team — razonamiento

**Problema.**
- En G3 pediste que el pipeline sacara el contexto de los tres repos y viera cada cambio de manera holística. v0.4 lo hace, pero solo cuando alguien corre `coyote impact` en su máquina. Nada lo corre en cada PR, así que un cambio que rompe a la app o al backoffice todavía llega a `main` sin que nadie lo vea.
- `coyote run` corre un paso a la vez y lo lanza la persona. Los workstreams (`plan.yaml`) son documentos, no planes que se ejecuten, y `supervised` y `autonomous` se aceptan en `project.yaml` sin tener efecto.
- Las aprobaciones valen en una sola máquina (D21): en equipo, una compañera no puede aprobar lo que tu agente propone ni revisar un cambio de riesgo en el PR (D6).
- No hay corridas reales medidas: G4 autorizó el presupuesto, pero la primera corrida es tuya.

**Lo que mostró v0.4.**
- El mapa del producto cuesta segundos y cero dólares: 930 interfaces en unos 3 s y el impacto de un diff en 5 a 8 s. Correrlo en cada PR es barato.
- Lo caro y lo riesgoso son los agentes: dólares y efectos. El gate y los topes ya los contienen.
- El piloto encontró tópicos que nadie publica y cambios sin commit que tocan a la app. Es justo lo que un chequeo en el PR debe mostrar antes del merge.

**Restricciones.**
- El pipeline corre en GitHub Actions de cada repo. Los otros dos repos son privados: el job necesita un token de solo lectura como secreto. Instalarlo en los repos de la organización es lo que autoriza G5; v0.5 lo construye y lo prueba con eventos simulados.
- Sin red ni dependencias nuevas en el entorno de construcción. La API de GitHub se prueba con servidores falsos, como en v0.2.
- La autonomía no afloja el gate sin evidencia (A1, A4):
  - `supervised` corre los pasos de un plan con un punto de control humano entre pasos;
  - `autonomous` corre dentro de los topes del plan en una rama `ws/`, y lo evalúa quien lo autorizó.

  En ambos, lo que tiene efectos sigue pasando por el gate y ningún agente se aprueba.
- C1 y R15 como siempre; el costo de cada paso en el ledger (A3).

**Opciones para el pipeline.**
- A. Un workflow en cada repo que en el PR:
  1. trae el proyecto del producto y los otros repos, solo lectura;
  2. corre `coyote impact` sobre el diff del PR;
  3. deja el reporte en el resumen del job y en un comentario del PR.
- B. Un workflow central en el proyecto del producto, disparado desde cada repo con `repository_dispatch`. Es un salto más y un permiso de escritura entre repos.
- C. Solo un hook local antes del push. Depende de que cada persona lo tenga y no deja rastro en el PR.

**Opciones para el motor de workstreams.**
- A. Un motor en coyote: cada paso de `plan.yaml` declara agente, entradas, salida, riesgo y tope (A2). Corre con `coyote run`, pasa sus artefactos al siguiente paso, se detiene en los puntos de control y lleva el presupuesto del workstream.
- B. Un solo `coyote run` con un agente coordinador que llama subagentes. Menos control: un solo tope para todo y el ledger no ve cada paso.

**Decisión.**
- **Pipeline: A** (ADR-0013).
  - `coyote ci impact` corre dentro de GitHub Actions: lee el evento del PR, arma el mapa con los otros repos y reporta el impacto del diff.
  - El reporte va en el resumen del job y en un solo comentario del PR que se actualiza, no uno por push.
  - Política: `warn` por defecto; `fail` hace fallar el chequeo cuando algo se rompe.
  - `coyote install --ci github` genera el workflow; `--check` lo verifica.
- **Motor: A** (ADR-0014). `coyote ws check|run|continue|status`:
  - En `supervised`, el motor se detiene después de cada paso hasta que la persona revisa el artefacto y corre `coyote ws continue`.
  - En `autonomous`, sigue hasta un punto de control, el tope del plan o una falla. Corre en una rama `ws/<W>`, y la persona que lo autorizó evalúa el resultado antes del merge.
  - El gate aplica el modo del proyecto: sigue sin aprobarse nada solo.
- **Equipo:** `coyote gate pr` es un chequeo de PR:
  - calcula el riesgo del cambio por rutas y por impacto;
  - un cambio R2 o R3, o uno que rompe a otro repo, pide la revisión aprobada de un dueño (CODEOWNERS) antes del merge (R17).

  Es la aprobación en equipo de D6, sin claves compartidas.
- **Medición:** las primeras corridas reales en el piloto las lanzas tú, dentro del presupuesto de G4. Sus números (costo por corrida, caché, turnos) ajustan los valores del router antes de dejar correr planes.
- **Alcance:** los demás IDEs, la web v1 y las herramientas de IaC, DevSecOps y SRE pasan a v0.6. v0.5 se enfoca en el pipeline, el motor y el trabajo en equipo, para que G5 autorice cosas verificadas.

**Riesgos.**
- Un token de lectura de los otros repos en cada repo. Mitigación:
  - de solo lectura (contents: read), limitado a los repos del producto;
  - el workflow corre en PRs de ramas del mismo repo, nunca de forks;
  - el workflow no ejecuta código del PR fuera de coyote.
- Comentarios ruidosos. Mitigación: un solo comentario que se actualiza y el detalle en el resumen del job.
- Un plan autónomo que gasta de más o hace algo inesperado. Mitigación:
  - tope del plan y de cada paso;
  - corre en una rama `ws/`;
  - lo con efectos pasa por el gate y cada paso queda en el ledger.
- Que el chequeo del PR bloquee por un falso positivo del mapa. Mitigación: `warn` por defecto; `fail` solo para lo que se rompe, con la lista de lo que el mapa no pudo leer.

### Tareas de v0.5

| ID | Tarea | Razonamiento | Aceptación | Estado |
| --- | --- | --- | --- | --- |
| T32 | Plan y ADR-0013, ADR-0014 | El pipeline y el motor cambian cómo se integra coyote al trabajo diario | Esta sección, los ADRs y W-0005 | Hecho |
| T33 | `coyote ci impact` | Es el pipeline que pediste en G3: cada PR se ve contra todo el producto | Lee el evento del PR, arma el mapa con los otros repos, escribe el resumen del job, crea o actualiza un solo comentario y sale según la política; pruebas con eventos y API simulados | Hecho |
| T34 | `coyote install --ci github` | Instalarlo debe ser un comando, igual que el gate | Workflow por repo del producto con permisos mínimos, sin forks, y la documentación del secreto; `--check` | Hecho: corre `gate pr`, con las reglas de riesgo del proyecto y un secreto aparte para leer coyote |
| T35 | Motor de workstreams | Sin motor, `supervised` y `autonomous` son palabras | `coyote ws check\|run\|continue\|status`: contrato por paso (A2), artefactos encadenados, puntos de control, tope del plan, rama `ws/` en autónomo; pruebas con un `claude` simulado | Hecho: estado desde el ledger; `autonomous` con la aprobación del plan por hash; retoma la sesión tras la cola del gate |
| T36 | `coyote gate pr` | La aprobación en equipo sin compartir claves | Riesgo por rutas e impacto; un R2, R3 o un cambio que rompe pide la revisión de un dueño; reporte en el PR | Hecho: CODEOWNERS de la rama base; los tres repos del piloto no tienen CODEOWNERS |
| T37 | Corridas medidas | Evidencia antes de dejar correr planes | Tus primeras corridas en el piloto, con su costo y caché en el ledger y en el reporte de G5 | Pendiente: las lanzas tú; condición para `supervised` en el piloto |
| T38 | Verificación y G5 | Evidencia antes de aplicar coyote en los repos de la organización | Pruebas, revisión adversarial y `docs/releases/v0.5.0.md` | Hecho: dos pasadas adversariales (13 hallazgos y 5 más, corregidos); `gate pr` validado en solo lectura sobre el PR #1 de servicios |

G5 autoriza:

1. Instalar el pipeline en los tres repos: los PR de los workflows y el secreto de lectura.
2. `supervised` en los workstreams del piloto.
3. Arrancar v0.6: los demás IDEs, la web v1 y las herramientas de IaC, DevSecOps y SRE.

## v0.5 a v1.0

Siguen el orden de la propuesta. Cada una recibirá su razonamiento completo al cerrar la anterior, con lo aprendido en su gate; así el plan no fija hoy lo que conviene decidir con evidencia.

## Supuestos sobre decisiones abiertas

| Decisión | Supuesto usado en la ejecución | Por qué | Se confirma en |
| --- | --- | --- | --- |
| D2 nube | Sin servidores; GitHub como backend | Menor costo y nada que operar para el MVP || Confirmado en G2 |
| D18 índice local | En memoria con caché en `.coyote/`; SQLite después (ADR-0007) | Sin dependencias nuevas; git es la fuente de verdad || Confirmado en G2 |
| D4 embeddings | Ninguno en v0.2 (BM25 local); locales cuando lleguen | No sacar código a terceros sin autorización || Confirmado en G2 |
| D6 aprobación R2–R3 | CLI con registro firmado en v0.3; pull request con `coyote gate pr` en CI después | La CLI funciona sin red; el PR agrega revisión de equipo | Confirmado en G3 |
| D11 IDE del piloto | Claude Code | Es el de nivel 1 con hooks más completos | Confirmado en G3 |
| D10 visibilidad de costos | Cada persona ve lo suyo, admins todo | Menor exposición por defecto | G5 (llega con el trabajo en equipo) |
| D12 hub | `kredius`, sin crearlo hasta G5 | Aislamiento | G5 |
| D13 nombres de archivo | `README.coyote.md` y `CONTEXT.coyote.md` | Coherente con `README.md` y `AGENTS.md` | Confirmado en G1 |
| D14 alcance de R15 | Commits, PRs, comentarios, docs, releases y autoría del commit | Es lo que pediste | Confirmado en G1 |
| D15 web | Plantillas Go embebidas, solo lectura y solo en 127.0.0.1 | Un solo binario, sin superficie de red || Confirmado en G2 |
| D16 tokens | Entorno, `gh auth token` o llavero del sistema; device flow con OAuth App (ADR-0008) | No dejar secretos en disco || Confirmado en G2 |
| D19 autonomía por defecto | `manual` | Nada con efectos sin aprobación mientras no haya evidencia | Confirmado en G3 |
| D3 runner de `coyote run` | Claude Code headless en la máquina de la persona (ADR-0012) | Aplica el gate y reporta tokens y costo sin runtime nuevo | Confirmado en G4 |
| D5 carriles de gasto | Tope mensual por proyecto en `project.yaml` y tope por corrida; la suscripción o la API key son de la persona | Gasto predecible y visible en el ledger | Confirmado en G4 |
| D24 pipeline de impacto | Workflow en cada repo con token de solo lectura de los otros repos del producto (ADR-0013) | El reporte queda en el PR donde se decide el merge | G5 |
| D25 autonomía en el piloto | `supervised` en los workstreams del piloto; `autonomous` solo después de medir planes supervisados | Un plan autónomo sin evidencia es gasto y riesgo sin control | G5 |
| D26 tokens del pipeline | Un secreto de lectura de los repos del producto y otro para leer coyote si vive en otra cuenta; los equipos de CODEOWNERS se verifican solo con un token con Members: read | Un token fino de GitHub cubre repos de un solo dueño | G5 |
| D27 revisión sin CODEOWNERS | Sin CODEOWNERS, un cambio R2 o R3 lo aprueba cualquier persona que no abrió el PR, y el reporte pide definir dueños | Los tres repos del piloto no tienen CODEOWNERS | G5 |
| D23 unidad de trabajo | Producto multi-repo: servicios, app y backoffice juntos, en un proyecto aparte que lee los tres (ADR-0011) | Un cambio en uno afecta a los otros (G3) | Confirmado en G4 |
| D20 lectura sin aprobación | Lista cerrada de comandos de solo lectura en la herramienta; sin patrones propios del proyecto | Leer no tiene efectos y evita la fatiga | Confirmado en G3 |
| D21 validez de una aprobación | Solo en la máquina donde se dio (firma con clave local), 24 h como máximo | Un registro copiado o fabricado no sirve | Confirmado en G3 |
| D22 gate sin coyote | Falla cerrado: el IDE no ejecuta herramientas en un proyecto con gate | Un gate que se apaga solo no es gate | Confirmado en G3 |
