# Plan de ejecución — Coyote

Estado: v0.1.0 aprobada en G1 (P-0001) · v0.2 en construcción · actualizado 2026-09-28

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
| v0.4 run | Router de modelos, workflows desde el dominio, autonomía, cierres con costo, web v1 | G4 | Presupuesto de API y features piloto |
| v0.5 team | Colaboración, IaC, DevSecOps, SRE, resto de IDEs | G5 | Aplicar Coyote a los repos de la organización |
| v1.0 | Endurecimiento, auditoría, documentación | G6 | Release 1.0 |

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
| T13 | Índice, `get context` y `ask` | Es lo que más ahorra tokens: un agente recibe un paquete acotado en vez de leer todo | Paquete con presupuesto de tokens y referencias; búsqueda con fuentes; pruebas | Pendiente |
| T14 | Ritmo humano y credenciales | Evitar que GitHub trate la cuenta como bot y no dejar secretos en disco | Token bucket persistido, `auth login/status/logout`, cliente con `Retry-After`; pruebas con servidor local | Pendiente |
| T15 | Repos del proyecto, `push` y `pull` | Colaborar como con git, con los gates antes de publicar | `repo add/list`, contexto remoto sin bajar código, push con lint y atribución, pull con resumen | Pendiente |
| T16 | Web FinOps v0 | Ver consumo y costo por proyecto y por persona | `coyote web` con vistas proyecto>usuario y usuario>proyecto, modelos y tokens | Pendiente |
| T17 | Verificación y G2 | Evidencia antes de autorizar el uso del token | Pruebas, demo, revisión adversarial, `docs/releases/v0.2.0.md` | Pendiente |

G2 autoriza: usar tu token contra GitHub (primero solo lectura), registrar o no una OAuth App para el device flow, e indexar en solo lectura los repos de la organización que elijas.

## v0.3 a v1.0

Siguen el orden de la propuesta. Cada una recibirá su razonamiento completo al cerrar la anterior, con lo aprendido en su gate; así el plan no fija hoy lo que conviene decidir con evidencia.

## Supuestos sobre decisiones abiertas

| Decisión | Supuesto usado en la ejecución | Por qué | Se confirma en |
| --- | --- | --- | --- |
| D2 nube | Sin servidores; GitHub como backend | Menor costo y nada que operar para el MVP | G2 |
| D18 índice local | En memoria con caché en `.coyote/`; SQLite después (ADR-0007) | Sin dependencias nuevas; git es la fuente de verdad | G2 |
| D4 embeddings | Ninguno en v0.2 (BM25 local); locales cuando lleguen | No sacar código a terceros sin autorización | G2 |
| D6 aprobación R2–R3 | Pull request de GitHub | Deja rastro con identidad y revisión | G3 |
| D10 visibilidad de costos | Cada persona ve lo suyo, admins todo | Menor exposición por defecto | G4 |
| D12 hub | `kredius`, sin crearlo hasta G5 | Aislamiento | G5 |
| D13 nombres de archivo | `README.coyote.md` y `CONTEXT.coyote.md` | Coherente con `README.md` y `AGENTS.md` | Confirmado en G1 |
| D14 alcance de R15 | Commits, PRs, comentarios, docs, releases y autoría del commit | Es lo que pediste | Confirmado en G1 |
| D15 web | Plantillas Go embebidas, solo lectura y solo en 127.0.0.1 | Un solo binario, sin superficie de red | G2 |
| D16 tokens | Entorno, `gh auth token` o llavero del sistema; device flow con OAuth App (ADR-0008) | No dejar secretos en disco | G2 |
| D19 autonomía por defecto | `manual` | Nada con efectos sin aprobación mientras no haya evidencia | G3 |
