# 16. Retry exhaustion: Catch-flow → error-boundary → Incident

- Status: Accepted
- Date: 2026-06-21

## Context

ADR-0015 adds an engine-modeled retry executor. A retry budget eventually runs out (attempts
exhausted, time budget exceeded, or a non-retryable error). What happens to the token then is a
distinct decision with real product consequences:

- **Fail the instance** (today's default for an unhandled `ActionFailed`) is destructive — a
  single transient-but-persistent failure tears down the whole instance, losing in-flight parallel
  work and any chance of in-place recovery.
- **Route to an alternative path** (AWS Step Functions "Catch": after `Retry` exhausts, a matching
  `Catch` sends the token to an alternative state with the error injected) lets a definition author
  declare automatic recovery. This matches the existing engine feature set (`SLAFlow` already
  routes a token down a named flow on SLA breach) and the project requirement "on breach, run
  alternative action(s) then take an alternative path".
- **Raise an incident** (Camunda: retries hit zero → a blocking incident; the token is *retained
  in place*, the instance keeps running, an admin diagnoses and bumps retries to resume) is the
  non-destructive, debuggable option and the one the user selected.

These are not mutually exclusive — Step Functions runs `Retry` then `Catch`; Camunda raises an
incident when no handler exists. The engine already has an orthogonal error-boundary feature
(`propagateError`) that must not regress.

## Decision

On a **terminal** action failure (non-retryable, or budget exhausted per ADR-0015), `Step`
applies a fixed precedence:

1. **Catch-flow.** If `Node.RecoveryFlow` (a sequence-flow ID, mirroring `SLAFlow`) is set, inject
   `_error` / `_errorMessage` / `_errorAttempts` into the instance variables (ResultPath-style)
   and route the token down that flow. Definition-authored automatic recovery.
2. **Error boundary.** Otherwise call the existing `propagateError` — an armed error-boundary
   event catches as it does today. This preserves the existing feature unchanged.
3. **Incident.** Otherwise append an `engine.Incident` (id, token, node, scope, last command id,
   error, attempts, time), move the token to a new `TokenIncident` state, and **leave the instance
   `StatusRunning`** — it is *stuck pending intervention*, not failed. Other tokens keep running.
   An admin resumes via a new `ResolveIncident{IncidentID, AddAttempts}` trigger, which removes the
   incident, re-grants attempt budget, and re-invokes the action.

Crucially, steps 1–3 are reached **only when an effective `RetryPolicy` exists** (ADR-0015's
opt-in gate). A legacy action with no policy never enters this path — it keeps hitting
`propagateError` / `StatusFailed` verbatim. Incidents therefore cannot change the behaviour of any
existing definition; they are a property of the retry subsystem.

`model.Validate` gains `ErrInvalidRecoveryFlow` (the referenced flow must exist and originate at
the node). `cloneState` deep-copies `Incidents`. `ResolveIncident` on an unknown id is an
idempotent no-op (late/duplicate admin actions are safe).

## Consequences

**Easier:** definitions get two recovery affordances — automatic (catch-flow) and manual
(incident) — without any destructive default. A poison action parks one token as a visible,
persisted incident that admin/monitoring can surface and resolve, while the rest of the instance
keeps executing. Reuses `SLAFlow`-style routing and the existing `propagateError`, so the new
surface is small. Instance-failure semantics for *retry-less* flows are untouched.

**Harder / trade-offs:** a new `TokenState` (`TokenIncident`), a new trigger
(`ResolveIncident`), and an `Incidents` slice enlarge the sealed sets and the snapshot, each
needing codec + `cloneState` + monitoring plumbing. An instance with an unresolved incident sits
`StatusRunning` indefinitely until an admin acts — operators must monitor `IncidentCount`, or such
instances leak. The catch-flow's `_error*` variable injection is a documented, reserved namespace
that could collide with consumer variables (mitigated by the underscore prefix convention).
Authorization on `ResolveIncident` is left to the transport/admin gate in v1 (no per-incident
casbin rule yet).
