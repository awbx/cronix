package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/reconcile"
)

func newDriftCmd() *cobra.Command {
	cmd := buildDriftVariant("drift", bindBackendFlags, "")
	addBackendSubcommands(cmd, func(name string, bind func(*cobra.Command, *backendOpts)) *cobra.Command {
		return buildDriftVariant(name, bind, name)
	})
	return cmd
}

func buildDriftVariant(use string, bindBE func(*cobra.Command, *backendOpts), forcedBackend string) *cobra.Command {
	var (
		manifestSource string
		secretRefs     []string
		bopts          backendOpts
		output         string
		exitOnDrift    bool
	)
	short := "Report entries whose installed state diverges from the manifest"
	if forcedBackend != "" {
		short = fmt.Sprintf("Report drift between the manifest and the %s backend", forcedBackend)
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long: `drift reads the manifest and the backend's current state, then prints
the operations apply would perform to bring the backend into agreement.

With --exit-on-drift, exits non-zero (5) when any drift is detected — useful
in CI to fail builds whose deployed cron state has drifted from the source
of truth.`,
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
			plan, err := reconcile.Drift(ctx, normalized, b)
			if err != nil {
				return err
			}
			if err := printPlan(cmd, output, plan, normalized, false); err != nil {
				return err
			}
			if exitOnDrift && !plan.IsNoop() {
				return errExitOnDrift
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&manifestSource, "manifest", "", "manifest source — required")
	_ = cmd.MarkFlagRequired("manifest")
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "secret_ref for HTTPS manifest fetches")
	bindBE(cmd, &bopts)
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	cmd.Flags().BoolVar(&exitOnDrift, "exit-on-drift", false, "exit 5 when drift is detected")
	return cmd
}

// errExitOnDrift is a sentinel surfaced through cobra so the binary can
// translate it into exit code 5 (documented per CLI exit code map).
var errExitOnDrift = exitErr{code: 5, msg: "drift detected"}

type exitErr struct {
	code int
	msg  string
}

func (e exitErr) Error() string { return e.msg }
func (e exitErr) ExitCode() int { return e.code }
