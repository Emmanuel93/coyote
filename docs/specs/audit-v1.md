# coyote audit v1

Estado: nuevo en v1.0 · Implementación: `internal/audit`, `coyote audit` · Decisión: ADR-0024

`coyote audit` arma el reporte de auditoría de un mes desde lo que coyote ya registra. Lee el ledger, los registros de `coyote/approvals/` y la historia de git. No usa red ni modelos, y no escribe nada salvo con `--write`.

```
coyote audit [--month AAAA-MM] [--json] [--write]
```

- Sin `--month`, audita el mes actual en UTC.
- Con `--write`, deja el reporte en `coyote/audit/AAAA-MM.md` para que la persona lo revise y le haga commit. Para un agente, `audit` es lectura libre y `audit --write` pide aprobación.
- Sale con 1 si alguna verificación da falla; los avisos no cambian la salida.

## Qué trae el reporte

- **Resumen.** Eventos, personas, agentes, costo, tope del mes y el conteo de fallas y avisos. También el commit sobre el que se armó y la huella (sha256) de lo que leyó del ledger hasta el fin del periodo: las líneas válidas con fecha anterior al fin y las inválidas de días anteriores, con su texto. Regenerar un mes ya auditado da la misma huella, salvo que ese mes o uno anterior se haya reescrito o le hayan llegado líneas con fecha pasada.
- **Eventos.** Por tipo y por actor, con su costo.
- **Aprobaciones y gate.**
  - Aprobaciones y rechazos registrados.
  - Usos de aprobaciones, acciones en la cola y acciones bloqueadas por el gate.
  - Concesiones: aprobaciones con varios usos o que duran más de una hora.
  - Planes `autonomous` autorizados.
  - Cada gate de release decidido en el periodo, con quién aprobó y quién lo registró en el ledger, aunque lo haya registrado en un mes posterior.
- **Costo.** Por persona, agente, modelo y workstream. Los cierres de workstream no se suman, porque resumen otros eventos.
- **Integridad.** Cada verificación da ok, aviso o falla, con su detalle:

| Verificación | Falla | Aviso |
| --- | --- | --- |
| El ledger solo agrega líneas | Un commit de la historia de HEAD (todos, no solo la cadena de primeros padres; un merge, contra cada uno de sus padres) cambia, borra o esconde líneas del ledger, también cambiando el archivo por un symlink o un submódulo; en el árbol de trabajo, un archivo de HEAD falta, ya no es un archivo o tiene líneas cambiadas, aunque se haya preparado un renombre o se use skip-worktree (un checkout con CRLF no cuenta); un archivo que no se pudo leer | No se pudo leer la historia; el clon es superficial; un commit que llegó antes del periodo borra o cambia líneas del ledger (un reporte anterior pudo no verlo, o la historia se reescribió después): dice el mes en que llegó |
| Eventos con fecha anterior a su commit | Un `apr` o `rej` de un agente con una fecha de un mes anterior al del commit que lo trajo | Cualquier otro evento con una fecha más de un día anterior a su commit, como una importación con `--ts` o un squash; un commit con fecha anterior a la de su padre |
| Actor, archivo y autor del commit | — | Una línea de una persona en el archivo de otra; un mismo autor de git registra a nombre de varias personas |
| Aprobaciones registradas por un agente | Un `apr` o `rej` de un agente sin un registro de `coyote/approvals/` que lo respalde con la respuesta de la persona | Lo registró un agente, con la respuesta de la persona |
| Gates de release con su evento en el ledger | — | Una decisión del periodo en `coyote/approvals/` sin su evento `apr`, en cualquier mes |
| Eventos con fecha futura | Más de 5 minutos en el futuro | — |
| Líneas inválidas del ledger | Cualquiera | — |
| Commits del periodo sin atribución a IA (R15) | Un mensaje con atribución, también un trailer como `Assisted-by: <herramienta o modelo>` | — |

El reporte también cuenta cuántos commits del periodo llevan una firma: el encabezado `gpgsig` del commit. La firma se cuenta, no se verifica. Verificarla corre gpg, ssh-keygen o el programa que declare la configuración del repo, y auditar no ejecuta nada ajeno: git corre con esos programas anulados. Quien necesite la verificación usa `git verify-commit` con sus llaves.

Un evento `apr` o `rej` se vincula con los registros que cita (`apr:P-0004`), y todos tienen que respaldarlo, o, si no cita ninguno, con el primer registro libre del gate que nombra al empezar (`G4 aprobado: …`), como se registraron los primeros gates de coyote. Un registro de un gate respalda un solo evento: el primero que nombra su gate al empezar, dice su decisión (`apr` con `approved`, `rej` con `rejected`) y no es anterior a ella. La decisión de un agente solo la respalda el registro de un gate: un agente no aprueba acciones, y un registro sin gate respaldaría cualquier cosa.

Un commit cuenta para el mes en que llegó a la cadena de primeros padres de HEAD, no por su fecha: una rama con fechas viejas, o un commit con la fecha cambiada, cuenta cuando se integra, y lo que trae un merge se mide en el merge. La fecha de llegada no retrocede y nada llega en el futuro: un commit con fecha posterior a ahora cuenta como llegado ahora y se avisa, porque si no uno fechado en 2099 sacaría de todo periodo a los que le siguen. Las verificaciones de commits revisan los que llegaron en el periodo, de cualquier rama, cada uno contra cada uno de sus padres. Los diffs van en texto (`--text`), así que un `.gitattributes` con `-diff` no los esconde, y git corre sin injertos (`info/grafts`) ni objetos de reemplazo. Una línea que solo gana su salto final, o que cambia de lugar, no cuenta como cambiada.

Los vínculos de aprobación se arman en este orden: primero los eventos de las personas y después los de los agentes, cada grupo por fecha, cada uno con el registro libre de fecha más cercana. Citar un registro no adelanta al evento en la fila, y el primero que respalda un registro se lo queda. Así ni un agente que registra antes, ni uno que cita el registro de un gate ya usado, le quita su respaldo a la persona o al evento legítimo.

Lo que viene del ledger o de un registro sale en una sola línea, sin controles ni caracteres invisibles y, en una tabla, con las barras escapadas: un registro no puede abrir una sección del reporte. `--write` guarda el reporte escapado.

- **Controles.** Qué control apoya cada parte:
  - del Anexo A de ISO/IEC 42001:2023: A.6.2.8, A.3.2, A.9.2, A.6.2.6 y A.4.4;
  - del NIST AI RMF 1.0: GOVERN, MEASURE y MANAGE.
- **Límites.** Lo que el reporte no prueba:
  - la identidad es la que declara la máquina;
  - una historia reescrita (amend, rebase, push forzado) no se ve desde HEAD: el commit y la huella del ledger permiten compararla con el reporte anterior;
  - un commit con fecha de un mes ya auditado cuenta cuando llega, no cuando dice su fecha: si borra líneas del ledger, el reporte de ese mes lo da por falla y los de los meses siguientes lo avisan, pero un reporte ya publicado no cambia;
  - un registro de `coyote/approvals/` respalda al primer evento que nombra su gate, sin comparar a qué release se refiere: si nadie más lo usó, un agente puede apoyarse en el registro de otro release del mismo gate, y el reporte lo deja como aviso ("lo registró un agente"), no como falla;
  - las firmas de los commits se cuentan, no se verifican;
  - lo que un agente hizo por fuera de coyote no está;
  - el costo es una estimación;
  - una aprobación del gate vale solo en la máquina que la firmó.

  Es evidencia para una auditoría, no una certificación.
