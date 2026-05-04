# Express example

Wires `@cronix/sdk` into Express. The SDK has no Express-specific code; this is just two route handlers (~30 lines of glue) calling `cron.verify()`.

```bash
pnpm install
pnpm --filter @cronix-example/express-app dev
# CRON_SECRET_V2=... CRON_SECRET_V1=... PUBLIC_URL=https://billing.example.com pnpm start
```

The manifest will be served at `GET /.well-known/cron-manifest` (signed) and triggers fire at `POST /api/v1/scheduled/<name>` (signed).
