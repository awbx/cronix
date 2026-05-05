// TS workspace bumper: walks every workspace package under ts/packages
// (and ts/examples — `pnpm -r list` returns both) and writes the new
// version into each package.json. Returns the list of touched paths so
// the orchestrator can stage exactly those files.
//
// Skips: the top-level `cronix` aggregator package — it stays at 0.0.0
// because nothing publishes it.

import { execSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

export const name = "ts";

export function bump(version, { tsRoot }) {
  const list = execSync("pnpm -r list --json --depth -1", {
    cwd: tsRoot,
    encoding: "utf-8",
  });
  const packages = JSON.parse(list);
  const touched = [];

  for (const pkg of packages) {
    const pkgPath = join(pkg.path, "package.json");
    const json = JSON.parse(readFileSync(pkgPath, "utf-8"));
    if (json.private && json.name === "cronix") continue;
    json.version = version;
    writeFileSync(pkgPath, `${JSON.stringify(json, null, 2)}\n`);
    touched.push(pkgPath);
    console.log(`  ${json.name} -> ${version}`);
  }

  return { touched };
}
