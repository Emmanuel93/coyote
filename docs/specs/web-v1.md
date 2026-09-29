# Web v1

Estado: nuevo en v0.7.0 (la web v0 llegó en v0.2.0) · Implementación: `internal/web`, `coyote web` · Decisión: ADR-0019

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
- Un evento con fecha futura (un reloj adelantado en otra máquina) no cuenta en ningún periodo, tampoco en el gasto o el estado de un workstream, ni en la web ni en `coyote run --ws`.
- Un hub que no se puede leer deja un aviso y a nadie como admin del hub: por defecto se muestra menos, no más.
- Es visibilidad por defecto, no control de acceso: el ledger está en git y quien lee el repo lo lee. Cada página lo dice. Por eso un admin del proyecto que registra en `repos:` otro proyecto de la organización ve el desglose de su ledger: es lo que vería clonándolo.
- Un repo registrado que resuelve a la carpeta del proyecto, o a otro repo ya leído, no se lee dos veces.

## Superficie

- Escucha solo en loopback; `localhost` se traduce a 127.0.0.1.
- Rechaza un `Host` que no sea loopback (DNS rebinding) y las peticiones en forma absoluta.
- Solo GET y HEAD. No hay formularios: aprobar, rechazar y revocar se hacen en la terminal, donde coyote verifica que decide una persona.
- Mirar no escribe: la cola se lee sin borrar las propuestas vencidas y la clave de firmas no se crea si no existe.
- Timeouts de lectura, escritura e inactividad, y sin el manejador general de `OPTIONS *`.
- Sin JavaScript. La CSP es `default-src 'none'` con estilos en línea; `frame-ancestors 'none'`, `no-referrer` y `no-store`.
- Todo texto del ledger, de los planes o de la cola se escapa. Además, ninguna respuesta lleva controles, separadores de línea Unicode ni caracteres de formato invisibles (los que reordenan un texto): se escriben como su código (`\u202e`).
- Una acción de la cola se muestra en una línea visible y corta; si parece llevar un secreto, se oculta y se revisa con `coyote review <id>`. El marcador de dispensa de secretos no aplica aquí, tampoco anidado.
- Cualquier usuario de la misma máquina puede pedir la página por loopback: en una máquina compartida, `coyote web` muestra a los demás usuarios lo que ve quien la corre. Queda para v1.0 una clave por sesión en la URL.
