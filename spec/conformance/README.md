# cronix conformance runner

A language-neutral test harness that runs `spec/manifest-vectors.json` and `spec/auth-vectors.json` against an **SDK adapter** — a small CLI any cronix SDK ships so the runner can drive its library API through `stdin`/`stdout`.

This directory is what makes the on-the-wire contract portable. Any future SDK (Rust, Python, Java, …) ships a conformance adapter and runs:

```sh
go run github.com/awbx/cronix/go/cmd/conformance \
  --vectors spec \
  --adapter "<adapter-command>"
```

If every vector passes, the SDK is byte-for-byte cronix-compliant.

## The adapter contract

An adapter is any command that responds to three subcommands. It reads a JSON object from stdin, writes a JSON object (or raw text where noted) to stdout, and exits 0 unconditionally — failures are reported via the JSON payload, not via exit code. **One adapter binary, three subcommands.**

### `<adapter> manifest-canonicalize`

Reads a manifest input. Produces the canonical normalized JSON form OR an error report.

| Stream | Shape |
|---|---|
| stdin | A manifest input object (matches `spec/manifest.schema.json` for valid cases; arbitrary JSON for invalid cases) |
| stdout (valid) | The canonical normalized JSON string, with default policy fields filled in, jobs sorted, headers sorted, etc. — **byte-for-byte matching the vector's `expected` field** |
| stdout (invalid) | `{"error":{"paths":["a","b"]}}` listing JSON paths where validation failed |

### `<adapter> auth-sign`

Reads sign options. Produces the canonical HMAC-SHA256 header value.

| Stream | Shape |
|---|---|
| stdin | `{"secret":"<webhook-secret>","method":"GET","path":"/api/x","bodyB64":"<base64>","timestamp":1730000000}` |
| stdout | The header string (raw text, no trailing newline). Format: `t=<unix>,v1=<hex-sha256>` |

### `<adapter> auth-verify`

Reads verify options. Reports whether the header is valid against the given secrets.

| Stream | Shape |
|---|---|
| stdin | `{"secrets":["s1","s2"],"method":"GET","path":"/api/x","bodyB64":"<base64>","header":"<header>","now":1730000000,"maxSkewSeconds":300}` |
| stdout success | `{"ok":true,"secret_index":0}` — the index in `secrets` that produced a valid signature |
| stdout failure | `{"ok":false,"error":"<error-code>"}` — error codes per `spec/auth-vectors.json` (`"MalformedHeader"`, `"StaleTimestamp"`, `"SignatureMismatch"`) |

## Running the runner

```sh
# Against the bundled TypeScript adapter
go run ./go/cmd/conformance \
  --vectors spec \
  --adapter "node ts/packages/sdk/test/conformance-adapter.mjs"

# Against your own SDK adapter
go run ./go/cmd/conformance \
  --vectors spec \
  --adapter "./my-rust-adapter"
```

The runner prints per-vector pass/fail and exits non-zero if any vector fails.

## Why a Go runner

The runner has no SDK dependency — it shells out to adapters. It needs JSON parsing, subprocess management, and a CLI. Go provides all three with zero ceremony and is already in the cronix toolchain. **Adapter authors only need their own language's stdlib** to write the adapter; the runner runs everywhere Go runs.

## Adding an adapter for a new SDK

1. Implement the three subcommands above. The simplest adapter is ~50 lines of code calling your SDK's library API.
2. Run `go run ./go/cmd/conformance --adapter "<your-cmd>"` against the bundled vectors.
3. When green, link your adapter from this README under `## Conformant adapters`.
4. (Optional) Open a PR adding your adapter as a CI matrix entry so cronix's CI catches future regressions in your SDK.

## Conformant adapters

- TypeScript (`@awbx/cronix-sdk`) — [`ts/packages/sdk/test/conformance-adapter.mjs`](../../ts/packages/sdk/test/conformance-adapter.mjs)
- Go (`pkg/cronsdk`) — *pending, tracked in a follow-up issue (see ROADMAP.md)*
