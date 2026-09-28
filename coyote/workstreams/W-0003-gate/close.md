# Cierre W-0003 — v0.3 gate

Responsable del resultado: @eramirezhdez (modo manual). Se cierra con la decisión de G3.

## Resultado

| Paso | Entregable | Evidencia |
|------|------------|-----------|
| T18 | plan razonado, ADR-0009 y ADR-0010 | `docs/plan/EXECUTION_PLAN.md`, `coyote/decisions/` |
| T19 | gate por hash: lectura libre, bloqueos duros, hash exacto, falla cerrado | `internal/gate`, 138 decisiones en tabla de pruebas |
| T20 | cola y registros de aprobación firmados | `internal/approval`, flujos completos y usos en paralelo |
| T21 | 14 agentes y 8 skills | `agents/`, `skills/`, `internal/agents` |
| T22 | `coyote install` y `doctor --ide` para Claude Code y Cursor | `internal/install`, fusión sin pisar configuración |
| T23 | identidad de agente y autonomía | roster, identidad del IDE en el ledger, sesiones de agente sin poder aprobar |
| T24 | revisión adversarial: 4 hallazgos y 4 notas corregidos | `docs/releases/v0.3.0.md` |

## Consumo

| Concepto | Valor |
|----------|-------|
| Tokens entrada/caché/salida | sin medir |
| Costo USD | sin medir |
| Modelos | sin medir |

Desde v0.4 el router llena este cierre solo.
