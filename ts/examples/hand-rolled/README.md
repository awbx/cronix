# hand-rolled example

A static `manifest.json` and nothing else. Demonstrates that any HTTP server can serve a cronix manifest — no SDK required. To use:

1. Place this file (`manifest.json`) at `/.well-known/cron-manifest` on your server.
2. Sign manifest fetches with HMAC-SHA256 (Phase 2 — see `spec/RFC.md` § Authentication).
3. Run `cronix apply --manifest=https://your-app/.well-known/cron-manifest`.

The manifest will round-trip through the conformance suite at `spec/manifest-vectors.json`.
