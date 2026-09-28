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

- corre en `pull_request` y en cada revisión (`pull_request_review`), solo para ramas del mismo repo: un fork no recibe secretos ni corre coyote;
- tiene permisos `contents: read` y `pull-requests: write`, y no guarda credenciales en los checkouts;
- trae el PR en su último commit, los otros repos en su rama principal y coyote en una versión etiquetada, con acciones fijadas por commit;
- compila coyote sin red de módulos y corre `coyote gate pr`.

Nada de lo que entra al YAML o al shell pasa sin validar: nombres, repos, versión, política y reglas de riesgo.

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

Lee el evento del PR (`GITHUB_EVENT_PATH`, o `--base` y `--head`), arma el mapa con los repos y analiza el diff `base...head`. El reporte va:

- en la salida;
- en el resumen del job (`GITHUB_STEP_SUMMARY`);
- con `--comment`, en un solo comentario del PR, marcado con `<!-- coyote:impacto -->`, que se actualiza en cada push en lugar de repetirse.

Con `fail`, el chequeo falla si el cambio rompe a un consumidor.

## coyote gate pr

Es la aprobación en equipo sin claves compartidas. Hace lo mismo que `ci impact` y además decide si el cambio pide la revisión de un dueño.

**Riesgo por rutas.** Cada archivo cambiado toma el riesgo más alto de las reglas que le aplican. Las reglas genéricas:

| Riesgo | Rutas |
|--------|-------|
| R3 | el pipeline y CODEOWNERS; migraciones y SQL; `auth`, `security`, `crypto`; infraestructura (Terraform, Helm, Kubernetes, Dockerfile); la configuración del gate y del estándar |
| R2 | contratos de API (OpenAPI, AsyncAPI, proto, Avro, GraphQL); dependencias (`go.mod`, `pom.xml`, Gradle, `package.json`, `pubspec.yaml`…); configuración del servicio (`application*.yml`) |

El proyecto agrega las suyas en `coyote/project.yaml`, un archivo que ningún agente puede editar. `install --ci` las lleva a cada workflow:

```yaml
risk:
  R3: ["services/*/src/**/pagos/**"]
  R2: ["**/*.graphqls"]
```

**Riesgo por impacto.** Romper a un consumidor es R3. Llegar a otro repo del producto es R2.

**Revisión.** Un cambio R2 o R3 pide la aprobación de un dueño de lo que cambia:

- **Qué se revisa:** los archivos de riesgo por ruta y los archivos cuyas interfaces cambian.
- **Quién es dueño:** sale del CODEOWNERS de la **rama base**, así un PR no se nombra dueño a sí mismo. La última regla que coincide manda.
  - Un `@org/equipo` cuenta solo si el token puede verificar la membresía.
  - Sin CODEOWNERS, vale cualquier persona que no abrió el PR.
- **Cómo se aprueba:**
  - quien abrió el PR no cuenta;
  - un dueño que pide cambios detiene el merge;
  - en R3, la aprobación tiene que ser del último commit.

El comentario muestra el riesgo, los archivos, quién puede aprobar y el impacto. Con `--policy fail`, el chequeo falla hasta que llega la aprobación. El workflow vuelve a correr con cada revisión. Para exigirla, se marca el chequeo "coyote gate pr / riesgo e impacto" como requerido en la protección de la rama.

## Límites

- El mapa lee patrones, no compila: lo que no pudo leer aparece como tal en el reporte (docs/specs/product-v1.md).
- GitHub responde igual cuando una persona no es de un equipo y cuando el token no ve el equipo. coyote distingue los dos casos consultando el equipo; si no lo ve, lo dice y esa aprobación no cuenta.
- Una revisión descartada deja de contar; un comentario después de aprobar no quita la aprobación.
