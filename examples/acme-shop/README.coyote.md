---
coyote: 1
repo: acme-shop
type: backend
stack: [typescript, node]
owners: ["@ana"]
standards: { profile: backend }
---
# acme-shop
purpose|API de pedidos de una tienda ficticia; proyecto sintético para probar coyote
run|npm test
test|npm test
entry|src/orders/service.ts|servicio de aplicación del contexto de pedidos
mod|orders|agregado Pedido e invariantes del dominio|src/orders|createOrder, confirm, OrdersService
mod|payments|interfaz hacia el contexto de pagos|src/payments|PaymentsGateway
docs|docs/domain.md|modelo de dominio, estados e invariantes
docs|contracts/openapi.yaml|contrato HTTP del contexto de pedidos
dep|payments|captura de pagos por PaymentsGateway
