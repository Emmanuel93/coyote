---
name: coyote-patch
description: "Cómo proponer un cambio de código como diff unificado que la persona aprueba una sola vez, con commit convencional y sin atribución a IA."
---
# Parche propuesto

1. Escribe el diff unificado desde la raíz del repo (`--- a/ruta` y `+++ b/ruta`), aplicable con `git apply --check`.
2. Incluye las pruebas en el mismo diff o en `tests.diff`.
3. Cambio mínimo: sin reformatear lo que no tocas ni renombrar por gusto.
4. Mensaje de commit: `tipo(ámbito): descripción`, en el idioma de docs del proyecto; tipos: feat, fix, docs, refactor, test, chore, ci, build, perf.
5. Nunca agregues `Co-Authored-By`, "Generated with", enlaces de sesión ni firmas de herramientas o modelos de IA (R15). El commit lleva la identidad de la persona.

Cómo pasa por el gate (el flujo que menos aprobaciones pide):

1. La sesión principal escribe el diff en `coyote/workstreams/<W>/patch.diff`: una sola propuesta que la persona revisa completa con `coyote review`.
2. Con la aprobación, pide `git apply coyote/workstreams/<W>/patch.diff` (otra aprobación) y después las pruebas.
3. Si el gate bloquea, no busques otro camino: explica qué necesitas y espera la decisión.
