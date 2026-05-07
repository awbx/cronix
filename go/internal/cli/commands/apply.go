package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
	"github.com/awbx/cronix/go/internal/backends/aws"
	"github.com/awbx/cronix/go/internal/backends/crontab"
	"github.com/awbx/cronix/go/internal/backends/kubernetes"
	"github.com/awbx/cronix/go/internal/backends/systemd"
	"github.com/awbx/cronix/go/internal/backends/vercel"
	"github.com/awbx/cronix/go/internal/cli/config"
	"github.com/awbx/cronix/go/internal/manifest"
	"github.com/awbx/cronix/go/internal/reconcile"
	"github.com/awbx/cronix/go/internal/trigger"
)

// backendOpts collects the flag-driven options for buildBackend so the
// signature stays manageable as new backends land.
type backendOpts struct {
	name              string
	crontabPath       string
	triggerBin        string
	systemdDir        string
	k8sNamespace      string
	k8sImage          string
	k8sKubeconfig     string
	k8sInCluster      bool
	awsRegion         string
	awsScheduleGroup  string
	awsTargetArn      string
	awsRoleArn        string
	vercelJsonPath    string
	vercelTriggerPath string
	secretRefs        []string
}

func newApplyCmd() *cobra.Command {
	cmd := buildApplyVariant("apply", bindBackendFlags, "")
	addBackendSubcommands(cmd, func(name string, bind func(*cobra.Command, *backendOpts)) *cobra.Command {
		return buildApplyVariant(name, bind, name)
	})
	return cmd
}

// buildApplyVariant builds a fresh `apply`-shaped cobra.Command. When
// forcedBackend is "" (top-level legacy form), --backend is honored
// at run time and bindBE is the all-in-one binder. When forcedBackend
// is set (sub-subcommand form, e.g. `cronix apply kubernetes`), bindBE
// registers only that backend's flags and the backend name is locked
// at run time.
func buildApplyVariant(use string, bindBE func(*cobra.Command, *backendOpts), forcedBackend string) *cobra.Command {
	var (
		manifestSource string
		secretRefs     []string
		bopts          backendOpts
		specDir        string
		dryRun         bool
		output         string
	)
	short := "Reconcile a manifest against the host scheduler"
	long := `apply reads a manifest, computes a Plan against the configured backend,
and executes it. With --dry-run, only the Plan is printed (same output as
` + "`cronix plan`" + `).

cronix apply with no manifest changes is a complete no-op (D-027) — safe
to run on every CI deploy.`
	if forcedBackend != "" {
		short = fmt.Sprintf("Reconcile a manifest against the %s backend", forcedBackend)
		long = fmt.Sprintf("Apply for the %s backend. Equivalent to "+"`cronix apply --backend %s`"+
			", but only the flags relevant to %s are exposed in --help and shell completion.",
			forcedBackend, forcedBackend, forcedBackend)
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if forcedBackend != "" {
				bopts.name = forcedBackend
			}
			ctx := cmd.Context()
			normalized, err := loadAndNormalize(ctx, manifestSource, secretRefs)
			if err != nil {
				return err
			}
			bopts.secretRefs = secretRefs
			b, err := buildBackend(bopts)
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
			if specDir != "" && bopts.name != "kubernetes" {
				if err := writeSpecs(specDir, normalized, secretRefs); err != nil {
					return fmt.Errorf("apply: write specs: %w", err)
				}
				if err := removeOrphanSpecs(specDir, plan); err != nil {
					return fmt.Errorf("apply: remove orphan specs: %w", err)
				}
			}
			return printApplyResult(cmd, output, plan, res)
		},
	}
	cmd.Flags().StringVar(&manifestSource, "manifest", "", "manifest source (file://, ./path, /abs/path, https://, http://localhost) — required")
	_ = cmd.MarkFlagRequired("manifest")
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "secret_ref for HTTPS manifest fetches and trigger spec files")
	bindBE(cmd, &bopts)
	cmd.Flags().StringVar(&specDir, "spec-dir", "/etc/cronix/jobs", "where to write per-job spec files for the trigger shim (ignored for kubernetes — specs live in ConfigMaps)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the Plan but do not execute")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

// newPlanAlias returns a `plan` subcommand that mirrors `apply` with
// --dry-run forced. Aliased as `diff`. Inherits the same per-backend
// sub-subcommand structure (e.g. `cronix plan kubernetes`).
func newPlanAlias(_ *cobra.Command) *cobra.Command {
	cmd := buildPlanVariant("plan", bindBackendFlags, "")
	cmd.Aliases = []string{"diff"}
	addBackendSubcommands(cmd, func(name string, bind func(*cobra.Command, *backendOpts)) *cobra.Command {
		return buildPlanVariant(name, bind, name)
	})
	return cmd
}

func buildPlanVariant(use string, bindBE func(*cobra.Command, *backendOpts), forcedBackend string) *cobra.Command {
	cmd := buildApplyVariant(use, bindBE, forcedBackend)
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
//
// This is the entry point for flag-driven commands (apply, plan, drift,
// list, prune). global-status calls BuildBackendFromEntry directly.
func buildBackend(opts backendOpts) (backends.Backend, error) {
	return BuildBackendFromEntry(opts.toEntry(), opts.secretRefs)
}

// BuildBackendFromEntry constructs the backend described by a config
// entry. Exported so the global-status command (which loads entries
// from ~/.cronix/config.yaml) can share the same construction logic.
func BuildBackendFromEntry(e config.BackendEntry, secretRefs []string) (backends.Backend, error) {
	t := e.Type
	if t == "" {
		t = "crontab"
	}
	switch t {
	case "crontab":
		return crontab.New(crontab.Options{Path: e.CrontabPath, TriggerBin: e.TriggerBin})
	case "systemd-timer":
		return systemd.New(systemd.Options{
			UnitDir:    e.UnitDir,
			TriggerBin: e.TriggerBin,
		})
	case "kubernetes":
		return kubernetes.New(kubernetes.Options{
			Image:      e.Image,
			Namespace:  e.Namespace,
			SecretRefs: secretRefs,
			Kubeconfig: e.Kubeconfig,
			InCluster:  e.InCluster,
		})
	case "aws-scheduler":
		return aws.New(context.Background(), aws.Options{
			Region:        e.Region,
			ScheduleGroup: e.ScheduleGroup,
			TargetArn:     e.TargetArn,
			RoleArn:       e.RoleArn,
			SecretRefs:    secretRefs,
		})
	case "vercel":
		return vercel.New(vercel.Options{
			JsonPath:          e.VercelJsonPath,
			TriggerPathPrefix: e.VercelTriggerPath,
		})
	default:
		return nil, fmt.Errorf("unknown backend %q", t)
	}
}

// toEntry projects the flag-driven options into a config.BackendEntry so
// the two construction paths can share BuildBackendFromEntry.
func (o backendOpts) toEntry() config.BackendEntry {
	return config.BackendEntry{
		Type:              o.name,
		CrontabPath:       o.crontabPath,
		TriggerBin:        o.triggerBin,
		UnitDir:           o.systemdDir,
		Namespace:         o.k8sNamespace,
		Image:             o.k8sImage,
		Kubeconfig:        o.k8sKubeconfig,
		InCluster:         o.k8sInCluster,
		Region:            o.awsRegion,
		ScheduleGroup:     o.awsScheduleGroup,
		TargetArn:         o.awsTargetArn,
		RoleArn:           o.awsRoleArn,
		VercelJsonPath:    o.vercelJsonPath,
		VercelTriggerPath: o.vercelTriggerPath,
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

// removeOrphanSpecs deletes spec files for jobs the plan removed from the
// backend. The backend Delete op cleans the schedule entry; this cleans the
// matching <app>.<job>.json so the trigger shim can never re-fire a stale spec.
func removeOrphanSpecs(dir string, plan *reconcile.Plan) error {
	for _, op := range plan.Ops {
		if op.Action != reconcile.ActionDelete {
			continue
		}
		path := filepath.Join(dir, op.App+"."+op.JobName+".json")
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", path, err)
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
