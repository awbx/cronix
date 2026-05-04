package commands

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/backends"
)

func newListCmd() *cobra.Command {
	var (
		backendName string
		crontabPath string
		triggerBin  string
		output      string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List cronix-owned entries currently installed in the backend",
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := buildBackend(backendName, crontabPath, triggerBin)
			if err != nil {
				return err
			}
			entries, err := b.List(cmd.Context())
			if err != nil {
				return err
			}
			return printList(cmd, output, b.Name(), entries)
		},
	}
	cmd.Flags().StringVar(&backendName, "backend", "crontab", "host scheduler backend (crontab|systemd-timer|kubernetes)")
	cmd.Flags().StringVar(&crontabPath, "crontab-path", "/etc/crontab", "crontab file (when --backend=crontab)")
	cmd.Flags().StringVar(&triggerBin, "trigger-bin", "/usr/local/bin/cronix", "absolute path to the cronix binary on the host")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json")
	return cmd
}

func printList(cmd *cobra.Command, output, backend string, entries []backends.ManagedEntry) error {
	switch output {
	case "json":
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Backend string                  `json:"backend"`
			Entries []backends.ManagedEntry `json:"entries"`
		}{backend, entries})
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "APP\tJOB\tIDX\tHASH\n")
		for _, e := range entries {
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", e.App, e.Job, e.Index, short(e.Hash))
		}
		return w.Flush()
	}
}
