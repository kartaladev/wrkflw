# Rename `ProcessDriver.Deliver` → `ApplyTrigger`

Date: 2026-07-08
Status: Approved (design) — pending implementation plan
Scope: single public-method rename, behavior-preserving

## Context

`ProcessDriver.Deliver(ctx, def, instanceID, trg engine.Trigger)` is the low-level primitive
that applies **one** trigger to **one** existing instance: Load → `engine.Step` → Save → record
journal → perform commands. `Drive` (start/advance a new instance with variables) is its sibling
entry point; `DeliverMessage` and `BroadcastSignal` are ergonomic facades that build a concrete
`Trigger` and call this primitive.

The name `Deliver` is confusing on two counts (user-confirmed, both apply):

1. **Collides with `DeliverMessage`.** They read as siblings, but `Deliver` is the generic
   primitive that `DeliverMessage`/`BroadcastSignal` are built on. The name hides that hierarchy.
2. **Misdescribes the operation.** It does not deliver to a mailbox; it *applies a trigger to the
   instance's state machine*. The verb should name that effect.

Decision (approved): rename to **`ApplyTrigger`** — the most explicit, self-documenting name; the
verb agrees with the `engine.Trigger` parameter and makes the facade hierarchy obvious. Runner-up
`Advance` was rejected for not conveying that the call takes a trigger.

Migration style (approved): **hard rename, no deprecated alias**, matching this repo's history
(`Run`→`Drive`, `NewCatch`→`NewIntermediateCatch` were hard renames). The module is pre-1.0.

## Decision

Rename the method and every reference to it to `ApplyTrigger`, with an identical signature and
identical behavior. Add ADR-0107 recording the decision.

Resulting API shape:

```
Drive(ctx, def, id, vars)                     // start/advance a NEW instance with variables
ApplyTrigger(ctx, def, id, trg)               // advance an EXISTING instance with a raw trigger  ← primitives
DeliverMessage(ctx, def, name, key, payload)  // facade → builds MessageReceived → ApplyTrigger
BroadcastSignal(ctx, name, payload)           // facade → Publish → ApplyTrigger per waiter
```

## Scope (what changes)

- **Method definition** — `runtime/processdriver.go:360`.
- **Tracer span name** — `"wrkflw.runner.Deliver"` → `"wrkflw.runner.ApplyTrigger"`
  (`processdriver.go:361`), so traces stay consistent with the method name.
- **Production call sites** — `processdriver_cancel.go` (×2), `processdriver_message.go`,
  `processdriver_incident.go`, `timerops.go`, `service/service.go` (×2).
- **processtest call sites (the METHOD, not the Decision)** — `harness.go` (×2), `drive.go`
  (`h.driver.Deliver(`, `e.h.driver.Deliver(`, `e.driver.Deliver(`).
- **Examples** — signal_broadcast, sqlite_wiring, mysql_wiring, usertask_approval,
  inwait_reminder, attribute_authz, compensation_saga (call sites + method-referring comments).
- **Godoc usage examples** — `signal/signalbus.go`, `calllink/notifier.go`,
  `persistence/persistence.go` (`runner.Deliver(...)` snippets), and the `Drive/Deliver` prose in
  `processdriver.go`.
- **Tests** — the method call sites and method-referring comments across the 11 `_test.go` files
  that reference it.
- **New `docs/adr/0107-rename-deliver-to-apply-trigger.md`**.

## CRITICAL constraint — do NOT touch the `processtest.Deliver` collision

`processtest` exports its **own unrelated** `Deliver` — a `ParkHandler` `Decision` constructor
(`processtest/drive.go:62`, `func Deliver(trigger engine.Trigger) Decision`). It must remain
`Deliver`. A whole-word `\bDeliver\b` replace is therefore **forbidden**; it would corrupt:

- the constructor definition `processtest/drive.go:62`;
- its bare calls `Deliver(...)` in `processtest/handlers.go:72,78,93,108`;
- its **package-qualified** call `processtest.Deliver(...)` in `processtest/drive_test.go:60`
  (note: this matches a naive `.Deliver(` pattern — it must be excluded);
- its doc references `[Deliver]` in `processtest/doc.go:27` and `processtest/drive.go:53`.

Disambiguation rule: the METHOD is always invoked on a ProcessDriver receiver named `driver` or
`runner` (possibly prefixed: `h.driver`, `e.driver`, `e.h.driver`). The Decision constructor is
invoked bare (`Deliver(`) or qualified by the package (`processtest.Deliver(`). Rename only the
receiver-qualified `driver.Deliver(` / `runner.Deliver(` forms plus the method definition and span
string; never the bare or `processtest.`-qualified forms.

## Non-goals

- **Historical ADRs/plans** (`docs/adr/0011,0019,0020,0028,0048,0105,0106`, `docs/plans/*`) are
  point-in-time records and are left unchanged; ADR-0107 supersedes the naming.
- No signature, behavior, parameter, or return change. No new facades.
- `DeliverMessage`, `BroadcastSignal`, `DeliverFunc` (signal), and `processtest.Deliver` keep
  their names.

## Execution approach

No `gopls`/`gorename` is available, so use **targeted, receiver-scoped** text replacement (never
whole-word):

1. `driver.Deliver(` → `driver.ApplyTrigger(` (catches `h.driver.`, `e.driver.`, `e.h.driver.`).
2. `runner.Deliver(` → `runner.ApplyTrigger(` (godoc snippets).
3. Method definition `) Deliver(` on the `*ProcessDriver` receiver line → `) ApplyTrigger(`.
4. Span string `"wrkflw.runner.Deliver"` → `"wrkflw.runner.ApplyTrigger"`.
5. Method-referring prose comments → `ApplyTrigger`, **excluding** `processtest`'s `[Deliver]`
   Decision references (`processtest/doc.go`, `processtest/drive.go`, and prose that clearly means
   the Decision).

After replacement, grep-audit that the only remaining whole-word `Deliver` in `.go` are:
`DeliverMessage`, `DeliverFunc`, and the `processtest.Deliver` Decision (definition, calls, docs).

## Verification checklist

- [ ] `go build ./...` clean.
- [ ] `go vet ./...` clean.
- [ ] `go test -race ./...` — 0 failures, 0 races (full suite; existing tests are the safety net
      for this behavior-preserving refactor — no new tests per TDD refactor rule).
- [ ] `golangci-lint run ./...` — 0 issues.
- [ ] The runnable examples still execute (signal_broadcast, usertask_approval, inwait_reminder,
      attribute_authz, compensation_saga, sqlite_wiring).
- [ ] Grep audit: no stray `driver.Deliver(` / `runner.Deliver(` remain; `processtest.Deliver`
      Decision untouched; `"wrkflw.runner.Deliver"` span gone.
- [ ] ADR-0107 written.

## Risks

- **The `processtest.Deliver` collision** (primary risk) — mitigated by receiver-scoped patterns
  and the post-replace grep audit above.
- **Trace dashboards** keyed on the old span name `wrkflw.runner.Deliver` would need updating —
  acceptable for a pre-1.0 library; noted in ADR-0107.

## Parallelization

The code rename is inherently one coherent edit (method + all call sites must change together to
compile), so it is a single serial task. Independent work that can run in parallel with it:
writing ADR-0107. Verification (build/test/lint/examples) runs after the rename. The plan reflects
this: ADR-0107 ∥ code-rename, then a verification fan-out.
