---
status: proposed
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0002: Escrituras con el git del sistema

## Contexto y problema
Los commits deben llevar la autoría de la persona que puso sus credenciales, con su firma y sus hooks. Una implementación de git dentro del binario no ve la configuración, las llaves ni el credential helper del usuario.

## Opciones consideradas
- go-git para todo: sin dependencia externa, pero ignora `user.signingkey`, `gpg.format`, credential helpers y hooks.
- git del sistema para todo: respeta la configuración del usuario; exige git instalado.
- Mixto: git del sistema para escribir y leer; go-git solo si una lectura masiva lo justifica.

## Decisión
Mixto, empezando solo con el git del sistema (versión 2.28 o superior por `init -b`).

## Consecuencias
Autoría, firma y hooks idénticos a los de un commit manual. `coyote doctor` verifica que git exista y que la identidad esté configurada.
