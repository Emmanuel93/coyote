# Cierre W-0001 — Alta y confirmación de pedidos

Datos sintéticos. Responsable del resultado: @ana (modo manual, cuatro gates humanos).

| Step | Agente | Modelo | Tokens entrada/caché/salida | Costo USD |
|------|--------|--------|------------------------------|-----------|
| S1 dominio | coyote-analyst | sonnet-5 | 18.2k/12k/2.4k | 0.0388 |
| S2 contrato | coyote-architect | opus-5.5 | 24.6k/16k/3.1k | 0.1028 |
| S3 código y pruebas | coyote-dev | sonnet-5 | 61.3k/48.9k/7.8k | 0.1126 |
| S4 revisión | coyote-reviewer | haiku-4.5 | 30.4k/22.1k/1.6k | 0.0185 |
| **Total** | | 3 modelos | **134.5k/99k/14.9k** | **0.2727** (entrada 0.1007, salida 0.172) |

Presupuesto: 3.00 USD y 400k tokens; se usó el 9 % del costo y el 37 % de los tokens (149.4k de entrada más salida).

Resultado: `docs/domain.md`, `contracts/openapi.yaml`, `src/orders/`, `test/orders.test.ts` (5 pruebas en verde).
