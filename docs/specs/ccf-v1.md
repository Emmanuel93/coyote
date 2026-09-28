# CCF v1 — Coyote Compact Format del ledger

Estado: estable desde v0.1.0 · Implementación: `internal/ccf`, `internal/ledger` · Decisión: ADR-0003

## Para qué

El ledger registra todo lo que pasa en un proyecto: commits, notas, planes, aprobaciones, pasos de agentes, cierres y su costo. Tiene que ser barato de leer para un modelo (pocos tokens por evento), fácil de mezclar entre personas (sin conflictos de git) y auditable (solo se agrega, nunca se reescribe).

## Archivos

- Ruta: `coyote/ledger/AAAA/MM/DD-<persona>.ccf`, un archivo por día UTC y por persona.
- Cada persona escribe solo en sus archivos, así dos ramas nunca editan el mismo archivo y el merge no choca.
- La primera línea es el encabezado, un comentario:
  `# ts|actor|project|repo|type|scope|what|refs|tokens in/cache/out|cost in+out|status`
- Las líneas que empiezan con `#` y las vacías se ignoran.
- Solo se agregan líneas. Corregir un evento es agregar otro; nunca se edita uno anterior.
- Codificación UTF-8 y fin de línea `\n`.

## Línea

Once campos separados por `|`, sin espacios alrededor:

```
ts|actor|project|repo|type|scope|what|refs|tokens|cost|status
```

| # | Campo | Formato | Ejemplo |
|---|-------|---------|---------|
| 1 | ts | UTC con minuto: `AAAA-MM-DDTHH:MMZ` (al leer también se acepta RFC 3339) | `2026-09-28T00:55Z` |
| 2 | actor | `@persona`, `@persona/agente` o `system` en minúsculas | `@ana/coyote-architect` |
| 3 | project | workstream (`W-0001`) o `-` | `W-0001` |
| 4 | repo | nombre del repo o proyecto | `acme-shop` |
| 5 | type | vocabulario cerrado (abajo) | `feat` |
| 6 | scope | módulo o tema; letras, números, `.`, `_`, `/`, `-`; o `-` | `pedidos/diseño` |
| 7 | what | texto libre de 1 a 12 palabras, sin `\|` ni saltos | `alta del proyecto` |
| 8 | refs | `clave:valor` separadas por espacio, o `-` | `sha:2f09eb6 doc:CONTEXT.coyote.md` |
| 9 | tokens | `entrada/caché/salida` compactos, o `-` | `12.4k/8.7k/1.1k` |
| 10 | cost | USD `$entrada+$salida`, o `-` | `$0.009+$0.011` |
| 11 | status | `ok`, `pend`, `fail` o `skip` | `ok` |

Reglas:

- El actor siempre es una persona. Un agente actúa a nombre de alguien y se escribe `@persona/agente`; `system` queda para procesos sin persona, como la CI.
- `tokens.entrada` es la entrada total, incluida la leída de caché; `caché` no puede superarla.
- Los conteos usan `950`, `12.4k` o `1.2M`; el costo, hasta cuatro decimales. No se aceptan valores negativos, `NaN`, infinitos, conteos de más de 10^15 ni costos de más de 10^9 USD por evento.
- Antes de escribir una línea, coyote la vuelve a leer tal como quedará: lo que no se puede leer no se escribe. Si el archivo no termina en salto de línea (una edición a mano), se agrega uno para no unir dos eventos.
- El texto de `what` es un resumen. El contenido vive en git y se cita en `refs`, nunca se copia al ledger.
- Un evento `close` resume los tokens y el costo de su workstream. Los totales de `coyote log` no lo suman, para no contar dos veces.

## Tipos

| Tipo | Significado | Tipo | Significado |
|------|-------------|------|-------------|
| feat | funcionalidad | adr | decisión de arquitectura |
| fix | corrección | spec | especificación |
| doc | documentación | note | nota de contexto |
| refactor | refactor | idx | indexado |
| test | pruebas | rev | revisión |
| chore | mantenimiento | apr | aprobación |
| ci | integración continua | rej | rechazo |
| build | build | gate | gate |
| perf | rendimiento | attr | atribución a IA eliminada |
| run | paso de agente | conf | conflicto |
| ses | sesión | cost | costo |
| close | cierre de workstream | init | inicialización |
| rel | release | plan | plan de workstream |
| ask | consulta al contexto | sync | sincronización con el remoto |

`coyote commit` traduce el tipo de commit al del ledger: `docs` → `doc`, `style` → `chore`, `revert` → `fix`, y cualquier otro desconocido → `chore`.

## Claves de referencia usadas

`sha:` commit · `doc:` documento del repo · `adr:` decisión · `ws:` workstream · `apr:` aprobación · `pr:` pull request · `art:` artefacto · `model:` modelo usado. Una clave nueva no rompe lectores: la gramática es `[a-z][a-z0-9-]*:valor`.

## Validación

Una línea es válida si tiene los once campos y pasa las reglas de arriba. `coyote log` y `coyote status` informan las líneas inválidas con archivo y número de línea, pero no se detienen: el ledger se lee aunque tenga una línea mala.

## Costo en tokens

Con el estimador conservador de la herramienta (ADR-0006), un evento cuesta de 25 a 60 tokens según cuántas referencias y cifras lleve. Un día intenso de una persona (60 eventos) cabe en unos 2 500 tokens, así que un agente puede leer la semana de un workstream sin buscar en otro lado.

## Ejemplo

```
# ts|actor|project|repo|type|scope|what|refs|tokens in/cache/out|cost in+out|status
2026-09-28T00:54Z|@ana|-|acme-shop|init|coyote|proyecto inicializado con coyote|-|-|-|ok
2026-09-28T00:55Z|@ana/coyote-architect|W-0001|acme-shop|plan|pedidos|definir dominio de pedidos|model:sonnet-5|12.4k/8.7k/1.1k|$0.009+$0.011|ok
2026-09-28T01:10Z|@ana|W-0001|acme-shop|attr|pedidos|1 marca de atribución a IA quitada del mensaje|-|-|-|ok
2026-09-28T01:10Z|@ana|W-0001|acme-shop|feat|pedidos|alta del proyecto|sha:f414d8a|-|-|ok
```

## Compatibilidad

- Una versión nueva del formato usará otro encabezado.
- Agregar tipos es un cambio menor: un lector anterior marca esas líneas como inválidas y sigue leyendo el resto. Agregar claves de referencia no afecta a nadie.
- Quitar un campo o cambiar su significado requiere una versión nueva.
