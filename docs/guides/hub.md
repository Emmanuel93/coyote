# El hub de la organización

El hub es un repo coyote de tipo `hub` con el estándar de la organización, sus admins, su presupuesto y la lista de sus proyectos. Cada proyecto lo lee del commit que rige en su clon, nunca del árbol de trabajo. Un cambio al hub rige cuando llega a `main` por su PR (ADR-0018).

## 1. Crearlo

```sh
coyote hub init acme-hub --org acme
cd acme-hub && git add -A && coyote commit -m "chore(hub): hub de acme"
```

Edita `coyote/hub.yaml`:

```yaml
version: 1
requires: ">=1.0"
org: acme
admins: ["@ana"]                  # ven en la web el gasto de todas las personas
budgets:
  monthly_usd: 200                # tope de la organización
projects:
  - { name: tienda, path: ../tienda, repo: acme/tienda }
```

Una clave desconocida es un error: un `admin:` mal escrito dejaría a la organización sin admins sin que nadie lo notara.

## 2. Que un proyecto lo siga

En el proyecto, en una rama:

```yaml
# coyote/project.yaml
hub: { path: ../acme-hub, ref: main }
# coyote/standards/rules.yaml
extends: hub
```

```sh
coyote standards lint     # coyote:default → hub acme (rama main@…) → proyecto
coyote hub status         # admins, presupuesto y proyectos, con el commit que rige
```

Un agente no escribe en el clon del hub ni en su carpeta de git.

Para una organización nueva, `coyote init <proyecto> --hub ../acme-hub` deja el proyecto apuntando al hub desde el principio.
