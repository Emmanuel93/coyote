# Hub de la organización v1

Estado: nuevo en v0.7.0 · Implementación: `internal/hub`, `coyote hub init|status`, la capa `hub` del estándar, `doctor` y la web · Decisión: ADR-0018

El hub es un proyecto coyote de tipo `hub` con lo que es de la organización y no de un proyecto: su estándar, sus dominios, sus admins, su presupuesto total y la lista de sus proyectos. Rige lo que tiene commit en la ref que cada proyecto declara, nunca el árbol de trabajo del clon.

## Crearlo

```sh
coyote hub init acme-hub --org acme
```

Crea el proyecto con `coyote/hub.yaml`, `coyote/standards/rules.yaml`, `domains/README.md` y los documentos de siempre. El hub rige desde su primer commit en `main`.

## coyote/hub.yaml

```yaml
version: 1
org: acme
admins: ["@ana"]              # ven en la web los costos de todas las personas (D10)
budgets:
  monthly_usd: 200            # tope mensual de la organización, suma de sus proyectos; 0 = sin tope
projects:                     # para la vista de admins y hub status
  - { name: shop, path: ../shop, repo: acme/shop }
```

- `org`: letras, números, punto, guion o guion bajo.
- `admins`: personas como en el ledger (`@usuario`); se aceptan sin arroba y se guardan en minúsculas.
- `projects[].path`: el clon local, relativo al hub o con `~`. Es de la máquina de cada quien: un proyecto sin clon se muestra como tal, sin fallar. `repo` (owner/nombre) es informativo.
- Una clave desconocida es un error: un `admin:` mal escrito dejaría a la organización sin admins sin que nadie lo notara.

## Declararlo en un proyecto

```yaml
# coyote/project.yaml
hub: { path: ../acme-hub, ref: main }     # o solo la ruta: hub: ../acme-hub
```

```yaml
# coyote/standards/rules.yaml
extends: hub
```

- `path` es el clon del hub, relativo al proyecto o con `~`. Una URL no vale: se clona el hub y se pone la ruta del clon.
- `ref` es una rama, etiqueta o commit; `main` si falta. Fijar un commit o una etiqueta hace que un cambio del hub llegue a un proyecto solo cuando ese proyecto sube su `ref`, en su propio PR.

## Cómo se lee

- Con git de plomería: `rev-parse` de la ref, `ls-tree` y `cat-file` del commit. Sin hooks, filtros, fsmonitor ni transportes, y sin tocar el árbol de trabajo del clon.
- La carpeta del clon tiene que ser la raíz de su propio repo. Una carpeta dentro de otro repo leería los commits de ese otro.
- Un symlink, una carpeta o un archivo de más de 1 MiB dentro del hub no se leen.
- Un `extends` relativo dentro del hub se resuelve en el mismo commit y no puede salir del repo del hub; uno absoluto es un error.
- Un hub declarado que no se puede leer (sin clon, sin la ref, un `hub.yaml` inválido) es un error. El estándar no vuelve en silencio a `coyote:default`.
- Sin hub declarado, `extends: hub` avisa y rige `coyote:default`.
- Un hub cuyo clon vive dentro del proyecto no tiene la exención de la capa del hub: cuenta como el proyecto (ver standards-v1).

## Comandos

- `coyote hub status [--json]`: organización, ref y commit que rigen, admins, presupuesto y, por proyecto de la organización, si tiene clon, si sigue a este hub y en qué commit.
- `coyote doctor`: una línea con el hub que rige, o el error que impide leerlo.
- `coyote standards lint|show` y `coyote status`: la capa del hub con su organización, ref y commit.
