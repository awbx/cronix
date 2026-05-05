---
title: AWS EventBridge Scheduler backend
description: Reconcile against EventBridge Schedules → cronix-trigger Lambda.
---

:::note[Status]
`apply` / `plan` / `drift` / `list` / `prune` work end-to-end against EventBridge Scheduler as of v0.6.0. `cronix history --backend aws-scheduler` returns nil — CloudWatch Logs Insights wiring is a follow-up. The Lambda trigger shim (the recommended target) is also a follow-up; see "Target shape" below.
:::

## Layout

Per (app, job, schedule-index), cronix manages one EventBridge Schedule:

```
Name:        cronix-<app>-<job>-<idx>
Group:       <schedule-group>     (operator-configured, defaults to "default")
Description: cronix-managed app=<app> job=<job> idx=<idx> hash=<hash>
Target:      <target-arn> (RoleArn=<role-arn>)
Input:       {"app":"<app>","job":"<job>","index":<idx>}
```

Ownership lives in the `cronix-` name prefix and the structured `Description` field. Both come back on `GetSchedule`, so `List` needs no extra API calls and never touches schedules whose description doesn't match `cronix-managed app=… job=…`.

## Reconciling a manifest

```bash
cronix apply \
  --manifest ./billing.cronix.json \
  --backend aws-scheduler \
  --aws-region us-east-1 \
  --aws-schedule-group cronix \
  --aws-target-arn  arn:aws:lambda:us-east-1:123456789012:function:cronix-trigger \
  --aws-role-arn    arn:aws:iam::123456789012:role/cronix-scheduler-invoke
```

Credentials follow the standard AWS SDK chain (env vars → `~/.aws/config` → EC2/EKS metadata → SSO). `--aws-region` overrides the chain when set; otherwise the chain's region is used.

`cronix list`, `cronix plan`, `cronix drift`, `cronix prune`, and `cronix show` all accept the same backend flags.

## Target shape

EventBridge Scheduler can target HTTPS endpoints directly, but the request body and headers are frozen at schedule-create time. cronix's signed-trigger contract requires a per-fire timestamp + HMAC over a canonical body (see `spec/RFC.md` §Authentication), which a static target body can't provide.

The recommended target is a thin Lambda that:

1. Receives the schedule's input — `{"app":"…","job":"…","index":N}`.
2. Loads the per-job spec (S3 / SSM / bundled) to find the application URL and `secret_refs`.
3. Resolves the secret from SSM Parameter Store / Secrets Manager.
4. Signs the canonical request per spec.
5. Issues the HTTPS POST to `https://<app>/api/v1/scheduled/<job>`.

One Lambda is deployed per AWS account. cronix creates one EventBridge Schedule per (app, job, index) targeting that Lambda with per-job input. Reference Lambda code: `deploy/aws/cronix-trigger-lambda/` (follow-up).

## Schedule expression translation

EventBridge uses `cron(min hr day-of-month month day-of-week year)` — six fields with the trailing year usually `*`. day-of-month and day-of-week are mutually exclusive: exactly one must be `?`.

| Manifest schedule | Rendered EventBridge expression |
|---|---|
| `@hourly` | `cron(0 * * * ? *)` |
| `@daily` / `@midnight` | `cron(0 0 * * ? *)` |
| `@weekly` | `cron(0 0 ? * SUN *)` |
| `@monthly` | `cron(0 0 1 * ? *)` |
| `@yearly` / `@annually` | `cron(0 0 1 1 ? *)` |
| `*/15 * * * *` | `cron(*/15 * * * ? *)` |
| `0 9 * * 1-5` | `cron(0 9 ? * 1-5 *)` |

`@every <duration>` is rejected at validate time. Use a 5-field cron expression instead.

## IAM

Two roles are involved.

**Operator role** (used to run `cronix apply`) needs at minimum:

```json
{
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "scheduler:ListSchedules",
      "scheduler:GetSchedule",
      "scheduler:CreateSchedule",
      "scheduler:UpdateSchedule",
      "scheduler:DeleteSchedule",
      "iam:PassRole",
      "sts:GetCallerIdentity"
    ],
    "Resource": "*"
  }]
}
```

`iam:PassRole` is required because EventBridge Scheduler validates that the caller can pass `--aws-role-arn` to the schedule.

**Invoke role** (`--aws-role-arn`) is what EventBridge assumes to call the target. For a Lambda target, the trust policy is `scheduler.amazonaws.com` and the policy needs `lambda:InvokeFunction` on the target ARN.

## Schedule group

The schedule group must exist before `cronix apply` runs. Either use the AWS-managed `default` group or create one out-of-band:

```bash
aws scheduler create-schedule-group --name cronix
```

cronix never creates the group itself — groups are usually shared infrastructure with their own tagging / IAM conventions.

## Run history

Until `cronix history --backend aws-scheduler` ships, use CloudWatch Logs:

```bash
aws logs tail /aws/lambda/cronix-trigger --since 24h --filter-pattern '"reconcile-payments"'
```

The Lambda shim emits one structured JSON line per fire matching the same `shimEvent` shape the systemd / k8s backends use, so the eventual history wiring is a Logs Insights query and a fold.

## Limitations

- `@every <duration>` is not supported. AWS has `rate(...)` for fixed intervals, but cronix's manifest doesn't model `rate(...)` separately yet — use a 5-field cron expression.
- `cronix history` returns nil. CloudWatch Logs Insights wiring is non-trivial because operator log-group conventions vary; the Lambda shim and matching Logs reader land together in a follow-up.
- The Lambda shim itself ships separately. Until then, operators can target an HTTPS endpoint directly with a fixed body, but the application has to accept unsigned requests — not a v1-grade story.
- Schedule name limit: `cronix-<app>-<job>-<idx>` must be ≤ 64 characters. cronix validates this at apply time.
