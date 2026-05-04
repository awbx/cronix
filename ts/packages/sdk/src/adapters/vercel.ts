export type FetchHandler = (req: Request) => Response | Promise<Response>;

/**
 * Lift a Web Fetch handler to a Vercel route handler. Modern Vercel
 * functions accept a Web `Request` and return a `Response`, so this is
 * essentially identity — the adapter exists for symmetry with the other
 * framework adapters and as a discoverable entry point.
 *
 * ```ts
 * // app/api/cron/[[...slug]]/route.ts
 * import { handle } from "@awbx/cronix-sdk/vercel";
 * import { cron } from "@/lib/cron";
 *
 * export const POST = handle((req) => cron.handle(req));
 * export const GET = handle((req) => cron.handle(req));
 * ```
 *
 * Mirrors the shape of `hono/vercel`'s `handle`, which is also a
 * pass-through because Vercel's runtime already supplies a Web Request.
 */
export function handle(fn: FetchHandler): (req: Request) => Promise<Response> {
  return async (req) => fn(req);
}
