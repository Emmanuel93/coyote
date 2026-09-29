---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0021: gitleaks como opción del pipeline

## Contexto y problema
El escáner de coyote solo reconoce formas de alta confianza (ADR-0016). gitleaks tiene cientos de reglas, pero en un PR lee la configuración, el `.gitleaksignore` y los comentarios `gitleaks:allow` del mismo código que revisa: el autor del PR lo podría apagar. Su acción de GitHub pide licencia en cuentas de organización.

## Opciones consideradas
- La acción oficial: licencia en organizaciones y configuración tomada del PR.
- El binario en un paso del workflow que genera coyote, fijado por hash y con la configuración de la rama base.
- No usarlo: se pierden patrones que el escáner propio no tiene.

## Decisión
- **Opcional.** `features.gitleaks` en `project.yaml`; `install --ci github` agrega el paso y `--check` lo verifica.
- **Binario fijado.** El paso descarga gitleaks v8.30.1 para Linux x64 y verifica su sha256 contra el que trae coyote. Subir de versión es un cambio de coyote con su prueba.
- **Configuración de la base.** El paso toma `.gitleaks.toml` y `.gitleaksignore` de la rama base, ignora los `gitleaks:allow` del PR y escanea solo sus commits (`--log-opts base..head`). Escanea la carpeta `.git` del repo: con gitleaks 8.30.1 comprobamos que lee `.gitleaksignore` de la carpeta que escanea aunque se le pase otra ruta, y en el árbol de trabajo sería el del PR.
- **Sin falsos verdes.** gitleaks 8.30.1 sale con 0 cuando git falla (una base que no existe): no revisa nada y dice "no leaks found". El paso verifica antes que los dos commits existan y falla si gitleaks registra un error.
- **Sin valores.** Corre con `--redact`. `gate pr --gitleaks` lee del reporte solo archivo, línea, regla y commit, y suma cada hallazgo como R3.
- **Falla cerrado.** Si gitleaks no corre, el paso falla; si encuentra algo, decide la política del pipeline (`warn` o `fail`).

## Consecuencias
- Un secreto con forma conocida por gitleaks llega al reporte del PR aunque el escáner de coyote no lo reconozca.
- Un falso positivo se dispensa en la configuración de la rama base, que revisan sus dueños.
- El pipeline descarga un binario de GitHub en cada corrida; el hash evita que cambie sin que se note.
