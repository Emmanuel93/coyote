# El pipeline de PRs

En cada PR, coyote calcula el riesgo de lo que cambia, el impacto en los otros repos del producto y quién lo tiene que aprobar. Lo deja en un comentario. El código del PR se lee como dato y nunca se ejecuta (ADR-0013).

## 1. Instalarlo

En el proyecto del producto, en una rama:

```sh
coyote install --ci github --policy warn     # warn reporta; fail hace fallar el chequeo hasta que aprueba un dueño
```

`install` escribe un workflow en cada repo del producto, con las acciones fijadas por commit. En GitHub, cada repo necesita el secreto `COYOTE_PRODUCT_TOKEN`: un token fino de solo lectura (Contents: read) sobre los repos del producto.

## 2. Leer el reporte

| Riesgo | Qué lo dispara |
| --- | --- |
| R3 | El pipeline o CODEOWNERS; migraciones y SQL; `auth`, `security`, `crypto`; infraestructura; la configuración del gate y del estándar; un ledger reescrito; un SLO relajado; un secreto; controles de dirección del texto en el código; romper a un consumidor de otro repo |
| R2 | Contratos de API, dependencias, configuración del servicio, SLOs, instrucciones de los agentes (`AGENTS.md`, `CLAUDE.md`, `CONTEXT.coyote.md`…), texto con forma de orden para un agente en un documento, llegar a otro repo del producto |

- Un cambio R2 o R3 lo aprueba un dueño del CODEOWNERS de la rama base, nunca el autor del PR.
- Después de aprobar, vuelve a correr el chequeo o empuja un commit.
- El proyecto agrega reglas propias en `project.yaml`, por ejemplo `risk: { R3: ["services/*/src/**/pagos/**"] }`.

La lista completa está en [ci-v1](../specs/ci-v1.md).

## 3. gitleaks, si lo quieres

En `coyote/project.yaml`:

```yaml
features:
  gitleaks: true
```

Después corre `coyote install --ci github` de nuevo. El paso baja gitleaks fijado por hash, usa la configuración de la rama base y suma cada hallazgo como R3, sin mostrar el valor (ADR-0021).
