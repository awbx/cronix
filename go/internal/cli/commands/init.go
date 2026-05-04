package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

func newInitCmd() *cobra.Command {
	var (
		configPath  string
		appName     string
		manifestURL string
		secretRefs  []string
		force       bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a cronix.yaml at ~/.cronix/cronix.yaml",
		Long: `init writes a heavily-commented operator config to the chosen path.
Refuses to overwrite an existing file unless --force is passed.

Pre-fill an app entry at scaffold time:

  cronix init \
    --app billing \
    --manifest-url https://billing.example.com/.well-known/cron-manifest \
    --secret-ref env:CRON_SECRET

Default path is $CRONIX_CONFIG, ~/.cronix/cronix.yaml, or
/etc/cronix/cronix.yaml — same resolution as the rest of the CLI.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			path := configPath
			if path == "" {
				if env := os.Getenv("CRONIX_CONFIG"); env != "" {
					path = env
				}
			}
			if path == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("init: cannot resolve $HOME: %w", err)
				}
				path = filepath.Join(home, ".cronix", "cronix.yaml")
			}
			if !force {
				if _, err := os.Stat(path); err == nil {
					return fmt.Errorf("init: %s already exists (pass --force to overwrite)", path)
				}
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("init: mkdir: %w", err)
			}
			contents := renderInitConfig(appName, manifestURL, secretRefs)
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				return fmt.Errorf("init: write %s: %w", path, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", path)
			fmt.Fprintln(cmd.OutOrStdout(), "next: edit manifest_sources[].url and secret_refs, then run `cronix apply --config "+path+"`.")
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "destination path (defaults to $CRONIX_CONFIG / ~/.cronix/cronix.yaml)")
	cmd.Flags().StringVar(&appName, "app", "", "pre-fill the first manifest_sources entry's app name")
	cmd.Flags().StringVar(&manifestURL, "manifest-url", "", "pre-fill the first manifest_sources entry's url")
	cmd.Flags().StringSliceVar(&secretRefs, "secret-ref", nil, "pre-fill secret_refs (env:NAME / file:/path / raw:literal)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

func renderInitConfig(app, url string, secretRefs []string) string {
	var manifestBlock string
	if app != "" || url != "" || len(secretRefs) > 0 {
		var refs strings.Builder
		if len(secretRefs) == 0 {
			refs.WriteString("      - env:CRON_SECRET   # FIXME: replace with your real secret_ref")
		} else {
			for i, r := range secretRefs {
				if i > 0 {
					refs.WriteString("\n")
				}
				refs.WriteString("      - " + r)
			}
		}
		manifestBlock = fmt.Sprintf(`manifest_sources:
  - app: %s
    url: %s
    secret_refs:
%s
`, defaultIfEmpty(app, "<app>"), defaultIfEmpty(url, "https://example.com/.well-known/cron-manifest"), refs.String())
	} else {
		manifestBlock = `manifest_sources: []
  # Each entry is one app whose manifest cronix should reconcile:
  # - app: billing
  #   url: https://billing.example.com/.well-known/cron-manifest
  #   secret_refs:
  #     - env:CRON_SECRET
`
	}
	return `# cronix operator configuration.
# Resolved order: --config flag, $CRONIX_CONFIG, ~/.cronix/cronix.yaml,
# /etc/cronix/cronix.yaml. First match wins.

log_level: info

` + manifestBlock + `
locks:
  default: flock
  flock:
    dir: /var/lock/cronix
  # Uncomment when any job uses concurrency_scope: global:
  # redis:
  #   addr: redis:6379
  #   db: 0
  #   key_prefix: cronix:

defaults:
  timeout_seconds: 60
  retries:
    max_attempts: 3
    min_seconds: 1
    max_seconds: 60
`
}

func defaultIfEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
