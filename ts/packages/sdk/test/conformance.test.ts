import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { applyDefaults, canonicalize, parseManifest } from "../src/core/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const vectorsPath = resolve(here, "../../../../spec/manifest-vectors.json");

type ValidVector = { name: string; valid: true; input: unknown; expected: string };
type InvalidVector = { name: string; valid: false; input: unknown; errorPaths: string[] };
type Vector = ValidVector | InvalidVector;

const file = JSON.parse(readFileSync(vectorsPath, "utf8")) as { vectors: Vector[] };

describe("manifest conformance vectors", () => {
  if (file.vectors.length === 0) {
    it.skip("(no vectors)", () => undefined);
    return;
  }

  for (const v of file.vectors) {
    if (v.valid) {
      it(`valid: ${v.name}`, () => {
        const parsed = parseManifest(v.input);
        expect(parsed.ok, parsed.ok ? "ok" : JSON.stringify(parsed.error)).toBe(true);
        if (!parsed.ok) return;
        const normalized = applyDefaults(parsed.value);
        const canonical = canonicalize(normalized);
        expect(canonical).toBe(v.expected);
      });
    } else {
      it(`invalid: ${v.name}`, () => {
        const parsed = parseManifest(v.input);
        expect(parsed.ok).toBe(false);
        if (parsed.ok) return;
        const reportedPaths = new Set(parsed.error.issues.map((i) => i.path.join("/")));
        for (const expectedPath of v.errorPaths) {
          const matchedPrefix = [...reportedPaths].some(
            (rp) => rp === expectedPath || rp.startsWith(`${expectedPath}/`),
          );
          expect(
            matchedPrefix,
            `expected an issue at path ${JSON.stringify(expectedPath)}; reported paths: ${JSON.stringify([...reportedPaths])}`,
          ).toBe(true);
        }
      });
    }
  }
});
