package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
)

func newPruneCmd() *cobra.Command {
	var (
		bopts   backendOpts
		appOnly string
		yes     bool
		output  string
	)
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Remove all cronix-owned entries from the backend",
		Long: `prune lists every entry the configured backend reports as cronix-owned
and deletes them. With --app, only entries belonging to that app are pruned.

Destructive — the backend's Delete is invoked for every owned (app, job) pair.
Defaults to interactive confirmation; pass --yes to skip the prompt (suitable
for CI uninstalls).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			b, err := buildBackend(bopts)
			if err != nil {
				return err
			}
			entries, err := b.List(ctx)
			if err != nil {
				return err
			}
			matched := filterByApp(entries, appOnly)
			if len(matched) == 0 {
				return printPruneResult(cmd, output, b.Name(), 0, nil)
			}
			pairs := uniqueAppJob(matched)
			if !yes && !confirm(cmd, fmt.Sprintf("This will delete %d cronix-owned entries from backend %q. Continue?", len(matched), b.Name())) {
				return fmt.Errorf("prune: aborted")
			}
			deleted := make([]appJob, 0, len(pairs))
			for _, p := range pairs {
				if err := b.Delete(ctx, p.App, p.Job); err != nil {
					return fmt.Errorf("prune: delete %s.%s: %w", p.App, p.Job, err)
				}
				deleted = append(deleted, p)
			}
			return printPruneResult(cmd, output, b.Name(), len(matched), deleted)
		},
	}
	bindBackendFlags(cmd, &bopts)
	cmd.Flags().StringVar(&appOnly, "app", "", "limit pruning to entries belonging to this app")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the interactive confirmation prompt")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

type appJob struct {
	App string `json:"app"`
	Job string `json:"job"`
}

func filterByApp(entries []backends.ManagedEntry, app string) []backends.ManagedEntry {
	if app == "" {
		return entries
	}
	out := make([]backends.ManagedEntry, 0, len(entries))
	for _, e := range entries {
		if e.App == app {
			out = append(out, e)
		}
	}
	return out
}

// uniqueAppJob collapses multi-schedule entries into one (app, job) pair so
// we call Delete once per job (Backend.Delete removes all schedules at once).
func uniqueAppJob(entries []backends.ManagedEntry) []appJob {
	seen := map[appJob]struct{}{}
	out := make([]appJob, 0)
	for _, e := range entries {
		key := appJob{App: e.App, Job: e.Job}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
}

func confirm(cmd *cobra.Command, prompt string) bool {
	fmt.Fprintf(cmd.OutOrStdout(), "%s [y/N]: ", prompt)
	r := bufio.NewReader(cmd.InOrStdin())
	if cmd.InOrStdin() == os.Stdin && !isTerminal(os.Stdin) {
		return false
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func printPruneResult(cmd *cobra.Command, output, backend string, count int, deleted []appJob) error {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Backend string   `json:"backend"`
			Pruned  int      `json:"pruned"`
			Jobs    []appJob `json:"jobs"`
		}{backend, count, deleted})
	default:
		if count == 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "Prune: backend=%s nothing to remove\n", backend)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Prune: backend=%s removed %d entries across %d jobs\n", backend, count, len(deleted))
		for _, p := range deleted {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s.%s\n", p.App, p.Job)
		}
	}
	return nil
}
