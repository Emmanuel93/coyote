# Pipeline v1 — impacto, riesgo y revisión de un dueño en cada PR

Estado: nuevo en v0.5.0; en v0.7.0 suma SLOs y el paso opcional de gitleaks · Implementación: `internal/ci`, `coyote ci impact`, `coyote gate pr`, `coyote install --ci github` · Decisiones: ADR-0013, ADR-0020, ADR-0021, R17, D6

Cada PR de un repo del producto se ve contra todo el producto antes del merge. El análisis sale del código, sin modelo y sin costo, en unos segundos. Corre en GitHub Actions del repo; no necesita el proyecto del producto ni escribe en los repos.

## Instalar

En el proyecto del producto (`type: product`), con los repos registrados y sus URLs de GitHub:

```sh
coyote install --ci github [--policy warn|fail] [--coyote-ref v0.5.0] [--coyote-repo owner/coyote]
coyote install --ci github --check      # para la CI del proyecto: ¿los workflows están al día?
```

Escribe `coyote/ci/<repo>.yml` por repo. Llevarlo a cada repo es un PR en ese repo, como `.github/workflows/coyote.yml`. El workflow:

- **Disparador.** Corre en `pull_request_target`: el workflow, la política y las reglas de riesgo salen de la rama base, así un PR no puede cambiar el chequeo que lo evalúa.
  - El código del PR se trae como dato y se lee con git de plomería: nunca se compila ni se ejecuta.
  - Por eso los forks se evalúan igual que las ramas del repo, en lugar de saltarse: un job saltado cuenta como éxito en un chequeo requerido.
- **Permisos.** `contents: read` y `pull-requests: write`; ningún checkout guarda credenciales.
- **Checkouts.** El PR en su último commit, los otros repos en su rama principal y coyote en una versión etiquetada, con acciones fijadas por commit.
- **Paso final.** Compila coyote sin red de módulos y corre `coyote gate pr`.

Nada de lo que entra al YAML o al shell pasa sin validar: nombres, repos, versión, política y reglas de riesgo.

`pull_request_target` no corre con las revisiones. Después de aprobar, se vuelve a correr el chequeo ("Re-run") o se empuja un commit: coyote lee las revisiones en el momento. Un disparador `pull_request_review` correría la versión del workflow que trae el PR, y con eso un PR podría aprobarse su propio chequeo.

### Secretos

| Secreto | Para qué |
|---------|----------|
| `COYOTE_PRODUCT_TOKEN` | leer los otros repos del producto (Contents: read) |
| `COYOTE_TOOL_TOKEN` | leer el repo de coyote, si es de otra cuenta que el producto: un token fino de GitHub cubre los repos de un solo dueño; sin él se usa el anterior |
| `COYOTE_TEAMS_TOKEN` | opcional: Members: read de la organización, para verificar los equipos de CODEOWNERS |

Alguien con permiso de escritura en el repo puede usar los secretos desde un workflow propio. Por eso los tokens son de solo lectura y se limitan a los repos del producto.

## coyote ci impact

```sh
coyote ci impact --repo servicios=repos/servicios --repo app=repos/app --self servicios [--policy warn|fail] [--comment]
```

Lee el evento del PR (`GITHUB_EVENT_PATH`, o `--base` y `--head`), arma el mapa con los repos y analiza el diff `base...head`. No analiza forks: con `fail`, un PR de un fork no pasa. El reporte va:

- en la salida;
- en el resumen del job (`GITHUB_STEP_SUMMARY`);
- con `--comment`, en un solo comentario del PR, marcado con `<!-- coyote:impacto -->`, que se actualiza en cada push en lugar de repetirse.

Con `fail`, el chequeo falla si el cambio rompe a un consumidor.

## coyote gate pr

Es la aprobación en equipo sin claves compartidas. Hace lo mismo que `ci impact` y además decide si el cambio pide la revisión de un dueño.

**Riesgo por rutas.** Cada archivo cambiado toma el riesgo más alto de las reglas que le aplican. La lista sale de `git diff-tree --name-status -z`: cuentan los binarios, los archivos vacíos, los que `.gitattributes` marca sin diff y los nombres con comillas. Las reglas genéricas:

| Riesgo | Rutas |
|--------|-------|
| R3 | el pipeline, las acciones propias y CODEOWNERS; `.gitattributes` y `.gitmodules`; `.gitleaks.toml` y `.gitleaksignore`, y los nombres que gitleaks salta sin mirar (cualquier ruta con `gitleaks.toml`, o que termina en `go.mod`, `go.sum` o `go.work` sin llamarse así); migraciones y SQL; `auth`, `security`, `crypto`; infraestructura (Terraform con `*.tf.json`, `*.tfvars`, `terragrunt.hcl` y `.terraform.lock.hcl`, Helm, Kubernetes, Dockerfile); la configuración del gate, de los hooks de cada IDE, del inventario (`coyote/infra.yaml`) y del estándar |
| R2 | contratos de API (OpenAPI, AsyncAPI, proto, Avro, GraphQL); dependencias (`go.mod`, `pom.xml`, Gradle, `package.json`, `pubspec.yaml`…); configuración del servicio (`application*.yml`) |

El proyecto agrega las suyas en `coyote/project.yaml`, un archivo que ningún agente puede editar. `install --ci` las lleva a cada workflow:

```yaml
risk:
  R3: ["services/*/src/**/pagos/**"]
  R2: ["**/*.graphqls"]
```

**Riesgo por impacto.** Romper a un consumidor es R3. Llegar a otro repo del producto es R2.

**Plan de Terraform.** En un repo de infraestructura, el pipeline puede generar el plan y pasarlo con `--plan plan.json`: su riesgo se suma al del PR y el comentario lleva la tabla de recursos, sin valores (docs/specs/infra-v1.md). Generarlo necesita credenciales de la nube en el pipeline; coyote no las pide.

El riesgo del plan es del cambio entero: lo aprueba un dueño de lo que cambió el PR, aunque el PR no toque rutas de riesgo (un plan que reemplaza una base de datos por un cambio en un `.tfvars`). Un riesgo que pide revisión nunca queda sin grupo de dueños: sin archivos que lo expliquen, aprueba cualquier dueño de lo cambiado o, sin CODEOWNERS, cualquier persona que no abrió el PR.

**Secretos.** Las dispensas de secretos (`secrets.allow` de `coyote/project.yaml`) salen de la rama base: un PR no se dispensa a sí mismo.

**Secretos (R18).** Lo que agrega el PR pasa por el escáner de secretos, como dato y sin ejecutar nada: los archivos de secretos nuevos por su nombre, las líneas agregadas y, completos, los archivos que git lista sin hunks. Un PR que agrega secretos queda en R3 y no pasa con ninguna aprobación: el secreto ya está en GitHub y hay que rotarlo. El comentario dice archivo, línea y tipo, nunca el valor (docs/specs/secrets-v1.md).

**SLOs.** Un archivo de `coyote/slo/` es R2 por su ruta. `gate pr` lo compara con el de la rama base, como dato, y sube a R3 lo que relaja un SLO: bajar un objetivo, quitar un SLO, cambiar qué cuenta como error, apagar la alerta page o cambiar a quién le llega (docs/specs/slo-v1.md).

**gitleaks.** Con `--gitleaks reporte.json` y `--gitleaks-net neto.json`, los hallazgos de gitleaks sobre los commits y sobre el diff neto del PR se suman al reporte como R3, cada uno con archivo, línea, regla y commit; del reporte nunca se lee el valor. Los archivos con hallazgos piden la aprobación de su dueño. Sin el reporte, o con uno que no es una lista JSON de hallazgos, `gate pr` falla: sin reporte no hay revisión. Ver [Paso de gitleaks](#paso-de-gitleaks).

**Revisión.** Un cambio R2 o R3 pide la aprobación de un dueño de lo que cambia:

- **Qué se revisa:** los archivos de riesgo por ruta y los archivos cuyas interfaces cambian.
- **Quién es dueño:** sale del CODEOWNERS de la **rama base**, así un PR no se nombra dueño a sí mismo. La última regla que coincide manda.
  - Un `@org/equipo` cuenta solo si el token puede verificar la membresía.
  - Un dueño por correo no se puede comparar con quien aprueba: esa regla espera y el reporte pide nombrar `@persona` o `@org/equipo`.
  - Sin CODEOWNERS, vale cualquier persona que no abrió el PR.
- **Cómo se aprueba:**
  - no cuentan quien abrió el PR ni quien escribió o subió commits en él;
  - un dueño que pide cambios detiene el merge, y también quien pide cambios y no se puede verificar como dueño;
  - en R3, la aprobación tiene que ser del último commit.

El comentario muestra el riesgo, los archivos, quién puede aprobar y el impacto. Con `--policy fail`, el chequeo falla hasta que llega la aprobación. Para exigirla, se marca el chequeo "coyote gate pr / riesgo e impacto" como requerido en la protección de la rama.

### Bandera `pr_enforcement`

Que un chequeo bloquee el merge depende de GitHub: en repos privados, exigir un chequeo pide un plan de pago. La bandera deja lista la función para cuando exista:

```yaml
# coyote/project.yaml del producto; ningún agente puede editarlo
features:
  pr_enforcement: false   # apagada: gate pr avisa (warn); prendida: el chequeo falla hasta que aprueba un dueño (fail)
```

- **La política sale de la bandera.** `coyote install --ci github` toma de ella la política de los workflows, y `--policy` manda sobre la bandera con un aviso.
- **Al prenderla, se regeneran los workflows.** `install --ci github --check` avisa que están desactualizados y cada workflow pasa a `fail`. Después se marca el chequeo como requerido en la protección de la rama.
- **Banderas desconocidas.** Una bandera que coyote no conoce es un error de `project.yaml`.

### Paso de gitleaks

Es opcional (ADR-0021). Se prende en el proyecto del producto y se regeneran los workflows:

```yaml
features:
  gitleaks: true   # el pipeline escanea el PR con gitleaks
```

El workflow agrega un paso antes de `gate pr`:

- **Binario fijado.** Descarga gitleaks 8.30.1 para Linux x64 de su release en GitHub y verifica el sha256 que trae coyote. Subir de versión es un cambio de coyote con su prueba.
- **Dos escaneos.**
  - Los commits del PR (`base..head`), con `--remerge-diff`, para ver lo que entra al resolver un merge, y `--no-renames`, para que un archivo movido se lea entero.
  - El diff neto del PR: un commit aparte con el árbol del PR sobre el merge-base, escrito fuera del clon. Ve lo que se integra aunque haya llegado por un camino que el primer escaneo no lee, como un archivo que fue binario en un commit intermedio. El commit lleva autor y fecha fijos y sin firma: no depende de la identidad de git del runner.
  - `gate pr` recibe los dos reportes (`--gitleaks` y `--gitleaks-net`) y cuenta una vez cada archivo con su regla. Lo que solo aparece en el diff neto va marcado "diff del PR".
- **Configuración de la base.** Usa `.gitleaks.toml` y `.gitleaksignore` de la rama base e ignora los comentarios `gitleaks:allow` que agregue el PR. Sin `.gitleaks.toml`, usa las reglas por defecto de esa versión de gitleaks, descargadas y verificadas por hash, sin su lista global de rutas que salta (lockfiles, `node_modules/`, imágenes, cualquier ruta con `gitleaks.toml`…): un PR no puede esconder un archivo en esas rutas. El archivo que resulta también va verificado por hash.
  - Escanea la carpeta `.git` del repo: gitleaks busca `.gitleaksignore` en la carpeta que escanea, y en el árbol de trabajo sería el del PR.
  - Pasa la configuración por ruta absoluta: gitleaks salta el archivo del repo cuya ruta es igual a la de `--config`, y una ruta del repo nunca es absoluta.
  - Lee sin atributos de git (`GIT_ATTR_SOURCE` apunta al árbol vacío): un `.gitattributes` del PR no esconde un archivo como binario.
  - Cambiar `.gitleaks.toml` o `.gitleaksignore` es R3 por su ruta: una dispensa nueva la aprueba un dueño.
- **Sin falsos verdes.** gitleaks sale con 0 aunque git falle y no revise nada. El paso verifica antes que los dos commits existan y corre git por un envoltorio que deja sus errores en el log. Falla ante cualquier error de git o de gitleaks.
- **Sin valores.** Corre con `--redact` y solo escribe los reportes en la carpeta temporal del job, que no se publica.
- **Cambio de base.** El workflow también corre con `edited`: cambiar la rama base de un PR vuelve a correr el chequeo.

Un falso positivo lo aprueba un dueño en el PR y se agrega a `.gitleaksignore` en la rama base, con su propio PR, sin commit: `archivo:regla:línea`. La forma con commit que imprime gitleaks solo calla el primer escaneo, porque el commit del diff neto cambia en cada corrida; sin commit, la excepción vale para ese archivo en cualquier commit. No se usa la acción de gitleaks: pide licencia en cuentas de organización y toma la configuración del código que revisa.

Lo que el paso no ve:

- Lo que excluye la configuración de la base, también en el diff neto. Una base con `.gitleaks.toml` que extiende las reglas por defecto (`useDefault = true`) hereda su lista de rutas que salta: imágenes, fuentes, documentos de Office, PDF y binarios por su extensión; lockfiles; `node_modules/`, `bower_components/`, algunas rutas de `vendor/` y de Python; los wrappers de Gradle y Maven; y cualquier ruta que contenga `gitleaks.toml`, `verification-metadata.xml` o `Database.refactorlog`. Los nombres raros de esa lista (`*gitleaks.toml*`, nombres que terminan en `go.mod`, `go.sum` o `go.work`) son R3 en `gate pr`. Sin `.gitleaks.toml`, el paso no salta ninguna.
- Un secreto dentro de un archivo binario o en UTF-16, y los mensajes de commit.
- Un secreto que se subió y se quitó con un force-push: ya no está en los commits del PR, aunque GitHub lo guardó. Hay que rotarlo igual.
- Un `[extend] path` en el `.gitleaks.toml` de la base: el paso solo copia ese archivo, así que gitleaks no encuentra la otra configuración y el paso falla.

### Probar sin tocar la rama principal

`pull_request_target` usa el workflow de la rama base del PR. Para ensayar el pipeline sin integrar nada a `main`:

1. Se deja el workflow en una rama (`coyote/pipeline`).
2. Se abren PRs contra esa rama.
3. Se cierran sin merge.

Para cerrar el círculo, conviene activar además "Require review from Code Owners" en la protección de la rama, con CODEOWNERS sobre `.github/`. Así, cambiar el workflow que corre el gate también pasa por un dueño, en un PR aparte, porque el workflow que evalúa ese PR es el de la rama base.

## Límites

- El mapa lee patrones, no compila: lo que no pudo leer aparece como tal en el reporte (docs/specs/product-v1.md).
- GitHub responde igual cuando una persona no es de un equipo y cuando el token no ve el equipo. coyote distingue los dos casos consultando el equipo; si no lo ve, lo dice y esa aprobación no cuenta.
- Una revisión descartada deja de contar; un comentario después de aprobar no quita la aprobación.
- Con dueños por correo o equipos que el token no ve, un pedido de cambios de cualquier persona detiene el merge: coyote no puede descartar que sea dueña. Es a propósito, y se resuelve nombrando `@personas` o dando Members: read.
- Un PR con 250 commits o más no deja saber quién los escribió (GitHub no los lista todos): ninguna aprobación cuenta.
- Las rutas y los tópicos que salen del código del PR van como código en el comentario: no arman enlaces ni HTML.
