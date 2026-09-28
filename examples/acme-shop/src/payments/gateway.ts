// The only way the orders context talks to payments (project rule P1).

export interface Capture {
  ok: boolean;
  capturedCents: number;
  reference: string;
}

export interface PaymentsGateway {
  capture(orderId: string, amountCents: number): Promise<Capture>;
}
