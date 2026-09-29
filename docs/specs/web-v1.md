# Web v1

Estado: nuevo en v0.7.0 (la web v0 llegó en v0.2.0); clave por sesión desde v1.0 · Implementación: `internal/web`, `coyote web` · Decisiones: ADR-0019, ADR-0022

`coyote web` sirve la operación del proyecto en el navegador: costos, presupuesto, workstreams, gate, SLOs y, para admins, la organización. Todo se lee en cada petición del ledger, de los planes, de la cola del gate y del hub; la web no guarda nada ni agrega comandos.

## Secciones

| Ruta | Muestra | Fuente |
| --- | --- | --- |
| `/` | Costos por proyecto, persona, modelo o agente, en 7, 30 o 90 días o todo | el ledger del proyecto y de sus repos registrados |
| `/presupuesto` | Gasto del mes (UTC) contado como lo cuenta `coyote run` (el ledger del proyecto), tope del proyecto, proyección a fin de mes, tu parte, tope por corrida y de la organización, workstreams abiertos con su tope | `project.yaml`, `router.yaml`, el hub y los planes |
| `/workstreams` | Cada plan con el estado de sus pasos, corridas y costo | `coyote/workstreams/*/plan.yaml` y el ledger |
| `/gate` | La cola, las aprobaciones firmadas en esta máquina y los gates de release | `.coyote/proposals`, `coyote/approvals` y el ledger |
| `/slo` | Objetivos, presupuesto de error y estado de las alertas generadas | `coyote/slo/*.yaml` (slo-v1) |
| `/org` | Solo admins del hub: gasto del mes de cada proyecto de la organización y por persona, contra el tope del hub | `hub.yaml` y el ledger del clon de cada proyecto |
| `/api/usage` | Los costos en JSON, con la misma visibilidad | el ledger |

## Visibilidad (D10)

- La persona es la identidad de git de quien corre `coyote web`, como en el ledger: `COYOTE_USER`, `git config coyote.user` o el correo. Es la identidad que declara la máquina, no una autenticación: dos correos que se reducen al mismo identificador (`anaé@…` y `ana@…` dan `@ana`) son la misma persona para coyote.
- Los admins del proyecto son los de `hub.yaml` y los de `admins:` en `project.yaml`: ven el desglose por persona del proyecto.
- La organización (`/org` y su gasto en el presupuesto) la ven solo los admins de `hub.yaml`, que salen del commit que rige. Un admin del proyecto recibe 403: `project.yaml` se edita en el proyecto, el hub se revisa en su PR.
- Una persona que no es admin ve sus eventos con desglose y los totales del proyecto sin desglose, tampoco de modelos.
- El gasto de un workstream lo ven su dueño (`owner` del plan) y los admins: un plan suele ser de una sola persona.
- Un evento con fecha futura (un reloj adelantado en otra máquina) no cuenta en ningún periodo de costos. En un workstream, una aceptación con fecha futura no deja avanzar el plan; su gasto, sus rechazos y su cierre sí cuentan, en la web y en `coyote run --ws`, para que un reloj adelantado no afloje el tope.
- Un hub que no se puede leer deja un aviso y a nadie como admin del hub: por defecto se muestra menos, no más.
- Es visibilidad por defecto, no control de acceso: el ledger está en git y quien lee el repo lo lee. Cada página lo dice. Por eso un admin del proyecto que registra en `repos:` otro proyecto de la organización ve el desglose de su ledger: es lo que vería clonándolo.
- Un repo registrado que resuelve a la carpeta del proyecto, o a otro repo ya leído, no se lee dos veces.

## Superficie

- Escucha solo en loopback; `localhost` se traduce a 127.0.0.1.
- **Clave por sesión (ADR-0022).** Cada corrida genera una clave de 256 bits y la imprime solo en la terminal, dentro de la URL: `http://127.0.0.1:7410/s/<clave>/`.
  - Todas las rutas cuelgan de ese prefijo, también `api/usage`.
  - Una ruta sin la clave correcta responde 404, sin decir por qué. La clave se compara en tiempo constante.
  - La clave no se escribe en disco ni en el ledger, y no está en los argumentos del proceso. Cambia en cada arranque, así que un marcador no sirve de una corrida a otra.
  - Va en la ruta y no en una cookie: las cookies de 127.0.0.1 viajan a cualquier puerto de la máquina (RFC 6265, §8.5).
- Rechaza un `Host` que no sea loopback (DNS rebinding) y las peticiones en forma absoluta.
- Solo GET y HEAD. No hay formularios: aprobar, rechazar y revocar se hacen en la terminal, donde coyote verifica que decide una persona.
- Mirar no escribe: la cola se lee sin borrar las propuestas vencidas y la clave de firmas no se crea si no existe.
- Timeouts de lectura, escritura e inactividad, y sin el manejador general de `OPTIONS *`.
- Sin JavaScript. La CSP es `default-src 'none'` con estilos en línea; `frame-ancestors 'none'`, `no-referrer` y `no-store`.
- Todo texto del ledger, de los planes o de la cola se escapa. Además, ninguna respuesta lleva controles, separadores de línea Unicode ni caracteres de formato invisibles (los que reordenan un texto): se escriben como su código (`\u202e`).
- Una acción de la cola se muestra en una línea visible y corta; si parece llevar un secreto, se oculta y se revisa con `coyote review <id>`. El marcador de dispensa de secretos no aplica aquí, tampoco anidado.
- Quien tenga la URL con la clave ve lo que ve quien corre `coyote web` hasta que se cierre. Quien tenga los permisos de esa persona, o de root, en la misma máquina puede leer el proceso: eso queda fuera del alcance.
