---
name: coyote-spec
description: "Cómo escribir una spec que parte del dominio, con criterios de aceptación en Gherkin verificables por una persona y por una prueba."
---
# Spec desde el dominio

Estructura de `spec.md`:

1. **Problema.** Una frase: qué duele, a quién y cómo se mide el éxito.
2. **Dominio.** Entidades, eventos y reglas que cambian; lo que no cambia, explícito.
3. **Criterios de aceptación.** Gherkin, al menos tres escenarios: camino feliz, error y borde.
4. **Fuera de alcance.**
5. **Preguntas abiertas.** Lo que decide una persona, no el agente.

```gherkin
Escenario: pedido pagado se confirma
  Dado un pedido con un pago capturado
  Cuando se procesa la confirmación
  Entonces el pedido queda confirmado
  Y se publica el evento PedidoConfirmado
```

Reglas: usa los términos (`term`) del proyecto; cada criterio debe poder volverse una prueba; nada de soluciones técnicas en la spec.
