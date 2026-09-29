# Infraestructura v1 — inventario, revisión, plan y gate por ambiente

Estado: nuevo en v0.6.0 · Implementación: `internal/infra`, `coyote infra check|plan|propose`, check `infra` (R13), `coyote extract`, `coyote gate pr --plan`, gate · Decisión: ADR-0017

coyote no corre Terraform ni habla con una nube. Lee archivos: el inventario del repo, sus stacks y el plan en JSON que generan la persona o el pipeline. Con eso dice qué hace un cambio antes de aplicarlo y no deja que un agente aplique.

## Inventario

`coyote/infra.yaml` en el repo de infraestructura (perfil `infra`, regla R13):

```yaml
version: 1
tool: terraform                 # terraform u opentofu
versions: .tool-versions        # dónde se fijan las versiones
owners: ["@ana"]                # quién revisa los cambios de infraestructura
environments:
  demo:
    var_file: environments/demo.tfvars
    budget: { target_usd: 104, cap_usd: 150 }   # meta y tope mensual
    apply: local                # una persona puede aplicar desde su terminal
    match: ["ENV=demo"]         # marcas que delatan el ambiente en un comando
  prod:
    var_file: environments/prod.tfvars
    budget: { target_usd: 1200, cap_usd: 1800 }
    apply: reviewed             # solo la persona o un pipeline con revisor (el default)
    match: ["ENV=prod", "gke_tienda-prod"]
stacks:
  - { path: stacks/gcp/demo, cloud: gcp, env: demo, status: active }
  - { path: stacks/gcp/prod, cloud: gcp, env: prod, status: scaffold }
commands:
  apply: ["make apply", "make destroy"]         # comandos del repo que aplican cambios
```

- **Ambientes.** Cada uno declara su var-file, su presupuesto, su política de `apply` y sus marcas. Un ambiente sin `apply` es `reviewed`.
- **Marcas.** Son palabras o tramos de ruta: `ENV=prod` no coincide con `ENV=production`. El var-file y las carpetas de sus stacks también cuentan como marcas.
- **Stacks.** `active` tiene Terraform que se aplica; `scaffold` es andamiaje.
- **Protección.** Un campo desconocido es un error. Ningún agente escribe el inventario: está protegido como `project.yaml`, también para los comandos de shell. Un inventario que es un symlink, no es un archivo regular o pesa más de 1 MiB no se lee: el gate bloquea los cambios de infraestructura hasta corregirlo.
- **Stacks repetidos.** `p`, `p/` y `./p` son el mismo stack: repetirlo es un error.
- **Topes.** Hasta 64 ambientes, 32 marcas por ambiente (de 3 a 128 caracteres), 256 stacks, 64 comandos de apply y 64 dueños: el gate lee el inventario en cada comando. Un comando de infraestructura de más de 64 KiB se bloquea: es demasiado largo para saber a qué ambiente toca.

`coyote infra propose` arma el inventario desde el repo, sin escribir nada:

- toma la herramienta de `.tool-versions`;
- toma los ambientes de `environments/*.tfvars`;
- toma los stacks de `stacks/<nube>/<ambiente>` y de las carpetas con backend;
- toma los comandos de apply de los targets del Makefile que aplican, destruyen, encienden o apagan.

Presupuestos y dueños quedan pendientes: la revisión los pide. En un producto, `coyote extract` deja la propuesta en `coyote/repos/<repo>/infra.yaml`.

## Revisión

`coyote infra check [--file inventario]` compara el inventario con el repo:

| Hallazgo | Nivel |
|----------|-------|
| Stack activo sin archivos `.tf`, sin backend o con backend local | error |
| Var-file que no existe; ambiente sin presupuesto o con tope en 0 | error |
| tfstate o carpeta `.terraform` en git | error |
| Stack declarado que no existe | error |
| Sin `.terraform.lock.hcl`, o fuera de git | aviso |
| Versiones sin fijar, sin dueños, ambiente sin marcas o sin stacks | aviso |
| `prod` con `apply: local`; carpeta con backend fuera del inventario; andamiaje que ya tiene backend | aviso |

R13 corre esta revisión en el lint: los errores cuentan como MUST y los avisos como SHOULD.

## Plan

```sh
terraform plan -out tfplan && terraform show -json tfplan > plan.json   # lo corre la persona o el pipeline
coyote infra plan plan.json [--format text|md|json]
```

Cada recurso que cambia se clasifica:

| Riesgo | Cambios |
|--------|---------|
| R3 | destruir o reemplazar; permisos (IAM, roles, políticas, llaves de cuentas de servicio); llaves y secretos (KMS, Secret Manager, Key Vault); entrada abierta a internet en cualquier recurso (un rango público de 8 bits o menos, como `0.0.0.0/0` o `0.0.0.0/1` con `128.0.0.0/1`; `::/0`; `*` en Azure; de `0.x` a `255.x`), también en NACL, redes autorizadas de una base de datos o el endpoint de un cluster; acceso público (`allUsers`, el grupo `AllUsers` de S3, ACL `public-read`); bases de datos con `deletion_protection` o `deletion_protection_enabled` en `false`; un recurso demasiado grande para revisarlo entero |
| R2 | cualquier otra creación o cambio; sacar un recurso del estado sin destruirlo (`forget`) o importarlo; una acción que esta versión no conoce |
| R1 | un plan sin cambios |

La clasificación recorre el JSON del estado final de cada recurso, no su texto, y en orden. Un estado que no se puede leer entero cuenta como R3: un plan con sangría (`jq .`) da lo mismo que uno compacto, y dos lecturas del mismo plan dan lo mismo. Una regla de salida (`egress`) o una ruta abierta a internet son lo normal y no suben el riesgo.

- **Solo metadatos.** El plan en JSON lleva los valores de los recursos, secretos incluidos. coyote lo lee sin copiarlo y solo reporta direcciones, tipos y acciones.
- **Costo.** Los tipos que suelen mover el costo (clusters, grupos de nodos, bases de datos, balanceadores, NAT) se listan para compararlos con el presupuesto del ambiente.
- **En el PR.** `coyote gate pr --plan plan.json` suma el riesgo del plan al PR y pone la tabla en el comentario. Generar el plan en el pipeline necesita credenciales de la nube. Es una decisión del repo de infraestructura (por ejemplo, identidad federada de GitHub con GCP), no de coyote.

## Gate por ambiente

| Comando de un agente | Decisión |
|----------------------|----------|
| `terraform` o `tofu` con `apply`, `destroy`, `import`, `refresh`, `taint`, `force-unlock` o `state mv/rm/push`; `terragrunt`, `pulumi up/destroy` y `cdk deploy/destroy` | bloqueado siempre, en cualquier proyecto |
| Una de esas herramientas con un subcomando que llega al correr: `… \| xargs terraform`, `terraform "$@"` (una función o `set --`), un alias, `terraform $VERBO` o `$(…)` en el lugar del subcomando | bloqueado siempre: podría ser apply |
| Un comando de `commands.apply` del inventario (`make apply`), con el programa por su nombre y los targets entre sus argumentos: `make -C . apply`, `make ENV=prod apply`, `/usr/bin/make apply`, `sudo make apply`, `bash -c "make apply"` | bloqueado siempre |
| Un cambio a la nube o al cluster (`kubectl apply`, `helm upgrade`, `helmfile apply`, `gcloud … create`, `aws … delete-…`, `az … update`), o a las alertas (docs/specs/slo-v1.md), con la marca de un ambiente `reviewed`. El programa cuenta también con un sufijo de release o de imagen (`mimirtool-linux-amd64`, `grafana/mimirtool:2.14.0`) y con un subcomando o argumento que llega al correr | bloqueado siempre |
| Ese mismo cambio sin marca de ambiente, o en un ambiente `local` | aprobación de un solo uso: `coyote approve --uses N` la deja en 1 |
| `terraform plan`, `init`, `validate` | aprobación normal (corren código de proveedores y leen la nube) |

Si `coyote/infra.yaml` existe pero no se puede leer, los cambios de infraestructura se bloquean hasta corregirlo. Leer el estado, las salidas o el plan con valores (`terraform output`, `show`, `state pull`) es leer credenciales: se bloquea siempre (docs/specs/secrets-v1.md).

## Límites

- Sin marca, el ambiente de un comando no se conoce: `kubectl` sin `--context` usa el contexto actual. En ese caso pide aprobación normal, y la revisión muestra el comando completo.
- Un target de `make` que llega por la entrada (`echo apply | xargs make`) o un programa armado con una sustitución (`$(which terraform) apply`) no se reconocen: piden la aprobación normal. Cuando lo que llega al correr es el subcomando de una herramienta de apply conocida, se bloquea.
- `coyote infra` lee los archivos del repo con tope y solo si son regulares: un `.tf` o un `Makefile` que apuntan a un dispositivo no se leen. En total lee a lo sumo 256 MiB de `.tf` y 256 por stack, y no sigue los symlinks al buscar backends fuera del inventario.
- La clasificación del plan va por tipos y acciones de recursos de GCP, AWS y Azure. Un proveedor con otros nombres cae en R2 si no destruye ni reemplaza.
- coyote no estima costos en dólares: lista los recursos que los mueven. Una estimación (Infracost) puede sumarse en el pipeline del repo de infraestructura.
