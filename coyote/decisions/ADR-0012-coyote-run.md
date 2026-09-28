---
status: accepted
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0012: coyote run sobre Claude Code headless, con router y cierre con costo

## Contexto y problema
Los agentes existen desde v0.3, pero coyote no puede correrlos, elegir su modelo ni saber cuánto cuestan. La propuesta pide gasto predecible: topes por corrida y por proyecto, y un cierre con tokens, modelos y costo.

## Opciones consideradas
- Un runner propio sobre el Agent SDK: dependencia nueva y otro runtime que mantener.
- Claude Code en modo headless: `claude -p --output-format json` con `--agent`, `--model`, `--max-turns`, `--max-budget-usd` y `--add-dir`. Aplica los hooks del proyecto (el gate) y devuelve tokens, caché, costo estimado y uso por modelo.

## Decisión
`coyote run --agent A --ws W "tarea"` arma el paquete de contexto (y el impacto, si hay un cambio) y lo pasa por la entrada estándar. Luego:

1. Pide el modelo al router.
2. Corre Claude Code en la máquina de la persona con topes de turnos y de dólares.
3. Guarda el artefacto en `coyote/workstreams/<W>/`.
4. Registra un evento `run` con tokens de entrada, caché y salida, el costo y `model:`.

El gate sigue activo dentro de la corrida. Si la corrida necesita una aprobación, termina con la acción en la cola y la persona decide.

El router lee `coyote/router.yaml`: modelo por agente, piso por riesgo y degradación por presupuesto (al 80 % del tope mensual avisa y baja un nivel salvo R3; al 100 % no corre). `coyote close W` suma del ledger el consumo del workstream y lo escribe en `close.md` y en el evento `close`.

## Consecuencias
- El costo de cada paso queda en el ledger y en la web, por proyecto, persona, modelo y agente.
- El costo que reporta Claude Code es una estimación del cliente, no la factura. Se marca como tal.
- Nada corre en el entorno de construcción: las corridas reales se hacen en la máquina de la persona, con el presupuesto que autorice G4.
