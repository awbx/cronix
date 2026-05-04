# Security

## Reporting a vulnerability

If you find a security issue in cronix — particularly anything that:

- weakens HMAC-SHA256 sign or verify (timing attacks, key extraction, signature forgery),
- breaks the constant-time-comparison invariant,
- allows a manifest to install or trigger jobs the operator didn't intend,
- escapes the reconciler's "never modify unmanaged entries" guarantee,

… please report it privately rather than filing a public issue.

**Email**: abellaismail@gmail.com (PGP key on request).

Include:

- Affected version (`cronix version` for binaries, `@cronix/sdk` version for the TS SDK).
- Reproduction steps or proof of concept.
- Your suggested severity assessment.

We will acknowledge within 72 hours, work with you on a fix, and credit you in the release notes (unless you prefer otherwise).

## Scope

In-scope:

- The reference Go binary (`cmd/cronix`).
- The reference TypeScript SDK (`@cronix/sdk`).
- The on-the-wire spec (HMAC scheme, manifest shape) — design weaknesses count.

Out of scope:

- Bugs in third-party Cron implementations cronix shells out to (`cron`, `systemd-timer`, Kubernetes API).
- Operator misconfiguration (e.g. committing secrets to git, setting `concurrency: Allow` on a non-idempotent job).
- DoS by repeated invalid signed requests — apps should rate-limit at the framework layer.

## Cryptographic posture

- Algorithm: HMAC-SHA256 only (D-014). No algorithm negotiation.
- Comparison: constant-time. Go uses `crypto/subtle.ConstantTimeCompare`; TypeScript uses a manual XOR loop because not every Web-Crypto runtime exposes a constant-time helper.
- Replay protection: timestamp + receiver-configurable skew window (default 300s, D-017).
- Key rotation: multi-secret manifests + verifier acceptance of the first matching secret (D-019).

CI greps both languages for loose comparison adjacent to HMAC values and fails on a hit. If you find a path where this grep can be bypassed, that's a vulnerability — report it.
