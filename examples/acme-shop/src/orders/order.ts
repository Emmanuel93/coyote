// Order aggregate: enforces the invariants in docs/domain.md.

export type OrderStatus = "draft" | "confirmed" | "shipped" | "cancelled";

export interface Line {
  sku: string;
  quantity: number;
  unitPriceCents: number;
}

export interface Order {
  id: string;
  lines: Line[];
  totalCents: number;
  status: OrderStatus;
}

export class DomainError extends Error {}

export function createOrder(id: string, lines: Line[]): Order {
  if (lines.length === 0) {
    throw new DomainError("an order needs at least one line");
  }
  for (const line of lines) {
    if (!Number.isInteger(line.quantity) || line.quantity < 1) {
      throw new DomainError(`invalid quantity for ${line.sku}`);
    }
    if (!Number.isInteger(line.unitPriceCents) || line.unitPriceCents < 0) {
      throw new DomainError(`invalid price for ${line.sku}`);
    }
  }
  return { id, lines: [...lines], totalCents: total(lines), status: "draft" };
}

export function total(lines: Line[]): number {
  return lines.reduce((sum, l) => sum + l.quantity * l.unitPriceCents, 0);
}

export function confirm(order: Order, capturedCents: number): Order {
  if (order.status !== "draft") {
    throw new DomainError(`cannot confirm an order in ${order.status}`);
  }
  if (capturedCents !== order.totalCents) {
    throw new DomainError("captured amount must equal the order total");
  }
  return { ...order, status: "confirmed" };
}

export function cancel(order: Order): Order {
  if (order.status !== "draft") {
    throw new DomainError("only draft orders are cancelled here; refunds belong to payments");
  }
  return { ...order, status: "cancelled" };
}
