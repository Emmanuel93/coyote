# Cierre W-0001 — Bootstrap de coyote v0.1

Responsable del resultado: @eramirezhdez (modo manual; se cierra en el gate G1).

## Resultado

| Paso | Entregable | Evidencia |
|------|------------|-----------|
| T1–T2 | entorno aislado, plan razonado, ADR-0001 a ADR-0006 | `docs/plan/EXECUTION_PLAN.md`, `coyote/decisions/` |
| T3–T4 | CCF v1, CCF-doc v1, ledger | `docs/specs/`, pruebas de `internal/ccf`, `internal/ccfdoc`, `internal/ledger` |
| T5–T6 | estándar por capas, filtro de atribución en cinco capas | `standards/default/`, pruebas de `internal/standards`, `internal/attribution` |
| T7 | CLI de 14 comandos | pruebas de punta a punta en `internal/cli` |
| T8–T9 | dogfooding, ejemplo sintético, CI | `coyote standards lint` en verde aquí y en `examples/acme-shop` |
| T10 | revisión adversarial: 13 hallazgos corregidos | `docs/releases/v0.1.0.md` |

## Consumo

| Concepto | Valor |
|----------|-------|
| Tokens entrada/caché/salida | sin medir |
| Costo USD | sin medir |
| Modelos | sin medir |

v0.1 todavía no se conecta con el proveedor de modelos, así que este workstream no tiene su consumo registrado evento por evento. A partir de v0.4, el router registra tokens, modelo y costo de cada step en el ledger, y este cierre se llena solo.
