# Dominio de pedidos — acme-shop

Proyecto sintético: la tienda, las personas y los datos son ficticios.

El trabajo empieza aquí (R16): primero el modelo y sus invariantes, luego el contrato (`contracts/openapi.yaml`) y al final el código.

## Contexto delimitado: Pedidos

| Concepto | Qué es |
|----------|--------|
| SKU | unidad vendible con precio en centavos; un producto agrupa SKUs |
| Pedido | líneas de SKU con cantidad, un total y un estado |
| Pago | captura del total en la pasarela; lo dueño es el contexto Pagos |

## Estados del pedido

```
borrador ──confirmar (pago capturado)──▶ confirmado ──enviar──▶ enviado
    │
    └──cancelar──▶ cancelado
```

## Invariantes

1. Un pedido tiene al menos una línea y cada cantidad es un entero positivo.
2. El total es la suma de precio × cantidad de sus líneas, en centavos; nunca se recalcula con decimales.
3. Un pedido solo pasa a `confirmado` con un pago capturado por el total exacto.
4. Un pedido confirmado no se cancela desde este contexto: se reembolsa desde Pagos.

## Integraciones

- Pagos se consume por la interfaz `PaymentsGateway`; ningún módulo llama directo al proveedor (regla P1 del proyecto).
