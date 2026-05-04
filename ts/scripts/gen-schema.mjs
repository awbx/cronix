#!/usr/bin/env node
// Regenerate ../spec/manifest.schema.json from the Zod schema in
// @cronix/sdk. CI fails if running this produces a diff.

import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { z } from "zod";
import { manifestSchema } from "../packages/sdk/dist/index.js";

const here = dirname(fileURLToPath(import.meta.url));
const out = resolve(here, "..", "..", "spec", "manifest.schema.json");

const generated = z.toJSONSchema(manifestSchema, { target: "draft-2020-12" });

const wrapped = {
  $schema: "https://json-schema.org/draft/2020-12/schema",
  $id: "https://cronix.dev/schemas/manifest-v1.json",
  title: "Cronix Manifest v1",
  description:
    "Generated from the @cronix/sdk Zod schema by ts/scripts/gen-schema.mjs. Do not hand-edit. CI fails on drift.",
  ...generated,
};

writeFileSync(out, `${JSON.stringify(wrapped, null, 2)}\n`, "utf8");
console.log(`wrote ${out}`);
