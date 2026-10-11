# Secretos e infraestructura

Hay dos cosas que un agente no hace ni con tu aprobación: leer o escribir un secreto, y aplicar infraestructura en un ambiente revisado. Las hacen la persona o un pipeline con revisor (ADR-0016, ADR-0017).

## Secretos

El gate bloquea siempre que un agente:

- lea o escriba un archivo de secretos: `.env`, llaves privadas, `*.tfstate`, kubeconfig, `credentials.json` y parecidos;
- escriba un secreto literal en cualquier archivo;
- corra un comando que imprime credenciales, como `gcloud auth print-access-token`, `kubectl get secret`, `terraform output` o `env` a secas.

Las plantillas (`.env.example`, `.env.sample`) no son secretos: un agente las lee y las edita. Para que sepa qué variables existen sin leerlas:

```sh
coyote secrets list                       # rutas, tipos y nombres de variables, nunca valores
```

Un secreto escrito se detiene en `coyote commit`, en el hook `commit-msg` y en el PR, donde queda en R3 sin aprobación posible. Para revisar un repo:

```sh
coyote secrets scan                       # los archivos versionados
coyote secrets scan --range main...HEAD   # lo que agrega tu rama
```

Si un secreto ya llegó al remoto, rótalo: quitarlo del archivo no basta.

El escáner reconoce formas conocidas: llaves privadas, tokens con prefijo y URLs con contraseña. Una contraseña de forma libre, como la de un `application.yml`, se protege declarando su archivo en `coyote/project.yaml`, que ningún agente edita:

```yaml
secrets:
  files: ["config/prod/*.yaml"]           # también son archivos de secretos
  allow:
    - { path: "testdata/**", reason: "llaves generadas para las pruebas de TLS" }
```

## Infraestructura

coyote no corre Terraform ni habla con una nube. Lee el inventario, los stacks y el plan.

### 1. El inventario

En el repo de infraestructura (un proyecto coyote de tipo `infra`), en una rama:

```sh
coyote infra propose > coyote/infra.yaml  # lo arma desde .tool-versions, environments/*.tfvars, los stacks y el Makefile
```

Completa los presupuestos, los dueños y cómo se aplica cada ambiente:

```yaml
owners: ["@ana"]
environments:
  demo:
    var_file: environments/demo.tfvars
    budget: { target_usd: 100, cap_usd: 150 }
    apply: local                  # una persona puede aplicar desde su terminal
    match: ["ENV=demo"]           # marcas que delatan el ambiente en un comando
  prod:
    var_file: environments/prod.tfvars
    budget: { target_usd: 1200, cap_usd: 1800 }
    apply: reviewed               # solo la persona o un pipeline con revisor; es el default
    match: ["ENV=prod"]
```

```sh
coyote infra check                        # stacks sin backend, tfstate en git, ambientes sin presupuesto…
```

### 2. Lo que puede pedir un agente

| Comando | Decisión |
| --- | --- |
| `terraform apply`, `destroy`, `import` o `state rm`; `pulumi up`; `cdk deploy`; un comando de apply del inventario, como `make apply` | Bloqueado siempre |
| `kubectl apply`, `helm upgrade` o `gcloud … create` con la marca de un ambiente `reviewed` | Bloqueado siempre |
| Ese mismo cambio sin marca, o en un ambiente `local` | Aprobación de un solo uso |
| `terraform plan`, `init` o `validate` | Aprobación normal |

### 3. El plan en el PR

```sh
terraform plan -out tfplan && terraform show -json tfplan > plan.json   # tú o el pipeline
coyote infra plan plan.json               # direcciones, tipos y acciones; nunca valores
```

En el pipeline, `coyote gate pr --plan plan.json` suma el riesgo del plan al PR. Destruir o reemplazar, permisos, llaves, entrada abierta a internet y acceso público son R3. Generar el plan en el pipeline necesita credenciales de la nube: es una decisión del repo de infraestructura, no de coyote.

El detalle está en [secrets-v1](../specs/secrets-v1.md) e [infra-v1](../specs/infra-v1.md).
