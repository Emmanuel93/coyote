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

- **Resumen.** Eventos, personas, agentes, costo, tope del mes y el conteo de fallas y avisos. También el commit sobre el que se armó y la huella (sha256) del ledger hasta el fin del periodo, tal como está en HEAD: regenerar un mes ya auditado da la misma huella, salvo que ese mes o uno anterior se haya reescrito o le hayan llegado líneas con fecha pasada.
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
| El ledger solo agrega líneas | Un commit de la historia de HEAD (todos, no solo la cadena de primeros padres; un merge, contra cada uno de sus padres) cambia, borra o esconde líneas del ledger, también cambiando el archivo por un symlink o un submódulo; en el árbol de trabajo, un archivo de HEAD falta, ya no es un archivo o tiene líneas cambiadas, aunque se haya preparado un renombre o se use skip-worktree (un checkout con CRLF no cuenta); un archivo que no se pudo leer | No se pudo leer la historia; el clon es superficial |
| Eventos con fecha anterior a su commit | Un `apr` o `rej` de un agente con una fecha de un mes anterior al del commit que lo trajo | Cualquier otro evento con una fecha más de un día anterior a su commit, como una importación con `--ts` o un squash; un commit con fecha anterior a la de su padre |
| Actor, archivo y autor del commit | — | Una línea de una persona en el archivo de otra; un mismo autor de git registra a nombre de varias personas |
| Aprobaciones registradas por un agente | Un `apr` o `rej` de un agente sin un registro de `coyote/approvals/` que lo respalde con la respuesta de la persona | Lo registró un agente, con la respuesta de la persona |
| Gates de release con su evento en el ledger | — | Una decisión del periodo en `coyote/approvals/` sin su evento `apr`, en cualquier mes |
| Eventos con fecha futura | Más de 5 minutos en el futuro | — |
| Líneas inválidas del ledger | Cualquiera | — |
| Commits del periodo sin atribución a IA (R15) | Un mensaje con atribución | — |

El reporte también cuenta cuántos commits del periodo van firmados.

Un evento `apr` o `rej` se vincula con los registros que cita (`apr:P-0004`), y todos tienen que respaldarlo, o, si no cita ninguno, con el primer registro libre del gate que nombra al empezar (`G4 aprobado: …`), como se registraron los primeros gates de coyote. Un registro de un gate respalda un solo evento: el primero que nombra su gate al empezar, dice su decisión (`apr` con `approved`, `rej` con `rejected`) y no es anterior a ella. La decisión de un agente solo la respalda el registro de un gate: un agente no aprueba acciones, y un registro sin gate respaldaría cualquier cosa.

Los commits se leen todos y la fecha se filtra después: git deja de recorrer con `--since` en el primer commit con fecha vieja. Los diffs van en texto (`--text`), así que un `.gitattributes` con `-diff` no los esconde.

Lo que viene del ledger o de un registro sale en una sola línea, sin controles ni caracteres invisibles y, en una tabla, con las barras escapadas: un registro no puede abrir una sección del reporte. `--write` guarda el reporte escapado.

- **Controles.** Qué control apoya cada parte:
  - del Anexo A de ISO/IEC 42001:2023: A.6.2.8, A.3.2, A.9.2, A.6.2.6 y A.4.4;
  - del NIST AI RMF 1.0: GOVERN, MEASURE y MANAGE.
- **Límites.** Lo que el reporte no prueba:
  - la identidad es la que declara la máquina;
  - una historia reescrita (amend, rebase, push forzado) no se ve desde HEAD: el commit y la huella del ledger permiten compararla con el reporte anterior;
  - lo que un agente hizo por fuera de coyote no está;
  - el costo es una estimación;
  - una aprobación del gate vale solo en la máquina que la firmó.

  Es evidencia para una auditoría, no una certificación.
