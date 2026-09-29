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
# coyote/standards/rules.yaml (coyote init --hub ya lo escribe así)
extends: hub
```

- `path` es el clon del hub, relativo al proyecto o con `~`. Una URL no vale: se clona el hub y se pone la ruta del clon. Sin caracteres de control ni invisibles.
- `ref` es una rama, una etiqueta, una rama remota (`origin/main`) o un commit completo; `main` si falta. Fijar un commit o una etiqueta hace que un cambio del hub llegue a un proyecto solo cuando ese proyecto sube su `ref`, en su propio PR.
  - Una ref corta se busca como rama, etiqueta y rama remota. Si existe en más de uno es un error: git preferiría la etiqueta, y quien pueda empujar una etiqueta llamada `main` cambiaría lo que rige sin pasar por el PR del hub.
  - Cada candidata se busca por su nombre completo exacto (`show-ref --verify`), sin las reglas de git para completar nombres: `refs/heads/release` no resuelve a una etiqueta llamada `refs/tags/refs/heads/release`. Con la ref completa no hay duda.
  - Un commit va con sus 40 caracteres. `HEAD` y sus parientes no valen: dependen de lo que el clon tenga abierto.
  - `doctor`, `status` y el lint dicen qué rige: `acme (rama main@abc1234)`, `acme (etiqueta v3@…)` o `acme (commit abc1234)`.

## Cómo se lee

- Con git de plomería: `rev-parse` de la ref, `ls-tree` y `cat-file` del commit. Sin hooks, filtros, fsmonitor ni transportes, y sin tocar el árbol de trabajo del clon.
- Las variables `GIT_*` del entorno se descartan, salvo las que eligen de dónde sale la configuración: `GIT_DIR` o `GIT_WORK_TREE`, que git exporta dentro de sus hooks y en los worktrees, harían leer otro repo.
- `hub.yaml` y los `rules.yaml` del estándar son un solo documento YAML, sin anclas ni alias: un segundo documento se ignoraría sin aviso, con las reglas que traiga. `hub.yaml` admite hasta 100 admins y 500 proyectos.
- La carpeta del clon tiene que ser la raíz de su propio repo. Una carpeta dentro de otro repo leería los commits de ese otro.
- Un symlink, una carpeta o un archivo de más de 1 MiB dentro del hub no se leen.
- Un `extends` relativo dentro del hub se resuelve en el mismo commit y no puede salir del repo del hub; uno absoluto es un error.
- Un hub declarado que no se puede leer (sin clon, sin la ref, un `hub.yaml` inválido) es un error. El estándar no vuelve en silencio a `coyote:default`.
- Sin hub declarado, `extends: hub` avisa y rige `coyote:default`.
- Con hub declarado, el estándar del proyecto pasa por `extends: hub`. Si no (por ejemplo, `extends: coyote:default`), el lint falla con S2 y `doctor` lo muestra en rojo, salvo que `rules.yaml` del proyecto declare `reason:` con el motivo. Un `extends` que lee el clon del hub del disco es un error: tomaría el árbol de trabajo, con cambios sin commit.
- Un hub cuyo clon vive dentro del proyecto no tiene la exención de la capa del hub: cuenta como el proyecto (ver standards-v1).

## Comandos

- `coyote hub status [--json]`: organización, ref y commit que rigen, admins, presupuesto y, por proyecto de la organización, si tiene clon, si sigue a este hub y en qué commit.
- `coyote doctor`: una línea con el hub que rige, o el error que impide leerlo, y otra si el estándar del proyecto no pasa por el hub.
- `coyote standards lint|show` y `coyote status`: la capa del hub con su organización, ref y commit.

## Gate

Un agente del proyecto no escribe nada en el clon del hub: ni `hub.yaml` ni el estándar, ni la carpeta `.git` del clon, que decide qué commit rige (sus refs, `packed-refs`, `config`, alternates). Un comando con efectos que nombra el clon fuera de sus datos (`git -C ../acme-hub commit`, `cd ../acme-hub && sed -i …`) se bloquea siempre; un mensaje de commit que lo menciona, o copiar desde el clon al proyecto, no. Leer el clon es libre, salvo su configuración de git, que puede llevar un token (docs/specs/gate-v1.md). Un comando que llega al clon por otro camino (`cd ..` y después `cd acme-hub`) no se reconoce y pide aprobación.

Un proyecto que vive dentro del repo del hub (un monorepo con el hub en la raíz) escribe sus propios archivos: ahí se protegen `coyote/hub.yaml` y `coyote/standards/` del hub y la carpeta de git, que es la misma.
