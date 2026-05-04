package commands

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/backends/crontab"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/reconcile"
	"github.com/awbx/cronix/go/internal/trigger"
)

func newApplyCmd() *cobra.Command {
	var (
		manifestSource string
		secretRefs     []string
		backendName    string
		crontabPath    string
		triggerBin     string
		specDir        string
		dryRun         bool
		output         string
	)
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Reconcile a manifest against the host scheduler",
		Long: `apply reads a manifest, computes a Plan against the configured backend,
and executes it. With --dry-run, only the Plan is printed (same output as
` + "`cronix plan`" + `).

cronix apply with no manifest changes is a complete no-op (D-027) — safe
to run on every CI deploy.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			normalized, err := loadAndNormalize(ctx, manifestSource, secretRefs)
			if err != nil {
				return err
			}
			b, err := buildBackend(backendName, crontabPath, triggerBin)
			if err != nil {
				return err
			}
			plan, err := reconcile.Compute(ctx, normalized, b)
			if err != nil {
				return err
			}
			if dryRun {
				return printPlan(cmd, output, plan, normalized, true)
			}
			res, err := reconcile.Apply(ctx, plan, b)
			if err != nil {
				return err
			}
			if specDir != "" {
				if err := writeSpecs(specDir, normalized, secretRefs); err != nil {
					return fmt.Errorf("apply: write specs: %w", err)
				}
			}
			return printApplyResult(cmd, output, plan, res)
		},
	}
	cmd.Flags().StringVar(&manifestSource, "manifest", "", "manifest source (file://, ./path, /abs/path, https://, http://localhost) — required")
	_ = cmd.MarkFlagRequired("manifest")
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "secret_ref for HTTPS manifest fetches and trigger spec files")
	cmd.Flags().StringVar(&backendName, "backend", "crontab", "host scheduler backend (crontab|systemd-timer|kubernetes)")
	cmd.Flags().StringVar(&crontabPath, "crontab-path", "/etc/crontab", "crontab file (when --backend=crontab)")
	cmd.Flags().StringVar(&triggerBin, "trigger-bin", "/usr/local/bin/cronix", "absolute path to the cronix binary on the host")
	cmd.Flags().StringVar(&specDir, "spec-dir", "/etc/cronix/jobs", "where to write per-job spec files for the trigger shim")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Plan but do not execute")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

// newPlanAlias returns a `plan` subcommand that mirrors `apply` with
// --dry-run forced. Aliased as `diff`.
func newPlanAlias(_ *cobra.Command) *cobra.Command {
	cmd := newApplyCmd()
	cmd.Use = "plan"
	cmd.Aliases = []string{"diff"}
	cmd.Short = "Show the Plan that apply would execute"
	cmd.Long = `plan reads a manifest, computes a Plan against the configured backend,
and prints what apply would do. Equivalent to apply --dry-run.`
	originalRun := cmd.RunE
	cmd.RunE = func(c *cobra.Command, args []string) error {
		_ = c.Flags().Set("dry-run", "true")
		return originalRun(c, args)
	}
	return cmd
}

// buildBackend constructs the named backend with the given options.
func buildBackend(name, crontabPath, triggerBin string) (backends.Backend, error) {
	switch name {
	case "", "crontab":
		return crontab.New(crontab.Options{Path: crontabPath, TriggerBin: triggerBin})
	case "systemd-timer":
		return nil, fmt.Errorf("backend systemd-timer is render-only in this phase — see PLAN.md §5c")
	case "kubernetes":
		return nil, fmt.Errorf("backend kubernetes is render-only in this phase — see PLAN.md §5d")
	default:
		return nil, fmt.Errorf("unknown backend %q", name)
	}
}

// writeSpecs emits the per-job trigger spec files the shim reads at fire time.
func writeSpecs(dir string, m *manifest.NormalizedManifest, secretRefs []string) error {
	for _, job := range m.Jobs {
		spec := &trigger.SpecFile{
			App:        m.App,
			Job:        job,
			SecretRefs: append([]string(nil), secretRefs...),
		}
		if err := spec.Save(dir); err != nil {
			return err
		}
	}
	return nil
}

type planReport struct {
	Backend string         `json:"backend"`
	Ops     []planOpReport `json:"ops"`
	Noop    bool           `json:"noop"`
}

type planOpReport struct {
	Action  string `json:"action"`
	App     string `json:"app"`
	Job     string `json:"job"`
	OldHash string `json:"old_hash,omitempty"`
	NewHash string `json:"new_hash,omitempty"`
}

func printPlan(cmd *cobra.Command, output string, plan *reconcile.Plan, _ *manifest.NormalizedManifest, _ bool) error {
	rep := planReport{Backend: plan.Backend, Noop: plan.IsNoop()}
	for _, op := range plan.Ops {
		rep.Ops = append(rep.Ops, planOpReport{
			Action: string(op.Action), App: op.App, Job: op.JobName,
			OldHash: op.OldHash, NewHash: op.NewHash,
		})
	}
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "Plan: backend=%s noop=%v ops=%d\n", plan.Backend, plan.IsNoop(), len(plan.Ops))
		for _, op := range plan.Ops {
			marker := actionMarker(string(op.Action))
			line := fmt.Sprintf("  %s %-7s %s.%s", marker, op.Action, op.App, op.JobName)
			if op.OldHash != "" || op.NewHash != "" {
				line += fmt.Sprintf("  (%s → %s)", short(op.OldHash), short(op.NewHash))
			}
			fmt.Fprintln(cmd.OutOrStdout(), line)
		}
	}
	return nil
}

func printApplyResult(cmd *cobra.Command, output string, plan *reconcile.Plan, res reconcile.Result) error {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Backend string `json:"backend"`
			Created int    `json:"created"`
			Updated int    `json:"updated"`
			Deleted int    `json:"deleted"`
			Skipped int    `json:"skipped"`
		}{plan.Backend, res.Created, res.Updated, res.Deleted, res.Skipped})
	default:
		if plan.IsNoop() {
			fmt.Fprintln(cmd.OutOrStdout(), "Apply: noop (nothing to change)")
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Apply: backend=%s created=%d updated=%d deleted=%d skipped=%d\n",
			plan.Backend, res.Created, res.Updated, res.Deleted, res.Skipped)
	}
	return nil
}

func actionMarker(action string) string {
	switch action {
	case "create":
		return "+"
	case "update":
		return "~"
	case "delete":
		return "-"
	case "skip":
		return "·"
	}
	return "?"
}

func short(h string) string {
	if h == "" {
		return "(none)"
	}
	if len(h) > 16 {
		return h[:16]
	}
	return h
}
