---
status: proposed
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0009: Gate por hash de la acción exacta en el hook del IDE

## Contexto y problema
La regla A1 dice que ningún agente ejecuta acciones con efectos sin aprobación humana. Hasta v0.2 es una promesa: un agente en Claude Code o Cursor corre comandos, edita archivos y llama herramientas MCP sin registro. El control tiene que vivir donde el agente actúa, no en su prompt, y no puede cansar a la persona al punto de que lo apague.

## Opciones consideradas
- Instrucciones en los prompts: un agente con una instrucción inyectada las ignora.
- Listas de comandos permitidos por patrón: aprueban acciones que nadie vio; la propuesta prohíbe los comodines.
- Hash de la acción exacta en el hook previo del IDE, con cola de propuestas y registros firmados.

## Decisión
El hook previo del IDE (`PreToolUse` en Claude Code, `preToolUse` en Cursor) llama a `coyote gate check`, que decide así:

1. Bloquea siempre, aun con aprobación: que un agente apruebe, rechace o revoque; que escriba en el gate (hooks y configuración del IDE, `.git/`, `coyote/approvals/`, `.coyote/`); que lea o escriba credenciales (llaves SSH, tokens de `gh`, la clave de coyote); y que un commit lleve atribución a IA o autoría de una herramienta (R15).
2. Deja pasar sin registro las herramientas que solo leen (Read, Grep, Glob, búsquedas) y una lista cerrada de comandos de solo lectura. La lista prohíbe redirecciones, sustituciones, variables y las banderas que escriben o ejecutan (`find -exec`, `sort -o`, `git -c`, `git diff --output`…).
3. Para todo lo demás calcula el hash SHA-256 de la acción normalizada: comando y carpeta en Bash; ruta y contenido en Write; ruta y textos viejo y nuevo en Edit; nombre y entrada completa en cualquier otra herramienta. Busca una aprobación con ese hash que esté vigente, con usos disponibles, sin revocar y firmada con la clave local de la persona.
4. Si la encuentra, deja pasar y escribe un evento `gate` con la aprobación, que consume un uso. Si no, encola una propuesta, escribe el rechazo en el ledger y bloquea con un mensaje que dice cómo aprobar.

Las aprobaciones las da una persona desde su terminal con `coyote approve`: caducan en 24 horas como máximo, tienen de 1 a 100 usos y se guardan en `coyote/approvals/` con una firma HMAC hecha con una clave local (0600, fuera del repo). Los usos, rechazos y revocaciones van en el ledger, que solo agrega líneas; el registro JSON nunca se reescribe. Si coyote no está instalado o falla, el hook bloquea: el gate falla cerrado.

## Consecuencias
- Nada con efectos corre sin una decisión humana registrada, y el ledger guarda cada decisión con el agente que reporta el IDE.
- Una aprobación solo sirve en la máquina donde se dio: un registro copiado o fabricado no valida.
- Leer no cuesta aprobaciones; ejecutar código (pruebas incluidas) sí, porque corre código que el agente pudo escribir. Para eso están las aprobaciones de varios usos.
- Un proyecto con gate no se puede usar desde el IDE sin coyote instalado.
- Queda fuera: lo que hace un comando aprobado (si apruebas `make x`, corre lo que diga el Makefile) y los IDEs sin hook previo, que dependen del gate de CI (v0.5).
