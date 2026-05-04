#!/usr/bin/env node
// Generate spec/auth-vectors.json. Uses the @awbx/cronix-sdk reference
// implementation to seed signatures for happy-path "verify" cases, then
// hand-authors the failure-mode vectors with deliberately broken inputs.
//
// Run: pnpm gen:auth-vectors
//
// The Go implementation in internal/auth must agree with every vector this
// script emits. CI fails on drift between this script's output and the
// committed file.

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { sign } from "../packages/sdk/dist/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const out = resolve(here, "..", "..", "spec", "auth-vectors.json");

const SECRET_A = "whsec_test_primary_aaaaaaaaaaaaaaaaaaaaaaaaaaa";
const SECRET_B = "whsec_test_rotated_bbbbbbbbbbbbbbbbbbbbbbbbbbb";
const SECRET_C = "whsec_test_unrelated_ccccccccccccccccccccccccc";

const TS_BASE = 1_730_000_000;

const enc = (s) => new TextEncoder().encode(s);
const fromHex = (h) => Uint8Array.from(h.match(/.{1,2}/g).map((b) => Number.parseInt(b, 16)));

const happy = [];

const addSign = (name, opts) => {
  happy.push({ name, opts });
};

addSign("post-empty-body", {
  secret: SECRET_A,
  method: "POST",
  path: "/.well-known/cron-manifest",
  body: enc(""),
  timestamp: TS_BASE,
});

addSign("get-no-body", {
  secret: SECRET_A,
  method: "GET",
  path: "/.well-known/cron-manifest",
  body: enc(""),
  timestamp: TS_BASE + 1,
});

addSign("post-json-body", {
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/reconcile-payments",
  body: enc('{"runId":"abc","attempt":1}'),
  timestamp: TS_BASE + 2,
});

addSign("post-utf8-emoji-body", {
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/celebrate",
  body: enc("hello 🌍 world — café"),
  timestamp: TS_BASE + 3,
});

addSign("post-large-body", {
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/big",
  body: new Uint8Array(1024 * 1024).fill(65), // 1 MiB of 'A'
  timestamp: TS_BASE + 4,
});

addSign("body-with-embedded-nulls", {
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/binary",
  body: new Uint8Array([0, 1, 2, 0, 3, 0, 0, 4]),
  timestamp: TS_BASE + 5,
});

addSign("path-with-percent-encoding", {
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/with%20space",
  body: enc(""),
  timestamp: TS_BASE + 6,
});

addSign("uppercased-method-via-lowercase-input", {
  secret: SECRET_A,
  method: "post", // gets uppercased in canonicalization
  path: "/api/v1/scheduled/foo",
  body: enc("x"),
  timestamp: TS_BASE + 7,
});

addSign("rotated-second-secret-matches", {
  secret: SECRET_B, // signature made with second secret
  method: "POST",
  path: "/api/v1/scheduled/foo",
  body: enc("rot"),
  timestamp: TS_BASE + 8,
});

const vectors = [];

const toB64 = (u8) => Buffer.from(u8).toString("base64");

for (const { name, opts } of happy) {
  // eslint-disable-next-line no-await-in-loop
  const result = await sign(opts);
  vectors.push({
    name: `verify-ok/${name}`,
    kind: "verify",
    secrets: name === "rotated-second-secret-matches" ? [SECRET_A, SECRET_B] : [SECRET_A],
    method: opts.method,
    path: opts.path,
    bodyB64: toB64(opts.body),
    header: result.header,
    now: result.timestamp,
    maxSkewSeconds: 300,
    expect: "ok",
    expectedSecretIndex: name === "rotated-second-secret-matches" ? 1 : 0,
  });
  vectors.push({
    name: `sign-emits/${name}`,
    kind: "sign",
    secret: opts.secret,
    method: opts.method,
    path: opts.path,
    bodyB64: toB64(opts.body),
    timestamp: opts.timestamp,
    expectedHeader: result.header,
  });
}

// Failure-mode and edge-case verify vectors.

const ref = await sign({
  secret: SECRET_A,
  method: "POST",
  path: "/api/v1/scheduled/foo",
  body: enc("ref"),
  timestamp: TS_BASE,
});
const refMatch = /v1=([0-9a-f]{64})/.exec(ref.header);
if (!refMatch) throw new Error("could not extract v1 hex");
const refHex = refMatch[1];
const flippedHex =
  refHex.slice(0, -1) + (refHex.slice(-1) === "0" ? "1" : "0"); // mutate one nybble

vectors.push({
  name: "verify-fail/empty-header",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: "",
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/missing-t",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `v1=${refHex}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/missing-v1",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `t=${TS_BASE}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/wrong-algorithm-tag",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: `t=${TS_BASE},v2=${refHex}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/v1-wrong-length",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `t=${TS_BASE},v1=${refHex.slice(0, 32)}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/v1-not-hex",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `t=${TS_BASE},v1=${"z".repeat(64)}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/segment-no-equals",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `t${TS_BASE},v1=${refHex}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/timestamp-not-integer",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/x",
  bodyB64: "",
  header: `t=abc,v1=${refHex}`,
  now: TS_BASE,
  expect: "MalformedHeader",
});

vectors.push({
  name: "verify-fail/stale-timestamp-past",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE + 301, // 1s past the 300s default
  maxSkewSeconds: 300,
  expect: "StaleTimestamp",
});

vectors.push({
  name: "verify-fail/stale-timestamp-future",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE - 301,
  maxSkewSeconds: 300,
  expect: "StaleTimestamp",
});

vectors.push({
  name: "verify-fail/skew-respects-custom-window",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE + 31,
  maxSkewSeconds: 30,
  expect: "StaleTimestamp",
});

vectors.push({
  name: "verify-fail/tampered-signature",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: `t=${TS_BASE},v1=${flippedHex}`,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

vectors.push({
  name: "verify-fail/tampered-body",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("REF")), // body was "ref" when signed
  header: ref.header,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

vectors.push({
  name: "verify-fail/tampered-method",
  kind: "verify",
  secrets: [SECRET_A],
  method: "GET", // signed with POST
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

vectors.push({
  name: "verify-fail/tampered-path",
  kind: "verify",
  secrets: [SECRET_A],
  method: "POST",
  path: "/api/v1/scheduled/bar", // signed for /foo
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

vectors.push({
  name: "verify-fail/wrong-secret",
  kind: "verify",
  secrets: [SECRET_C],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

vectors.push({
  name: "verify-fail/no-acceptable-secret-among-many",
  kind: "verify",
  secrets: [SECRET_B, SECRET_C],
  method: "POST",
  path: "/api/v1/scheduled/foo",
  bodyB64: toB64(enc("ref")),
  header: ref.header,
  now: TS_BASE,
  expect: "SignatureMismatch",
});

const wrapped = {
  $comment:
    "Conformance vectors for cronix HMAC-SHA256 signing and verification. Every implementation (TS @awbx/cronix-sdk, Go internal/auth, future SDKs) must agree on every vector. For 'sign' vectors, sign(opts) must produce expectedHeader byte-for-byte. For 'verify' vectors with expect='ok', verify(opts) must succeed and report expectedSecretIndex. For 'verify' vectors with a failure code, verify(opts) must fail with that error category. Bodies are base64-encoded so binary cases (NULs, non-UTF-8) are representable safely. Generated by ts/scripts/gen-auth-vectors.mjs — do not hand-edit.",
  version: 1,
  vectors,
};

writeFileSync(out, `${JSON.stringify(wrapped, null, 2)}\n`, "utf8");
console.log(`wrote ${out} (${vectors.length} vectors)`);
