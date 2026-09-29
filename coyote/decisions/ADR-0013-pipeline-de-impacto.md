---
status: accepted
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

1. **Disparador.** Corre en `pull_request_target`, con `contents: read` y `pull-requests: write`.
   - El workflow, la política y las reglas de riesgo son los de la rama base: un PR no cambia el chequeo que lo evalúa.
   - El código del PR se trae como dato y se lee con git de plomería, sin compilarlo ni ejecutarlo. Por eso los forks se evalúan igual en lugar de saltarse: un job saltado cuenta como éxito.
2. **Repos.** Trae los otros repos con un secreto de solo lectura, y coyote en una versión etiquetada.
3. **Análisis.** Corre `coyote gate pr`:
   - lee el evento y arma el mapa;
   - analiza el diff del PR (`base...head`);
   - calcula el riesgo por rutas e impacto;
   - si es R2 o R3, pide la aprobación de un dueño (R17).
4. **Reporte.** Deja el resumen del job y crea o actualiza un solo comentario en el PR, identificado por un marcador.

Política configurable: `warn` (por defecto), que solo reporta, o `fail`, que hace fallar el chequeo hasta que aprueba un dueño.

Revisión del 2026-09-28, después de la revisión adversarial de v0.5. La propuesta original usaba `pull_request` y `pull_request_review` y saltaba los forks. Con eso, un PR corría su propia versión del workflow y un fork pasaba como éxito. `pull_request_target` no corre con las revisiones: después de aprobar, el chequeo se vuelve a correr.

## Consecuencias
- Cada PR muestra qué toca en los otros repos antes del merge, sin modelo y sin costo.
- Cada repo guarda un secreto de lectura de los otros repos del producto, limitado a esos repos y a lectura. Con `pull_request_target`, el job de un fork también lo tiene: el código del fork nunca se ejecuta, y leerlo usa git sin filtros, hooks ni transportes (docs/specs/product-v1.md).
- Instalarlo en los repos de la organización lo autoriza G5.
