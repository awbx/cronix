package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/locks/flock"
	"github.com/awbx/cronix/go/internal/trigger"
)

func newTriggerCmd() *cobra.Command {
	var (
		specDir string
		lockDir string
	)
	cmd := &cobra.Command{
		Use:   "trigger <app>.<job>",
		Short: "Per-fire executor invoked by the host scheduler",
		Long: `cronix trigger is invoked by the host scheduler (crontab, systemd-timer,
Kubernetes CronJob) at every fire. It loads the job spec from the operator-
managed spec directory, resolves the configured secrets, acquires the
concurrency lock per the job's policy, signs the HTTP request with HMAC-
SHA256, sends it with the configured timeout, and retries on transient
failure with exponential backoff.

Exit codes:
  0   success (any 2xx)
  1   app rejected (4xx) — does not retry
  2   retries exhausted (5xx, network, timeout)
  3   internal error (panic, bad spec, unresolved secrets)
  4   lock contended (Forbid; transient)
  75  same as 4 (POSIX EX_TEMPFAIL)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.SplitN(args[0], ".", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("trigger: argument must be `<app>.<job>`, got %q", args[0])
			}
			app, jobName := parts[0], parts[1]

			lockBackend, err := flock.New(lockDir)
			if err != nil {
				return fmt.Errorf("trigger: lock backend: %w", err)
			}

			handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
			logger := slog.New(handler)

			res := trigger.Run(context.Background(), trigger.Options{
				App:     app,
				JobName: jobName,
				SpecDir: specDir,
				Lock:    lockBackend,
				Logger:  logger,
			})
			os.Exit(res.ExitCode)
			return nil
		},
	}
	cmd.Flags().StringVar(&specDir, "spec-dir", "", "directory containing <app>.<job>.json spec files (default: $CRONIX_JOB_SPEC_DIR or /etc/cronix/jobs)")
	cmd.Flags().StringVar(&lockDir, "lock-dir", "", "directory for flock files (default: /var/lock/cronix)")
	return cmd
}
