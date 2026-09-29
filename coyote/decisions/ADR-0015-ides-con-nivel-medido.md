---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0015: Adaptadores de IDE con nivel medido

## Contexto y problema
El gate (ADR-0009) vive en el hook previo del IDE y hoy se instala en Claude Code y Cursor. Codex, Copilot, Gemini CLI, Windsurf (hoy Devin Desktop), Devin CLI y Junie tienen hooks parecidos, pero con otros campos, otros nombres de herramientas y otra forma de negar. Varios pueden quedar apagados sin aviso: carpeta sin confianza, modo restringido, política de la organización o una función en EAP. Además, hay reportes de negaciones que no detienen la herramienta. R17 limita a los IDEs de nivel 2 o 3, pero hoy el nivel de cada uno se declara, no se mide.

## Opciones consideradas
- Solo `AGENTS.md`: instrucciones que un agente puede ignorar.
- Un adaptador por IDE que traduce su hook al gate, instala su archivo y mide en la máquina de la persona que el IDE llama al gate y respeta la negación.
- Envolver la shell con shims en el `PATH`: no cubre ediciones ni MCP, y se esquiva con una ruta absoluta.

## Decisión
- **Un adaptador por IDE, en una tabla.** Cada fila dice:
  - cómo se reconoce la entrada de su hook;
  - cómo se llaman sus herramientas: shell, lectura, búsqueda, archivo y MCP;
  - cómo niega;
  - dónde se instala su hook.

  Una herramienta que la tabla no conoce pide aprobación, como hasta ahora.
- **Instalación.** `coyote install --ide` suma `codex`, `gemini`, `copilot`, `windsurf` y `devin`. Fusiona con la configuración existente sin pisarla, igual que con Claude Code y Cursor, y `--check` verifica que esté al día.
- **El nivel se mide con un canario.** `coyote doctor --ide X` pide que el agente corra `coyote doctor canary <código>`, un comando que el gate niega siempre.
  - El gate anota la llamada en `.coyote/`. Si el comando llega a correr, deja constancia de que el IDE no respetó la negación.
  - Nivel 1: el IDE llamó al gate y el comando no corrió. Nivel 2: corrió de todos modos. Nivel 3: el gate nunca se enteró.
- **Junie** solo tiene hooks en su CLI, en EAP, y el plugin del IDE no los llama: en el IDE es nivel 3 hasta que los llame. **Kiro, Cline y Zed** quedan en la matriz de la spec, sin adaptador.

## Consecuencias
- El gate cubre a los agentes que usa el equipo, con la misma política para todos.
- El nivel de cada IDE es un dato de tu máquina, no una promesa. Un IDE que apaga sus hooks o ignora la negación aparece como tal en `doctor`.
- Un IDE nuevo o un cambio de formato es una fila de la tabla, con sus pruebas.
- Los agentes remotos (el agente de Copilot en GitHub, Devin en la nube) no corren en tu máquina: los cubre `gate pr` en el PR (R17).
