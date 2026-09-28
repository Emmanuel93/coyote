---
name: coyote-log
description: "Cómo escribir eventos del ledger en CCF v1 - once campos, vocabulario cerrado de tipos, referencias clave:valor, tokens y costo."
---
# Eventos del ledger (CCF v1)

Una línea por evento, once campos separados por `|`:

```
ts|actor|project|repo|type|scope|what|refs|tokens|cost|status
2026-09-28T01:00Z|@ana/coyote-dev|W-0003|tienda|feat|pagos|captura con reintentos|adr:ADR-0002|12.4k/8.7k/1.1k|$0.009+$0.011|ok
```

- `ts`: UTC al minuto. `actor`: `@persona` o `@persona/agente`.
- `project`: el workstream (`W-0003`) o `-`.
- `type`, vocabulario cerrado: feat, fix, doc, refactor, test, chore, ci, build, perf, adr, spec, note, idx, rev, apr, rej, gate, attr, conf, run, ses, cost, close, init, rel, plan, ask, sync.
- `what`: de 1 a 12 palabras, sin `|` ni saltos de línea.
- `refs`: `clave:valor` separados por espacio: `sha:`, `doc:`, `adr:`, `ws:`, `apr:`, `pr:`, `art:`, `model:`.
- `tokens`: entrada/caché/salida (`12.4k/8.7k/1.1k`); `cost`: `$entrada+$salida`; `-` si no se midió.
- `status`: ok, pend, fail o skip.

Se escribe con `coyote record <type> "<qué>" --scope S --ws W --refs "..." --tokens ... --cost ...`; `coyote commit` agrega el suyo. El ledger solo agrega líneas: nunca se edita.
