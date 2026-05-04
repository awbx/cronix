package commands

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/awbx/cronix/go/internal/manifest"
)

func newValidateCmd() *cobra.Command {
	var (
		secretRefs []string
		output     string
	)
	cmd := &cobra.Command{
		Use:   "validate <source>",
		Short: "Lint a manifest from a file path or signed URL",
		Long: `validate parses, validates, and normalizes a manifest, reporting any
errors with the path-and-message format used by the conformance vectors.
No side effects.

Sources:
  ./manifest.json        — local file (relative)
  /etc/manifest.json     — local file (absolute)
  file://path            — local file
  https://app/.well-known/cron-manifest — signed HTTPS fetch (requires --secret-ref)`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			normalized, err := loadAndNormalize(ctx, args[0], secretRefs)
			if err != nil {
				var mfErr *manifest.Error
				if errors.As(err, &mfErr) {
					return printValidationError(cmd, output, mfErr)
				}
				return err
			}
			return printValidationOK(cmd, output, normalized)
		},
	}
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "secret_ref (env:NAME, file:/path, raw:literal). Required for HTTPS sources.")
	cmd.Flags().StringVarP(&output, "output", "o", "table", "output format: table|json|yaml")
	return cmd
}

type validationReport struct {
	OK     bool             `json:"ok"`
	App    string           `json:"app,omitempty"`
	Jobs   int              `json:"jobs,omitempty"`
	Issues []manifest.Issue `json:"issues,omitempty"`
}

func printValidationOK(cmd *cobra.Command, output string, m *manifest.NormalizedManifest) error {
	rep := validationReport{OK: true, App: m.App, Jobs: len(m.Jobs)}
	switch output {
	case "json":
		return jsonOut(cmd, rep)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "OK  app=%s jobs=%d\n", m.App, len(m.Jobs))
		for _, j := range m.Jobs {
			fmt.Fprintf(cmd.OutOrStdout(), "  - %s  schedules=%v  policy.timeout=%ds  retries=%d\n",
				j.Name, j.Schedules, j.Policy.TimeoutSeconds, j.Policy.Retries.MaxAttempts)
		}
	}
	return nil
}

func printValidationError(cmd *cobra.Command, output string, mfErr *manifest.Error) error {
	rep := validationReport{OK: false, Issues: mfErr.Issues}
	switch output {
	case "json":
		_ = jsonOut(cmd, rep)
	default:
		fmt.Fprintln(cmd.ErrOrStderr(), "INVALID")
		for _, is := range mfErr.Issues {
			fmt.Fprintf(cmd.ErrOrStderr(), "  %s: %s (%s)\n", joinPath(is.Path), is.Message, is.Code)
		}
	}
	return mfErr
}

func jsonOut(cmd *cobra.Command, v any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func joinPath(p []string) string {
	out := ""
	for i, s := range p {
		if i > 0 {
			out += "/"
		}
		out += s
	}
	if out == "" {
		out = "(root)"
	}
	return out
}
