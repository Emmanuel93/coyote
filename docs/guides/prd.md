# PRD: del problema al plan

Un PRD (Product Requirements Document) dice qué se construye, para quién, cómo se sabe que sirvió y qué queda fuera, antes de que alguien lo diseñe o lo programe. En coyote es un archivo de `coyote/prd/` que `coyote-pm` redacta, una persona revisa y el plan declara como su punto de partida (R20, ADR-0028).

Hay dos niveles con la misma plantilla:

- **El PRD del producto** (`coyote/prd/producto.md`) es un documento vivo que cubre todos los repos del producto.
- **El PRD de una feature** (`coyote/prd/<feature>.md`) cuelga de él: dice cuáles de sus metas mueve y no las contradice.

El ejemplo completo está en [`examples/acme-shop/coyote/prd/`](../../examples/acme-shop/coyote/prd/).

## 1. Qué es cada documento

| Documento | Responde | Dónde vive |
| --- | --- | --- |
| PRD del producto | Qué producto es, para quién y cómo se mide | `coyote/prd/producto.md` |
| PRD de una feature | Qué se construye ahora y qué metas del producto mueve | `coyote/prd/<feature>.md` |
| Dominio o spec (R16) | Las entidades, las reglas y los escenarios | `docs/domain.md`, `spec.md` |
| ADR (R8) | Una decisión técnica, con sus opciones | `coyote/decisions/` |
| Plan | Cómo se ejecuta, con topes y puntos de revisión | `coyote/workstreams/W-xxxx/plan.yaml` |

El PRD va primero: el dominio, el ADR y el plan lo toman como punto de partida.

## 2. La plantilla

Ocho secciones con estos títulos y en este orden. La skill `coyote-prd` se las enseña a `coyote-pm`.

| Sección | Qué lleva |
| --- | --- |
| Problema | Qué duele y a quién, en una frase, con su evidencia |
| Usuarios | Un rol por línea: qué intenta lograr y cómo lo hace hoy |
| Metas y métricas | `M1`, `M2`… con su métrica, línea base, objetivo y fecha |
| Alcance | Lo que entra, lo que no y los supuestos |
| Requisitos | Una tabla con `RQ-01`… , su prioridad (debe, debería, podría) y la meta que sirven |
| Criterios de aceptación | Gherkin; todo requisito que "debe" tiene al menos un escenario, y entre todos hay un camino feliz, un error y un borde. El título de cada escenario cita su requisito: `Escenario: … [RQ-01]` |
| Riesgos | Qué puede salir mal, a quién le pega, cómo se mitiga y quién lo vigila |
| Preguntas abiertas | Lo que decide una persona, con sus opciones |

Las metas, las prioridades y el alcance son decisiones tuyas. Si el agente no las tiene, las propone y las deja como pregunta abierta. El PRD del producto agrega al final una tabla **Features** con el PRD de cada una, su estado, las metas que mueve y su workstream.

Un ID de requisito es un solo requisito en todo `coyote/prd/`: `RQ-03` dice lo mismo en el PRD del producto y en el de la feature que lo construye, con el mismo texto. Una feature copia el ID y el texto de los requisitos del producto que construye, y los nuevos toman un número que nadie usa. Así `grep RQ-03 coyote/prd` encuentra un solo requisito.

## 3. El PRD del producto

Pídele un borrador a `coyote-pm` y revísalo:

```sh
coyote run --agent coyote-pm "Escribe el PRD del producto con la skill coyote-prd. Deja como preguntas abiertas lo que no sepas" --risk R1 --max-usd 1
```

`coyote run` imprime dónde dejó el artefacto (`coyote/runs/…-coyote-pm.md`). Completa las metas y guarda el PRD donde vive:

```sh
mkdir -p coyote/prd
sed -n '/^# PRD/,$p' coyote/runs/20261011-101500-coyote-pm.md > coyote/prd/producto.md
git add coyote/prd && coyote commit -m "docs(prd): PRD del producto"
```

Desde ese commit el índice lo lee: `coyote ask` y los paquetes de contexto citan sus requisitos con su ruta y su línea.

## 4. El PRD de una feature, como primer paso del plan

El primer paso del plan lo escribe `coyote-pm`, y el segundo lo recibe como entrada. El plan lo declara con `prd:`:

```yaml
id: W-0007
title: cobros idempotentes
autonomy: supervised
risk: R2
prd: step:S1                   # el plan parte de la entrega del paso S1
budget_usd: 3
steps:
  - id: S1
    does: escribir el PRD de la feature
    agent: coyote-pm
    input: [coyote/prd/producto.md]
    output: PRD de la feature, con "Parte de" y las metas que mueve
    sections: [Problema, Usuarios, Metas y métricas, Alcance, Requisitos, Criterios de aceptación, Riesgos, Preguntas abiertas]
    max_usd: 0.8
  - id: S2
    does: diseñar el cobro
    agent: coyote-architect
    input: [step:S1]
    output: diseño, con un apartado por requisito
    max_usd: 1
```

```sh
coyote ws run W-0007          # corre S1 y se detiene para que lo leas
coyote ws continue W-0007     # aceptas el PRD y corre S2
coyote ws continue W-0007 --redo "falta el caso de la tarjeta rechazada"
```

Aceptar el paso es aprobar el PRD: queda en el ledger. Si lo rechazas con `--redo`, `coyote-pm` ve su entrega anterior y tu observación. Después guarda una copia en `coyote/prd/` para que el equipo la encuentre y otros workstreams la citen, igual que en el apartado anterior.

Si el PRD ya existe, `prd:` apunta al archivo `.md` y el paso que lo necesita lo recibe como entrada:

```yaml
prd: coyote/prd/cobros-idempotentes.md
steps:
  - { id: S1, does: diseñar el cobro, agent: coyote-architect, input: [coyote/prd/cobros-idempotentes.md], output: diseño, max_usd: 1 }
```

Un PRD largo entra a un paso recortado a unos 3000 tokens, como cualquier entrada. El agente lee el resto con `Read`.

Lo que un paso recibe de un archivo es dato, no una instrucción: una línea con forma de orden para un agente llega con una marca que lo dice, y los caracteres invisibles se muestran escritos con su código, con una marca aparte que dice que son caracteres que no se ven. Un final de línea de Windows llega como un salto de línea y los acentos, el CJK y los emojis llegan intactos.

En `autonomous`, autorizar el plan es autorizar también lo que lee. `coyote review` lista cada archivo del PRD y de las entradas con las primeras cifras de su huella. Si uno cambia después de aprobar, el motor pide una autorización nueva; si cambia mientras corre, se detiene antes del paso que lo recibe y dice cuál. Y el paso que escribe el PRD lleva `gate: human`: sin esa revisión, el paso siguiente usaría un PRD que nadie leyó (con `--mode supervised`, que se detiene tras cada paso, no hace falta). Un archivo que un paso del plan escribe y otro lee detiene el loop por lo mismo: pasa lo que un paso produce al siguiente con `step:ID`.

## 5. Qué revisa `coyote ws check`

```sh
coyote ws check W-0007
```

| Hallazgo | Nivel |
| --- | --- |
| Un plan R2 o R3 sin `prd:` (R20) | aviso |
| `prd:` que no es una ruta `.md` del proyecto ni `step:ID` en minúsculas, que es un `diff:` o que lleva un salto de línea, un control, un carácter invisible o un espacio al borde de un nombre | error |
| `step:ID` que no existe, o que es un paso de la persona | error |
| En `autonomous`, el paso del PRD sin `gate: human` (se mira el modo con el que corre: con `--mode supervised` se acepta) | error |
| El archivo existe pero no es un archivo regular del proyecto (un enlace, una carpeta o más de 8 MB) | error |
| El archivo todavía no existe | aviso |
| El archivo no es texto | aviso |
| `step:ID` con un paso de agente antes: esos corren sin PRD | aviso |
| Ningún paso recibe el PRD como entrada | aviso |

R20 es SHOULD: un plan sin PRD corre, solo avisa. Los errores sí lo detienen, porque el plan no dice de qué parte. El comando no juzga si el PRD es bueno: eso lo revisas tú.

## 6. Mantenerlo vivo

- **Cambia el PRD del producto primero.** Si una feature necesita otra meta, se cambia `producto.md` en su propio commit y después la feature.
- **Un commit por cambio.** Un PRD que cambia pasa por el PR con la revisión de siempre (`coyote/prd/` es R1 para `gate pr`).
- **El estado dice dónde está.** `borrador`, `revisado`, `aprobado` o `construido`. Al construir una feature, actualiza su fila en la tabla Features.
- **Si cambias un requisito mientras corre un plan**, los pasos aceptados no se repiten solos. En `manual` y `supervised`, si el PRD es un archivo, los pasos que faltan leen el texto nuevo; si es la entrega de un paso, es la que aceptaste. En `autonomous`, el cambio de un archivo que el plan lee pide autorizar otra vez: el motor se detiene antes del paso que lo recibe. Rehaz con `ws continue --redo` el paso que dependa del cambio.
