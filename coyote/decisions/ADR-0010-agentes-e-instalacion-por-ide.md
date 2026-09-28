---
status: accepted
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0010: Una definición por agente, instalada en el formato de cada IDE

## Contexto y problema
La propuesta define 14 agentes `coyote-*` y skills que deben funcionar en varios IDEs. Cada IDE lee archivos distintos: Claude Code usa `.claude/agents/`, `.claude/skills/` y `.claude/settings.json`; Cursor usa `.cursor/agents/`, `.cursor/hooks.json` y `.agents/skills/`. Mantenerlos a mano duplica el trabajo y deja configuraciones distintas por proyecto.

## Opciones consideradas
- Escribir a mano los archivos de cada IDE en cada proyecto.
- Una sola definición por agente y por skill en la herramienta, más las del proyecto, que `coyote install` traduce al formato de cada IDE.

## Decisión
Los agentes y skills viven en la herramienta (`agents/` y `skills/`, embebidos en el binario) y el proyecto puede agregar los suyos en `coyote/agents/` y `coyote/skills/`. `coyote install --ide claude-code|cursor|all` genera:

- Claude Code: `.claude/agents/`, `.claude/skills/`, los hooks y la atribución apagada en `.claude/settings.json`, y `CLAUDE.md` con `@AGENTS.md`.
- Cursor: `.cursor/agents/`, `.agents/skills/` y `.cursor/hooks.json`.
- Para ambos: `AGENTS.md` y el hook `commit-msg`.

Como pide la propuesta, los agentes exploran y proponen: no tienen Bash, Write ni Edit. Lo que cambia el código lo aplica la sesión principal del IDE y pasa por el gate (ADR-0009). La configuración existente se fusiona: coyote reemplaza solo sus propias entradas y conserva las tuyas. Todo archivo generado lo dice en su encabezado, y `coyote install --check` falla en CI si alguno difiere.

## Consecuencias
- Un agente se corrige una vez y se reinstala en todos los proyectos y los IDEs.
- Los agentes del proyecto conviven con los de la herramienta; si se llaman igual, gana el del proyecto.
- En v0.3 solo Claude Code y Cursor, que tienen hook previo para comandos, ediciones y MCP (nivel 1). Windsurf, Copilot, Codex, Gemini CLI, Junie, Zed y Devin llegan en v0.5.
