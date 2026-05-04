package commands

import (
	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/reconcile"
)

func newDriftCmd() *cobra.Command {
	var (
		manifestSource string
		secretRefs     []string
		backendName    string
		crontabPath    string
		triggerBin     string
		output         string
		exitOnDrift    bool
	)
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Report entries whose installed state diverges from the manifest",
		Long: `drift reads the manifest and the backend's current state, then prints
the operations apply would perform to bring the backend into agreement.

With --exit-on-drift, exits non-zero (5) when any drift is detected — useful
in CI to fail builds whose deployed cron state has drifted from the source
of truth.`,
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
	cmd.Flags().StringVar(&backendName, "backend", "crontab", "host scheduler backend (crontab|systemd-timer|kubernetes)")
	cmd.Flags().StringVar(&crontabPath, "crontab-path", "/etc/crontab", "crontab file")
	cmd.Flags().StringVar(&triggerBin, "trigger-bin", "/usr/local/bin/cronix", "absolute path to the cronix binary on the host")
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
