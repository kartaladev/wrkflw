# Engine Package Simplification Program

**Status:** Approved (brainstorming) — 2026-07-13
**Scope:** the `engine` package (the token state machine). Umbrella spec covering three
independently-mergeable phases. Each phase gets its own implementation plan under
`docs/plans/`, its own `/code-review` + `/security-review`, and its own `--no-ff` merge to
`main`, following the established autonomous-SDD cadence.

## Context

A deliberate production-readiness + maintainability audit of `engine/` (6.8K non-test LOC,
18.6K test LOC, 20 non-test files) found a package whose **design is sound** but whose
**cognitive load is high** and whose **safety net has a hole**.

What is genuinely good (do not disturb these properties):

- One pure entry point `Step(def, state, trigger) → (state, []Command)`.
- Two sealed-union vocabularies — `Trigger` (13 impls) and `Command` (12 impls) — closed by
  unexported marker methods and constructed only via `New*` constructors.
- A real strategy table (`nodeStrategies`) for the 15 dispatched node kinds.
- The core is **genuinely pure** — verified at import level (`go list`, not grep): only
  `definition/*`, `action`, `authz`, `humantask`, `internal/expreval`, stdlib. No transport,
  persistence, watermill, gocron, clockwork, otel, or `time.Now`. Time enters only via
  `Trigger.OccurredAt()`.
- Excellent godoc; well-formed error sentinels with correct `errors.Is` chains.

### Problems this program addresses

**Safety-net hole (High):** `purity_test.go` enforces **only** the OTel import ban, yet the
README claims it guards no-transport / no-persistence / no-scheduler / no-wall-clock. Those hold
today by convention only — a `gocron` or persistence import would keep every test green.

**Convergent duplication** (flagged independently by three audit passes):

- **Three parallel "arm" families** — `armedEvent`, `boundaryArm`,
  `eventTriggeredSubprocessArm` — near-identical structs, each with its own
  `byTimer`/`bySignal`/`byMessage` scan trio + `removeForX` filter (~13 clone methods in
  `state.go`), plus duplicated arm-time encoding switches and fire-time
  interrupting/non-interrupting skeletons across `step_boundaries.go` +
  `step_eventsubprocess.go`. The single biggest structural duplication.
- **The token-cancel sweep** (cancel deadline/reminder timers → cancel in-wait reminder →
  remove boundary arms → `consumeToken`) copy-pasted ~4× across
  `step_compensation.go` (151-177), `step_errors.go` (308-336), `step_eventsubprocess.go`
  (197-234), `step_boundaries.go` (145-163).
- **The fire-once `FireAndForget` emit** duplicated 4×; the **resume-parked-token-and-drive
  ritual** 5×; the **3-way arm-dispatch preamble** (gateway→boundary→event-sub) 3× across the
  Timer/Signal/Message handlers; **`node_accessors.go`'s four identical 7-case type switches**;
  `serviceTaskStrategy` == `businessRuleTaskStrategy` byte-for-byte; 11 dead per-strategy
  type-assertion guards.

**Monster functions** where the real logic hides:

- `endEventStrategy.enter` — 256 lines, 5-deep nesting, correctness pinned to hand-narrated
  `stopped`/`tok.State` comments at ~8 return sites (`step_nodes.go:214-470`).
- `propagateError` — 310 lines, two-phase BPMN error walk (`step_errors.go:109-419`).
- The compensation trio `beginCompensation` (131L) / `stepCompensationFinish` (106L) /
  `applyFinish` (77L), with index/eligibility math duplicated between `beginCompensation` and
  `stepCompensationAdvance` (`step_compensation.go`).

**Implicit invariants** a reader must reconstruct: zero-value-as-sentinel
(`FinalStatus==StatusRunning` means "unset"; `Compensating` inferred from `Status`);
`compensationCursor`'s walk-mode inferred from which of its 14 fields happen to be non-zero;
`finishPlan`'s mutual-exclusion invariants stated only in prose; aliasing slice-interior
pointers (`&slice[i]`) returned from accessors.

**Silent no-ops with no `slog`:** late timers, stale tokens, missing-node parks, swallowed
`ErrorExpr` eval errors — all deliberate resilience, none observable.

**Verified-real completeness gaps** (current source, not shipped by Plan 8):

- `handleSubInstanceFailed` (`step_triggers.go:717`) always fails the parent — a child failure
  **cannot** be caught by a parent error boundary on the call-activity node.
- `closeScope` (`state.go:1139`) does not cascade to child scopes; callers must close children
  manually and nothing enforces it.

**Carved out of this program** (separate future spec — new event semantics, live in the node
strategies, not in the code this program touches): the intermediate throw/catch event variants
(message-throw, error-throw, non-timer/signal/message catch — `step_nodes.go:740,950`).

## Non-goals

- No change to the public conceptual API contract: `Step`, the sealed `Trigger`/`Command`
  vocabularies, the `New*` constructors, the error sentinels, and their observable behavior all
  stay the same. Internal helper extraction and file reorganization must be transparent to a
  library consumer.
- No new node kinds or event semantics (the carved-out intermediate events).
- No persistence-schema break without an explicit parity-first migration (Phase B only).

## Overarching constraints

- **No regression is a hard requirement.** Every phase keeps `go test ./...` green from repo
  root, `golangci-lint run ./...` clean, and ≥85% line coverage on touched packages. A green
  baseline is captured before Phase A begins and is the reference.
- **TDD is audited.** Pure-refactor items (behavior-preserving extraction / file moves) lean on
  the existing test suite: it must be green *before and after* each item, and the refactor is
  the only change in that step. Any item that changes observable behavior — A0 (new test), C7
  (new logs), C8, C9, and all of Phase B — is **red-first**: a failing test precedes the
  implementation, and the red state is observable in the transcript.
- **Purity preserved.** The engine core must not gain a transport/persistence/vendor/wall-clock
  import. A0 makes this machine-checked.
- Each phase: implementation plan → subagent-driven execution → whole-branch `/code-review` →
  `/security-review` → fix all findings → `--no-ff` merge to `main` → push.

## Phase A — Safe sweep

*Behavior-preserving; the existing test suite is the safety net; no ADR (except A0 is a new
test, which is red-first). Removes ~300-400 lines of pure repetition and makes behavior
findable.*

- **A0 — Harden `purity_test.go` (do first).** Extend the test from OTel-only to enforce every
  guarantee the README claims: an import denylist over the package's non-test imports
  (`transport/`, `internal/persistence`, `watermill`, `gocron`, `clockwork`, plus the existing
  OTel entry) and an AST check banning `time.Now` / `time.Since` / `time.Tick` calls in
  non-test files. Red-first: assert the new checks against a deliberately-violating fixture or
  a temporary import to see red, then wire the real check. This lands first because it protects
  every later step.
- **A1 — `cancelTokenWaits(s, tok, at) []Command`.** Extract the 4× token-cancel sweep. One
  implementation consumed by compensation, errors, event-subprocess, and boundary fire paths.
  Preserve the `evtgw:` `AwaitCommand` special-case exactly.
- **A2 — `emitFireOnceAction(s, name) []Command`.** Extract the 4× `InvokeAction{FireAndForget:
  true, Input: copyVars(...)}` emit.
- **A3 — `resumeAndDrive(...)`.** Extract the 5× resume-parked-token ritual (clear
  `AwaitCommand`, set `TokenActive`, cancel token timers, `defForScope`, `moveAlongSingleFlow`,
  `drive`).
- **A4 — arm-dispatch preamble helper.** Extract the 3-way gateway→boundary→event-sub cascade
  shared by `handleTimerFired` / `handleSignalReceived` / `handleMessageReceived`. Signal's
  broadcast-vs-first-match tail stays specialized.
- **A5 — collapse action-task strategies.** Merge `serviceTaskStrategy` and
  `businessRuleTaskStrategy` into one `actionTaskStrategy` registered for both kinds; extract
  the shared `InvokeAction`+park+`armBoundaries` body into `emitActionInvoke`, reused by
  `reinvokeServiceAction`.
- **A6 — drop dead type-assertion guards.** Remove the 11 per-strategy
  `if _, ok := node.(X); !ok { park }` guards (the registry key already guarantees the kind);
  if defensiveness is wanted, do it once in `drive()`.
- **A7 — `Resiliencer` activity interface.** Replace `node_accessors.go`'s four identical
  7-case type switches (`compensateActionOf` / `cancelActionOf` / `recoveryFlowOf` /
  `completionActionOf`) with one small interface implemented once per activity type, resolved
  via a single type assertion. (This adds methods to `definition/activity` types — confirm no
  import cycle; the interface lives where it is consumed.)
- **A8 — split `state.go`.** Break the 1166-line file by subsystem into `state_timers.go`,
  `state_arms.go`, `state_compensation.go`, `state_waiters.go`, leaving core types +
  `InstanceState` + `Status`/`Token` in `state.go`. Move ADR-narration prose out of struct
  bodies, keeping one-line godoc and the `(ADR-NNNN)` tag. Pure move — no behavior change.
- **A9 — signpost dispatch.** Split the `nodeStrategies` registry (and the `nodeStrategy`
  interface + `drive` wiring) out of `step_nodes.go` into `step_dispatch.go`, so "where is
  dispatch wired" is findable by filename.
- **A10 — fix test-file naming drift.** Rename to satisfy the project's pair-each-`.go`-with-a
  same-named `_test.go` convention: `step_gateway_test.go` → `step_gateways_test.go`,
  `step_timer_test.go` → `step_timers_test.go` (audit the full list; rename the test files, not
  the impl files).

### Phase A verification checklist

- [ ] A0's new purity checks fail red against a violating fixture, then pass once real.
- [ ] Each of A1-A9: full engine test suite green immediately before and immediately after; the
      diff for that step contains only the extraction/move, no behavioral edit.
- [ ] `go test ./...` from repo root green (no regression outside `engine`).
- [ ] `golangci-lint run ./...` clean.
- [ ] `engine` coverage ≥85%.
- [ ] `go doc ./engine` surface unchanged (no accidental export/unexport).
- [ ] `/code-review` (whole branch) findings all resolved; `/security-review` clean.

## Phase C — Decompose monsters + make invariants explicit

*Medium risk; correctness-focused. Pure decompositions lean on existing tests; C7/C8/C9 are
red-first. Folds in the two verified completeness gaps that live in the touched code.*

- **C1 — decompose `endEventStrategy.enter`** (256L) into `exitRootScope` /
  `exitRegularSubprocessScope` / `exitEventSubprocessScope` (root-level | nested) with a shared
  `resumeInParentScope(enclosingNodeID, parentScopeID)`. Preserve every `stopped`/`tok.State`
  outcome exactly; the existing end-event/ESP tests are the oracle.
- **C2 — decompose `propagateError`** (310L) into `findDirectBoundary` /
  `findEnclosingBoundary` (each returning the matched boundary + target scope) and a shared
  `routeToBoundary` tail (fire-once action + outgoing-flow resolve + `placeToken` + `drive`).
- **C3 — collapse compensation eligibility math.** Compute the eligible index range once via
  `eligibleRange(records, toNode) (start, stopExclusive)`, consumed by both `beginCompensation`
  and `stepCompensationAdvance`; replace full-struct cursor re-lists with copy-and-mutate.
- **C4 — explicit `compensationCursor` walk-mode.** Add a `walkMode` enum
  (`walkAdmin` | `walkThrowTargeted` | `walkThrowScopeWide` | `walkReverse`) set at cursor
  creation, replacing the inferred-from-which-field-is-zero logic. Retires the "zero for every
  other walk" comment invariants.
- **C5 — kill positional bool/string tails.** Replace `beginCompensation`'s 11-positional-arg
  signature and `propagateError`'s trailing `raiseIncidentOnUnhandled bool` with an options
  struct / named type so call sites read clearly.
- **C6 — enforce `finishPlan` invariants.** Move the documented mutual-exclusion invariants
  (e.g. `resetVars` xor `toNode`; terminate plans never `scopeWideThrow`) into a constructor or
  a checked assertion instead of prose.
- **C7 — observability on silent no-ops** *(red-first)*. Add `slog` (Warn/Debug as
  appropriate, via the standard library `slog`, no vendor) to: late-timer no-op on terminal
  instance, stale-token fire no-op, missing-node park in `drive`, swallowed `ErrorExpr` eval
  error. Tests assert a log record is emitted (via an `slog` test handler), not merely that
  behavior is unchanged.
- **C8 — [folded gap] `SubInstanceFailed` → parent error boundary** *(red-first)*. When a
  boundary error event is attached to the call-activity node whose child failed, route to it
  (reusing the Phase-C `findDirectBoundary`/`routeToBoundary` machinery and the child's error
  code) instead of unconditionally failing the parent. Fall back to `FailInstance` when no
  matching boundary exists. Regression test reproduces the current always-fail behavior first.
- **C9 — [folded gap] `closeScope` cascades to child scopes** *(red-first)*. `closeScope`
  closes descendant scopes (and their arms/timers) rather than requiring callers to pre-close
  children. Test asserts a parent-scope close removes child scopes; verify no caller currently
  relies on the manual-cascade behavior (audit call sites first).

### Phase C verification checklist

- [ ] C1/C2/C3: the decomposed functions produce byte-identical `[]Command` / state outcomes on
      the existing end-event, error-propagation, and compensation test suites (green before and
      after; no test edits except mechanical helper-name references).
- [ ] C4/C5/C6: no behavior change; existing tests green; new invariant assertions covered.
- [ ] C7: red-first — a test asserting each new log record fails before the `slog` call exists.
- [ ] C8: red-first regression test (child failure under a parent error boundary) fails, then
      passes; the no-boundary path still `FailInstance`s.
- [ ] C9: red-first — parent-scope close leaving orphaned child scopes fails, then passes.
- [ ] `go test ./...` from repo root green; `golangci-lint run ./...` clean; `engine` ≥85%.
- [ ] `/code-review` + `/security-review` clean.

## Phase B — Unify the arm/trigger model

*Medium risk; needs an ADR; the only phase touching the persistence wire format. Parity-first
migration.*

- **B1 — ADR (0128 or next free).** Record the decision to unify the three arm families into one
  `triggerArm` type behind a generic `armTable[T]`, and the parity-first wire-format migration.
  Nygard template.
- **B2 — unified `triggerArm`.** Collapse `armedEvent` / `boundaryArm` /
  `eventTriggeredSubprocessArm` into one `triggerArm{Signal, Message, MessageKey, TimerID,
  NonInterrupting}` (plus the discriminating owner key each currently carries — host token vs
  enclosing scope — modeled explicitly). A generic `armTable[T]` provides
  `byTimer`/`bySignal`/`byMessage`/`removeForKey`, replacing the ~13 clone accessors in
  `state.go` (now `state_arms.go` after A8).
- **B3 — shared arm→fire→cancel lifecycle** across boundaries + event-subprocess + gateway,
  reusing `cancelTokenWaits` (A1) and `emitFireOnceAction` (A2). The interrupting vs
  non-interrupting (repeatable-arm, ADR-0124) skeleton is written once.
- **B4 — parity-first migration.** `InstanceState.Boundaries` / `EventTriggeredSubprocesses` /
  `ArmedEvents` are serialized as the persistence snapshot. Preserve JSON wire compatibility:
  keep round-trip tests for the old shapes, add the new shape, prove both deserialize to
  equivalent runtime state, migrate producers, then delete the old types. Follow the
  established parity-first pattern (port tests → both forms green → then delete).

### Phase B verification checklist

- [ ] ADR committed under `docs/adr/` (Nygard template).
- [ ] Old-shape JSON snapshots still deserialize to equivalent state (round-trip parity test)
      before the old types are deleted.
- [ ] Every arm behavior (boundary error/timer/signal/message, event-sub interrupting +
      non-interrupting repeatable, gateway arm) covered green under the unified type.
- [ ] Persistence round-trip tests (all three dialects where applicable) green — no snapshot
      corruption.
- [ ] `go test ./...` from repo root green; `golangci-lint run ./...` clean; `engine` ≥85%.
- [ ] `/code-review` + `/security-review` clean; `--no-ff` merge + push.

## Sequencing rationale

A → C → B. A is safe and behavior-preserving, lands the machine-checked purity net, and builds
the test confidence that de-risks the rest. C decomposes the highest-correctness-risk code and
closes the two in-scope completeness gaps as a side effect of opening that code. B is the
biggest structural win but the only one touching the persistence wire format, so it goes last
behind its own ADR and a parity-first migration. Each phase is independently valuable and
independently mergeable.
