import { test } from "node:test";
import assert from "node:assert/strict";

import { cancel, confirm, createOrder, DomainError } from "../src/orders/order.ts";
import { OrdersService, PaymentNotCaptured } from "../src/orders/service.ts";
import type { PaymentsGateway } from "../src/payments/gateway.ts";

const lines = [
  { sku: "SKU-1", quantity: 2, unitPriceCents: 1250 },
  { sku: "SKU-2", quantity: 1, unitPriceCents: 499 },
];

function gateway(ok: boolean, delta = 0): PaymentsGateway {
  return {
    capture: async (_id, amountCents) => ({ ok, capturedCents: amountCents + delta, reference: "cap-1" }),
  };
}

test("total is computed in cents", () => {
  assert.equal(createOrder("o1", lines).totalCents, 2999);
});

test("an order needs at least one line and positive quantities", () => {
  assert.throws(() => createOrder("o1", []), DomainError);
  assert.throws(() => createOrder("o1", [{ sku: "SKU-1", quantity: 0, unitPriceCents: 10 }]), DomainError);
});

test("confirmation requires the exact captured total", () => {
  const order = createOrder("o1", lines);
  assert.throws(() => confirm(order, 2998), DomainError);
  assert.equal(confirm(order, 2999).status, "confirmed");
});

test("confirmed orders are not cancelled here", () => {
  const confirmed = confirm(createOrder("o1", lines), 2999);
  assert.throws(() => cancel(confirmed), DomainError);
});

test("service confirms only with a captured payment", async () => {
  const ok = new OrdersService(gateway(true));
  const order = ok.create(lines);
  assert.equal((await ok.confirm(order.id)).status, "confirmed");

  const declined = new OrdersService(gateway(false));
  const other = declined.create(lines);
  await assert.rejects(declined.confirm(other.id), PaymentNotCaptured);

  const short = new OrdersService(gateway(true, -1));
  const third = short.create(lines);
  await assert.rejects(short.confirm(third.id), DomainError);
});
