# Compatibilidad de coyote 1.x

Estado: desde v1.0.0 · Decisión: ADR-0026

coyote sigue versionado semántico desde 1.0. Este documento dice qué no cambia dentro de 1.x, qué sí puede cambiar y qué pasa cuando un archivo pide una versión más nueva.

## Lo que no cambia en 1.x

| Qué | Promesa |
| --- | --- |
| Formatos v1 | CCF, CCF-doc, `project.yaml`, `hub.yaml`, SLO v1, aprobaciones, planes y golden solo crecen con campos opcionales. Quitar un campo o cambiar su sentido es una versión nueva del formato y de coyote |
| Comandos y banderas | Mantienen su nombre y su sentido. Una bandera que se retira sigue funcionando con un aviso durante al menos un minor |
| Salidas `--json` | Solo agregan campos |
| Códigos de salida | 0: todo bien. 1: hallazgos o fallas (lint, gate pr con `--policy fail`, audit, eval, secrets scan). 2: uso incorrecto o una decisión del gate que niega |
| Estándar default | El estándar 1.0 mantiene sus IDs. Una regla MUST nueva llega primero como SHOULD en un minor. Relajar o quitar una regla es un cambio mayor |

## Lo que sí puede cambiar

- **La salida para personas.** Tablas, textos y orden pueden mejorar en cualquier versión. Para automatizar, usa `--json`.
- **El gate.** Puede negar más en cualquier versión, porque es una corrección de seguridad. Puede pedir aprobaciones nuevas o bloquear algo que antes pasaba. Nunca niega menos sin un ADR.
- **Las métricas del golden.** Mejorar la recuperación sube la línea base. Bajarla más de 2 puntos falla en la CI (ADR-0025).

## `requires`

`project.yaml` y `hub.yaml` aceptan la versión mínima de coyote que los entiende:

```yaml
version: 1
requires: ">=1.0"
```

- **Lectura.** coyote lee `requires` antes que el resto. Si su versión no alcanza, se detiene con "pide coyote >=1.N y tienes 1.M: actualiza coyote", en vez de leer a medias o fallar con un error de YAML.
- **Escritura.** `coyote init` escribe la línea del binario que crea el archivo (`>=1.0` para coyote 1.0). Cuando un minor agregue un campo y coyote lo escriba, subirá `requires` a ese minor.
- **Forma.** Solo `>=MAYOR.MENOR`. El parche y las versiones previas de un release no cuentan: 1.0.0-dev ya es de la línea 1.0.
- **Builds sin versión.** Un build sin versión (`go run`, las pruebas) no compara, y `doctor` lo dice en amarillo. Un build con commit pero con una versión que no se entiende (`nightly`) no puede decir que alcanza: falla. El workflow que genera `coyote install --ci github` compila coyote con su versión, así el pipeline también compara.
- **Lo que depende de `project.yaml`.** Si un `project.yaml` no se puede leer (porque pide un coyote más nuevo o porque está roto), `secrets scan`, `secrets list`, el hook `commit-msg` y `gate pr` muestran lo que encontraron, nombran ese archivo y salen con 1 en vez de dar el visto bueno sin las reglas del proyecto: sin `secrets.files`, un secreto declarado pasaría. Un proyecto dentro de `vendor/`, `node_modules/` o `third_party/` no cuenta, y en un proyecto anidado valen también los secretos que declara el de afuera.
- **Binarios anteriores a 1.0.** No conocen `requires`. En `hub.yaml` fallan con "clave desconocida requires", que al menos la nombra. En `project.yaml` la ignoran: actualiza coyote antes de abrir un proyecto creado con 1.x.

`coyote doctor` muestra qué versión pide el proyecto y cuál tienes.

## Retiros

Lo que se retira en un minor se anuncia en `CHANGELOG.md` con su reemplazo y sigue funcionando hasta el siguiente mayor.
