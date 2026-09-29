---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0017: Infraestructura como código: inventario, plan y gate por ambiente

## Contexto y problema
R13 pide `coyote/infra.yaml` en un repo de infraestructura, pero no define su contenido y solo revisa que exista. En un PR, `gate pr` sabe que cambió Terraform o Kubernetes (R3), no qué se crea, se reemplaza o se destruye. La definición de `coyote-infra` dice que un agente nunca propone `apply`, pero el gate lo dejaría correr si la persona lo aprueba. El repo de infraestructura del piloto muestra lo típico: dos ambientes con posturas distintas, presupuestos escritos en un documento y `make apply` directo.

## Opciones consideradas
- Dejarlo a los agentes: nada queda verificable.
- Un inventario con esquema y su revisión, la lectura del plan de Terraform en JSON y reglas del gate por ambiente.
- Motores de políticas (OPA, checkov) como fuente: son binarios externos; coyote puede leer su salida después.

## Decisión
- **Inventario.** `coyote/infra.yaml` v1 (spec en `docs/specs/infra-v1.md`) declara:
  - la herramienta y sus versiones;
  - los ambientes: var-file, presupuesto (meta y tope), política de `apply` (`reviewed` o `local`) y las marcas que los delatan en un comando;
  - los stacks: nube, ambiente y estado;
  - los dueños.
- **Revisión.** `coyote extract` propone el inventario para un repo de Terraform. `coyote infra check` lo compara con el repo, y R13 usa esa revisión.
- **Plan.** `coyote infra plan <plan.json>` clasifica el plan de `terraform show -json` por recurso y acción:
  - destruir o reemplazar, IAM, red expuesta y llaves son R3;
  - lo demás que cambia es R2.

  Solo muestra direcciones y tipos. `gate pr --plan` lo suma al PR.
- **Gate.**
  - Un agente nunca corre `apply`, `destroy`, `import` ni cambios de estado de Terraform u OpenTofu.
  - Un comando con efectos contra un ambiente `reviewed` se bloquea siempre.
  - Los demás comandos de infraestructura con efectos piden una aprobación de un solo uso.
- **Sin nube.** coyote no corre Terraform ni llama a las nubes: lee archivos y el plan que producen la persona o el pipeline.

## Consecuencias
- Un cambio de infraestructura llega al PR con lo que va a hacer, no solo con los archivos que toca.
- Prod se aplica desde tu terminal o desde un pipeline con revisor; los agentes proponen.
- El pipeline que genera el plan necesita credenciales de la nube. Esa decisión es del repo de infraestructura (por ejemplo, identidad federada de GitHub con GCP), no de coyote.
