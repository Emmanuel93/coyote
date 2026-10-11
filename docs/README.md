# Documentación de coyote

La documentación sigue cuatro formas, según lo que necesitas en ese momento.

| Si quieres… | Lee | Qué hay |
| --- | --- | --- |
| Aprender desde cero | [Tutorial](tutorial/primeros-15-minutos.md) | Tus primeros 15 minutos: un proyecto con contexto, un commit, el gate, una aprobación y el reporte de lo que pasó. Una prueba corre cada comando del tutorial, así que funciona tal como está escrito |
| Resolver una tarea | [Guías](guides/README.md) | El gate en tu IDE, contexto, workstreams, el pipeline de PRs, el hub, SLOs, secretos e infraestructura, y auditoría |
| Consultar un detalle | [Referencia de comandos](reference/cli.md), [especificaciones](specs/), [compatibilidad](reference/compatibilidad.md), [build](reference/build.md) | Cada comando y bandera (generado desde la CLI), cada formato y lo que no cambia en 1.x |
| Entender por qué | [Por qué coyote es así](explanation/diseno.md), [modelo de amenazas](security/threat-model.md), [decisiones](../coyote/decisions/) | Los principios, lo que se protege y de quién, y cada decisión con sus opciones |

## Historia del proyecto

- [CHANGELOG](../CHANGELOG.md): qué cambió en cada versión.
- [Reportes de release](releases/): la evidencia que se presentó en cada gate, con sus revisiones adversariales.
- [Plan de ejecución](plan/EXECUTION_PLAN.md): el razonamiento de cada release, sus tareas y sus decisiones.
