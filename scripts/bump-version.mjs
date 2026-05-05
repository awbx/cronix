// Atomic polyglot release flow:
//
//   pnpm version:bump 0.6.1
//   # or directly:
//   node scripts/bump-version.mjs 0.6.1
//
// Lives at the repo root because cronix is polyglot. The script
// orchestrates one bumper per language; each bumper knows what files
// to write for its language. Adding a new language (eg. Python) is a
// new file under scripts/bumpers/ and a one-line append to the
// `bumpers` array below.
//
// What this does, in order:
//
//   1. Validate the semver shape.
//   2. Run each bumper, collecting the paths each one wrote.
//   3. Regenerate CHANGELOG.md from `git log` since the previous tag.
//   4. Stage exactly the files we wrote (never `git add -A`, which
//      would sweep unrelated work into a release commit).
//   5. Commit `v<version>`, tag `v<version>`, push both to origin.
//
// The pushed tag triggers .github/workflows/release.yml.

import { execSync } from "node:child_process";
import { writeChangelog } from "./generate-changelog.mjs";
import * as tsBumper from "./bumpers/ts.mjs";
import * as goBumper from "./bumpers/go.mjs";
import * as pythonBumper from "./bumpers/python.mjs";

// Add a new bumper here when a new language joins the repo.
const bumpers = [tsBumper, goBumper, pythonBumper];

const version = process.argv[2];

if (!version || !/^\d+\.\d+\.\d+(?:-[\w.-]+)?$/.test(version)) {
  console.error("Usage: node scripts/bump-version.mjs <version>");
  console.error("Example: node scripts/bump-version.mjs 0.6.1");
  process.exit(1);
}

const repoRoot = new URL("..", import.meta.url).pathname;
const tsRoot = new URL("../ts/", import.meta.url).pathname;
const ctx = { repoRoot, tsRoot };

const touched = [];

for (const bumper of bumpers) {
  console.log(`\n[${bumper.name}]`);
  const { touched: paths } = bumper.bump(version, ctx);
  if (Array.isArray(paths)) touched.push(...paths);
}

console.log(`\n[changelog]`);
const changelogPaths = writeChangelog(version);
if (Array.isArray(changelogPaths)) touched.push(...changelogPaths);

if (touched.length === 0) {
  console.error("\nNothing to bump — no files were touched. Aborting.");
  process.exit(1);
}

// Stage the touched files only — no `git add -A`.
execSync(`git add -- ${touched.map((p) => JSON.stringify(p)).join(" ")}`, {
  cwd: repoRoot,
  stdio: "inherit",
});

execSync(`git commit -m "v${version}"`, { cwd: repoRoot, stdio: "inherit" });
execSync(`git tag v${version}`, { cwd: repoRoot, stdio: "inherit" });
execSync(`git push origin main v${version}`, { cwd: repoRoot, stdio: "inherit" });

console.log(`\nReleased v${version}`);
console.log(`  → release workflow runs on the tag push`);
