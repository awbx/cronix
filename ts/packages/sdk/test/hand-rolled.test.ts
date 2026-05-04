import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { applyDefaults, parseManifest } from "../src/core/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const manifestPath = resolve(here, "../../../examples/hand-rolled/manifest.json");

describe("hand-rolled example", () => {
  it("parses and normalizes the example manifest", () => {
    const raw = JSON.parse(readFileSync(manifestPath, "utf8"));
    const parsed = parseManifest(raw);
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    const normalized = applyDefaults(parsed.value);
    expect(normalized.app).toBe("billing-service");
    expect(normalized.jobs.map((j) => j.name)).toEqual(["reconcile-payments", "settle-invoices"]);
    expect(normalized.jobs[0]?.policy.timeout_seconds).toBe(120);
    expect(normalized.jobs[1]?.schedules).toEqual(["0 2 * * *", "0 14 * * 1-5"]);
  });
});
