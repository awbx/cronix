// Generate CHANGELOG.md from git tags and commit messages. Categorizes
// each commit by Conventional Commits prefix (feat:, fix:, refactor:,
// chore:, docs:) and groups them under the version they shipped in.
//
// Standalone:
//   node ts/scripts/generate-changelog.mjs            # print to stdout
//   node ts/scripts/generate-changelog.mjs --write    # write CHANGELOG.md
//   node ts/scripts/generate-changelog.mjs 0.7.0 --write   # include pending v0.7.0 from HEAD
//
// Imported by ts/scripts/bump-version.mjs.

import { execSync } from "node:child_process";
import { writeFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = new URL("../..", import.meta.url).pathname;
const changelogPath = join(repoRoot, "CHANGELOG.md");

/** Sorted descending: newest tag first. Filter to vX.Y.Z form. */
function getTags() {
  const out = execSync("git tag --sort=-version:refname", { encoding: "utf-8" }).trim();
  return out ? out.split("\n").filter((t) => /^v\d+\.\d+\.\d+/.test(t)) : [];
}

function getTagDate(tag) {
  return execSync(`git log -1 --format=%ai ${tag}`, { encoding: "utf-8" }).trim().slice(0, 10);
}

/** Commits in [from, to], excluding version-bump commits and merge commits. */
function getCommits(from, to) {
  const range = from ? `${from}..${to}` : to;
  const out = execSync(`git log --oneline --no-merges ${range}`, { encoding: "utf-8" }).trim();
  if (!out) return [];
  return out
    .split("\n")
    .map((line) => ({ hash: line.slice(0, 7), message: line.slice(8) }))
    .filter((c) => !/^v\d+\.\d+\.\d+/.test(c.message));
}

/**
 * Categorize a Conventional Commits message. Falls back to keyword
 * heuristics for non-conventional history (commits made before the
 * convention took hold).
 */
function categorize(message) {
  const lower = message.toLowerCase();
  if (/^feat(\(|:|!)/.test(lower)) return "Features";
  if (/^fix(\(|:|!)/.test(lower)) return "Bug Fixes";
  if (/^refactor(\(|:|!)/.test(lower)) return "Refactors";
  if (/^perf(\(|:|!)/.test(lower)) return "Performance";
  if (/^docs(\(|:|!)/.test(lower)) return "Docs";
  if (/^test(\(|:|!)/.test(lower)) return "Tests";
  if (/^chore(\(|:|!)/.test(lower)) return "Chores";
  // Heuristic fallback for older commits.
  if (lower.startsWith("add") || lower.startsWith("implement")) return "Features";
  if (lower.startsWith("fix")) return "Bug Fixes";
  if (lower.startsWith("update") || lower.startsWith("upgrade") || lower.startsWith("refactor")) return "Refactors";
  if (lower.startsWith("remove") || lower.startsWith("delete")) return "Removals";
  return "Other";
}

/**
 * Build the changelog markdown. When newVersion is provided, the
 * commits since the latest tag are grouped under that version using
 * today's date — useful for a release-candidate preview.
 */
export function generateChangelog(newVersion) {
  const tags = getTags();
  const entries = [];

  if (newVersion) {
    const vTag = newVersion.startsWith("v") ? newVersion : `v${newVersion}`;
    const commits = getCommits(tags[0], "HEAD");
    if (commits.length > 0) {
      entries.push({
        tag: vTag,
        date: new Date().toISOString().slice(0, 10),
        commits,
      });
    }
  }

  for (let i = 0; i < tags.length; i++) {
    const commits = getCommits(tags[i + 1], tags[i]);
    if (commits.length > 0) {
      entries.push({ tag: tags[i], date: getTagDate(tags[i]), commits });
    }
  }

  let md = "# Changelog\n\nAll notable changes to cronix are documented here. Generated from `git log`; see ts/scripts/generate-changelog.mjs.\n";

  for (const entry of entries) {
    md += `\n## [${entry.tag.replace(/^v/, "")}] - ${entry.date}\n`;
    const groups = {};
    for (const c of entry.commits) {
      const cat = categorize(c.message);
      (groups[cat] ??= []).push(c);
    }
    const order = ["Features", "Bug Fixes", "Refactors", "Performance", "Docs", "Tests", "Chores", "Removals", "Other"];
    for (const cat of order) {
      if (!groups[cat]) continue;
      md += `\n### ${cat}\n\n`;
      for (const c of groups[cat]) {
        md += `- ${c.message} (\`${c.hash}\`)\n`;
      }
    }
  }

  return md;
}

/**
 * Write CHANGELOG.md to repo root. Returns the list of touched paths so
 * the bump script can stage exactly those files.
 */
export function writeChangelog(newVersion) {
  const content = generateChangelog(newVersion);
  writeFileSync(changelogPath, content);
  console.log(`  changelog: CHANGELOG.md`);
  return [changelogPath];
}

// Standalone:  node ts/scripts/generate-changelog.mjs [version] [--write]
const isMain = process.argv[1]?.endsWith("generate-changelog.mjs");
if (isMain) {
  const args = process.argv.slice(2);
  const flags = args.filter((a) => a.startsWith("--"));
  const positional = args.filter((a) => !a.startsWith("--"));
  const version = positional[0];
  const content = generateChangelog(version);
  if (flags.includes("--write")) {
    writeFileSync(changelogPath, content);
    console.log("Wrote CHANGELOG.md");
  } else {
    process.stdout.write(content);
  }
}
