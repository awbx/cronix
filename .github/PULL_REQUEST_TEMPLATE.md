<!--
Thanks for sending a pull request. A few things before you hit "Create":

- Spec change? Make sure a Q-NNN entry exists in spec/OPEN_QUESTIONS.md (or
  this PR is the Q→D promotion). Spec changes need conformance vectors.
- All commits must be DCO signed off (`git commit -s`). The DCO check
  blocks merge otherwise.
- Both languages stay in lock-step. If you touch manifest shape, header
  format, or signing, land TypeScript and Go in this PR.
-->

## What this PR does

<!-- One-paragraph description. Link the issue this closes with "Closes #N". -->

## Why

<!-- Motivation. If this is a spec change, link the Q-NNN or D-NNN entry. -->

## How

<!-- Implementation notes worth calling out for the reviewer. -->

## Checklist

- [ ] Conventional Commit title (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, …)
- [ ] All commits DCO signed off (`git commit -s`)
- [ ] Tests added or updated; coverage does not drop
- [ ] If spec change: `spec/OPEN_QUESTIONS.md` → `spec/DECISIONS.md` promotion done, and conformance vectors updated
- [ ] If TS surface change: `pnpm changeset` ran
- [ ] If new backend: `Backend` interface implemented, integration test added, RFC §Backend Fidelity Matrix updated
- [ ] Docs updated (`docs-site/`, README, RFC) if user-visible behavior changed
