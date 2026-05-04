package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newCompletionCmd(root *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <bash|zsh|fish|powershell>",
		Short: "Emit a shell completion script",
		Long: `completion writes a shell completion script to stdout. To install:
  bash:  cronix completion bash > /etc/bash_completion.d/cronix
  zsh:   cronix completion zsh > "${fpath[1]}/_cronix"
  fish:  cronix completion fish > ~/.config/fish/completions/cronix.fish`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			switch args[0] {
			case "bash":
				return root.GenBashCompletion(out)
			case "zsh":
				return root.GenZshCompletion(out)
			case "fish":
				return root.GenFishCompletion(out, true)
			case "powershell":
				return root.GenPowerShellCompletionWithDesc(out)
			}
			return fmt.Errorf("unknown shell: %s", args[0])
		},
	}
	return cmd
}
