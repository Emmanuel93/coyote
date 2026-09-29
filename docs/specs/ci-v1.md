# Pipeline v1 — impacto, riesgo y revisión de un dueño en cada PR

Estado: nuevo en v0.5.0 · Implementación: `internal/ci`, `coyote ci impact`, `coyote gate pr`, `coyote install --ci github` · Decisiones: ADR-0013, R17, D6

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
| R3 | el pipeline, las acciones propias y CODEOWNERS; `.gitattributes` y `.gitmodules`; migraciones y SQL; `auth`, `security`, `crypto`; infraestructura (Terraform con `*.tf.json`, `*.tfvars`, `terragrunt.hcl` y `.terraform.lock.hcl`, Helm, Kubernetes, Dockerfile); la configuración del gate, de los hooks de cada IDE, del inventario (`coyote/infra.yaml`) y del estándar |
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

**gitleaks.** Con `--gitleaks reporte.json`, los hallazgos de gitleaks sobre los commits del PR se suman al reporte como R3, cada uno con archivo, línea, regla y commit; del reporte nunca se lee el valor. Los archivos con hallazgos piden la aprobación de su dueño. Sin el reporte, `gate pr` falla: sin reporte no hay revisión. Ver [Paso de gitleaks](#paso-de-gitleaks).

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
  gitleaks: true   # el pipeline escanea los commits del PR con gitleaks
```

El workflow agrega un paso antes de `gate pr`:

- **Binario fijado.** Descarga gitleaks 8.30.1 para Linux x64 de su release en GitHub y verifica el sha256 que trae coyote. Subir de versión es un cambio de coyote con su prueba.
- **Solo los commits del PR.** Escanea `base..head` con `--log-opts` y verifica antes que los dos commits existan. gitleaks sale con 0 aunque git falle y no revise nada; por eso el paso falla si gitleaks registra un error.
- **Configuración de la base.** Usa `.gitleaks.toml` y `.gitleaksignore` de la rama base (sin `.gitleaks.toml`, las reglas de gitleaks por defecto). Escanea la carpeta `.git` del repo: gitleaks busca `.gitleaksignore` en la carpeta que escanea, y en el árbol de trabajo sería el del PR. Ignora los comentarios `gitleaks:allow` que agregue el PR.
- **Sin valores.** Corre con `--redact` y solo escribe el reporte en la carpeta temporal del job, que no se publica.

Un falso positivo lo aprueba un dueño en el PR y se agrega a `.gitleaksignore` en la rama base, con su propio PR. No se usa la acción de gitleaks: pide licencia en cuentas de organización y toma la configuración del código que revisa.

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
