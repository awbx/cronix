package commands

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
)

func newHistoryCmd() *cobra.Command {
	var (
		bopts  backendOpts
		since  string
		until  string
		status string
		limit  int
		output string
	)
	cmd := &cobra.Command{
		Use:   "history <app>.<job>",
		Short: "Show recent runs for one cronix-managed job",
		Long: `history reads run records from the backend's native source — journalctl
for systemd-timer, K8s events + pod logs for kubernetes (TODO), syslog
for crontab (TODO) — and prints one row per terminal run.

  cronix history billing.reconcile --backend systemd-timer --since 24h
  cronix history billing.reconcile --backend systemd-timer --status failed -o json

The history surface relies on Backend.History, which in v0.4 is wired
for systemd-timer only. crontab and kubernetes return empty lists; use
` + "`journalctl`" + ` or ` + "`kubectl logs`" + ` directly until the wiring lands.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			app, job, ok := splitAppJob(args[0])
			if !ok {
				return fmt.Errorf("history: expected <app>.<job>, got %q", args[0])
			}
			b, err := buildBackend(bopts)
			if err != nil {
				return err
			}
			opts := backends.HistoryOpts{App: app, Job: job, Status: status, Limit: limit}
			if since != "" {
				dur, err := time.ParseDuration(since)
				if err != nil {
					return fmt.Errorf("history: --since: %w", err)
				}
				opts.Since = time.Now().Add(-dur)
			}
			if until != "" {
				dur, err := time.ParseDuration(until)
				if err != nil {
					return fmt.Errorf("history: --until: %w", err)
				}
				opts.Until = time.Now().Add(-dur)
			}
			entries, err := b.History(ctx, opts)
			if err != nil {
				return err
			}
			return printHistory(cmd, output, b.Name(), entries)
		},
	}
	bindBackendFlags(cmd, &bopts)
	cmd.Flags().StringVar(&since, "since", "", "look back this duration (e.g. 1h, 24h, 7d)")
	cmd.Flags().StringVar(&until, "until", "", "stop at this duration ago (defaults to now)")
	cmd.Flags().StringVar(&status, "status", "", "filter: ok|failed|lock-contended|timeout|unknown")
	cmd.Flags().IntVar(&limit, "limit", 50, "max records to show (0 = no limit)")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

func printHistory(cmd *cobra.Command, output, backend string, entries []backends.HistoryEntry) error {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Backend string                  `json:"backend"`
			Entries []backends.HistoryEntry `json:"entries"`
		}{backend, entries})
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "WHEN\tRUN_ID\tATTEMPT\tSTATUS\tSOURCE")
		for _, e := range entries {
			ts := e.FinishedAt
			if ts.IsZero() {
				ts = e.StartedAt
			}
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
				ts.Format(time.RFC3339), shortID(e.RunID), e.Attempt, e.Status, e.Source)
		}
		return w.Flush()
	}
}

func shortID(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
