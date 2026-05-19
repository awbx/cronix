import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

export default defineConfig({
  site: "https://awbx.github.io",
  base: "/cronix",
  integrations: [
    starlight({
      title: "cronix",
      description: "Cron jobs as code — reconcile app-declared schedules against the host's native scheduler.",
      social: {
        github: "https://github.com/awbx/cronix",
        npm: "https://www.npmjs.com/package/@awbx/cronix-sdk",
      },
      editLink: {
        baseUrl: "https://github.com/awbx/cronix/edit/main/docs/",
      },
      lastUpdated: true,
      sidebar: [
        {
          label: "Getting started",
          items: [
            { label: "Install", slug: "install" },
            { label: "Quick start", slug: "quickstart" },
          ],
        },
        {
          label: "Concepts",
          collapsed: false,
          items: [
            { label: "Manifest format", slug: "concepts/manifest" },
            { label: "Authentication", slug: "concepts/auth" },
            { label: "Secrets & rotation", slug: "concepts/secrets" },
            { label: "Concurrency policies", slug: "concepts/concurrency" },
            { label: "Retries & timeouts", slug: "concepts/retries" },
            { label: "Trigger lifecycle", slug: "concepts/trigger-lifecycle" },
            { label: "Drift detection", slug: "concepts/drift" },
            { label: "State management", slug: "concepts/state" },
          ],
        },
        {
          label: "Backends",
          collapsed: false,
          items: [
            { label: "Overview", slug: "backends/overview" },
            { label: "Coverage strategy", slug: "backends/coverage" },
            { label: "crontab", slug: "backends/crontab" },
            { label: "systemd-timer", slug: "backends/systemd" },
            { label: "Kubernetes", slug: "backends/kubernetes" },
            { label: "AWS EventBridge Scheduler", slug: "backends/aws" },
            { label: "Vercel Cron", slug: "backends/vercel" },
          ],
        },
        {
          label: "CLI",
          collapsed: false,
          items: [
            { label: "apply", slug: "cli/apply" },
            { label: "plan / diff", slug: "cli/plan" },
            { label: "drift", slug: "cli/drift" },
            { label: "list", slug: "cli/list" },
            { label: "show", slug: "cli/show" },
            { label: "prune", slug: "cli/prune" },
            { label: "history", slug: "cli/history" },
            { label: "validate", slug: "cli/validate" },
            { label: "trigger", slug: "cli/trigger" },
            { label: "init", slug: "cli/init" },
            { label: "version", slug: "cli/version" },
            { label: "completion", slug: "cli/completion" },
            { label: "global-status", slug: "cli/global-status" },
            { label: "Backend flags", slug: "cli/backend-flags" },
          ],
        },
        {
          label: "SDK",
          collapsed: false,
          items: [
            { label: "TypeScript", slug: "sdk/typescript" },
            { label: "Framework adapters", slug: "sdk/adapters" },
            { label: "Extension points", slug: "sdk/extension-points" },
            { label: "Go", slug: "sdk/go" },
          ],
        },
        {
          label: "Reference",
          items: [
            { label: "RFC (protocol)", link: "https://github.com/awbx/cronix/blob/main/spec/RFC.md" },
            { label: "Contributing", link: "https://github.com/awbx/cronix/blob/main/CONTRIBUTING.md" },
            { label: "Security", link: "https://github.com/awbx/cronix/blob/main/SECURITY.md" },
          ],
        },
      ],
    }),
  ],
});
