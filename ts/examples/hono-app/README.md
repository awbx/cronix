# Hono example

Wires `@awbx/cronix-sdk` into Hono. Runs unchanged on Node, Bun, and Cloudflare Workers (export `default app`; remove the `serve()` call when deploying to Workers).

```bash
pnpm install
pnpm --filter @cronix-example/hono-app dev    # Node via tsx
pnpm --filter @cronix-example/hono-app bun    # Bun
```
