---
status: proposed
date: 2026-09-27
deciders: "@eramirezhdez"
---
# ADR-0004: Dependencias mínimas y compilación sin red

## Contexto y problema
El entorno de construcción no alcanza el proxy de módulos de Go, y cada dependencia amplía la cadena de suministro que la herramienta audita en otros repos.

## Opciones consideradas
- cobra más yaml.v3 desde el proxy: ergonomía estándar, pero no compila sin red.
- Solo biblioteca estándar: sin YAML, que es el formato de configuración del diseño.
- Biblioteca estándar más go-yaml copiado en `third_party/` con `replace` local.

## Decisión
Biblioteca estándar para la CLI y go-yaml v3.0.5 (fork mantenido) copiado con su licencia y procedencia en `third_party/go-yaml`.

## Consecuencias
Compila sin red en cualquier máquina y en CI; el SBOM tiene una sola dependencia. Actualizar go-yaml es reemplazar la copia o quitar el `replace`.
