# Governance

cronix is an open-source project. This document describes how it is governed.

## Roles

### Users

Anyone using cronix. Users contribute by filing issues, sharing real-world
configurations, and adding themselves to [`ADOPTERS.md`](./ADOPTERS.md).

### Contributors

Anyone who has submitted a pull request, opened a substantive issue, or
otherwise materially helped the project. Contributors are recognized in
the release notes for the release that shipped their work.

### Maintainers

Maintainers have write access to the repository and own the project's
direction. The current list lives in [`MAINTAINERS.md`](./MAINTAINERS.md).

Maintainer responsibilities:

- Reviewing and merging pull requests
- Triaging issues
- Cutting releases
- Owning the spec — moving `## Q-NNN:` open questions to `## D-NNN:`
  decisions
- Upholding the [Code of Conduct](./CODE_OF_CONDUCT.md)

### Becoming a maintainer

A contributor becomes a maintainer when:

1. They have landed 5+ substantive pull requests over 3+ months
2. They have demonstrated familiarity with the RFC and the conformance
   vectors
3. An existing maintainer nominates them and the other maintainers do not
   object within 7 days (lazy consensus)

The nominating maintainer opens a PR to `MAINTAINERS.md`. Merging the PR
formalizes the change.

### Stepping down / removal

A maintainer may step down at any time by opening a PR removing themselves
from `MAINTAINERS.md`.

An inactive maintainer (no review or commit activity for 6 months and no
response to an inquiry from another maintainer) may be moved to "emeritus"
status by lazy consensus of the remaining active maintainers.

## Decision-making

cronix uses **lazy consensus**: if no maintainer objects within 7 days of a
proposal, the proposal carries.

### Code changes

Regular code changes go via pull request. One maintainer approval is
sufficient for non-RFC code. The author may not self-approve.

### Spec changes

Anything that touches manifest shape, header format, signing scheme, or
backend semantics is a **spec change**. Spec changes follow the RFC
process:

1. Open a `## Q-NNN:` entry in [`spec/OPEN_QUESTIONS.md`](./spec/OPEN_QUESTIONS.md)
   with the question, options, and current leaning.
2. Discuss in the issue or pull request.
3. When settled, the question is promoted to a `## D-NNN:` entry in
   [`spec/DECISIONS.md`](./spec/DECISIONS.md). The promotion PR also lands
   the code that enforces the decision and any new conformance vectors.

Spec changes require approval from **two** maintainers (or, if there is
only one maintainer, the maintainer plus a 7-day public comment period
with no unaddressed objections).

### Breaking changes

Breaking changes to the on-the-wire contract (`version: 1`) are not made.
The next breaking version of the manifest will be `version: 2`, served
alongside `version: 1` from the same endpoint, not as a replacement.

## Release process

Maintainers cut releases by tagging `vX.Y.Z` on `main`. The release
workflow handles publish (GitHub releases, Homebrew tap, npm,
container registry, deb/rpm/apk).

Pre-release versions (`-rc.N`, `-beta.N`) are permitted for milestone
gating. See [`ROADMAP.md`](./ROADMAP.md).

## Conflict resolution

If maintainers cannot reach consensus on a question, the contested issue is
discussed in a dedicated GitHub Discussion. If discussion does not resolve
it, a simple majority vote of active maintainers decides. In case of a tie,
the maintainer with the longest tenure breaks it.

For Code-of-Conduct issues, see [`CODE_OF_CONDUCT.md`](./CODE_OF_CONDUCT.md).

## Code of Conduct enforcement

Reports go to **asabani.work@gmail.com** (the project's primary
maintainer). All maintainers are required to act on reports within 72
hours, even if "act on" means acknowledging receipt and explaining
escalation.

## Licensing

cronix is licensed under the [Apache License, Version 2.0](./LICENSE).
Releases prior to v1.0.0 were distributed under the MIT License; see
[`LICENSE-MIT`](./LICENSE-MIT) for the historical text and `D-036` in
[`spec/DECISIONS.md`](./spec/DECISIONS.md) for the relicensing decision.

Contributors retain copyright on their contributions; the project does
not require a CLA. Every commit must be DCO-signed (see below).

### License changes

While the project has external contributors with copyright in the
codebase (verifiable via `git shortlog -sne`), license changes require
unanimous maintainer agreement plus a 30-day public comment window.

While the project is sole-authored (`git shortlog -sne` shows a single
committer), the sole maintainer may relicense at will, with the
decision recorded as a `D-NNN` entry in `spec/DECISIONS.md`. This
clause exists because a public comment window with no external
copyright holders is theatre, not governance.

A move *away* from an OSI-approved license is not permitted under this
clause.

## DCO

All commits must be signed off per the [Developer Certificate of Origin](https://developercertificate.org/).
Add `Signed-off-by: Your Name <you@example.com>` to commit messages
(`git commit -s`). The DCO check in CI enforces this.
