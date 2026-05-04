# cronix-trigger-lambda

Companion Lambda for the `aws-scheduler` backend. EventBridge Scheduler invokes this function on every fire; the Lambda signs the canonical request per `spec/RFC.md` §Authentication and POSTs to the application URL.

The Lambda is stateless. Every fire's spec rides on the EventBridge Schedule's `Input` field — there is no separate spec store (S3, SSM Parameter Store) for v1. Bodies up to ~256 KB fit comfortably under EventBridge's `Input` limit.

## Build

The Lambda binary lives in the main Go module so it shares `internal/auth` / `internal/headers` / `internal/trigger` with the on-host shim — wire format stays byte-identical across backends.

```bash
cd go
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap \
  ./cmd/cronix-trigger-lambda
zip cronix-trigger.zip bootstrap
```

Resulting binary is ~8.5 MB statically linked (no `glibc` runtime dep).

## Deploy

### IAM execution role

The Lambda needs to read its own logs + any secrets backends it actually uses. Minimum policy assuming env-only secrets:

```json
{
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["logs:CreateLogGroup", "logs:CreateLogStream", "logs:PutLogEvents"],
      "Resource": "arn:aws:logs:*:*:*"
    }
  ]
}
```

For SSM Parameter Store secrets, add:

```json
{ "Effect": "Allow", "Action": ["ssm:GetParameter"], "Resource": "arn:aws:ssm:*:*:parameter/cronix/*" }
```

For Secrets Manager, add:

```json
{ "Effect": "Allow", "Action": ["secretsmanager:GetSecretValue"], "Resource": "arn:aws:secretsmanager:*:*:secret:cronix/*" }
```

### Function

```bash
aws lambda create-function \
  --function-name cronix-trigger \
  --runtime provided.al2 \
  --architectures arm64 \
  --role arn:aws:iam::123456789012:role/cronix-trigger-lambda \
  --handler bootstrap \
  --zip-file fileb://cronix-trigger.zip \
  --timeout 60 \
  --memory-size 256
```

Sized for typical jobs (HMAC + one HTTP request). Bump `--timeout` for long-running app endpoints — the Lambda timeout must exceed the job's `policy.timeout_seconds`.

### Schedule invoke role

Separate from the execution role. EventBridge Scheduler assumes this role to call `lambda:InvokeFunction`:

```json
{
  "Statement": [{
    "Effect": "Allow",
    "Action": "lambda:InvokeFunction",
    "Resource": "arn:aws:lambda:us-east-1:123456789012:function:cronix-trigger"
  }]
}
```

Trust policy allows `scheduler.amazonaws.com`. Pass this role's ARN to `cronix apply --aws-role-arn`.

## Secret resolution

The Lambda's resolver supports five `secret_ref` schemes:

| Scheme | Source | Notes |
|---|---|---|
| `env:NAME` | `os.Getenv("NAME")` | Set via Lambda `--environment` |
| `file:/path` | File on the Lambda image | Bundled into the zip |
| `raw:literal` | Literal string | Discouraged in production — visible in `GetSchedule` |
| `ssm:/path` | SSM Parameter Store | `WithDecryption=true` |
| `secretsmanager:id` | Secrets Manager | `SecretString` or `SecretBinary` |

Schemes are resolved in `secret_refs` order. The first non-empty value signs each fire — verifier accepts any of the listed secrets, so rotation is a drop-in extra entry at index 0 followed by a deploy.

## Event shape

EventBridge passes the schedule's `Input` field as the raw event JSON. cronix sets `Input` to a marshaled `trigger.SpecFile`:

```json
{
  "app": "billing",
  "job": {
    "name": "reconcile-payments",
    "schedules": ["*/15 * * * *"],
    "request": {
      "method": "POST",
      "url": "https://api.example.com/api/v1/scheduled/reconcile-payments",
      "headers": {},
      "body": ""
    },
    "policy": { "concurrency": "Forbid", "timeout_seconds": 60, "retries": { "max_attempts": 3, "min_seconds": 1, "max_seconds": 60 } },
    "auth": { "secret_refs": [] }
  },
  "secret_refs": ["ssm:/cronix/billing/secret"],
  "schedule_index": 0
}
```

The Lambda parses this directly and reuses `trigger.Run` — same code path as the on-host shim.

## Concurrency

The Lambda does not acquire a distributed lock. `policy.concurrency=Forbid` is enforced by AWS Scheduler at most once-per-fire semantics + application idempotency. Operators needing hard cross-fire `Forbid` semantics should attach a Redis lock backend behind a VPC and set `LOCK_REDIS_URL` (not yet wired — follow-up).

## Logs

Each fire emits one structured JSON line per phase to CloudWatch Logs. The eventual `cronix history --backend aws-scheduler` will run a Logs Insights query against the function's log group.
