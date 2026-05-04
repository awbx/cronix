# go-app — Go SDK example

A small Go HTTP server that verifies signed cronix triggers using `github.com/awbx/cronix/go/pkg/cronsdk`. The verification is a single call (`cronsdk.VerifyHTTP`) — the SDK is intentionally tiny in v1 (signature verification only).

```bash
cd go/examples/go-app
CRON_SECRET=whsec_dev go run .
```

In another shell:

```bash
# Generate a manifest declaring this app, then apply it.
cat > /tmp/cronix-manifest.json <<EOF
{
  "version": 1,
  "app": "billing",
  "jobs": [
    { "name": "reconcile-payments", "schedule": "*/15 * * * *",
      "request": { "url": "http://localhost:3000/api/v1/scheduled/reconcile-payments" } }
  ]
}
EOF
cronix apply --manifest /tmp/cronix-manifest.json --backend crontab \
  --crontab-path /tmp/cronix-test-crontab --trigger-bin "$(go env GOPATH)/bin/cronix" \
  --spec-dir /tmp/cronix-specs --secret-ref raw:whsec_dev

# Manually fire one:
CRONIX_JOB_SPEC_DIR=/tmp/cronix-specs cronix trigger billing.reconcile-payments
```
