---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0026: Compatibilidad de coyote 1.x (D41)

## Contexto y problema
Hasta v0.7, cada release podía cambiar formatos, comandos y reglas. El binario de v0.6 no pudo leer el `hub:` con `path` y `ref` de v0.7, y falló con un error de YAML que no decía que faltaba actualizar coyote. Un equipo que adopta la 1.0 necesita saber qué no va a cambiar y qué hacer cuando un archivo pide una versión más nueva.

## Opciones consideradas
- **Sin promesa.** Cada minor puede romper algo.
- **Semver sobre formatos, CLI y estándar**, con un campo que declara la versión mínima.
- **Leer los archivos sin estricto e ignorar las claves desconocidas.** Pierde la protección contra errores de tipeo, como un `admins` mal escrito en `hub.yaml`.

## Decisión
- **Formatos.** Los formatos v1 solo crecen con campos opcionales durante 1.x: CCF, CCF-doc, `project.yaml`, `hub.yaml`, SLO v1, aprobaciones, planes y golden. Quitar un campo o cambiar su sentido es una versión nueva del formato y de coyote.
- **`requires`.**
  - `project.yaml` y `hub.yaml` aceptan `requires: ">=1.N"`. coyote lo lee antes que el resto y, si no alcanza, dice "este proyecto pide coyote >=1.N; tienes 1.M".
  - Cuando coyote escribe un campo que llegó en 1.N, sube `requires` a ese mínimo.
  - La lectura sigue siendo estricta.
- **CLI.** En 1.x, los comandos, las banderas, las salidas `--json` y los códigos de salida no cambian de sentido:
  - los códigos son 0 cuando todo está bien, 1 cuando hay hallazgos o fallas y 2 cuando el uso es incorrecto;
  - una bandera que se retira sigue funcionando con un aviso durante al menos un minor;
  - la salida para personas sí puede cambiar.
- **Estándar.**
  - El estándar default es la versión 1.0.
  - Los IDs de sus reglas son estables.
  - Una regla MUST nueva llega primero como SHOULD en un minor, y sube a MUST en el siguiente.
- **Excepción: el gate.** El gate puede negar más en cualquier versión, porque es una corrección de seguridad. Nunca niega menos sin un ADR.
- **Registro.** `CHANGELOG.md` lista, por versión, lo agregado, lo corregido y lo que se retira.

## Consecuencias
- Un proyecto con `requires` le avisa a quien tenga un coyote viejo, en vez de fallar raro.
- Un binario anterior a 1.0 no conoce `requires`: falla con "clave desconocida", que al menos nombra la clave.
- Endurecer el gate puede pedir aprobaciones nuevas en un minor. Es a propósito.
