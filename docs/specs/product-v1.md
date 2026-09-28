# Producto v1 — mapa de interfaces e impacto entre repos

Estado: nuevo en v0.4.0 · Implementación: `internal/product`, `coyote map`, `coyote extract`, `coyote impact` · Decisión: ADR-0011

## Para qué

Un producto suele vivir en varios repos: servicios con sus BFF, una app móvil, un backoffice web. Un cambio en uno puede romper a otro sin que nadie lo note antes del merge. Un proyecto coyote de tipo `product` une esos repos, saca del código qué expone y qué consume cada uno, y cruza cada cambio contra ese mapa.

Todo es determinista: sin modelo, sin red y sin costo. Se puede repetir en la CI.

## El proyecto del producto

```sh
coyote init mi-producto --type product --purpose "tienda en tres repos"
coyote repo add servicios --path ../servicios      # ruta local, relativa al proyecto o absoluta
coyote repo add app --path ../app
coyote repo add backoffice --path ../backoffice
```

| Ruta | Qué es | Se versiona |
|------|--------|-------------|
| `coyote/project.yaml` | los repos, por ruta local (y URL si la tienen) | sí |
| `coyote/map/<repo>.map` | las interfaces de cada repo | sí |
| `coyote/repos/<repo>/` | README.coyote.md y CONTEXT.coyote.md propuestos para cada repo | sí |
| `.coyote/` | cachés | no |

## Los repos se leen, nunca se escriben

Coyote lee los repos del producto sin modificarlos ni correr lo que su configuración declare:

- Git corre con `GIT_OPTIONAL_LOCKS=0` y los diffs usan comandos de plomería (`diff-tree`, `diff-index`): `git diff` y `git status` pueden reescribir el índice aunque solo se les pida leer.
- Sin hooks, fsmonitor, diff externo, textconv ni submódulos. Los filtros `clean`, `smudge` y `process` que declare la configuración se anulan por nombre; si un nombre no se puede anular, no se compara el árbol de trabajo.
- Ningún transporte: un clon parcial no trae objetos (traerlos escribiría en `.git` y correría `uploadpack` o `ssh`).
- `safe.directory` se limita a la carpeta registrada.
- Lo que coyote escribe queda en el proyecto del producto, nunca a través de un symlink, y con temporales de nombre al azar.

## Qué se extrae

| Stack | Expone | Llama | Eventos |
|-------|--------|-------|---------|
| Spring (Java, Kotlin) | `@RequestMapping` en la clase y `@GetMapping`, `@PostMapping`… en los métodos | WebClient y RestClient (`.uri`, `.path` dentro de `.uri`), sus envoltorios `get(…)`/`post(…)`, RestTemplate, clientes `@FeignClient` | `@KafkaListener` y `send` de un `KafkaTemplate`, también desde métodos propios (`send(TOPIC, …)`) |
| Dart (Flutter) | — | Dio y `http`: `.get('/x/$id')` | — |
| TypeScript, JavaScript | — | `fetch`, `axios`, `request` y `api.get(…)`; el verbo sale de las opciones de la misma llamada | — |

Las constantes se resuelven en este orden:

1. Las del mismo archivo.
2. Las de la clase indicada (`Topics.PAGO`) o importada con `import static`.
3. Un nombre con un único valor en el módulo o en el repo.

Los `${propiedad}` se resuelven con `application.yml` o `application.properties` del módulo, o con su valor por defecto. También se resuelven los campos de una clase `@ConfigurationProperties`. Una concatenación (`"/cuentas/" + id + "/libro"`) da `/cuentas/{}/libro`.

Lo que no se puede leer no se inventa: queda como "sin resolver", con su archivo y línea.

El mapa es el del árbol de trabajo, con lo nuevo que git no ignora. Si hay código sin commit, el archivo del mapa lo dice en una nota.

## Formato del mapa

```
# coyote map v1|servicios|a1b2c3d
# árbol de trabajo: 3 archivos de código sin commit
expone|GET|/api/v1/pedidos/{}|services/pedidos|services/pedidos/src/…/PedidosController.java|42|/api/v1/pedidos/{id}
llama|POST|/pedidos|packages/pedidos|packages/pedidos/lib/api.dart|12|/pedidos
publica|-|pedidos.pedido-creado|services/pedidos|…/Publisher.java|25|TOPIC
escucha?|-|${app.topics.x}|services/envios|…/Escucha.java|7|${app.topics.x}
```

- Una línea por interfaz, en orden: el diff de un commit muestra qué cambió en los contratos.
- Campos: `rol|verbo|ruta|módulo|archivo|línea|texto original`. El rol termina en `?` si no se resolvió. Las rutas se normalizan: los parámetros son `{}`.
- Los enlaces no se guardan: se deducen siempre de las entradas.

## Enlaces

- Una llamada se enlaza con el endpoint que mejor coincide por segmentos: los literales pesan más, los parámetros cuentan menos y los verbos deben coincidir si los dos se conocen.
- Una ruta sin ningún segmento fijo (`/{}`) no se enlaza.
- Si dos servicios empatan (dos BFF exponen `/notifications`), gana el servicio con el que ya habla el módulo que llama o, si no, su repo. Si sigue el empate, el enlace queda marcado como ambiguo.
- Un tópico se enlaza por nombre exacto entre quien lo publica y quien lo escucha.

`coyote map` muestra, por repo, cuántas interfaces hay y cuántas quedaron enlazadas, sin enlace o sin resolver. También muestra las llamadas sin proveedor en el producto: servicios externos, tópicos que nadie publica o huecos del extractor. `coyote map --check` falla si el mapa guardado ya no refleja el código, sin contar cambios de línea. `--ambiguous` lista los empates.

## Impacto de un cambio

```sh
coyote impact "GET /api/v1/pedidos/{id}"
coyote impact --topic pedidos.pedido-creado
coyote impact --diff servicios=main...HEAD --format md          # para el PR
coyote impact --diff servicios=HEAD                               # lo que aún no tiene commit
coyote impact --diff servicios=main...feat --diff app=main...feat # un cambio coordinado
coyote impact "cancelar pedido"                                   # texto libre
```

El reporte tiene tres niveles:

- **Cambia:** las interfaces que toca el cambio.
- **Afecta directo:** quienes las consumen o las proveen, en otros módulos o repos.
- **Afecta a través de un servicio intermedio:** los clientes de un BFF cuya implementación llega, por llamadas entre métodos (hasta tres saltos), a la llamada afectada. Si no se puede seguir por método, se sigue por archivo y se marca como posible.

En un diff:

- Cada archivo de código se compara antes y después, así que el reporte dice qué interfaces se agregan y cuáles se eliminan.
- Quien usa algo que se elimina aparece como **se rompe**.
- Un cambio dentro del método de un endpoint lo toca. Un tipo que cambia (un DTO) toca las interfaces cuyo método lo menciona.
- Las líneas del mapa se comparan con el lado del diff del que es el mapa: base, final o árbol de trabajo. Si el mapa no es de ningún lado, el reporte lo avisa.

Salidas: texto, `--format md` (tablas para un PR) y `--json`. `--record` deja un evento `rev` en el ledger.

## Propuestas por repo

`coyote extract` propone `README.coyote.md` y `CONTEXT.coyote.md` para cada repo en `coyote/repos/<repo>/`:

- **Identidad del repo:** propósito (el primer párrafo en prosa de su README), comandos para correr, probar y construir, y módulos con sus interfaces.
- **Dependencias con otros repos del producto:** `dep|servicios|llama 35 endpoints de bff-movil`.
- **Invariantes de contrato:** quién usa cada módulo y de quién depende.
- **Brechas:** sin contratos OpenAPI o AsyncAPI, y lo que quedó sin resolver.

Las propuestas respetan los topes de CCF-doc, y lo que no cabe se cuenta y queda en el mapa. Cada archivo lleva una huella: si la persona lo edita, `coyote extract` no lo reemplaza (salvo `--force`). Para llevar una propuesta a su repo se copia con un PR de ese repo.

## Contexto para agentes

El índice incluye las interfaces del mapa y las propuestas de cada repo:

- `coyote ask "quién llama /pedidos"` encuentra las interfaces.
- `coyote get context --query …` agrega la sección "Interfaces del producto".
- `coyote get context <repo>` usa la propuesta mientras el repo no tenga sus propios documentos.
- La caché del índice de otro repo queda en el proyecto que pregunta, nunca en el repo.

## Límites conocidos

- Los extractores leen patrones, no compilan. Un cliente generado, una ruta armada en tiempo de ejecución o un tópico elegido por un mapa de configuración (`topics.get(proveedor)`) quedan sin enlace o sin resolver.
- Un DTO que cambia marca los endpoints cuyo método lo menciona; decidir si el cambio es de contrato sigue siendo de la persona.
- Los repos se leen por ruta local; los que solo tienen URL usan su mapa guardado.
