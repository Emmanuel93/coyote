---
status: proposed
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0006: Estimador local de tokens provisional

## Contexto y problema
Los topes de `README.coyote.md` y `CONTEXT.coyote.md` se validan en cada commit y en CI, sin red y sin costo.

## Opciones consideradas
- Endpoint `count_tokens` de la API: exacto, pero requiere red y API key en cada validación.
- Tokenizador local exacto: no está publicado para los modelos actuales.
- Heurística conservadora local, contrastada después con `count_tokens`.

## Decisión
Heurística: el mayor entre caracteres entre 3.5 y palabras por 1.3 más signos por 0.5. En v0.2 se mide su desviación contra `count_tokens` sobre documentos reales.

## Consecuencias
Los topes pueden dispararse un poco antes que con el conteo real. Rara vez después, pero es una heurística: la medición de v0.2 confirma o ajusta los factores.
