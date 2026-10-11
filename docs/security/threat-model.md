# Modelo de amenazas de coyote

Estado: v1.0 · Decisión: ADR-0023 · Referencias: OWASP Top 10 for LLM Applications 2025, OWASP Top 10 for Agentic Applications 2026 · Revisado: 2026-10-10

coyote pone a agentes de IA a trabajar en repos de verdad. Este documento dice qué protege, de quién, dónde está cada defensa en el código y qué riesgo queda. Cada fila apunta a su prueba: si una defensa no tiene prueba, no cuenta.

## Qué se protege

| Activo | Dónde vive | Qué lo daña |
| --- | --- | --- |
| Credenciales | Llavero del sistema, archivos de secretos del proyecto, `~/.ssh`, la llave local de firmas de aprobaciones | Que un agente las lea, las imprima o las mande a la red |
| Ramas que importan | `main` y las ramas de release de cada repo | Un push o un merge sin revisión |
| Infraestructura y alertas | Terraform, Helm, las reglas de Faro | Un `apply` o una carga de reglas hecha por un agente |
| Integridad del registro | `coyote/ledger/`, `coyote/approvals/`, los planes | Borrar, reescribir o fabricar eventos y aprobaciones |
| Presupuesto | Corridas de agentes | Un loop sin tope o que se sale de su plan |
| Instrucciones de los agentes | `AGENTS.md`, `CLAUDE.md`, `CONTEXT.coyote.md`, skills | Una orden plantada que el agente toma como suya |

## Actores

- **Agente con instrucciones inyectadas.** Leyó algo escrito por otra persona: un documento, un PR, una nota, una página web. Es el caso central: el agente actúa de buena fe con un objetivo ajeno.
- **Autor de un PR malicioso o con la cuenta comprometida.** Controla el contenido del PR, pero no la rama base ni la configuración del pipeline de la base.
- **Otro usuario de la misma máquina.** Puede abrir puertos en loopback. No tiene los permisos de la persona.
- **Herramienta o dependencia comprometida.** Por ejemplo, un binario del pipeline o una skill de terceros.
- **La persona con prisa.** Aprueba sin leer. Es el eslabón que los ataques a la confianza buscan (ASI09).

Fuera del alcance: quien ya tiene los permisos de la persona o de root en su máquina. Con eso puede leer el llavero y el proceso de coyote.

## Fronteras de confianza

1. **IDE → hook → `coyote gate check`.** Toda herramienta de un agente pasa por el gate. Si coyote falta o falla, el hook niega (ADR-0009, docs/specs/install-v1.md).
2. **Repos → índice → paquete de contexto → agente.** Lo citado es dato: se enmarca y se marca (ADR-0023).
3. **PR → pipeline → `gate pr`.** El código del PR se lee como dato y nunca se ejecuta. Las reglas y los dueños salen de la rama base (ADR-0013, ADR-0021).
4. **Clon del hub → proyecto.** El estándar, los admins y el presupuesto salen de un commit, no del árbol de trabajo (ADR-0018).
5. **Terminal de la persona → `coyote approve`.** Solo una persona, en una terminal, con firma local, por 24 h y usos contados (ADR-0009).
6. **Navegador → `coyote web`.** Loopback, Host validado, solo lectura y la clave de la corrida en la ruta (ADR-0019, ADR-0022).
7. **Release → binario de la persona.** Hashes, SBOM y procedencia; reconstruible desde la etiqueta (ADR-0027).

## OWASP Top 10 for LLM Applications 2025

| Riesgo | Cómo llega a coyote | Defensa | Prueba | Queda |
| --- | --- | --- | --- | --- |
| LLM01 Prompt Injection | Un documento, un PR o una nota con órdenes para el agente | Paquetes y `ask` enmarcados; clasificador que marca; `note` y `generate agents` rechazan órdenes; `gate pr` marca instrucciones de agentes y órdenes en documentos (R2); toda acción con efectos espera a una persona | `internal/inject`, `TestContenidoDeLosReposEsDato`, `TestGatePRInstruccionesOrdenesYLedger` | Una orden bien disfrazada pasa el clasificador; su efecto no pasa el gate |
| LLM02 Sensitive Information Disclosure | El agente lee un secreto y lo escribe en su respuesta o en un commit | El gate niega leer archivos de secretos y los comandos que imprimen credenciales, aun con aprobación; `secrets scan` y R18; `gate pr` no muestra valores (ADR-0016) | `TestSecretFilesAreBlocked`, `TestBusquedasQueAlcanzanSecretos`, `TestGatePRSecretos` | Una contraseña de forma libre, si su archivo no está en `secrets.files` |
| LLM03 Supply Chain | Una dependencia o un binario del pipeline cambiado | Una sola dependencia, copiada en `third_party`; gitleaks fijado por hash; release reproducible desde un árbol limpio, con SBOM y procedencia que solo cubren binarios de ese commit; el coyote del pipeline lleva su versión y compara `requires` (ADR-0004, ADR-0021, ADR-0026, ADR-0027) | `internal/install`, `tools/distmeta`, `make verify-dist` en la CI | SLSA L1 no prueba quién construyó el binario |
| LLM04 Data and Model Poisoning | Envenenar lo que el agente sabe del proyecto, o la medida de ese saber | Lo mismo que LLM01; el ledger solo agrega, comparado byte por byte (R3 en `gate pr`); la caché del índice va firmada con HMAC local (ADR-0007); cambiar el golden o la línea base es R2, y `eval` compara sobre las preguntas de la línea base, así que quitar preguntas o agregar copias no tapa una regresión (ADR-0025) | `internal/index`, `TestGatePRInstruccionesOrdenesYLedger`, `TestGatePRLedgerRecodificadoOEscondido`, `TestEvalConLineaBase` | Un documento falso pero sin forma de orden |
| LLM05 Improper Output Handling | La salida de un agente o del ledger llega a una terminal, a la web, a un comentario de PR o al reporte de auditoría | safetext escapa controles, caracteres de formato y marcas invisibles (U+034F, selectores de variación fuera de un emoji, los del mongol) en toda salida; la web escapa HTML y no tiene JavaScript; el comentario del PR escapa Markdown; el reporte de auditoría escapa cada campo de un registro | `internal/safetext` (fuzzing), `internal/web`, `TestComentarioSinInyeccion`, `TestReporteSinInyeccion` | — |
| LLM06 Excessive Agency | El agente hace más de lo que la persona quería | Gate por hash de la acción exacta; modos `manual`, `supervised` y `autonomous` con topes; un agente nunca aprueba, nunca aplica infraestructura y nunca carga alertas (ADR-0009, ADR-0014, ADR-0017, ADR-0020) | `internal/gate`, `TestEscapeDelLoopAutonomo` | La persona que aprueba sin leer (ASI09) |
| LLM07 System Prompt Leakage | Revelar instrucciones o secretos del sistema | coyote no pone secretos en instrucciones: `AGENTS.md` es público a propósito y las credenciales viven en el llavero (ADR-0008) | `TestTokenSources` (`internal/auth`), `TestSecretFilesAreBlocked` | — |
| LLM08 Vector and Embedding Weaknesses | Envenenar o filtrar un índice vectorial | No hay embeddings: el índice es BM25 local, reconstruido desde git (D4, ADR-0007) | `internal/index` | Si llegan embeddings, se revisa esta fila |
| LLM09 Misinformation | Contexto viejo o equivocado | Cada respuesta cita su fuente; golden con métricas en la CI (ADR-0025) | `coyote eval` | El agente puede usar mal una fuente correcta; eso lo mide el juez |
| LLM10 Unbounded Consumption | Corridas sin fin o caras | Topes por corrida, por plan y por mes; ritmo humano en el remoto (ADR-0012) | `internal/cli/run_test.go`, `TestMotorTopeDelPlanYManual` | El costo es la estimación de Claude Code, no la factura |

## OWASP Top 10 for Agentic Applications 2026

| Riesgo | Cómo llega a coyote | Defensa | Prueba | Queda |
| --- | --- | --- | --- | --- |
| ASI01 Agent Goal Hijack | Un texto le cambia el objetivo al agente | Lo de LLM01; el paso y su plan van firmados por hash: cambiar el plan pide otra autorización | `TestMotorAutonomo` | — |
| ASI02 Tool Misuse & Exploitation | Una herramienta legítima usada para otra cosa | El gate lee cada comando como lo correría el shell: tuberías, sustituciones, `-c`, variables, flags pegados; las rutas, limpias y resueltas contra la carpeta del IDE, cada `cd`, `-C` o `--chdir`, los comodines y los enlaces; una bandera que niega cuenta también después de `--`; leer es libre y lo demás se aprueba | `internal/gate`, `TestDashDashYRutasEquivalentes`, `TestRutasDesdeLaCarpetaDelIDE`, fuzzing del lector de shell | Un comando que arma sus palabras al correr baja a aprobación, no a bloqueo |
| ASI03 Identity & Privilege Abuse | El agente usa los permisos de la persona o se aprueba solo | El agente actúa como `@persona/agente`; no aprueba ni desde el IDE ni sin terminal; las aprobaciones valen en la máquina que las firmó; los tokens viven en el llavero; en la auditoría, un registro de un gate respalda un solo evento, que nombra ese gate y dice su decisión | `TestGateFlujoDeAprobacion`, `TestEscapeDelLoopAutonomo`, `TestRespaldoUnoAUno` | La identidad la declara la máquina; `coyote audit` la cruza con los commits |
| ASI04 Agentic Supply Chain | Skills, agentes o configuración de IDE de terceros | Los agentes y skills salen de las definiciones de coyote; la configuración de los IDEs es R3 y las instrucciones de agentes R2 en `gate pr`, también `CLAUDE.local.md`, los prompts, modos y agentes de Copilot, las reglas de Cline, el steering de Kiro y las guías de Junie | `internal/ci`, `TestGatePRInstruccionesOrdenesYLedger`, `TestArchivosDeInstrucciones` | Una skill de terceros instalada a mano no pasa por coyote |
| ASI05 Unexpected Code Execution | El agente baja y corre código, o el pipeline corre el del PR | Bajar y correr pide una persona; los repos del producto se leen con plomería de git, sin filtros ni transportes; `gate pr` nunca ejecuta el PR | `internal/product`, `TestGatePRSecretos` | La persona que aprueba un instalador que se baja y se corre en un solo comando |
| ASI06 Memory & Context Poisoning | Una nota o un documento plantado que vuelve en cada sesión | `note` sin órdenes; las entradas de `AGENTS.md` y el nombre del frontmatter pasan por el clasificador; el ledger solo agrega; la caché del índice firmada | `TestContenidoDeLosReposEsDato`, `TestAgentsMDNoLlevaOrdenesDelFrontmatter` | Los títulos de las reglas del estándar no pasan por el clasificador: una prohibición tiene la forma de lo que prohíbe. El estándar se revisa en su PR como R3 |
| ASI07 Insecure Inter-Agent Communication | Un agente le pasa órdenes a otro | coyote no abre canales entre agentes: un paso le entrega su salida al siguiente como archivo, marcada como información y no como instrucción | `TestMotorSupervisado` | Llega con A2A, después de v1.0 |
| ASI08 Cascading Failures | Una falla se multiplica en el loop | El gate falla cerrado; un hook roto niega; el motor se detiene en su tope o cuando un paso falla | `internal/install`, `TestMotorTopeDelPlanYManual` | — |
| ASI09 Human-Agent Trust Exploitation | Convencer a la persona de aprobar | `review` muestra el comando o el diff exacto y se aprueba por hash: lo que se ve es lo que corre; la web no tiene botón de aprobar; los textos marcados se ven | `TestGateFlujoDeAprobacion`, `internal/web` | Aprobar sin leer |
| ASI10 Rogue Agents | Un agente que se sale de su alcance | `autonomous` solo en su rama `ws/`, con el plan aprobado por hash y con topes; todo queda en el ledger | `TestEscapeDelLoopAutonomo`, `TestMotorAutonomo` | — |

## Lo que queda

- **Gate.**
  - Un comando que arma sus palabras al correr (sustituciones, variables del entorno) pide aprobación en vez de bloquearse.
  - Un comando que llega al clon del hub sin nombrarlo (`cd ..` y después `cd` al hub) también pide aprobación.
  - No se reconoce un target de `make` que llega por la entrada.
  - Un push a `main` se puede aprobar: la persona decide.
- **Secretos.** El escáner reconoce formas conocidas. Una contraseña de forma libre se protege declarando su archivo en `secrets.files` (docs/specs/secrets-v1.md).
- **gitleaks.** No revisa binarios, UTF-16, mensajes de commit ni lo que se quitó con un force-push. Además, una base con `useDefault` hereda las rutas que gitleaks salta (docs/specs/ci-v1.md).
- **Web.** Quien tenga la URL con la clave ve la página mientras corre. Es visibilidad por defecto, no control de acceso: el ledger está en git (ADR-0019).
- **Identidad.** Es la que declara la máquina. Solo un commit firmado la prueba.
- **Clasificador.** Tiene falsos negativos por diseño: marca formas conocidas.
- **Selectores de variación.** Uno después de un símbolo, un signo o un dígito se acepta: es la forma de color de un emoji (⚠️, ‼️, 1️⃣). Dentro de una palabra se marca y no parte una orden.
- **Costo.** Es la estimación de Claude Code.
- **Release.** SLSA Build L1: la procedencia dice cómo se construyó, pero no está firmada.

## Cómo se prueba

- **Pruebas.** Cada fila de las tablas apunta a las suyas. `make check` corre vet, gofmt, las pruebas y el lint del propio estándar. Una prueba recorre el repo y falla si encuentra caracteres invisibles o controles de dirección.
- **Fuzzing nativo de Go.** Corre sobre ocho lectores que reciben texto ajeno:
  - el de shell del gate y el JSON del hook;
  - CCF y CCF-doc;
  - safetext;
  - las consultas y los archivos de SLO;
  - el clasificador.

  El 2026-09-29 corrió 40 s por lector, unos 4.9 millones de entradas en total, sin fallas.
- **Revisiones adversariales.** Hubo revisiones en cada release; sus hallazgos y sus correcciones están en `docs/releases/`. La de v1.0 (2026-10-10) corrió tres revisores independientes sobre la seguridad del gate, la integridad de la auditoría y el release, y después dos más sobre los arreglos; los 52 hallazgos se corrigieron con su prueba, salvo uno que queda abajo como límite.
- **Calibración del clasificador.** El 2026-09-29 corrió en solo lectura sobre 304 documentos de ocho repos reales. Dio 2 marcas, las dos correctas: dos instaladores que se bajan y se corren en un solo comando, en un plan de producto. No dio falsos positivos después de ajustar una regla.
