# SLOs y alertas

Un servicio declara sus SLOs en `coyote/slo/<servicio>.yaml`. coyote genera sus alertas de Prometheus y, en cada PR, dice si el cambio relaja un SLO. Cargar las reglas en Mimir o en Prometheus lo hacen la persona o un pipeline, nunca un agente (ADR-0020).

## 1. Declarar el SLO

En el proyecto, o en la raíz del repo del servicio aunque no sea un proyecto coyote:

```yaml
# coyote/slo/pagos-api.yaml
version: 1
service: pagos-api                  # igual al nombre del archivo
labels: { team: pagos }             # se suman a todas las reglas
period: 30d                         # 30d o 28d
slos:
  - name: disponibilidad
    objective: 99.9
    description: Respuestas sin error 5xx
    sli:
      errors: sum(rate(http_server_request_duration_seconds_count{job="pagos-api",http_response_status_code=~"5.."}[{{.window}}]))
      total: sum(rate(http_server_request_duration_seconds_count{job="pagos-api"}[{{.window}}]))
    alerts:
      runbook: coyote/runbooks/pagos-api-disponibilidad.md   # obligatorio mientras la page esté prendida
```

- La ventana se escribe `{{.window}}`: coyote pone ahí cada ventana de las alertas. Una ventana fija, como `[5m]`, es un error.
- Cada selector nombra su métrica, sin `offset`, `@` ni `scalar()`.
- Un SLI que no es una proporción, como la saturación de una cola, no va aquí: es una alerta de umbral escrita a mano.

## 2. Generar las reglas

```sh
coyote slo check     # valida cada archivo, su runbook y que las reglas estén al día
coyote slo rules     # escribe coyote/slo/prometheus/pagos-api.yaml
```

Haz commit de los dos archivos. Las reglas generadas no se editan a mano: el mismo archivo de SLOs da siempre los mismos bytes.

Por cada SLO salen las reglas del SLI, sus metadatos y cuatro alertas con dos ventanas cada una:

| Alerta | Consumo | Ventanas | Gasta del presupuesto |
| --- | --- | --- | --- |
| page | 14.4 | 1h y 5m | 2 % en una hora |
| page | 6 | 6h y 30m | 5 % en seis horas |
| ticket | 3 | 1d y 2h | 10 % en un día |
| ticket | 1 | 3d y 6h | 10 % en tres días |

## 3. Cargarlas

En el pipeline del repo de observabilidad, o desde tu terminal:

```sh
coyote slo rules --check                             # falla si alguien olvidó regenerar
promtool check rules coyote/slo/prometheus/*.yaml
mimirtool rules load --address "$MIMIR_URL" --id "$TENANT" coyote/slo/prometheus/*.yaml
```

Si un agente pide `mimirtool`, `amtool` o escribir en una API de reglas o de silencios, necesita tu aprobación de un solo uso. Contra un ambiente `reviewed` del [inventario de infraestructura](secretos-e-infra.md#infraestructura), se bloquea siempre.

## 4. En un PR

| Cambio | Riesgo |
| --- | --- |
| Bajar un objetivo, cambiar el periodo o una consulta, apagar la page, cambiar a quién le llega o editar a mano las reglas generadas | R3 |
| Agregar un SLO, subir un objetivo, cambiar el runbook o la descripción | R2 |

Los dos los aprueba un dueño del CODEOWNERS, nunca el autor del PR. En el lint, la regla R19 revisa que cada alerta salga de su archivo, que esté al día y que la page enlace un runbook. La página SLOs de `coyote web` muestra cada objetivo, su presupuesto de error en minutos, si las reglas están vigentes y su runbook.

La tabla completa está en [slo-v1](../specs/slo-v1.md).
