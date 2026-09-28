---
status: proposed
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0003: CCF v1 para el ledger y CCF-doc v1 para el contexto por repo

## Contexto y problema
El ledger y el contexto por repo se inyectan a los modelos en cada sesión: su tamaño es costo recurrente. También deben leerse en un diff de git.

## Opciones consideradas
- JSON Lines: estándar, pero repite claves y comillas en cada línea (el doble de tokens estimado).
- Markdown libre: legible, pero sin validación ni tope.
- Líneas posicionales separadas por `|` con vocabulario cerrado y referencias en lugar de contenido.

## Decisión
Líneas posicionales: CCF v1 (11 campos) para el ledger y CCF-doc v1 (frontmatter YAML y una línea por hecho) para `README.coyote.md` y `CONTEXT.coyote.md`, con topes de 300 a 400 y 1 500 tokens validados por la herramienta.

## Consecuencias
Registro compacto, validable y sin conflictos de merge (un archivo por día y persona). Las especificaciones viven en `docs/specs/`.
