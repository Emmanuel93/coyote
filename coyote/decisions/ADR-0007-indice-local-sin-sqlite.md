---
status: accepted
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0007: Índice local en memoria en v0.2; SQLite después

## Contexto y problema
v0.2 necesita un índice local para `get context`, `ask` y la web de costos. La propuesta suponía SQLite. Los datos viven en git (ledger CCF, documentos CCF-doc, ADRs), que es la fuente de verdad. El entorno de construcción no llega al proxy de módulos de Go y los binarios se generan con `CGO_ENABLED=0`.

## Opciones consideradas
- `modernc.org/sqlite`: puro Go; decenas de módulos que no se pueden traer aquí y un repo mucho más pesado.
- `mattn/go-sqlite3`: requiere cgo y un compilador de C; rompe los binarios cruzados.
- Índice en memoria reconstruido desde los archivos, con caché en `.coyote/index.json` y una interfaz de almacenamiento.

## Decisión
Índice en memoria con caché, detrás de una interfaz. Se invalida por ruta, tamaño y fecha de modificación de cada archivo. `.coyote/` no se versiona.

## Consecuencias
Cero dependencias nuevas, y el índice nunca contradice a git porque se reconstruye desde él. Si un proyecto crece a cientos de miles de eventos o hace falta búsqueda vectorial, se mide y se agrega SQLite detrás de la misma interfaz; se decide en G3 o G4.
