# Open Questions

Format: each entry is a `## Q-NNN: <question>` heading with `Raised`,
`Phase`, `Context`, `Options`, `Currently leaning`. When resolved, move
to `DECISIONS.md` with the same number prefix (Q-007 → D-029, etc. — pick
the next free `D-` number).

---

*(No currently open questions.)*

---

## Resolved

### Q-001: OpenTelemetry trace shape for `cronix trigger` → [D-037](./DECISIONS.md#d-037-opentelemetry-trace-shape-for-cronix-trigger)

Raised: 2026-05-19. Resolved: 2026-05-19. The trace shape, attribute
set, and propagation semantics for the per-fire OTel emission. Three
alternatives considered (single-span, span-per-attempt, span-per-step);
B + conditional lock-span chosen. See D-037 for the locked spec.
