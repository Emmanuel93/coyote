# PRD: Alta y confirmación de pedidos

Estado: construido
Responsable: @ana
Fecha: 2026-09-28
Parte de: coyote/prd/producto.md. Metas que mueve: M1, M2
Workstreams: W-0001

Datos sintéticos. Es el PRD con el que partió el workstream W-0001.

## Problema

Un pedido pagado no se confirma hasta la conciliación nocturna (M1), y nada impide hoy confirmar uno sin pago (M2). Evidencia: `docs/domain.md`, invariante 3.

## Usuarios

- **Cliente.** Paga y espera la confirmación en la misma pantalla.
- **Operaciones.** Revisa pedidos atascados; quiere que el caso sea la excepción.

## Metas y métricas

Las de `producto.md`, sin cambios:

- **M1.** p95 de pago a confirmación menor a un minuto.
- **M2.** Cero pedidos confirmados sin un pago capturado por el total exacto.

## Alcance

- **Dentro.** El agregado Pedido, su servicio de confirmación y su contrato HTTP.
- **Fuera.** Reembolsos, envíos y el motivo visible para operaciones (RQ-04 del producto, para otra feature).
- **Supuestos.** Pagos captura por el total exacto, a través de `PaymentsGateway`.

## Requisitos

| ID | Requisito | Prioridad | Meta |
|----|-----------|-----------|------|
| RQ-01 | Un pedido tiene al menos una línea y cada cantidad es un entero positivo | debe | M2 |
| RQ-02 | El total es la suma de precio por cantidad de sus líneas, en centavos enteros | debe | M2 |
| RQ-03 | Un pedido con un pago capturado por su total exacto queda confirmado | debe | M1 |
| RQ-04 | Un pedido sin pago, o con un pago por otro monto, no se confirma | debe | M2 |

## Criterios de aceptación

```gherkin
Escenario: un pedido pagado se confirma [RQ-03]
  Dado un pedido con dos líneas y un pago capturado por el total exacto
  Cuando se procesa la confirmación
  Entonces el pedido queda confirmado
  Y se publica el evento PedidoConfirmado

Escenario: un pago por otro monto no confirma [RQ-04]
  Dado un pedido de 2500 centavos y un pago capturado por 2000
  Cuando se procesa la confirmación
  Entonces el pedido sigue en borrador
  Y se informa que el monto no coincide

Escenario: un pedido sin líneas no se crea [RQ-01]
  Dado un pedido sin líneas
  Cuando se intenta crear
  Entonces se rechaza con el error "pedido sin líneas"

Escenario: el total no usa decimales [RQ-02]
  Dado una línea de 3 unidades a 333 centavos
  Cuando se calcula el total
  Entonces el total es 999 centavos
```

## Riesgos

| Riesgo | A quién le pega | Mitigación | Vigila |
|--------|-----------------|------------|--------|
| El sandbox responde 200 aun con la tarjeta rechazada (R2) | Cliente | La confirmación revisa `capture.ok` | @ana |

## Preguntas abiertas

- Ninguna. Las de producto están en `producto.md`.
