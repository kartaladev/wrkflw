# Triage — backlog 44–69 + blockers 5, 7, 8

Read-only triage sweep, 2026-08-20. Every entry is source-verified against the
working tree at `main` unless labelled otherwise. **No Docker was used** — every
claim requiring a live Postgres/MySQL container is labelled
`ASSUMPTION (unverified)` with the probe that would settle it.

Statements come from `docs/plans/HANDOVER.md` (items 44–50 under "🆕 opened by
ADR-0184" / "🆕 opened by /code-review"; 51–69 under "🆕 from the 2026-08-19
verification of `AUDIT.md`"; blockers under `## Pre-v0.1.0 blockers`).

Tier legend: **S** = small (≤~100 lines, no new public API, no architectural
decision) · **D** = needs a spec/ADR · **A** = adjudication (not a defect / already
closed / owner decision / duplicate / research).

---

## 44 — the 16 `Never` sites in `scheduler/` are vacuity-prone

- **Package(s):** `scheduler` (test-only), `scheduler/internal/gocron`,
  `scheduler/internal/gocron/myelector`. Files:
  `scheduler/clock_option_test.go:89`, `scheduler/scheduler_surface_test.go:169,228,256,289`,
  `scheduler/internal/gocron/scheduler_test.go:141,161,195,220`,
  `scheduler/internal/gocron/bump_regression_test.go:47`,
  `scheduler/internal/gocron/job_schedule_test.go:68,88,128,160`,
  `scheduler/internal/gocron/clock_option_test.go:87`,
  `scheduler/internal/gocron/myelector/mysql_elector_heartbeat_test.go:64`.
- **Verification: VERIFIED.** `grep -rn '\.Never(' scheduler/ | wc -l` → **16**, and the
  handover's distribution is exact: 100 ms × 1 (`scheduler_test.go:195`), 150 ms × 10,
  200 ms × 4, 300 ms × 1 (`myelector`). Hand-counted, not scripted.
- **Tier: S.** Test-only, no public API, no contract change. It does span three
  packages, so per rule #11 it must run **serially** (concurrent agents in one Go
  package break each other's compile) — but it is one mechanical pattern.
- **Fix sketch:** before each `Never`, assert a *positive* liveness precondition
  proving the goroutine under test actually ran (e.g. a sibling job that DOES fire,
  or `clk.Advance` + a `require.Eventually` on an unrelated counter), so
  "did not fire" is distinguishable from "nothing ran". Budgets stay unchanged —
  a `Never` window is paid on every GREEN run.
- **Falsifiable test note:** the falsifier is a **mutation, not an assertion**: stub
  the scheduler so `ScheduleJob` registers nothing at all, re-run each `Never` test,
  and observe it still PASSES. That is the vacuity. A converted test must go RED
  under the same mutation. ⚠ Do not write the "improved" test without running that
  ablation first — this is exactly the ADR-0184 / backlog 42 failure mode.
- **Dependencies:** mirror image of **42** (closed). Independent of **45**/**46**,
  but **46** (a `synctest` spike) would make this item moot for `scheduler/internal/gocron`
  if it succeeded, so sequence 46 → 44 if 46 is ever taken.

---

## 45 — blocker 5 + the `runtime/` `Eventually` sites, and a 5th copy of the constant

- **Package(s):** `runtime`, `runtime/calllink`, `internal/persistence/store`,
  plus the four existing constant sites: `scheduler/waitbudget_test.go:32`,
  `scheduler/internal/gocron/waitbudget_test.go`,
  `scheduler/internal/gocron/pgelector/waitbudget_test.go`,
  `scheduler/internal/gocron/myelector/waitbudget_test.go` (all
  `const eventuallyBudget = 10 * time.Second`).
- **Verification: partly CONTRADICTED / partly ASSUMPTION.**
  - Constant copies: **4** today (`grep -rln 'const eventuallyBudget'`), so an
    adoption in `runtime/` would indeed make a 5th. VERIFIED.
  - `runtime/` `Eventually` sites: **4 only** —
    `runtime/processdriver_scheduler_e2e_test.go:112`,
    `runtime/jobstore_rehydrate_durable_test.go:93`,
    `runtime/calllink/notifier_options_test.go:124,129`. VERIFIED. Repo-wide there
    are **59** `Eventually` sites, **34** of them under `scheduler/internal`; the
    non-scheduler remainder is spread over `internal/persistence` (7),
    `persistence` (3), `eventing` (2), `runtime`+`calllink` (4), `casbinauthz`,
    `internal/authz`, `action/email` (1 each). So "the `runtime/` sites" is a
    **four-site** problem, not a cluster.
  - **"Same class as blocker 5" is an INHERITED, UNVERIFIED claim.** Blocker 5's
    own handover entry marks its "load-flaky" label unverified, and backlog 42
    carried the identical inherited diagnosis and was **wrong**. Restating it here
    is the Premise-Discipline failure that item warns about.
- **Tier: A.** Not a defect: it is a test-convention proposal whose load-bearing
  premise is unverified and whose scope (4 sites) does not justify a shared
  `internal/` test package on its own. **Blocked on blocker 5 being diagnosed by
  execution.** If blocker 5 turns out to be a production race (as 42 did), this
  item evaporates.
- **Falsifiable test note:** ⚠ **vacuity-risk.** There is no test that can fail
  today; the artifact would be a helper package plus mechanical call-site edits.
- **Dependencies:** gated on **blocker 5**. Overlaps **44** (same packages, test-only).

---

## 46 — a `testing/synctest` spike for `scheduler/`

- **Package(s):** `scheduler/internal/gocron` (research target). `go.mod` declares
  `go 1.25.7`, so `testing/synctest` is GA and available. VERIFIED (`head -5 go.mod`).
- **Verification: VERIFIED** as *stated* — the handover files it as "Research, not a
  flake fix", and the repo has zero `synctest` usage today.
- **Tier: A.** Research/spike by its own statement. It is also **not obviously
  viable**: gocron owns real `time.Timer`s inside a third-party library, and the
  project pins `gocron v2.22.0` (ADR-0135), so a bubble would have to tolerate
  gocron's internal goroutines. That viability question is the whole spike.
- **Fix sketch:** timeboxed spike — wrap one `scheduler/internal/gocron` table case in
  `synctest.Test`, see whether the bubble reaches durable idle with gocron's
  goroutines alive; report, do not adopt.
- **Falsifiable test note:** the spike's own falsifier is binary — the bubble either
  reaches durable-idle or deadlocks/panics. That IS the result. Not a defect test.
- **Dependencies:** would subsume much of **44** and part of **45** if viable.

---

## 47 — `checkTaskStoreConformance` stops at the first break on the legal leg

- **Package:** `processtest`. Symbol `checkTaskStoreConformance`,
  `processtest/taskstoreconformance.go:192`; the early returns are at **:208-210**
  (`if !assert.NoErrorf(... "this shape is legal and must be accepted") { return }`)
  and **:212-214** (`if !assert.NoErrorf(... "the task must be readable") { return }`).
  The skipped check is `checkTaskStoreAcceptedTaskIsListed` at **:218**.
- **Verification: behaviour VERIFIED; the item's *doc-contradiction* premise is
  CONTRADICTED.** The doc comment no longer claims "never stops early" without
  qualification — it was corrected inside ADR-0184's own bundle. Today
  `taskstoreconformance.go:187-191` reads:

  > "On the REJECTED leg it never stops early … On the LEGAL leg it DOES stop at the
  > first break — a plain `return` after the Upsert and the Get checks — so a store
  > that fails both sees only the first (backlog 47)."

  and `:21-25` repeats the disclosure on `conformanceReporter`. The **exported**
  `RunTaskStoreConformance` godoc (`:370` and the ~40 lines above it) contains no
  "never stops early" claim at all. So the live defect is the *asymmetry* between the
  two legs, not a false doc.
- **Tier: D.** Small in lines, but it changes **what an exported helper reports** to a
  third-party consumer's test run — a consumer whose store fails two checks would
  start seeing two failures instead of one. ADR-0184 already treated the conformance
  contract as ADR-worthy. Needs a decision recorded, not just a patch.
- **Fix sketch:** replace the two early `return`s with a guard variable so
  `checkTaskStoreAcceptedTaskIsListed` still runs (skipping only the assertions that
  need a successfully-read `got`), i.e. make the legal leg symmetric with
  `checkTaskStoreRejectedTaskIsNotListed`.
- **Falsifiable test note:** **non-vacuous, and the fixture already exists.**
  `writeOnlyTaskStore` (a store that accepts and never persists) is currently pinned
  at **exactly 1** reported failure — the handover's own "⚠ Things a fresh session
  must not get wrong" says so, and calls that inconsistency this item. A test asserting
  the recorder sees the read-back miss **and** the broken-inbox report fails today with
  `1 != 2`. ⚠ The pinned count **must be updated in the same change** — it is the
  regression pin, so it is also the thing that breaks.
- **Dependencies:** touches the same file/symbols as **43** (closed) and the
  `writeOnlyTaskStore` pin. Conflicts with nothing else in this slice.

---

## 48 — CI runs `go test` at the 600 s default timeout

- **Package:** `docs-only` / build tooling. `.github/workflows/ci.yml:33` →
  `go test -race -coverprofile=cover.out ./...`; `scripts/coverage.sh:23` →
  `go test -race -coverprofile="${profile}" ./...`. Neither passes `-timeout`.
- **Verification: VERIFIED** —
  `grep -rn "go test|timeout" .github/workflows/*.yml scripts/coverage.sh` returns
  exactly those two lines and no `-timeout` anywhere.
- **Tier: S.** Two one-line edits plus (optionally) a guard test.
- **Fix sketch:** add an explicit `-timeout` to both invocations, and add a guard test
  in `scheduler` asserting `eventuallyBudget × (site count in the densest package) <
  the configured timeout` — the rule ADR-0184 states but nothing enforces.
- **Falsifiable test note:** the guard test is non-vacuous only if it derives the site
  count from source rather than hard-coding it. What makes a hard-coded version
  vacuous: it passes forever regardless of new `Eventually` sites — i.e. it is
  precisely the class of test this repo has shipped six of. Prefer parsing
  `scheduler/internal/gocron/*_test.go` for `eventuallyBudget` occurrences (**34**
  today under `scheduler/internal`) and comparing against a `-timeout` constant.
  ⚠ Without that derivation: **vacuity-risk**.
- **Dependencies:** interacts with **blocker 7** (both change how the suite is
  invoked in CI) and with **44** (`Never` budgets are also paid per run).

---

## 49 — a past-due one-shot expressed as a DURATION is refused, leaking a raw gocron error

- **Package:** `scheduler/internal/gocron`. Symbols: `jobDefinition`
  (`scheduler/internal/gocron/trigger.go:176`), whose duration branch at **:187-189**
  is `d, _ := t.Duration(); fireAt := now.Add(d); return
  gocron.OneTimeJob(gocron.OneTimeJobStartDateTime(fireAt)), true, nil` — **no
  past-due branch**, unlike the absolute branch at **:179-185**; and
  `GocronScheduler.ScheduleJob` (`scheduler/internal/gocron/job_schedule.go:72`),
  whose `fireImmediately` decision at **:106-118** is gated on
  `trig.AbsTime()` returning `ok`, which `TriggerDef.AbsTime` (`trigger.go:136-141`)
  returns **false** for the duration form. The raw gocron error escapes unwrapped at
  `job_schedule.go:140-143` (`if err != nil { return time.Time{}, err }`).
  Reachability confirmed: `runtime/timerops.go:46-51` maps `schedule.KindOneTime`
  without an `AbsTime` to `scheduler.After(d)`.
- **Verification: VERIFIED by source** (the reproduction itself is recorded in the
  handover and transcribed into `job_schedule.go:28-33` as a live caveat).
- **Tier: S.** Contained to one package; the contract it aligns with
  ("timers are NEVER dropped") is already decided by ADR-0184 Decision 6.
- **Fix sketch:** give `jobDefinition`'s duration branch the same
  `if !fireAt.After(now) { return gocron.OneTimeJob(gocron.OneTimeJobStartImmediately()), true, nil }`
  guard, widen `ScheduleJob`'s `fireImmediately` predicate to cover it (it currently
  cannot see the duration form), and wrap the `NewJob` error with a
  `workflow-scheduler:` sentinel either way.
- **Falsifiable test note:** **non-vacuous.**
  `s.ScheduleJob(ctx, "id", sched.After(-1*time.Minute), task, false)` today returns
  `next = 0001-01-01T00:00:00Z` and
  `err = "gocron: OneTimeJob: start must not be in the past"`. A test asserting
  `require.NoError` + `assert.False(next.IsZero())` fails today on the first line
  with that exact string. Add the `After(0)` case too.
  ⚠ Assert on `errors.Is`/`next`, **not** on the gocron message text.
- **Dependencies:** same file/symbol as **50**; both should land in one bundle or
  strictly ordered, since both edit `ScheduleJob`'s body. Independent of **42**
  (closed) — that was a race, this is a refusal.

---

## 50 — the `closed` guard's residual `Close`/`ScheduleJob` window

- **Package:** `scheduler/internal/gocron`. Symbols: `GocronScheduler.Close`
  (`scheduler/internal/gocron/scheduler.go:360-365`) and `CloseWithContext`
  (**:373-378**), both of which do `s.mu.Lock(); s.closed = true; s.mu.Unlock();
  return s.sched.Shutdown(…)` — **`Shutdown` outside `s.mu`**;
  `GocronScheduler.ScheduleJob` (`job_schedule.go:72-161`) holds `s.mu` for its
  entire body via `defer s.mu.Unlock()` at **:73-74**. The `AfterJobRuns` listener
  registered at **:122-131** also takes `s.mu`, which is why holding `Shutdown` under
  the lock risks a deadlock.
- **Verification: VERIFIED by source.** Every mechanical claim in the item's text is
  reproduced in `job_schedule.go:57-71`'s own doc comment, and matches the code.
  The durable-row half — that `NativeScheduler.Schedule` has already persisted before
  `activateJob` calls this — is `ASSUMPTION (unverified)`: I read the ordering in
  `scheduler/scheduler.go` but did not execute it.
- **Tier: D.** The item itself enumerates why the two obvious fixes do not work
  (deadlock against the `AfterJobRuns` listener; a post-`NewJob` re-check cannot help
  because `closed` cannot change mid-call). Closing it needs a lock-ordering or
  draining/epoch design → ADR.
- **Fix sketch:** a two-phase close — a `closing` flag plus an in-flight
  `ScheduleJob` counter (or an RWMutex where `ScheduleJob` takes the read side and
  `Close` the write side) so `Shutdown` runs only after registrations quiesce; the
  listener must be moved off `s.mu` or given its own mutex.
- **Falsifiable test note:** ⚠ **vacuity-risk as usually written.** A naive
  concurrency test races and passes by luck. Make it deterministic: inject a hook
  (test-only) that blocks inside `ScheduleJob` after `NewJob`, start `Close` from
  another goroutine, release, and assert the returned job **fires**. What makes it
  fail today: with the hook held, `Shutdown()` completes first and the job never runs
  though `ScheduleJob` returned `(non-zero, nil)`. Without such a hook, do not claim
  RED was observed.
- **Dependencies:** same symbol as **49**. Also touches the same guard ADR-0184 added
  late; do not "clean up" `scheduler.ErrSchedulerClosed`, which is a deliberate var
  **alias**, not a copy.

---

## 51 — the bundled HTTP task handlers take the authorization principal from the request body ⚠ BREAKING

- **Package(s):** `transport/http/httpcore` (source of the defect);
  `transport/http/{stdlib,gin,fiber,parity}` (test fallout only).
- **Verification: VERIFIED, every sub-claim.**
  - Three task sites, exact: `transport/http/httpcore/endpoints.go:119`
    (`Actor: authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles}` — claim), **:132**
    (complete), **:150** (`By: authz.Actor{ID: in.By.ID, Roles: in.By.Roles}` —
    reassign). Hand-counted from `grep -n "authz.Actor" endpoints.go` → exactly 3 hits.
  - The wire types are `httpcore.Actor` (`dto.go:12-15`), embedded in `ClaimInput`
    (`:43-45`), `CompleteInput` (`:49-58`) and `ReassignInput.By` (`:60-67`).
  - **No actor seam in `CustomizeConfig`**: `transport/http/httpcore/seam.go:19-33`
    declares exactly `BasePath`, `Wrap`, `InstanceMapper`, `Logger`,
    `TracerProvider`, `MeterProvider`. Nothing reads an identity off the request
    context, so consumer middleware cannot supply one.
- **Tier: D.** New public seam (an actor resolver on `CustomizeConfig`), a
  behavioural contract change, and it composes with 52/53. Wants its own ADR.
- **Fix sketch:** add `CustomizeConfig.ActorFromRequest func(context.Context) (authz.Actor, error)`
  (or a `PrincipalResolver`), default it to the current body-derived behaviour ONLY
  behind an explicit opt-in, and make `endpoints.go`'s three sites read from it;
  drop `Actor`/`By` from the DTOs once the seam exists.

### What breaks (enumerated — this is the item's specific ask)

**Production/library source (3 sites):** `httpcore/endpoints.go:119,132,150`.
**DTO shape (4 declarations):** `httpcore/dto.go:12,43,49,63`.

**Tests that currently pin the body-derived actor as the contract** — hand-counted
from `grep -rn "httpcore.Actor{|\"actor\"" transport/`:

| file | lines |
|---|---|
| `transport/http/httpcore/endpoints_test.go` | **405**, **422** (the two the item names), plus **466**, **485**, **531**, **560** |
| `transport/http/httpcore/dto_test.go` | 57, 68, 151, 161 (raw JSON bodies carrying `"actor"`) |
| `transport/http/parity/parity_test.go` | 497 |
| `transport/http/gin/gin_test.go` | 413, 421, 443 |
| `transport/http/gin/gin_coverage_test.go` | 192, 218 |
| `transport/http/fiber/fiber_test.go` | 563, 585, 592, 615 |
| `transport/http/stdlib/stdlib_test.go` | 471 |
| `transport/http/stdlib/errors_test.go` | 155 |
| `transport/http/stdlib/coverage_test.go` | 92 |

⚠ The item names only `endpoints_test.go:405,422`. That is an **undercount**: the
same contract is pinned at **21 sites across 9 files in 5 packages**. `:405`/`:422`
are merely the pair that pins *authorization outcome* (alice/manager → 200,
bob/viewer → error); the rest pin the *wire shape* and break on any DTO change.

**`examples/` callers: ZERO.** Verified — `grep -rn "httpcore\.|/tasks/|Routes\(" examples/`
returns only three `httpcore.HealthCheck` uses
(`examples/production_wiring/main.go:103`, `examples/sqlite_wiring/main.go:274`,
`examples/mysql_wiring/main.go:258`). No example mounts the task routes or posts an
actor body. `examples/scenarios/attribute_authz/main.go:92` mentions `"actor"` only
inside a **comment** about the authz expression env. **Correcting the record: no
example wiring breaks.**

- **Falsifiable test note:** **non-vacuous.** `endpoints_test.go:422` currently
  asserts an error for `{ID:"bob",Roles:["viewer"]}`. Under a seam-based fix, a test
  that establishes `bob` in the request context and sends a body claiming
  `{"id":"alice","roles":["manager"]}` must still be **denied**. That test fails today
  (the body wins), which is exactly the escalation.
- **Dependencies:** composes with **52** and **53** — fixing any one alone leaves the
  path open (a resolved principal still meets an allow-all authorizer). Interacts
  with **54** (same route family, unredacted reads) and **90** (a claim precondition,
  which a correct principal makes enforceable). BREAKING; in scope in the pre-v0.1.0
  window.

---

## 52 — `service.NewProcessEngine` defaults to `authz.AllowAll{}`; `DurableProvider` has no `Authorizer()`

- **Package(s):** `service` (defect), `persistence` (the concrete provider).
  Symbols: `service/service.go:200` — `c.authz = authz.AllowAll{}`;
  `service/durable.go:17-24` — the `DurableProvider` interface, **6 methods**
  (`InstanceStore`, `Definitions`, `Lister`, `TaskStore`, `TimerStore`,
  `CallLinkStore`) and **no `Authorizer()`**; `persistence/durableprovider.go:21-35`
  — the concrete struct mirrors those six fields exactly;
  `service/service.go:316` — `if _, ok := c.authz.(authz.AllowAll); ok { authzLabel = "allow-all" }`,
  emitted by `logConstructionSummary` at **`slog.LevelDebug`** only.
- **Verification: VERIFIED, all three sub-claims** (default, missing provider method,
  DEBUG-only logging).
- **⚠ Sharper than the item states.** The only way to supply an authorizer is
  `service.WithHumanTasks(taskStore, az)` (`service/options.go:77-86`) — there is **no
  standalone `WithAuthorizer`** (`grep -n "^func With" service/options.go` → 10
  options, none of them an authorizer). And `WithDurableStore`
  (`service/options.go:169-181`) **overwrites `c.taskStore`** from the provider, so
  the durable wiring must be written as `WithDurableStore(p)` *then*
  `WithHumanTasks(nil, az)` — passing a non-nil store there silently loses to, or
  clobbers, the provider depending on option order. That ordering trap is the real
  ergonomics defect and is not in the item's text.
- **Tier: D.** Adding `Authorizer()` to an exported interface is BREAKING for any
  third-party `DurableProvider` implementer, and "what should the default be" is a
  security posture decision (fail-open vs fail-closed vs construction error). ADR.
- **Fix sketch:** add `service.WithAuthorizer(az authz.Authorizer)`; make the
  no-authorizer case either an explicit `AllowAll` opt-in (`WithAllowAllAuthorizer()`)
  or a construction error; raise the summary from DEBUG to WARN when it resolves to
  allow-all; and decide whether `DurableProvider` grows an optional `Authorizer()`
  (capability-interface style, like `Notifier`/`Locker` in ADR-0081) rather than a
  required method.
- **Falsifiable test note:** **non-vacuous.** A test asserting
  `NewProcessEngine(WithDurableStore(p))` returns an error (or logs at WARN) fails
  today: it returns a working engine and logs at DEBUG. Assert on the resolved
  authorizer through an exported accessor or on a captured `slog` handler — ⚠ **not**
  on `logConstructionSummary`'s text, which would be a change-detector.
- **Dependencies:** composes with **51** and **53**. Interacts with **80** (caching +
  `AlwaysOwn` is also the default `DurableProvider` and also warns weakly) — both are
  "the natural wiring silently lands somewhere unsafe", and a single
  `WarnUnsafeConfig`-style pass could cover both.

---

## 53 — `RoleAuthorizer` treats an empty/zero `AuthzSpec` as allow-all

- **⚠ CONFIRMED: this is ONE item, not two.** It is the same defect as **blocker 1's
  tail** (`HANDOVER.md:241-243`) and the same defect as the `NEXT WORK` bullet
  "The fail-open `AuthzSpec`" (`HANDOVER.md:184-188`). Three prose locations, one
  defect. It is *not* a sub-case of blocker 1 proper (strict decoding), which is
  closed by ADR-0167 — an empty list is **valid input**, so a stricter decoder can
  never reach it. Track it under **53** and delete the other two restatements when
  the ADR lands.
- **Package:** `authz`. Symbols: `AuthzSpec` (`authz/authz.go:79-86`) whose godoc
  says verbatim *"An empty spec means allow-all."*; `RoleAuthorizer.Authorize`
  (`authz/authz.go:124`) whose role gate is `if len(spec.Roles) > 0 &&
  !hasAnyRole(actor.Roles, spec.Roles) { return ErrNotAuthorized }` — an empty
  `spec.Roles` short-circuits the check entirely, and with `spec.Attribute == ""`
  the function returns `nil`. `RoleAuthorizer`'s own godoc (**:110-112**) states it:
  *"spec.Roles is empty (open access)"*.
- **Verification: VERIFIED by source.** ⚠ `spec.Privileges` is documented (**:119-120**)
  as *reserved and NOT evaluated by `RoleAuthorizer`* — so a spec carrying **only**
  privileges is also allow-all under the default authorizer. That leg is not in the
  item's text; add it to the ADR's Context.
- **Tier: D.** Reversing a documented, exported default is a behavioural contract
  change with a migration story (every definition that relies on the empty spec today
  starts being denied). The handover already says "Wants its own ADR" — agreed.
- **Fix sketch:** introduce an explicit `AuthzSpec.Open bool` (or an
  `authz.OpenSpec()` constructor) so "no restriction" must be *stated*, make a zero
  spec deny under `RoleAuthorizer`, and add an authoring-time gate in
  `definition`/`model.Validate` that refuses a human-task node with neither an
  explicit open marker nor a non-empty spec — mirroring ADR-0182's never-due
  authoring gate.
- **Falsifiable test note:** **non-vacuous.**
  `RoleAuthorizer{}.Authorize(ctx, authz.AuthzSpec{}, authz.Actor{}, nil)` returns
  `nil` today; a test requiring `errors.Is(err, authz.ErrNotAuthorized)` fails on that
  exact call. Cover four fixtures: zero spec, `Roles: []string{}`, `Roles: nil`, and
  `Privileges`-only.
  ⚠ **Fixture check:** a test whose fixture supplies a non-empty `Roles` cannot fail
  for this reason — the fixture must be the *empty* one.
- **Dependencies:** the third leg of **51 + 52 + 53**; fixing 51 and 52 without this
  still resolves a real principal against an allow-all spec. Also interacts with
  **90** (silent claim theft — an eligibility check that passes vacuously is what lets
  any actor through) and with **65**/**54** (unredacted reads guarded by the same
  specs).

---

## 54 — instance/task routes carry no auth caveat; IDs default to `xid`; variables returned unredacted

- **Package(s):** `transport/http/{httpcore,stdlib,gin,fiber}` (the caveat + redaction
  seam), `runtime/idgen` (the default), `service` (where the default is chosen).
- **Verification: VERIFIED, all four sub-claims.**
  - **`SECURITY:` on `AdminRoutes` only** — exactly 3 non-test hits, all identical and
    all on the admin group: `transport/http/stdlib/groups.go:189`,
    `transport/http/gin/groups.go:204`, `transport/http/fiber/groups.go:209`
    (*"SECURITY: these routes have NO built-in authentication. Mount AdminRoutes only
    …"*). The instance and task groups carry no such comment.
  - **`idgen.UUIDv7()` exists and is opt-in** — `runtime/idgen/idgen.go:32`
    (`func UUIDv7() Generator`) vs `:24` (`func XID() Generator`); the default is
    chosen at `service/service.go` (`if c.idgen == nil { c.idgen = idgen.XID() }`).
    xid embeds machine ID + PID + a monotonic counter, so instance IDs are guessable
    /enumerable — which matters precisely because of the next two points.
  - **Variables returned verbatim** — `transport/http/httpcore/view.go:19`
    (`Variables map[string]any \`json:"variables,omitempty"\``) and **:31**
    (`Variables: st.Variables`). No redaction hook, and the assignment **aliases** the
    engine map rather than copying it.
  - **No ownership/tenant predicate** — `runtime/kernel/lister.go:118-131`,
    `InstanceFilter` is exactly `Status`, `Limit`, `Cursor`, `IncludeTotal`.
    No owner, tenant, or subject field anywhere on the read path.
- **Tier: D.** A redaction seam is new public API (`CustomizeConfig`), and an
  ownership predicate is a cross-package mechanism reaching `InstanceFilter`, the
  store queries and the transport. Two ADRs' worth; at minimum one.
- **Fix sketch:** (a) add the `SECURITY:` caveat to the instance/task route groups —
  that half is `S` and can ship immediately; (b) add
  `CustomizeConfig.RedactVariables func(map[string]any) map[string]any` and make
  `view.go` route `st.Variables` through it, defaulting to a **copy** (fixing the
  aliasing regardless); (c) an ownership predicate is its own decision — see 62.
- **Falsifiable test note:** the redaction half is **non-vacuous**: a test mounting a
  `RedactVariables` that drops `"ssn"` and asserting the JSON response omits it fails
  today with `undefined: RedactVariables`, then with the key present. The aliasing
  half is separately falsifiable: mutate the returned `view.Variables` map and observe
  the engine's `InstanceState.Variables` change. ⚠ The caveat-comment half has **no
  test** and should not pretend to — it is a doc change.
- **Dependencies:** shares the redaction concern with **65** (`httpcall` response
  bodies land in `httpBody`, read out through exactly this path). The ownership
  predicate overlaps **62**'s `InstanceFilter` widening — do them together or the
  filter gets reworked twice. Depends on **51**/**52**/**53** for there to be a
  principal to check ownership against at all.

---

## 55 — `drive` has no iteration budget

- **Package(s):** `engine` (the loop), `definition/model` (the authoring-time gate).
  Symbols: `drive` — `engine/step.go:304`, whose loop is `for {` at **:306** with the
  only exit being `if tok := s.firstActive(); tok == nil { break }` at **:307-310**;
  `InstanceState.openVisit` — `engine/step_state.go:251-253`, literally
  `s.History = append(s.History, NodeVisit{…})` with no cap, called from
  `step_gateways.go:251`, `step_state.go:51,61,205,275`.
- **Verification: VERIFIED, and the "no cycle detection" claim needs a precise
  restatement.** `model.Validate` **does** have a cycle guard —
  `definition/model/validate.go:288-296`, `validateStructure(d, seen map[*ProcessDefinition]bool)`
  — but it guards the **sub-process pointer graph** against stack overflow, not
  sequence-flow cycles. The only flow-graph walk, `forwardReachable`
  (`validate.go:831-845`), is explicitly *"BFS, cycle-safe via the visited set"* — it
  **tolerates** a flow cycle, it does not reject one. So: no hop cap, no runtime cycle
  guard, no authoring-time flow-cycle rejection. All three legs stand.
  ⚠ The handover already records the audit's own repro sketch as **REJECTED by
  validation** — use a join/split pair, not `gwA -[true]→ gwB → gwA`.
- **Tier: D.** An engine-core hang. A budget is a new engine-wide policy knob with a
  public error, and "reject at authoring time vs bound at runtime vs both" is a real
  trade-off (a legitimate loop-until-approved process is a cycle). ADR.
- **Fix sketch:** add a `stepPolicy` hop budget consumed by `drive`'s loop, returning a
  new sentinel (`workflow-engine: …` prefix per convention) and — per the incident
  machinery that already exists — raising an incident rather than failing the
  instance; separately cap/monotonically-bound `openVisit`'s `History` growth (see
  **73**, which measures the cost of that slice, and **114**, the deep-copy trap in
  the same area).
- **Falsifiable test note:** **non-vacuous, with a caveat.** A join/split cycle drives
  1.44 M hops in 2 s and `Step` never returns — so a naive test **hangs the binary**
  rather than failing. Write it as a `Step` call under a bounded context with the
  budget asserted, i.e. the test must be written *against the fix*, and its RED is
  "the budget constant does not exist / `Step` does not return". ⚠ Do **not** write a
  test that relies on `Step` returning today — it does not.
- **Dependencies:** **73** (per-`Step` O(entire state), driven by the same `History`
  slice) and **114** (the uncovered `cloneState` deep-copy on that slice) are the same
  data structure — sequence 114 → 73 → 55 or the budget fix ships state corruption
  under a green suite. Independent of 51/52/53.

---

## 56 — incident lifecycle is token-keyed, not command-keyed

- **Package:** `engine`. Symbols, all verified:
  - `handleUnhandledError` — `engine/step_errors.go:212`; at **:222-223** it reads
    `cmdID = failingTok.AwaitCommand` and sets `failingTok.State = TokenIncident`
    **without clearing `AwaitCommand`**, then appends the `Incident` at **:225-234**.
  - `InstanceState.tokenAwaiting` — `engine/step_state.go:79-89`; the loop body is
    `if s.Tokens[i].AwaitCommand == cmdID { return &s.Tokens[i] }` — **no `State`
    check**, so a `TokenIncident` token is still matched by a late `ActionCompleted`.
  - `handleResolveIncident` — `engine/step_triggers.go:1370`; at **:1425-1439** it
    removes the incident, resolves `tok := s.tokenByID(inc.TokenID)` and calls
    `reinvokeServiceAction(ctx, def, s, tok, …)`, which at
    `engine/step_timers.go:232` resolves `tdef.Node(tok.NodeID)` — **the token's
    current node, not `inc.NodeID`**.
- **Verification: VERIFIED by source, every link in the chain.** The handover's own
  hedge is correct and must be preserved: the orphan-incident leg is *conditional* on
  the token surviving; the **double-invoke leg is unconditional**.
- **Tier: D.** Fixing it changes when an incident is resolvable and what a late
  `ActionCompleted` does to an incident token — a behavioural contract change on the
  admin API, adjacent to ADR-0165's terminal guard and ADR-0175's verb whitelist.
  Both candidate fixes (clear `AwaitCommand` on incident; or key resolution on
  `inc.CommandID`/`inc.NodeID`) have visible consequences. ADR.
- **Fix sketch:** stop matching incident tokens in `tokenAwaiting` (add
  `s.Tokens[i].State != TokenIncident`, or clear `AwaitCommand` via the existing
  `clearAwait` helper at `engine/state.go:147` — whose godoc already says *"every site
  that clears AwaitCommand calls this instead; that is the invariant"*), and make
  `handleResolveIncident` re-invoke `inc.NodeID` rather than `tok.NodeID`.
- **Falsifiable test note:** **non-vacuous, and the fixture is the whole test.**
  Drive a service task to an incident, deliver a late `ActionCompleted` carrying the
  incident token's `AwaitCommand`, then `ResolveIncident`. Today: the late trigger
  resumes the `TokenIncident` token (`tokenAwaiting` matched it) and `ResolveIncident`
  emits an `InvokeAction` for the token's *new* node while the first is in flight.
  Assert the emitted `Command`'s node ID equals `inc.NodeID` — it does not today.
  ⚠ **Fixture check:** the definition must have a node *after* the failing one, or
  `tok.NodeID == inc.NodeID` and the assertion cannot fail.
- **Dependencies:** touches `tokenAwaiting`, which is on the trigger hot path — this
  is an `engine`-internal change, so per rule #11 it runs **strictly serial** with
  **55**, **70**–**74** and any other `engine` item. Related to **64** (compensation/
  deadline/reminder actions carry no idempotency key — a double-invoke is exactly the
  class that key would deduplicate).

---

## 57 — one undecodable outbox row halts the entire relay

- **Package(s):** `internal/persistence/store` (the defect), `persistence` (the
  public godoc that blesses it). Symbols:
  - `scanClaimRows` — `internal/persistence/store/relay.go:416`; **`:432`**
    `return nil, fmt.Errorf("workflow-store: relay: unmarshal payload id=%d: %w", id, err)`
    and **`:437`** `… "workflow-store: relay: def ref id=%d: %w"` — both abort the
    **whole batch** on one bad row.
  - `Relay.drainUntilEmpty` — **`:452-462`**, `if err != nil { return err }`.
  - `Relay.Run` — **`:481`**; the pre-tick drain and the ticker branch both
    `return err` when `ctx.Err() == nil` — no backoff, no skip.
  - `Relay.DrainOnce` — **`:241`** (the per-row quarantine lives on the *publish*
    path, not here).
- **Verification: VERIFIED, including the godoc claim, and the attribution is
  correct.** The false blessing is on the **public** interface:
  `persistence/persistence.go:69-76` — *"Publish failures are absorbed per-row … Only
  infrastructure errors (claim or commit failures) propagate and terminate the loop."*
  A JSON decode failure is neither a claim nor a commit failure, yet it propagates.
  The same sentence is repeated on the internal `Run` godoc
  (`internal/persistence/store/relay.go`, Run's comment block).
- **Tier: D.** Where a poison row goes is a durability decision (quarantine on decode
  vs skip-and-log vs dead-letter with the raw bytes preserved), and it changes a
  **documented public contract** on `persistence.Relay`. ADR.
- **Fix sketch:** make `scanClaimRows` collect per-row decode failures instead of
  aborting — return the good rows plus a `[]failedRow`, and have `DrainOnce` route the
  bad ones through the **existing** dead-letter path (`ListDeadLettered`/`Redrive`
  already exist on the public interface, so no new verbs are needed); then correct
  `persistence.Relay.Run`'s godoc.
- **Falsifiable test note:** **non-vacuous.** Insert an outbox row whose `payload`
  column is invalid JSON (e.g. `'{'`) plus a valid row behind it; today `DrainOnce`
  returns `(0, "workflow-store: relay: unmarshal payload id=N: …")` and the valid row
  is never published. Assert the valid row IS published and the bad one is
  `status='dead'`. ⚠ Needs a real database — **Docker required**; state that in the
  implementing agent's brief (Golang rule #3 / the standing Docker carve-out does
  **not** extend to subagents).
- **Dependencies:** interacts with **81** (the relay holds row locks across a network
  `Publish`; both are `DrainOnce` batch-shape changes and should not be designed
  independently) and **91** (no schema/version envelope — the reason a payload becomes
  undecodable in the first place is usually a shape change).

---

## 58 — `examples/production_wiring` has durable instance state but no durable timers

- **Package:** `examples/production_wiring` (`examples/production_wiring/main.go`).
- **Verification: VERIFIED for every claim I could check statically.**
  - **No `WithScheduler`, no `WithTimerStore`** — `grep -n` for both in
    `examples/production_wiring/main.go` → **0 hits**. The driver is built at
    **`:162`** (`runtime.NewProcessDriver(`).
  - **Timers DO fire** — `runtime/processdriver.go:228-234`: *"Default scheduler: when
    the consumer did not wire a usable one via [WithScheduler], create an in-process
    gocron-backed scheduler."* Confirms the handover's ⚠ REWRITE: the original "timers
    silently disabled" statement was **FALSE** and must not be re-propagated.
  - **`notifier` appears 3× and all three are comments** — `:13`, `:71`, `:216`, each
    inside a `//` block. Hand-counted; **exactly 3**.
  - **No metrics** — 0 hits for `Metric`/`Meter`.
  - **`AdminRoutes` not mounted** — 0 hits in the file.
  - **The `DATABASE_URL` branch** is at **`:105`**, with the in-memory fallback logged
    at **`:137`**.
  - ⚠ **`ASSUMPTION (unverified)` — the durability measurement** ("`RehydrateTimers`
    refuses; a 2 s timer never fired 8 s after restart while the instance stayed
    `running`") is **inherited from the 2026-08-20 run**, not re-derived here. It needs
    a live driver + store; I did not run it. Its *precondition* (no `WithTimerStore`)
    is verified, which is what makes it plausible.
- **Tier: S.** It is an example file: add `WithTimerStore(provider.TimerStore())` and
  `WithScheduler(...)` on the `DATABASE_URL` branch, mount `AdminRoutes` behind the
  existing `SECURITY:` caveat, and wire a meter. No public API, no decision.
  ⚠ Do **not** let it grow into "rewrite the example" — that is a different item.
- **Falsifiable test note:** ⚠ **vacuity-risk as an assertion**; examples have no
  tests. The honest falsifier is the restart probe itself: arm a 2 s timer, restart
  the process, observe whether it fires. What makes it fail today is the absence of
  `WithTimerStore`, which is grep-verifiable. If a test is wanted, it belongs in
  `runtime` (does `RehydrateTimers` refuse without a timer store?), not in `examples`.
- **Dependencies:** overlaps **121** (documentation/example rot cluster, per the
  handover's own cross-reference) and **54** (the `AdminRoutes` `SECURITY:` caveat this
  example would be mounting under). Independent of everything else in this slice.

---

## 59 — no stuck-instance observability, and the active gauge drifts NEGATIVE

- **Package(s):** `runtime` (the driver instruments), `runtime/monitor` (the DB-truth
  collectors).
- **Verification: VERIFIED — with ONE WRONG COUNT in the item's own text.**
  - ⚠ **`driverObs` holds 12 instruments, not 13.** `runtime/observability.go:17-32`
    declares `tel observability.Telemetry` plus exactly twelve metric fields:
    `instStarted`, `instCompleted`, `instActive`, `stepDuration`, `actionDuration`,
    `actionRetries`, `actionFailures`, `timerFired`, `timerArmsRefused`,
    `incidentsRaised`, `incidentsResolved`, `humanTasks`. Hand-counted twice, and
    cross-checked against the twelve `tel.*` constructor lines at **`:48-59`**.
    The "13" appears to count the `tel` field as an instrument. **Correct the handover.**
  - **`instActive` is the only UpDownCounter** — `:22`/`:50`,
    `metric.Int64UpDownCounter("wrkflw_instances_active", …)`. VERIFIED, sole hit.
  - **Zero hits for `oldest_active`, `incidents_open`, `compensation_walks`** —
    all three greps → **0**. VERIFIED.
  - **`incidentsRaised` carries no incident-kind attribute** — its only call site,
    `runtime/processdriver.go:707`, is
    `driver.obs.incidentsRaised.Add(ctx, 1, metric.WithAttributes(attribute.String("def", def.ID)))`
    — `def` only. VERIFIED, and it means ADR-0179's `IncidentCompensationFailed`
    cannot be split out on a dashboard.
  - **Exactly 2 DB-truth collectors** — `runtime/monitor/stats_collector.go:36`
    (`NewOutboxStatsCollector`, three gauges incl. `wrkflw_outbox_oldest_pending_age_seconds`)
    and **`:116`** (`NewTimerStatsCollector`, one gauge `wrkflw_timers_armed`).
    There is **no instance-truth collector** — which is exactly why `instActive` can
    drift and nothing corrects it. VERIFIED.
  - ⚠ **`ASSUMPTION (unverified)`: the `active=-3` measurement.** Inherited from the
    2026-08-20 run; I did not restart a driver. Its *mechanism* is verified by source
    (a process-local UpDownCounter with no DB-truth reconciler), which is the part a
    fix depends on.
- **Tier: D.** Adding a DB-truth instance collector means a new `InstanceStatsReader`
  port + store queries + a public constructor — a cross-package mechanism with a new
  public API. The handover is right that it is **part of finding A's (item 66) fix**,
  not separable: a permanently-stuck instance must be *visible* before an escape hatch
  is useful.
- **Fix sketch:** add `kernel.InstanceStatsReader` + `monitor.NewInstanceStatsCollector`
  exposing `wrkflw_instances_active` (DB truth, replacing or shadowing the
  process-local UpDownCounter), `wrkflw_instances_oldest_active_age_seconds` and
  `wrkflw_incidents_open`; add an `incident_kind` attribute to `incidentsRaised.Add`.
- **Falsifiable test note:** the **attribute** half is cheap and non-vacuous — assert
  the recorded metric carries `incident_kind`; it fails today because only `def` is
  attached (`processdriver.go:707`). The **negative-drift** half needs a real store and
  a driver restart; write it as a `runtime` test with a durable store and assert the
  collector reports DB truth, not the counter. ⚠ Docker required for the second half.
- **Dependencies:** **66** (same fix, per the handover), **69** (an escape hatch you
  cannot see the need for is useless), **37**/**39** (leaked timer rows are one of the
  stuck states this would surface).

---

## 60 — traces die at every async boundary

- **Package(s):** `runtime`, `engine`, `eventing`, `scheduler`,
  `internal/persistence/store` (all five would need the seam);
  `transport/http/httpcore` (where propagation already exists and stops).
- **Verification: VERIFIED.** `grep -rn traceparent` → **0** `.go` hits repo-wide;
  `grep -rn TraceContext` → **0**. The **only** propagation in the repo is at the
  inbound HTTP edge: `transport/http/httpcore/observability.go:12`
  (`import "go.opentelemetry.io/otel/propagation"`), `:27`
  (`propagator propagation.TextMapPropagator`), `:87`
  (`ctx = i.propagator.Extract(ctx, propagation.HeaderCarrier(hdr))`). Nothing
  **injects**, and no outbox or timer table carries a trace column — a timer fire, a
  relay publish and a task completion therefore each root a fresh trace.
- **Tier: D.** A `traceparent` column on the outbox and timer tables is a **schema
  migration** plus a cross-package context-carrying convention. Squarely ADR
  territory, and it touches five packages.
- **Fix sketch:** add a nullable `traceparent` column to `wrkflw_outbox` and
  `wrkflw_timers` (all three dialects), inject the current span context at
  write time, extract-and-link at fire/publish time, and give
  `kernel.OutboxEvent`/the timer record a carrier field. ⚠ Per CLAUDE.md the engine
  **core** stays vendor-free — the carrier must be a plain `map[string]string` or
  `string`, not an OTel type, at the `engine` boundary.
- **Falsifiable test note:** **non-vacuous.** Start a span, drive a step that arms a
  timer, fire the timer, and assert the fired span's `TraceID` equals the starting
  one. Today it differs (a fresh root), so the assertion fails. Use the OTel
  `tracetest.SpanRecorder`. ⚠ **Fixture check:** the test must *start a span* — with no
  parent span there is nothing to propagate and the assertion is vacuous.
- **Dependencies:** shares the outbox-row shape with **57** and **91** (envelope) —
  a `traceparent` field is an envelope field, so **91 and 60 should be one ADR**, not
  two migrations of the same table. Interacts with **59** (both are the observability
  story).

---

## 61 — `engine.InstanceState` exports public fields whose types are unexported ⚠ BREAKING-ish

- **Package(s):** `engine` (the struct), `runtime/kernel` (the store contract that
  traffics in it).
- **Verification: VERIFIED, and the "…" in the item resolves to exactly one more
  field — the closed set is FIVE:**

  | field | line | unexported type | declared at |
  |---|---|---|---|
  | `Timers []timerRecord` | `engine/state.go:300` | `timerRecord` | `engine/state_timers.go:12` |
  | `ArmedEvents []armedEvent` | `:307` | `armedEvent` | `engine/state_arms.go:52` |
  | `Boundaries []boundaryArm` | `:314` | `boundaryArm` | `engine/state_arms.go:70` |
  | `EventTriggeredSubprocesses []eventTriggeredSubprocessArm` | `:345` | `eventTriggeredSubprocessArm` | `engine/state_arms.go:104` |
  | `Compensating compensationCursor` | `:352` | `compensationCursor` | `engine/state_compensation.go:69` |

  The store contract is `runtime/kernel/ports.go:69-73` —
  `InstanceStore{ Create(…AppliedStep), Load(…) (engine.InstanceState, Version, error),
  Commit(…) }` — so a third-party store does receive the struct. VERIFIED.
  The "grew since the audit" claims are all VERIFIED in `engine/state.go`:
  `Token.AwaitTimer` (**:118**, ADR-0177), `Token.RetryAttempts` (**:125**),
  `Incident.Kind IncidentKind` (**:228**, ADR-0179).

### What breaks (enumerated — this is the item's specific ask)

⚠ **Sharper than the item states: exporting the five types is NOT a consumer break.**
Verified by enumeration — **zero Go code outside `engine/` names any of the five**.
Every out-of-package hit is a **comment**, and each comment says so explicitly:

| reference | kind |
|---|---|
| `processtest/park_compensation_failure_test.go:173` | comment — *"because timerRecord is …"* |
| `processtest/park_compensation_stall_test.go:85` | comment — *"timerRecord is unexported, …"* |
| `runtime/signal_boundary_e2e_test.go:110` | comment — *"tracked as an armedEvent"* |
| `runtime/event_gateway_message_e2e_test.go:59` | comment |
| `service/compensation_stall_test.go:48` | comment — *"compensationCursor is unexported, but its FIELDS are exported"* |

`boundaryArm` and `eventTriggeredSubprocessArm`: **zero** references outside `engine/`
at all. **`examples/` callers: ZERO** for all five.

So the mechanical rename is confined to `engine/`: **24–34 non-test sites per type**
(`timerRecord` 24, `armedEvent` 34, `boundaryArm` 26, `compensationCursor` 25,
`eventTriggeredSubprocessArm` 25), plus their tests (55/64/49/75/48 total hits).
Per rule #11 this is a **single-package, strictly serial, compile-breaking** change —
it must stay **inline in the controller**, never fanned out.

**Where the REAL break is:** the durable **wire format**. Backlog 32 records that the
snapshot has **no json tags at all**, so the persisted field names are the Go field
names. Renaming a *type* does not move the wire; renaming or restructuring a *field*
does, and every stored row would need a migration. ⚠ **Do not let this item's fix
quietly add json tags** — that is backlog 32's decision, not this one's.

- **Tier: D.** Even though the rename is additive, the item is really "what is the
  durable contract of `InstanceState`, and who owns it" — the same question as
  backlog **32** and item **62**. It needs a versioning decision, not just `sed`.
- **Fix sketch:** export the five types (mechanical, inside `engine/`), document each
  as part of the `InstanceStore` durable contract, and — separately, under backlog 32's
  ADR — decide the snapshot's versioning/tagging scheme before v0.1.0 freezes it.
- **Falsifiable test note:** **non-vacuous.** An **external** (`package engine_test` in
  a different directory, or a tiny `_test` package under `processtest`) test that
  constructs `engine.InstanceState{Timers: []engine.TimerRecord{{…}}}` does not
  compile today (`undefined: engine.TimerRecord`). That compile failure IS the RED.
  ⚠ It must live outside `package engine`, or it can name the unexported type and
  cannot fail.
- **Dependencies:** **32** (unversioned snapshot — the blocking decision), **62**
  (the API-surface question), **114** (`cloneState`'s deep-copy over these very
  fields — read it before touching them), **73**. BREAKING window: pre-v0.1.0, so it
  is in scope now and gets much more expensive after.

---

## 62 — the read half of three APIs is missing ⚠ BREAKING (additive-plus)

- **Package(s):** `humantask` (the store interface), `service` + `transport/http/httpcore`
  (where the exposure would go), `runtime/kernel` (`InstanceFilter`), `persistence`
  (definition lifecycle).
- **Verification: two legs VERIFIED, one leg needs a CORRECTION.**
  1. **Task inbox — VERIFIED.** `AssignedTo`/`ClaimableBy` are declared on
     `humantask.TaskStore` (`humantask/humantask.go:206,210`) and implemented by
     `MemTaskStore` (`humantask/memory.go:69,94`) and `HumanTaskStore`
     (`internal/persistence/store/humantask_store.go:227,241`). Both signatures return
     a bare `[]HumanTask` — **no limit, no cursor**. `grep -rn ListTasks` over the whole
     repo returns **one hit, and it is `HANDOVER.md` itself** — the symbol does not
     exist. Zero references in `service/` or `transport/`. ⚠ The handover's own
     correction is CONFIRMED: **six** `examples/` mains call `ClaimableBy` —
     `inwait_reminder`, `input_validation`, `instance_cancellation`,
     `completion_action`, `usertask_approval`, `cache_wiring`. So the audit's
     "exactly one non-mock reference" (R) is false, as already recorded.
  2. **`InstanceFilter` — VERIFIED exactly.** `runtime/kernel/lister.go:118-131` is
     precisely `Status *engine.Status`, `Limit int`, `Cursor string`,
     `IncludeTotal bool`. No `DefID`, no time range, no variable filter, and **no
     owner/tenant field** (which is also item **54**'s missing predicate).
  3. ⚠ **Definition lifecycle — the "no list" sub-claim needs restating.**
     `PutDefinition`+`Lookup` are the only two methods on the public
     `persistence.DefinitionStore` (`persistence/persistence.go:55-64`) — VERIFIED —
     and `RetireDefinition`/`MigrateInstance` have **zero hits repo-wide** — VERIFIED.
     **But `ListDefinitions` DOES exist**: `kernel.DefinitionLister.ListDefinitions`
     (`runtime/kernel/definition_lister.go:16-20`), implemented by
     `MemDefinitionRegistry` (`runtime/kernel/mem_definition_registry.go:122`),
     `CachingDefinitionRegistry` (`:139`) and `definition_registry.go:61`. It is
     consumed only by `ProcessDriver.listDefinitions`
     (`runtime/processdriver.go:523-529`) — **unexported**, and used solely to enable
     event-based START. `grep -rn ListDefinitions service/ transport/` → **0 hits**.
     Correct statement: *the enumeration capability exists at the kernel port and is
     not surfaced through `service` or any transport.*
- **Tier: D.** Three new public read APIs plus pagination contracts. Obviously ADR
  work; probably **three** ADRs, or one with three decisions.
- **Fix sketch:** (a) add `ListTasks(ctx, TaskFilter) ([]HumanTask, string, error)` to
  `humantask.TaskStore` — ⚠ that is a **breaking interface change** for every consumer
  store; (b) surface `ListDefinitions` through `service` + `httpcore`, and add
  retire/migrate as a separate decision; (c) widen `InstanceFilter` with `DefID`, a
  time range and — jointly with **54** — an ownership predicate.
- **What breaks (the item's specific ask):** adding a method to `humantask.TaskStore`
  breaks every third-party implementer at **compile time** — which is *good* (loud,
  not silent) and is exactly why `processtest.RunTaskStoreConformance` exists. In-repo
  implementers to update: `humantask.MemTaskStore` (`humantask/memory.go`) and
  `internal/persistence/store.HumanTaskStore`, plus the mock double and
  `processtest/taskstoreconformance.go`'s case set. ⚠ Widening `InstanceFilter` is
  **additive** (a struct, not an interface) and breaks nothing. ⚠ Adding a method to
  `service.DurableProvider` (item **52**) is the same class of break — **bundle 52 and
  62 or the interface breaks twice**.
- **Falsifiable test note:** **non-vacuous.** `svc.ListTasks(ctx, …)` does not compile
  today (`undefined`); `InstanceFilter{DefID: "x"}` does not compile today
  (`unknown field`). Those compile failures are the RED. ⚠ For the pagination
  contract, extend `RunTaskStoreConformance` rather than writing a store-specific
  test — and per ADR-0184's lesson, every new case must declare which query returns
  it, or the assertion is vacuous.
- **Dependencies:** **54** (ownership predicate on the same filter), **52** (the same
  provider-interface break), **51** (a principal to filter by), **32**/**61** (what the
  durable contract is). ⚠ **ADR-0184 hardened the store *conformance* contract, not the
  API surface — it does NOT close this**, as the item correctly warns.

---

## 63 — a timer armed on a non-leader replica is never fired under stable leadership

- **Package(s):** `scheduler` (the elector contract), `scheduler/internal/gocron`
  (the gate), `runtime` (rehydration), `internal/persistence/store` (the reclaim job).
- **Verification: VERIFIED by source.**
  - The gate: `scheduler/elector.go:6,19-21` — the `Elector` godoc states it outright,
    *"is elected leader and runs ALL timer fires; on the others [Elector.IsLeader] …"*;
    the adapter that feeds gocron is `scheduler/internal/gocron/adapt.go:29,70`.
    `myelector/mysql_elector.go:41` repeats it: *"the others' IsLeader returns
    ErrNotLeader so gocron [does not run]."* The job is registered in the arming
    replica's own gocron and gated off there; the leader has no such registration.
  - **The only path that re-reads durable rows is boot rehydration** — exactly two
    rehydrators exist repo-wide: `ProcessDriver.RehydrateTimers`
    (`runtime/timerops.go:385`) and `RehydrateStartTimers` (**:548**). VERIFIED by
    `grep -rn "func.*Rehydrate"` → 2 non-test hits. Nothing runs while leadership is
    stable.
  - ⚠ **`ReclaimNeverDueTimers` deletes, it does not arm** — VERIFIED:
    `internal/persistence/store/pruner.go:284-292`, the statement is
    `DELETE FROM wrkflw_timers …`.
- **Tier: D.** Cross-package leader/ownership mechanism with a durable-state
  reconciler. It is the multi-replica half of the same class as **66**/**76**/**77**.
  ADR.
- **Fix sketch:** a leader-side periodic reconciler that re-reads due/overdue
  `wrkflw_timers` rows and arms any it does not already hold (rather than only at
  boot) — i.e. make `RehydrateTimers` a *recurring* leader job with an ownership/lease
  column, not a one-shot. ⚠ Do **not** reuse `ReclaimNeverDueTimers`, which deletes.
- **Falsifiable test note:** **non-vacuous but needs two drivers.** Arm a timer through
  a driver whose elector returns `ErrNotLeader`, run a second driver that IS leader,
  advance past the fire time, and assert the fire is delivered. Today it is not (the
  job lives only in the non-leader's gocron; the leader has no registration).
  ⚠ Docker required (durable timer store). ⚠ **Fixture check:** the non-leader driver
  must actually *arm* (i.e. reach `Activate`), or the test proves nothing.
- **Dependencies:** **76** (every replica arms every timer — the *opposite* failure
  mode of the same missing ownership model; design them together or the fix for one
  causes the other), **77** (a post-commit `Activate` failure is the same "durable but
  unarmed" state), **24**, **66**, **69**.

---

## 64 — the `Action` contract is unwritten

- **Package(s):** `action` (the contract), `runtime` (where the real behaviour lives),
  `engine` (where the key is stamped).
- **Verification: VERIFIED, and the handover's ⚠ correction to the audit is CONFIRMED —
  plus a further sharpening.**
  - The interface is bare: `action/action.go:12-14` —
    `Do(ctx context.Context, in map[string]any) (out map[string]any, err error)`.
    Neither the package doc (`:1-5`) nor the interface doc (`:9-11`) states a timeout,
    panic-recovery or at-least-once semantics, and there is **no `CommandID` and no
    attempt number** in the signature. VERIFIED.
  - **The stable key exists and is narrowly scoped** —
    `engine/step_state.go:337-343`, `serviceActionInput` sets
    `in["_idempotencyKey"] = s.InstanceID + ":" + node.ID()`, and its own godoc at
    **:332-336** says verbatim: *"v1 scope: only the primary service-task action
    carries this key. Deadline, reminder, and compensation actions do NOT."* Its only
    call site is `engine/step_nodes.go:52`; `reinvokeServiceAction`
    (`engine/step_timers.go:237-239`) re-emits the same key. So the audit's "no key at
    all" was wrong and the handover's narrower statement is correct.
  - ⚠ **Further sharpening the handover does not have:** the *behaviours* the item says
    are "not stated" mostly **exist** — they are just undocumented on the contract.
    `runtime/processdriver_action.go:156` has `if rec := recover(); rec != nil`
    (panic recovery), and `runtime/processdriver_options.go:49-59` defines
    `defaultActionTimeout = 30 * time.Second` with `WithActionTimeout`. So this is a
    **documentation + signature** gap, not a missing-behaviour gap — which makes it
    much cheaper than it reads. Do not design a timeout that already ships.
- **Tier: D.** Adding a `CommandID`/attempt to `Do` changes an exported interface every
  consumer implements — the single most breaking change available in this repo — and
  "what the key is for compensation/deadline/reminder" is a genuine design question
  (the existing godoc explains why the naive `instanceID:nodeID` would be *wrong*
  there). ADR.
- **Fix sketch:** (a) **`S`, do first:** write the contract into `action/action.go`'s
  godoc — timeout (30 s default, `runtime.WithActionTimeout`), panic recovery,
  at-least-once delivery, and the `_idempotencyKey` convention with its v1 scope;
  (b) **`D`:** extend the key to compensation/deadline/reminder with a *discriminated*
  form (e.g. `"<instanceID>:<nodeID>:compensate:<attempt>"`) so it cannot collide with
  the primary action's — the exact collision the current godoc gives as the reason for
  omitting it.
- **Falsifiable test note:** **non-vacuous for (b).** Assert that a compensation
  action's `in` map contains `_idempotencyKey`; today it does not (only
  `engine/step_nodes.go:52` stamps it), so `assert.Contains(in, "_idempotencyKey")`
  fails. ⚠ **Fixture check:** the definition must declare a `CompensateAction` **and**
  the test must capture the *compensation* invocation, not the primary one — capturing
  the primary passes vacuously. (a) is a doc change with no test.
- **Dependencies:** **56** (the unconditional double-invoke a key would deduplicate),
  **85**/**88** (the other idempotency gaps), **79**. ADR-0179's compensation retry is
  precisely the path that re-invokes keyless actions — that is what makes (b) urgent.

---

## 65 — `httpcall`'s URL/body may derive from process variables with no SSRF guard

- **Package:** `action/httpcall`. Symbols: `WithURLExpr`
  (`action/httpcall/httpcall.go:125-134`) — `prog, err := expr.Compile(exprStr)`,
  i.e. **raw `expr.Compile`, not the repo's `expreval`**, so no evaluation timeout;
  the default client at **`:209`** — `client: &http.Client{Timeout: 30 * time.Second}`,
  with **no `CheckRedirect`** (0 hits repo-wide in the package) and no address
  allowlist.
- **Verification: VERIFIED, and the godoc does name the hazard**
  (`httpcall.go:119-123`): *"The resulting URL is not validated; do not derive it from
  untrusted input without an allowlist or a restricted \*http.Client transport (SSRF
  risk)."* So this is a **knowingly-documented** hazard, not an oversight — which
  changes the adjudication from "bug" to "should the library ship a safe default".
  ⚠ The `expreval` bypass is the *undocumented* half and is a CLAUDE.md pitfall in its
  own right (the repo has an in-house evaluator precisely so expressions are bounded);
  it is also the same class as **99** (an unmetered expression stall).
- **Tier: D.** A default-deny SSRF policy is a security posture decision with a
  migration story for anyone relying on the current permissive behaviour, and it adds
  public options. ADR. ⚠ The `expreval` swap alone would be `S` — consider splitting.
- **Fix sketch:** route `WithURLExpr` through `expreval` (bounded evaluation, matching
  every other expression site); add `WithAllowedHosts([]string)` / a default
  `CheckRedirect` that refuses cross-host and link-local/loopback/metadata addresses
  (`169.254.169.254`, RFC1918, `::1`); make the permissive mode explicit
  (`WithUnrestrictedTransport()`).
- **Falsifiable test note:** **non-vacuous.** Configure `WithURLExpr("vars.url")` with
  `vars.url = "http://169.254.169.254/latest/meta-data/"` and assert `Do` returns a
  non-retryable error. Today it performs the request. Second case: a redirect to a
  loopback address — today it follows (no `CheckRedirect`). The `expreval` half is
  separately falsifiable with a pathological expression and a wall-clock bound.
  ⚠ Use `httptest` for the redirect case; do **not** dial real link-local addresses in
  CI — assert on the transport's refusal, not on a network result.
- **Dependencies:** **54** (response bodies land in `httpBody` and are read out
  unredacted through the instance read), **99** (unmetered expression evaluation —
  same `expr.Compile`-without-`expreval` class), **53** (whether the caller was
  authorized to reach the node at all).

---

## 66 — post-commit projections have no crash-recovery path

- **Package(s):** `runtime` (all four projections), plus wherever the reconcilers
  would live.
- **Verification: VERIFIED by source.** In `ProcessDriver`'s commit loop
  (`runtime/processdriver.go`), everything after the tx is non-transactional and
  best-effort:
  - timer `Deactivate` at **`:850`**, logged as *"timer cancel: post-commit deactivate
    failed (continuing)"* at WARN and swallowed;
  - `driver.syncWaiters(st)` at **`:859`**, commented *"Reconcile signal-bus and
    message waiters after each committed save"*;
  - action dispatch — `driver.perform(stepCtx, def, st, c)` at **`:868`** — inside the
    same post-commit block (human-task `Upsert` runs under it).
  - **Only two reconcilers exist repo-wide**: `RehydrateTimers`
    (`runtime/timerops.go:385`) and `RehydrateStartTimers` (**:548**). VERIFIED.
  - **No `ReconcileInstance` / `RetryCommand` verb** — `grep -rn` over `.go` returns
    exactly **2 hits, both a substring of a test NAME**
    (`engine/step_compensation_retry_dying_and_late_reply_test.go:74,92`,
    `TestLateReplyToASupersededRetryCommandIsBenign`). The verbs do not exist.
    ⚠ This is the CLAUDE.md pitfall in miniature — a grep that "found something" found
    nothing.
- **Tier: D.** This is the audit's dominant theme and a whole subsystem: a reconciler
  loop, an ownership/lease model, and new operator verbs. It is the **class**; **37**,
  **63**, **67**, **77** are instances. Needs a program of ADRs, not one.
- **Fix sketch:** derive every post-commit projection from durable state and add a
  leader-side reconciler that re-derives waiters, tasks and in-flight commands from the
  committed `InstanceState` — i.e. make `syncWaiters` idempotently re-runnable at boot
  and on a tick, not only as a step side-effect (the state **already carries** the
  waiters, per **67**).
- **Falsifiable test note:** **non-vacuous per instance, vacuous as a class.** Do not
  write "a test for item 66" — write **67**'s test (restart, deliver a correlated
  message, assert it routes) and **77**'s. What makes those fail today is that
  `driver.msgWaiters` is empty after a restart even though `st.MessageWaiters()` is not.
- **Dependencies:** **parent of 37, 63, 67, 77**; blocked-by nothing, blocks **69**
  (an escape hatch presupposes knowing what is stuck) and **59** (you cannot reconcile
  what you cannot see). Sequence: 59 → 67 → 66's general verb.

---

## 67 — message and signal waiters are process-local, with no durable row and no boot reconciler

- **Package:** `runtime`. Symbols: `ProcessDriver.syncWaiters`
  (`runtime/processdriver_waiters.go:20-23`), `syncSignalBus` (**:32-44**, delegating
  to the in-memory `driver.sigbus.Sync`), `syncMsgWaiters` (**:55-93**, mutating
  `driver.msgWaiters` — an in-memory `map[msgKey]string` under `driver.msgMu`),
  `findMessageWaiter` (**:97+**). The single call site is
  `runtime/processdriver.go:859` — *"after each committed save"*, i.e. **only as a
  side effect of stepping**.
- **Verification: VERIFIED by source, including the item's own ⚠ nuance.**
  `syncMsgWaiters` rebuilds the map from `st.MessageWaiters()`
  (`processdriver_waiters.go:76`) — so **the snapshot does carry the waiter**; what is
  missing is anything that calls `syncWaiters` at boot. The only boot-time rehydrators
  are the two timer ones (see 66). ⚠ The multi-replica leg follows structurally: the
  map is per-`ProcessDriver`, so a second replica is indistinguishable from a restarted
  one.
  ⚠ **`ASSUMPTION (unverified)`: the executed drop/misroute measurement**
  (`err=<nil>`, payload gone, or a message-start definition consuming it) is inherited
  from the 2026-08-19 run — I did not restart a driver. The mechanism is verified.
  ⚠ **ADR-0155 is on `main` as a DOCUMENT only (banner-marked NOT IMPLEMENTED)** —
  its title is not a closure. Confirmed against the handover's State section.
- **Tier: D.** A durable waiter projection is new persisted state (table + migration in
  three dialects) plus a boot/leader reconciler plus a redelivery contract for the
  broker handler that currently acks unconditionally. ADR — and **ADR-0155 already
  exists as a failed draft**, so the ADR is a *revision*, not a new number.
- **Fix sketch:** call `syncWaiters` from a boot reconciler over non-terminal instances
  (cheapest correct step — the state already has the data), then persist a
  `wrkflw_waiters` projection for the multi-replica case; make the broker handler
  return a non-nil error (nack/redeliver) when no waiter matched instead of `nil`.
- **Falsifiable test note:** **non-vacuous and cheap for the restart leg.** Park an
  instance on a message catch with a durable store, construct a **second**
  `ProcessDriver` over the same store, deliver the correlated message, and assert it
  routes. Today `findMessageWaiter` returns `("", false)` because the new driver's
  `msgWaiters` map is empty — while `st.MessageWaiters()` on the reloaded state
  reports the waiter, which is the contradiction the test pins. ⚠ Docker required.
  ⚠ **Fixture check:** the definition must actually park on a message catch (assert
  `len(st.MessageWaiters()) == 1` after the reload) or the delivery assertion is vacuous.
- **Dependencies:** instance of **66**; interacts with **85** (signal fan-out
  non-idempotency), **63** (the same "process-local vs durable" split for timers) and
  **91** (envelope). Revises ADR-0155.

---

## 68 — the single Go module forces gin, fiber, watermill, redis, memcache and testcontainers on consumers ⚠ DEFERRED

- **Package:** `docs-only` for now (the artifact is an ADR, not code). The concrete
  evidence lives at `persistence/cache/cachetest/containers.go` and `go.mod`.
- **Verification: VERIFIED, both halves.**
  - `persistence/cache/cachetest/containers.go` is a **non-test file** (no `_test.go`
    suffix — the directory holds `conformance.go`, `containers.go` and their `_test`
    siblings) in a **public, importable** package, and it imports
    `github.com/testcontainers/testcontainers-go` and `.../wait` at lines 7-8,
    exporting `RunTestRedis` (`:15`) and `RunTestMemcached`.
  - All six named dependencies are **direct** requires in the single root `go.mod`:
    `ThreeDotsLabs/watermill` (`go.mod:6`), `bradfitz/gomemcache` (`:7`),
    `gin-gonic/gin` (`:10`), `gofiber/fiber/v3` (`:14`), `redis/go-redis/v9` (`:22`),
    `testcontainers/testcontainers-go` (`:28`, plus the mysql/postgres modules at
    `:29-30`).
- **Tier: A — DEFERRED by instruction. No fix is planned here.**
- ⚠ **`CLAUDE.md` locks the layout**: *"One `go.mod` at the repo root"* (Repository
  Layout), and it is stated as a structural property of the product, not a default.
  **Changing it requires an ADR by the project's own rules** — the same clause that
  locks the tech-stack table. This item therefore cannot be actioned as a fix; it can
  only be actioned as a decision.

### What an ADR would have to decide (recorded, not proposed)

1. **Whether to split the module at all**, against CLAUDE.md's single-`go.mod` rule —
   and if not, what the accepted cost is and where it is documented for consumers.
2. **The split boundary**, if any: per-transport (`transport/http/gin`,
   `transport/http/fiber`), per-backend (`persistence/cache/redis`,
   `.../memcache`), or a single `.../contrib` module. Each choice changes the import
   path consumers write, which is itself breaking.
3. **Versioning and release**: N modules means N tags per release and a cross-module
   compatibility matrix; who cuts them and how CI enforces they build together.
4. **`persistence/cache/cachetest`** specifically — the cheapest independent decision:
   is it (a) moved to `internal/`, (b) renamed so its files are `_test.go`-only,
   or (c) split out? ⚠ Option (a)/(c) is **breaking** for any consumer already
   importing it to conformance-test their own cache. This sub-decision is separable
   from 1–3 and is arguably the only part worth doing pre-v0.1.0.
5. **The window**: the item is correct that this is far cheaper before v0.1.0. If the
   answer is "not splitting", say so explicitly in the ADR so it stops being re-raised.
- **Falsifiable test note:** ⚠ **vacuity-risk / not applicable.** A `go build` in a
  scratch consumer module can *demonstrate* the transitive dependency set (`go mod
  graph | grep gin`), but there is no test that fails today.
- **Dependencies:** **92** (generated gomock doubles compiled into the public `service`
  package) is the same "the public surface carries test machinery" class and is
  **contained** (4 mock files) — do 92 regardless of how 68 is decided.

---

## 69 — the general operator escape-hatch contract is missing

- **Package(s):** `runtime` (where the verbs live), `service` + `transport/http/httpcore`
  (exposure).
- **Verification: VERIFIED.** `ProcessDriver`'s exported operator surface is exactly
  five verbs, enumerated from `grep -n "^func (driver \*ProcessDriver) [A-Z]"`:
  `ResolveIncident` (`runtime/processdriver_incident.go:17`), `DeliverMessage`
  (`processdriver_message.go:48`), `ResolveCompensationStall`
  (`processdriver_compensation_stall.go:28` — ADR-0175's fifth, bespoke verb),
  `CancelInstance` (`processdriver_cancel.go:24`), `ReverseInstance`
  (`processdriver_reverse.go:77`). Plus lifecycle (`Start`, `Shutdown`,
  `IsShuttingDown`), the driving entry points (`Drive`, `ApplyTrigger`,
  `BroadcastSignal`) and the two rehydrators. **No general verb exists** — confirmed by
  the same `ReconcileInstance`/`RetryCommand` grep as item 66 (2 hits, both a test
  name). The five uncovered stuck states named by the item map to open backlog items:
  lost in-flight action (**66**), lost waiter (**67**), lost human-task projection
  (**66**), dropped timer fire after CAS exhaustion (**86**), non-leader-armed timer
  (**63**).
- **Tier: D.** It is explicitly a *contract* item: what verbs exist, what they promise,
  what they refuse, and how a consumer discovers which one applies. That is an ADR
  (and it should probably supersede the bespoke-verb pattern ADR-0175 established).
- **Fix sketch:** define one general `ReconcileInstance(ctx, instanceID)` that
  re-derives every post-commit projection from committed state (which is **66**'s fix),
  plus a documented taxonomy of stuck states and which verb addresses each — rather
  than a sixth bespoke verb. ⚠ ADR-0175's lesson applies: a verb whose predicate
  refuses the useful case and admits the harmful one survives a design audit; **this
  one must be executed against a real stuck instance, not reasoned about**.
- **Falsifiable test note:** ⚠ **vacuity-risk as a single test.** "The contract is
  missing" is not falsifiable. Make it falsifiable per state: for each of the five,
  drive an instance into it and assert the escape verb recovers it. Today four of the
  five have **no** verb (so the test does not compile — that is the RED) and the fifth
  (`ResolveCompensationStall`) is scoped to compensation only.
- **Dependencies:** **blocked by 66** (its fix *is* the general verb) and by **59**
  (visibility). Consumes **63**, **67**, **86**, **37**. Partially closed by ADR-0175
  (1 of 6) — the handover's "audit F — partially closed" is accurate.

---

# Pre-v0.1.0 blockers

## Blocker 5 — `TestPgxNotifierListenDrainsBeforePollInterval`

⚠ **The "load-flaky" label is NOT restated here.** Per the handover's own warning
(and ADR-0184's lesson, where backlog 42 carried an identical inherited diagnosis that
was wrong), what follows is *what the test asserts* and *what could make each
assertion fail* — nothing about which one actually does.

- **Package:** `internal/persistence/store` (test file
  `internal/persistence/store/notifier_pgx_test.go`, test at **:51**). The symbol under
  test is `store.NewPgxNotifier` (**:60**) plus `store.NewRelay` (**:65-70**).

### What the test actually asserts (read, line by line)

| # | line(s) | assertion | failure mode |
|---|---|---|---|
| 1 | `:53` | `require.NoError(persistence.Migrate(...))` | infrastructure; fails fast |
| 2 | `:71` | `require.NoError` on `store.NewRelay` | construction; fails fast |
| 3 | **`:76-81`** | `select { case <-listenReady: case <-time.After(5*time.Second): t.Fatal("pgxNotifier: LISTEN not established within 5s") }` | **a 5 s timing bound** — a *distinct* one, before the drain is even attempted |
| 4 | `:84-93` | `require.NoError` on `store.New` and `st.Create` | infrastructure; fails fast |
| 5 | **`:96-98`** | `require.Eventually(func() bool { return pub.n.Load() == 1 }, 5*time.Second, 25*time.Millisecond, "relay must drain via NOTIFY wakeup, not poll (poll interval = 30s)")` | **the second 5 s timing bound** — the one the "load-flaky" label presumably means |
| 6 | **`:104`** | `goleak.VerifyNone(t, opt)`, called **immediately** after `cancel()` with no sleep, against a baseline `goleak.IgnoreCurrent()` taken at `:57` | **not a 5 s bound at all** — goleak v1.3.0 retries for roughly a second, then fails with a **goroutine dump and no assertion message** |

The design is deliberate and worth preserving: `WithRelayPollInterval(30*time.Second)`
(`:66`) means polling **cannot** drain inside the 5 s window, so a pass proves NOTIFY
woke the relay. That is what makes assertion 5 non-vacuous — and it is also why the
budget cannot simply be raised past 30 s without destroying the test's meaning.

⚠ **Three independent failure modes, and they look nothing alike.** #3 fails with
`pgxNotifier: LISTEN not established within 5s`; #5 fails with
`relay must drain via NOTIFY wakeup, not poll`; #6 fails with a goroutine stack dump.
Any report that says only "it's flaky" has not distinguished them. The test does **not**
call `t.Parallel()` (the two `t.Parallel()` hits in the file, `:487` and `:522`, belong
to `TestRelayRun_MySQL_SQLite_StillPoll`'s subtests), so contention arrives from the
repo-wide `go test ./...` running packages concurrently — not from within the package.

- **Verification: the test's CONTENT is VERIFIED (read in full).
  Its RUNTIME BEHAVIOUR is `ASSUMPTION (unverified)`** — it requires Postgres via
  `dbtest.RunTestDatabase` (`:52`) and therefore Docker, which this triage has no
  permission to start.

### The probe that would settle it

Run the failure, not the test — and capture the **duration** and the **text**:

```bash
# terminal A — manufacture contention
go test -count=1 ./... > /tmp/load.log 2>&1 &

# terminal B — run the suspect under -v so you can prove it RAN
go test -race -count=20 -v \
  -run '^TestPgxNotifierListenDrainsBeforePollInterval$' \
  ./internal/persistence/store/ > /tmp/b5.log 2>&1
echo "EXIT=$?"
grep -n -- '--- \(FAIL\|PASS\)' /tmp/b5.log
```

Then read the `--- FAIL: … (X.XXs)` duration and classify:

- **≈ 0.00 s** → not a timing bound at all; something failed before any wait. This is
  the backlog-42 shape and would refute the label outright.
- **≈ 5.0 s** → one of the two 5 s bounds; the message text says which (#3 vs #5).
- **≈ 1 s with a goroutine dump and no assertion message** → **#6, goleak** — a
  shutdown-ordering defect, not a wait-budget one, and raising any budget would be
  the wrong fix.
- **Green 20/20 in isolation** → proves nothing (that is exactly the mistake ADR-0184's
  audit made). Only the contended run counts.

⚠ Per the CLAUDE.md pitfall: `-run` on a name that does not exist exits 0. The `-v`
flag above is what proves the test ran; do not infer it from the exit code.

- **Tier: A (adjudication / research) until the probe runs.** It cannot be tiered
  honestly before then: #3/#5 would be `S` (a budget, or a `listenReady` handshake);
  #6 would be `S`-to-`D` (Relay shutdown ordering — `Run` already `defer wg.Wait()`s,
  so a leak there is a real defect in production code); a 0.00 s failure would be a
  production race and `D`. **Do not design a fix first.**
- **Fix sketch:** none — deliberately. The handover says *"do not silence it"*; the
  corollary is *do not tune it either* until the failing assertion is named.
- **Falsifiable test note:** the test is already non-vacuous *by construction* — the
  30 s poll interval is the fixture that makes assertion #5 unable to pass by polling.
  ⚠ What is **not** established is which assertion fails, and no amount of reading
  can establish it.
- **Dependencies:** **blocks 45** (whose whole premise is that this is the same class
  as the `runtime/` `Eventually` sites — unverifiable until this is diagnosed).
  Interacts with **blocker 7** (the handover says so: a faster suite changes the
  contention profile, so fixing 7 may mask or expose this) and with **57**/**81**
  (same `Relay` symbol).

---

## Blocker 7 — suite speed: `internal/dbtest`'s per-package `sync.Once` boot

- **Package(s):** `internal/dbtest` (the helper), plus CI/scripts
  (`.github/workflows/ci.yml`, `scripts/`).
- **Verification: VERIFIED, and the counts are EXACT.**
  - The two `sync.Once`s: `internal/dbtest/postgres.go:87` (`sharedOnce sync.Once`)
    and `internal/dbtest/mysql.go:31` (`mysqlSharedOnce sync.Once`). A package-level
    `sync.Once` fires **once per test binary**, and Go builds one binary per package —
    hence one boot per package, which is the whole defect.
  - **12 Postgres packages** (hand-counted from `grep -rl "RunTestDatabase\|RunTestPostgres"`,
    deduplicated by directory): `casbinauthz`, `eventing`, `internal/authz/casbin`,
    `internal/database`, `internal/database/transaction`, `internal/dbtest`,
    `internal/persistence/store`, `internal/transporttest`, `persistence`, `runtime`,
    `scheduler`, `scheduler/internal/gocron/pgelector`. **= 12.** ✅ matches.
  - **7 MySQL packages** (`grep -rl "RunTestMySQL"`): `internal/database`,
    `internal/dbtest`, `internal/persistence/store`, `persistence`, `runtime`,
    `scheduler`, `scheduler/internal/gocron/myelector`. **= 7.** ✅ matches.
  - **`WRKFLW_TEST_POSTGRES_DSN` / `WRKFLW_TEST_MYSQL_DSN`: 0 hits repo-wide.** The
    env-var path does not exist yet.
  - **`scripts/testdb.sh` does not exist** — `scripts/` contains exactly
    `check-extraction.sh` and `coverage.sh`.
- **Tier: S.** ⚠ *Borderline* — it is test infrastructure with no public API and no
  behavioural contract, and the shape is already fully specified by the handover
  (honour the DSN env vars, fall back to testcontainers, add `scripts/testdb.sh
  up|down`, wire CI). It exceeds ~100 lines mainly in the script and CI YAML, which do
  not count against the tier's intent. **Tier it `S`, but note it is the largest `S`
  in this slice.**
- **Fix sketch:** in `internal/dbtest`, have `RunTestDatabase`/`RunTestMySQL` check
  `WRKFLW_TEST_POSTGRES_DSN`/`WRKFLW_TEST_MYSQL_DSN` first and connect directly
  (per-test schema/database isolation, since the container is now shared across
  binaries), falling back to the existing `sync.Once` testcontainers boot; add
  `scripts/testdb.sh up|down` to manage the shared containers; export the DSNs in
  `.github/workflows/ci.yml`.
- **Falsifiable test note:** **non-vacuous.** A test in `internal/dbtest` that sets
  `WRKFLW_TEST_POSTGRES_DSN` to a live DSN and asserts `RunTestDatabase` returns a pool
  **without** starting a container fails today — the env var is read nowhere (0 hits),
  so the container path always runs. ⚠ Assert on "no container started" via a
  testcontainers hook or an elapsed-time bound, **not** on a log line — and ⚠ a
  wall-clock bound is a claim about the mode it was measured in (`-race` is ~8× slower),
  so state the mode.
  ⚠ **The headline benefit (suite wall-time) is `ASSUMPTION (unverified)` here** — I did
  not time the suite. Measure before and after, in the same mode, and record both.
- **Dependencies:** **48** (both change how CI invokes `go test`; do them in one
  bundle or the workflow file is edited twice), **blocker 5** (changes the contention
  profile that item is diagnosed under — ⚠ **diagnose 5 BEFORE fixing 7**, or the
  evidence moves).

---

## Blocker 8 — the `forceTerminate` → `endInstance` boundary sweep is "entirely uncovered"

- **Package:** `engine`. Symbols: `forceTerminate` (`engine/step_nodes.go:616-634`),
  `InstanceState.endInstance` (`engine/state.go:590-637`),
  `cancelAllScheduledWork` (`engine/state_arms.go:188-192`) →
  `cancelAllTimers` (`engine/state_timers.go:126`) +
  `cancelAllArmsAndBoundaries` (`engine/state_arms.go:140-155`) +
  `removeAllEventTriggeredSubprocessArms` (`engine/state_arms.go:564`).
- **Verification: ⚠ CONTRADICTED AS STATED, and corrected by EXECUTION.**

  Ran (container-free, `engine` only):
  `go test -count=1 -coverprofile=… ./engine/...` → **EXIT=0, coverage 93.0 %**, then
  `go tool cover -func`:

  | symbol | coverage |
  |---|---|
  | `forceTerminate` (`step_nodes.go:616`) | **90.0 %** |
  | `endInstance` (`state.go:590`) | **100.0 %** |
  | `cancelAllScheduledWork` (`state_arms.go:188`) | **100.0 %** |
  | `cancelAllTimers` (`state_timers.go:126`) | **100.0 %** |
  | `removeAllEventTriggeredSubprocessArms` (`state_arms.go:564`) | **100.0 %** |
  | `cancelAllArmsAndBoundaries` (`state_arms.go:140`) | **80.0 %** |

  **"Entirely uncovered" is false.** The real gap is two statements, identified from
  the raw profile (`grep ' 0$'`):

  - **`engine/state_arms.go:142.35,143.23` and `143.23,145.4` — count 0.**
    That is the **`ArmedEvents`** loop:
    ```go
    for _, ae := range s.ArmedEvents {
        if ae.TimerID != "" {
            cmds = append(cmds, CancelTimer{TimerID: ae.TimerID})
        }
    }
    ```
    ⚠ **The `Boundaries` loop immediately below it (`:148-152`) IS covered.** So the
    blocker's own name is inverted: the *boundary* half is exercised; the
    **event-gateway arm half is what no test reaches** — nothing in `./engine/...`
    ever calls `cancelAllArmsAndBoundaries` with a non-empty `s.ArmedEvents`.
  - **`engine/step_nodes.go:628.19,630.4` — count 0.** That is `forceTerminate`'s
    default-reason branch: `if reason == "" { reason = "force-terminated" }`. No test
    force-terminates without an explicit `TerminationReason`.

  ⚠ **Caveat on the measurement:** this is `./engine/...`'s **own** suite. Tests in
  `runtime`, `processtest` and `service` also drive engine code but do not contribute
  to this profile. The settling probe is
  `go test -count=1 -coverpkg=./engine/... -coverprofile=all.out ./...` (⚠ **Docker
  required** — that sweep includes testcontainers packages). Until that runs, the
  precise claim is: *unreached by the `engine` package's own tests.*
- **Tier: S.** Two tests in one package, no production change. ⚠ **Rewrite the
  blocker's statement first** — a fix aimed at "the boundary sweep" would add a test
  for the half that is already covered.
- **Fix sketch:** add an `engine` test that parks an **event-based gateway** with a
  timer arm (so `s.ArmedEvents` is non-empty and carries a `TimerID`) and then drives a
  force-termination end event, asserting the emitted `[]Command` contains a
  `CancelTimer` for that arm's `TimerID`; add a second case force-terminating with an
  empty `TerminationReason` and asserting the instance ends `terminated` with
  `"force-terminated"`.
- **Falsifiable test note:** **non-vacuous, with an explicit fixture requirement.**
  What makes it fail today: those two statements have execution count **0**, so the
  mutation is direct — delete the `ArmedEvents` loop body and the new test must go
  RED while the existing suite stays green (proving the existing suite does not cover
  it). ⚠⚠ **Fixture check, and it is the whole risk:** the definition MUST declare an
  event-based gateway with a **timer** arm. A fixture whose arms carry no `TimerID`
  makes the inner `if` false and the assertion vacuous — precisely the
  `assert.Empty(state.Boundaries)`-with-no-boundary-node trap CLAUDE.md names.
  Assert `len(st.ArmedEvents) > 0` **and** a non-empty `TimerID` **before** the
  force-termination, or the test proves nothing.
- **Dependencies:** same package as **55**, **56**, **61**, **70-74** — `engine` work
  is **strictly serial** (rule #11). Touches the code `cancelTokenWaits`/ADR-0164
  reason about; read backlog **12** (a `PendingCancel` surviving onto a `Running`
  instance) before changing `forceTerminate` itself. This item as scoped changes **no
  production code**, so the conflict risk is low.

---

## Cross-cutting notes for the next planner

1. **Counts corrected by this sweep** (Premise Discipline — re-derive, do not inherit):
   - **59**: `driverObs` holds **12** instruments, not 13 (`runtime/observability.go:17-32`).
   - **51**: the body-actor contract is pinned at **21 test sites across 9 files in 5
     packages**, not the 2 lines the item names; and **zero `examples/` callers** break.
   - **61**: **zero** Go code outside `engine/` names the five unexported types — every
     out-of-package hit is a comment — so exporting them is **not** a consumer break.
   - **Blocker 8**: not "entirely uncovered"; the uncovered statements are the
     **`ArmedEvents`** loop (`state_arms.go:142-145`) and `forceTerminate`'s empty-reason
     default (`step_nodes.go:628-630`). The **boundary** half is covered.
   - **62**: `ListDefinitions` **does** exist (`runtime/kernel/definition_lister.go:20`);
     it is unexposed above the kernel, which is a different sentence.
   - **47**: the doc comment no longer contradicts the behaviour — it was corrected
     in ADR-0184's own bundle and cites backlog 47 by number.
   - **64**: panic recovery (`runtime/processdriver_action.go:156`) and an action
     timeout (`runtime/processdriver_options.go:49-59`) **do** ship; the gap is the
     written contract and the key's scope.
   - **Blocker 7**: 12 Postgres + 7 MySQL packages — ✅ both counts confirmed exactly.
   - **58**: `notifier` appears exactly **3×**, all comments — ✅ confirmed.
   - **44**: exactly **16** `Never` sites with the stated budget distribution — ✅ confirmed.

2. **The authorization chain 51 + 52 + 53 is one delivery, not three.** Each alone
   leaves the path open. 53 is the same defect as blocker 1's tail and as the `NEXT
   WORK` bullet — collapse the three prose entries into one when the ADR lands.

3. **`engine` items run strictly serial** (55, 56, 61, blocker 8, and 70–74 outside
   this slice). Fan-out is only safe across `scheduler` / `transport` / `persistence` /
   `processtest`.

4. **Interface-breaking changes to bundle together** (each breaks third-party
   implementers exactly once): `service.DurableProvider` (**52**) and
   `humantask.TaskStore` (**62**). Doing them separately breaks consumers twice.

5. **Docker is required** for the falsifying tests of **57**, **59** (second half),
   **63**, **67**, **blocker 7**, and for blocker 8's settling `-coverpkg` sweep. A
   subagent brief must say so explicitly — the standing permission covers the
   controller's Verification runs only.
