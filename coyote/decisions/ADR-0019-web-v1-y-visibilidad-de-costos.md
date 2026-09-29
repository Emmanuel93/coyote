---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0019: Web v1 y visibilidad de costos (D10)

## Contexto y problema
`coyote web` muestra costos por proyecto, persona, modelo y agente, y le muestra los de todas las personas a quien la abra. Los workstreams, la cola del gate, las aprobaciones vigentes y el presupuesto del mes solo se ven en la terminal. D10 pide que cada persona vea lo suyo y los admins todo.

## Opciones consideradas
- Filtrar por identidad: cada persona ve lo suyo y los totales; los admins ven todo.
- Sacar del ledger el costo por persona y dejar en git solo agregados: pierde la trazabilidad de A3.
- Cifrar el costo por persona para los admins: llaves que operar para un dato que ya está en git.

## Decisión
- **Vistas.**
  - Costos.
  - Presupuesto del mes: gastado, tope y proyección al cierre.
  - Workstreams con el estado de cada paso.
  - Cola del gate, aprobaciones vigentes y gates de release.
  - SLOs.
  - La organización, solo para admins: sus proyectos con su gasto del mes.
- **D10.** La persona es la identidad de git de quien corre `coyote web`. Los admins son los del hub y los de `project.yaml`.
  - Una persona que no es admin ve sus costos y los totales del proyecto, sin el desglose por persona.
  - Un admin ve todo.
- **Honestidad.** D10 es visibilidad por defecto, no control de acceso: el ledger está en git y quien lee el repo lo lee. La web lo dice en cada página.
- **Solo lectura.** Aprobar, rechazar y revocar se hacen en la terminal. Un botón en la web abriría la puerta a CSRF y se saltaría la verificación de que decide una persona.
- **Superficie.** Loopback, Host verificado, solo GET y HEAD, sin JavaScript y con una CSP que no carga nada de fuera.

## Consecuencias
- Compartir la pantalla con la web abierta no expone el gasto de otras personas.
- El control de acceso real queda fuera mientras no haya servidor (D2).
- La web no agrega comandos: todo lo que muestra sale del ledger, de los planes y de la cola que ya existen.
