---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0024: Auditoría desde el ledger y git (D39)

## Contexto y problema
A1 y A3 dejan en el ledger cada acción con efectos, cada aprobación y cada costo. Revisarlos hoy es leer líneas a mano. La propuesta (C6.5) pide una auditoría tipo ISO 42001 con eventos, aprobaciones, concesiones de autonomía, negaciones y costo por proyecto y por persona.

La lectura encontró tres cosas que un auditor preguntaría:
- 4 de las 6 aprobaciones del ledger de coyote las escribió un agente a nombre de la persona;
- un PR puede reescribir líneas del ledger;
- la identidad del actor es la que declara la máquina.

## Opciones consideradas
- **Exportar el ledger a CSV.** Son datos sin conclusiones.
- **Un reporte con controles y verificaciones de integridad**, generado desde el ledger, las aprobaciones y la historia de git.
- **Una herramienta GRC externa.** Es otro sistema que operar, y sacaría el ledger del repo.

## Decisión
`coyote audit [--month AAAA-MM] [--json] [--write]` lee el ledger, `coyote/approvals/`, los planes de `coyote/workstreams/` y la historia de git con plomería. No usa red ni modelos. Arma un reporte de solo lectura con cuatro partes:

- **Eventos.**
  - Los eventos por tipo y actor.
  - Las aprobaciones y los rechazos, con los gates de release y la respuesta de la persona.
  - Las negaciones del gate.
  - Las concesiones de autonomía: aprobaciones con usos o plazo, y planes `autonomous` aprobados por su hash.
  - El costo por proyecto, persona, agente y modelo, frente al presupuesto del proyecto y del hub. Incluye las corridas que pasaron su tope.
- **Integridad.** Cada verificación da ok, aviso o falla, con la línea y el commit:
  - el ledger solo agrega líneas: ningún commit cambia ni borra una línea existente;
  - el actor de cada línea coincide con la persona del nombre del archivo y con quien hizo el commit;
  - una aprobación o un rechazo registrados por un agente llevan su registro en `coyote/approvals/` con la respuesta de la persona; si no, es falla;
  - no hay eventos con fecha futura ni líneas inválidas;
  - ningún commit del periodo lleva atribución a herramientas de IA (R15);
  - cuántos commits del periodo están firmados.
- **Controles.** Cada parte dice qué control apoya:
  - ISO/IEC 42001:2023, Anexo A: A.6.2.8 (registro de eventos), A.3.2 (roles y responsabilidades), A.9.2 (procesos de uso responsable), A.6.2.6 (operación y monitoreo) y A.4.4 (herramientas);
  - NIST AI RMF 1.0: GOVERN, MEASURE y MANAGE.
- **Límites.** Lo que el reporte no puede probar:
  - la identidad es declarada, salvo en los commits firmados;
  - lo que un agente hizo por fuera de coyote no está en el ledger;
  - el costo es la estimación de Claude Code;
  - una aprobación del gate vale solo en la máquina que la firmó.

El reporte sale en Markdown. Con `--json` sale en JSON, y con `--write` queda en `coyote/audit/AAAA-MM.md` para que la persona lo revise y le haga commit.

## Consecuencias
- Es evidencia para una auditoría, no una certificación. El reporte lo dice.
- El primer reporte de coyote va a marcar sus propias aprobaciones escritas por un agente. Tienen su respuesta en `coyote/approvals/`, así que quedan como aviso y no como falla.
- Firmar los commits convierte la identidad declarada en verificable. El reporte lo recomienda, pero no lo exige.
