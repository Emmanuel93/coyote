---
name: coyote-review
description: "Lista de revisión adversarial - estándar coyote, aceptación, seguridad de aplicaciones con LLM y agentes - con formato de hallazgos y severidades."
---
# Revisión adversarial

Revisa en este orden y reporta solo lo que puedas demostrar:

1. **Aceptación.** ¿Cada criterio de la spec se cumple y se prueba?
2. **Estándar.** `coyote standards show` y `coyote standards lint`: formato de commit (R2), ADR antes del parche en R2 y R3 (R8), contratos (R11), documentos coyote válidos (R14), sin atribución a IA (R15).
3. **Invariantes y decisiones** del paquete de contexto.
4. **Seguridad**, con el OWASP Top 10 para aplicaciones con LLM (2025) como guía: inyección de prompt directa e indirecta, divulgación de información sensible, cadena de suministro, envenenamiento de datos, manejo inseguro de la salida, agencia excesiva, fuga del prompt de sistema, debilidades de embeddings, desinformación y consumo sin límite.
5. **Agentes.** Permisos de herramientas más amplios que la tarea, acciones con efectos sin gate, identidad de agente que se puede suplantar y datos que se tratan como instrucciones.
6. **Operación.** Errores silenciosos, reintentos sin tope, falta de registro o de pruebas.

Formato de cada hallazgo:

| Severidad | Ubicación | Problema | Evidencia | Criterio de cierre |
|-----------|-----------|----------|-----------|--------------------|
| alta | `ruta#L12` | qué falla | reproducción o razonamiento verificable | qué prueba lo da por resuelto |

Alta: rompe seguridad, datos o una invariante. Media: rompe la aceptación o el estándar. Baja: mantenibilidad.
