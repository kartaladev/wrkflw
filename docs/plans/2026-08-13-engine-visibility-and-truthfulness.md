# Plan — Engine visibility and truthfulness (ADR-0177, ADR-0178, ADR-0180)

> Spec: [`docs/specs/2026-08-13-engine-visibility-and-truthfulness.md`](../specs/2026-08-13-engine-visibility-and-truthfulness.md)
> ADRs: 0177, 0178, 0180 · Evidence: `docs/specs/2026-08-13-adr-{0177,0178-0180}-premise-evidence.md`
> Audit: `docs/specs/2026-08-13-adr-0177-0180-audit-lens-{a,b,c}.md`

## ▶ Progress

- **Branch**: `feat/engine-visibility-and-truthfulness` (off `main` at `12c9d7e3`; newest code on
  `main` is the ADR-0176 merge `52bf0f80`). One commit, amended per phase — never stacked.
- **State**: ✅ **COMPLETE — P1, P2, P3, P4 all landed; `/code-review` findings folded (P5).** Design
  audited (3 lenses, 27 findings, all accepted) and corrected; implementation corrected the design
  five further times and `/code-review` a sixth, seventh and eighth — all amended in-bundle.
  **⏸ STILL AT the Delivery Gate**: `/security-review` has not run. Do not merge until it has and
  its findings are folded via `--amend`.

**P5 (owner-gate `/code-review`) — landed. Three findings, all accepted, none a false positive.**

1. **HIGH — the dying-timer refusal orphaned a recurring scheduler job.** Retiring the record is
   what stops the terminal sweep (`endInstance` → `cancelAllTimers`, which reads `s.Timers`) from
   ever disarming it, and a `TimerInWait` reminder is armed **recurring**. Measured on
   `dyingTimerDef`: `(A)` fired once while dying → refusal `commands=[]`, terminal
   `cancelTimers=[tm1 tm3]`; `(B)` never fired → `[tm1 tm2 tm3]`. The refusal now emits exactly
   `[CancelTimer{rec.TimerID}]`. ADR-0178 + spec §2/§6.2 amended.
   `TestFiredTimerOnDyingInstanceEmitsNothing` **renamed** to
   `…OnlyDisarmsItsTimer` — the fix made the old name false.
2. **MEDIUM — the ADR-0177 widening flipped a fourth shape and broke `AutoTimers`' contract.**
   ⚠⚠ **The adjudicated fix handed to implementation was wrong and was NOT applied as written.**
   "The timer rung wins when some token has `Token.AwaitTimer != \"\"`" is, executed, *worse than
   the bug*: it reclassifies the **retry-backoff** park (`ReasonTimer` since long before ADR-0177 —
   `HasArmedTimers` already read any non-`TimerCompensationStall` **record**, `engine/state_timers.go`
   at `99da2026^`) and the **event-gateway** park (whose promotion ADR-0177 is what added) down to
   `ReasonAsyncChild`, silently stopping `AutoTimers()` from advancing every retry backoff.
   Shipped instead: a timer outranks a command wait only for a **primary** timer park —
   `processtest.primaryTimerPark`, a set-membership test of a waiting token's `AwaitCommand` against
   `state.TimerWaiters()`'s ids, plus a non-empty `state.TimerArmedEventWaiters()`. Five shapes
   measured and pinned. ADR-0177 + spec §1/§6.1 amended.
3. **LOW — a by-design outcome logged as a failure.** `propagateCancel`'s WARN
   `"cancel child instance failed"` was unconditional, so a dropped cancel
   (`ErrCancelNotApplicable`) — the expected answer for a child owned by an admin partial rollback —
   read as a failure. The WARN moved inside the `!errors.Is(...)` branch; the drop gets its own
   Debug line, `"…child kept its own compensation walk; cancel dropped"`, with `reason` not `error`.
   ADR-0180 + spec §3b amended.

**P5 evidence.** RED observed for findings 1 and 2 before either fix (finding 1: expected
`[CancelTimer{dying-1-tm1}]`, actual `nil`, three of four rows; finding 2: `reason` 5 vs 6 and
decision `kind:2` vs `kind:0`). Five mutations recorded: reverting the refusal to `Commands: nil`
reddens the three dying rows and not the control; and for `primaryTimerPark`, **each clause is
pinned by a different non-empty subset of rows** — bare `HasArmedTimers` reddens the two secondary
rows, dropping the gateway clause reddens the gateway row, dropping the `AwaitCommand` clause
reddens plain-ICE + retry, and restoring the adjudicated `AwaitTimer` form reddens retry + gateway.

**Verification** (Docker up, nothing skipped, judged by exit code, on the bundle tree):
`go test -race ./...` **EXIT=0, no failures**; coverage **74.5 %** repo-wide (baseline 74.2 %);
`golangci-lint run ./...` repo-wide **0 issues**; `go vet ./...` EXIT=0.

**P3 (ADR-0180) — landed.** `ErrInstanceAlreadyStarted` on the two unguarded start entry points,
keyed on `StartedAt.IsZero() && len(Tokens)==0 && len(History)==0`; `ErrCancelNotApplicable` as a
**reporting** outcome returned after propagation. RED for all five cycles, including the runtime
propagation test reproducing the audit's `expected: 4 actual: 0` for child and grandchild. Three
mutations, each pinned by exactly **one** row — the naive predicate reddens only the
zero-`OccurredAt` row; returning the sentinel early reddens parent/child but not grandchild; the
loop's `continue` reddens grandchild but not parent/child.

⚠⚠ **Implementation corrected ADR-0180 three ways** (all amended in-bundle): **422, not 409**
(`ErrConflict`/`ErrInvalidTransition` classify to 422; 409 is `ErrConcurrentUpdate` alone — the ADR,
spec *and* evidence record all said 409); the dropped-cancel site serves **two** situations and the
ADR generalised from the one it measured (an admin *partial* rollback vs ADR-0034's *idempotent
re-cancel*, which must stay silent — gated on `!s.Compensating.walkTerminates(s.PendingCancel)`);
and both sentinels **must wrap `ErrInvalidTransition`** or the driver's answer reaches HTTP as a
**500 with an empty body**. ✅ `transport/http` needed no production edit, as predicted.

**P4 (doc-only) — landed. THIRTEEN false comments, not eight.** The count grew three times: six
pre-audit → eight (counting lens) → nine (P1 found one in `processtest`, which the audit's
`engine`-only enumeration structurally could not see) → thirteen (P4 found four more while editing
the same files, **two of them in production code** making the same claim as the test comments the
audit did catch). *An enumeration of enumerations rots exactly like the enumerations in it.*

**P1 (ADR-0177) — landed.** `TimerWaiter`, `TimerWaiters()`, five accessors, `timerWaitersOf[T,PT]`,
`Token.AwaitTimer` (dual-written, cleared at all seven sites via `Token.clearAwait()`),
`HasArmedTimers()` redefined over `TimerKind.walkScoped()`. New symbols 100% covered.
⚠ Two plan steps were **not RED-able as ordered** (step 9's pins depend on step 8; step 5's
`HasArmedTimers` assertion is vacuous against the old predicate) — covered by mutation instead. If a
later phase reuses that shape, put pin tests **before** the widening.

**P2 (ADR-0178) — landed.** Path-4 guard `!rec.Kind.walkScoped() && !s.spawnsNewWork()`; refused
fires retire the record, WARN, and emit exactly `[CancelTimer{rec.TimerID}]` (⚠ "emit nothing"
until P5 — see above). RED observed for all three kinds (the deadline took
tokens 2 → 1). Mutating to the blanket form reddens **9** tests incl.
`TestStallIncidentIsRaisedOnADyingWalk`; a `Status`-test mutation reddens only the control row.

⚠⚠ **Implementation corrected the audit (rule #11), amended in-bundle:** the prescribed
**cancel-started** fixture **cannot exist** for these three kinds — `beginCompensation`'s prologue
cancels every token and sweeps `s.Timers` (tokens=0, records=0), so path 4 has nothing to fire. The
requirement is *"a walk that **terminates**, asserted"*; the constructible fixture is a **resuming
throw walk carrying a deferred cancel**. ✅ ADR-0178's retry `ASSUMPTION (unverified)` is
**discharged** — measured `InvokeAction{work, …, FireAndForget:false}`.

**Verified after P1** (Docker up, nothing skipped, judged by exit code): `go test -race ./...`
green except `TestPgxNotifierListenDrainsBeforePollInterval` — **pre-existing blocker 5**,
load-flaky, re-run in isolation `EXIT=0`, PASS observed under `-v`. Coverage repo-wide **74.5 %**
(baseline 74.2 %). `golangci-lint run ./...` repo-wide **0 issues**. `go vet ./...` EXIT=0.

⚠ **A NINTH false comment** was found during P1, missed by the audit's eight because §5 enumerates
`engine` only: `processtest/park.go:92-100` still claims the timer kind is not visible outside the
engine package. Add to P4.
- ⚠ **ADR-0179 was SPLIT OUT of this bundle by the audit** — 4 of 6 Criticals landed on it. It
  ships as **bundle C** with its own rule-#9 audit of the rewritten design. This plan no longer
  contains it.
- **Bundle order**: A (this) → B (`feat/never-due-gate-and-orphan-reclamation`, ADR-0181/0182) →
  C (ADR-0179). Packages are disjoint, so order is a merge convenience, not a dependency — except
  that **bundle C must extend `walkScoped()`**, introduced here.

---

## Execution order (STRICTLY SERIAL — no fan-out)

Every phase touches package `engine`. Concurrent agents inside one Go package break each other's
`go test` compile even on disjoint files, so this delivery runs inline in the controller or as one
subagent at a time.

| phase | ADR | package(s) | note |
|---|---|---|---|
| P1 | 0177 | `engine`, `processtest` | — |
| P2 | 0178 | `engine` | independent of P1 |
| P3 | 0180 | `engine`, `runtime`, `service` (+`transport/http`, likely no-op) | ⚠ `runtime` is **not** container-free |
| P4 | doc-only | `engine` | after P1–P3 — the comments describe shipped code |

---

## P1 — ADR-0177: `TimerWaiters()`

1. **RED** — `TestTimerWaiters`, five sub-cases, one per source. **Fails today**: `TimerWaiters`
   does not exist (compile error = valid RED).
   ⚠ Each fixture must declare a **real** arm node. A boundary sub-case whose definition has no
   boundary node asserts nothing — this repo has shipped that trap twice.
2. **GREEN** — `TimerWaiter` struct, `timerWaitersOf[T]` in `state_arms.go`, five accessors,
   `TimerWaiters()`. Deterministic order, `nil` when empty.
3. **RED** — `TestTokenAwaitTimerIsSetOnPlainIntermediateCatch`. **Fails today**: no such field.
4. **GREEN** — add `Token.AwaitTimer`; write it at the plain-ICE arm site. Dual-write; leave
   `AwaitCommand` as is.
5. ⚠⚠ **RED** — `TestTokenAwaitTimerIsClearedOnResume`: fire the timer, resume the token, assert
   `HasArmedTimers()` is **false** afterwards. **This is the audit's CRITICAL**: without it the
   field stays set forever and ADR-0177's purpose inverts. **Fails** against a set-only
   implementation.
6. **GREEN** — one `Token.clearAwait()` helper used at **all seven** `AwaitCommand`-clearing sites
   (`step_gateways.go:243`, `step_timers.go:83`, `step_triggers.go:112/376/569/741/1002`).
   ⚠ `:569` is inside the path-5 fall-through: clearing a field there is not a dispatch change.
7. **RED** — `TestHasArmedTimersSeesEveryArmSource` + the stall-exclusion case. **Fails today**:
   measured `false` for boundary, gateway, ESP and plain-ICE.
8. **GREEN** — redefine `HasArmedTimers()` over `TimerWaiters()`; introduce `TimerKind.walkScoped()`
   covering `TimerCompensationStall`. ⚠ It does **not** exist today — this is an introduction, not
   a reuse.
9. **RED/GREEN** — `processtest` reclassification, **three directions**: (a) timer arm with no
   task/signal/message flips `ReasonAsyncChild` → `ReasonTimer`; (b) boundary timer **+ awaited
   message** stays `ReasonMessage`; (c) boundary timer **+ open user task** stays
   `ReasonHumanTask` — the `openTasks` rung, which (b) does not pin.
10. **RED/GREEN** — the KNOWN LIMITATION pin **and** its complement: a rehydrated pre-change
    snapshot yields no token-source waiter, **and** the same token with `AwaitTimer` set does. The
    second is what genuinely fails before step 4; the first alone is near-vacuous.

**Verify**: `go test -count=1 ./engine/... ./processtest/... ; echo "EXIT=$?"`

## P2 — ADR-0178: the dying-instance guard

1. **RED** — `TestFiredTimerOnDyingInstanceOnlyDisarmsItsTimer`: reminder, deadline, retry. **Fails
   today**: measured, the reminder emits `InvokeAction{remind}`; the deadline emits three commands
   and takes tokens 1 → 0.
   ⚠⚠ **The fixture must be a walk that TERMINATES, asserted** with
   `require.False(t, engine.SpawnsNewWork(&st))` before firing. A plain `driveToScopeWideThrow`
   measures `SpawnsNewWork = TRUE` and yields a test that passes whether or not the guard exists.
   ⚠ **Corrected in-bundle**: the audit prescribed a *cancel-started* walk; executed, that fixture
   cannot exist here — `beginCompensation`'s prologue cancels every token and sweeps `s.Timers`,
   leaving tokens=0 and records=0, so path 4 has nothing to fire. Use a **resuming throw walk
   carrying a deferred cancel** (`PendingCancel=true`), which is what the evidence file measured.
   ✅ The **retry** sub-case's `ASSUMPTION (unverified)` is **discharged** — the fixture was
   constructible and its command is measurably not fire-and-forget.
2. **GREEN** — guard path 4 with `!rec.Kind.walkScoped() && !s.spawnsNewWork()`; retire the record,
   `slog.WarnContext`, no commands.
3. **RED/GREEN** — the exemption: a stall timer still fires on a dying walk. Confirm
   `TestStallIncidentIsRaisedOnADyingWalk` still passes.

**Verify**: `go test -count=1 ./engine/... ; echo "EXIT=$?"`

## P3 — ADR-0180: truthful commands

1. **RED** — `TestSecondStartInstanceIsRefused`, **three rows**: (a) second start refused;
   (b) **control** — a fresh instance still starts; (c) ⚠ **zero-`OccurredAt`** start then a second
   start, refused. **Fails today**: measured `err=<nil>`, tokens 1 → 3. ⚠ Rows (a) and (b) both
   pass under a `StartedAt`-only predicate; **row (c) is the one that fails**, and it is the
   audit's MAJOR.
2. **GREEN** — `ErrInstanceAlreadyStarted`; predicate
   `s.StartedAt.IsZero() && len(s.Tokens) == 0 && len(s.History) == 0`.
3. **RED** — `TestDroppedCancelReportsNotApplicable`, `TestDeferredCancelStillReturnsNil`, **plus
   the two propagation cases**: (a) a parent whose own cancel is dropped still terminates its
   children; (b) a child whose cancel is dropped does not orphan its grandchildren. **All fail
   today** — the first two return `nil`/200, and the propagation pair fails against the naive
   implementation (proved by the audit's mutation).
4. **GREEN** — `ErrCancelNotApplicable` from `:210` only; in `CancelInstance`
   `if err != nil && !errors.Is(err, engine.ErrCancelNotApplicable) { return st, err }` so
   propagation still runs, returning the sentinel after it; in the child loop, log and **recurse
   anyway**; `service` maps to `ErrConflict` → 409. `:196` untouched.

**Verify**: `go test -count=1 ./engine/... ./runtime/... ./service/... ./transport/http/... ; echo "EXIT=$?"`
⚠ `./runtime/...` is **not** container-free — a subagent running this needs Docker stated in its
brief. Then `go vet ./...` (compiles every test file, the cheap proof of no hidden consumer).

## P4 — doc-only

The **eight** false comments in spec §5. ⚠ Row 8
(`step_compensation_stall_incident_test.go:79-88`) is falsified *by this bundle*, so it can only be
corrected after P2. Row 7 (`state_timers.go:14`) and row 4 remain stale for the kind bundle C adds
— note that forward.

---

## Verification checklist

- [ ] Observable **RED** in the transcript before every new symbol. Self-audit: could a reviewer
      reading the conversation see it?
- [ ] Load-bearing tests **mutation-verified** (break, observe RED, restore from a `cp` backup —
      ⚠ never `git checkout <path>`; it restores from the index and has destroyed uncommitted work
      here twice — then `diff`).
- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — ≥ 85 % on
      touched packages, hot paths and their failure branches first. Docker: probe and run (standing
      permission for this run); if down, say so and label any container-free subset as partial.
- [ ] `go test ./...` repo-root — no regressions.
- [ ] `golangci-lint run ./...` **repo-wide** (not `./engine/...`) — clean.
- [ ] Documents describe what shipped; **amend in-bundle** anything implementation refuted, with
      the measurement.
- [ ] Diff's own comments swept for unexecuted claims and over-reaching quantifiers.
- [ ] `HANDOVER.md` rewritten in place; this `▶ Progress` updated; auto-memory updated.
- [ ] **PAUSE** — owner runs `/code-review` and `/security-review`. Fold via `--amend`, re-run on
      the merged tree, merge `--no-ff`, push.

## Traps (the audit's confirmed ones)

1. **`AwaitTimer` set but never cleared** — inverts ADR-0177. P1 steps 5–6.
2. **A throw-walk fixture for the dying-instance guard** — passes regardless of the guard. P2.1.
3. **A `Status`- or `StartedAt`-only start predicate** — P3.1 row (c).
4. **A propagation-halting cancel sentinel** — orphans the child subtree. P3.3–4.
5. **`walkScoped()` treated as pre-existing** — it does not exist; P1.8 introduces it, and bundle C
   must extend it or ADR-0179 silently never works.
