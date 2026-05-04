import type { CronEnv, CronInstance, VerifyHandleOptions } from "../core/types.js";

/**
 * Vercel adapter (Hono-shaped). Modern Vercel functions accept a Web `Request`
 * and return a `Response` directly, so the adapter is a one-line wrapper.
 *
 * ```ts
 * // app/api/cron/[[...slug]]/route.ts
 * import { handle } from "@awbx/cronix-sdk/vercel";
 * import { cron } from "@/lib/cron";
 *
 * export const GET = handle(cron);
 * export const POST = handle(cron);
 * ```
 *
 * Or with per-fire variables derived from the request:
 *
 * ```ts
 * export const POST = handle(cron, (req) => ({
 *   vars: { traceId: req.headers.get("x-trace-id") ?? crypto.randomUUID() },
 * }));
 * ```
 *
 * Source: mirrors `hono/vercel`'s `handle` adapter, which is itself a
 * pass-through to `app.fetch` because Vercel's runtime already supplies a
 * Web Request.
 */
export function handle<E extends CronEnv>(
  cron: CronInstance<E>,
  varsFromRequest?: (req: Request) => VerifyHandleOptions<E>,
): (req: Request) => Promise<Response> {
  if (varsFromRequest === undefined) {
    return (req) => cron.handle(req);
  }
  return (req) => cron.handle(req, varsFromRequest(req));
}
