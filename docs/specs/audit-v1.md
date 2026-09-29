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

- **Resumen.** Eventos, personas, agentes, costo, tope del mes y el conteo de fallas y avisos.
- **Eventos.** Por tipo y por actor, con su costo.
- **Aprobaciones y gate.**
  - Aprobaciones y rechazos registrados.
  - Usos de aprobaciones, acciones en la cola y acciones bloqueadas por el gate.
  - Concesiones: aprobaciones con varios usos o que duran más de una hora.
  - Planes `autonomous` autorizados.
  - Cada gate de release, con quién aprobó y quién lo registró en el ledger.
- **Costo.** Por persona, agente, modelo y workstream. Los cierres de workstream no se suman, porque resumen otros eventos.
- **Integridad.** Cada verificación da ok, aviso o falla, con su detalle:

| Verificación | Falla | Aviso |
| --- | --- | --- |
| El ledger solo agrega líneas | Un commit (un merge, contra su primer padre) o el árbol de trabajo cambia o borra líneas del ledger | No se pudo leer la historia |
| Actor, archivo y autor del commit | — | Una línea de una persona en el archivo de otra; un mismo autor de git registra a nombre de varias personas |
| Aprobaciones registradas por un agente | Un `apr` o `rej` de un agente sin el registro de `coyote/approvals/` con la respuesta de la persona | Lo registró un agente, con la respuesta de la persona |
| Gates de release con su evento en el ledger | — | Una decisión del periodo en `coyote/approvals/` sin su evento `apr` |
| Eventos con fecha futura | Más de 5 minutos en el futuro | — |
| Líneas inválidas del ledger | Cualquiera | — |
| Commits del periodo sin atribución a IA (R15) | Un mensaje con atribución | — |

El reporte también cuenta cuántos commits del periodo van firmados.

Un evento `apr` sin referencia `apr:` se vincula por el gate que nombra al empezar (`G4 aprobado: …`). Así se registraron los primeros gates de coyote.

- **Controles.** Qué control apoya cada parte:
  - del Anexo A de ISO/IEC 42001:2023: A.6.2.8, A.3.2, A.9.2, A.6.2.6 y A.4.4;
  - del NIST AI RMF 1.0: GOVERN, MEASURE y MANAGE.
- **Límites.** Lo que el reporte no prueba:
  - la identidad es la que declara la máquina;
  - lo que un agente hizo por fuera de coyote no está;
  - el costo es una estimación;
  - una aprobación del gate vale solo en la máquina que la firmó.

  Es evidencia para una auditoría, no una certificación.
