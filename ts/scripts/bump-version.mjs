// Atomic release flow:
//
//   pnpm version:bump 0.6.1
//
// What this does, in order:
//
//   1. Validate the semver shape.
//   2. Bump every workspace package's package.json to the new version
//      (5 packages today: sdk + 4 adapters). Discovery via `pnpm -r list
//      --json`, so adding a sibling package later is automatic.
//   3. Regenerate CHANGELOG.md from `git log` since the previous tag.
//   4. Stage exactly the files we wrote (never `git add -A`, which
//      would sweep unrelated work — eg. an in-progress example edit —
//      into the release commit).
//   5. Commit `v<version>`, tag `v<version>`, push both to origin.
//
// The pushed tag triggers .github/workflows/release.yml which runs both
// goreleaser (Go binaries / packages / Docker images) and the npm
// publish job (the 5 TS packages, in dependency order).

import { execSync } from "node:child_process";
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { writeChangelog } from "./generate-changelog.mjs";

const version = process.argv[2];

if (!version || !/^\d+\.\d+\.\d+(?:-[\w.-]+)?$/.test(version)) {
  console.error("Usage: pnpm version:bump <version>");
  console.error("Example: pnpm version:bump 0.6.1");
  process.exit(1);
}

const repoRoot = new URL("../..", import.meta.url).pathname;
const tsRoot = new URL("..", import.meta.url).pathname;

// Track every file the script writes so we can stage them explicitly.
const touched = [];

// Discover every workspace package (public + private) under ts/.
const list = execSync("pnpm -r list --json --depth -1", {
  cwd: tsRoot,
  encoding: "utf-8",
});
const packages = JSON.parse(list);

for (const pkg of packages) {
  const pkgPath = join(pkg.path, "package.json");
  const json = JSON.parse(readFileSync(pkgPath, "utf-8"));
  // Skip the top-level `cronix` aggregator package — it stays at 0.0.0
  // because nothing publishes it. Bumping it would imply a release tag
  // that doesn't actually exist on npm.
  if (json.private && json.name === "cronix") continue;
  json.version = version;
  writeFileSync(pkgPath, `${JSON.stringify(json, null, 2)}\n`);
  touched.push(pkgPath);
  console.log(`  ${json.name} -> ${version}`);
}

// Regenerate the changelog with the pending tag included.
const changelogPaths = writeChangelog(version);
if (Array.isArray(changelogPaths)) touched.push(...changelogPaths);

// Stage the touched files only — no `git add -A`.
execSync(`git add -- ${touched.map((p) => JSON.stringify(p)).join(" ")}`, {
  cwd: repoRoot,
  stdio: "inherit",
});

execSync(`git commit -m "v${version}"`, {
  cwd: repoRoot,
  stdio: "inherit",
});
execSync(`git tag v${version}`, {
  cwd: repoRoot,
  stdio: "inherit",
});
execSync(`git push origin main v${version}`, {
  cwd: repoRoot,
  stdio: "inherit",
});

console.log(`\nReleased v${version}`);
console.log(`  → goreleaser + npm publish workflow runs on the tag push`);
