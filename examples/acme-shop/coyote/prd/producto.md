# PRD: acme-shop

Estado: aprobado
Responsable: @ana
Fecha: 2026-09-28

Proyecto sintético: la tienda, las personas y los datos son ficticios. Este es el PRD del producto, el documento vivo del que cuelga el PRD de cada feature (`alta-de-pedidos.md`).

## Problema

Los pedidos pagados quedan sin confirmar hasta la conciliación de la noche, y cada pedido atascado termina en un reclamo al equipo de operaciones. Evidencia: `docs/domain.md` (un pedido solo pasa a `confirmado` con un pago capturado) y el riesgo anotado en `CONTEXT.coyote.md`.

## Usuarios

- **Cliente.** Quiere saber que su pedido quedó confirmado apenas paga. Hoy espera hasta el día siguiente.
- **Operaciones.** Revisa los pedidos atascados a mano. Quiere que no haya ninguno.

## Metas y métricas

- **M1.** Confirmar el pedido apenas se captura el pago. Métrica: p95 del tiempo entre el pago y la confirmación. Línea base: sin dato. Objetivo: menos de un minuto, antes del 2026-12-31.
- **M2.** Ningún pedido confirmado sin su pago. Métrica: pedidos confirmados sin un pago capturado por el total exacto. Línea base: sin dato. Objetivo: cero, siempre.

No es una meta cobrar menos comisión ni cambiar de proveedor de pagos.

## Alcance

- **Dentro.** Alta y confirmación de pedidos en `acme-shop`, que habla con pagos solo por `PaymentsGateway`.
- **Fuera.** Reembolsos de pedidos confirmados, envíos y cambios de precio.
- **Supuestos.** El contexto de Pagos expone la captura de un pago por el total exacto.

## Requisitos

| ID | Requisito | Prioridad | Meta |
|----|-----------|-----------|------|
| RQ-01 | Un pedido nuevo tiene al menos una línea y cada cantidad es un entero positivo | debe | M2 |
| RQ-02 | Un pedido con un pago capturado por su total exacto queda confirmado sin esperar a nadie | debe | M1 |
| RQ-03 | Un pedido sin pago, o con un pago por otro monto, no se confirma | debe | M2 |
| RQ-04 | Operaciones ve por qué un pedido no se confirmó | podría | M1 |

## Criterios de aceptación

El detalle de cada escenario vive en el PRD de la feature que lo construye (`alta-de-pedidos.md`). A nivel producto, el recorrido que no puede fallar:

```gherkin
Escenario: un pedido pagado se confirma al instante [RQ-02]
  Dado un pedido con dos líneas y un pago capturado por el total exacto
  Cuando se procesa la confirmación
  Entonces el pedido queda confirmado
```

## Riesgos

| Riesgo | A quién le pega | Mitigación | Vigila |
|--------|-----------------|------------|--------|
| El sandbox del proveedor de pagos responde 200 aun con la tarjeta rechazada (R2) | Cliente y operaciones | Revisar `capture.ok` y no la respuesta HTTP | @ana |
| Los reembolsos de pedidos confirmados no tienen contrato con Pagos | Operaciones | Fuera de alcance hasta que Pagos publique el contrato | @ana |

## Preguntas abiertas

- ¿Cuál es el tiempo máximo aceptable entre el pago y la confirmación para el cliente? Opciones: un minuto (M1 como está) o cinco. Decide: @ana.
- ¿Operaciones necesita ver el motivo en una pantalla o basta con el ledger? Decide: @ana.

## Features

| PRD | Estado | Metas | Workstream |
|-----|--------|-------|------------|
| [alta-de-pedidos.md](alta-de-pedidos.md) | construido | M1, M2 | W-0001 |
