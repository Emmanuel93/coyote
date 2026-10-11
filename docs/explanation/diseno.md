# Por qué coyote es así

Cuando un equipo trabaja con agentes de IA, pierde cuatro cosas. Lo que el equipo sabe, porque cada sesión de un agente empieza de cero. El control, porque un agente con una terminal hace en un minuto lo que una persona revisaría en una hora. El costo, porque se ve en la factura y no en el trabajo. Y el estándar, porque cada IDE trae sus propias reglas. Cada decisión de coyote responde a una de esas pérdidas sin agregar un servidor. Este documento explica el porqué; el cómo está en las [guías](../guides/README.md) y cada decisión, con sus opciones, en los ADRs de `coyote/decisions/`.

## Todo vive en git

El contexto, el ledger, el estándar, las decisiones, los planes y los registros de aprobación son archivos del repo. Se revisan en un PR, viajan con cada clon y funcionan sin red. No hay base de datos que respaldar ni servicio que mantener.

- El índice de búsqueda es una caché que se reconstruye desde git, y `.coyote/` nunca se versiona (ADR-0007).
- coyote usa el git del sistema, así respeta la identidad, la firma y las credenciales de cada persona, y nunca guarda credenciales de git (ADR-0002, ADR-0008).

El costo: lo que otra persona registró se ve después de un `pull`, no en tiempo real.

## El ledger solo agrega

Cada evento es una línea de once campos en un archivo por día y por persona (ADR-0003). Dos personas nunca editan el mismo archivo, así que el ledger no tiene conflictos de merge. Todo lo demás se deriva de ahí: el costo, el estado de un workstream (ADR-0014) y la auditoría (ADR-0024).

Un error se corrige con un evento nuevo, nunca editando. El error queda a la vista, y eso es lo que se busca: un registro que se puede reescribir no sirve como evidencia. Por eso `gate pr` marca R3 un ledger reescrito y `coyote audit` lo reporta como falla.

## Las personas deciden; los agentes proponen

El gate vive en el hook previo de cada IDE. Leer es libre. Lo que tiene efectos espera a que una persona apruebe esa acción exacta, identificada por su hash (ADR-0009).

- **Por qué el hash.** Aprobar "el agente puede correr comandos" no es una decisión que alguien pueda revisar. Aprobar `make test` en esta carpeta sí lo es. Si el agente cambia una letra, la acción vuelve a la cola.
- **Por qué un agente no aprueba.** Aprobar exige una terminal interactiva fuera de una sesión de agente, y la aprobación se firma con una llave de la máquina. Vale en esa máquina, por un tiempo y un número de usos.
- **Por qué falla cerrado.** Si coyote falta, el hook está roto o el proyecto no se puede leer, el gate niega. Un gate que deja pasar cuando falla no es un gate.
- **Lo que nunca pasa,** ni con aprobación: leer credenciales, aplicar infraestructura en un ambiente revisado, editar la configuración del gate, escribir en la carpeta de git o en el clon del hub.
- **El nivel se mide, no se declara.** Que el hook exista no prueba que el IDE lo respete. Un canario lo mide en cada máquina, y un IDE que no lo respeta no hace tareas de riesgo fuera de ramas revisadas (ADR-0015).

El costo es fricción. Las concesiones (`--uses`, `--for`) y los modos `supervised` y `autonomous` la bajan, pero el gate aplica igual en los tres modos.

## Lo que se lee es dato

Un agente lee repos, documentos, PRs y páginas web. Cualquiera de esas fuentes puede traer una orden escrita por otra persona. Eso no se puede evitar del todo (ADR-0023), así que coyote lo hace visible y frena sus efectos:

- lo que cita de los repos va enmarcado como dato, y lo que tiene forma de orden para un agente lleva ⚠;
- una nota con forma de orden no entra a `CONTEXT.coyote.md` ni a `AGENTS.md`, porque un agente toma ese archivo como instrucción;
- en un PR, cambiar las instrucciones de los agentes es R2, y esconder texto con caracteres invisibles o de dirección es R2 o R3.

Si aun así una orden convence al agente, lo que intente hacer con efectos pasa por el gate. El [modelo de amenazas](../security/threat-model.md) dice qué queda abierto.

Las revisiones adversariales de cada release atacan justamente estas fronteras con reproducciones concretas. La de v1.0 encontró, por ejemplo, que un `--` delante de una bandera la escondía del gate y que una ruta con `//` no se reconocía. Las dos se cerraron con su prueba antes de publicar.

## Un cambio no se juzga con sus propias reglas

En un PR, coyote toma de la rama base todo lo que decide: el workflow, las reglas de riesgo, el CODEOWNERS, las dispensas de secretos, la configuración de gitleaks y los SLOs con los que compara. El código del PR se lee como dato y nunca se ejecuta (ADR-0013). Así un PR no puede relajar la regla que lo revisa, y el autor nunca cuenta como su propio revisor.

Por la misma razón, un cambio al hub rige cuando llega a `main` por su PR, no cuando alguien lo edita en su clon (ADR-0018).

## Sin modelos en el camino crítico

El gate, la búsqueda, los paquetes de contexto, `gate pr`, la evaluación y la auditoría no llaman a ningún modelo. Tampoco usan la red, salvo `gate pr`, que lee de GitHub las revisiones y los autores del PR. Una decisión de seguridad no puede depender de algo que se deja persuadir. Además así cada decisión se repite igual, no cuesta nada y se prueba con tablas y fuzzing.

Los modelos entran solo en `coyote run` y `coyote ws`, que corren Claude Code con topes y con el gate puesto (ADR-0012).

El costo: una búsqueda BM25 entiende menos que un modelo. Por eso se mide. `coyote eval` compara la recuperación con un golden de preguntas reales (ADR-0025). La primera medida mostró que el paquete de contexto cubría el 1.7 % de lo esperado. Después de reordenarlo, cubre el 93.3 %, y no se usó ningún modelo para medirlo.

## El costo tiene topes

Cada corrida tiene topes de turnos y de dólares. Cada plan y cada mes tienen su tope. Al 80 % del mes, el router baja un nivel de modelo, salvo en R3, y al 100 % no corre (ADR-0012). Cada persona ve su gasto y los totales; el desglose por persona lo ven los admins (ADR-0019).

El costo que registra coyote es la estimación de Claude Code, no la factura. Los reportes lo dicen.

## Un estándar por capas

El estándar se arma en capas: `coyote:default`, el hub de la organización y el proyecto. Un proyecto agrega reglas y relaja las que no le aplican, siempre con un motivo y, si quiere, una fecha de vencimiento. Redefinir una regla no le baja el nivel, y el cambio queda anotado en el lint. Los chequeos son código: corren en la CI con resultados deterministas, y los scripts propios solo con `--scripts`.

## Los entregables son de la persona

Los commits, los PRs, los documentos y los releases no llevan atribución a herramientas de IA (R15, ADR-0005). La persona que revisa y firma responde por el contenido. Qué agente hizo qué, con qué modelo y a qué costo queda en el ledger. Lo aplican varias capas: la configuración del IDE, el gate, `coyote commit`, el hook `commit-msg` y el lint en la CI.

## Pocas dependencias y builds verificables

coyote es un binario de Go con la biblioteca estándar y una sola dependencia, go-yaml, copiada en el repo (ADR-0001, ADR-0004). No necesita nada más instalado para correr. El build es reproducible y cada binario trae su SBOM y su procedencia: se verifica reconstruyéndolo (ADR-0027).

Desde 1.0 coyote sigue versionado semántico. Los formatos, los comandos y el estándar no cambian de sentido dentro de 1.x. La excepción es el gate, que puede negar más en cualquier versión porque es una corrección de seguridad (ADR-0026, [compatibilidad](../reference/compatibilidad.md)).

## Lo que coyote no hace

- No es un servicio: todo lo que registra queda en el repo y en tu máquina.
- No aplica infraestructura ni carga alertas: lee el plan y genera las reglas, y la persona o un pipeline las aplican.
- No ejecuta el código de un PR ni de los repos que lee.
- No certifica: su auditoría es evidencia.
- No reemplaza la revisión de código: dice qué revisar y quién lo tiene que aprobar.
