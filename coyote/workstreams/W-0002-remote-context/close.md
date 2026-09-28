# Cierre W-0002 — v0.2 remote + context

Responsable del resultado: @eramirezhdez (modo manual). Se cierra con la decisión de G2.

## Resultado

| Paso | Entregable | Evidencia |
|------|------------|-----------|
| T12 | plan razonado, ADR-0007 y ADR-0008 | `docs/plan/EXECUTION_PLAN.md`, `coyote/decisions/` |
| T13 | índice, `get context`, `ask`, repos del proyecto | `internal/index`, pruebas de punta a punta con un remoto real |
| T14 | ritmo humano, credenciales y cliente de GitHub | `internal/pace`, `internal/auth`, `internal/github` |
| T15 | `push` y `pull` con gates | pruebas entre dos personas sobre un remoto bare |
| T16 | web FinOps | `internal/web`, capturas en light, dark y móvil |
| T17 | revisión adversarial: 14 hallazgos corregidos | `docs/releases/v0.2.0.md` |

## Consumo

| Concepto | Valor |
|----------|-------|
| Tokens entrada/caché/salida | sin medir |
| Costo USD | sin medir |
| Modelos | sin medir |

v0.2 todavía no se conecta con el proveedor de modelos; desde v0.4 el router llena este cierre solo.
