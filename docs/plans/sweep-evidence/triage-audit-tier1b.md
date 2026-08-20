# Triage: backlog items 104–126 (tier-1b slice)

Date: 2026-08-20. Source statements: `docs/plans/HANDOVER.md` (Security §4.5 tail, Operational
readiness §4.6, and the "Defects `AUDIT.md` does not contain at all" block).

Read-only triage. No repo file outside this one was modified; no ablation was committed; no
container was started.

Legend — **Tier**: `S` small (≤~100 lines, no new public API, no architectural decision) ·
`D` design (needs spec/ADR) · `A` adjudication (not a defect / closed / duplicate / owner call).

---

## 114 ⭐⭐ — `cloneState`'s `History` deep-copy is untested and its obvious test is vacuous

**Status: VERIFIED (by source reading; ablation deliberately NOT committed, per brief).**

### 1. Package(s) / file(s) and exact symbols

- `engine` — `engine/step_state.go:374`, inside `func cloneState(st InstanceState) InstanceState`
  (declared at `engine/step_state.go:361`):

  ```go
  s.History = append([]NodeVisit(nil), st.History...)
  ```

  This is the entire line. It carries **no comment**, unlike every other deep-copy in the same
  function (`Tasks`, `Timers`, `ArmedEvents`, `Boundaries`, `EventTriggeredSubprocesses`,
  `RootCompensations`, `Compensating.Records`, `Scopes`, `ArchivedCompensations`, `Incidents`,
  `DeferredCompensationThrows`, `RecentCompensationCmdIDs` all have a "why" comment). That
  asymmetry is itself part of the trap: the line reads like dead ceremony.
- The two mutation sites it protects, both in the same file:
  - `engine/step_state.go:252` — `func (s *InstanceState) openVisit(tokenID, nodeID string, at time.Time)`:
    `s.History = append(s.History, NodeVisit{NodeID: nodeID, TokenID: tokenID, EnteredAt: at})`
    → **append vector** (needs `cap > len` to corrupt).
  - `engine/step_state.go:183-194` — `func (s *InstanceState) openVisitFor(...) *NodeVisit` returns
    `&s.History[i]`; `closeVisit`/`closeVisitAs`/`setVisitTask` write `LeftAt`, `CloseKind`, `TaskID`
    **through that pointer, in place** → **in-place vector** (corrupts at any capacity, but only when
    the caller's base History already holds an *open* visit that this Step closes).
- Type: `engine.NodeVisit` (`engine/state.go:248-262`) — fields `NodeID`, `TokenID`, `EnteredAt`,
  `LeftAt *time.Time`, `TaskID`, `CloseKind`. Note `LeftAt` is a **pointer**, so even the current
  line is only a *slice-level* copy: the pointees stay shared. Not the item's defect, but worth a
  sentence in whatever comment lands (see fix sketch).

### 2. Green-when-ablated claim — confirmed by reading

`grep -rn "History:" engine/*_test.go` returns exactly **three** fixtures in the whole package:

| file:line | fixture | why it cannot observe the aliasing |
|---|---|---|
| `engine/step_cancel_test.go:38` | 2 visits, composite literal (`len == cap`) | never calls `Step`/`cloneState` — calls `cancelTokenWaits(s, …)` directly on `*InstanceState`; asserts only `s.Timers` and token presence |
| `engine/step_human_test.go:476` | 1 open visit (`approve`, `LeftAt == nil`), composite literal | calls `Step` **once**, and the Step is expected to **error** (`require.ErrorIs(err, humantask.ErrTaskNotFound)`); nothing is asserted about the input state's `History` |
| `engine/step_state_test.go:250` | 3 visits, composite literal | never calls `Step` — exercises `s.openVisitFor(...)` directly |

The two tests that *do* Step twice from one base are `TestStepIsDeterministic`
(`engine/step_test.go:79-92`) and `TestStepDoesNotMutateInput` (`engine/step_test.go:94-124`) —
and **both build a base with `History` left at its nil zero value**. `append(nil, …)` allocates a
fresh array on every call, so with the line ablated they still pass. `TestStepDoesNotMutateInput`
asserts `in.Tokens`, `in.Variables`, `in.Scopes` — **never `in.History`**; its own header comment
(`engine/step_state.go:383`) cites it as the gate for `Tasks`, which is accurate, and that citation
is exactly what makes a reader assume History is covered too. *A cited test is not a covering test.*

⇒ **No test in `engine` can distinguish `s.History = append([]NodeVisit(nil), st.History...)` from
`s.History = st.History`.** Zero coverage, VERIFIED by enumeration of the only three History
fixtures plus the only two multi-Step-from-one-base tests.

### 3. Tier

**`S`** for the test (≤~40 lines, no API change). ⚠ But it is **blocking for item 73**, which is
`D`: item 73 wants this exact line ablated for a 1,880× step-cost win, and that is a real
architectural change (History must stop being copied per Step — e.g. append-only journal, or
copy-on-write). **The test must land first, on `main`, before item 73 is designed**, or item 73's
implementer deletes the line, sees EXIT=0, and ships state corruption.

### 4. Fix sketch (≤12 words for the table)

Add `TestCloneStateDeepCopiesHistory` with a `cap > len` base + two `Step`s; comment the line.

Longer: mirror `TestCloneStateDeepCopiesRecentCompensationCmdIDs`
(`engine/state_recent_compensation_cmd_ids_test.go:56`) — that test already solved this exact
"shared spare capacity" problem for the cmd-id ring and documents it at lines 50-55. Also give
`step_state.go:374` the missing why-comment naming both vectors, so the next reader of item 73
cannot mistake it for ceremony.

### 5. Falsifiable test note — the fixture shape that WOULD fail

**What makes it fail today: nothing — it passes today and fails only under the ablation.** That is
the point: this is a *regression guard for a change item 73 will make*, so its RED must be produced
by mutation (`cp` backup → replace line 374 with `s.History = st.History` → observe RED → restore →
`diff`), exactly as `RecentCompensationCmdIDs` was mutation-verified.

⚠ **`cap == len` does not reproduce it.** The required shape:

```go
// engine/state_test.go (package engine_test)
func TestCloneStateDeepCopiesHistory(t *testing.T) {
	at := time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)

	// cap > len is LOAD-BEARING: a composite literal ([]engine.NodeVisit{v}) has
	// cap == len, so both Steps reallocate and the corruption disappears.
	h := make([]engine.NodeVisit, 1, 4)
	h[0] = engine.NodeVisit{NodeID: "seed", TokenID: "t0", EnteredAt: at}

	base := engine.InstanceState{InstanceID: "i1", History: h /* + whatever linearDef needs */}

	require.Greater(t, cap(base.History), len(base.History),
		"control: without spare capacity both Steps reallocate and this test cannot fail")

	a, err := engine.Step(t.Context(), def, base, engine.NewStartInstance(at, nil), engine.StepOptions{})
	require.NoError(t, err)
	require.Greater(t, len(a.State.History), 1, "control: the Step must have appended a visit")
	firstEntered := a.State.History[1].EnteredAt

	// Second Step from the SAME base, four hours later.
	later := at.Add(4 * time.Hour)
	_, err = engine.Step(t.Context(), def, base, engine.NewStartInstance(later, nil), engine.StepOptions{})
	require.NoError(t, err)

	assert.Equal(t, firstEntered, a.State.History[1].EnteredAt,
		"History aliased — the second Step overwrote the first Step's visit in the shared backing slot")
}
```

The measured corruption in the HANDOVER entry (`workA/09:00` → `workA/13:00`) is precisely
`a.State.History[1].EnteredAt` changing from the first Step's clock to the second's.

**Two controls are mandatory and both are in the sketch**: the `cap > len` assertion (a future
refactor to a composite literal silently disarms the test) and the `len(a.State.History) > 1`
assertion (if the chosen `def` appends no visit, the test is vacuous — this repo has shipped that
exact failure, cf. `assert.Empty(state.Boundaries)` on a definition with no boundary node).

**Second test worth adding (cheap, catches the other vector):** base with one *open* visit
(`LeftAt == nil`) for a token the Step advances; after `Step`, assert `base.History[0].LeftAt` is
still `nil`. This one fails under ablation at **any** capacity, because `closeVisit` writes through
`&s.History[i]`. It is the assertion `TestStepDoesNotMutateInput` should have had all along, and it
belongs next to the existing `in.Scopes` assertions at `engine/step_test.go:118-123`.

### 6. Dependencies / conflicts

- **Pairs with item 73 (not in this slice) — hard ordering: 114 before 73.** Land the guard on
  `main` first.
- Touches `engine` only ⇒ strictly serial with every other `engine` item in this backlog
  (115, 124, and any 73 work). Fan-out by package does not help here.
- No conflict with 125 (`internal/persistence/store` / `trigger_codec.go`).
- `engine` is container-free; `go test -count=1 ./engine/...` is the verification command.
- ⚠ `engine/` mixes `package engine` and `package engine_test` — `head -1` the target test file
  before writing into it. `engine/state_test.go` is `package engine_test`;
  `engine/step_state_test.go` is `package engine`.

---

## 116 — `runtime/monitor`'s collector options are unreachable from outside the module

**Status: VERIFIED.**

### 1. Package(s) / file(s) and exact symbols

- `runtime/monitor` — `runtime/monitor/stats_collector.go`:
  - `:36` `func NewOutboxStatsCollector(r kernel.OutboxStatsReader, opts ...observability.Option) *OutboxStatsCollector`
  - `:116` `func NewTimerStatsCollector(r kernel.TimerStatsReader, opts ...observability.Option) *TimerStatsCollector`
  - `:9` `import "github.com/kartaladev/wrkflw/internal/observability"`
- The option type is `internal/observability/observability.go:41` `type Option func(*config)`;
  `:65` `func WithMeterProvider(mp metric.MeterProvider) Option`; `:78` `func New(...)`.
- The **public** root package `observability/` exists but exports only `NewHandler` and
  `NewLogger` (`observability/handler.go:32,37`) — **no `Option`, no `WithMeterProvider`**.

### 2. Scope of the leak — measured, not assumed

```
grep -rn "^func [A-Z]…(.*observability\.\|^func ([^)]*) [A-Z]…(.*observability\." \
  --include=*.go . | grep -v ^./internal/ | grep -v _test
→ runtime/monitor/stats_collector.go:36
→ runtime/monitor/stats_collector.go:116
```

**Exactly two exported symbols in the whole module leak an `internal/` type into a public
signature, and both are in `runtime/monitor`.** Every other public package that needs OTel
configuration already uses the correct pattern — its *own* functional-option type holding the
internal option in an unexported field:

| package | own option type | internal option kept where |
|---|---|---|
| `runtime` | `func(*ProcessDriver)` (`runtime/processdriver_options.go:250-262`) | `driver.logOpt/tpOpt/mpOpt` (unexported) |
| `runtime/calllink` | `func(*CallNotifier)` (`runtime/calllink/notifier.go:91-103`) | `n.logOpt/tpOpt/mpOpt` (unexported, `:47-49`) |
| `runtime/chain` | `func(*chainerConfig)` (`runtime/chain/chainer.go:103-115`) | `cfg.obsOpts` (unexported, `:80`) |
| `transport/http/httpcore` | `cfg.Logger/TracerProvider/MeterProvider` fields (`observability.go:41-52`) | local `opts` slice |
| `runtime/task` | `cfg.mp` (`runtime/task/service.go:173-177`) | local `obsOpts` slice |

So `runtime/monitor` is a **one-package outlier against five in-repo precedents**, not a design
gap. That makes it `S`, not `D`.

**Consequence today**: an external consumer can construct the collectors (both readers are public
via `runtime/kernel`) but cannot pass *any* option, so `observability.New` falls back and both
DB-truth gauge sets (`wrkflw_outbox_pending/_dead/_oldest_pending_age_seconds`, `wrkflw_timers_armed`)
are pinned to the OTel **global** MeterProvider. A consumer running a non-global provider gets
silence, with no compile error at the call they *can* write.

### 3. Tier — `S`

Copy the `runtime/calllink` shape verbatim. No architectural decision; ADR-0004 already settled
that `internal/` must not surface (this is "audit finding N"'s shape again).

### 4. Fix sketch

Add `monitor.Option`/`monitor.WithMeterProvider`/`WithLogger`/`WithTracerProvider` mirroring
`runtime/calllink/notifier.go:91-103`; change both variadics to `...monitor.Option`.

### 5. Falsifiable test note

**What makes it fail today: the call does not compile.** The proof cannot be an in-module test
(`runtime/monitor/stats_collector_test.go:144,212` already pass `observability.WithMeterProvider(mp)`
and compile fine — they are *inside* the module, which is exactly why this shipped). Two options,
both falsifiable:

1. **Preferred, and it is the only honest gate**: an `Example` in `runtime/monitor` that uses the
   **new** `monitor.WithMeterProvider` — plus a `go vet`-visible compile-time assertion. Falsifies
   on the signature, not on internal reachability.
2. **The real guard**: a repo-level test that AST-walks every non-`internal`, non-`_test` package
   and fails if an exported func/method signature names a type from `…/internal/…`. This is the
   same "derive the set from the sources, never hard-code a count" technique already used in
   `engine/state_recent_compensation_cmd_ids_test.go:26-45` (it imports `go/ast`, `go/parser`).
   **It fails today on exactly the two lines above** — that is its RED, observable without any
   mutation, and it also permanently closes the ADR-0004 class rather than this instance.

⚠ Do **not** write "external module compiles the doc recipe" as a test — it needs a network/module
scaffold; keep it as a manual delivery-gate step.

### 6. Dependencies / conflicts

- **Blocks item 108.** 108's recipe cannot be made to compile by editing the doc; 116 must land
  first (or 108's fix must delete the option from the recipe and document the global-provider
  limitation as the honest current state).
- `runtime/monitor` is **not** in the container-free list; `go build ./runtime/... && go vet ./...`
  is the cheap check that no consumer breaks.
- Same class as audit finding N (ADR-0004 internal-leak). Consider folding both into one bundle.

---

## 108 — `docs/observability.md`'s wiring recipes do not compile

**Status: VERIFIED — every sub-claim, including the two the audit missed.**

### 1. File and exact defects

`docs/observability.md`. Three separate problems:

**(a) Collector-wiring recipe, `docs/observability.md:71-108`** — verified against source:

| doc text | reality | verdict |
|---|---|---|
| `import ".../runtime"` then `runtime.NewOutboxStatsCollector` | symbol lives in `runtime/monitor` (`stats_collector.go:36`) | wrong package |
| `collector, err := runtime.NewOutboxStatsCollector(...)` | returns **one** value `*OutboxStatsCollector`, no `error` (`:36`) | assignment mismatch |
| `import ".../observability"` + `observability.WithMeterProvider(mp)` | public `observability/` exports only `NewHandler`, `NewLogger` (`handler.go:32,37`) | undefined |
| `runtime.OutboxStatsReader` / `runtime.TimerStatsReader` (prose) | `runtime/kernel.OutboxStatsReader` / `.TimerStatsReader` (`runtime/kernel/opsstats.go:35,41`) | wrong package |
| `runtime.NewTimerStatsCollector(...)` (`:97`) returning `(collector, err)` | `runtime/monitor.NewTimerStatsCollector`, one return (`:116`) | same two errors again |
| closing prose "Both constructors accept the same `observability.Option` variadic … or omit it to use the global provider" (`:110-112`) | true only *inside* the module — see **116** | misleading |

**(b) Health-probe recipe, `docs/observability.md:120-142` — the recipe the audit never mentions.**
It imports `github.com/kartaladev/wrkflw/rest`:

```
grep -rn "wrkflw/rest" .   → only docs/observability.md:122 (and HANDOVER's own note)
ls -d rest                 → NO rest/ DIRECTORY
```

**`github.com/kartaladev/wrkflw/rest` does not exist anywhere in the module.** `go mod tidy`/`go
build` on this snippet fails at resolution, before any type error. The three symbols it names —
`rest.NewHealthHandler`, `rest.WithHealthCheck`, `rest.HealthCheck` — **do not exist under any
package**:

```
grep -rn "func NewHealthHandler\|func WithHealthCheck" --include=*.go .  → 0 hits
```

The real API is:
- `transport/http/httpcore.HealthCheck` (interface, `httpcore/health.go:16`), `HealthCheckFunc`
  (`:32`), `EvaluateReady` (`:44`);
- mounting is `stdlib.MountHealth(mux, checks...)` (`transport/http/stdlib/mount.go:26`) or
  `stdlib.HealthRoutes{Checks: []httpcore.HealthCheck{...}}.Customize(mux)`
  (`transport/http/stdlib/groups.go:460-465`), registering `GET /healthz` and `GET /readyz`.

The one thing the recipe gets right: `persistence.NewRelayBacklogCheck(r kernel.OutboxStatsReader,
opts ...RelayBacklogOption)` (`persistence/relay_health.go:53`), `WithMaxDead` (`:21`),
`WithMaxPending` (`:29`) all exist as written. But the doc's closing sentence "returns a
`rest.HealthCheck`" names a type that does not exist; it returns `RelayBacklogCheck`.

**(c) Two inventory gaps.**

- **Admin table (`:157-167`) omits exactly one route.** `grep -n '"/admin' transport/http/stdlib/groups.go`
  → **15** registrations; the doc's 9 rows cover 14 of them (the `policies`/`role-bindings` row
  covers 6). The single missing one is `POST /admin/instances/{id}/compensation/resolve-stall`
  (`transport/http/stdlib/groups.go:247`, gin `:275`, fiber `:264`, `httpcore/dto.go:139` —
  ADR-0175's operator escape verb).
- **Metric inventory (`:11-60`) lists 18 and misses exactly 5 real instruments** — enumerated and
  each source-verified as a live instrument name, not a table or a test fixture:

  | missing metric | declared at |
  |---|---|
  | `wrkflw_eventing_published_total` | `internal/eventing/watermill/publisher.go:52` |
  | `wrkflw_human_task_audit_drops_total` | `internal/persistence/store/humantask_store.go:106` |
  | `wrkflw_scheduler_job_runs_total` | `scheduler/internal/gocron/monitor.go:33` |
  | `wrkflw_scheduler_job_duration_seconds` | `scheduler/internal/gocron/monitor.go:37` |
  | `wrkflw_timer_arms_refused_total` | `runtime/observability.go:56` (ADR-0176) |

  ⚠ Also stale-by-tone: `wrkflw_timer_fired_total` and `wrkflw_action_failures_total` are still
  labelled "**New this release**" and the gauge section is headed "(new this release)" — 40+ ADRs
  later. Sweep those while editing.

### 2. Tier — `S` for the doc edit, but **gated by 116**

The doc edit itself is <100 lines. But per **116** the collector recipe is **uncompilable from
outside the module no matter what the doc says**. Order:

1. **116 first** (`runtime/monitor` gets its own `Option`), then
2. **108** rewrites the recipe against the shipped symbols.

If the owner declines 116, then 108 must ship the *honest* version: no option argument, plus an
explicit "these gauges currently bind to the OTel global MeterProvider; per-provider wiring is not
reachable from outside the module (backlog 116)". Silently "correcting the paths" produces a
second, subtler false document.

### 3. Fix sketch

After 116: repoint recipes at `runtime/monitor` + `runtime/kernel` + `httpcore`/`stdlib.MountHealth`,
drop the `err`, delete `wrkflw/rest`, add `resolve-stall` and the 5 metrics.

### 4. Automated check that would keep the doc true

Doc-only ⇒ no unit test. Three checks, in increasing value:

1. **Compile the recipes.** Extract each ```go block into `examples/observability_wiring/` as a
   real, built `main` (or an `Example` in a `docs`-adjacent package). `go build ./...` then fails
   the moment a recipe rots. This is the standing repo pattern (`examples/` = reference wiring) and
   is the only check that would have caught **all six** wiring defects. ⚠ It would **not** have
   caught 116, because an in-module example compiles — 116 needs the AST guard in its own entry.
2. **Metric-inventory drift test**: a test that greps the module for `Int64Counter(`/
   `Float64Histogram(`/`Int64ObservableGauge(` string literals matching `^wrkflw_` and asserts every
   one appears in `docs/observability.md`. **It fails today on the 5 metrics above** — a real RED,
   no mutation needed.
3. **Admin-route drift test**: same idea over `'"/admin'` literals in `transport/http/stdlib/groups.go`.
   **Fails today on `resolve-stall`.** Cheap, and `transport/http` is container-free.

Both (2) and (3) are derive-from-source, never a hard-coded count — the failure mode
`engine/state_recent_compensation_cmd_ids_test.go:17-21` warns about.

### 5. Dependencies / conflicts

- **Hard dependency on 116** (stated above).
- Overlaps **106** (readiness doc/health-check surface) and **107** (timer lateness metric): if
  either adds a metric or a `HealthCheck` impl, the inventory edit should be folded into that
  bundle instead of shipped twice.
- Overlaps **111** (`ReverseInstance(WithTargetNode)` is on no HTTP route) — if 111 adds a route,
  the admin table changes again.
- No Go code changes ⇒ no package-serialization concern, unless the `examples/` compile-check
  option is taken (then it touches `examples/`).

---

## 104 — 4xx bodies echo internals, including the verbatim ABAC predicate source on 403

**Status: VERIFIED, including the "twice" and the "five classes" counts.**

### 1. Package / file / symbol

- `transport/http/httpcore` — `transport/http/httpcore/errors.go`,
  `func ClassifyError(err error) (int, ErrorBody)` (`:26`), and `type ErrorBody` (`:19`).
- The predicate text originates in `internal/expreval` —
  `internal/expreval/expreval.go:135`:
  `return false, fmt.Errorf("workflow-expreval: run %q: %w", code, err)`
  (and `:139` for the non-bool case, same `%q`).
- Reached through **two** authorizers, both wrapping with `%w` so `errors.Is` finds
  `authz.ErrNotAuthorized` and `ClassifyError` picks the 403 arm:
  - `internal/authz/casbin/authorizer.go:70` — `fmt.Errorf("%w: attribute predicate: %w", authz.ErrNotAuthorized, err)`
  - `authz/authz.go` `RoleAuthorizer.Authorize` — same wrap.

### 2. The counts, re-derived

**Five 4xx classes echo `err.Error()` verbatim** — read off `errors.go` directly, not inherited:

| status | line | body |
|---|---|---|
| 404 | `:31` | `{Error:"not_found", Message: err.Error()}` |
| 403 | `:33` | `{Error:"forbidden", Message: err.Error()}` |
| 409 | `:35` | `{Error:"conflict", Message: err.Error()}` |
| 400 | `:50` | `{Error:"bad_request", Message: err.Error()}` |
| 422 | `:56` | `{Error:"conflict_state", Message: err.Error()}` |
| 500 | `:58` | `{Error:"internal_error"}` — **Message empty, correct** |

⇒ the item's warning holds exactly: **a "generic 400/422" fix leaves 403 — the worst case —
untouched.** `ErrorBody.Message` is `json:"message,omitempty"`, so blanking is a one-field change.

**"Twice" — verified in the dependency, not assumed.** `expr-lang`'s `file.Error.Error()`
(`$GOMODCACHE/github.com/expr-lang/expr@v1.17.8/file/error.go:17,72-78`) formats as
`"<message> (<line>:<col>)<snippet>"`, and `Bind` (`:23-63`) builds `Snippet` from the **source
line itself** plus a `....^` caret. So the predicate source appears **once from `%q` in
expreval.go:135 and once again inside expr's own snippet** — two verbatim copies of the ABAC
expression, carrying whatever process/actor variable names the deployment's policy names.

### 3. Tier — `D`

Not `S`: deciding *what* a 4xx may say is a public-API contract change (`ErrorBody.Message` is an
exported field consumers parse) and it trades operability for confidentiality. Needs an ADR
covering: which sentinels keep a message, whether a correlation id replaces it, whether the raw
error is logged at the transport seam instead, and the migration note for consumers matching on
`message`. ⚠ Do **not** blanket-blank all five — `400 bad_request` and `409 conflict` messages are
the actionable ones a consumer needs.

### 4. Fix sketch

Give `ClassifyError` per-class message policy: static text for 403 (+ log raw), keep 400/409/422.

### 5. Falsifiable test note

`transport/http/httpcore` is **container-free**. Table test over `ClassifyError`:

- **`TestClassifyErrorDoesNotEchoPredicateSource`**: build the real 403 error by calling
  `authz.RoleAuthorizer{}.Authorize(ctx, authz.AuthzSpec{Attribute: "vars.internalApprovalLimit > actor.attributes.tier"}, actor, vars)` with an env that makes `EvalBool` error, then assert
  `body.Message` does **not** contain `"internalApprovalLimit"`.
  **What makes it fail today:** `errors.go:33` sets `Message: err.Error()`, and that string
  contains the identifier twice (expreval `%q` + expr snippet). Real RED, no mutation needed.
- **Control that must accompany it:** `require.Error(t, err)` on the Authorize call *and*
  `require.Contains(t, err.Error(), "internalApprovalLimit")` **before** classifying — otherwise a
  predicate that silently evaluates to `false` (returning bare `ErrNotAuthorized`, `authorizer.go:73`)
  makes the assertion pass vacuously. ⚠ This is the exact trap: **the deny path and the eval-error
  path both produce 403, and only the eval-error path leaks.**
- Keep a positive case asserting 400/409 messages are still present, so the fix cannot over-blank.

### 6. Dependencies / conflicts

- Overlaps **103** (negative/deny-list ABAC predicates fail OPEN) — same file
  `internal/authz/casbin/authorizer.go` and the same `EvalBool` seam. 103 changes *whether* an error
  is produced; 104 changes what happens to it. **Fixing 103 first changes 104's fixture** (more
  predicates will error rather than silently allow). Sequence 103 → 104, or bundle them.
- Overlaps **101/124** (tamper-evident trail / forged actor) only insofar as both want the raw error
  logged rather than returned.
- `transport/http` is container-free ⇒ verification is `go test -count=1 ./transport/http/...`.

---

## 105 — the default email sender silently downgrades to plaintext

**Status: VERIFIED, including the item's own correction of the audit.**

### 1. Package / file / symbols

- `action/email` — `action/email/email.go`:
  - `:49-55` `type smtpSender struct{}` / `func (smtpSender) send(...) error { return smtp.SendMail(addr, auth, from, to, msg) }`
  - `:74-79` `const ( tlsModeNone tlsMode = iota; tlsModeStartTLS; tlsModeImplicit )` — **`tlsModeNone`
    is `iota` 0, i.e. the zero value, i.e. the default when neither `WithTLS` nor `WithStartTLS` is
    passed.** Comment on `:77` says so verbatim: `// default: use smtpSender (smtp.SendMail)`.
  - `:337` `return map[string]any{"emailSent": true, "recipientCount": sent}, nil`
- The enforcing alternatives already exist and are correct:
  `action/email/starttls.go:46-49` **rejects** a server that does not advertise STARTTLS
  (`"workflow-email: server does not support STARTTLS"`); `action/email/tls.go` does implicit TLS.
  So the fix is a **default change**, not new machinery.

### 2. The audit correction — confirmed against the stdlib

`smtp.SendMail` is **opportunistic**, not plaintext-only. `$GOROOT/src/net/smtp/smtp.go:338-346`:

```go
if ok, _ := c.Extension("STARTTLS"); ok {
    config := &tls.Config{ServerName: c.serverName}
    ...
    if err = c.StartTLS(config); err != nil { return err }
}
```

No `else`, no error. Strip `250 STARTTLS` from the greeting and the session continues in the clear,
`SendMail` returns nil, and `Do` returns `{"emailSent":true, "recipientCount":n}, nil` — **success
and silent-downgrade are indistinguishable to the process definition.** The item's phrasing is
right and the audit's "unconditional plaintext" would have been wrong.

⚠ **One refinement the item does not state, and it narrows the blast radius:** the downgrade is
only *silent* when **no auth is configured**. With `WithAuth(...)`,
`$GOROOT/src/net/smtp/auth.go:61-68` (`plainAuth.Start`) returns `errors.New("unencrypted connection")`
for a non-TLS, non-localhost server, so `SendMail` errors and `Do` returns a retryable error. The
credential is protected; **the message body is not.** Any spec written for this must say so — an
unqualified "auth protects you" would be a false claim, and an unqualified "credentials leak" would
be too.

### 3. Tier — `D`

The obvious fix (flip the default to enforced STARTTLS) is **breaking** for every consumer whose
relay is a plaintext in-cluster MTA. Needs an ADR: new default, an explicit
`WithInsecurePlaintext()` opt-out, and the CHANGELOG/`STABILITY.md` note. The code change is small;
the decision is not.

### 4. Fix sketch

Default `tlsMode` to `tlsModeStartTLS`; add explicit `WithInsecurePlaintext()` opt-out; ADR + CHANGELOG.

### 5. Falsifiable test note

`action/email` is pure Go (no container — it dials an in-test `net.Listener` speaking SMTP).

- **`TestNewEmailDefaultsToEnforcedSTARTTLS`**: stand up a fake SMTP server that **omits**
  `250-STARTTLS` from its EHLO response; build the action with neither TLS option; assert `Do`
  returns an error and **no** `emailSent` output.
  **What makes it fail today:** `email.go:77` makes `tlsModeNone` the zero value → `smtpSender` →
  `smtp.SendMail` takes the no-`else` branch above, sends in the clear, and returns nil, so today's
  code returns `{"emailSent":true}, nil`. Real RED against the current default.
- **Mandatory control:** a second case where the fake server **does** advertise STARTTLS, asserting
  the send succeeds — otherwise the first assertion passes for any unrelated dial failure and the
  test proves nothing about STARTTLS.
- ⚠ **Do not test through `WithSender`/`SenderFunc`** (`email.go:43-47`): substituting the sender
  bypasses `smtp.SendMail` entirely, so such a test is structurally incapable of observing the
  downgrade. The fixture must be a real listener.

### 6. Dependencies / conflicts

- `SECURITY.md` currently assigns SMTP TLS to the embedder; that sentence must be corrected in the
  same bundle (it mitigates nothing, since the embedder cannot see the downgrade).
- Independent of every other item in this slice — `action/email` is touched by nothing else here.
- Breaking-change coordination with `STABILITY.md` / `CHANGELOG`, which item **113** also touches.
  If both ship in one window, write the CHANGELOG entries together.

---

## 106 — readiness cannot see the scheduler, elector or notifier

**Status: VERIFIED, including the ⚠ comment-trap correction.**

### 1. Packages / files / symbols

- Interface: `transport/http/httpcore` — `type HealthCheck interface { Name() string; Check(ctx) error }`
  (`transport/http/httpcore/health.go:16`), `HealthCheckFunc` (`:32`), `EvaluateReady` (`:44`).
- Mounting: `transport/http/{stdlib,gin,fiber}` — `MountHealth(..., checks ...httpcore.HealthCheck)`
  (`stdlib/mount.go:26`, `gin/mount.go:22`, `fiber/mount.go:23`).
- **Exactly two shipped check types**, both in `persistence`, re-derived by grepping constructors:
  - `persistence.PingCheck` — `persistence/health.go:62 NewPingCheck` (pgx), `:83 NewMySQLPingCheck`, `:104 NewSQLitePingCheck` (three constructors, **one** type)
  - `persistence.RelayBacklogCheck` — `persistence/relay_health.go:53 NewRelayBacklogCheck`
  (`httpcore.healthCheckFunc`, `health.go:22`, is the inline adapter, not a shipped probe.)
- Nothing in `scheduler`, `runtime`, `runtime/calllink` or `scheduler/internal/gocron/pgelector`
  implements `HealthCheck`.
- Elector: `scheduler/internal/gocron/pgelector/elector.go` — `func (e *PostgresElector) IsLeader(ctx) error`
  (`:186`).

### 2. The ⚠ correction — confirmed, the audit's parenthetical is a comment trap

`elector.go:196-197` reads:

```go
// Sticky fast-path: still leader, no DB round-trip. The heartbeat is what
// catches a silently-lost lock and flips this flag back.
```

That is a **description of working ADR-0061 machinery**, not an admission of a gap. Anyone
"fixing" what that comment appears to say would be fixing nothing.

**The real gap is the metric.** `grep` for a leadership instrument returns zero hits, so the
audit's own proposed alert (`is_leader == 0`) has no series to evaluate. Compare the metrics that
*do* exist for the scheduler (`wrkflw_scheduler_job_runs_total`, `_job_duration_seconds` —
`scheduler/internal/gocron/monitor.go:33,37`): leadership is the one scheduler state with no
instrument at all.

### 3. Tier — `D`

Two coupled decisions, neither mechanical: (a) which subsystems get a shipped `HealthCheck`
(scheduler running? elector holding? notifier connected?) and what "unready" means for each — a
readiness failure de-registers the pod, so a *follower* replica reporting 503 for "not leader"
would be catastrophic; (b) adding `wrkflw_scheduler_is_leader`. Needs an ADR fixing the
leader-vs-ready semantics before any code.

### 4. Fix sketch

Ship `scheduler.NewLeadershipCheck`/`NewSchedulerRunningCheck` + a `wrkflw_scheduler_is_leader` gauge; ADR the ready-vs-leader semantics.

### 5. Falsifiable test note

- `transport/http` and `httpcore` are container-free; the ablation in the original probe (register
  a failing leadership check → 503) already proves `EvaluateReady` works, so **do not re-test that**
  — it would be a test of shipped machinery, not of the fix.
- The falsifiable test is on the **new** check: `TestLeadershipCheckUnavailableWhenNotLeader` with a
  stub elector returning `ErrNotLeader`, asserting `Check` returns non-nil and `Name()` is stable.
  **What makes it fail today: the symbol does not exist** (compile error = valid RED).
- For the gauge: an OTel `sdkmetric.NewManualReader` test asserting `wrkflw_scheduler_is_leader` is
  collected with value 0 when the stub elector refuses. **Fails today: the instrument is never
  registered, so the reader collects zero matching metrics.**
- ⚠ **Vacuity guard**: assert the metric is *present with value 0*, never merely `NoError` — a
  collector that registers nothing also produces no error.

### 6. Dependencies / conflicts

- Feeds **108** (the doc's health-probe recipe and metric inventory both change).
- `pgelector` is Postgres-only ⇒ its tests need Docker. **Design the check against the
  `Elector` interface** and unit-test with a stub so the new tests stay container-free; leave the
  real-Postgres path to the existing pgelector integration suite.
- Independent of 107, but both add instruments — if shipped together, one CHANGELOG/doc edit.

---

## 107 — timer lateness is not measured

**Status: VERIFIED, including the ⚠ "computed once" correction.**

### 1. Packages / files / symbols

- Discard site: `runtime/monitor` — `runtime/monitor/stats_collector.go:136-144`, the
  `NewTimerStatsCollector` callback:

  ```go
  stats, err := c.reader.Stats(ctx)
  ...
  o.ObserveInt64(g, stats.Armed)   // ← only Armed is observed
  ```

  `kernel.TimerStats` carries **both** `Armed` and `NextFireAt *time.Time`
  (`runtime/kernel/opsstats.go:28-30`), and the store genuinely computes `NextFireAt` in SQL on
  every call (`internal/persistence/store/timerstore.go:395` and `:419` both
  `return kernel.TimerStats{Armed: armed, NextFireAt: nextFireAt}`). **The data is fetched from the
  DB every scrape and thrown away** — the fix costs one extra `ObserveInt64`, not a new query.
- The single existing lateness computation: `scheduler/internal/gocron/job_schedule.go:109`
  `lateness := now.Sub(at)`, guarded by `if oneShot { if at, ok := trig.AbsTime(); ok && !at.After(now) {`
  (`:112-113`) and emitted only as `s.tel.Logger.Warn("workflow-scheduler: past-due timer exceeds
  time-skew tolerance; firing immediately", …)` (`:115-119`).

  ⇒ the ⚠ correction is right and must be honoured in the spec: lateness **is** computed, but only
  for a **one-shot that is already past due at arm time**, only when it exceeds `s.timeSkew`, and
  only as a log line. Writing "lateness is not measured anywhere" would be a false claim.
  **A recurring timer, and a one-shot that goes overdue *after* being armed — the 45-minute
  stalled-scheduler case — reach this code never.**

### 2. Tier — `S`

One gauge derived from data already in hand. `wrkflw_timers_next_fire_age_seconds` (or
`_oldest_overdue_seconds`) computed as `max(0, now - NextFireAt)`, mirroring the existing
`wrkflw_outbox_oldest_pending_age_seconds` (`stats_collector.go:65-68`) exactly — same shape, same
file, same nil/zero convention. No architectural decision.

⚠ One design detail to settle inline, not in an ADR: `NextFireAt` is `*time.Time` and
`internal/persistence/store/pruner.go:250` notes it can be `0001-01-01` (ADR-0181). Observe `0`
for nil **and** for the zero time; do not emit a ~2000-year age.

### 3. Fix sketch

Observe a second gauge from the `NextFireAt` already returned by `TimerStats`; clamp nil/zero to 0.

### 4. Falsifiable test note

`runtime/monitor` has an existing pattern to copy: `runtime/monitor/stats_collector_test.go:154-212`
(`TestTimerStatsCollector`) already drives a stub `kernel.TimerStatsReader` through an OTel
`ManualReader`.

- **`TestTimerStatsCollectorReportsOverdueAge`**: stub returns `TimerStats{Armed: 7, NextFireAt:
  &fortyFiveMinutesAgo}`; assert the collected metric set contains
  `wrkflw_timers_next_fire_age_seconds ≈ 2700`.
  **What makes it fail today: the collector registers exactly one instrument
  (`wrkflw_timers_armed`, `stats_collector.go:128`), so the assertion finds no such metric.** This
  is the probe's measured "1 instrument" result, turned into an assertion. Real RED, no mutation.
- **Table cases that must accompany it, or the fix ships a lie**: `NextFireAt == nil` → 0;
  `NextFireAt == time.Time{}` → 0 (the ADR-0181 pruner case); `NextFireAt` in the **future** → 0,
  not negative.
- ⚠ Use a fixed clock, not `time.Now()`, in the assertion — see **126**.

### 5. Dependencies / conflicts

- **Same file as 116.** 116 changes `NewTimerStatsCollector`'s signature; 107 changes its body.
  **Sequence 116 → 107**, or bundle them — `runtime/monitor` is one package, so they cannot run as
  concurrent subagents.
- Feeds **108**'s metric inventory (and the alert list, which currently has no timer-lateness alert).
- `runtime/monitor` is **not** container-free; its own tests use stubs, so `go test ./runtime/monitor/...`
  is fine, but a full `./runtime/...` sweep is not.

---

## 109 — `OpenSQLite` never checks the single-writer contract

**Status: VERIFIED, including the causal correction.**

### 1. Packages / files / symbols

- `persistence` — `func OpenSQLite(ctx context.Context, db *sql.DB, opts ...Option) (InstanceStore, error)`
  at `persistence/sqlite.go:66`. Its whole body is: `database.From(db)` → `database.ProbeUTC(ctx, q,
  database.SQLite)` → `store.New(db, dialect.NewSQLite(), opts...)`. **No `db.Stats()` inspection,
  no `PRAGMA busy_timeout` probe, no `MaxOpenConnections` assertion.**
- The contract it fails to enforce *is* documented, in its own godoc (`persistence/sqlite.go:52-54`):
  "…and for setting `db.SetMaxOpenConns(1)` to enforce single-writer serialisation." So this is a
  **documented-but-unchecked** invariant, not an undocumented one — which is the strongest possible
  case for a startup check.
- `persistence.DeploymentProfile` — `persistence/unsafe_config.go:18-24`. **Exactly 5 fields**,
  re-counted: `MultiReplica`, `CallLinksEnabled`, `CallLinkLeaseWired`, `HistoryCapSet`,
  `PruningScheduled`. **None is dialect-aware**, and `WarnUnsafeConfig` (`:30-43`) evaluates exactly
  three rules, none about SQLite.

### 2. The causal correction — honour it, it changes the fix

The item's correction is load-bearing: **pool > 1 alone is not sufficient** (0 failures in 4 runs
*with* `busy_timeout`; 174–195 of 200 failures in 4–17 ms *without* it). So a check that only
asserts `MaxOpenConnections == 1` would fire on safe deployments and stay silent on the actually
dangerous one. **The dangerous configuration is `pool > 1` AND `busy_timeout == 0`** — and per
**117** the second half is the *default* for anyone following the shipped documentation.

### 3. Tier — `D`

Not `S`: the choice is *reject vs warn*. `OpenSQLite` returning an error on `MaxOpenConns != 1`
is breaking for existing consumers; a `slog.Warn` is not, but is ignorable. That is an ADR
decision, and it should be taken **together with 117** since the two form one story.

### 4. Fix sketch

`OpenSQLite` probes `db.Stats().MaxOpenConnections` and `PRAGMA busy_timeout`; warn or reject per ADR.

### 5. Falsifiable test note

**Container-free** — `dbtest.RunTestSQLite` is pure-Go (`internal/dbtest/sqlite.go`).

- **`TestOpenSQLiteFlagsUnsafePool`**: `sql.Open` a temp SQLite DB with `SetMaxOpenConns(8)` and a
  DSN **without** `_pragma=busy_timeout(...)`; call `OpenSQLite`; assert the chosen signal (error,
  or a captured `slog` record via a `slog.Handler` test double).
  **What makes it fail today: `persistence/sqlite.go:66-79` inspects neither property**, so today
  it returns `(store, nil)` and emits nothing. Real RED.
- **Mandatory second case (this is the causal correction as an assertion)**: `MaxOpenConns(8)`
  **with** `busy_timeout` set → **no** signal. Without this case the test would drive a fix that
  warns on a configuration measured to have **zero** failures, and the warning would be noise
  consumers learn to ignore.
- ⚠ **Do not build the fixture with `dbtest.RunTestSQLite`** — it applies WAL, `busy_timeout(5000)`
  and `SetMaxOpenConns(1)` for you (`internal/dbtest/sqlite.go:28`), i.e. it constructs precisely
  the *safe* configuration, making the unsafe-path assertion structurally unable to fail. Open the
  `*sql.DB` by hand for this test.

### 6. Dependencies / conflicts

- **Bundle with 117** — 117 supplies the correct `_pragma=busy_timeout(5000)` syntax the check
  should verify, and 117's doc fix without 109's check leaves the old copy-pasted DSNs unflagged.
- Related to backlog **112** (`db.Stats()` unexposed) — both want `db.Stats()`; 109 reads it once at
  open, 112 wants it continuously. Same accessor, different lifetimes; no conflict.
- Related to backlog **81** (relay turns SQLite engine commits into hard `SQLITE_BUSY`), which is
  the same root cause seen from the runtime side.

---

## 117 — ADR-0082's documented SQLite DSN is inert

**Status: VERIFIED — and there is a second, worse instance the item does not name.**

### 1. Files / exact text

- `docs/adr/0082-sqlite-backend.md:38-41`, the "Driver and DSN" list:
  - `_journal_mode=WAL`
  - `_busy_timeout=5000` — "5-second busy-wait on locked pages before returning `SQLITE_BUSY`"
  - `_foreign_keys=on`

  All three are **mattn/go-sqlite3 syntax**. The locked driver is `modernc.org/sqlite`
  (CLAUDE.md tech-stack table, hard pin, this same ADR), whose parameter form is
  `_pragma=<name>(<value>)`. The repo's own working code proves the correct syntax:
  `internal/dbtest/sqlite.go:28` `"&_pragma=busy_timeout(5000)"` and
  `examples/migrate/main_test.go:21` (same string). Unrecognised `_`-prefixed keys are ignored
  silently, so `PRAGMA busy_timeout` reads **0**.

- ⭐ **Second instance, not in the item — and it is on the more-copied surface.** The godoc example
  in `persistence/sqlite.go:60`, i.e. the example a consumer reads from `pkg.go.dev` for the
  function they are about to call:

  ```go
  db, _ := sql.Open("sqlite", "file:app.db?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
  ```

  This one uses the **correct** `_pragma=` syntax — and **omits `busy_timeout` entirely.** Per
  **109**'s measurement that is the 174–195/200-failure configuration, arriving as the library's own
  canonical example. Fixing only the ADR leaves this untouched. `ls`-level check:

  ```
  grep -rn "busy_timeout" --include=*.go .
  → internal/dbtest/sqlite.go:28
  → examples/migrate/main_test.go:21
  ```
  **Two hits, both test-only. No non-test, consumer-facing example sets it.**

### 2. Tier — `S`

Doc + godoc string edits. The *behavioural* half (enforce/warn) is **109** and is `D`; keep them in
one bundle but the edit here is small.

### 3. Fix sketch

Rewrite ADR-0082's DSN list to `_pragma=` form; add `busy_timeout(5000)` to `OpenSQLite`'s godoc example.

### 4. Automated check that would keep it true

Doc/godoc ⇒ no unit test on its own. Two real options:

1. **Executable godoc.** Convert `OpenSQLite`'s example into a real `Example_openSQLite` in
   `persistence` (Go rule #6 already asks for testable examples on consumer-facing API). A DSN typo
   then fails `go test ./persistence/...`. ⚠ This catches *syntax*, not a *missing* pragma.
2. **The pragma assertion** — the only check that catches the missing `busy_timeout`: after opening
   with the documented DSN, `SELECT * FROM pragma_busy_timeout` and assert it is `5000`.
   **This fails today for both strings**: mattn syntax → 0, and the godoc example → 0. Real RED for
   each, container-free.
   This is also exactly the probe **109**'s startup check needs, so write it once.

### 5. Dependencies / conflicts

- **Bundle with 109.** 117 without 109 fixes the words while every already-deployed DSN stays inert.
- Interacts with backlog **81** (relay → hard `SQLITE_BUSY`): 117 is one of its two preconditions.
- Doc-only + one godoc string ⇒ no package-serialization concern.

---

## 118 — the same `SQLITE_BUSY` reaches callers under two identities

**Status: VERIFIED for the defect. ⚠ TWO of the item's own counts are WRONG, and correcting one of
them reveals a SECOND unmapped site in the same function.**

### 1. Package / file / exact symbol

- `internal/persistence/store` — `internal/persistence/store/store_core.go`,
  `func (s *Store) Create(ctx context.Context, step kernel.AppliedStep) (kernel.Version, error)`
  (declared `:64`). The unmapped branch is `:98-101`:

  ```go
  ); err != nil {
      if s.dialect.IsUniqueViolation(err) {
          return 0, kernel.ErrInstanceExists
      }
      return 0, fmt.Errorf("workflow-store: create: insert instance: %w", err)   // ← no mapConflict
  }
  ```

- The mapper it skips: `func (s *Store) mapConflict(err error) error` (`:322-328`) —
  `if s.dialect.IsRetryableConflict(err) { return kernel.ErrConcurrentUpdate }`.
- ⇒ dialect-neutral, exactly as the item says: `IsRetryableConflict` is what recognises a Postgres
  serialization failure (40001) and a MySQL deadlock (1213) too, so **all three dialects** leak raw
  driver text out of this one INSERT.

### 2. ⚠ Count corrections (counting lens)

Re-derived with `awk` over the function line ranges, not inherited:

| item's claim | measured | verdict |
|---|---|---|
| "all four other `Create` paths … call it" | `:105, :108, :116, :121` → **4** | ✅ **correct** |
| "and all eight in `Commit`" | `:236, :259, :264, :269, :276, :283` → **6** | ❌ **WRONG — it is 6, not 8** |

⭐ **And the miscount hides a real second defect.** `Commit` (`:188-291`) has **nine** error returns;
six map, **three do not**:

| `Commit` branch | line | maps? |
|---|---|---|
| `transaction.JoinOrBegin` → `"commit: begin"` | `:205-209` | ❌ **raw** |
| `json.Marshal` → `"marshal snapshot"` | `:216-220` | ❌ raw (not a DB error — correct as-is) |
| `UPDATE` exec | `:236` | ✅ |
| `res.RowsAffected()` → `"rows affected"` | `:240-244` | ❌ **raw** (driver call) |
| `writeJournal` / `writeOutbox` / `maybeNotify` / `flipCallLink` / `q.Commit` | `:259,264,269,276,283` | ✅ |

**`Create` has the identical unmapped `begin`**, at `:67-69`:
`return 0, fmt.Errorf("workflow-store: create: begin: %w", err)`.

⇒ the true finding is **not** "one outlier INSERT" but **"`Create`'s instance-INSERT, plus the
`begin` and `RowsAffected` paths in both `Create` and `Commit`, return raw driver errors."** A fix
scoped to the single line the item names would leave the `begin` paths leaking, and a consumer
retrying on `kernel.ErrConcurrentUpdate` would still miss them. **Fix the class, not the line.**

⚠ The `~93% unclassified / ~7% ErrConcurrentUpdate` split is inherited from the probe and is
**not re-derived here** — measuring it needs a contended SQLite run. `ASSUMPTION (unverified)` as
to the ratio; the mechanism above is VERIFIED.

### 3. Tier — `S`

Wrap the identified branches in `s.mapConflict(...)`. No API change (`kernel.ErrConcurrentUpdate`
already exists and is already the documented sentinel), no new decision. ~6 lines.

⚠ One judgement call to state in the commit message, not an ADR: keep `IsUniqueViolation` →
`ErrInstanceExists` **checked first**, so a genuine duplicate-instance INSERT is not reclassified as
retryable contention. The fix is `mapConflict` on the *fallthrough* only.

### 4. Fix sketch

Route `Create`'s insert/begin and `Commit`'s begin/RowsAffected fallthroughs through `s.mapConflict`.

### 5. Falsifiable test note

`internal/persistence/store` is **not** container-free as a package, **but `dbtest.RunTestSQLite`
starts no container** — use it.

- **`TestCreateMapsRetryableConflict`**: drive a contended `Create` on a SQLite DB opened
  **without** `busy_timeout` and with `SetMaxOpenConns(>1)` (per **109**, that reproduces
  `SQLITE_BUSY` at 87–97%), assert `errors.Is(err, kernel.ErrConcurrentUpdate)`.
  **What makes it fail today: `store_core.go:101` returns `fmt.Errorf(... %w, err)` wrapping the raw
  driver error, so `errors.Is` against `ErrConcurrentUpdate` is false.** Real RED.
  ⚠ Flaky-by-construction — prefer the deterministic form below and keep this as an optional
  integration check.
- **Deterministic and preferred**: a fake `dialect.Dialect` whose `IsRetryableConflict` returns true
  for a sentinel, and a `*sql.DB` stub whose INSERT returns that sentinel. Assert
  `errors.Is(err, kernel.ErrConcurrentUpdate)` at **each** of the four sites named above (INSERT,
  `Create` begin, `Commit` begin, `RowsAffected`). Table-driven, one case per site.
  **What makes each case fail today: the corresponding branch never calls `mapConflict`.**
- **Control that keeps it honest**: a case where `IsRetryableConflict` returns **false** asserting
  the original error still passes through unchanged, and a case where `IsUniqueViolation` is true
  asserting `kernel.ErrInstanceExists` — otherwise the fix can regress duplicate detection into
  "retry forever".

### 6. Dependencies / conflicts

- **Reads on 109/117** for the SQLite fixture (a DB with `busy_timeout` set cannot produce the
  error). If 109/117 land first, this test's fixture must deliberately opt *out* of the safe DSN.
- Same package as **125** (`store.Create` SIGSEGV on nil `AppliedStep.Trigger`) — **both are in
  `Store.Create`**, so they must be one serial change or one bundle, never two concurrent agents.
- Same package as backlog **81**.

---

## 110 — `ErrDrainTimeout` abandons an in-flight step with no cancellation, no log, no guidance

**Status: VERIFIED, all four sub-claims.**

### 1. Package / file / symbols

`runtime` — `runtime/driver_shutdown.go` (75 lines, the whole file):

- `:24` `var ErrDrainTimeout = errors.New("workflow-runtime: shutdown drain timed out")`
- `:49-61` `func (driver *ProcessDriver) waitInflight(ctx context.Context) error` — the only producer:
  `case <-ctx.Done(): return fmt.Errorf("%w: %w", ErrDrainTimeout, ctx.Err())` (`:58-59`).
- **No cancellation, by design and stated three times in the godoc**: `:22-23`
  ("In-flight work is NOT force-cancelled; it keeps running to completion on its own goroutine"),
  `:47-48`, and `runtime/processdriver.go:312` ("…WITHOUT force-cancelling in-flight work").
  ⇒ the probe's `ctx.Err() == <nil>` inside the action is the **documented** behaviour, not a bug.
- **"zero log lines (the file has no logger)" — VERIFIED structurally.** The file's entire import
  block (`:3-9`) is `context`, `errors`, `fmt`, `runtime/kernel`. **No `log/slog`, and `driver.obs`
  is never referenced.** A drain timeout is therefore invisible in logs even though the driver *has*
  a telemetry handle (`runtime/observability.go:18 tel observability.Telemetry`).
- **"The checklist mentions 'shutdown' 0 times" — VERIFIED**: `grep -ci shutdown docs/production-checklist.md` → **0**.

### 2. Tier — `D`

Three separable decisions, and the wrong pick on any of them is worse than today:

1. **Log** the timeout (with the in-flight count) — uncontroversially good, and alone is `S`.
2. **Cancel** in-flight work after the deadline — this is the ADR-worthy one. Today's non-cancellation
   is *deliberate and documented*, and the probe's own evidence supports it: `os.Exit(7)` mid-tx left
   **0 rows**, i.e. the store's transactional boundary is what makes abandonment safe. Flipping to
   force-cancel would need a matching argument about action idempotency.
3. **Guidance** in `docs/production-checklist.md`.

Splitting 1+3 (`S`) from 2 (`D`) is the right shape; do not let the easy log fix be blocked on the
cancellation debate.

### 3. Fix sketch

Log the drain timeout with the in-flight count via `driver.tel`; add a shutdown section to the checklist; ADR the cancel-vs-abandon question separately.

### 4. Falsifiable test note

`runtime` is **not** container-free as a package, but `waitInflight` needs no DB.

- **`TestShutdownLogsDrainTimeout`**: admit a slot via `driver.admit()`, never release it, call
  `Shutdown` with a 50 ms deadline, and assert a captured `slog` record (test `slog.Handler`) at WARN
  or ERROR naming `ErrDrainTimeout`.
  **What makes it fail today: `runtime/driver_shutdown.go` imports no logger at all** — zero records
  are emitted, so any assertion on log output fails. Real RED, observable without mutation.
- ⚠ **Do not** write a test asserting `ctx.Err() != nil` inside the action unless decision 2 is taken —
  that would assert against documented behaviour and put the test and three godoc comments in conflict.
- ⚠ Use `clockwork.NewFakeClockAt` if the test needs a clock — see **126**.

### 5. Dependencies / conflicts

- `docs/production-checklist.md` is also touched by **109/117** (SQLite `busy_timeout`) and **112**
  (pool metrics). If several land in one window, do one checklist edit.
- Independent of everything else in `runtime` here except **121**, whose example *also* never calls
  `driver.Shutdown` — fixing 121 gives 110's guidance a correct example to point at. **Do 110's
  checklist section and 121 together.**

---

## 111 — repair verbs below instance granularity are missing

**Status: VERIFIED, including the ⚠ "there is no move-token is FALSE" correction.**

### 1. Packages / files / symbols

- The verb **does** exist: `runtime` — `runtime/processdriver_reverse.go`
  - `:47` `func WithTargetNode(nodeID string) ReverseOption`
  - `:77` `func (driver *ProcessDriver) ReverseInstance(ctx, def, instanceID string, opts ...ReverseOption) (engine.InstanceState, error)`
  - Guards at `:89` (mutually exclusive with `WithFullReverse`) and `:92` (empty node ID).
- **Reachability, re-derived:**
  ```
  grep -rn "ReverseInstance" --include=*.go service/ transport/ | grep -v _test
  → (no output)
  ```
  **Zero hits.** It is on `*runtime.ProcessDriver` only — **not on `service.Service`, not on any
  HTTP route** in `stdlib`/`gin`/`fiber`. A consumer who mounted the transports and talks to the
  engine through `service` cannot reach it at all. ⇒ the item's correction is exactly right, and it
  changes the fix from "build a move-token verb" to "**expose the one that already exists**".
- Constraint the item names, confirmed in the godoc (`processdriver_reverse.go:32-46`): the target
  must have **completed** and declared a compensate action. So `WithTargetNode` is not a general
  move-token; it is a compensating rewind.
- The reproduced scenario: `ResolveIncident` re-drives with the same input (that is its documented
  contract) — so a poison input loops. Its route exists
  (`POST /admin/instances/{id}/incidents/{incidentID}/resolve`, `transport/http/stdlib/groups.go:233`).

### 2. Tier — `D`

Two decisions, neither mechanical: (a) does `ReverseInstance` become part of `service.Service`
(a public interface change consumers implement/mock) and a new admin route — and if so, with what
authorization, given ADR-0095's default-absent admin composition; (b) is a true
*move-token-without-compensating* verb added, which `WithTargetNode` explicitly is not. (b) is a
much larger design question than (a) and should not be smuggled in with it.

⚠ **Do not restate the audit's framing.** Anyone writing this spec must open with the correction:
the missing thing is **exposure**, not the capability. Writing "there is no move-token" would
re-inject the false premise into a new bundle.

### 3. Fix sketch

Promote `ReverseInstance`+`WithTargetNode` onto `service.Service` and an `AdminRoutes` verb; ADR the semantics.

### 4. Falsifiable test note

`service` and `transport/http` are both container-free.

- **`TestAdminReverseInstanceRoute`** in `transport/http/parity` + per-adapter suites: POST the new
  route, assert 200 and the resumed node.
  **What makes it fail today: the route does not exist** → 404 (compile error first, since the
  `AdminRoutes` field is absent). Valid RED.
- ⚠ **The parity suite will NOT catch a route added to only one adapter** — backlog **96** measured
  exactly that (adding `/auditprobe` to stdlib only left `./transport/...` at EXIT=0). So the new
  route must be added to the parity suite's route list **by hand, in the same commit**, or it is
  unguarded across gin/fiber. State this in the plan; it is the known failure mode.
- **Vacuity guard on the service test**: assert the returned state's resume node **and** that
  variables were restored, not merely `NoError` — `ReverseInstance` returns a zero
  `engine.InstanceState` on several guard paths (`:89`, `:92`).

### 5. Dependencies / conflicts

- Adds an admin route ⇒ **108**'s admin table changes again. Sequence 111 before 108's doc edit, or
  accept a second doc pass.
- Touches `service` **and** `transport/http/{httpcore,stdlib,gin,fiber,parity}` — five packages, one
  compile unit in practice. **Serial, or one agent**; a shared interface change is exactly the
  "stays inline in the controller" case.
- Related to **96** (parity blindness) — consider fixing 96 first so 111's route is auto-guarded.

---

## 112 — DB pool saturation is invisible

**Status: VERIFIED — the searches genuinely return zero.**

### 1. Packages / files / symbols

Re-derived, non-test, whole repo:

```
grep -rn "db.Stats()\|MaxOpenConnections\|InUse\|WaitCount\|pool.Stat()" --include=*.go . | grep -v _test
→ (no output)
```

**Zero hits.** No symbol, no metric name, no call site — neither `*sql.DB.Stats()` (MySQL/SQLite)
nor `*pgxpool.Pool.Stat()` (Postgres) is called anywhere. Confirmed against the metric inventory
too: the 27 real `wrkflw_*` instrument names contain nothing pool-related.

The mitigation the item cites is real: the consumer constructs and owns the pool
(`persistence.OpenPostgres`/`OpenMySQL`/`OpenSQLite` all take an already-open handle —
`persistence/sqlite.go:66` takes `db *sql.DB`), so `db.Stats()` is reachable **from the consumer's
side**. **Low is the right severity.**

### 2. Tier — `S`

An optional `persistence.NewPoolStatsCollector(db)` in the same shape as
`runtime/monitor.NewOutboxStatsCollector` (observable gauges, no goroutine, read in the callback).
No architectural decision — the consumer already owns the handle, so this is a convenience
collector, opt-in by construction.

⚠ Two handle types (`*sql.DB` and `*pgxpool.Pool`) with different stat structs ⇒ either two
constructors (matching the existing `NewPingCheck`/`NewMySQLPingCheck`/`NewSQLitePingCheck` triple
in `persistence/health.go:62,83,104`) or a tiny reader interface. Prefer the triple — it is the
in-repo precedent and needs no new abstraction.

### 3. Fix sketch

Add `persistence.NewPoolStatsCollector` gauges (`in_use`, `idle`, `wait_count`) mirroring the outbox collector.

### 4. Falsifiable test note

Container-free via `dbtest.RunTestSQLite` (pure Go) for the `*sql.DB` variant.

- **`TestPoolStatsCollectorObservesInUse`**: open a SQLite `*sql.DB`, hold one connection
  (`db.Conn(ctx)`), collect through an OTel `sdkmetric.NewManualReader`, assert
  `wrkflw_db_pool_in_use == 1`.
  **What makes it fail today: the constructor does not exist** (compile error = valid RED); after it
  exists, the mutation is deleting the `ObserveInt64` for `in_use`.
- ⚠ **Assert a specific value, never just presence.** A collector that registers a gauge and
  observes nothing still yields a metric name in some readers — the probe must pin `1`, and a second
  case must pin `0` after `conn.Close()`.
- The `*pgxpool.Pool` variant needs Docker; keep it in the existing Postgres integration suite and
  do **not** make it the gate.

### 5. Dependencies / conflicts

- Shares `db.Stats()` with **109** (which reads `MaxOpenConnections` once at open). No conflict —
  109 reads at construction, 112 reads per scrape. Land 109 first if both are queued; its accessor
  is the smaller change.
- Feeds **108**'s metric inventory and **110**'s checklist. Same doc-edit window.

---

## 113 — no N-1 / rolling-upgrade compatibility statement

**Status: VERIFIED — all three citations still say what the item claims, verbatim.**

### 1. The three citations, re-read today (not inherited)

| citation | exact line | text |
|---|---|---|
| `docs/adr/0173-*.md:226` | ✅ **exact** | "**Mixed-version deployments are NOT safe.** … old code reading a new cursor silently drops the three window fields and re-serializes without them, reinstating the double-run. Do not run pre-0173 and post-0173 engine builds against the same instance store." |
| `docs/adr/0175-*.md:277` | ✅ **exact** | "⚠ **Mixed-version deployment is unsafe.** `Incident.Kind` and `timerRecord.CommandID` enter the persisted snapshot … an old build round-trips a new snapshot with `Kind` **dropped**, degrading an `IncidentCompensationStall` into a resolvable `IncidentAction` that the shipped endpoint will then delete … **Do not run pre-0175 and post-0175 builds against the same instance store.**" |
| `engine/state.go:223` | ✅ **exact** (inside `type Incident struct`, on the `Kind IncidentKind` field) | "⚠ Kind enters the persisted snapshot. An OLD build round-trips a NEW snapshot with Kind dropped … Do not run pre-0175 and post-0175 builds against the same instance store." |

`grep -cin "N-1\|rolling upgrade\|rolling-upgrade\|mixed.version" CHANGELOG.md` → **0**. VERIFIED.

⭐ **The shared root cause, which the statement must name**: the store marshals the *whole*
`engine.InstanceState` as JSON (`internal/persistence/store/store_core.go:79,215` —
`json.Marshal(capHistory(step.State, s.historyCap))`) and decodes **without**
`DisallowUnknownFields` on that path. So **every** future field added to `InstanceState` is silently
dropped by an older build on round-trip. This is not two incidents; it is a structural property, and
it will recur on the next state-carrying ADR.

⇒ **The audit's suggested wording — "schema N works with library N-1" — would be a FALSE GUARANTEE.**
Adopting it would publish the opposite of what two ADRs and a struct-field comment say.

### 2. Tier — `A` for the audit's proposal (reject it), `S` for what should ship instead

The *finding* (no statement exists) is real and worth closing; the *suggested fix* is wrong and must
be adjudicated as rejected in writing, with the reason above.

What should ship is a **truthful** compatibility section in `STABILITY.md`, roughly:

> **Rolling upgrades are not supported.** A `wrkflw` build persists `engine.InstanceState` as JSON;
> an older build reading a newer snapshot silently drops fields it does not know, and re-serializes
> without them. Never run two different `wrkflw` versions against the same instance store. Upgrade
> by draining, stopping all replicas, then starting the new version. (ADR-0173, ADR-0175.)

That is a doc edit + a CHANGELOG entry ⇒ `S`. It is also **breaking-ish news** for anyone who
assumed otherwise, so it belongs in the CHANGELOG, not only `STABILITY.md`.

### 3. Fix sketch

Add a truthful "no rolling upgrades" section to `STABILITY.md` + CHANGELOG; **reject** the audit's N-1 wording.

### 4. Automated check that would keep it true

Doc-only, but there is a genuinely useful guard: a test asserting that the snapshot decode path is
**not** strict and that `InstanceState` round-trips lossily for unknown fields — i.e. encode a
`map[string]any` snapshot carrying an unknown key, decode into `engine.InstanceState`, re-encode,
assert the key is **gone**. **That test passes today and documents the exact mechanism**; its value
is that it fails loudly if someone later adds `DisallowUnknownFields` and changes the contract
without updating `STABILITY.md`. Mark it as a characterization test so nobody reads it as an
endorsement.

⚠ Nothing can keep the prose true automatically. The real control is rule #10: a state-carrying ADR
must add its own mixed-version line, as 0173 and 0175 both did.

### 5. Dependencies / conflicts

- **`STABILITY.md` is also wrong in two other places — fix in one pass**: it says `gocron` is pinned
  to **v2.21.2** (`STABILITY.md:81`) while `go.mod:11` has **v2.22.0**, and it lists **`samber/do` v2**
  as a locked dependency (see **120**) and a root **`model/`** package (`STABILITY.md:35`) that does
  not exist. Bundle 113 + 120 + backlog 95 into one `docs(stability):` commit.
- Coordinates with **105**, which also needs a CHANGELOG breaking-change entry.

---

## 115 — duplicate node IDs build clean

**Status: VERIFIED, with the failure mechanism located.**

### 1. Packages / files / symbols

- `definition/model` — `func Validate(d *ProcessDefinition) error`
  (`definition/model/validate.go:276`) → `validateStructure` (`:293`).
- **No duplicate-ID rule exists.** Re-derived: the file declares **32** `Err…` sentinels
  (`grep -n "Err[A-Z][A-Za-z]* = errors.New" definition/model/validate.go`) and none concerns node
  or flow identity. The two "duplicate" sentinels are about something else:
  `ErrDuplicateOutcome` (`validate.go:233`, user-task outcome sets) and `ErrDuplicateScopedAction`
  (`definition/model/builder.go:16`, action names).
- **Why it is harmful, not merely untidy** — `definition/model/definition.go`:
  - `:43` `Nodes []Node` — a **slice**, not a map, so duplicates are representable.
  - `:74-81` `func (d *ProcessDefinition) Node(id string) (Node, bool)` is a **linear scan returning
    the FIRST match**. ⇒ the second node sharing an ID is **permanently unreachable** — no error,
    ever.
  - `:84-92` `Outgoing(nodeID)` / `:94` `Incoming(nodeID)` filter `d.Flows` on the **string** id, so
    they return the union of *both* nodes' flows. ⇒ the reachable node inherits the shadowed node's
    routing.

  That combination — first-wins lookup plus union routing — is silent misrouting, which is why this
  is worth more than its size suggests.

### 2. Tier — `S`

One pass over `d.Nodes` and one over `d.Flows` in `validateStructure`, two new sentinels
(`ErrDuplicateNodeID`, `ErrDuplicateFlowID`), appended to the existing `errs` slice exactly like
every neighbouring rule. **Cheapest real fix on the whole list**, as the HANDOVER says.

⚠ One judgement call, worth one sentence in the commit message: `validateStructure` recurses into
subprocess definitions with a visited-set (`:293-297`). Decide whether IDs must be unique
**per-definition** (simplest, and matches `Node`'s per-definition scan) or globally across nested
definitions. **Per-definition is correct** — nested definitions have their own `Node` lookup — but
say so, or the next reader assumes the other.

### 3. Fix sketch

Add `ErrDuplicateNodeID`/`ErrDuplicateFlowID` checks over `d.Nodes`/`d.Flows` in `validateStructure`.

### 4. Falsifiable test note

`definition/model` is pure Go, container-free.

- **`TestValidateRejectsDuplicateNodeID`**: build a definition with two nodes both `id="charge"`;
  assert `errors.Is(Validate(d), ErrDuplicateNodeID)`.
  **What makes it fail today: `Validate` has no such rule, so it returns nil** — the probe measured
  `nodes=4, no error`. Real RED (compile error on the sentinel first, then an assertion failure).
- **Second case, same table**: duplicate `flow.SequenceFlow.ID`.
- **Mandatory controls, or the fix over-rejects**: (a) a valid definition with *distinct* IDs must
  still validate — a naive `map[string]` check that counts a node twice would break every existing
  definition; (b) a node and a flow sharing the same string must **not** error (different namespaces),
  unless the ADR-free decision says otherwise — decide and assert it either way.
- ⚠ `Validate` uses `errors.Join`, so assert with `errors.Is`, never string equality — a joined
  error's `Error()` concatenates every finding and a substring match would pass for the wrong reason.

### 5. Dependencies / conflicts

- `definition/model` is imported by `engine`, `runtime`, `service`, `processtest` and every example.
  A **new rejection** can break existing test fixtures that happen to reuse an ID. Run
  `go vet ./...` (compiles every test file, including Docker-only ones) and the full suite before
  claiming done — this is the classic "hidden consumer" case.
- No conflict with 114/124 (also `engine`-adjacent) since this is `definition/model`; they can run
  as separate agents.

---

## 119 — `NewSQLiteDeduper` does not exist

**Status: VERIFIED.**

### 1. Packages / files / symbols

- `persistence` — `persistence/dedup.go`:
  - `:26` `type Deduper interface`
  - `:43` `var _ Deduper = (*store.Deduper)(nil)`
  - `:48` `func NewDeduper(pool *pgxpool.Pool) (Deduper, error) { return store.NewDeduper(pool, dialect.NewPostgres()) }`
  - `:59` `func NewMySQLDeduper(db *sql.DB) (Deduper, error) { return store.NewDeduper(db, dialect.NewMySQL()) }`
  - **No `NewSQLiteDeduper`.**
- The table exists for SQLite: `wrkflw_processed_message` is created in **all three** migration
  sets — `internal/persistence/store/migrations/{sqlite,postgres,mysql}/0001_init.sql`.
- The implementation is dialect-neutral and already takes the shapes needed:
  `store.NewDeduper(db, dialect)` accepts a `*sql.DB` (proven by `NewMySQLDeduper`), and
  `dialect.NewSQLite()` already exists (used by `persistence/sqlite.go:74`).

⇒ the missing constructor is a **three-line function**, and its absence is the only thing keeping
SQLite consumers off the idempotent-consumer path their schema already provisions.

### 2. Tier — `S`

New exported symbol, but a pure mirror of two existing ones. No decision.

### 3. Fix sketch

Add `func NewSQLiteDeduper(db *sql.DB) (Deduper, error) { return store.NewDeduper(db, dialect.NewSQLite()) }`.

### 4. Falsifiable test note

Container-free via `dbtest.RunTestSQLite`.

- **`TestNewSQLiteDeduperSeenIsIdempotent`**: `db := dbtest.RunTestSQLite(t)`; construct; call
  `Seen(ctx, "msg-1")` twice; assert first reports new and second reports already-seen.
  **What makes it fail today: `NewSQLiteDeduper` is undefined** — compile error, a valid RED under
  the repo's own rule ("`undefined: NewThing` is a valid red state").
- **Control that the test is not vacuous**: assert the *distinct* key `"msg-2"` still reports new in
  the same test — otherwise a `Seen` that always reports already-seen would pass.
- **Also assert the ambient-transaction behaviour** the `Deduper` godoc promises
  (`persistence/dedup.go:19`: "Seen joins the ambient transaction stashed in ctx"). ⚠ Without this
  the new constructor could ship satisfying the interface while violating the contract that makes
  the interface useful — and a compile-only RED proves nothing about behaviour.

### 5. Dependencies / conflicts

- Same package as **109/117** (`persistence`). If 109 adds a startup check to `OpenSQLite`, this and
  that are one serial change in one package — bundle them.
- Depends on nothing; can ship alone.

---

## 120 — `samber/do` is documented as locked but absent from the module

**Status: VERIFIED, all four measurements — and there is a second stale fact in the SAME SENTENCE.**

### 1. The measurements

| claim | measurement | verdict |
|---|---|---|
| listed in `STABILITY.md` | `STABILITY.md:81` — "Locked dependencies (… `gocron` pinned to v2.21.2, `clockwork`, `casbin`, **`samber/do` v2**) are changed only via an ADR." | ✅ |
| listed in `CLAUDE.md`'s tech-stack table | "DI container \| [samber/do v2] \| application-layer wiring only", plus an entire **Dependency Injection** section prescribing `do.Provide` / `do.MustInvoke[T]` | ✅ |
| absent from `go.mod` | `grep -i samber go.mod` → only `github.com/samber/hot v0.13.0` (`:25`) and `github.com/samber/go-singleflightx v0.3.2 // indirect` (`:128`). **No `samber/do`, direct or indirect.** | ✅ |
| imported by zero files, two comments reference it | `grep -rn "samber/do" --include=*.go .` → `runtime/processdriver.go:323` and `service/service.go:279`, **both inside comments** ("Its signature matches samber/do's…", "samber/do ShutdownerWithContextAndError"). **Zero import statements.** | ✅ |

⭐ **Second stale fact, same sentence**: `STABILITY.md:81` pins `gocron` to **v2.21.2**;
`go.mod:11` has `github.com/go-co-op/gocron/v2 v2.22.0`, and CLAUDE.md says v2.22.0 (ADR-0135).
Whoever edits line 81 for `samber/do` must fix the gocron version in the same edit — it is nine
words away. (This is backlog **95**'s first sub-claim; see the addendum.)

### 2. Tier — `A` (owner decision)

Not a code defect. Two crisp options; the owner picks one:

**Option A — adopt it.** Add `github.com/samber/do/v2` to `go.mod` and use it where CLAUDE.md
already says it should be: `examples/production_wiring` (and any internal composition root).
*For*: CLAUDE.md's DI section becomes true; `examples/` gains a container-based reference wiring
alongside the plain-constructor one, which is what a "DI is a consumer choice" library should show.
*Against*: a new dependency on a library the codebase has demonstrably not needed for 184 ADRs; the
two comments show the shutdown seam was already designed to be `do`-compatible **without** importing
it, which is arguably the better library posture.

**Option B — correct both documents.** Remove `samber/do` from `STABILITY.md:81`'s locked list and
rewrite CLAUDE.md's tech-stack row + Dependency Injection section to describe what the repo actually
does: plain constructors and interface parameters, with a shutdown seam whose signature is
*compatible with* `do`'s `ShutdownerWithContextAndError` so a consumer who uses `do` gets it for
free. *For*: zero new dependencies, documents become true immediately, and it matches the
library-first principle. *Against*: `examples/` loses a promised DI reference.

⚠ **Recommend B, but it is not the agent's call.** ⚠ Whichever is chosen, **CLAUDE.md must be
edited by the owner** — an agent must not modify `CLAUDE.md` on its own initiative.

### 3. Fix sketch

Owner decides: adopt `samber/do` v2 in `go.mod` + `examples/`, or strike it from `STABILITY.md` and `CLAUDE.md`.

### 4. Automated check that would keep it true

**This one is genuinely checkable, and it is the highest-value part of this item.** A test that
parses `STABILITY.md`'s locked-dependency list and asserts every named module (a) appears in
`go.mod` and (b) at the stated version. **It fails today twice** — on `samber/do` (absent) and on
`gocron v2.21.2` vs `v2.22.0`. Real RED, no mutation, and it permanently closes this class of rot,
which has now produced backlog items 95 and 120 from one sentence.

### 5. Dependencies / conflicts

- **Bundle with 113 and backlog 95** — all three edit `STABILITY.md`, two of them the same line.
- Option A touches `go.mod` and `examples/production_wiring`, which is also **121**'s file. If both
  A and 121 are taken, do them together.

---

## 121 — `examples/production_wiring` never calls `driver.Start` or `driver.Shutdown`

**Status: VERIFIED — and the consequence is WORSE than the item states.**

### 1. Package / file / symbols

`examples/production_wiring/main.go`:

- `:162` `driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store), runtime.WithHumanTasks(resolver, taskStore, az))`
- `:171` `service.WithProcessDriver(driver)` — the **only** other use of the variable.
- `grep -c "driver\." examples/production_wiring/main.go` → **0**. No `driver.Start`, no
  `driver.Shutdown`.
- The hand-built scheduler: `:89` `sched, err := scheduler.NewScheduler(scheduler.WithClock(clk), scheduler.WithLogger(logger))`,
  `:93` `shutdown.AddCloser(sched)`.

### 2. ⭐ The sharper consequence

`NewProcessDriver` is called **without `runtime.WithScheduler(sched)`**, so the driver builds its
**own** scheduler and stores it as `driver.ownedScheduler` (`runtime/processdriver.go:257`, field at
`:158`). And `Start` is the only thing that ever starts it:

```go
func (driver *ProcessDriver) Start(ctx context.Context) error {   // processdriver.go:286
    if driver.ownedScheduler == nil { return nil }
    if err := driver.ownedScheduler.Start(ctx); err != nil { ... }
    return nil
}
```

⇒ In this example the driver's scheduler is **never started at all**, so **no timer, deadline,
reminder or in-wait action ever fires** — not merely "never drained". Meanwhile `sched` (`:89`),
the one the reader believes is doing the work, is wired into *nothing* and is registered for
shutdown only. The example's shutdown sequence (`:222` `srv.Shutdown`, `:228` `shutdown.Shutdown`)
also never drains in-flight engine work, because `driver.Shutdown` — the function that sets
`draining`, waits on `inflight`, and can return `ErrDrainTimeout` (**110**) — is never called.

**This is the reference wiring a new consumer copies.** Per CLAUDE.md, `examples/` is illustrative
only and never the product — but an illustration that silently disables timers and skips graceful
shutdown is worse than no illustration.

### 3. Tier — `S`

Either pass `runtime.WithScheduler(sched)` and delete the duplicate, or drop the hand-built `sched`
and call `driver.Start(ctx)` / `driver.Shutdown(ctx)`. Roughly ten lines. **Prefer the latter and
keep `sched`** only if the example is meant to demonstrate an injected scheduler — in which case
`WithScheduler` is mandatory, and that is the whole point of showing it.

### 4. Falsifiable test note

`examples/` has no test suite, which is exactly why this rotted.

- **The check that would have caught it, and should ship with the fix**: an
  `examples/production_wiring` test (or a `TestMain`-driven smoke run) that starts the wiring,
  arms a short timer through the engine, advances the fake clock, and asserts the timer fired.
  **What makes it fail today: `driver.Start` is never called, so `ownedScheduler` is never started
  and the timer never fires** — the assertion fails on the current code. Real RED, no mutation.
  This is far stronger than a `grep`-for-`driver.Start` lint, which a refactor renames away.
- **Mandatory control**: assert the timer fires *after* the clock advance and **not** before —
  otherwise a test that passes because of an unrelated synchronous path proves nothing.
- ⚠ Use `clockwork.NewFakeClockAt`, never `NewFakeClock` — **126**.
- ⚠ `examples/production_wiring` uses Postgres wiring; if the smoke test needs a DB, port it to
  SQLite (`dbtest.RunTestSQLite`, no container) or keep it in the Docker-gated set and say so.

### 5. Dependencies / conflicts

- **Do with 110** — 110 adds shutdown guidance to `docs/production-checklist.md`, and this example is
  what that guidance should point at. Fixing one without the other leaves the doc pointing at broken
  wiring.
- Related to the item-58 correction (the HANDOVER cross-reference) — read it before writing the spec.
- If **120 Option A** is chosen, that also edits this file. One agent, one commit.

---

## 122 — `examples/broker_wiring` claims `Run` "loops DrainOnce with backoff"

**Status: the COMMENT IS FALSE (verified) — but ⚠ the ITEM'S OWN WORDING IS ALSO FALSE.**

### 1. The comment

`examples/broker_wiring/main.go:130-133`:

```go
// DrainOnce publishes every currently-pending outbox row and returns — used here
// so the example is a single synchronous pass with no goroutines. In production
// the same relay runs continuously in a goroutine via relay.Run(ctx), which loops
// DrainOnce with backoff and (on Postgres/MySQL) LISTEN/NOTIFY wakeups.
```

### 2. What `Run` actually does — `internal/persistence/store/relay.go:481-538`

- `:482` `ticker := r.clk.NewTicker(r.poll)` — a **fixed-interval** ticker. No backoff, no jitter,
  no interval growth anywhere in the loop.
- `:505` an immediate `drainUntilEmpty` before the first tick, then `:512-530` a `select` over
  `ctx.Done()`, `ticker.Chan()` and `r.wake`, each calling **`drainUntilEmpty`** — **not
  `DrainOnce`**.
- `:452-462` `drainUntilEmpty` loops `DrainOnce` until it returns 0, and **returns on the first
  error**.

So the comment is wrong on **two** counts: the function looped is `drainUntilEmpty`, and the loop's
pacing is a fixed poll interval.

### 3. ⚠ Counting-lens correction — the item over-generalises

The HANDOVER says: *"There is no backoff in `Run`, `drainUntilEmpty` or `DrainOnce`."*
`grep -cin backoff internal/persistence/store/relay.go` → **11 hits**, and one of them is inside
`DrainOnce` (which spans `:241-356`):

```go
// relay.go:296, inside DrainOnce
nextAttempt := now.Add(RelayBackoff(c.retryCount, r.backoff.base, r.backoff.max))
```

plus `:40` (godoc: "exponential backoff"), `:53` `backoff struct{ base, max time.Duration }`,
`:120-129` `WithRelayBackoff`, `:200-201` defaults (`1s` base, `1m` max), `:226` (godoc).

⇒ **There IS a capped exponential backoff in `DrainOnce`** — a *per-row* backoff written to
`next_attempt_at` after a failed publish — and `drainUntilEmpty` inherits it by calling `DrainOnce`.
What does not exist is a **loop-level** backoff in `Run`.

**Corrected finding**: *"`Run` paces on a fixed poll ticker and loops `drainUntilEmpty`, not
`DrainOnce`; the relay's only backoff is per-row on `next_attempt_at` inside `DrainOnce`, which is
not what the comment describes."* Anyone who fixed the comment from the item's wording would write
a **second** false comment claiming the relay has no backoff at all.

### 4. Tier — `S`

One comment. But **write it from §3, not from the item.**

### 5. Fix sketch

Rewrite the comment: `Run` loops `drainUntilEmpty` on a fixed poll ticker + NOTIFY wakeups; backoff is per-row in `DrainOnce`.

### 6. Automated check

None realistically — comment prose is not testable. The mitigation is the Delivery Gate's rule
(sweep the diff's comments for unexecuted claims), which is where this class is cheapest to kill.
⚠ Note this comment is in `examples/`, which no gate currently sweeps; that is the structural reason
121, 122 and 123 all survived.

### 7. Dependencies / conflicts

- Same file as nothing else in this slice. Ships alone in a `docs(examples):` or `chore(examples):`
  commit, ideally batched with **121** and **123** as one comment-rot sweep.

---

## 123 — `scheduler/job.go`'s comment says foreign jobs are "treated as non-singleton"

**Status: VERIFIED — and the correct behaviour is documented CORRECTLY nine lines away, in the same package.**

### 1. The two comments

**The false one** — `scheduler/job.go:141-142`, in the godoc of `func (j *job) singleton() bool`:

> "Consumer-implemented Jobs that don't satisfy it are simply **treated as non-singleton** by the façade."

**The true one** — `scheduler/scheduler.go:556-560`, the godoc of the function that actually
implements the fallback:

> "A FOREIGN Job implementation (defined outside this package) cannot satisfy an unexported-method
> interface, so it defaults to the safe equivalent: **serialized when its Trigger is recurring**,
> unrestricted when one-shot."

### 2. The code — `scheduler/scheduler.go:561-566`

```go
func jobSingleton(j Job) bool {
	if s, ok := j.(interface{ singleton() bool }); ok {
		return s.singleton()
	}
	return j.Trigger().Recurring()      // ← foreign + recurring ⇒ TRUE
}
```

⇒ `jobSingleton(foreign recurring) == true`, exactly as the probe measured. `job.go:141` is false for
every recurring foreign job — i.e. for the overrun-protection case the flag exists to serve. It is
right only for one-shot foreign jobs.

⭐ **The instructive part**: this is not a case of nobody knowing the behaviour. The correct
description was written on the implementing function, and the stale one survived on the *interface*
side where a reader looks first. **A comment on the abstraction rotted while the comment on the
implementation stayed true** — which is why "check the code, not the neighbouring comment" is the
only reliable rule.

### 3. Tier — `S`

One comment. Replace `job.go:141-142` with a pointer to `jobSingleton`'s own godoc rather than a
restatement — restating is how the divergence happened, and per Premise Discipline a restated claim
loses its hedge and stops being re-checked.

### 4. Fix sketch

Correct `scheduler/job.go:141-142` to "serialized when recurring, unrestricted when one-shot"; cite `jobSingleton`.

### 5. Falsifiable test note

`scheduler` is pure Go for this path (no container).

- The behaviour **is** already testable and partly tested — `scheduler/job_test.go:251` covers
  "one-shot trigger defaults to non-singleton". **What is missing is the foreign-recurring case**,
  which is exactly the one the comment gets wrong.
- **`TestJobSingletonForeignRecurringIsSerialized`**: declare a test-local type implementing the
  public `Job` interface (and therefore *not* the unexported `singleton()`), with a recurring
  Trigger; assert `jobSingleton(j) == true`.
  ⚠ **What makes it fail today: nothing — it passes.** This is a *characterization* test that pins
  the behaviour so the comment cannot drift again; its RED must come from mutation (change
  `scheduler.go:565` to `return false`, observe RED, restore from a `cp` backup, `diff`). Say so in
  the plan, or it will be mistaken for a regression test.
  ⚠ **Vacuity trap specific to this test**: the fixture type must be declared in a `_test` package
  or otherwise genuinely foreign. A test type declared inside `package scheduler` **can** spell the
  unexported `singleton()` method, and if it accidentally has one the test takes the *other* branch
  and proves nothing. Assert the branch: `_, ok := j.(interface{ singleton() bool }); require.False(t, ok)`.

### 6. Dependencies / conflicts

- None. Batch with **121**/**122** as a single comment-rot sweep commit.
- ⚠ Unrelated stale comment found while verifying, worth fixing in the same sweep:
  `engine/step_nodes_test.go:10` says "Keep in sync with nodeStrategies in **step_nodes.go**" —
  `nodeStrategies` is declared in `engine/step_dispatch.go:36`.

---

## 124 — the engine holds the data that refutes a forged actor and never compares it

**Status: VERIFIED by exhaustive grep of the handler.**

### 1. Packages / files / symbols

- The data, co-located in one struct — `humantask/humantask.go`:
  - `:101` `Candidates []authz.Actor` — "the resolved eligible actors (filled by the runtime…)"
  - `:70-79` `type Completion struct { Actor authz.Actor …}`
  - `:84-86` the struct godoc: "Claim and Completion form the task's audit trail: both … carry the
    full `authz.Actor` the engine observed" — so a third identity fact, `Claim.Actor`, is present too.
- The write with no check — `engine/step_triggers.go`,
  `func handleHumanCompleted(ctx, def, s, t HumanCompleted, opt)` (declared `:839`):

  ```go
  // :931-936
  task.Completion = &humantask.Completion{
      Actor:   t.Actor,          // ← straight off the trigger, unvalidated
      At:      t.OccurredAt(),
      Outcome: t.Outcome,
      Note:    t.Note,
  }
  ```

- **Measured**: `awk 'NR>=839 && NR<=960' engine/step_triggers.go | grep -n "Candidates\|Eligibility\|Claim"`
  → **no output**. The handler validates the *outcome* thoroughly (`:906` manual-task payload,
  `:912-921` the closed outcome domain, `ErrOutcomeRequired`/`ErrInvalidOutcome`) and the *actor*
  not at all. The eligibility predicate the store already knows how to evaluate lives in
  `humantask/memory.go:103` (`candidateContains(t.Candidates, actor.ID) || hasRoleOverlap(...)`) and
  is used by `ClaimableBy` — the **claim** path — never by the **completion** path.

### 2. Tier — `D`

A cheap partial mitigation for **101**, but not a mechanical one: refusing a completion whose actor
is outside `Candidates` is a **behaviour change that can strand instances** (delegation, an actor
whose roles changed after the task was minted, a resolver returning a smaller candidate set on
re-resolution). The decision — refuse (new sentinel), or record a mismatch flag on the audit trail
and allow — is exactly the "ask what the guard must STILL DO" question. ADR it.

⚠ Note the natural comparison target is ambiguous and the ADR must pick: `Candidates`,
`Eligibility.Roles`, or `Claim.Actor`. **`Claim.Actor` is the strongest and the cheapest** (an actor
who holds the claim completing their own task), and it composes with backlog **90** (silent claim
theft) — but it only helps for tasks that were claimed.

### 3. Fix sketch

`handleHumanCompleted` compares `t.Actor` against `task.Claim.Actor`/`Candidates`; refuse or flag per ADR.

### 4. Falsifiable test note

`engine` is container-free.

- **`TestHumanCompletedRejectsNonCandidateActor`**: mint a task with `Candidates: [alice]`, drive
  `NewHumanCompleted(..., actor=mallory)`, assert the chosen sentinel.
  **What makes it fail today: `handleHumanCompleted` contains zero references to `Candidates`,
  `Eligibility` or `Claim`** (measured above), so today it returns a successful `StepResult` with
  `Completion.Actor == mallory` — the probe's exact result. Real RED.
- **Mandatory control, and it is the one that decides the ADR**: an `alice` completion must still
  succeed, and a task with an **empty** `Candidates` slice must still complete (otherwise every
  role-only-eligible task breaks). Both cases in the same table.
- ⚠ **Fixture check, not line check**: the test's definition must actually mint a task with a
  populated `Candidates` — a fixture whose `Candidates` is nil makes the rejection assertion
  unreachable and the test vacuous. Assert `require.NotEmpty(t, task.Candidates)` before acting.

### 5. Dependencies / conflicts

- **Partial mitigation for 101** (no tamper-evident audit trail) and adjacent to backlog **90**
  (silent claim theft) — 90 is in the same `humantask`/`engine` seam and is described as small and
  self-contained. **Consider one bundle: 90 + 124.**
- `engine` package ⇒ **strictly serial** with **114** and any item-73 work.
- Touches `engine/step_triggers.go`, which ADR-0183 recently changed (claim invariant) — re-read
  that ADR before designing, so the new guard does not contradict it.

---

## 125 — `store.Create` SIGSEGVs on a nil `AppliedStep.Trigger`

**Status: VERIFIED.**

### 1. Package / file / symbol

`internal/persistence/store` — `internal/persistence/store/trigger_codec.go`,
`func MarshalTrigger(t engine.Trigger) ([]byte, string, error)` (`:100`). First statement:

```go
env := triggerEnvelope{At: t.OccurredAt()}   // :101 — method call on a nil interface ⇒ SIGSEGV
```

The godoc immediately above it (`:96-98`) promises the opposite:

> "Every variant of the sealed engine.Trigger set is handled. Passing an unknown (future) variant
> returns a **descriptive error** rather than silently producing an empty payload."

The type switch's `default` arm that produces that error is **unreachable for nil**, because
`t.OccurredAt()` is dereferenced one line earlier. So the promise holds for an *unknown* variant and
fails for a *nil* one.

Reached from `Store.Create` via `s.writeJournal(ctx, q, step, version, now)`
(`internal/persistence/store/store_core.go:104`, helper at `:349`).

### 2. Tier — `S`

A two-line nil guard returning a sentinel, plus the godoc correction. No decision.

⚠ `internal/`-only with **no live vector** — every in-repo caller constructs `AppliedStep` with a
real trigger. This is defence-in-depth against a future caller, so it is **low priority**, and the
entry should say so rather than being sized as a crash bug.

### 3. Fix sketch

Guard `if t == nil { return nil, "", fmt.Errorf("workflow-store: marshal trigger: nil trigger") }` at `:101`.

### 4. Falsifiable test note

`internal/persistence/store` is not container-free as a package, but this function needs **no DB at
all** — it is a pure codec.

- **`TestMarshalTriggerRejectsNilTrigger`**: `_, _, err := MarshalTrigger(nil)`; assert a non-nil
  error and no panic.
  **What makes it fail today: `trigger_codec.go:101` calls `t.OccurredAt()` on a nil interface, so
  the test panics with SIGSEGV rather than failing an assertion.** A panic is a valid RED, but the
  plan must say the failure mode is a **panic**, not an assertion failure — otherwise the
  implementer expects a red `--- FAIL` line and is confused by a stack trace.
- ⚠ **Do not use `require.Panics` as the final test.** That would pin the *current* broken behaviour.
  The assertion must be `require.Error` (which panics today and passes after the fix).
- Add the symmetric case for `UnmarshalTrigger` if it has the same shape — **check, do not assume**;
  this entry did not verify the unmarshal side. `ASSUMPTION (unverified)`.

### 5. Dependencies / conflicts

- **Same function as 118's fix target region (`Store.Create`)** — both are in
  `internal/persistence/store`. One serial change or one bundle; never two concurrent agents in one
  package.
- No consumer-visible API change.

---

## 126 — `clockwork.NewFakeClock()` seeds from wall time (⚠ NOT a defect)

**Status: VERIFIED. Tier: `A` — a test-authoring constraint, not a backlog defect.**

### 1. The fact

`github.com/jonboulle/clockwork v0.5.0` (`go.mod:18`),
`$GOMODCACHE/github.com/jonboulle/clockwork@v0.5.0/clockwork.go:86-88`:

```go
func NewFakeClock() *FakeClock {
	return NewFakeClockAt(time.Now())
}

// NewFakeClockAt returns a FakeClock initialised at the given time.Time.
func NewFakeClockAt(t time.Time) *FakeClock { return &FakeClock{time: t} }
```

`NewFakeClock()` is **not** deterministic: it seeds from the wall clock. `NewFakeClockAt(t)` is the
deterministic constructor.

### 2. Why this is an `A`, and how it must be phrased

There is nothing to fix in `wrkflw`. The value of this entry is entirely as a **constraint on other
items' tests**, so it should be phrased as one and carried into every plan that prescribes a
clock-sensitive test:

> **Every test that compares a wrkflw timestamp against a clock-derived expectation MUST construct
> its clock with `clockwork.NewFakeClockAt(<fixed time>)`. `clockwork.NewFakeClock()` seeds from
> `time.Now()`, so a test that asserts "the value the engine stored equals the value the clock
> reports" will pass whether or not the production code reads the fake clock — the two agree by
> accident, because both are approximately wall-clock time.**

That is precisely the trap for a **backlog 84** regression test ("the store layer reads the wall
clock directly, against ADR-0138"): the bug *is* "production code reads `time.Now()` instead of the
injected clock", and a `NewFakeClock()`-seeded test **cannot distinguish the two**. It would be
another entry in this repo's list of prescribed tests that could not fail.

### 3. Items in THIS slice whose prescribed tests are exposed to it

- **107** (timer lateness) — asserts an *age* derived from `NextFireAt` vs now.
- **110** (drain timeout) — deadline-driven.
- **121** (production_wiring smoke) — advances a clock to fire a timer.
- Anything touching `runtime/timerops`, `scheduler`, or `internal/persistence/store` timestamps.

Each of those entries above carries the ⚠ inline.

### 4. The check that would enforce it

A lint-style test (or a `golangci-lint` `forbidigo` rule) banning `clockwork.NewFakeClock()` in
`_test.go` files in favour of `NewFakeClockAt`. **It would fail today wherever the bare constructor
is already used** — worth measuring before prescribing, since existing call sites may be legitimate
(a test that never compares against a wall-clock-derived value). ⚠ **Not measured in this triage:**
`ASSUMPTION (unverified)` as to how many call sites exist.

### 5. Dependencies / conflicts

Carries into **84**'s plan (outside this slice) and into 107/110/121 here. No code change.

---

## Addendum — backlog 95 (outside this slice; verified on request)

`STABILITY.md` contains stale facts. Measured:

| sub-claim | measurement | verdict |
|---|---|---|
| gocron `v2.21.2` vs go.mod `v2.22.0` | `STABILITY.md:81` says v2.21.2; `go.mod:11` `github.com/go-co-op/gocron/v2 v2.22.0` | ✅ **VERIFIED** |
| a root `model/` package that does not exist | `STABILITY.md:35` lists "(`engine/`, `model/`, `runtime/`, `action/`, `authz/`, …)"; `ls -d model` → **no such directory** (the package is `definition/model`) | ✅ **VERIFIED** |
| `samber/do` listed but absent | see **120** | ✅ **VERIFIED** |
| README says "19 node kinds"; the repo has 18 | `README.md:633` says 19 ✅. **But "18" is itself unverified and probably measuring a third thing** — see below | ⚠ **PARTIALLY CONTRADICTED** |

⚠ **The node-kind count is a three-way disagreement and the fix must pick a denominator.** Measured:

- `definition/model/definition.go:17-34` — the `NodeKind` const block: `KindUnspecified` (iota 0)
  **+ 17 named kinds**.
- `engine/step_dispatch.go:36-52` — `nodeStrategies`: **16 entries** (`KindBoundaryEvent` has none;
  the map's own comment at `:33` says "maps each **arm-bearing** NodeKind to its strategy", so
  boundary events are arm-driven rather than token-entered — correct, not a gap).
- `README.md:633` — **19**, but the README counts **constructor table rows**, not `NodeKind`s
  (e.g. `ErrorEndEvent` is its own row and is `KindEndEvent` underneath).

⇒ 19, 18, 17 and 16 are all "true" of different denominators. **The fix is to state the denominator
in the README sentence** ("17 `model.NodeKind` values, exposed through N constructors"), not to
change 19 to 18. Changing it to 18 would replace one unsourced number with another.

⚠ Also note MEMORY.md's standing line "**All 18 BPMN2 node kinds have exec impls** (`nodeStrategies`
in `engine/step_nodes.go`)" is stale in **both** halves: `nodeStrategies` lives in
`engine/step_dispatch.go:36`, and it holds **16** entries. The claim it is making (zero
unimplemented kinds) is still correct.


### ⭐ NEW defect found while verifying 95 — not in `AUDIT.md` and not in backlog 51–126

**`README.md` documents a constructor that does not exist.** The Events table (`README.md`, Events
section, third row) reads:

> | **ErrorEndEvent** | Throws an error code, caught by a boundary error event. | `event.NewErrorEnd(id, errorCode string, name ...string) Node` |

Measured:

```
grep -rn "NewErrorEnd" --include=*.go .   → 0 hits (including tests)
```

`definition/event` exports exactly **six** constructors (`definition/event/event.go`):
`NewStart` (`:215`), `NewEnd` (`:225`), `NewIntermediateCatch` (`:236`), `NewIntermediateThrow`
(`:246`), `NewCompensateThrow` (`:258`), `NewBoundary` (`:269`). **There is no `NewErrorEnd`.**

The real authoring form is an option on `NewEnd`:
`func WithErrorCode(errorCode string) EndOption` (`definition/event/options.go:339`) — "makes an
EndEvent throw a workflow error when reached (BPMN error end event) … Mutually exclusive with
`WithForceTermination`; if both are applied the last one wins" (`:334-338`).

⇒ A consumer following the README's Events table to throw an error code gets
`undefined: event.NewErrorEnd`. **Tier `S`** (one README row: replace the constructor with
`event.NewEnd(id, event.WithErrorCode("..."))` and fold the row into the EndEvent row).
**Automated check**: the same "compile the README/doc snippets as `examples/`" mechanism proposed
under **108** would catch this class — and this is the second doc in two files (`docs/observability.md`,
`README.md`) whose consumer-facing snippet names a symbol that does not exist, which is the argument
for building that check once rather than per-document.

⚠ This also means backlog 95's node-kind row understates the problem: the Events table is wrong in a
way that is *worse* than the count, and the count itself needs a denominator (above). Fold both into
one `docs(readme):` pass.
