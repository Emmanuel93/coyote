---
status: proposed
date: 2026-10-11
deciders: "@eramirezhdez"
---
# ADR-0028: El PRD como primera fase del ciclo (D46)

## Contexto y problema
Hasta 1.0 el ciclo de coyote empieza en el dominio (R16): `coyote-pm` escribe `domain.md` o `spec.md` y el plan parte de ahí. Ningún documento dice, antes de diseñar, qué problema se resuelve, para quién, cómo se mide que sirvió y qué queda fuera. El primer paso del plan decide el alcance por su cuenta, el agente gasta en diseñar y programar algo que nadie validó y el equipo no tiene dónde ver qué se construye y por qué.

En el gate G8 la persona pidió agregar la fase del PRD (Product Requirements Document) antes del piloto: "no lo veo establecido, de ahí partimos". Pidió dos niveles, uno del producto y uno por feature, y que fuera una plantilla, un agente, una regla y un aviso, sin comandos nuevos.

## Opciones consideradas
- **Solo una plantilla en una skill.** Barata, pero nada la conecta con el plan: se puede ignorar sin que nadie lo vea.
- **Un PRD por feature, escrito por el primer paso del plan, y nada a nivel producto.** Las metas de cada feature quedan sueltas, sin un lugar donde se comparen entre sí ni con los tres repos del producto.
- **Dos niveles, con plantilla, agente, regla SHOULD y aviso en `ws check`, sin comandos nuevos.** El plan declara de qué PRD parte y la herramienta lo revisa sin juzgarlo.
- **Además, un comando `coyote prd new|check` con números de PRD y trazabilidad de los requisitos a las pruebas.** Más superficie de compatibilidad antes de ver un solo uso real. Se reevalúa con lo que deje el piloto.
- **La regla como MUST.** Una regla MUST nueva llega primero como SHOULD (ADR-0026).
- **El PRD en un wiki o en un gestor de tickets.** Los agentes y la CI solo leen el repo, y un PRD fuera de git no se revisa junto con el plan, el ADR y el diff.

## Decisión
- **Dos niveles, la misma plantilla.**
  - El PRD del producto es `coyote/prd/producto.md`. Es un documento vivo que cubre todos los repos del producto y vive en el proyecto producto. Cada cambio es un commit que una persona revisa.
  - El PRD de una feature es `coyote/prd/<feature>.md`. Cuelga del PRD del producto: su encabezado dice de cuál parte y qué metas mueve. Si una feature necesita contradecir al producto, primero se cambia el PRD del producto.
- **La plantilla** vive en la skill `coyote-prd` y fija ocho secciones, en este orden: Problema, Usuarios, Metas y métricas, Alcance, Requisitos, Criterios de aceptación, Riesgos y Preguntas abiertas.
  - Las metas llevan ID `M1`, `M2`… y los requisitos `RQ-01`, `RQ-02`…, cada uno con su prioridad y la meta que sirve.
  - Los criterios son Gherkin y el título de cada escenario cita el requisito que prueba (`[RQ-01]`), para que un revisor o una prueba lo encuentren con `grep`.
  - Las metas, las prioridades y el alcance son de la persona. Lo que el agente no sabe va a Preguntas abiertas, con opciones.
- **Quién lo escribe.** `coyote-pm`, con la skill. Entrega el borrador como su artefacto, como cualquier otro paso, y una persona lo revisa antes de que el plan siga. Nadie obliga a usar al agente: la persona puede escribirlo a mano con la misma plantilla.
- **Cómo entra al plan.** `plan.yaml` acepta un campo opcional, `prd:`.
  - Vale una ruta del proyecto (`coyote/prd/listado.md`) o `step:ID`, cuando el primer paso del plan es el que escribe el PRD y la persona lo acepta con `ws continue`.
  - El campo crece el formato v1 sin quitar nada (docs/reference/compatibilidad.md). Un plan con `prd:` no lo lee coyote 1.0.0-rc.1: falla nombrando el campo.
- **La regla R20 (SHOULD).** Todo plan de riesgo R2 o R3 parte de un PRD que una persona revisó. `coyote ws check` avisa cuando un plan R2 o R3 no declara `prd:`.
- **Qué revisa `ws check`.**
  - Son errores un `prd:` mal escrito, un `step:` que no existe y un paso de la persona, que no deja artefacto: el plan no dice lo que quiere decir.
  - Son avisos un plan R2 o R3 sin `prd:`, un archivo que no existe todavía, un paso que no es el primero y un PRD que ningún paso recibe como entrada.
  - No juzga si el PRD es bueno.
- **Contexto.** El índice lee `coyote/prd/` como documentos, así que `coyote ask` y los paquetes de contexto citan los requisitos de un PRD con su ruta y su línea.
- **Sin comandos nuevos.** `gate pr` trata `coyote/prd/` como cualquier documento: R1, con la revisión normal del PR.

## Consecuencias
- Un plan muestra de qué PRD parte y un revisor puede medir el diff contra los `RQ-NN` en vez de contra lo que entendió el agente.
- La regla es SHOULD: un plan sin PRD corre, solo avisa. Subirla a MUST pide un ADR y pasar antes por un minor con el aviso (ADR-0026).
- El aviso comprueba que el PRD exista y esté conectado al plan, no que esté bien escrito. Esa revisión es de la persona.
- Los planes que ya existen siguen valiendo. Los de riesgo R2 o R3 sin `prd:` reciben el aviso al actualizar.
- Un PRD aceptado como artefacto de un paso queda junto a las corridas. Para que el equipo lo encuentre y otros workstreams lo citen, la persona lo guarda en `coyote/prd/`: hoy es un `cp` y un commit. Si el piloto muestra que duele, se propone un comando para eso y lo decide la persona: v1.1 trae hoy solo los pendientes de G5 a G7.
- Un PRD largo entra a un paso recortado a unos 3000 tokens, como cualquier entrada (docs/specs/workstream-v1.md). El agente lee el resto con `Read`.
