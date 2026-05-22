package commands

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/manifest"
)

// newAdoptCmd builds `cronix adopt <app>.<job>` — claims an existing
// scheduler entry that already invokes `cronix trigger` so it comes
// under cronix management without being re-created. Implements
// issue #11 (the crontab side; other backends pending Adopter
// implementations).
func newAdoptCmd() *cobra.Command {
	var (
		opts       backendOpts
		manifestSrc string
		dryRun     bool
		output     string
	)
	cmd := &cobra.Command{
		Use:   "adopt <app>.<job>",
		Short: "Take ownership of an existing scheduler entry without re-creating it",
		Long: `adopt finds a backend entry that already invokes ` + "`cronix trigger <app>.<job>`" + ` and
applies the cronix ownership markers so the entry comes under management
without being re-created. The original line/unit/CronJob/schedule is
preserved byte-for-byte; only the ownership annotation is added.

When the candidate entry diverges from the manifest in a way that would
change semantics (different schedule, different command-line tail, etc.),
adopt refuses to modify it and prints the divergences. Re-run with
` + "`cronix apply`" + ` after reconciling the manifest if you want the entry
brought into agreement.

Migration story: see docs-site §Operations → Migrating from hand-edited
crontab.

Supported backends (v1.0.0-rc.1): crontab. systemd-timer, kubernetes,
aws-scheduler, and vercel adopt support land as follow-up issues.

Sources:
  ./manifest.json
  /etc/manifest.json
  file://path
  https://app/.well-known/cron-manifest`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.SplitN(args[0], ".", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("argument must be `<app>.<job>`, got %q", args[0])
			}
			app, jobName := parts[0], parts[1]

			if manifestSrc == "" {
				return fmt.Errorf("--manifest is required")
			}

			normalized, err := loadAndNormalize(cmd.Context(), manifestSrc, opts.secretRefs)
			if err != nil {
				return fmt.Errorf("load manifest: %w", err)
			}
			if normalized.App != app {
				return fmt.Errorf("manifest declares app %q, command argument is for %q", normalized.App, app)
			}

			job, ok := findJob(normalized, jobName)
			if !ok {
				return fmt.Errorf("job %q not found in manifest", jobName)
			}

			be, err := buildBackend(opts)
			if err != nil {
				return fmt.Errorf("build backend: %w", err)
			}

			adopter, ok := be.(backends.Adopter)
			if !ok {
				return fmt.Errorf("backend %q does not yet support adopt — track the per-backend implementation issue under area/backend-%s", be.Name(), be.Name())
			}

			res, err := adopter.Adopt(cmd.Context(), app, *job, backends.AdoptOpts{DryRun: dryRun})
			if err != nil {
				return fmt.Errorf("adopt: %w", err)
			}
			return printAdoptResult(cmd, output, app, jobName, dryRun, res)
		},
	}
	cmd.Flags().StringVar(&manifestSrc, "manifest", "", "manifest source (URL or path); required")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "report what would be adopted without modifying the backend")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	bindBackendFlags(cmd, &opts)
	return cmd
}

// findJob returns the first NormalizedJob whose Name matches.
func findJob(m *manifest.NormalizedManifest, name string) (*manifest.NormalizedJob, bool) {
	for i := range m.Jobs {
		if m.Jobs[i].Name == name {
			return &m.Jobs[i], true
		}
	}
	return nil, false
}

type adoptReport struct {
	App            string                  `json:"app"`
	Job            string                  `json:"job"`
	DryRun         bool                    `json:"dry_run"`
	Found          bool                    `json:"found"`
	Adopted        bool                    `json:"adopted"`
	AlreadyManaged bool                    `json:"already_managed"`
	Diverged       bool                    `json:"diverged"`
	Divergences    []string                `json:"divergences,omitempty"`
	Entries        []backends.ManagedEntry `json:"entries,omitempty"`
}

func printAdoptResult(cmd *cobra.Command, output, app, job string, dryRun bool, r backends.AdoptResult) error {
	rep := adoptReport{
		App:            app,
		Job:            job,
		DryRun:         dryRun,
		Found:          r.Found,
		Adopted:        r.Adopted,
		AlreadyManaged: r.AlreadyManaged,
		Diverged:       r.Diverged,
		Divergences:    r.Divergences,
		Entries:        r.Entries,
	}
	if output == "json" {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	out := cmd.OutOrStdout()
	switch {
	case r.AlreadyManaged:
		fmt.Fprintf(out, "ALREADY-MANAGED  %s.%s  (no action taken)\n", app, job)
	case r.Diverged:
		fmt.Fprintf(out, "DIVERGED         %s.%s  (no action taken)\n", app, job)
		for _, d := range r.Divergences {
			fmt.Fprintf(out, "  ! %s\n", d)
		}
		fmt.Fprintln(out, "\nResolve by editing the manifest or the backend so they agree, then re-run `cronix adopt`. Or run `cronix apply` to overwrite the backend entry with the manifest's form.")
	case r.Adopted:
		fmt.Fprintf(out, "ADOPTED          %s.%s  (%d entr%s now under management)\n", app, job, len(r.Entries), pluralY(len(r.Entries)))
		for _, e := range r.Entries {
			fmt.Fprintf(out, "  + idx=%d hash=%s\n", e.Index, e.Hash)
		}
	case r.Found && dryRun:
		fmt.Fprintf(out, "WOULD-ADOPT      %s.%s  (%d entr%s — dry-run, no changes)\n", app, job, len(r.Entries), pluralY(len(r.Entries)))
		for _, e := range r.Entries {
			fmt.Fprintf(out, "  + idx=%d hash=%s\n", e.Index, e.Hash)
		}
	default:
		fmt.Fprintf(out, "NOT-FOUND        %s.%s  (no candidate entry on backend)\n", app, job)
	}
	// Non-zero exit when adopt could not complete cleanly; this is how
	// CI consumers detect "needs operator attention". Already-managed and
	// successful adopt are exit 0. Dry-run that found candidates is also
	// exit 0 — the user asked for a preview, not a commit.
	if r.Diverged {
		return exitErr{code: 6, msg: "adopt: candidate diverges from manifest"}
	}
	if !r.Found && !r.AlreadyManaged {
		return exitErr{code: 7, msg: "adopt: no candidate entry found"}
	}
	return nil
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
