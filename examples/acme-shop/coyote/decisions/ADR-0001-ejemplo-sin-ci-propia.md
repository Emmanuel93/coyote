---
status: accepted
date: 2026-09-28
deciders: "@ana"
---
# ADR-0001: El ejemplo usa la CI del repo de la herramienta

## Contexto y problema
acme-shop es un proyecto sintético dentro del repo de coyote. R3.ci y R9 piden CI propia, pero un ejemplo anidado no tiene pipeline independiente.

## Opciones consideradas
- Agregar un workflow propio que GitHub no ejecutaría desde `examples/`.
- Relajar R3.ci y R9 a MAY con motivo y caducidad, y validar el ejemplo desde la CI de la herramienta.

## Decisión
La segunda. La CI de coyote corre `coyote -C examples/acme-shop standards lint` en cada cambio. El ajuste vence el 2026-12-31 para revisarlo.

## Consecuencias
El ejemplo muestra cómo se relaja una regla con `reason`, `until` y ADR; `coyote standards diff` lo deja visible.
