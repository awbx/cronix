package commands

import (
	"github.com/spf13/cobra"
)

// backendBinder pairs a backend name with the function that registers
// only that backend's flags onto a cobra command. Used to build the
// kubectl-style sub-subcommands (e.g. `cronix apply kubernetes`) where
// --help and shell completion list only the relevant flags.
type backendBinder struct {
	Name string
	Bind func(*cobra.Command, *backendOpts)
}

// backendBinders is the canonical order. Driven by:
//   - the legacy --backend flag's enum description,
//   - sub-subcommand registration order,
//   - completion suggestions for --backend.
var backendBinders = []backendBinder{
	{"crontab", bindCrontabFlags},
	{"systemd-timer", bindSystemdFlags},
	{"kubernetes", bindKubernetesFlags},
	{"aws-scheduler", bindAwsSchedulerFlags},
	{"vercel", bindVercelFlags},
}

func backendNames() []string {
	out := make([]string, len(backendBinders))
	for i, b := range backendBinders {
		out[i] = b.Name
	}
	return out
}

// bindBackendSelector wires only the legacy --backend flag plus its
// shell-completion enum.
func bindBackendSelector(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.name, "backend", "crontab",
		"host scheduler backend (crontab|systemd-timer|kubernetes|aws-scheduler|vercel)")
	_ = cmd.RegisterFlagCompletionFunc("backend",
		func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return backendNames(), cobra.ShellCompDirectiveNoFileComp
		})
}

// bindTriggerBinFlag is shared between the crontab and systemd-timer
// binders (both invoke the cronix binary on the host). Kept separate
// so each per-backend binder can include it without redefining a flag
// when the legacy bindBackendFlags also wires it.
func bindTriggerBinFlag(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.triggerBin, "trigger-bin", "/usr/local/bin/cronix",
		"absolute path to the cronix binary on the host")
}

func bindCrontabFlags(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.crontabPath, "crontab-path", "/etc/crontab",
		"crontab file")
	bindTriggerBinFlag(cmd, opts)
}

func bindSystemdFlags(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.systemdDir, "systemd-unit-dir", "/etc/systemd/system",
		"directory for owned timer/service unit files")
	bindTriggerBinFlag(cmd, opts)
}

func bindKubernetesFlags(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.k8sNamespace, "k8s-namespace", "default",
		"namespace for owned CronJobs/ConfigMaps")
	cmd.Flags().StringVar(&opts.k8sImage, "k8s-image", "awbx/cronix:latest",
		"cronix container image used by the CronJob pod")
	cmd.Flags().StringVar(&opts.k8sKubeconfig, "kubeconfig", "",
		"path to kubeconfig (defaults to KUBECONFIG / ~/.kube/config / in-cluster)")
	cmd.Flags().BoolVar(&opts.k8sInCluster, "in-cluster", false,
		"load API config from the in-cluster service account")
}

func bindAwsSchedulerFlags(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.awsRegion, "aws-region", "",
		"AWS region (defaults to SDK chain)")
	cmd.Flags().StringVar(&opts.awsScheduleGroup, "aws-schedule-group", "default",
		"EventBridge Schedule group")
	cmd.Flags().StringVar(&opts.awsTargetArn, "aws-target-arn", "",
		"ARN the schedule invokes — typically the cronix-trigger Lambda")
	cmd.Flags().StringVar(&opts.awsRoleArn, "aws-role-arn", "",
		"IAM role EventBridge assumes to call the target")
}

func bindVercelFlags(cmd *cobra.Command, opts *backendOpts) {
	cmd.Flags().StringVar(&opts.vercelJsonPath, "vercel-json-path", "vercel.json",
		"path to vercel.json")
	cmd.Flags().StringVar(&opts.vercelTriggerPath, "vercel-trigger-prefix", "/api/v1/scheduled/",
		"trigger path prefix that identifies cronix-owned cron entries in vercel.json")
}

// bindBackendFlags is the legacy all-in-one binder. Top-level
// commands (cronix apply, plan, drift, list, show, prune, history)
// keep accepting --backend X plus every backend's flag for backwards
// compatibility. The kubectl-style sub-subcommands
// (cronix apply <backend>) call only the matching per-backend binder
// so their --help and shell completion list a focused flag set.
func bindBackendFlags(cmd *cobra.Command, opts *backendOpts) {
	bindBackendSelector(cmd, opts)
	cmd.Flags().StringVar(&opts.crontabPath, "crontab-path", "/etc/crontab",
		"crontab file (when --backend=crontab)")
	bindTriggerBinFlag(cmd, opts)
	cmd.Flags().StringVar(&opts.systemdDir, "systemd-unit-dir", "/etc/systemd/system",
		"directory for owned timer/service unit files (when --backend=systemd-timer)")
	cmd.Flags().StringVar(&opts.k8sNamespace, "k8s-namespace", "default",
		"namespace for owned CronJobs/ConfigMaps (when --backend=kubernetes)")
	cmd.Flags().StringVar(&opts.k8sImage, "k8s-image", "awbx/cronix:latest",
		"cronix container image used by the CronJob pod (when --backend=kubernetes)")
	cmd.Flags().StringVar(&opts.k8sKubeconfig, "kubeconfig", "",
		"path to kubeconfig (defaults to KUBECONFIG / ~/.kube/config / in-cluster)")
	cmd.Flags().BoolVar(&opts.k8sInCluster, "in-cluster", false,
		"load API config from the in-cluster service account (when --backend=kubernetes)")
	cmd.Flags().StringVar(&opts.awsRegion, "aws-region", "",
		"AWS region (when --backend=aws-scheduler; defaults to SDK chain)")
	cmd.Flags().StringVar(&opts.awsScheduleGroup, "aws-schedule-group", "default",
		"EventBridge Schedule group (when --backend=aws-scheduler)")
	cmd.Flags().StringVar(&opts.awsTargetArn, "aws-target-arn", "",
		"ARN the schedule invokes — typically the cronix-trigger Lambda (when --backend=aws-scheduler)")
	cmd.Flags().StringVar(&opts.awsRoleArn, "aws-role-arn", "",
		"IAM role EventBridge assumes to call the target (when --backend=aws-scheduler)")
	cmd.Flags().StringVar(&opts.vercelJsonPath, "vercel-json-path", "vercel.json",
		"path to vercel.json (when --backend=vercel)")
	cmd.Flags().StringVar(&opts.vercelTriggerPath, "vercel-trigger-prefix", "/api/v1/scheduled/",
		"trigger path prefix that identifies cronix-owned cron entries in vercel.json")
}

// addBackendSubcommands registers one sub-subcommand per backend onto
// parent. Each sub-subcommand is built via factory(name, bind) and
// receives only that backend's flags. Used by apply / plan / drift /
// list / show / prune / history.
func addBackendSubcommands(parent *cobra.Command, factory func(name string, bind func(*cobra.Command, *backendOpts)) *cobra.Command) {
	for _, b := range backendBinders {
		parent.AddCommand(factory(b.Name, b.Bind))
	}
}
