package commands

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

func newShowCmd() *cobra.Command {
	cmd := buildShowVariant("show <app>.<job>", bindBackendFlags, "")
	addBackendSubcommands(cmd, func(name string, bind func(*cobra.Command, *backendOpts)) *cobra.Command {
		return buildShowVariant(name+" <app>.<job>", bind, name)
	})
	return cmd
}

func buildShowVariant(use string, bindBE func(*cobra.Command, *backendOpts), forcedBackend string) *cobra.Command {
	var (
		bopts          backendOpts
		manifestSource string
		secretRefs     []string
		output         string
	)
	short := "Inspect one cronix-managed job"
	if forcedBackend != "" {
		short = fmt.Sprintf("Inspect one cronix-managed job in the %s backend", forcedBackend)
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `show prints the backend's current state for a single (app, job) pair —
schedules, hashes, and per-index entries. With --manifest, the desired spec
is loaded and printed alongside, with an in-sync / drifted indicator
computed from the same hash the reconciler uses.

Examples:
  cronix show billing.reconcile
  cronix show billing.reconcile --backend kubernetes --k8s-namespace billing
  cronix show kubernetes billing.reconcile --k8s-namespace billing
  cronix show billing.reconcile --manifest ./billing.cronix.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if forcedBackend != "" {
				bopts.name = forcedBackend
			}
			ctx := cmd.Context()
			app, job, ok := splitAppJob(args[0])
			if !ok {
				return fmt.Errorf("show: expected <app>.<job>, got %q", args[0])
			}
			b, err := buildBackend(bopts)
			if err != nil {
				return err
			}
			entries, err := b.List(ctx)
			if err != nil {
				return err
			}
			matched := filterAppJob(entries, app, job)

			var desired *manifest.NormalizedJob
			if manifestSource != "" {
				normalized, err := loadAndNormalize(ctx, manifestSource, secretRefs)
				if err != nil {
					return err
				}
				if normalized.App != app {
					return fmt.Errorf("show: manifest app=%q does not match requested %q", normalized.App, app)
				}
				for i := range normalized.Jobs {
					if normalized.Jobs[i].Name == job {
						desired = &normalized.Jobs[i]
						break
					}
				}
				if desired == nil {
					return fmt.Errorf("show: job %q not found in manifest", job)
				}
			}

			return printShow(cmd, output, b.Name(), app, job, desired, matched)
		},
	}
	bindBE(cmd, &bopts)
	cmd.Flags().StringVar(&manifestSource, "manifest", "", "manifest source — when set, desired spec is shown and drift is reported")
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "secret_ref for HTTPS manifest fetches")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

func splitAppJob(s string) (app, job string, ok bool) {
	idx := strings.IndexByte(s, '.')
	if idx <= 0 || idx == len(s)-1 {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

func filterAppJob(entries []backends.ManagedEntry, app, job string) []backends.ManagedEntry {
	out := make([]backends.ManagedEntry, 0, len(entries))
	for _, e := range entries {
		if e.App == app && e.Job == job {
			out = append(out, e)
		}
	}
	return out
}

type showReport struct {
	Backend string                  `json:"backend"`
	App     string                  `json:"app"`
	Job     string                  `json:"job"`
	Found   bool                    `json:"found"`
	InSync  *bool                   `json:"in_sync,omitempty"`
	Desired *manifest.NormalizedJob `json:"desired,omitempty"`
	Entries []backends.ManagedEntry `json:"entries"`
}

func printShow(cmd *cobra.Command, output, backend, app, job string, desired *manifest.NormalizedJob, entries []backends.ManagedEntry) error {
	rep := showReport{
		Backend: backend,
		App:     app,
		Job:     job,
		Found:   len(entries) > 0,
		Desired: desired,
		Entries: entries,
	}
	if desired != nil {
		ok := computeInSync(*desired, entries)
		rep.InSync = &ok
	}
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "BACKEND\t%s\n", backend)
		fmt.Fprintf(w, "JOB\t%s.%s\n", app, job)
		if !rep.Found {
			fmt.Fprintf(w, "STATUS\tnot installed\n")
		}
		if rep.InSync != nil {
			label := "drifted"
			if *rep.InSync {
				label = "in-sync"
			}
			fmt.Fprintf(w, "DRIFT\t%s\n", label)
		}
		if desired != nil {
			fmt.Fprintf(w, "TIMEZONE\t%s\n", desired.Timezone)
			fmt.Fprintf(w, "URL\t%s %s\n", desired.Request.Method, desired.Request.URL)
			fmt.Fprintf(w, "SCHEDULES\t%s\n", strings.Join(desired.Schedules, ", "))
		}
		_ = w.Flush()
		if len(entries) > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
			t := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(t, "IDX\tHASH")
			for _, e := range entries {
				fmt.Fprintf(t, "%d\t%s\n", e.Index, short(e.Hash))
			}
			_ = t.Flush()
		}
	}
	return nil
}

// computeInSync recomputes the desired hashes for `desired` and checks
// they cover every backend entry's index with matching values. Mirrors
// reconcile.Compute's hash logic without exposing the package internals.
func computeInSync(desired manifest.NormalizedJob, entries []backends.ManagedEntry) bool {
	if len(entries) != len(desired.Schedules) {
		return false
	}
	want := make(map[int]string, len(desired.Schedules))
	for i := range desired.Schedules {
		want[i] = jobScheduleHash(desired, i)
	}
	got := make(map[int]string, len(entries))
	for _, e := range entries {
		got[e.Index] = e.Hash
	}
	for idx, hash := range want {
		if got[idx] != hash {
			return false
		}
	}
	return true
}

// jobScheduleHash is the same FNV-1a-over-canonicalized-manifest the
// crontab/k8s/systemd backends embed. Duplicated locally rather than
// reaching into a backend package; the algorithm is the contract.
func jobScheduleHash(job manifest.NormalizedJob, idx int) string {
	b, _ := manifest.Canonicalize(&manifest.NormalizedManifest{
		Version: 1,
		App:     "_hash_",
		Jobs:    []manifest.NormalizedJob{job},
	})
	const (
		offset64 = uint64(1469598103934665603)
		prime64  = uint64(1099511628211)
	)
	h := offset64
	for _, x := range b {
		h ^= uint64(x)
		h *= prime64
	}
	h ^= uint64(idx)
	h *= prime64
	return fmt.Sprintf("%016x", h)
}
