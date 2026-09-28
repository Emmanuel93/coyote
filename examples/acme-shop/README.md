# acme-shop

API de pedidos de una tienda ficticia. Es un proyecto sintético para probar coyote: la tienda, las personas (`@ana`) y los números del ledger son inventados.

## Qué muestra

- Dominio primero: `docs/domain.md` → `contracts/openapi.yaml` → `src/` → `test/`.
- Los tres documentos de todo repo: este `README.md`, `README.coyote.md` y `CONTEXT.coyote.md`, más el `AGENTS.md` que genera coyote.
- Un estándar de proyecto que extiende el default con una regla propia (P1) y relaja dos reglas con motivo, caducidad y ADR.
- Un ledger con eventos de ejemplo y su costo.
- Un workstream de ejemplo en `coyote/workstreams/W-0001-alta-de-pedidos/`.

## Probar

```sh
npm test                                    # Node 22.6 o superior, sin dependencias
coyote -C examples/acme-shop status         # desde la raíz del repo de coyote
coyote -C examples/acme-shop standards lint
coyote -C examples/acme-shop standards diff
coyote -C examples/acme-shop log
```
