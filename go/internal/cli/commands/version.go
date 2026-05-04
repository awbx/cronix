package commands

import (
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show version, build, and target platform",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Printf("cronix %s\n  commit: %s\n  built:  %s\n  go:     %s\n  target: %s/%s\n",
				version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return nil
		},
	}
}
