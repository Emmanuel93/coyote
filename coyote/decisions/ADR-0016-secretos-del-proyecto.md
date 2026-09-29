---
status: accepted
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0016: Secretos del proyecto y comandos que imprimen credenciales

## Contexto y problema
El gate bloquea siempre las credenciales de la carpeta personal (`~/.aws`, `~/.ssh`, el llavero), pero no las que viven dentro del proyecto: `.env`, `terraform.tfstate`, llaves, keystores, kubeconfig. Tampoco los comandos que las imprimen: `gcloud auth print-access-token`, `kubectl get secret`, `terraform output -json`, `vault kv get`, `env`. Un agente que lee un secreto lo tiene en su contexto y lo puede escribir en un archivo, en un comando o en un PR. Y nada impide hacer commit de un secreto.

## Opciones consideradas
- Escáneres externos (gitleaks, trufflehog) en la CI: tienen más patrones, pero llegan en el PR, no cubren lo que lee un agente y son binarios que instalar.
- En coyote: los secretos del proyecto y los comandos que imprimen credenciales cuentan como credenciales en el gate, y un escáner de alta confianza revisa commit, lint y PR.
- Lo anterior más un escáner externo en la CI.

## Decisión
- **Gate.** Leer un archivo de secretos del proyecto, o correr un comando que imprime credenciales, se bloquea siempre, aun con aprobación, igual que las credenciales de la carpeta personal.
  - **Archivos.**
    - `.env` y `.env.*`, salvo `.example`, `.sample` y `.template`; también `.envrc`.
    - `*.tfstate*`, llaves y certificados privados, keystores, `key.properties`, kubeconfig, cuentas de servicio y `secrets.*`.
    - El proyecto agrega los suyos en `project.yaml` y dispensa un falso positivo con motivo.
  - **Comandos.** Los que emiten tokens o secretos en gcloud, aws, az, kubectl, terraform y tofu, helm, vault, docker, gh y los gestores de contraseñas, más `env` y `printenv` sin argumentos.
- **Sin fatiga.** `coyote secrets list` muestra los nombres de las variables, sin sus valores.
- **Escáner.** Busca patrones de alta confianza: llaves privadas, llaves de AWS, tokens de GitHub, Slack y Stripe, cuentas de servicio de Google y URLs con usuario y contraseña.
  - `coyote commit` no deja pasar un secreto.
  - Una regla nueva, R18 (MUST), pide que ningún archivo versionado lleve secretos.
  - `gate pr` sube el riesgo a R3 y cita archivo y línea.
  - Un agente que escribe un secreto literal se detiene en el gate.
  - Un hallazgo nunca muestra el valor. Una línea con `coyote:allow-secret` queda dispensada, y la persona lo ve al revisar.
- **gitleaks en la CI** queda como opción de v0.7.

## Consecuencias
- Un agente trabaja con los nombres de las variables, no con sus valores; los valores los pone la persona.
- Algunas lecturas legítimas se bloquean, como un `.env` sin secretos: se dispensan con motivo.
- El escáner cubre lo común, no todo: un secreto de forma libre, como una contraseña escrita en texto, no se reconoce por patrón. La defensa principal es no dejar leer los archivos de secretos.
