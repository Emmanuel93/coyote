---
status: accepted
date: 2026-09-28
deciders: "@eramirezhdez"
---
# ADR-0011: Producto multi-repo con mapa de interfaces sacado del código

## Contexto y problema
El producto vive en tres repos (servicios con sus BFF, app móvil y backoffice web), y un cambio en uno puede romper a los otros (G3). Coyote trataba cada repo por separado. Los contratos reales están en el código: anotaciones de rutas en los servicios, llamadas HTTP en los clientes y tópicos de eventos como literales. Casi no hay archivos OpenAPI ni AsyncAPI.

## Opciones consideradas
- Un agente que lea los tres repos por cada cambio: caro, lento y no se puede repetir.
- Exigir contratos OpenAPI y AsyncAPI antes de analizar: correcto a largo plazo, pero hoy no existen.
- Un mapa del producto sacado del código con extractores deterministas por stack, y un análisis de impacto que cruza cada cambio contra ese mapa.

## Decisión
Un proyecto coyote de tipo `product` registra sus repos por ruta local (solo lectura) o por URL. Tres comandos trabajan sobre ellos sin modelo y sin escribir en los repos:

- `coyote extract <repo>`: stack, comandos, módulos y documentos. Propone README.coyote.md y CONTEXT.coyote.md en `coyote/repos/<repo>/` del producto.
- `coyote map`: qué expone y qué consume cada módulo (endpoints HTTP y tópicos de eventos), con `repo@sha:ruta#L`. Se guarda en `coyote/map/` como CCF y se reconstruye desde los repos.
- `coyote impact`: cruza un cambio (diff de un repo, endpoint, tópico o texto) contra el mapa. Reporta los proveedores y consumidores afectados en cada repo, directos e indirectos a través de los BFF, y lo que no pudo resolver.

Los extractores son por stack: Spring (anotaciones, WebClient, Kafka), Dart (Dio y http) y TypeScript (fetch, axios y funciones `request`). Las rutas se comparan por segmentos, con los parámetros normalizados (`{id}`, `$id`, `${id}`, `:id`). Cuando un cambio lleva un contrato OpenAPI o AsyncAPI, el contrato manda sobre lo inferido.

## Consecuencias
- Ver un cambio contra los tres repos cuesta cero y se puede repetir en CI.
- Los agentes reciben el mapa y el impacto como contexto acotado.
- Lo que el extractor no entiende no se inventa: queda como "sin resolver", con su archivo y línea.
- El proyecto del producto vive aparte de la herramienta y de los repos. Llevar una propuesta a un repo es un cambio de ese repo, con su PR y su gate.
