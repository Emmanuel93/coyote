---
name: coyote-card
description: "Cómo escribir README.coyote.md (identidad del repo, 300 tokens) y entradas de CONTEXT.coyote.md (contexto vivo) en formato CCF-doc."
---
# README.coyote.md y CONTEXT.coyote.md

Ambos empiezan con frontmatter YAML (`coyote: 1`, `repo`) y siguen con una entrada por línea: `tipo|campos`.

README.coyote.md (tope: 300 tokens, 400 si tiene módulos):

```
purpose|API de pedidos de la tienda demo
run|npm run dev
test|npm test
mod|orders|alta y consulta de pedidos|src/orders|OrdersService
docs|docs/domain.md|modelo de dominio
dep|payments-api|captura de pagos
```

CONTEXT.coyote.md: `tipo|ámbito|texto|ref`, con texto de 20 palabras como máximo.

| Tipo | Úsalo para |
|------|-----------|
| inv | algo que nunca se rompe |
| dec | una decisión vigente (ref: el ADR) |
| gap | una trampa conocida |
| how | cómo se hace algo |
| term | un término del dominio |
| risk | un riesgo abierto |
| todo | un pendiente |

- `ámbito` sin espacios (`pagos/reembolsos`). `ref`: `ruta#Llínea`, `ADR-nnnn`, un sha o `-`.
- Se agrega con `coyote note "<texto>" --type inv --scope pagos --ref docs/dominio.md`.
- `coyote doctor` valida ambos; `coyote generate agents` regenera AGENTS.md desde ellos.
