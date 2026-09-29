# Contexto archivado en v1.0

Entradas que salieron de `CONTEXT.coyote.md` el 2026-09-29 para dejar lugar bajo su tope de 1 500 tokens (docs/specs/ccf-doc-v1.md). Siguen vigentes: su decisión vive en el ADR o la spec que citan.

```
dec|formats|CCF para el ledger y CCF-doc para README.coyote.md y CONTEXT.coyote.md|ADR-0003
risk|tokens|el estimador local de tokens puede desviarse del real; los eventos run ya traen el uso que reporta Claude Code|ADR-0006
dec|agents|una definición por agente y skill; coyote install la traduce a Claude Code y Cursor sin pisar su configuración|ADR-0010
dec|gate|nivel de IDE medido con un canario que el gate siempre niega; nivel 2 o 3 cae en R17|ADR-0015
how|release|make dist genera binarios de macOS y Linux con la versión inyectada por ldflags|Makefile
gap|product|un tópico elegido por un mapa de configuración o un cliente generado quedan sin enlace en el mapa|docs/specs/product-v1.md
gap|ws|retomar una sesión con --resume y --agent en headless no se probó contra Claude Code real|docs/specs/workstream-v1.md
```
