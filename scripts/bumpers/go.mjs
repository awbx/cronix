// Go bumper: intentionally a no-op.
//
// Go modules are versioned by git tag — there is no `version` field in
// go.mod, and we deliberately keep no source-level constant either.
// goreleaser sets the runtime version at build time:
//
//   -X github.com/awbx/cronix/go/internal/cli/commands.version={{.Version}}
//
// The `version = "dev"` default in commands/root.go is overwritten on
// every release build, so the source never needs editing for a bump.
//
// This bumper exists so the orchestrator's bumper list reads as the
// "official" list of what's versionable in the repo. If a future
// release process needs to write a Go const (eg. for a `cronix sdk`
// minimum-version check), wire it here and return its path in `touched`.

export const name = "go";

export function bump(_version, _ctx) {
  console.log(`  go: no source-level bump needed (goreleaser injects via -ldflags)`);
  return { touched: [] };
}
