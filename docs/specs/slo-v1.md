# SLOs y alertas como código v1

Estado: nuevo en v0.7.0 · Implementación: `internal/slo`, `coyote slo check|rules`, regla R19, `coyote gate pr`, gate de comandos de alertas, sección `/slo` de la web · Decisión: ADR-0020

Un servicio declara sus SLOs en `coyote/slo/<servicio>.yaml`. coyote los valida, genera sus reglas de Prometheus y, en un PR, dice si el cambio relaja un SLO. coyote no habla con Mimir ni con Prometheus: cargar las reglas lo hacen la persona o un pipeline.

## Archivo

```yaml
version: 1
service: pagos-api                 # igual al nombre del archivo: minúsculas, números y guiones
labels: { team: pagos }            # se suman a todas las reglas y alertas
period: 30d                        # 30d (por defecto) o 28d
slos:
  - name: disponibilidad
    objective: 99.9                # porcentaje: de 50 a menos de 100, hasta cuatro decimales
    description: Respuestas sin error 5xx
    sli:                           # errors y total, o error_ratio; {{.window}} donde va la ventana
      errors: sum(rate(http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}[{{.window}}]))
      total: sum(rate(http_server_request_duration_seconds_count{job="pagos-api"}[{{.window}}]))
    alerts:
      name: PagosApiDisponibilidad # opcional; por defecto, servicio y SLO en CamelCase
      runbook: coyote/runbooks/pagos-api-disponibilidad.md   # obligatorio con page: ruta del repo o URL https
      labels: {}                   # para page y ticket
      annotations: {}              # summary, description y runbook_url los pone coyote; aquí se agregan o cambian
      page: { labels: { severity: page } }        # disable: true la apaga
      ticket: { labels: { severity: ticket } }
```

- Una clave desconocida es un error, como en el resto de los archivos de coyote.
- Las consultas usan `{{.window}}` y ningún otro marcador. Una ventana fija (`[5m]`) es un error: la ventana la pone coyote en cada regla.
- coyote revisa la forma de las consultas (paréntesis, corchetes, llaves y comillas), no PromQL completo. La CI de quien carga las reglas corre `promtool check rules`.
- Las etiquetas `slo_id`, `slo_service`, `slo_name`, `slo_window` y `slo_severity` las pone coyote.
- Un SLI que no es una proporción (la saturación de una cola, errores sostenidos) no va aquí: es una alerta de umbral escrita a mano.

## Reglas generadas

`coyote slo rules` escribe `coyote/slo/prometheus/<servicio>.yaml`, un archivo de reglas de Prometheus que carga el ruler de Mimir o de Prometheus. Es determinista: el mismo archivo de SLOs da los mismos bytes. No se edita a mano.

Por cada SLO, tres grupos:

1. **SLI.** `slo:sli_error:ratio_rate<ventana>` en 5m, 30m, 1h, 2h, 6h, 1d y 3d, con la consulta de errores entre la de totales. La del periodo (30d o 28d) promedia la de 5m con `sum_over_time` entre `count_over_time`, para no evaluar 30 días de datos en cada ciclo.
2. **Metadatos.** `slo:objective:ratio`, `slo:error_budget:ratio`, `slo:time_period:days`, `slo:current_burn_rate:ratio`, `slo:period_burn_rate:ratio` y `slo:period_error_budget_remaining:ratio`.
3. **Alertas**, con el método de varias ventanas y varias tasas de consumo del libro de SRE de Google:

| Alerta | Consumo | Ventana larga y corta | Gasta del presupuesto |
| --- | --- | --- | --- |
| page | 14.4 | 1h y 5m | 2 % en una hora |
| page | 6 | 6h y 30m | 5 % en seis horas |
| ticket | 3 | 1d y 2h | 10 % en un día |
| ticket | 1 | 3d y 6h | 10 % en tres días |

La alerta dispara si las dos ventanas de un renglón pasan el umbral: la larga evita que un pico de minutos despierte a alguien y la corta hace que la alerta se apague pronto cuando el problema pasa. Con un periodo de 28 días los factores se recalculan para gastar la misma parte del presupuesto (13.44, 5.6, 2.8 y 0.93).

Cada alerta lleva `slo_severity` (`page` o `ticket`), las etiquetas del archivo y las anotaciones `summary`, `description` y `runbook_url`.

## Revisión

- `coyote slo check` valida cada archivo, que su nombre sea el del servicio, que el runbook de cada SLO exista en el repo y que las reglas generadas estén al día. También avisa de reglas generadas sin su archivo.
- **R19 (MUST)** corre esa revisión en `coyote standards lint`. Un proyecto sin `coyote/slo/` cumple.
- `coyote slo rules --check` falla si hay que regenerar, sin escribir (para la CI).

## En un PR

`gate pr` compara cada archivo de SLOs de la rama base con el del PR, como dato. La ruta ya es R2 ("los SLOs y sus alertas"); el contenido sube a R3 lo que puede relajar un SLO o evitar que una page llegue:

| Cambio | Riesgo |
| --- | --- |
| Quitar el archivo o un SLO, o cambiar el servicio | R3 |
| Bajar un objetivo | R3 |
| Cambiar la consulta de errores o de totales (qué cuenta como falla) | R3 |
| Apagar la page, o cambiar su nombre o sus etiquetas (a quién le llega) | R3 |
| Cambiar las etiquetas de todo el archivo | R3 |
| Un archivo que no valida | R3 |
| Agregar un archivo o un SLO, subir un objetivo, prender la page | R2 |
| Apagar el ticket o cambiar sus etiquetas, cambiar el runbook o el periodo | R2 |

## Gate

Cargar reglas o configuración de alertas, o silenciarlas, es un comando de infraestructura con efectos (ADR-0017): `mimirtool` y `cortextool` con `rules load|sync|delete` o `alertmanager load|delete`, y `amtool` con `silence add|expire|import` o `alert add`. Un agente pide aprobación de un solo uso y, contra un ambiente `reviewed`, queda bloqueado siempre. `coyote slo check` y `coyote slo rules --check|--stdout` solo leen.
