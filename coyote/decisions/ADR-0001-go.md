---
status: accepted
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0001: Go como lenguaje de la herramienta

## Contexto y problema
Coyote se distribuye a personas con distintos IDEs y sistemas operativos, corre como CLI, como hook de IDE en cada llamada a herramienta y como servicio de equipo. Necesita arrancar en milisegundos y no depender de un runtime instalado.

## Opciones consideradas
- Go: un binario estático por plataforma, `go:embed`, compilación cruzada, SDK oficiales de MCP y de la API de Claude.
- Scala: comparte JVM con servicios Java, pero exige runtime y arranca lento para un hook.
- TypeScript: ecosistema del Agent SDK, pero exige Node en cada máquina.

## Decisión
Go (decisión D1 del plan aprobado).

## Consecuencias
Un binario por plataforma; los hooks corren en milisegundos; la integración con el Agent SDK de TypeScript, si hace falta, va como proceso aparte (D3).
