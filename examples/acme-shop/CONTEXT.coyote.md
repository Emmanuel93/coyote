---
coyote: 1
repo: acme-shop
updated: 2026-09-28
---
# CONTEXT.coyote.md
how|coyote|registra lo aprendido con coyote note y valida con coyote doctor|-
inv|orders|un pedido se confirma solo con un pago capturado por el total exacto|docs/domain.md
inv|orders|los montos van en centavos enteros; nunca se calcula con decimales|docs/domain.md
dec|payments|pedidos habla con pagos solo por PaymentsGateway|coyote/standards/rules.yaml
gap|payments|el sandbox del proveedor de pagos responde 200 aun con tarjeta rechazada; revisa capture.ok|src/orders/service.ts
term|dominio|SKU es la unidad vendible; un producto agrupa SKUs|docs/domain.md
risk|payments|los reembolsos de pedidos confirmados aún no tienen contrato con el contexto de pagos|docs/domain.md
