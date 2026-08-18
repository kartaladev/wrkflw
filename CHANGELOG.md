# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **Pre-1.0 notice.** Until a `v1.0.0` tag is cut, the public API may change between
> minor versions. See [`STABILITY.md`](STABILITY.md) for the stability and deprecation policy.

## [Unreleased]

The first tagged release (`v0.1.0`) will be cut from this section: it is the inaugural
feature set, built across ADRs 0001–0095. Because nothing has shipped yet, everything below
is **Added** — the list describes the engine as it stands today, not a delta from a prior
release.

### Breaking changes (pre-v0.1.0 — no stability promise)

- **`processtest.RunTaskStoreConformance` now verifies that an accepted task reaches its inbox
  (ADR-0184).** ADR-0183 shipped this helper precisely because adopting it is a *silent* break for a
  consumer's own `humantask.TaskStore`. The helper had the same defect: its documentation promised a
  rejected write is *"neither readable through `Get` nor listed by `AssignedTo` or `ClaimableBy`"*,
  and a thirty-line comment argued the two inbox queries were essential and non-redundant — but the
  **legal** leg never asked an inbox anything, so nothing established the queries worked at all.
  Measured: a store answering both inbox queries with `nil, nil` **passed the entire suite**, with
  every `NotContains` holding vacuously.

  Each conformance case now declares which inbox must return it. The legal `Unclaimed` control must
  be returned by `ClaimableBy` for an actor holding the `manager` role, and the legal `Claimed`
  control by `AssignedTo(its claimant)`. The terminal and anonymous-kiosk shapes declare no
  expectation, because neither query is contracted to return them.

  ⚠ **The break is SILENT, exactly like ADR-0183's.** There is no signature change, so nothing
  recompiles differently: a consumer `TaskStore` that passes today can fail after upgrading. This is
  a *correct* tightening — a store that fails the new assertions was never satisfying the documented
  contract, and the failure it now reports is a real defect in that store, most likely list queries
  that silently return nothing.

  The three bundled implementations were verified against the tightened contract and all pass:
  `humantask.MemTaskStore`, the SQL `store.HumanTaskStore` on SQLite, and
  `persistence.CachingTaskStore` (a decorator with its own inbox caching).

  Not a behaviour change, but shipped alongside: the helper's two misuse guards (a nil factory, and
  a factory returning a nil store) are now executed by tests for the first time, and every
  `require.Eventually` under `scheduler/` now shares a per-package `eventuallyBudget` constant
  instead of a per-site literal (1–3 s) — a test-only change that raises the bound at all 40 sites
  to 10 s, ~1000× the measured 0.01 s fire time. This does not remove the load-dependent
  bound; `require.Eventually` is still a real-time wait, just a much larger one. ⚠ It is also **not**
  what closes backlog 42 — see Fixed, below.

- **A human task's claim invariant is now enforced before it can be committed (ADR-0183).**
  `humantask.HumanTask` has always documented *"`Claim` … nil when Unclaimed"* on its own field, and
  the read path was built to uphold it; the **write** path upheld nothing. `Upsert` bound the state
  and claim columns independently, so `State: Claimed, Claim: nil` persisted and read back
  unchanged, and an `Unclaimed` row **carrying** a claim was returned by `AssignedTo("alice")` *and*
  `ClaimableBy(alice)` — double-listed as both held and offered.

  A new `humantask.Validate(t HumanTask) error` is the single definition of the rule, returning an
  error wrapping the new **`humantask.ErrInvalidTask`**: a `Claimed` task must carry a `Claim`, an
  `Unclaimed` task must not, and `State` must be one of the four declared constants. It is enforced
  **pre-commit** in the runtime (the primary seam) and again in all three bundled `Upsert`
  implementations (`humantask.MemTaskStore`, the SQL `store.HumanTaskStore`, and
  `persistence.CachingTaskStore`) as defence-in-depth.

  ⚠ **Three breaking surfaces:**

  1. **`Upsert` rejects contradictory shapes that silently succeeded before**, across all three
     bundled stores. `TaskStore.Upsert`'s interface doc now states this as a contract implementations
     MUST uphold, directing them to call `Validate` rather than re-derive it.
  2. **A reassignment with an empty `to` now fails** with the new
     **`engine.ErrEmptyReassignTarget`**, classified **400** — in `task.TaskService.Reassign`
     before it reads the task store, and again in `engine.Step` for a trigger built by hand.
     `POST /tasks/{token}/reassign
     {"to":""}` previously succeeded and minted a `Claimed` task nobody holds — invisible to
     `AssignedTo` (no claimant to match) and to `ClaimableBy` (not `Unclaimed`), reachable only by
     ID. It is refused before `cloneState`, so a rejected trigger touches no state at all.
  3. **`humantask.ErrInvalidTask` reaching HTTP is a 422 `conflict_state`, not a 500.** Both
     sentinels previously fell to the `default:` arm, which returns 500 with an **empty body** —
     discarding the message that names the task and the contradiction.

  ⚠ **For a consumer's own `TaskStore` implementation the break is SILENT.** The interface signature
  does not change, so nothing recompiles differently and a non-conforming store keeps accepting bad
  rows. **Verify yours with the new exported `processtest.RunTaskStoreConformance(t, newStore)`**,
  which exercises every rule plus "a rejected `Upsert` persists nothing". Consumer fixtures seeded
  through `processtest`'s `Tasks() *humantask.MemTaskStore` are also affected, since that store is
  now strict. Churn was measured as **zero**, but over *this repo only*.

  ⚠ **`Unclaimed` is the zero value of `TaskState`**, so the `Unclaimed` rule also rejects a task
  carrying a `Claim` whose `State` was never set — including a decode that dropped only `State`.
  Deliberate: such a record is exactly as contradictory as an explicitly `Unclaimed` one. The error
  message names `unclaimed`, which can read as wrong if you did not set that state yourself.

  ⚠ **An empty claimant remains LEGAL on every state.** `Claimed` + `Claim{Actor{ID: ""}}` is
  ADR-0148 amendment 1 §4's kiosk shape — anonymous, but carrying roles — and this release does not
  supersede it. The empty-ID **claim** route is likewise untouched; only the empty *reassignment
  target* is refused, because reassignment moves a claim *to someone* and mints an actor with no ID
  **and** no roles. `Completed` and `Cancelled` carry no claim rule at all: a task cancelled while
  held keeps its claim as audit, and an immediate manual task completes without one.

  **Not fixed, deliberately:** the completion axis (`Completed` ⟹ `Completion != nil`) stays
  unenforced, and **existing rows are not repaired** — there is no migration or backfill, since a
  blind one could only guess at claim data it must not invent. Rows written before this guard,
  including any double-listed one, stay as they are.

- **A failed compensation action is now visible, and retryable (ADR-0179).** A compensation action
  replying `ActionFailed` used to be skipped in **total silence** — no retry, no incident, and (despite
  ADR-0034's Consequences claiming otherwise) **no log line**. It now always emits a
  `slog.WarnContext` and raises an `engine.IncidentCompensationFailed`, and it is re-dispatched after
  a backoff when the consumer opts in via `runtime.WithCompensationRetryPolicy` (or
  `engine.StepOptions.CompensationRetryPolicy`). Opting in is **not** required to be affected by the
  visibility half — that is on for everyone.

  ⚠ **Three breaking surfaces, and all three fail SILENTLY — none produces a compile error:**

  1. **`processtest` consumers, even with retry OFF.** An incident is not an inert document field.
     `processtest.Classify` ranked its incident rung above its timer rung, so one walk-scoped
     incident flipped a park `timer → incident`; `AutoTimers()` acts only on `ReasonTimer` and
     stopped driving it, so `drive` reported `ErrUnhandledPark`. Measured end-to-end as
     `unhandled park: incident at node "charge"`. **Fixed in this release** (see item 3) — but a
     consumer who pinned the old classification will see the change.
  2. **A hand-built `engine.Incident{}` fixture loses its `ReasonIncident` classification** if it
     carries no `TokenID` and no companion `TokenIncident` token. That shape is unreachable from the
     engine itself (every real `IncidentAction` names and parks its token), so **no reachable state
     regresses** — but a test fixture is not a reachable state, and one such fixture existed in this
     repo's own suite.
  3. **`Classify`'s incident rung is now split by scope, and a walk-scoped incident no longer
     outranks an actionable park.** ⚠ **This also changes the pre-existing ADR-0175
     `IncidentCompensationStall` kind, not only the new one.**
     - a **token-scoped** incident (one naming a `TokenID`, or with a token in `TokenIncident`)
       keeps its position above the signal rung — it genuinely parks the instance;
     - a **walk-scoped** incident (token-less: `IncidentCompensationStall`,
       `IncidentCompensationFailed`) now sits immediately **above `ReasonUnknown`** and below every
       other reason.

     So a stall or failure incident coinciding with a signal, message, human-task or async-child
     park now classifies as **that** reason, where it previously classified `incident`. It still
     classifies `incident` when nothing else explains the park, which preserves ADR-0175's recorded
     consequence (c). `Park.Incidents` continues to report **every** incident unconditionally, so
     nothing is hidden — only the *primary reason* moved.

     Rationale: `Park.Reason` names what the harness must **do** to unblock the instance. A
     walk-scoped incident is a *record that something failed* — `ResolveIncident` refuses the kind by
     design — so it must never displace a reason that is actionable. Only `AutoTimers` is
     `Reason`-gated, so shipped handlers are unaffected; a consumer's own `Reason`-switching handler
     will see the change.

  **Also changed, non-breaking but worth knowing:**
  - `terminalEventErr` and `terminalErr` now publish an instance's cause of death through an
    **allow-list** (`engine.IncidentAction` only). This fixes a defect that **shipped before this
    release**: `st.Incidents[0].Error` was read positionally, so a walk-scoped
    `IncidentCompensationStall` at index 0 already won over the genuine failure at index 1.
  - `ErrIncidentNotResolvable` now covers two incident kinds, and its message names the
    compensation-**walk** verbs rather than the compensation-stall verbs.
  - `Pruner.PruneTimers` no longer deletes `engine.TimerCompensationRetry` rows: deleting one
    strands a live compensation walk. ⚠ Consequence: no bulk sweep can delete such a row, so one
    leaked by an instance that died mid-walk is **permanent**. Correctness is preserved;
    housekeeping is not.
  - ⚠ An **unset `MaxInterval` is not "no cap"** on a compensation retry: `Normalize()` fills it
    from `DefaultRetryPolicy()` (100s) and the backoff caps against it. Set it explicitly for long
    backoffs. `MaxElapsed` is **not** honoured (a walk holds no token, so there is no per-attempt
    start timestamp), and the backoff is **not** jittered.

- **Definition decoding rejects unknown fields (ADR-0167).** `model.ParseYAML` and
  `ProcessDefinition.UnmarshalJSON` silently discarded field names they did not
  recognise. A misspelled `eligible_roles` therefore left a `UserTask` with empty
  `EligibleRoles`, and an empty `AuthzSpec` is documented to mean allow-all — so a
  typo composed into a **silent authorization bypass** that errored neither at parse
  time nor at authorization time. Both decoders are now strict, with no opt-out.

  - **This is a DATA migration, not only a source one.**
    `DefinitionStore.GetDefinition` / `Lookup` decode persisted definition blobs
    through the now-strict `UnmarshalJSON`, so a row written by an earlier build
    becomes unloadable at runtime if it carries a key the wire structs no longer
    declare. New standing constraint: **a `NodeWire` field may not be removed
    without a migration.** `go vet` cannot prove this safe — it compiles, it does
    not decode.

    ⚠ **The primary trigger is ADR-0144 (`8179c0b`), which moved the definition
    wire to snake_case.** Five tags were camelCase before it — `compensateAction`,
    `compensationAction`, `completionAction`, `correlationKey`, `messageName` —
    and each was verified to fail now: `json: unknown field "compensateAction"`.
    Any definition row written before `8179c0b` that carries one of them stops
    loading, and every instance of that definition becomes unrunnable. That is a
    wider blast radius than the retired `errorEndEvent` kind noted below. **Audit
    stored definitions for these five keys before deploying**, or ship a backfill.

  - **YAML idioms that parsed before are now rejected**: a top-level anchor holder
    block (`_defaults: &d`) and `x-` extension keys. No reserved-prefix carve-out
    was added (owner decision) — one decoding rule beats a rule plus an exception,
    and the choice is cheap to reverse pre-v0.1.0.

  - **The canonical nested trigger forms are YAML-invisible.** `nodeYAML` is a
    strict subset of `NodeWire`; `boundary_action`, `boundary_error_expr`,
    `deadline_trigger`, `timer_trigger` and `wait_trigger` have no YAML
    counterpart, so authoring them in YAML now errors where it was previously
    dropped in silence. That is the same defect one level down, and turning it into
    an error is the improvement — but it is a recorded consequence.

  - **Error shapes are unchanged.** `ParseYAML` still wraps as
    `workflow-definition: parse YAML: %w`; `UnmarshalJSON` still returns its decode
    error unwrapped. No new sentinel. The two decoders report differently — `yaml.v3`
    names every unknown field with a line number, `encoding/json` stops at the first
    with no position — and that asymmetry is accepted rather than normalised.

  - **`README.md` was itself a live instance of the bug** and is fixed: **nine
    distinct camelCase keys over seven lines** across two quickstart blocks
    (`compensateAction`, `eligibleRoles`, `deadlineDuration`, `deadlineFlow`,
    `deadlineAction`, `retryPolicy` and its sub-keys `maxAttempts`,
    `initialInterval`, `backoffCoef`) — none declared by any struct tag. It also
    listed `errorEndEvent` as a valid `kind`, **which ADR-0127 retired**
    (`dcfe3f1` folded it into `EndEvent`); an error end is an `endEvent` with
    `end_behavior: error`. ⚠ That retirement also means a definition blob
    persisted before `dcfe3f1` carries `"kind":"errorEndEvent"` and does not load
    — pre-existing, not caused by strict decoding, but it sharpens the
    data-migration warning above.

- **`processtest.Classify` sees every signal and message waiter source (ADR-0166).**
  It derived `AwaitingSignals` / `AwaitingMessages` from `Token.AwaitSignal` and
  `Token.AwaitMessage` alone — the first of the **four** sources the engine
  enumerates behind `InstanceState.SignalWaiters()` / `MessageWaiters()`. Boundary
  arms, event-based-gateway arms and event-subprocess arms were silently dropped,
  so `PublishSignal` / `DeliverMessage` iterated an empty list and a definition
  parked purely on an arm could not be driven through the harness at all. `Classify`
  now calls those authorities instead of re-deriving them.

  - **`Park.AwaitingMessages` changes type** to `[]engine.MessageWaiter`, so the
    correlation key survives. A consumer has no other way to discover which key an
    arm expects — the arm slices on `InstanceState` have unexported element types.
    `DeliverMessage` still matches by **name only**; the key is what gets delivered.

  - **`Park.AwaitingSignals` keeps its type but changes semantics.** A consumer's
    `if len(p.AwaitingSignals) > 0` branch now fires on definitions where it never
    did. That is the fix, and it is still a behavioural change.

  - **`Park.Reason` shifts** for definitions carrying a live arm: an event-gateway
    signal arm moves `async-child` → `signal`, and a ReceiveTask with a signal
    boundary moves `message` → `signal`. A UserTask with a signal boundary is
    unchanged. Carved out: a timer catch that merely coexists with an arm still
    promotes to `timer`, so the `AutoTimers()` recipe keeps working.

  - **`Park.Node` now names the parked token's node** for arm-derived parks, where
    it previously collapsed to `""`. It still never names the arm's own node.

  - **`PublishSignal` / `DeliverMessage` bound ARM-derived deliveries.** An arm
    fires once per instance per **parked node** — without a bound, a
    non-interrupting arm (which stays armed after firing) would re-match an
    unchanged park until the drive hit its step limit. Two arms of one name on
    different activities both fire. A **token** signal/message catch is *not*
    bounded and behaves exactly as before: it is consumed when it fires, so loops
    back to the same catch and two sequential catches of one name both still fire
    every time.

  - **`CompleteTasksWith`'s memoization is now mutex-guarded.** Sharing one handler
    value across concurrent drives raced on its internal map. Found while testing
    the above; the harness documents concurrent drives as supported. The lock
    covers the map only — the `decide` callback still runs outside it.

  - **The `ReasonTimer` promotion is documented as `Harness`-only.** The
    free-function `DriveToCompletion` owns no scheduler, so a timer catch
    classifies as `ReasonAsyncChild` there and never `ReasonTimer`. Behaviour
    unchanged; the asymmetry was previously undocumented.

  **Not closed for timers.** `Park.HasArmedTimers` carries the identical one-source
  defect (`len(state.Timers) > 0`), so a definition parked purely on a **timer** arm
  remains undriveable through the harness. Closing that needs an engine-side timer
  authority mirroring `SignalWaiters` and is filed as a follow-up.

- **Triggers declare their own terminal policy, enforced once in `Step` (ADR-0165).**
  A terminal process instance (`StatusCompleted`, `StatusFailed`, `StatusTerminated`)
  can no longer be resurrected by a late trigger. Previously the guard was hand-copied
  into eight individual handlers, so the handlers that lacked a copy silently let a
  dead instance come back to life.

  - **`engine.Trigger` gained an unexported method**, `terminalPolicy()`. No external
    implementation can exist — `isTrigger()` already seals the interface — so nothing
    outside the module breaks. Inside it, a new trigger type now fails to compile until
    its author declares a policy; that compile error is the mechanism.

  - **Two new sentinels**, `engine.ErrInstanceTerminal` and `engine.ErrTaskNotOpen`.
    Both wrap `engine.ErrInvalidTransition`, so the existing `service/` → `ErrConflict`
    → HTTP **422** mapping picks them up with no change to either layer.

  - **`runtime/task.ErrTaskNotOpen` is now an alias of `engine.ErrTaskNotOpen`.**
    `errors.Is` against either identity holds. The error *message* changes from
    `workflow-runtime: task is not open` to `workflow-engine: human task is not
    open`. The alias also makes the sentinel wrap `ErrInvalidTransition`, where the
    previous standalone `errors.New` wrapped nothing: a route mounted over
    `RefreshTaskCandidates` now classifies **422** through
    `httpcore.ClassifyError` where it previously fell through to a **500 with an
    empty body**. (This engine ships no HTTP route for that method, so the change
    is visible only to a consumer who mounts one.)

  - **Error-sentinel change.** `StartInstance`, `HumanClaimed`, `HumanReassigned`,
    `HumanCompleted` and `ResolveIncident` delivered to a terminal instance now return
    `engine.ErrInstanceTerminal`. What each did *before* is not one story — the table
    below is authoritative, and most of these did not return an error at all. They
    split by how the instance became terminal: where the terminal transition consumed
    the tokens (cancel, force-terminate), the old code found no awaiting token and
    returned `engine.ErrTokenNotFound` — a true-but-useless statement about a dead
    instance, indistinguishable from a mistyped task id. Where a token *survived* the
    transition, there was no error at all and the trigger was applied to the dead
    instance, which is the damage the table describes. A consumer classifying by
    `ErrInvalidTransition` is unaffected. A consumer matching
    `errors.Is(err, engine.ErrTokenNotFound)` to detect a dead instance must switch.
    The two sentinels are deliberately siblings, not parent and child.

  Behaviour on a terminal instance, before → after:

  | Trigger | Before | After |
  |---|---|---|
  | `StartInstance` | flipped `Failed` → `Running` with `EndedAt` still set, seeded a second start token, re-minted tasks | `ErrInstanceTerminal` |
  | `HumanClaimed`, `HumanReassigned` | re-opened the cancelled task and emitted `UpdateTask` | `ErrInstanceTerminal` |
  | `HumanCompleted` | completed post-mortem, appended a falsified history visit, and on a single-token instance flipped `Failed` → `Completed` | `ErrInstanceTerminal` |
  | `ResolveIncident` | silently did nothing | `ErrInstanceTerminal` |
  | `SignalReceived`, `MessageReceived` | merged the payload into the dead instance's `Variables` and drove a surviving token to a post-mortem end event | clean no-op, no error |
  | `CancelRequested` | re-entered the compensation walk on a force-terminated instance, re-firing every `InvokeAction` and publishing a **second** terminal event | clean no-op, no error |
  | `CompensateRequested` with `ToNode`/`ReverseNode` | rejected with an unsentinelled string | `ErrInstanceTerminal` (so now 422, not 500) |
  | `CompensateRequested`, plain full rollback, records surviving | walked | still walks (legitimate admin rollback, ADR-0164) |
  | `CompensateRequested`, plain full rollback, no records | flipped `Failed` → `Terminated`, discarded a surviving token and rewrote `EndedAt` while performing no compensation at all | `ErrInstanceTerminal` |

  `ActionCompleted`, `ActionFailed`, `HumanCandidatesResolved`, `TimerFired`,
  `SubInstanceCompleted` and `SubInstanceFailed` keep their existing silent-no-op
  behaviour; they are delivered asynchronously by the engine's own machinery, whose
  callers cannot tell a no-op from success and must not retry.

  **Separately, human-task triggers now check task lifetime**, a second key the
  instance-status guard cannot see: ADR-0163 closes a task while the instance keeps
  running. `HumanClaimed`, `HumanReassigned` and `HumanCompleted` against a task that
  is already `Completed` or `Cancelled` return `engine.ErrTaskNotOpen` instead of
  re-opening it.

  Making that guard reachable required `HumanCompleted` to resolve the task before
  the token, which moves one more error, toward precision and without changing an
  HTTP status: on the **deadline-breach** path, `ErrTokenNotFound` →
  `engine.ErrTaskNotOpen`.

  **All four human-task triggers agree on an id with no task record**, reporting
  `engine.ErrTokenNotFound` (→ `ErrInvalidTransition` → **422**). This is unchanged
  behaviour, restated because it was briefly not true: an earlier cut of this
  delivery moved `HumanCompleted` to `humantask.ErrTaskNotFound` (→ 404) on the
  grounds that the service layer answers an unknown id that way. That reasoning was
  backwards. `service.deliverTaskTrigger` reads the task store **first**, so a
  genuinely unknown id never reaches the engine at all; the engine's branch fires
  only for a **ghost** — an id the store knows and instance state does not — which
  is a state conflict, and answering 404 would deny a task the store still holds.
  `HumanCompleted` alone can also distinguish the invariant violation where a token
  *is* parked on a vanished record, and that case keeps its pre-existing
  `humantask.ErrTaskNotFound`.

  `HumanCandidatesResolved` deliberately keeps its **silent** no-op on a closed
  task rather than erroring: a closed task's candidate list is frozen audit, and
  the runtime restates candidates on its own schedule rather than in response to a
  synchronous call.

- **Instance-listing cursors are strict, and `kernel.EncodeCursor` returns an error
  (ADR-0160).** Two changes for consumers who implement `kernel.InstanceLister`
  themselves:
  - **`EncodeCursor` now returns `(string, error)`.** It previously discarded its
    `json.Marshal` error and returned `""` — which *is* the first-page sentinel, so a
    page could answer `has_more: true` with an empty `next_cursor` and a conforming
    client would re-request page one forever.
  - **Cursors issued before this release are rejected** with `kernel.ErrBadCursor`
    (HTTP 400). The envelope gained a `kind` discriminator, and decoding is now strict:
    unknown fields and trailing data are refused. A client paging across the upgrade
    receives one 400 and restarts from page one; no cursor is persisted anywhere, so
    that is the whole blast radius.

  Previously a cursor from another family decoded *silently* — an armed-timer cursor
  became `(zero time, "inst-x")`, and because instance listing is `DESC` that predicate
  matches nothing, so the operator got an empty page with a 200 and no diagnostic.

- **Armed-timer cursors reject trailing data (ADR-0160).** `DecodeArmedTimerCursor`
  accepted a valid payload with a second JSON value appended, returning the first
  value's contents and no error. Not a signature change; a previously-accepted
  malformed cursor is now an `ErrBadArmedTimerCursor`.

- **Bounded armed-timer reads (ADR-0159).** `service.TimerAdmin` gained
  `ListArmedPage(ctx, kernel.ArmedTimerFilter) (kernel.ArmedTimerPage, error)` —
  a breaking interface addition for any consumer implementing that port. `GET
  /admin/timers` is now paged (`limit`, `cursor`, `total` query parameters) instead of
  returning every armed timer in one body, and its response carries `next_cursor`,
  `has_more` and an opt-in `total_count`; a consumer that read the whole array
  unconditionally must follow the cursor. `kernel.TimerStore.ListArmed` is unchanged.
  (Backfilled — this entry was missing when ADR-0159 shipped.)

- **ProcessInstance audit view: snake_case wire format, node-visit history, and a
  human-task audit trail (ADR-0144–0151).** The instance JSON is now a single coherent,
  snake_case document. Concretely:
  - **All wire tags are snake_case** (ADR-0144), including `model.RetryPolicy` and
    `schedule.ClockTime`. Consumers parsing the old mixed-case JSON must update their
    field names. `scoped_actions` is marshal-only (dropped on unmarshal).
  - **`TaskToken` → `TaskID`** across the engine, and node execution history is exposed
    as `NodeVisit` records with a `close_kind` discriminator
    (`instance_cancelled`, `boundary_interrupted`, `terminated`, `errored`,
    `compensated`, `reversed`, `deadline_expired`; unset for normal advances) (ADR-0145).
  - **`TaskService.Complete` takes an `engine.CompletionInput`** (`{Outcome, Note, Output}`)
    instead of a bare `map[string]any`; `service.CompleteTaskRequest` gains `Outcome`
    and `Note` (ADR-0146). A user-task node may declare an outcome set, and a completion
    outside it is rejected with `engine.ErrInvalidOutcome` (see the outcome-rules entry
    below for the mandatory-outcome and exposure rules added in review).
  - **`HumanTask.ClaimedBy string` → `Claim *humantask.Claim`**, plus
    `Completion *humantask.Completion`, and `Candidates` changes from `[]string` to
    `[]authz.Actor` (ADR-0147). Both audit records carry the full observed actor rather
    than a bare ID. `runtime/view.ActionableTask` follows the same shape.
  - Candidate actors are now resolved **before** the commit that parks the task, so they
    are persisted with it; bounded by `runtime.WithCandidateResolveTimeout` (default 10s,
    non-positive disables). Fixes a latent bug where `UpdateTask` round-trips erased
    `HumanTask.Vars` and silently broke attribute-based authorization (ADR-0147).
  - Engine-minted ids move behind an injected `IDGenerator` port (ADR-0149), and
    `TaskService.RefreshCandidates` re-resolves an open task's candidates
    (`service.RefreshTaskCandidates`, requires `service.WithActorResolver`) (ADR-0150).

  **Upgrade note — the instance response got wider.** `ProcessInstance` now embeds the
  full `*model.ProcessDefinition` (replacing the derived `action_bindings` summary;
  `def_id` and `def_version` are retained and are now always emitted), and
  candidate/claim/completion actors render as full `authz.Actor`
  (`{id, roles, attributes}`) rather than bare ID strings. `Actor.Attributes` is
  consumer-populated (via your `ActorResolver`) and may hold directory data such as
  username or email; the embedded definition carries each node's `eligible_roles` /
  `eligible_privileges` / `eligible_expr`. If you mount `GET /instances/{id}/snapshot`
  or `GET /instances/{id}/actionable`, re-check that their audience should see this —
  unlike `GET /instances/{id}`, those two routes currently have no `InstanceMapper`
  redaction seam (extending it to them is queued).

- **User-task outcome rules are now fail-closed both ways (ADR-0146 amendments 1–3).**
  Declaring `WithOutcomes(...)` makes the outcome **mandatory**: completing such a node
  with no outcome is rejected with the new `engine.ErrOutcomeRequired` (previously it was
  silently accepted, published no routing variable, and blew up downstream as
  `ErrNoMatchingFlow`). Separately, `WithExposeOutcome()` / `WithOutcomeVariable(...)` now
  **require** a non-empty `Outcomes` set, enforced at authoring time by the new
  `model.ErrOutcomeExposureWithoutOutcomes` — publishing a completer-supplied string into
  the process variables demands a declared, closed value domain. Manual tasks are exempt
  from both rules (they are already forbidden from declaring outcomes). Both
  `engine.ErrInvalidOutcome` and `engine.ErrOutcomeRequired` now classify as HTTP
  **400 bad_request** instead of falling through to an opaque 500.

- **Renamed `engine.Completion` → `engine.CompletionInput` (ADR-0146 amendment 4).** The
  delivery otherwise shipped two exported types named `Completion` — the payload a caller
  submits and the persisted audit record `humantask.Completion` — with overlapping
  `Outcome`/`Note` fields. The input side now carries the `Input` suffix; the record side
  is unchanged. No deprecation alias (pre-v0.1.0 hard-rename convention).
  Migration: replace `engine.Completion{...}` with `engine.CompletionInput{...}`.

- **`engine.CloseKind` is a defined type (ADR-0145 amendment 1).** The `NodeVisit`
  close-reason constants were untyped strings, so `v.CloseKind = "cancelled"` compiled.
  `CloseKind` is now `type CloseKind string` with typed constants and a `String()` method,
  matching the sibling `TokenState`/`Status` discriminators. The JSON wire value is
  unchanged, and an empty close kind still means a normal advance. Migration: a consumer
  storing the field in a `string` needs `string(v.CloseKind)` or `v.CloseKind.String()`.

- **SQLite TEXT timestamps are now written with a fixed-width nine-digit fraction
  (ADR-0151).** `time.RFC3339Nano` trims trailing zeros, so the lexicographic `TEXT`
  comparison SQLite performs did not match chronological order — a genuinely due row
  could be skipped by `WHERE <col> <= ?`. This silently affected the relay outbox claim,
  call-link lease reclaim, retention pruning, and keyset pagination ordering. Reads are
  backward compatible (parsing still accepts any fraction width), and Postgres/MySQL are
  unaffected. **A pre-existing SQLite database file keeps the old encoding for rows
  already written and should be rebuilt** — no data migration ships, per the pre-1.0
  single-migration-file convention (ADR-0132).

- **Renamed `service.Engine` → `service.ProcessEngine` and `NewEngine` → `NewProcessEngine`
  (ADR-0141).** The public facade type and constructor read more clearly against the pure
  `engine` package and `runtime.ProcessDriver`. No deprecation alias (pre-v0.1.0 hard-rename
  convention, ADR-0098/0107/0108). Migration: replace `service.Engine` with
  `service.ProcessEngine` and `service.NewEngine(...)` with `service.NewProcessEngine(...)`
  everywhere.

- **The public `clock` package is removed; every `With…Clock` option now takes
  `clockwork.Clock` directly (ADR-0138, supersedes ADR-0003).** Outer stateful layers
  (the expression-evaluation timeout, the call-link ticker, the outbox relay ticker, the
  pgx-notifier backoff, and the casbin-watcher backoff) depend on
  [`jonboulle/clockwork`](https://github.com/jonboulle/clockwork)'s `Clock` interface in
  place of the deleted in-repo `clock.Clock`, **including
  `processtest.WithMemSchedulerClock`**. Consumers passing a `clock.Clock` value to any
  `With…Clock` option must migrate to a `clockwork.Clock` (e.g.
  `clockwork.NewRealClock()` / `clockwork.NewFakeClock()`). The pure engine core is
  unaffected — it stays clockwork-free and continues to receive time only via
  `Trigger.OccurredAt`. `processtest.FakeClock` itself is **not** a breaking change: the
  wrapper still exposes `Now`/`Advance`/`Set`. **Behavioral note:**
  `processtest.FakeClock.Advance` no longer no-ops on a non-positive duration (the old
  narrow fake guarded `d>0`); it now follows clockwork semantics — `Advance(0)` fires
  already-due waiters, and a negative duration moves time backward. All in-tree callers
  pass strictly positive durations, so nothing changes today.

- **Gateway constructors and `Builder` gateway methods now take functional options (ADR-0139).**
  `gateway.NewExclusive`/`NewParallel`/`NewInclusive`/`NewEventBased` change from
  `(id string, name ...string)` to `(id string, opts ...gateway.Option)`, and
  `Builder.AddExclusiveGateway`/`AddParallelGateway`/`AddInclusiveGateway`/`AddEventBasedGateway`
  change from `(id string, name ...string)` to `(id string, opts ...gateway.Option)`. Set a
  gateway's name with `gateway.WithName("…")` (and its human label with `gateway.WithLabel("…")`)
  instead of a trailing name string, e.g. `gateway.NewExclusive("decide", gateway.WithName("Decision?"))`.
  Bare id-only calls (`gateway.NewParallel("fork")`) are unaffected.

- **`scheduling` package renamed to `scheduler` and unified with the internal gocron engine.**
  The public import path is now `github.com/kartaladev/wrkflw/scheduler` (formerly
  `github.com/kartaladev/wrkflw/scheduling`). The internal gocron implementation relocated from
  `internal/scheduling/gocron` to `scheduler/internal/gocron`. Public signatures returning
  `scheduling.*` types now return `scheduler.*` equivalents (e.g. `persistence.NewSchedulerLocker`
  now returns `scheduler.Locker` instead of `scheduling.Locker`; `scheduler.Elector`,
  `scheduler.Scheduler`, etc. replace their `scheduling.*` counterparts).

- **Scheduler-owned durable jobs; `scheduler` is now a self-contained, spinnable-standalone
  library (ADR-0134).** The `runtime/kernel.Scheduler` / `kernel.JobStore` / `kernel.ScheduledJob`
  port that `runtime` previously depended on is **deleted from `kernel`**; `runtime.WithScheduler`
  now takes a `scheduler.Scheduler` directly, and `runtime.NewJobStore` returns a `scheduler.JobStore`.
  `kernel.ArmedTimer`, `kernel.TimerStore`, and `kernel.JobSpec` (+ `JobKind`) are unaffected and
  remain in `kernel`. The sentinels `ErrUnsupportedTrigger` and `ErrUnresolvedTimerDefinitions`
  move from `kernel` to `scheduler` (message prefix `workflow-scheduler:`, unchanged text otherwise).
  `scheduler.JobStore` gains a real `Save`/`Delete` write path (previously `LoadScheduled`-only,
  now `Load`/`Save`/`Delete`). **The old `AppliedStep.TimerArms`/`TimerCancels` fused-write
  mechanism is deleted** (`applyTimerOps` is gone from `Store.Create`/`Commit`); atomicity is now
  achieved by the runtime's own `jobStore.Save`/`deleteTimer` (routed through the new
  `kernel.TimerWriter` capability) running **inside the same state-commit transaction** as the
  step write (`kernel.TxRunner.RunInTx` / `JoinOrBegin`) — the scheduler itself is never called
  during commit (direct-save). New `scheduler.Job`/`scheduler.NewJob`/`scheduler.NewJobWithID`,
  `scheduler.ActivationType` (`ActivationAuto`/`ActivationManual`, `scheduler.WithManualActivation`),
  and `scheduler.Scheduler.Activate` close the fire-before-commit race this way: a Manual job's
  durable row is written inside the caller's own transaction, and only armed in-memory
  (`Activate`, an idempotent upsert-by-id) strictly **after** that transaction commits — a failed
  post-commit `Activate` is logged and benign, since the durable arm rehydrates on next boot.
  `scheduler.WithJobStore(kind, provide)` registers a per-`JobKind` store; on `NativeScheduler.Start`
  the scheduler self-rehydrates every registered kind (`Load` + `Activate` each). Job ids are
  unchanged engine timer ids — no composite id scheme. New observability:
  `wrkflw_scheduler_job_runs_total` counter and `wrkflw_scheduler_job_duration_seconds` histogram,
  emitted via gocron's native `MonitorStatus` hook (`scheduler.WithMeterProvider`).
  `go-co-op/gocron/v2` bumped to the pinned `v2.22.0` (ADR-0135). See `scheduler/example_test.go`
  for `NewScheduler`/`NewJob`/`Trigger`/`WithJobStore` usage.

- **Calendar (`Daily`/`Weekly`/`Monthly`) and cron triggers resolve their at-times in UTC by
  default on the live scheduler; opt into another zone with `scheduler.WithLocation`
  (ADR-0136, ADR-0137).** Previously the live scheduler fell through to the host's `time.Local`
  (the internal gocron engine never pinned a location). It is now pinned to UTC by default;
  `scheduler.WithLocation(*time.Location)` opts into `time.Local` or any named zone.
  `scheduler.Trigger.Next` resolves recurring at-times in the location of its `after` argument,
  and the runtime and the façade `Schedule()` pass `now` in the scheduler's configured location
  (new `scheduler.NativeScheduler.Location()`) — so the persisted/admin next-fire and
  `Schedule()`'s return match the live `Scheduled()`/`List()` instant under any location.
  Deployments running `TZ=UTC` (typical containers) are unaffected; a non-UTC host that intends
  host-local resolution passes `scheduler.WithLocation(time.Local)`. A consumer driving
  `Trigger.Next` directly with a non-UTC `after` now gets a result in that zone. Named zones
  resolve at-times per their DST rules on the live scheduler. In a multi-replica deployment
  every replica must use the same location. Caveat: a `Cron` trigger under a non-IANA
  `time.FixedZone` cannot schedule on the live engine (gocron resolves cron by zone name).
  (The former caveat that an `interval>1` calendar trigger's reported first-fire could differ
  from the live fire is closed by ADR-0140, below.)

- **`Trigger.Next`'s calendar (`Daily`/`Weekly`/`Monthly`) first fire is now interval-aware,
  matching the live scheduler for `interval>1` (ADR-0140).** `calendarNext` previously ignored
  `interval` on the day-by-day scan it uses to compute the first fire, so for an `interval>1`
  trigger whose current period's at-times had already passed, it returned the very next
  matching period-day instead of jumping by `interval` the way the live gocron engine does —
  the persisted/admin `NextRun` and the `Schedule()`-return value disagreed with the actual
  fire. `calendarNext` now accepts a scanned day only when its period index (day/week/month,
  anchored at the `after` instant) is a multiple of `interval`, converging exactly with gocron
  v2.22.0's own interval-jump logic (verified by an extended
  `TestNativeScheduler_ScheduleReturnMatchesLocation`, including a large-interval row that
  exercises the now-`interval`-scaled forward-scan bound). **Behavior change for `interval>1`
  calendar triggers only** — the value `Next` returns changes (to the correct one);
  `interval==1` and cron triggers are byte-identical to before. Pre-v0.1.0, no stability
  promise on this value.

- **`DefinitionRegistry.Lookup(ctx, defRef string)` → `Lookup(ctx, model.Qualifier)`;
  def-ref fields, params, and constructors now typed `definition.Qualifier` (ADR-0101).**
  The following Go symbols are now `definition.Qualifier` (or `model.Qualifier` internally)
  rather than `string`: `service.StartInstanceRequest.DefRef`,
  `service.DeliverMessageRequest.DefRef`, `engine.StartSubInstance.DefRef`,
  `activity.CallActivity.DefRef`, `kernel.OutboxEvent.DefinitionRef`,
  `kernel.ChainLink.{Predecessor,Successor}DefinitionRef`,
  `kernel.ChainLinkRef.DefinitionRef`, `chain.ChainEvent.PredecessorDefinitionRef`.
  Constructors `activity.NewCallActivity(id, ref model.Qualifier, …)` and
  `build.(*Builder).AddCallActivity(id, ref model.Qualifier, …)` take the typed value.
  `NewMapDefinitionRegistry` is now variadic (`...*model.ProcessDefinition`).
  The HTTP `def_ref` JSON key and the `definition_ref` TEXT database columns are
  **unchanged** — wire and schema remain byte-identical.
  Use `definition.Latest(id)`, `definition.Version(id, v)`, or `definition.ParseQualifier(s)`
  to construct a `Qualifier`.

- **`instance_id` removed from the start-instance request body; `StartInstanceRequest.InstanceID` removed.**
  Process-instance IDs are now server-generated (ADR-0100). Remove the `instance_id` field from
  any `POST /instances` request body and any direct use of `service.StartInstanceRequest.InstanceID`;
  the server mints the ID using `runtime/idgen.XID()` by default and returns it in the response.
  To use a different strategy, pass `service.WithIDGenerator(idgen.UUIDv7())` (or
  `idgen.Func(...)` in tests). The `instance_id` key is unchanged in all **response** bodies.
  Requests that address an existing instance (`DeliverSignal`, `CancelInstance`, `CompleteTask`,
  etc.) are unaffected.

- **`persistence.NewCachingInstanceStore` now requires a `cache.Provider` argument**
  (previously `runtime/kernel.NewCachingInstanceStore` took no provider; that name was itself
  renamed from `kernel.NewCachingStore` in ADR-0096 — full lineage: `kernel.NewCachingStore` →
  `kernel.NewCachingInstanceStore` → `persistence.NewCachingInstanceStore`). The type also
  moved from `runtime/kernel` to `persistence`. Supply `hotcache.New()` (the default) or any
  other `cache.Provider` from `persistence/cache/{hotcache,ottercache,rediscache,memcache}`.
  Consumers using `NewDurableProvider` / `NewMySQLDurableProvider` / `NewSQLiteDurableProvider`
  are unaffected — caching is wired automatically by the provider constructors.

- **`runtime.NewProcessDriver` is now all-optional.** The two required positional
  arguments (`cat action.Catalog`, `store kernel.InstanceStore`) have been replaced with
  functional options. A zero-argument call — `d, _ := runtime.NewProcessDriver()` — gives
  a fully usable in-memory, non-durable driver backed by `action.DefaultCatalog()`,
  `kernel.NewMemInstanceStore()`, and `runtime.DefaultDefinitionRegistry()`. A DEBUG log
  at construction reports the wired collaborators and advises how to go durable.
  - Supply your own catalog via `runtime.WithActionCatalog(cat)`.
  - Supply a durable store via `runtime.WithInstanceStore(store)`.
  - Supply an explicit definition registry via `runtime.WithDefinitions(reg)` (passing
    `nil` is a no-op — the default stands).
  - Populate the default catalog with `action.Register(name, fn)`,
    `action.RegisterFunc(name, fn)`, `action.MustRegister`, or `action.MustRegisterFunc`.
  - Populate the default definition registry with `runtime.RegisterDefinition(def)` or
    `runtime.MustRegisterDefinition(def)`.

- **`InstanceStore` / `MemInstanceStore` / `CachingInstanceStore` renames (breaking).**
  All references to the old names must be updated:
  - `kernel.Store` → `kernel.InstanceStore`
  - `kernel.MemStore` → `kernel.MemInstanceStore`; `kernel.NewMemStore(` → `kernel.NewMemInstanceStore(`; `kernel.MemStoreOption` → `kernel.MemInstanceStoreOption`
  - `kernel.CachingStore` → `kernel.CachingInstanceStore`; `kernel.NewCachingStore(` → `kernel.NewCachingInstanceStore(`; `kernel.CachingStoreOption` → `kernel.CachingInstanceStoreOption`
  - `persistence.Store` (the façade interface) → `persistence.InstanceStore`

- **`kernel.Token` → `kernel.Version`** — the optimistic-concurrency version scalar is
  now named `Version` throughout the kernel package.

- **`kernel.Outcome` → `kernel.ChainOutcome`** — the chain-outcome type is renamed to
  avoid colliding with the generic word "outcome".

- **`kernel.Ownership` → `kernel.InstanceOwnership`** — the ownership port interface is
  renamed for clarity.

- **`kernel.Publisher` → `kernel.OutboxPublisher`** (and `persistence.Publisher` alias
  → `persistence.OutboxPublisher`) — the outbox-publish port is renamed to be explicit
  about its role.

- **`action.Retryabler` → `action.RetryableError`** — the retry-classification interface
  is renamed to follow Go error-interface naming conventions.

- **`action.Default()` → `action.DefaultCatalog()`** — the zero-argument catalog accessor
  is renamed to be unambiguous.

- **Activity/event option-naming consolidation, deadline/wait split, and inline-action
  removal (ADR-0114).** Public option renames (hard renames, no deprecated aliases):
  - `activity.WithCompensation(a)` → `WithCompensateAction(a)` (field `CompensationAction`
    → `CompensateAction`).
  - `activity.WithCancelHandler(a)` → `WithCancelAction(a)` (field `CancelHandler` →
    `CancelAction`).
  - `activity.WithActionName(a)` → `WithTaskAction(a)`.
  - `activity.WithDeadline(t, flow, action)` / `event.WithCatchDeadline(t, flow, action)` →
    split into a mandatory `WithWaitDeadline(t schedule.TriggerSpec, flow string)` plus the
    new optional `WithDeadlineAction(action string)` (see Added below). `WithWaitDeadline`
    now rejects a recurring trigger at `Build`, returning the new sentinel error
    `ErrDeadlineTriggerRecurring`.
  - `activity.WithWaitReminder(t, action)` / `event.WithCatchWaitReminder(t, action)` →
    `WithWaitAction(t schedule.TriggerSpec, action string)` (accepts one-shot or recurring
    triggers). Backing fields rename `WaitFields.ReminderEvery` → `WaitEvery` and
    `ReminderAction` → `WaitAction`.
  - `event.WithStartMessage`/`WithCatchMessage`/`WithBoundaryMessage` → one
    `event.WithMessageCorrelator(msg, key string)` usable on Start/Catch/Boundary events.
  - `event.WithStartSignal`/`WithCatchSignal`/`WithBoundarySignal` → one
    `event.WithSignalName(name string)` usable on Start/Catch/Boundary events.
  - `event.WithThrowSignal(name)` → `event.WithThrowSignalName(name)`.
  - `processtest` harness: `WithAction`/`WithActionFunc` → `WithCatalogAction`/
    `WithCatalogActionFunc`.

  **Removed, no replacement:** `activity.WithAction`/`activity.WithActionFunc` (inline
  node-local action closures), `model.TaskAction.Inline`, `engine.InvokeAction.Inline`, and
  the inline-vs-name conflict check at `Build`. Every action now resolves by catalog name
  only — register it (`action.Register`/`action.RegisterFunc`) and reference it via
  `WithTaskAction` (or another `WithXxxAction` option). Definitions are consequently fully
  serializable: no node can carry a non-serializable closure.

  **Wire/YAML key renames** (persisted definitions serialized with the old keys will not
  decode — see the migration note below): `compensationAction` → `compensateAction`,
  `cancelHandler` → `cancelAction`, `reminderTrigger`/`reminderAction`/`reminderEvery` →
  `waitTrigger`/`waitAction`/`waitEvery`. The `service` instance JSON `inline` action-binding
  field is removed. **Unchanged:** `deadlineTrigger`/`deadlineFlow`/`deadlineAction`,
  `signalName`, `messageName`, `correlationKey`.

  **Migration note.** Persisted process definitions serialized with the old wire/YAML keys
  (`compensationAction`, `cancelHandler`, `reminderTrigger`/`reminderAction`/`reminderEvery`)
  will fail to decode after upgrading — re-author or re-serialize them with the renamed keys.
  Any definition relying on an inline node-local action closure must register that action in
  a catalog and reference it by name via `WithTaskAction` (or the matching `WithXxxAction`
  option) instead.

- **`DeliverMessage` drops its `def` parameter; `DeliverMessageRequest.DefRef` removed
  (ADR-0121).** `runtime.ProcessDriver.DeliverMessage(ctx, name, key, payload)` and
  `service.Engine.DeliverMessage` no longer take a target definition — message delivery is
  now def-less, matching `BroadcastSignal`. `service.DeliverMessageRequest.DefRef` is removed
  (`StartInstanceRequest.DefRef` is unaffected). Consumers correlating a message to a running
  instance must have that instance's definition registered with the driver's definition
  registry (resolved via `Lookup` at correlation time); an unregistered definition now fails
  correlation with `kernel.ErrDefinitionNotFound` instead of relying on the caller-supplied
  `def`. `BroadcastSignal` and `DeliverMessage` also change miss-branch behaviour: a signal or
  message with no waiter now additionally checks for a matching signal-/message-start event
  and creates an instance when one exists (previously always a no-op); definitions with no
  event-starts see no behaviour change.

- **`EventSubProcess` node kind removed; an event sub-process is now an `activity.SubProcess`
  with an event-triggered inner start (ADR-0122).** Deleted: `event.EventSubProcess`,
  `model.KindEventSubProcess`, `event.NewEventSubProcess`, `event.WithEventSubProcessNonInterrupting`,
  `event.EventSubProcessOption`, `build.(*Builder).AddEventSubProcess`, and the `"eventSubProcess"`
  wire discriminator (old JSON/YAML carrying it no longer unmarshals). Author an event sub-process
  as `activity.NewSubProcess(id, sub)` where `sub` has an event-triggered inner start; the new
  `event.WithNonInterrupting()` start option (`event.StartEvent.NonInterrupting`) carries the
  interrupting marker (default interrupting). New validation sentinel
  `model.ErrEventSubprocessOnFlow` rejects an event-triggered SubProcess that has an incoming
  sequence flow. Known limitation: `DeliverMessage` does not route to a message-triggered
  event-sub arm (pre-existing) — use `ApplyTrigger`.

- **`service.ProcessInstance` gains two methods** — `ActiveTask(nodeID string) (humantask.HumanTask, bool)`
  and `ActiveTasks() []humantask.HumanTask` — returning the open (Unclaimed|Claimed)
  human tasks of an instance, sorted ascending by `TaskID` (ADR-0142; the field was
  named `TaskToken` until ADR-0145 renamed it). Consumers
  who **embed** a ProcessInstance obtained from the engine need no code change but
  must **recompile**; consumers with a **hand-rolled** implementation must add the
  two methods, filtering `State().Tasks` by `humantask.IsOpen()`, returning a
  **non-nil** slice **sorted by `TaskID`** (`ActiveTasks`) and the **first** such
  match (`ActiveTask`).

### Fixed

- **`ScheduleJob` no longer reports a zero next-run for a past-due timer (ADR-0184, closes backlog
  42).** A one-shot whose absolute fire time had already elapsed is registered with
  `OneTimeJobStartImmediately` + `WithLimitedRuns(1)`, so gocron could run and retire it *before*
  `ScheduleJob` asked for its first-run time — returning `(time.Time{}, nil)`: a zero instant with a
  **nil error**, for a timer that was armed correctly and did fire. A caller could not distinguish
  that from "never scheduled". Measured (fresh re-derivation, 7 runs × 1000 arms each, fake clock):
  **~12 % of arms without `-race`** (848/7,000) and **~0.9 % under `-race`** (63/7,000) — the two
  modes differ by roughly 13×, confirming the race is real but far rarer under `-race`'s added
  synchronization. The fire-immediately case is now computed as the clock's current time rather than
  raced out of gocron, so after the fix the branch **cannot return zero by construction** — that is
  not a symmetric "0 of N" measurement, since the old racy path no longer exists to compare against.

  Scope: `ScheduleJob` is in an internal package and its only in-repo caller discards the returned
  instant, so no shipped behaviour depended on the wrong value — this hardens a contract rather than
  fixing an observed production failure. `GocronScheduler.NextRun(id)` is deliberately unchanged: a
  fired one-shot genuinely has no next run.

### Added

- **`service.WithoutEmbeddedDefinition()` — opt out of the `definition` embed (ADR-0144
  follow-up).** The embed stays the default: a marshalled `service.ProcessInstance` is
  self-contained, carrying the whole `*model.ProcessDefinition` under `definition`. That
  subtree is byte-identical for every instance of a definition and, on a ten-node graph, is
  the *larger* half of the document — so a UI polling a single instance, or a consumer
  assembling an aggregate from N `GetInstance` calls, re-ships the same template on every
  read. Building the engine with `service.WithoutEmbeddedDefinition()` drops the `definition`
  key from every document the facade hands out (start, get, signal, task and admin paths
  alike) while keeping `def_id` / `def_version`, so a slimmed document still identifies its
  template and the consumer can cache templates keyed by `(def_id, def_version)`. It is a
  marshalling policy only: `ProcessInstance.Definition()` still returns the resolved template
  in-process, and `service.NewProcessInstance` (hand-fabricated instances) always embeds.
  Note the shipped list endpoint is unaffected either way — `ListInstances` returns
  `kernel.InstanceSummary`, which never embedded a definition.

- **Optional human `label` on every node (ADR-0139).** Each node kind now carries an optional
  display label, set with `WithLabel("…")` (`activity.WithLabel`, `event.WithLabel` /
  `WithThrowLabel` / `WithCompensateThrowLabel`, `gateway.WithLabel`) and exposed via
  `Node.Label()`, which falls back to the node's `Name()` when no label was set. The label
  serializes as `"label"` in JSON (`omitempty`) and YAML; the raw value round-trips (an unset
  label is omitted, not baked in). `Name` is now documented as the node's *semantic/reference*
  name (code-facing), with `Label` as the human-facing display string.

- **Graceful shutdown for `runtime.ProcessDriver` (ADR-0133).** `ProcessDriver.Shutdown`
  now performs real admission control and in-flight drain: it rejects new externally-initiated
  work with `runtime.ErrDriverShuttingDown` (every exported entry point — `Drive`,
  `ApplyTrigger`, `DeliverMessage`, `BroadcastSignal`, `CancelInstance`, `ResolveIncident`,
  `ReverseInstance`, and timer-start fires) and waits for in-flight instance execution to
  complete before returning, bounded by the `ctx` deadline (or the new `WithShutdownTimeout`
  fallback when `ctx` carries none). On drain-deadline expiry it returns
  `runtime.ErrDrainTimeout` WITHOUT force-cancelling in-flight work. Added
  `runtime.WithShutdownTimeout(d)` and `ProcessDriver.IsShuttingDown()`. `service.Engine`
  inherits rejection automatically; its human-task ops (`ClaimTask`/`CompleteTask`/`ReassignTask`)
  reject before any task-store write. The owned scheduler is now closed via a deadline-raced
  closer so `Shutdown(ctx)` honours the `ctx` deadline when closing it (previously the close
  used gocron's internal stop timeout and ignored `ctx` — audit Finding 3).

- **Event-based start events: message, signal, and timer starts (ADR-0121).**
  A process definition may now declare multiple start events — up to one trigger-less
  **manual start** (BPMN's "none start", `ErrMultipleManualStarts` if more than one) plus any
  number of event-triggered starts, each with exactly one trigger family
  (`ErrAmbiguousStartTrigger`/`ErrEventStartMissingTrigger` otherwise). Reachability validation
  now walks from the union of all start nodes. `engine.StartInstance` gains `StartNodeID`
  (empty resolves the manual start, `ErrNoManualStart` if there is none); the driver resolves
  which start node fired and the engine only places the token.
  - **Signal start** — broadcast fan-out: `BroadcastSignal(ctx, name, payload)` now also
    creates one instance per registered definition with a matching signal-start, in addition
    to resuming parked waiters. Signal names need not be unique across definitions.
  - **Message start** — correlate-to-running-first, then create: `DeliverMessage` (see
    Breaking changes) resolves a running waiter by `(name, key)` first; on a miss it creates a
    new instance at the unique matching message-start (`ErrAmbiguousMessageStart` if more than
    one matches). New-instance dedup is via a deterministic `(messageName, correlationKey)`
    instance id plus `Store.Create`'s `ErrInstanceExists` — fully multi-replica and restart
    safe, no advisory lock, no new schema (the `runtime/chain.Chainer` pattern). Message-start
    name uniqueness is enforced at `RegisterDefinition`/`MustRegisterDefinition`
    (`ErrDuplicateMessageStart`).
  - **Timer start** — scheduler-driven, multi-replica safe via the existing
    `scheduler.Elector`. New explicit `runtime.ProcessDriver.RehydrateStartTimers(ctx) error`
    step (a sibling of `RehydrateTimers`) arms recurring/one-shot start timers by enumerating
    registered definitions; each fire creates one instance.
  - New opt-in `runtime/kernel.DefinitionLister` capability (`ListDefinitions(ctx)
    []*model.ProcessDefinition`) lets the event-start subsystem enumerate registered
    definitions for signal/message matching; `MemDefinitionRegistry` and
    `MapDefinitionRegistry` implement it, `CachingDefinitionRegistry` passes through. A
    registry that does not implement it disables event-based *start* (correlate-to-running
    still works).
  - A definition with only event-starts (no manual start) makes plain `Drive` error rather
    than silently doing nothing.
  - See `examples/scenarios/event_start` for a signal fan-out + message correlation walkthrough.

- **`definition.Qualifier`: typed process-definition reference (ADR-0101).**
  New value type `definition.Qualifier{ID string; Version int}` (`Version == 0` = latest)
  with helpers `definition.Latest(id)`, `definition.Version(id, v)`,
  `definition.ParseQualifier(s) (Qualifier, error)`, `q.IsLatest()`, and `q.String()`
  (`"id"` or `"id:version"`). JSON and YAML marshalers keep the wire byte-identical to the
  former string encoding. `ErrInvalidQualifier` (`"workflow-model: invalid qualifier"`)
  is returned for empty id, non-numeric/negative/zero explicit version (`:0` is rejected —
  zero is the reserved latest sentinel). `model.ParseQualifier` is re-exported from the
  `definition` root package as `definition.ParseQualifier`.

- **Server-generated process-instance IDs via pluggable `runtime/idgen` (ADR-0100).**
  New nested package `runtime/idgen` with three constructors: `XID()` (default — `github.com/rs/xid`,
  ~20-char lowercase base32hex, k-sortable, never errors), `UUIDv7()` (`github.com/google/uuid`
  NewV7, RFC 9562, propagates rare entropy errors), and `Func(fn)` (deterministic test adapter).
  `Generator` interface: `NewID() (string, error)`. New option `WithIDGenerator(gen)` on both
  `runtime.ProcessDriver` and `service.Engine` (nil-guarded, default `idgen.XID()`); mirrors the
  existing `WithClock` seam. `service.Engine.StartInstance` always mints the ID; the service option
  also threads the generator into the default driver so both layers agree on the strategy.

- **Token-based, BPMN-inspired engine core** — process definitions across 18 typed node
  kinds (start/end/error events, service/user/business-rule/send/receive tasks,
  exclusive/parallel/inclusive/event-based gateways, sub-process — embeddable as an event
  sub-process via an event-triggered inner start — call activity, boundary, and intermediate
  catch/throw/compensation events), token execution, and `expr-lang`-driven gateway routing.
  The vocabulary is BPMN-inspired, not BPMN-compatible; there is no BPMN2 XML loader.

- **Authoring** — the `definition` root package is a thin aggregator with two entry points:
  `definition.NewBuilder(id, version)` (fluent Go builder with one `Add<Kind>` per node kind)
  and `definition.NewLoader(r io.Reader)` (YAML). Types, validation and serialization live in
  `definition/model`; sequence flows in `definition/flow`; node constructors in
  `definition/{event,gateway,activity}`; the fluent builder in `definition/build`; the
  deserialization registration bundle in `definition/kinds`.

- **Service actions** — a name-resolved catalog (`action.Catalog`, `action.Action`,
  `action.ActionFunc`, `action.MapCatalog`, `action.Registry`) with definition-scoped and
  node-inline registration (three-tier resolution: inline → scoped → global). Built-in
  actions: `httpcall` (10 MiB body cap by default via `WithMaxResponseSize`), `email`,
  `transform`, and `logaction`. Service-action invocations time out after 30s by default
  (`runtime.WithActionTimeout`); a timeout surfaces as a retryable failure.

- **`activity.WithCompletionAction(name string)` — optional post-completion action hook on
  UserTask/ReceiveTask (ADR-0114).** Sets `ActivityFields.CompletionAction` (wire/YAML key
  `completionAction`, decode-only per existing YAML convention). When set,
  `handleHumanCompleted`/`handleMessageReceived` (`engine/step_triggers.go`) merge the
  completion's output vars, then invoke the named catalog action via the existing
  `InvokeAction`/`ActionCompleted` round-trip — parking the token as `TokenWaitingCommand` —
  before advancing; the action's return vars are merged and the token advances only once the
  action completes. Failure is governed by the host node's `RetryPolicy` and error boundary,
  identically to a service-task action failure: no new token state or failure model was
  introduced. Distinct from the existing `WithCompletionValidation` (which gates the
  completion input *before* it is accepted) — this option runs an action *after* the
  completion is accepted.
- **`activity.WithDeadlineAction(name string)` — optional standalone deadline-breach action
  (ADR-0114).** Split out of the old bundled `WithDeadline(t, flow, action)` (see the Breaking
  changes section above); pair it with the new mandatory `WithWaitDeadline(t, flow)` to
  attach a breach action only when one is actually wanted.

- **Default `DefinitionRegistry` for zero-config call activities (ADR-0097, follows ADR-0096).**
  `runtime.NewProcessDriver()` now wires `runtime.DefaultDefinitionRegistry()` automatically,
  giving `KindCallActivity` nodes a working registry without any `WithDefinitions` call —
  symmetric with how `action.DefaultCatalog()` works for service tasks. New API:
  - `runtime.DefaultDefinitionRegistry() *kernel.MemDefinitionRegistry` — returns the
    process-global mutable registry.
  - `runtime.RegisterDefinition(def *model.ProcessDefinition) error` — registers `def`
    into the global registry under both `"<ID>"` and `"<ID>:<Version>"`. Bare `"<ID>"`
    resolves to the most recently registered version. Returns `ErrDefinitionExists` if the
    exact `"<ID>:<Version>"` is already registered.
  - `runtime.MustRegisterDefinition(def *model.ProcessDefinition)` — panics on error
    (init-time wiring, mirrors `action.MustRegister`).
  - `kernel.MemDefinitionRegistry` — the new concurrency-safe, mutable sibling of the
    immutable `MapDefinitionRegistry`. Obtain with `kernel.NewMemDefinitionRegistry()`.
    New sentinel errors: `kernel.ErrNilDefinition`, `kernel.ErrEmptyDefinitionID`,
    `kernel.ErrDefinitionExists`.
  - **`runtime.WithDefinitions(nil)` is now a no-op** (nil-ignored, matching
    `WithActionCatalog` / `WithInstanceStore`). A nil argument no longer clobbers the
    default registry. Passing a non-nil registry overrides the default, as before. Tests
    needing a fully isolated, empty registry should pass
    `WithDefinitions(kernel.NewMemDefinitionRegistry())`.

- **Persistence** — SQL backends for **PostgreSQL 17**, **MySQL 8.0+**, and **SQLite**
  (`modernc.org/sqlite`, pure-Go, WAL, single-writer; single-node/test/embedded only) behind
  ONE neutral `internal/persistence/store` parametrized by `internal/persistence/dialect`
  (ADR-0081/0082). Capability interfaces `Notifier` (LISTEN/NOTIFY) and `Locker` (distributed
  advisory lock) are opt-in per dialect. Facade constructors `persistence.Open{Postgres,MySQL,SQLite}`
  and `persistence.Migrate{Postgres,MySQL,SQLite}` (plus a public `persistence.Migrator`).
  Optimistic-concurrency (CAS) writes, a transactional **outbox** relay with poison isolation +
  DLQ + redrive, hot-path caching (see below), and data-retention pruners.

- **Persistence caching layer (ADR-0099)** — a neutral `persistence/cache` port (`Cache`,
  optional `ValueCache` capability, `Provider`, generic `Codec[V]`) with **four swappable
  adapter subpackages**: `persistence/cache/hotcache` (`github.com/samber/hot`, **default**,
  in-memory), `persistence/cache/ottercache` (`github.com/maypok86/otter/v2`, in-memory
  alternative), `persistence/cache/rediscache` (`github.com/redis/go-redis/v9`, distributed),
  and `persistence/cache/memcache` (`github.com/bradfitz/gomemcache`, distributed). Each
  adapter lives in its own subpackage so its library dependency is optional. `CachingInstanceStore`
  is relocated from `runtime/kernel` into `persistence` and re-substrated onto the `Cache`
  port (all correctness-bearing behavior preserved: ownership gate, per-instance keyed locks,
  evict-on-`ErrConcurrentUpdate`, `AlwaysOwn` single-replica `Warn`, `Release`-evict-first).
  A new `CachingTaskStore` provides read-through / write-through point-read caching over
  `humantask.TaskStore` (set-wide queries `AssignedTo`/`ClaimableBy` are uncached in v1).
  Caching is **default-on** on all three `DurableProvider` constructors (`NewDurableProvider`,
  `NewMySQLDurableProvider`, `NewSQLiteDurableProvider`) using `hotcache` in-memory, `AlwaysOwn`
  + one-time Warn, instance TTL 5m, human-task TTL 30s. New `DurableOption`s:
  `WithCacheProvider`, `WithInstanceCacheProvider`, `WithHumanTaskCacheProvider`,
  `WithDurableInstanceCacheOwnership`, `WithDurableInstanceCacheTTL`,
  `WithDurableHumanTaskCacheTTL`, and `WithoutCache` (escape hatch). Definition caching is
  deferred; human-task query caching (`AssignedTo`/`ClaimableBy`) is deferred.

- **Runtime driver** — `runtime.ProcessDriver` wires the engine to persistence, scheduling,
  and actions; supporting pieces live in `runtime/{kernel,view,chain,task,signal,calllink,monitor}`.
  Stateful constructors fail fast, returning `(T, error)` and wrapping `kernel.ErrNilDependency`
  on a nil required dependency rather than panicking later.

- **Scheduling / waits** — `gocron`-driven timers, deadlines, and in-wait reminder actions;
  multi-replica timer exclusivity via advisory-lock leader election.

- **Resilience** — engine-modeled retry with backoff/jitter, incident creation on exhaustion,
  catch-flow handling, and a retryable-error contract (`action.IsRetryable` / `action.NonRetryable`).

- **Compensation** — optional per-node compensation actions, scope-targeted rollback, and
  best-effort cancel actions on instance cancellation.

- **Authorization** — pluggable `authz.Authorizer` with a casbin baseline (role,
  resource-privilege, and attribute/variable-based evaluation), a DB-backed policy adapter,
  and a runtime policy admin.

- **Eventing** — vendor-neutral eventing abstraction over watermill (in-process GoChannel
  publisher by default; broker wiring documented in `docs/eventing-brokers.md`), transactional
  `SendTask` messaging via the outbox (`message.*` topics), and event-driven process-instance
  chaining (`chain.Chainer`).

- **HTTP transports** — mountable route groups over a shared pure root:
  `transport/http/httpcore` (pure per-endpoint functions, DTOs with `go-playground/validator/v10`
  validation, `ClassifyError` with 5xx body redaction, `NewInstanceView`, health-probe
  evaluation, static-route-template observability, and the generic `RouteCustomizer[R]` /
  `CustomizeOption[R]` / `CustomizeConfig[R]` seam), plus three native adapters —
  `transport/http/stdlib` (`*http.ServeMux`), `transport/http/gin`, and `transport/http/fiber`
  (fiber v3). Each adapter exposes `InstanceRoutes`, `TaskRoutes`, `MessageRoutes`,
  `AdminRoutes`, and `HealthRoutes` structs plus `Mount`/`MountHealth` conveniences. Admin
  routes are **default-absent by composition** — they exist only when a consumer mounts
  `AdminRoutes` (with the desired admin-port fields set) on a router group their own auth
  middleware already protects. Import isolation: stdlib pulls no third-party transport
  dependency; gin/fiber consumers pull only their respective framework.

- **Service façade** — `service.Service` is the single transport-neutral application seam;
  the HTTP adapters are thin translators over it.

- **Observability** — OpenTelemetry metrics + traces and `slog` logging across runtime,
  transports, scheduling, eventing, and the persistence relay; SLI gauges/counters
  (`wrkflw_outbox_*`, `wrkflw_timers_armed`, `wrkflw_timer_fired_total`,
  `wrkflw_action_failures_total{action,retryable}`), `/healthz` + `/readyz` handlers, and
  reference `docs/dashboards/`, `docs/runbooks/`, and `docs/observability.md`.

- **Operability** — graceful `runtime.ShutdownGroup`, opt-in `persistence.WarnUnsafeConfig`,
  a `processtest` consumer test harness, example reference wiring under `examples/`, and
  `STABILITY.md` / `docs/production-checklist.md`.

- **Project** — Apache-2.0 license, contributor and security policies, and a GitHub Actions
  CI pipeline (build, race tests, lint, `gosec`/`bodyclose`/`errorlint`, vulnerability scan,
  CodeQL).

[Unreleased]: https://github.com/kartaladev/wrkflw/commits/main
