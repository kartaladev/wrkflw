# 64. Rename SLA concept to Deadline across the public API and wire format

- Status: Accepted
- Date: 2026-06-26

## Context

The workflow engine originally used the term "SLA" (Service Level Agreement) for the
mechanism that fires an alternative process path when an activity is not completed within a
configured duration. The term "SLA" is a business/contract concept; the engine's role is
purely mechanical — it schedules a timer and, on expiry, routes the token to an alternative
flow. "Deadline" is the accurate technical term for that mechanism.

Using "SLA" throughout the public API, internal identifiers, and wire format created
confusion: callers who do not model contractual service levels still needed to use SLA-named
symbols for basic timeout routing. The term also polluted error messages (e.g.
`"SLA breach: SLAFlow %q not found"`) with business jargon that has no place in a
library error string.

The library is pre-consumer (no production data persisted under the old wire keys) so a
breaking wire-format change is acceptable.

## Decision

We rename the SLA concept to Deadline everywhere:

### Exported Go API

| Old | New |
|---|---|
| `WithICESLA(dur, flow, action)` | `WithICEDeadline(dur, flow, action)` |
| `WithSLA(dur, flow, action)` | `WithDeadline(dur, flow, action)` |
| `SLAOf(node)` | `DeadlineOf(node)` |
| `activityFields.SLADuration` | `activityFields.DeadlineDuration` |
| `activityFields.SLAFlow` | `activityFields.DeadlineFlow` |
| `activityFields.SLAAction` | `activityFields.DeadlineAction` |
| `IntermediateCatchEvent.SLADuration` | `IntermediateCatchEvent.DeadlineDuration` |
| `IntermediateCatchEvent.SLAFlow` | `IntermediateCatchEvent.DeadlineFlow` |
| `IntermediateCatchEvent.SLAAction` | `IntermediateCatchEvent.DeadlineAction` |
| `TimerSLA` (TimerKind constant) | `TimerDeadline` |

### Wire / YAML struct-tag keys (breaking)

| Old JSON/YAML key | New JSON/YAML key |
|---|---|
| `slaDuration` | `deadlineDuration` |
| `slaFlow` | `deadlineFlow` |
| `slaAction` | `deadlineAction` |

### Internal identifiers

Internal function names (`handleSLAFired` → `handleDeadlineFired`), internal struct types
(`iceSLAOpt` → `iceDeadlineOpt`), and all local variable names containing `sla`/`SLA` are
renamed to `deadline`/`Deadline` for consistency.

### False positives not touched

- `internal/expreval/expreval_test.go`: `slaSeconds` is an unrelated test-fixture variable
  (an expression environment key in an example expression), not a part of our API.
- `github.com/ThreeDotsLabs/watermill` and the identifier `ThreeDotsLabs`: the substring
  "sLab" is a coincidental match in the watermill import path.
- Historical ADR narratives that introduced the SLA concept: ADRs are immutable
  point-in-time records; their wording is preserved as history.

## Consequences

- **Breaking Go API change**: all callers using `WithSLA`, `WithICESLA`, `SLAOf`,
  `activityFields.SLADuration/SLAFlow/SLAAction`, `IntermediateCatchEvent.SLADuration/SLAFlow/SLAAction`,
  or `TimerSLA` must update to the new names. The library is pre-consumer, so no external
  callers exist at this time.
- **Breaking wire/YAML format change**: process definitions persisted as JSON (JSONB in
  PostgreSQL) or YAML using the old `slaDuration`, `slaFlow`, `slaAction` keys will not
  decode the deadline fields after this change. No production data exists under the old keys
  at the time of this ADR; this is accepted.
- **Improved clarity**: error messages, log output, and the public API now use the neutral
  technical term "deadline", making the engine more accessible to consumers who do not model
  SLA-style business contracts.
- **Existing tests continue to pass**: all unit and integration tests were updated to use
  the new names; the rename is behavior-preserving (logic unchanged).
