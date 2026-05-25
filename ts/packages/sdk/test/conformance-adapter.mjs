#!/usr/bin/env node
// cronix conformance adapter for @awbx/cronix-sdk.
//
// Implements the three-subcommand stdin/stdout contract documented in
// spec/conformance/README.md so the language-neutral runner
// (go/cmd/conformance) can drive this SDK and verify it agrees with
// every conformance vector byte-for-byte.
//
// Usage (from the repo root):
//
//   go run ./go/cmd/conformance \
//     --vectors spec \
//     --adapter "node ts/packages/sdk/test/conformance-adapter.mjs"
//
// Build the SDK first if dist/ is stale: `pnpm -C ts/packages/sdk build`.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const distEntry = resolve(here, "../dist/index.js");

// Import from the built output rather than ts source so this adapter
// runs without ts-node. The CI step builds the SDK before invoking.
const { applyDefaults, canonicalize, parseManifest, sign, verify } = await import(distEntry);

const subcommand = process.argv[2];

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString("utf8");
}

const fromB64 = (s) => (s === "" ? new Uint8Array(0) : Uint8Array.from(Buffer.from(s, "base64")));

function fail(msg) {
  process.stderr.write(`adapter error: ${msg}\n`);
  process.exit(2);
}

async function main() {
  const stdin = await readStdin();
  switch (subcommand) {
    case "manifest-canonicalize": {
      const input = JSON.parse(stdin);
      const parsed = parseManifest(input);
      if (!parsed.ok) {
        // Per the contract, surface the JSON paths where validation
        // failed. The TS SDK's issue.path is already an array of
        // segments — join with "/" to match the vector format.
        const paths = parsed.error.issues.map((i) => i.path.join("/"));
        process.stdout.write(JSON.stringify({ error: { paths } }));
        return;
      }
      const normalized = applyDefaults(parsed.value);
      const canonical = canonicalize(normalized);
      process.stdout.write(canonical);
      return;
    }
    case "auth-sign": {
      const opts = JSON.parse(stdin);
      const result = await sign({
        secret: opts.secret,
        method: opts.method,
        path: opts.path,
        body: fromB64(opts.bodyB64),
        timestamp: opts.timestamp,
      });
      process.stdout.write(result.header);
      return;
    }
    case "auth-verify": {
      const opts = JSON.parse(stdin);
      const verifyOpts = {
        secrets: opts.secrets,
        method: opts.method,
        path: opts.path,
        body: fromB64(opts.bodyB64),
        header: opts.header,
        now: opts.now,
        ...(opts.maxSkewSeconds !== undefined ? { maxSkewSeconds: opts.maxSkewSeconds } : {}),
      };
      const result = await verify(verifyOpts);
      if (result.ok) {
        process.stdout.write(JSON.stringify({ ok: true, secret_index: result.value.secretIndex }));
      } else {
        process.stdout.write(JSON.stringify({ ok: false, error: result.error.code }));
      }
      return;
    }
    default:
      fail(`unknown subcommand ${JSON.stringify(subcommand)}; expected one of manifest-canonicalize, auth-sign, auth-verify`);
  }
}

main().catch((err) => fail(err.stack ?? err.message ?? String(err)));
