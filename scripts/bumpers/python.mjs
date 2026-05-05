// Python bumper. Activates when the repo grows a python/ tree with one
// or more pyproject.toml files. Until then this is a no-op that reports
// "no Python packages found" — keeping it in the orchestrator's bumper
// list is intentional so that the day a Python package lands, no
// release-script change is needed; just `git add python/<pkg>/`.
//
// What this bumper does when active:
//   - Walks python/**/pyproject.toml (depth-limited; doesn't follow .venv)
//   - For each, replaces the `[project] version = "X"` line with the new
//     version. Uses a regex (single source of truth, three lines of code)
//     rather than pulling in a TOML parser.
//   - Returns every path it wrote so the orchestrator can stage exactly
//     those files.

import { existsSync, readFileSync, statSync, writeFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

export const name = "python";

export function bump(version, { repoRoot }) {
  const pythonRoot = join(repoRoot, "python");
  if (!existsSync(pythonRoot)) {
    return { touched: [] };
  }
  const touched = [];
  for (const file of findPyproject(pythonRoot)) {
    const before = readFileSync(file, "utf-8");
    const after = before.replace(
      /(\n\s*\[project\][^\[]*?\n\s*version\s*=\s*)"[^"]+"/,
      `$1"${version}"`,
    );
    if (after !== before) {
      writeFileSync(file, after);
      touched.push(file);
      console.log(`  ${file} -> ${version}`);
    }
  }
  if (touched.length === 0) {
    console.log(`  python: no pyproject.toml found under python/`);
  }
  return { touched };
}

// Bounded recursive walk; skips dot-dirs (.venv, .git) and node_modules.
function findPyproject(dir, depth = 0) {
  if (depth > 4) return [];
  const out = [];
  for (const ent of readdirSync(dir)) {
    if (ent.startsWith(".") || ent === "node_modules") continue;
    const full = join(dir, ent);
    const st = statSync(full);
    if (st.isDirectory()) {
      out.push(...findPyproject(full, depth + 1));
    } else if (ent === "pyproject.toml") {
      out.push(full);
    }
  }
  return out;
}
