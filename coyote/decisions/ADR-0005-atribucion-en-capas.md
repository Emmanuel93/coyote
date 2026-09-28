---
status: proposed
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0005: Filtro de atribución de IA en capas

## Contexto y problema
La regla R15 prohíbe firmas de herramientas o modelos de IA en commits, PRs y documentos. Los IDEs las agregan por defecto y su configuración no siempre las apaga.

## Opciones consideradas
- Solo configuración del IDE: insuficiente, falla en silencio.
- Solo CI: detecta tarde, cuando el commit ya existe.
- Capas: configuración del IDE, hook `commit-msg` autónomo, gate PreToolUse, `coyote commit` y chequeo en CI.

## Decisión
Capas, con los patrones en `standards/default/attribution.yaml` y la especificación en `docs/specs/attribution-v1.md`.

## Consecuencias
El hook funciona aunque `coyote` no esté instalado (filtro básico); el gate bloquea antes de ejecutar; el lint lo verifica en CI, merges incluidos. La trazabilidad de qué agente hizo qué vive en el ledger, que es interno.

La revisión adversarial de v0.1 ajustó tres criterios: las personas nunca cuentan (se exige el correo o el nombre del bot, con límites de palabra); en un mensaje de commit una frase de atribución detiene el commit en vez de reescribirse en silencio; y la autoría del commit (autor y committer) también se revisa, porque un agente puede firmar con su propia identidad.
