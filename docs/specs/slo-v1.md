# SLOs y alertas como código v1

Estado: nuevo en v0.7.0 · Implementación: `internal/slo`, `coyote slo check|rules`, regla R19, `coyote gate pr`, gate de comandos de alertas, sección `/slo` de la web · Decisión: ADR-0020

Un servicio declara sus SLOs en `coyote/slo/<servicio>.yaml`, en su proyecto coyote o en la raíz de su repo aunque ese repo no sea un proyecto coyote (un repo del producto). coyote los valida, genera sus reglas de Prometheus y, en un PR, dice si el cambio relaja un SLO. coyote no habla con Mimir ni con Prometheus: cargar las reglas lo hacen la persona o un pipeline.

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

- Una clave desconocida es un error, como en el resto de los archivos de coyote. El archivo es un solo documento YAML, sin anclas ni alias, de hasta 256 KiB y 40 SLOs.
- coyote no valida PromQL completo; la CI de quien carga las reglas corre `promtool check rules`. Sí revisa, fuera de las comillas, lo que haría que una consulta falle o mida otra cosa:
  - la ventana es siempre `{{.window}}`, dentro de `[..]`; una subconsulta lleva un paso de 1s a 5m o vacío (`[{{.window}}:1m]`), porque un paso más largo deja a las alertas sin muestras. Una ventana fija (`[5m]`, `[1h30m]`, `[300]`) o `{{.window}}` fuera de una ventana, también dentro de un texto, son errores;
  - sin otros marcadores, sin comentarios `#` (se tragarían el paréntesis que agrega coyote) y sin `offset` ni `@`, que corren la ventana; `offset` en cualquier combinación de mayúsculas;
  - cada selector nombra una métrica, también después de `or`, `and`, `unless` o `bool`: `{job="x"}` sin nombre, o con `__name__` en cualquier forma, elige varias métricas. `rate()` quita el nombre y, si dos métricas comparten etiquetas, la regla falla en cada evaluación y el SLO nunca se calcula. Se suma cada métrica con `(sum(rate(m[{{.window}}])) or vector(0))`; el `or vector(0)` evita que una métrica que no existe deje vacía toda la suma;
  - paréntesis, corchetes, llaves y comillas balanceados (un texto entre acentos graves no tiene escapes), UTF-8 válido y hasta 2000 caracteres.
- Prometheus ejecuta como plantilla el texto de las etiquetas y anotaciones de una alerta: `description`, `annotations`, los valores de `labels` y `runbook` no llevan `{{` ni `}}`. Un `{{ range }}` en una anotación colgaría la alerta justo cuando dispara.
- Con la alerta page prendida, el objetivo tiene que dejar que dispare: con 30 días, por debajo de 83.34 % los dos umbrales de la page pasan del 100 % de errores.
- Las etiquetas `slo_id`, `slo_service`, `slo_name`, `slo_window` y `slo_severity` las pone coyote.
- Un servicio se declara en un solo archivo por repo: `x.yaml` y `x.yml` en la misma carpeta, o el mismo servicio en otra carpeta de proyecto, generarían reglas con los mismos nombres, y mimirtool toma el nombre del archivo como namespace. `slo check` lo rechaza y en un PR es R3.
- Un SLI que no es una proporción (la saturación de una cola, errores sostenidos) no va aquí: es una alerta de umbral escrita a mano.

## Reglas generadas

`coyote slo rules` escribe `coyote/slo/prometheus/<servicio>.yaml`, un archivo de reglas de Prometheus que carga el ruler de Mimir o de Prometheus. Es determinista: el mismo archivo de SLOs da los mismos bytes. No se edita a mano.

Por cada SLO, tres grupos:

1. **SLI.** `slo:sli_error:ratio_rate<ventana>` en 5m, 30m, 1h, 2h, 6h, 1d y 3d. La del periodo (30d o 28d) promedia la de 5m con `sum_over_time` entre `count_over_time`, para no evaluar 30 días de datos en cada ciclo; por eso ninguna muestra puede ser NaN:
   - con `errors` y `total`: `((errors) or (0 * (total))) / ((total) > 0)`. Sin tráfico no hay muestra, en vez de un NaN que envenenaría el promedio de todo el periodo. Mientras la serie de errores no existe (suele aparecer con el primer error, por ejemplo después de un despliegue), el error cuenta 0: sin eso, el periodo solo promediaría los minutos con errores;
   - con `error_ratio`: `((error_ratio) >= 0)`, que deja fuera NaN y valores negativos.
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
| Quitar el archivo o un SLO, cambiar el servicio, o un servicio que no se llama como su archivo | R3 |
| Bajar un objetivo o cambiar el periodo (cambian los umbrales de todas las alertas) | R3 |
| Cambiar la consulta de errores o de totales, aunque sea un espacio (qué cuenta como falla) | R3 |
| Apagar la page o el ticket (con la page apagada, sin ticket el SLO se queda sin alertas), o cambiar el nombre o las etiquetas de la page (a quién le llega) | R3 |
| Cambiar las etiquetas de todo el archivo | R3 |
| Un archivo que no valida | R3 |
| Reglas generadas que no salen byte por byte de cada archivo que las genera (editadas a mano o sin regenerar), que faltan (aunque su archivo ya no valide), que no tienen archivo o cuyo archivo no valida | R3 |
| Cualquier otro archivo en `coyote/slo/prometheus/` (un `.yml`, una subcarpeta, aunque imite otro proyecto): un cargador que recorre la carpeta lo cargaría | R3 |
| Un servicio en más de un archivo de SLOs del repo (`x.yaml` y `x.yml`, o en otra carpeta) | R3 |
| Agregar un archivo o un SLO, subir un objetivo, prender la page | R2 |
| Cambiar las etiquetas del ticket, el runbook, la descripción o las anotaciones | R2 |

Una base que ya no valida (por ejemplo, después de subir coyote) se compara igual: arreglarla en el PR no esconde una relajación. El texto que `gate pr` cita de un archivo va sin saltos de línea, enlaces (tampoco `https://…` ni `www.…` sueltos, que GitHub enlaza solo), referencias a issues ni menciones.

## Gate

Cargar reglas o configuración de alertas, o silenciarlas, es un comando de infraestructura con efectos (ADR-0017):

- `mimirtool` y `cortextool` con `load`, `sync` o `delete` en cualquier lugar después del programa (kingpin acepta banderas entre el grupo y el subcomando), `amtool` con `add`, `expire`, `import` o `update`, y cualquiera de los tres con argumentos `@archivo`. El programa cuenta también con el nombre del binario de un release (`mimirtool-linux-amd64`) o como imagen con etiqueta o digest (`grafana/mimirtool:2.14.0`);
- `promtool push`, que manda muestras a un remote write y podría tapar un SLI;
- un cliente HTTP que escribe (curl con `-X POST|PUT|DELETE|PATCH` o con datos, wget con `--method` o `--post-*`, httpie) en las APIs de reglas, alertas, silencios o remote write (`/config/v1/rules`, `/api/v1/rules`, `/api/v2/silences`, `/api/v2/alerts`, `/-/reload`, `/api/v1/push`);
- cualquiera de estos programas con un subcomando o un argumento que llega al correr (una variable, `xargs`, `"$@"`, un alias).

Un agente pide aprobación de un solo uso y, contra un ambiente `reviewed`, queda bloqueado siempre. `coyote slo check` y `coyote slo rules --check|--stdout` solo leen; la última bandera manda (`--check --check=false` escribe y pide aprobación).
