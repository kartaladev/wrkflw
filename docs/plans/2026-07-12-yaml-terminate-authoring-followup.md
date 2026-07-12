# Follow-up ticket: YAML authoring of force-termination end events

- Filed: 2026-07-12 (during ADR-0127 delivery)
- Status: OPEN — low priority
- Area: `definition/model` (YAML authoring form)
- Related: ADR-0119 (force-termination), ADR-0127 (unified end/error event)

## Summary

Force-termination end events cannot be fully authored in **YAML**. The
`endBehavior: "terminate"` discriminator is accepted, but the terminate
**payload** — `terminationReason` and `terminationOutcome` — has no field on the
YAML authoring struct, so it is silently dropped. A terminate end authored in
YAML always decodes to `Outcome == OutcomeComplete` with an empty reason.

Error end events are unaffected: after ADR-0127 they are fully YAML-authorable
(`endBehavior: "error"` + `errorCode`).

## Root cause

`nodeYAML` (`definition/model/yaml.go`) mirrors a subset of `NodeWire`. It carries
`ErrorCode` and (as of ADR-0127) `EndBehavior`, but it never carried
`TerminationReason` / `TerminationOutcome` — ADR-0119 added those to `NodeWire`
(JSON) only, not to the YAML struct. `fromNodeYAML` therefore cannot populate
them.

This is **pre-existing** (since ADR-0119); ADR-0127 neither introduced nor fixed
it. Go and JSON authoring of terminate ends work correctly. The gap is YAML-only.

## Impact

- Low. Terminate ends are authorable via Go (`WithForceTermination`) and via JSON
  wire. Only the YAML authoring path is lossy.
- A YAML author writing `endBehavior: "terminate"` gets a working terminate end,
  but cannot choose `abort` (always `complete`) and cannot set a reason.
- Silent: no error is raised, so the loss is easy to miss.

## Proposed fix (when prioritized)

1. Add to `nodeYAML` (`definition/model/yaml.go`):
   ```go
   TerminationReason  string `yaml:"terminationReason,omitempty"`
   TerminationOutcome string `yaml:"terminationOutcome,omitempty"`
   ```
2. Copy both into `NodeWire` in `fromNodeYAML` (alongside the existing
   `EndBehavior: ny.EndBehavior` line).
3. TDD: a YAML round-trip test authoring
   `endBehavior: "terminate"` + `terminationReason: "..."` +
   `terminationOutcome: "abort"` and asserting the decoded `EndEvent` has
   `Behavior == EndTerminate`, the reason, and `Outcome == OutcomeAbort`.
   Add a mirror case for `terminationOutcome: "complete"`.
4. Consider a validation WARN (or the existing `forceTerminationWarnings` path)
   if `endBehavior: "terminate"` is written without an outcome, to surface the
   default rather than silently choosing `complete`.

## Non-goals

- No change to Go/JSON authoring (already correct).
- No new end-event definitions.
