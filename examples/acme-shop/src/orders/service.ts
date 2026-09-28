// Application service for the orders context (contracts/openapi.yaml).

import { confirm, createOrder, DomainError, type Line, type Order } from "./order.ts";
import type { PaymentsGateway } from "../payments/gateway.ts";

export class PaymentNotCaptured extends Error {}

export class OrdersService {
  private readonly orders = new Map<string, Order>();
  private readonly payments: PaymentsGateway;
  private seq = 0;

  constructor(payments: PaymentsGateway) {
    this.payments = payments;
  }

  create(lines: Line[]): Order {
    this.seq += 1;
    const order = createOrder(`ord-${this.seq}`, lines);
    this.orders.set(order.id, order);
    return order;
  }

  async confirm(id: string): Promise<Order> {
    const order = this.orders.get(id);
    if (!order) {
      throw new DomainError(`order ${id} not found`);
    }
    const capture = await this.payments.capture(order.id, order.totalCents);
    if (!capture.ok) {
      throw new PaymentNotCaptured(`payment for ${id} was not captured`);
    }
    const confirmed = confirm(order, capture.capturedCents);
    this.orders.set(id, confirmed);
    return confirmed;
  }
}
