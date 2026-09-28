# Contexto v1 — índice, `get context` y `ask`

Estado: nuevo en v0.2.0 · Implementación: `internal/index`, `coyote get context`, `coyote ask`, `coyote index` · Decisión: ADR-0007

## Para qué

Un agente no debería leer todo el repo para tocar una parte. `get context` le entrega un paquete acotado y referenciado de lo que necesita saber, y `ask` responde preguntas señalando dónde está la respuesta. Todo sale de git: el índice es una caché y nunca una segunda fuente de verdad.

## Qué se indexa

| Fuente | Clase | Fragmento |
|--------|-------|-----------|
| `README.coyote.md` | readme | una entrada (`purpose`, `run`, `mod`…) |
| `CONTEXT.coyote.md` | context | una entrada (`inv`, `dec`, `gap`…) con su ámbito |
| `coyote/decisions/*.md` | adr | una sección del ADR, con su estado |
| documentos citados en `docs\|…` y `README.md` | doc | una sección, partida cada 150 palabras |
| `coyote/workstreams/**` | workstream | una sección de `close.md` o una clave de `plan.yaml` |
| `coyote/ledger/**/*.ccf` | event | un evento |

Lo que coincide con `.coyoteignore` no se indexa (R12). Se usa la semántica de `.gitignore` para carpetas: un patrón que nombra una carpeta excluye todo lo que tiene debajo, con o sin `/` final. Tampoco se indexan archivos binarios, de más de 1 MB, especiales (FIFO, dispositivos) ni accesibles a través de symlinks.

La caché vive en `.coyote/index.json` (fuera de git). Cada archivo se reutiliza solo si su SHA-256 no cambió y si su entrada lleva una firma HMAC hecha con una clave local de la persona (0600, en su directorio de configuración). Así, una caché copiada de otra máquina o fabricada dentro de un repo no se usa: el índice nunca contradice a git.

## Búsqueda

BM25 local (k1 = 1.2, b = 0.75) sobre título, tipo, ámbito y texto. El ámbito y el título cuentan doble. Las palabras se normalizan: minúsculas, sin acentos, sin palabras vacías de español e inglés y con una reducción mínima de plurales. Los resultados se ponderan por clase: contexto × 1.4, readme × 1.2, ADR × 1.15, documento × 1, workstream × 0.9 y evento × 0.8. Dentro del contexto y el readme también pesa el tipo: `inv` × 1.25, `dec` × 1.15, `gap` × 1.1, `purpose` × 1.1 y `risk` × 1.05.

Un ámbito incluye sus subámbitos (`pagos` incluye `pagos/reembolsos`). Los ámbitos generales (`general`, `-`, vacío) aplican a todos.

## Paquete de contexto

`coyote get context [repo] [--scope S] [--query Q] [--budget N] [--format md|ccf]` llena el presupuesto (2 000 tokens por defecto) en este orden:

1. Identidad: propósito y cómo correr, probar y construir.
2. Invariantes del ámbito, todas.
3. Decisiones vigentes.
4. Trampas y riesgos.
5. Cómo hacer, términos y pendientes.
6. Módulos, dependencias y documentos.
7. ADRs relevantes; sin ámbito ni consulta, la lista de ADRs con su estado.
8. Documentos relacionados.
9. Actividad reciente del ámbito (8 eventos por defecto).

Cada línea cuesta lo que estima el estimador conservador. Lo que no cabe se cuenta como omitido y se avisa al final. `--format ccf` entrega líneas `tipo|ámbito|texto|ref` para agentes.

`repo` puede ser:

- el proyecto actual;
- una ruta a otro proyecto coyote;
- un repo registrado con `coyote repo add`. De ese repo se traen solo sus documentos: un clon superficial, sin blobs y con sparse checkout de `README.coyote.md`, `CONTEXT.coyote.md`, `README.md`, `.coyoteignore`, `coyote/` y los `.md` que cite. Su código no se baja.

## Preguntas

`coyote ask "pregunta" [--scope S] [--kind …] [--repo R] [--json] [--record]` devuelve los mejores fragmentos con su referencia `ruta#Llínea`, su relevancia relativa y el costo en tokens de leerlos. No llama a ningún modelo: no cuesta nada y nada sale de la máquina. Con `--record`, la pregunta queda en el ledger como evento `ask` con las referencias de sus tres mejores resultados.
