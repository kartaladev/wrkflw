# Plan — `processtest` sees every signal/message waiter source (ADR-0166)

Spec: `docs/specs/2026-08-07-processtest-waiter-enumeration.md`
ADR: `docs/adr/0166-processtest-delegates-waiter-enumeration.md`
Branch: `feat/processtest-waiter-enumeration`

## ▶ Progress

**IMPLEMENTED — phases 1–6 done. NOT YET DELIVERED: the owner-only gate
(`/code-review`, `/security-review`) and the full Docker suite are outstanding.**
Branch `feat/processtest-waiter-enumeration`, base `main` @ `abccb96`.

Rule-#9 audit: two Opus auditors, **17 findings, 17 accepted**. It reversed two
of the four original decisions (D3's `Reason` handling, D4's delivery bound),
rescoped a third (`HasArmedTimers` out, follow-up filed), and deleted one false
claim from the spec's own "measured" section. Three findings were escalated to
the owner and adjudicated. Detail: spec §2.3 and §7.

### ⚠ Implementation refuted D4's fingerprint inputs (rule #11, ADR amended)

The ADR specified the fingerprint as *"the sorted set of token IDs currently
awaiting that name, plus the three arm-slice lengths"*. **Implemented literally,
that breaks row 8 — the case D4 exists to protect.** Probe on
`start → c1("go") → c2("go") → end`:

```
park@c1: token id="d9qkk8983g3l2a2etb3g" node="c1"  boundaries=0 armedEvents=0 evtsubs=0
park@c2: token id="d9qkk8983g3l2a2etb3g" node="c2"  boundaries=0 armedEvents=0 evtsubs=0
```

The token **keeps its ID**; only its node changes. The spec's justification said
"the awaiting token changes (`c1` → `c2`)" — node ids — and the ADR restated that
as token ids, losing the distinction. Corrected in code, ADR and spec:

1. key on sorted **`tokenID@nodeID`** pairs;
2. additionally scope by **`state.InstanceID`** — an arm-derived park has *no*
   awaiting token, so two instances of one definition otherwise share a key and one
   handler value driving both delivers to the first only.

Neither authorship nor the audit caught this; both reasoned about token identity
instead of measuring it.

### ⚠⚠ The pre-delivery review then refuted the fingerprint ENTIRELY (spec §2.6)

Two adversarial reviewers on the committed bundle, before the owner gate. **Five
substantive findings, three of them executed regressions against `main`**, and both
reviewers independently found two of them. The corrected fingerprint was still
wrong three ways: a loop re-enters the *same* node (id **and** node unchanged); the
three arm-slice lengths are **instance-wide**, so the arm's own branch arming
anything re-authorises delivery forever; and one last-key slot **displaces** across
instances rather than colliding.

**Root cause: bounding token catches was never necessary.** A token catch is
*consumed* when it fires and cannot re-match — only an arm can. The bound was
applied to both, and that is what broke loops. Shipped instead:

- token catches **unbounded**;
- arm-derived deliveries bounded **per instance, per waiter-set-size for that
  name**, via `armFireLog` (a mutex-guarded `map[string]int`);
- `parkKey` **deleted**.

Also fixed from the same review: `armDerivedReason` demoted a genuine token
**message** await below a timer (it tested only the await that produced the
reason); `TestHandlersPassOnUnawaitedName` **could not fail** and was rewritten to
assert the `Decision`; and `CompleteTasksWith`'s `memo` map was a **pre-existing
unguarded data race**, fixed here as the same bug class in the same file.

Explicitly checked as NOT a regression: un-bounding token catches leaves a
wrong-correlation-key delivery exactly as it was on `main` — both spin to
`drive step limit exceeded after 1000 steps (last park: message)`, byte-identical.

### What shipped, per phase

| Phase | State | Evidence |
|---|---|---|
| 1 — type change alone | done | RED = type mismatch at `park_test.go:129`; then EXIT=0 |
| 2 — RED suite | done | rows 1–4 `nil` fields *while* `st.SignalWaiters()` asserts non-empty; rows 5–6 `unhandled park: human-task at node "approve"`; row 9 Reason `6`(async-child)→ wanted `3`(signal) |
| 3 — `Classify` delegates | done | rows 1–6, 9 GREEN; whole package GREEN |
| 4 — D3 widening + D4 fingerprint | done | row 7 RED `unhandled park: signal at node "t-catch"`; row 10 RED `drive step limit exceeded after 1000 steps` |
| 5 — mutations | done | 15 mutations, 15 caught (table below) |
| 6 — docs | done | README, CHANGELOG, ADR→Accepted, spec §2.5 |

### Mutation record (phase 5)

Snapshot → mutate → observe RED → restore → `diff` confirmed clean each time.

Mutations M3–M6 targeted the fingerprint and died with it. The set below is against
the **reworked** design; every one was re-run after the rework.

| # | Mutation | Test that must go RED | Observed |
|---|---|---|---|
| M1 | remove signal dedup | duplicate names collapse | `Not equal` |
| M2 | message dedup on name only | same name, different keys | `Not equal` |
| M7 | remove D5's `Node` fallback | arm-derived park names its node | `"evtgw"` → `""` |
| M8 | remove D3's widening | `AutoTimers` beside a live arm | `unhandled park: signal at node "t-catch"` |
| M9 | `armDerivedReason` default → `true` | secondary armed timer | clock `00:00` → `01:00` |
| Ma | bound token catches too | catch loop re-delivers | FAIL |
| Mb | count global arms, not this name | arm's branch arms something | FAIL |
| Mc | one slot instead of per-instance | shared handler determinism | FAIL |
| Md | remove the arm bound entirely | non-interrupting arm terminates | FAIL |
| Me | drop the `armFireLog` mutex | shared handler determinism | `WARNING: DATA RACE` |
| Mf | `armDerivedReason` per-await only | token message await vs timer | FAIL |
| Mg | neuter both name guards | handlers pass on unawaited name | FAIL on **both** halves |
| Mh | drop `CompleteTasksWith`'s memo mutex | shared handler determinism | `WARNING: DATA RACE` |

| Mj | never re-deliver after the first park | two sequential arms both fire | FAIL |
| Mk | record nothing, so every node reads fresh | same-name re-arm does not spin | FAIL |

**15 mutations, 15 caught.** (A 16th, a "count-proxy" variant, was **malformed** —
it never triggered — and is discarded rather than counted. The count-based bound's
real refutation is that both `/code-review` tests were RED against it as shipped.)

⚠ **Three mutation attempts were invalid on the first try and had to be redone** —
each would have been recorded as passing verification:

- **M4's first form deleted only the guard, not `var fp`,** so the package failed
  to *compile*. `EXIT=1` from a build failure is not a RED: it proves nothing about
  the assertion.
- **Mg's first form had the same defect** — `if false` left `slices` unused. Written
  as `<real condition> && false` it compiles and goes genuinely RED. The mutation
  harness now greps for `build failed` / `imported and not used` and prints
  `⚠ INVALID` instead of trusting the exit code.
- **M9's first form could not discriminate.** Asserting `ErrUnhandledPark` +
  `"human-task"` passes under the mutation *too*: a wrong promotion fires the timer,
  the drive advances, and the still-open task parks it anyway with the same error.
  Only asserting that the **clock did not move** distinguishes the two.

### Still-true facts, source-verified at implementation time

- `processtest` is container-free; everything above ran with **no Docker**.
- `go vet ./...` **EXIT=0** repo-wide. This compiles every test file, including
  `runtime`'s (which import `processtest` and cannot be *run* without Docker), so
  the breaking type change has no uncompiled consumer hiding behind the gate.
- `distinctAwaits` is gone — `grep -rn distinctAwaits processtest/` is empty.
- `Park.HasArmedTimers` is **untouched and still one-source**; no document in this
  bundle claims the ADR-0154 class is closed outright.

### Verification commands actually run

```bash
go test -race -count=1 -coverprofile=cover.out ./processtest/...  # EXIT=0, coverage 90.2%
go test -race -count=5 -run TestSharedHandler... ./processtest/   # EXIT=0 (determinism)
golangci-lint run ./...                                           # 0 issues
go vet ./...                                                      # EXIT=0
go build ./...                                                    # OK
```

Baseline was 88.0 %; the floor in the checklist is 88.0 %. The file holds **14**
test functions: the 13 spec rows (rows 1–4 are subtests of one table), **four**
added while closing coverage (D3's **message** branch, `DeliverMessage`'s
suppression path, the unawaited-name early exit, the secondary-armed-timer guard),
**four** more added as regressions for the pre-delivery stand-in review, and
**two** for `/code-review`'s delivery findings — **20 in all**
(`grep -c '^func Test' processtest/waiter_sources_test.go`). None
were added to chase the number; each covers a hot path or pins a found defect.

### ⚠⚠ `/code-review` then killed the waiter-COUNT key (spec §2.7)

The owner gate found **four** findings after the stand-ins. The headline one is the
audit's own falsification reproduced one level up: **two sequential ARMS of one
name each report a single waiter**, so a size-keyed bound suppressed the second
(`unhandled park: human-task at node "approve2"`). Its mirror: an arm whose branch
arms the **same** name makes the count grow every firing and spins
(`step limit exceeded after 60 steps`). The count is wrong in both directions.

An arm's identity is unreachable (unexported element types), so the bound now uses
the closest observable proxy: **which nodes tokens are parked on**. Two arms on
different activities are different nodes and both fire; one arm re-matching its own
park is the same node and fires once.

Also fixed: the new `CompleteTasksWith` mutex was held across the consumer's
`decide` callback (now map-only, with a re-check on insert); and the
`Harness`-only `ReasonTimer` promotion is documented in `drive.go` and the README
(behaviour unchanged — the free drive owns no scheduler).

⚠ **One mutation here was malformed and discarded, not recorded as evidence.** A
"count-proxy" mutation never triggered, so it proved nothing. The authoritative
evidence is that both new tests were RED against the actually-shipped count-based
bound — which is how the gate found them.

### Delivery gate — 4/4 PASSED

1. **Verification.** `processtest` **90.2 %** (baseline 88.0), lint 0, `go vet` 0.
2. **Documents describe what shipped.** Every intermediate form of the delivery
   bound was rewritten out of the ADR, spec, README, CHANGELOG and handover; the
   discarded ones survive only inside the labelled correction blocks.
3. **`/code-review`** — 4 findings, **4 fixed** (spec §2.7).
4. **`/security-review`** — **0 vulnerabilities.** Assessed net-zero exposure: the
   correlation key `Park.AwaitingMessages` now surfaces was already reachable via
   the exported `Park.State.MessageWaiters()` / `.Variables` on `main`, and the
   caller's key is matched by string equality only (`engine/state_arms.go:248`,
   `engine/step_state.go:147`) — never evaluated as an expr.

**Full suite, run TWICE** (owner-approved Docker), the second time on the
post-`/code-review` tree because the first certified a tree those fixes had since
changed:

```
go test -race -coverprofile=cover.out ./...
EXIT=0 · 64 packages ok · 0 failures · 0 data races · repo 73.8 % (baseline 73.6)
```

`origin/main` was verified still at `abccb96` with the branch directly on top, so
the branch tree *is* the merge content and this run certifies what lands.

## Shape of the work

Entirely inside one Go package (`processtest`), so it does **not** fan out — one
agent, or inline.

Files touched:

- `processtest/park.go` — `AwaitingMessages` type, `Classify`'s two assignments,
  `Node` fallback (D5), dedup helpers, doc comments.
- `processtest/handlers.go` — `PublishSignal`/`DeliverMessage`: new type, the
  mutex-guarded park fingerprint (D4), doc comments.
- `processtest/harness.go` — widen `harnessEnv.classify`'s `ReasonTimer`
  promotion (D3, ~line 305).
- `processtest/park_test.go` — the `[]string` assertion at line 129.
- `processtest/waiter_sources_test.go` — **new**, the regression suite.
- `processtest/README.md` — the `Park` struct block (line ~124) and the `Reason`
  prose. ⚠ Found by audit; it is a **fourth** consumer of the changed type.
- `CHANGELOG.md` — five consumer-visible changes (ADR Consequences 1–5).

## Fixture gotchas — all hit while probing; do not rediscover them

- `processtest.New()` returns `(*Harness, error)` and takes `Option`s, not `t`.
- `WithMessageCorrelator`'s key is an **expr**, not a literal: `"order-1"` parses
  as `order - 1` and fails `invalid operation: <nil> - int`. Write `` `"order-1"` ``.
- A boundary's downstream **action must be registered**
  (`processtest.WithCatalogActionFunc`) or the instance fails early and masks
  what the test measures. This is what hid the non-interrupting loop on its
  first probe.
- **Rows 1–4 cannot be table rows in `park_test.go`'s `TestClassify`.** That
  table builds `engine.InstanceState{Tokens: …}` literals; the arm slices
  (`Boundaries`, `ArmedEvents`, `EventTriggeredSubprocesses`, `Timers`) have
  **unexported element types**. Arm fixtures must be harness-driven:
  `h.Start(...)` then `Classify(st)`.
- A plain `ServiceTask` **never parks** — it completes synchronously. Do not
  build a park fixture on one.

## Phases

⚠ **Phase order matters and was corrected by the audit.** The original plan put
all tests in one file in phase 1, but row 4 needs the new type, so its compile
error would have failed the whole package build — zero tests running, no
observable RED. The type change therefore lands first.

### Phase 1 — the breaking type change alone

`Park.AwaitingMessages` → `[]engine.MessageWaiter`, and adapt the three code
sites (`park.go`'s priority switch keeps its `len()`; `handlers.go:106` becomes
`m.Name == name`; `park_test.go:129`).

**Verify:** `go test ./processtest/... ; echo "EXIT=$?"` EXIT=0. Pure type
change, no behaviour yet.

### Phase 2 — RED tests

Add `processtest/waiter_sources_test.go` (package `processtest_test`) with spec
§5 rows 1–6 and 9. Harness-driven, per the gotchas.

**Verify RED** and record the actual output per row: rows 1–4 fail on empty
fields, rows 5–6 fail with **`ErrUnhandledPark`** (⚠ *not*
`ErrDriveLimitExceeded` — the audit corrected this), row 9 fails on `Node == ""`.

### Phase 3 — `Classify` delegates (D1, D5)

- `AwaitingSignals = distinctStrings(state.SignalWaiters())`
- `AwaitingMessages = distinctWaiters(state.MessageWaiters())`, deduping on the
  `{Name, CorrelationKey}` pair
- `Node` fallback for arm-derived parks (D5)
- delete `distinctAwaits` once unused — no dead helper
- field doc comments name `SignalWaiters`/`MessageWaiters` as the authority

**Verify:** rows 1–6 and 9 GREEN.

### Phase 4 — the two regressions this fix would otherwise introduce

Both are guards; write each test, observe it RED against the phase-3 tree, then
fix.

- **D3 — row 7:** `AutoTimers()` must still drive a timer catch that coexists
  with a live arm. RED first (`unhandled park: signal at node …`), then widen
  `harnessEnv.classify`'s promotion to accept arm-derived
  `ReasonSignal`/`ReasonMessage`.
- **D4 — rows 8 and 10:** two sequential catches of one signal name must both
  fire (row 8), and a non-interrupting boundary must terminate rather than spin
  (row 10). Row 10 is RED only on the phase-3 tree
  (`drive step limit exceeded after 1000 steps`); row 8 goes RED only if
  fire-once-per-name is implemented, so **implement the fingerprint directly** —
  do not build the wrong bound first.

**Verify:** whole package GREEN, `go test -race ./processtest/...` EXIT=0.

### Phase 5 — mutations for the guards that cannot fail

Rows 10–13 pin semantics rather than reproduce the bug. Snapshot, mutate,
observe RED, restore, `diff` to confirm. A claimed RED that was not observed does
not count.

| Mutation | Row that must go RED |
|---|---|
| remove signal dedup | 11 (duplicate names collapse) |
| degrade message dedup to name-only | 12 (same name, different keys) |
| replace the fingerprint with fire-once-per-name | 8 |
| remove the fingerprint entirely | 10 |
| swap the fingerprint mutex for a bare `bool` | 13 (under `-race`) |

### Phase 6 — docs + delivery

- `processtest/README.md`: new `Park` type, corrected `Reason` prose.
- `CHANGELOG.md`: all **five** consumer-visible changes (ADR Consequences 1–5).
- **File the `HasArmedTimers` timer-arm gap** as a follow-up blocker in
  `HANDOVER.md`.
- Flip ADR to Accepted; update this `▶ Progress` block and `HANDOVER.md`.
- Gate: full suite (Docker + owner), `/code-review`, `/security-review`.
- Squash → merge `--no-ff` → push.

## Verification checklist

- [x] All three non-token sources covered for signals; message side covers token
      + boundary — **and event-gateway** (row 3 asserts both arms).
- [x] At least one assertion has a **non-empty `CorrelationKey`**, else D2 is
      untested — rows 4 and 12 (`order-1`, `order-2`).
- [x] Dedup: same name collapses; same name + different keys does not — rows 11/12,
      mutation-verified M1/M2.
- [x] `AutoTimers()` still drives a timer catch beside a live arm (D3) — row 7, and
      its message twin; M8.
- [x] Two sequential catches of one name both fire (D4) — row 8; M3, and M6 for the
      instance-scoping half.
- [x] Non-interrupting boundary terminates (D4) — row 10, and its message twin; M4.
- [x] `Node` non-empty for an arm-derived park (D5) — row 9; M7.
- [x] `go test -race ./processtest/...` EXIT=0 — the fingerprint is race-safe; M5
      shows the bare-variable form reporting `WARNING: DATA RACE`.
- [x] No `distinctAwaits` left behind.
- [x] `processtest/README.md` updated (struct **and** `Reason` prose, plus the
      per-park-state delivery bound and the timer-arm carve-out).
- [x] CHANGELOG carries all five changes.
- [x] No document claims the ADR-0154 class is closed outright — signals and
      messages only.
- [x] `processtest` coverage ≥ 88.0 % (**90.2 %**); `golangci-lint run ./...` 0 issues.
- [x] `go test ./...` EXIT=0 — **done**, 64 packages, 0 races, repo 73.8 %.
- [ ] `/code-review` and `/security-review` — **outstanding, owner-invoked only**.

## Commit message template

```
fix(processtest)!: Classify sees every signal/message waiter source (ADR-0166)

Classify derived AwaitingSignals/AwaitingMessages from Token.AwaitSignal and
Token.AwaitMessage alone, dropping the three other sources the engine
enumerates behind SignalWaiters()/MessageWaiters(): boundary arms,
event-based-gateway arms and event-subprocess arms. PublishSignal and
DeliverMessage iterate those fields, so a definition parked purely on an arm
could not be driven through the public harness at all -- ADR-0154's defect
class, one layer up, in a shipped product surface. Classify now calls the
engine's authorities instead of re-deriving them.

BREAKING: Park.AwaitingMessages is []engine.MessageWaiter so the correlation
key survives; a consumer has no other way to discover it, since the arm slices
are unexported. Park.AwaitingSignals keeps its type but changes semantics.
Reason shifts for three measured shapes, and Park.Node now names the parked
token for arm-derived parks instead of being empty.

Two regressions this fix would otherwise have introduced are fixed with it, both
found by the rule-#9 audit and both invisible to the existing suite:

- AutoTimers() stopped resolving timer catches that coexist with any live arm,
  because harnessEnv.classify promotes to ReasonTimer only from async-child /
  unknown. The promotion now also accepts an arm-derived signal/message reason.
- Bounding delivery to once-per-name broke two sequential catches of one signal
  name -- ordinary BPMN that passes today. Delivery is bounded per PARK STATE
  instead, via a mutex-guarded fingerprint (a bare bool was also a data race).

Scope, stated exactly: this closes the class for signals and messages only.
Park.HasArmedTimers carries the identical one-source defect and is filed as a
follow-up; a definition parked purely on a timer arm is still undriveable.
```
