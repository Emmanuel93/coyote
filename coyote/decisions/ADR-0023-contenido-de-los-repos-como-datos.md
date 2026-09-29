---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0023: El contenido de los repos es dato, no instrucción (D38)

## Contexto y problema
coyote le entrega a los agentes texto de los repos: los paquetes de `get context`, las respuestas de `ask` y el `AGENTS.md` que genera desde `CONTEXT.coyote.md` y `README.coyote.md`. Ese texto lo puede escribir cualquiera que abra un PR, y el agente lo lee junto a sus instrucciones. Es la inyección indirecta (LLM01:2025) y el envenenamiento de memoria y contexto (ASI06, OWASP Agentic 2026).

Tres huecos lo agravan:
- `AGENTS.md` es una instrucción: una nota de `CONTEXT.coyote.md` llega a los agentes como "Contexto vivo".
- `gate pr` no le da riesgo a los archivos de instrucciones de los agentes, así que un PR que planta una orden es R1.
- Un PR puede reescribir líneas del ledger sin que nada lo marque.

## Opciones consideradas
- **Borrar lo sospechoso.** Es silencioso y rompe documentación legítima.
- **Enmarcar y marcar.** El paquete dice que su contenido es dato, y un clasificador de reglas fijas marca lo que tiene forma de orden para un agente. No se borra nada.
- **Clasificar con un modelo.** Cuesta dinero, no es determinista y necesita red.
- **Dejarlo a la revisión humana, sin señal.** El revisor no sabe qué mirar.

## Decisión
- **Marco.** Los paquetes de contexto y las respuestas de `ask` abren con una línea que dice que lo citado de los repos son datos, no instrucciones. Cada fragmento sigue en una línea, escapado con safetext.
- **Clasificador de ingesta.** Son reglas fijas, en español y en inglés, sobre el texto normalizado (minúsculas y sin caracteres invisibles). Marca:
  - pedidos de ignorar u olvidar instrucciones, de cambiar de rol o de callarle algo a la persona;
  - comandos que bajan código y lo corren (`curl … | sh`), que leen credenciales o que apagan el gate (`--no-verify`, `coyote approve`, borrar la configuración del IDE);
  - texto oculto: caracteres de formato, etiquetas Unicode (U+E0000 a U+E007F) y comentarios HTML con órdenes.

  Un fragmento marcado sale con un aviso y el paquete dice cuántos hay. `coyote index` los lista con archivo y línea.
- **`AGENTS.md`.** `generate agents` se niega a llevar una nota marcada y dice cuál es. No hay bandera para forzarlo: sería lo primero que usaría un atacante. La persona reescribe la nota.
- **`gate pr`.**
  - Los archivos de instrucciones de los agentes son R2: `AGENTS.md`, `CLAUDE.md`, `GEMINI.md`, `CONTEXT.coyote.md`, `README.coyote.md`, `.github/copilot-instructions.md`, `.github/instructions/**`, `.cursorrules`, `.windsurfrules` y `.clinerules`, en cualquier carpeta.
  - Un texto marcado por el clasificador en lo que agrega el PR a Markdown o a texto plano es R2, con archivo, línea y regla.
  - Cambiar o borrar líneas existentes de `coyote/ledger/**` es R3. Agregar líneas no cambia el riesgo.
- **Escape del loop autónomo.** Una prueba con un agente falso en `autonomous` intenta cada una de estas cosas, y todas deben fallar:
  - salir de su rama `ws/`;
  - pasar su tope;
  - cambiar su plan aprobado o aprobarse a sí mismo;
  - tocar el gate o el hub;
  - hacer push fuera de `ws/`.
- **Fuzzing.** El fuzzing nativo de Go corre sobre los lectores expuestos a texto ajeno: el de shell del gate, CCF, CCF-doc, safetext y el de consultas de SLO. Cada falla queda como caso en su corpus.

## Consecuencias
- Más PRs quedan en R2: los que tocan instrucciones de agentes, que es lo que son.
- El clasificador no ve una orden bien disfrazada. La defensa que no depende del modelo sigue siendo el gate: una orden inyectada puede convencer al agente, pero su efecto pasa por la aprobación de una persona.
- Una nota legítima marcada por error se reescribe. Es el costo de no tener bandera para forzarla.
