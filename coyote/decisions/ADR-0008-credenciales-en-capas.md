---
status: accepted
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0008: Credenciales de la persona, en capas y fuera del proyecto

## Contexto y problema
v0.2 publica y lee en GitHub. Toda escritura debe llevar la identidad de quien pone sus credenciales y ningún secreto puede quedar en el repo ni en `.coyote/`.

## Opciones consideradas
- Pedir un token personal y guardarlo en un archivo: simple, pero deja un secreto en disco.
- Reutilizar lo que la persona ya tiene: su SSH o credential helper para git, `gh auth token` para la API y el llavero del sistema.
- OAuth device flow propio: la mejor experiencia, pero requiere registrar una OAuth App (client ID).

## Decisión
Git usa siempre las credenciales del sistema (SSH o credential helper); coyote no las toca. Para la API, el token se busca en este orden:
1. `COYOTE_GITHUB_TOKEN`, `GH_TOKEN` o `GITHUB_TOKEN`.
2. `gh auth token`.
3. El llavero del sistema, vía su binario: `security` en macOS y `secret-tool` en Linux.

`coyote auth login` guarda en el llavero, nunca en archivos. El device flow se habilita cuando la organización registre una OAuth App y configure su client ID; es una decisión de G2.

## Consecuencias
Sin secretos en disco y sin credenciales nuevas que administrar. En Linux sin `secret-tool`, `auth login` se niega a guardar el token y sugiere usar la variable de entorno o `gh`. El token nunca se imprime completo ni se escribe en el ledger.
