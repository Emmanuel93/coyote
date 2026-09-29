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
- **Configuración de la base.** El paso toma `.gitleaks.toml` y `.gitleaksignore` de la rama base e ignora los `gitleaks:allow` del PR.
  - Escanea la carpeta `.git` del repo: con gitleaks 8.30.1 comprobamos que lee `.gitleaksignore` de la carpeta que escanea aunque se le pase otra ruta, y en el árbol de trabajo sería el del PR.
  - Cambiar `.gitleaks.toml` o `.gitleaksignore` es R3 por su ruta: una dispensa nueva la aprueba un dueño en su propio PR.
- **Dos escaneos.** Una revisión adversarial mostró tres caminos para meter un secreto sin que gitleaks lo viera en `base..head`:
  - resolverlo en un merge (`git log -p` no muestra el diff de un merge);
  - agregarlo en una ruta que gitleaks ignora y moverlo con un rename;
  - marcarlo como binario con un `.gitattributes` del PR.

  Por eso el paso escanea los commits con `--remerge-diff --no-renames` y, aparte, el diff neto del PR: un commit con el árbol del PR sobre el merge-base, escrito en una carpeta de objetos temporal fuera del clon, con autor y fecha fijos y sin firma. Git corre sin atributos del repo (`GIT_ATTR_SOURCE` apunta al árbol vacío). `gate pr` junta los dos reportes y cuenta una vez cada archivo con su regla.
- **Segunda revisión.**
  - gitleaks salta el archivo del repo cuya ruta es igual a la de `--config`: la configuración va por ruta absoluta.
  - Las reglas por defecto saltan cualquier ruta que contenga `gitleaks.toml` y los nombres que terminan en `go.mod`, `go.sum` o `go.work`: esos nombres raros son R3 en `gate pr`.
  - Un falso positivo se exceptúa sin commit (`archivo:regla:línea`): el commit del diff neto cambia en cada corrida.
- **Tercera revisión.** Sin `.gitleaks.toml` en la base, el paso ya no usa `useDefault`: descarga las reglas por defecto de la misma versión, verificadas por hash, y les quita la lista global de rutas que saltan (lockfiles, `node_modules/`, imágenes, cualquier ruta con `gitleaks.toml`), que un PR podía usar para esconder un archivo. El resultado también va verificado por hash.
- **Sin falsos verdes.** gitleaks 8.30.1 sale con 0 cuando git falla (una base que no existe, un git que se cae): no revisa nada y dice "no leaks found".
  - El paso verifica antes que los dos commits existan.
  - Corre git por un envoltorio que deja sus errores en el log.
  - Falla si hay un error en el log, ya sin los colores que gitleaks escribe aunque se le pida que no.
- **Sin valores.** Corre con `--redact`. `gate pr --gitleaks` lee del reporte solo archivo, línea, regla y commit, y suma cada hallazgo como R3. Un reporte que no es una lista JSON de hallazgos hace fallar `gate pr`.
- **Falla cerrado.** Si gitleaks no corre, el paso falla; si encuentra algo, decide la política del pipeline (`warn` o `fail`).

## Consecuencias
- Un secreto con forma conocida por gitleaks llega al reporte del PR aunque el escáner de coyote no lo reconozca.
- Un falso positivo se dispensa en la configuración de la rama base, que revisan sus dueños.
- El pipeline descarga de GitHub, en cada corrida, el binario y, sin configuración en la base, las reglas por defecto; los hashes evitan que cambien sin que se note.
- Quedan fuera los archivos binarios o en UTF-16, los mensajes de commit, lo que excluye la configuración de la base y un secreto quitado con un force-push, que igual hay que rotar (docs/specs/ci-v1.md).
