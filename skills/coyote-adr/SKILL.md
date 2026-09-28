---
name: coyote-adr
description: "Cómo escribir un ADR en formato MADR en coyote/decisions y conectarlo con CONTEXT.coyote.md; obligatorio antes de un cambio de riesgo R2 o R3 (R8)."
---
# ADR en MADR

Archivo `coyote/decisions/ADR-nnnn-<tema>.md`, con el siguiente número libre:

```markdown
---
status: proposed        # proposed, accepted, superseded o deprecated
date: 2026-09-28
deciders: "@persona"
---
# ADR-nnnn: <decisión en una línea>

## Contexto y problema
## Opciones consideradas
## Decisión
## Consecuencias
```

- Al menos dos opciones reales, cada una con su costo.
- La decisión dice el criterio con que se eligió y qué se deja de poder hacer.
- Un ADR aceptado no se edita: se reemplaza por otro (`superseded`).
- Agrega su línea de contexto: `dec|<ámbito>|<decisión en 20 palabras>|ADR-nnnn`.
- Lo acepta una persona; el agente lo deja en `proposed`.
