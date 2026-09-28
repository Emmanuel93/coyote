# CCF-doc v1 — README.coyote.md y CONTEXT.coyote.md

Estado: estable desde v0.1.0 · Implementación: `internal/ccfdoc`, `internal/agentsmd` · Decisión: ADR-0003

## Para qué

Todo repo lleva tres documentos:

| Archivo | Para quién | Contenido |
|---------|-----------|-----------|
| `README.md` | personas | lo de siempre, en dos pantallas como máximo (R10) |
| `README.coyote.md` | agentes y la base de conocimiento | identidad del repo: propósito, cómo correrlo, módulos, dependencias |
| `CONTEXT.coyote.md` | agentes y personas | contexto vivo: invariantes, decisiones, trampas, términos |

Los dos documentos coyote tienen formato fijo para que un agente los lea completos con pocos tokens y para que `coyote` los valide. `AGENTS.md` se genera a partir de ellos (R7) y es lo que cargan los IDEs; `CLAUDE.md` solo importa `@AGENTS.md`.

## Estructura común

```
---
coyote: 1
repo: <nombre>
...otras claves...
---
# Título opcional
tipo|campo|campo...
```

- El frontmatter YAML es obligatorio y lleva `coyote: 1` y `repo`.
- Después, una entrada por línea: `tipo|campos`, separados por `|`. Las líneas vacías y las que empiezan con `#` se ignoran.
- Un campo no puede contener `|`; `coyote note` lo cambia por `/`.

## README.coyote.md

Frontmatter:

| Clave | Obligatoria | Ejemplo |
|-------|-------------|---------|
| coyote | sí | `1` |
| repo | sí | `acme-shop` |
| type | sí | `backend`, `mobile`, `web`, `infra`, `hub`, `tool`, `library`, `docs`, `data` u `other` |
| stack | no | `[typescript, postgres]` |
| owners | recomendada | `["@ana"]` |
| standards | no | `{ profile: backend, waive: [{id: R10, reason: "README heredado", adr: ADR-0003}] }` |

Entradas:

| Tipo | Campos | Ejemplo |
|------|--------|---------|
| purpose | texto (máx. 30 palabras), exactamente una | `purpose\|API de pedidos de la tienda demo` |
| run | comando | `run\|npm run dev` |
| test | comando | `test\|npm test` |
| build | comando | `build\|npm run build` |
| entry | ruta\|descripción | `entry\|src/server.ts\|arranque HTTP` |
| mod | nombre\|descripción\|ruta[\|interfaz] | `mod\|orders\|alta y consulta de pedidos\|src/orders\|OrdersService` |
| docs | ruta\|descripción | `docs\|docs/domain.md\|modelo de dominio` |
| dep | nombre\|para qué | `dep\|payments-api\|captura de pagos` |

Las descripciones llevan 20 palabras como máximo. Faltar `run` o `test`, o dejar un campo con TODO, es advertencia; lo demás es error.

Tope: 300 tokens, o 400 si hay entradas `mod`.

## CONTEXT.coyote.md

Frontmatter: `coyote: 1`, `repo` y `updated` (fecha que `coyote note` mantiene).

Entradas: `tipo|ámbito|texto|ref`

| Tipo | Significado |
|------|-------------|
| inv | invariante que no se rompe |
| dec | decisión vigente |
| gap | trampa o brecha conocida |
| how | cómo hacer algo |
| term | término del dominio |
| risk | riesgo abierto |
| todo | pendiente |

- `ámbito`: módulo o tema, sin espacios (`pedidos/diseño`).
- `texto`: 20 palabras como máximo.
- `ref`: dónde está el detalle (`src/orders/service.ts#L40`, `ADR-0002`, `W-0003`) o `-`.

Tope: 1 500 tokens. Cuando se llena, las entradas viejas se archivan en `coyote/decisions/` o en el workstream que les dio origen y se deja la referencia.

Ejemplo:

```
---
coyote: 1
repo: acme-shop
updated: 2026-09-28
---
inv|pedidos|un pedido se confirma solo con pago capturado|docs/domain.md
dec|api|REST con OpenAPI 3.1 como fuente de tipos|ADR-0002
gap|pagos|el sandbox del proveedor responde 200 aun con tarjeta rechazada|-
term|dominio|SKU es la unidad vendible; producto agrupa SKUs|docs/domain.md
```

## Estimación de tokens

Los topes usan el estimador conservador de ADR-0006: el mayor entre `caracteres / 3.5` y `palabras × 1.3 + signos × 0.5`. Sobreestima a propósito: es preferible que un documento choque con el tope un poco antes que después. En v0.2 se contrasta con el conteo real de la API.

## Validación y comandos

- `coyote doctor` y `coyote status` muestran el estado y el consumo de cada documento.
- `coyote standards lint` aplica R14 (existen y son válidos).
- `coyote commit` se niega a hacer commit si alguno tiene errores (R14), salvo con `--no-verify`.
- `coyote note "<texto>" --type inv --scope pedidos --ref docs/domain.md` agrega una entrada válida, actualiza `updated`, registra el evento en el ledger y regenera `AGENTS.md` si lo generó coyote.

## AGENTS.md

`coyote generate agents` arma `AGENTS.md` con el propósito, cómo correr y probar, módulos, el contexto vivo, las reglas MUST que aplican al perfil y el protocolo de trabajo. Empieza con un marcador; un `AGENTS.md` sin marcador es ajeno y no se sobrescribe sin `--force`. `coyote generate agents --check` falla si no está al día, para usarlo en la CI.
