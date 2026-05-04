import type { JobContext } from "@awbx/cronix-sdk";

// Cron handlers live in their own module. The example wires them via
// `cron.on(name, handler)` from index.ts.

export async function reconcilePayments(ctx: JobContext) {
  console.log(`[cron] ${ctx.name} run=${ctx.runId} attempt=${ctx.attempt}`);
  return { ok: true, status: 202 };
}

export async function settleInvoices(ctx: JobContext) {
  console.log(`[cron] ${ctx.name} run=${ctx.runId}`);
  return { ok: true };
}
