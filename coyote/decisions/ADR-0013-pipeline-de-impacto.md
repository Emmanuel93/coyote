---
status: proposed
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0013: Pipeline de impacto del producto en cada PR

## Contexto y problema
v0.4 ve un cambio contra los tres repos del producto, pero solo cuando alguien corre `coyote impact` en su máquina. En G3 se pidió que el pipeline lo hiciera: que cada cambio se vea de manera holística antes del merge. Los repos son privados y viven por separado; el mapa de uno necesita leer a los otros.

## Opciones consideradas
- Un workflow en cada repo que, en el PR, trae el proyecto del producto y los otros repos con un token de solo lectura, corre el impacto del diff y lo reporta en el PR.
- Un workflow central en el proyecto del producto, disparado desde cada repo. Suma un salto y un permiso de escritura entre repos.
- Un hook local antes del push. Depende de cada persona y no deja rastro en el PR.

## Decisión
Un workflow por repo del producto, generado por `coyote install --ci github`. El job:

1. corre en `pull_request` de ramas del mismo repo, nunca de forks, con `contents: read` y `pull-requests: write`;
2. trae el proyecto del producto y los otros repos con un secreto de solo lectura;
3. corre `coyote ci impact`, que lee el evento, arma el mapa, analiza el diff del PR (`base...head`) y escribe el reporte en el resumen del job;
4. crea o actualiza un solo comentario en el PR, identificado por un marcador.

Política configurable: `warn` (por defecto) o `fail`, que hace fallar el chequeo si el cambio rompe a un consumidor.

## Consecuencias
- Cada PR muestra qué toca en los otros repos antes del merge, sin modelo y sin costo.
- Cada repo guarda un secreto de lectura de los otros repos del producto. Se limita a esos repos y a lectura, y solo corre en PRs internos.
- Instalarlo en los repos de la organización lo autoriza G5.
