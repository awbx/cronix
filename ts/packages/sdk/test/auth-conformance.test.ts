import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { sign, verify } from "../src/core/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const vectorsPath = resolve(here, "../../../../spec/auth-vectors.json");

type SignVector = {
  name: string;
  kind: "sign";
  secret: string;
  method: string;
  path: string;
  bodyB64: string;
  timestamp: number;
  expectedHeader: string;
};

type VerifyVector = {
  name: string;
  kind: "verify";
  secrets: string[];
  method: string;
  path: string;
  bodyB64: string;
  header: string;
  now: number;
  maxSkewSeconds?: number;
  expect: "ok" | "MalformedHeader" | "StaleTimestamp" | "SignatureMismatch";
  expectedSecretIndex?: number;
};

type Vector = SignVector | VerifyVector;

const file = JSON.parse(readFileSync(vectorsPath, "utf8")) as { vectors: Vector[] };

const fromB64 = (s: string): Uint8Array => (s === "" ? new Uint8Array(0) : Uint8Array.from(Buffer.from(s, "base64")));

describe("auth conformance vectors", () => {
  if (file.vectors.length === 0) {
    it.skip("(no vectors)", () => undefined);
    return;
  }

  for (const v of file.vectors) {
    if (v.kind === "sign") {
      it(`${v.name}`, async () => {
        const result = await sign({
          secret: v.secret,
          method: v.method,
          path: v.path,
          body: fromB64(v.bodyB64),
          timestamp: v.timestamp,
        });
        expect(result.header).toBe(v.expectedHeader);
        expect(result.timestamp).toBe(v.timestamp);
      });
    } else {
      it(`${v.name}`, async () => {
        const opts = {
          secrets: v.secrets,
          method: v.method,
          path: v.path,
          body: fromB64(v.bodyB64),
          header: v.header,
          now: v.now,
          ...(v.maxSkewSeconds !== undefined ? { maxSkewSeconds: v.maxSkewSeconds } : {}),
        };
        const result = await verify(opts);
        if (v.expect === "ok") {
          expect(result.ok, !result.ok ? JSON.stringify(result.error) : "ok").toBe(true);
          if (result.ok && v.expectedSecretIndex !== undefined) {
            expect(result.value.secretIndex).toBe(v.expectedSecretIndex);
          }
        } else {
          expect(result.ok).toBe(false);
          if (!result.ok) {
            expect(result.error.code).toBe(v.expect);
          }
        }
      });
    }
  }
});
