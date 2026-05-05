---
title: Install
description: Install the cronix CLI and SDKs.
---

The CLI (the reconciler) and the app SDK are installed independently. You typically need both: the CLI on your laptop or CI host, the SDK in the application that owns the schedules.

## CLI (the reconciler)

### macOS — Homebrew

```bash
brew install awbx/cronix/cronix
```

Or, if you'll install more from this tap later:

```bash
brew tap awbx/cronix
brew install cronix
```

### Linux / macOS — one-liner

```bash
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh | sh
```

Pin a version + custom install dir:

```bash
curl -fsSL https://raw.githubusercontent.com/awbx/cronix/main/install.sh \
  | CRONIX_VERSION=v0.7.2 INSTALL_DIR=/usr/local/bin sh
```

### Linux packages (Debian/Ubuntu, RHEL/Fedora, Alpine)

Download from the [latest GitHub release](https://github.com/awbx/cronix/releases/latest):

| Distro | File | Install |
|---|---|---|
| Debian, Ubuntu | `cronix_<ver>_linux_amd64.deb` | `sudo dpkg -i cronix_*.deb` |
| RHEL, Fedora, openSUSE | `cronix_<ver>_linux_amd64.rpm` | `sudo rpm -i cronix_*.rpm` |
| Alpine | `cronix_<ver>_linux_amd64.apk` | `sudo apk add --allow-untrusted cronix_*.apk` |

### Go developers

```bash
go install github.com/awbx/cronix/go/cmd/cronix@latest
```

Note: built without goreleaser's `-ldflags`, so `cronix version` reports `dev` rather than the tagged version.

### Docker

```bash
docker pull awbx/cronix
```

The Docker image is what the [Kubernetes backend](/cronix/backends/kubernetes/) installs into your cluster's `CronJob` pods.

### Verify

```bash
cronix version
```

## App SDK

### TypeScript

```bash
pnpm add @awbx/cronix-sdk
```

For frameworks that don't speak Web Fetch natively, install the matching sibling adapter:

```bash
pnpm add @awbx/cronix-adapter-express
pnpm add @awbx/cronix-adapter-fastify
pnpm add @awbx/cronix-adapter-koa
pnpm add @awbx/cronix-adapter-nest
```

Hono, Bun, Workers, Vercel/Next.js, and Deno serve a Web `Request` natively — no adapter needed.

### Go

The Go module ships HMAC verification only — your app handles the schedule registration itself:

```bash
go get github.com/awbx/cronix/go/pkg/cronsdk
```

Future SDKs (Python, Ruby, …) will get their own packages; the `spec/` directory is the cross-language correctness contract.
