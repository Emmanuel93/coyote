---
status: proposed
date: 2026-09-29
deciders: "@eramirezhdez"
---
# ADR-0020: SLOs y alertas como código

## Contexto y problema
`coyote-sre` propone SLOs, pero nada los valida ni los convierte en alertas, y un SLO relajado en un PR pasa como cualquier YAML. El destino del piloto es Faro: Mimir con su ruler, que recibe reglas en el formato de Prometheus.

## Opciones consideradas
- Un generador externo (Sloth, Pyrra) u OpenSLO con su herramienta: más maduros, pero son binarios o CRDs y no saben de gates ni de PRs.
- Un formato propio y chico, cercano a esos, con un generador en Go.
- Alertas hechas en la interfaz de Grafana: no son código ni pasan por revisión.

## Decisión
- **Formato.** `coyote/slo/<servicio>.yaml` v1 (spec en `docs/specs/slo-v1.md`):
  - SLOs de proporción: una consulta de eventos malos y una de totales con `{{.window}}`, o una proporción directa;
  - objetivo, periodo (30 o 28 días) y etiquetas;
  - alertas `page` y `ticket`, cada una con sus etiquetas, y el runbook de la alerta.
- **Generador.** `coyote slo rules` escribe `coyote/slo/prometheus/<servicio>.yaml`, determinista y con el método del libro de SRE de Google:
  - el SLI en 5m, 30m, 1h, 2h, 6h, 1d, 3d y el periodo;
  - el objetivo y el presupuesto como reglas;
  - page con consumo de 14.4 en 1h y 5m o de 6 en 6h y 30m; ticket con 3 en 1d y 2h o 1 en 3d y 6h;
  - si el periodo no es de 30 días, los factores se recalculan para gastar la misma parte del presupuesto.
- **Revisión.** `coyote slo check` valida el formato, que lo generado esté vigente y que cada alerta page enlace un runbook que exista. La regla R19 lo pide en el lint.
- **PR.** `gate pr` compara cada SLO de la base con el del PR. Relajar es R3: bajar el objetivo, apagar la alerta page, quitar un SLO o cambiar qué cuenta como error o como total. Lo demás es R2.
- **Carga.** coyote no habla con Faro. Cargar reglas o silenciar alertas (`mimirtool`, `cortextool`, `amtool`) es un comando de infraestructura con efectos en el gate: un agente pide aprobación y, contra un ambiente `reviewed`, se bloquea siempre (ADR-0017).

## Consecuencias
- Un SLO llega al PR con lo que cambia y con su riesgo; las alertas salen de él y no se editan a mano.
- Las consultas son PromQL que coyote revisa solo en su forma. `promtool check rules` es el paso recomendado en la CI de quien carga las reglas.
- Los SLIs de umbral que no son proporción (saturación de una cola, errores sostenidos) quedan como alertas escritas a mano, fuera de este formato.
