package commands

import (
	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func NewRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "cronix",
		Short:         "Cron jobs as code",
		Long:          "cronix reconciles application-declared cron manifests against the host's native scheduler.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newTriggerCmd())
	cmd.AddCommand(newValidateCmd())
	applyCmd := newApplyCmd()
	cmd.AddCommand(applyCmd)
	cmd.AddCommand(newPlanAlias(applyCmd))
	cmd.AddCommand(newListCmd())
	cmd.AddCommand(newDriftCmd())
	cmd.AddCommand(newPruneCmd())
	cmd.AddCommand(newCompletionCmd(cmd))
	return cmd
}
