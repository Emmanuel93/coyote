---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0022: Clave por sesión en `coyote web` (D37)

## Contexto y problema
`coyote web` escucha solo en loopback y valida el Host contra DNS rebinding (ADR-0019). Aun así, cualquier usuario de la misma máquina puede pedir la página y ver lo que ve quien la corre: costos por persona según D10, la cola del gate y las aprobaciones vigentes. v0.7 lo dejó como límite conocido y prometió una clave por sesión para v1.0.

## Opciones consideradas
- **Clave en una cookie.** La primera visita cambia la clave de la URL por una cookie. Pero las cookies no se separan por puerto (RFC 6265, §8.5): otro usuario que levante un servidor en `127.0.0.1:8080` y logre que el navegador lo visite recibe la cookie.
- **Clave en la ruta de cada enlace** (`/s/<clave>/…`). El navegador solo la manda a esa URL.
- **Socket Unix con los permisos del usuario.** El navegador no abre un socket Unix sin un proxy.
- **Identificar al usuario del otro lado de la conexión TCP.** En Linux se puede por `/proc/net/tcp`; en macOS no hay API para TCP. No es portátil.
- **Contraseña HTTP básica.** Es una credencial más que guardar y que el navegador recuerda.

## Decisión
- **Clave por corrida.** Al arrancar, `coyote web` toma 32 bytes de `crypto/rand` y los codifica en base64url.
  - Imprime la URL con la clave (`http://127.0.0.1:7410/s/<clave>/`) solo en la terminal.
  - La clave no se escribe en disco ni en el ledger, y no aparece en los argumentos del proceso.
- **La ruta lleva la clave.** Todos los enlaces de la página cuelgan de `/s/<clave>/`. Una petición fuera de ese prefijo, o con otra clave, responde 404 sin decir por qué. La comparación es de tiempo constante (`crypto/subtle`).
- **Sin fugas por cabeceras.**
  - `Referrer-Policy: no-referrer`: un enlace externo no recibe la URL con la clave.
  - `Cache-Control: no-store`.
- **Lo que ya había sigue igual.** Loopback, validación de Host, solo lectura y sin JavaScript.

## Consecuencias
- Otro usuario de la máquina ya no puede leer la página sin la clave.
- Un marcador no sirve de una corrida a otra: la URL cambia cada vez que se arranca.
- Quien vea la terminal, o pegue la URL en un chat, comparte el acceso hasta que se cierre `coyote web`.
- Quien tenga los permisos de la persona, o de root, en la misma máquina sigue pudiendo leer el proceso. Eso queda fuera del alcance (docs/specs/web-v1.md).
