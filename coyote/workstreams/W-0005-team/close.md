# Cierre de W-0005

Qué dejó v0.5, además de lo construido:

- **Un gate de PR sin pull_request_target es decorativo.** Con `pull_request`, el PR trae su propia versión del workflow que lo evalúa, y un fork que se salta cuenta como éxito. La revisión adversarial lo encontró antes de instalarlo en los repos.
- **Leer la lista de archivos de un diff pide `--name-status -z`.** Un `.gitattributes`, un archivo vacío o un nombre con comillas escondían cambios de riesgo.
- **Bloquear a un agente por texto necesita palabras, no una lista de envoltorios.** Hay más formas de correr un comando de las que se pueden enumerar.
- **El piloto corrige los supuestos.**
  - Los tres repos no tienen CODEOWNERS.
  - Están en la cuenta de la persona, no en una organización.
  - Exigir un chequeo en repos privados pide un plan de pago.

  De ahí salieron un solo token y la bandera `pr_enforcement`.
- **La persona pidió hacer el ejercicio en ramas.** `v0.5.0` vive en `v0.5`, y el pipeline se ensaya en `coyote/pipeline` sin tocar `main`.

<!-- coyote close: inicio del consumo; se regenera con coyote close -->
## Consumo

Del ledger, 2026-09-29 01:07Z. El costo es el que estima Claude Code en cada corrida, no la factura.

| Total | Eventos | Tokens (entrada/caché/salida) | Caché | Costo |
|---|---|---|---|---|
| W-0005 | 16 | 0/0/0 | 0 % | $0.0000 |

| Modelo | Eventos | Tokens | Costo |
|---|---|---|---|
| sin modelo | 16 | 0/0/0 | $0.0000 |

| Agente | Eventos | Tokens | Costo |
|---|---|---|---|
| coyote-architect | 1 | 0/0/0 | $0.0000 |
| coyote-dev | 5 | 0/0/0 | $0.0000 |
| coyote-devsecops | 1 | 0/0/0 | $0.0000 |
| coyote-reviewer | 2 | 0/0/0 | $0.0000 |
| coyote-scribe | 1 | 0/0/0 | $0.0000 |
| coyote-security | 5 | 0/0/0 | $0.0000 |
| coyote-sr-solution-architect | 1 | 0/0/0 | $0.0000 |

Eventos por tipo: doc 4, feat 5, fix 6, refactor 1.

### Pasos del plan

| Paso | Agente | Estado | Corridas | Costo |
|---|---|---|---|---|
| T32 | coyote-sr-solution-architect | pendiente | 0 | $0.0000 |
| T33 | coyote-dev | pendiente | 0 | $0.0000 |
| T34 | coyote-devsecops | pendiente | 0 | $0.0000 |
| T35 | coyote-architect | pendiente | 0 | $0.0000 |
| T36 | coyote-dev | pendiente | 0 | $0.0000 |
| T37 | persona | pendiente | 0 | $0.0000 |
| T38 | coyote-reviewer | pendiente | 0 | $0.0000 |

7 pasos quedan sin cerrar: lo que corrió sin revisión lo evalúa quien autorizó el plan (A4).
<!-- coyote close: fin del consumo -->
