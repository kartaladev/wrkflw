# 182. Reject anchor-independent never-due triggers at definition validation

- Status: Proposed (audited 2026-08-14; audit findings adjudicated and folded)
- Date: 2026-08-13, revised 2026-08-14

> Design and every measurement:
> [`docs/specs/2026-08-13-never-due-gate-and-orphan-reclamation.md`](../specs/2026-08-13-never-due-gate-and-orphan-reclamation.md) §3.
> Premise evidence: `docs/specs/2026-08-13-adr-0181-0182-premise-evidence.md`.
> Audit adjudication: `docs/specs/2026-08-13-adr-0181-0182-audit-adjudication.md`.
>
> Closes the first of backlog **22**'s pieces. ⚠ Two others are **re-deferred** — see ADR-0176 and
> the spec §5. ⚠ **Breaking for authoring — including every boot that re-parses YAML.**

## Context

Executed: **nothing rejects a never-due timer at `model.Validate` today**, root or nested —
`Monthly(12,[31])`, `Cron("0 0 30 2 *")` and `Every(0)` all validate clean.

Three structural facts, each established by building or running rather than by reading:

- **`model → event` is a real import cycle** (`go build` EXIT=1), so `model` cannot see
  `event.*.Timer` and no accessor exposes it. ⚠ Note `model → scheduler` is **not** a cycle
  (EXIT=0) — the reason the rejected alternative below was even a candidate.
- **The wire form avoids the cycle**: the node's trigger fields are `model` types, and a nested
  `event`-package node's timer appears in `model`-driven output with no `event` import.
- **A rule outside `validateStructure` exempts every nested sub-process** — demonstrated with the
  one rule that actually sits in `Validate`: root `Version=0` errors, nested `Version=0` returns
  `nil`. A rule inside `validateStructure` reaches nested sub-processes, wrapped with the host id.

And the constraint that caps the decision (ADR-0176 measurements §9, still standing): a static
`Validate()` can only be **sound, never complete**. Measured at five anchors, `Monthly(12,[31])` is
never-due at three and due at two. **A deploy-time gate cannot replace the arm-time guard; it adds
early authoring feedback for the anchor-independent class only.**

## Decision

A **standalone predicate over `schedule.TriggerSpec`**, evaluated inside `validateStructure`.

⚠ The wire fields are `*model.TriggerWire`, **not** `schedule.TriggerSpec`. The gate reads **two**
of the node's three trigger-carrying field pairs:

| field pair | how the gate reads it |
|---|---|
| `TimerTrigger` / `TimerDuration` | `model.ReadTrigger(w.TimerTrigger, w.TimerDuration, false)` — there is no `TimerOf` accessor, because `model → event` is a real import cycle |
| `WaitTrigger` / `WaitEvery` | **`model.WaitActionOf(n)`** |

⚠ **Corrected during implementation.** This ADR originally prescribed
`ReadTrigger(w.WaitTrigger, w.WaitEvery, true)` for the in-wait half. That is the *same* decode —
`NodeWire.Wait()` is literally that expression — but `WaitActionOf` is the better choice, and for a
reason the design did not have: **`engine.armWaitReminder` reads `model.WaitActionOf(node)`**
(`engine/step_nodes.go:690`). Using the identical accessor means the gate and the production arm
path cannot disagree about *which spec* is being judged, which is the same drift argument that
governs the cross-check. It also matches the neighbouring deadline rule's use of `DeadlineOf(n)`.
Measured: `WaitActionOf` is populated on wire-reconstructed nodes (a JSON round-trip of a UserTask
built with `WithWaitAction(Every(0), …)` returns `zero=false neverdue=true`).

The sentinel is `model.ErrTriggerNeverDue` (`workflow-definition: trigger can never fire`), wrapped
per field as `": timer trigger on node %q"` / `": in-wait trigger on node %q"` — one sentinel so a
consumer's `errors.Is` covers both, with the field named in the message rather than in the sentinel.

### The rejection list, as executed branches

A prose enumeration of a code path's branches rots — the audit found the first draft of this list
wrong in six places at once, in **both** directions. It is therefore stated as the closed set of
`!ok` branches reachable from a `schedule.TriggerSpec` through `convertTrigger`, each verified at
five anchors through the production chain:

| class | exact predicate |
|---|---|
| non-positive `Every` | `dur <= 0` |
| `EveryRandom` bounds | `min <= 0 \|\| min >= max` |
| zero calendar interval | `interval == 0` for `Daily`, `Weekly` **and `Monthly`** |
| out-of-range at-time | `Hour > 23 \|\| Minute > 59 \|\| Second > 59` |
| out-of-range day-of-month | `d == 0 \|\| d > 31 \|\| d < -31`, for any day in the set |
| all-negative weekday set | `len(weekdays) > 0 && every w < 0` |

⚠ **Must NOT be rejected** — each measured due, each a soundness guard with a test:
`Monthly(12,[31])` (anchor-dependent); `Monthly(1,[-1])` and `Monthly(1,[-31])` (a negative day
counts back from month end); `Weekly(1,[Weekday(7)])` and `Weekly(1,[Weekday(9)])` (a weekday above
Saturday stays a raw day offset and always matches on the first pass);
`Weekly(1,[Weekday(-1), Monday])` (a **mixed** set is due — hence *all* negative, never *any*);
`Weekly(1,nil)` and `Monthly(1,nil)` (empty sets are substituted).

Two of these — out-of-range weekday, and negative day-of-month — were in the pre-audit reject list.
Rejecting them is the **ADR-0165 inverted-predicate shape verbatim**: a gate that refuses
definitions which fire.

### Cron is out of scope

Both cron never-due causes — an unparseable expression, and a parseable one matching no instant
(robfig searches five years, then returns the zero time with no error) — require `definition/model`
to import `github.com/robfig/cron/v3`, which it does not today. The owner declined that dependency,
so **the gate covers no cron class**. ⚠ Consequently `Cron("0 0 30 2 *")` is **no longer cited as
this gate's motivation**; it is a measured, documented incompleteness that reaches ADR-0176's arm
guard and nothing earlier. The motivating examples are `Every(0)`, `Daily(0)`, `Monthly(0, …)`.

### `WaitEvery` is in scope; the flat string forms cannot be

`armWaitReminder` (`engine/step_nodes.go:689`) emits `ScheduleTimer{Kind: TimerInWait}` from
`WaitEvery` — the identical durable-arm path the gate exists to guard — so it gets the same
predicate. ⚠ The **legacy flat string forms** decode via `ReadTrigger` to `AfterExpr`/`EveryExpr`,
expression kinds the engine resolves at run time; they are **not statically judgeable** and the arm
guard remains their only layer.

### Deadline triggers stay out, with the executed reason

The recurring half is already rejected at `validate.go:656` — ⚠ **inside `validateStructure`**, not
in `Build()` as ADR-0176 words it. The non-recurring half needs its reason recorded, because an
auditor re-derived it as a finding and then retracted it: `schedule.At(time.Time{})` *looks*
never-due, but `convertTrigger`'s `KindOneTime` branch guards on `AbsTime()` — `ok=false` for a zero
instant — so it converts to `scheduler.After(0)`, which is **due**. **There is no never-due one-shot
deadline reachable through the model.**

### Rejected alternative: asking `scheduler.Trigger.Next`

The authoritative answer would need `convertTrigger` — which lives in `runtime`, and
`model → runtime` is a measured cycle — extracted into a package `model` can import. That is what
the recon pass recommended. It is rejected on an architectural ground the recon pass did not have:
**ADR-0134 left spinning `scheduler` out into an independent module as an explicitly out-of-scope
follow-up**, and locked its preconditions with `scheduler/selfcontainment_guard_test.go`. This
decision assumes that follow-up is still intended. Making `definition/model` depend on `scheduler`
means that, after the split, the definition layer needs an **external module** to validate a
definition. The duplication is the cheaper coupling.

⚠ Note the guard's direction: `TestSchedulerTreeIsSelfContained` is **outbound-only**
(`scheduler → wrkflw/*`). It is what makes a shared calendar-math package impossible; it says
nothing about `definition/model → scheduler` and must not be cited as if it did.

## Consequences

**Positive.** An author learns at build time that a trigger can never fire, instead of discovering
a parked instance later. The rule reaches nested sub-processes — including event sub-processes —
because of where it is placed.

**Negative / accepted.**

- ⚠ **A second source of truth that can drift from the scheduler** — precisely the class ADR-0176
  spent a delivery eliminating. Mitigated, not eliminated, by a cross-check that asserts the **one
  direction** soundness permits: `predicate(spec) ⟹ Next is !ok at every probed anchor`.
  Completeness is deliberately not asserted; the converse is false, so a two-way assertion would be
  unsatisfiable rather than merely strict.
  ⚠ The cross-check lives in **`package runtime`** (internal test), not `package model_test`. Only
  there are the real unexported `convertTrigger` **and** `Trigger.Next` in scope, so the chain under
  test is the production chain; a `model_test` version would hand-roll the conversion — a third copy,
  blind to conversion drift by construction. It also survives the scheduler spin-out, where "the test
  must move with the scheduler" named a home that would exist in **neither** repo.
- ⚠ **Breaking for authoring, and "authoring" includes boot.** `Validate` has exactly one production
  caller, `definitionCore.build()`, reached by both `NewBuilder(...).Build()` and
  `NewLoader(yaml).Build()` — and `ParseYAML` returns a `DefinitionLoader`. Stored definitions are
  `json.Unmarshal`ed with no validation and keep loading, but **a consumer that parses YAML at
  process start fails at start**. Needs a release note.
- The gate is **sound but incomplete by construction**, and says so. It is not defence-in-depth for
  the anchor-dependent class, for cron, or for the flat expression forms; ADR-0176's arm guard
  remains the only layer that catches those. It also does not recurse into a `KindCallActivity`'s
  referenced definition — that definition is validated when it is itself built.
