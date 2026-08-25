# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack and was silently abandoned for 45 ADRs —
> see `docs/plans/HANDOVER-archive.md`. Per-delivery detail belongs in that delivery's plan under
> a `▶ Progress` block. This file carries only: where `main` is, what is unmerged, and what next.

## State — updated 2026-08-25 (**MERGED and PUSHED; nothing in flight; next is backlog 51**)

**`main` is PUSHED and clean at merge `b5fe7272`.** ⚠ Re-derive the head
(`git rev-parse --short refs/heads/main`); anchor on **merge** SHAs, which never move:
**`b5fe7272` (latest shipped — YAML boundary options, at-rest count, clone guard)**,
ADR-0187 `4e2c0af4`, ADR-0186 `13b3bfb0`, the backlog sweep `020af37b`, 0184 `be6e6b55`,
0183 `a7575ed5`, 0179 `962aeb25`, 0181/0182 `1ac140f6`, 0177/0178/0180 `a5b33e4c`, 0176 `52bf0f80`,
0175 `6e4addc8`.

**▶ NOTHING IS IN FLIGHT.** Branch `design/authz-identity-core` merged `--no-ff` and deleted;
worktrees clean. **▶▶ NEXT: backlog 51** — the section below headed *NEXT WORK* carries everything
needed to start it.

### ✅🚚 What `b5fe7272` shipped

**Code — four real defects**, all found while designing and then *rejecting* ADR-0188:
- **143** ⭐ the only user-facing one — `boundary_action` / `boundary_error_expr` are now authorable
  in YAML. Both were public Go options (`event.WithBoundaryAction`, `WithBoundaryErrorExpr`) with
  wire support and a dedicated example, but `nodeYAML` declared neither, so a YAML author could
  attach a boundary and never give it an action or an error predicate. ⚠ Field **and** mapping
  landed together: the field alone is a **net regression** under `KnownFields(true)`.
- **141** — `wrkflw_instances.snapshot` added to `atrest.PolicyAtRestLocations`; `SECURITY.md`'s
  policy-at-rest count 3 → 4. ADR-0187's completeness guard could not see it (the column is
  `ClassFreeform`). Also fixed a **hardcoded** count pin in `render_test.go`.
- **`scripts/gen-at-rest.sh`** — verified ONE test, so it could print *"regenerated and verified"*
  over a **red package**; now requires the package green, via `mktemp` + `trap`.
- **`humantask.Clone`** — its comment claimed a safety property it did not have; a reflective,
  value-based guard now makes the claim true for every reference field.

**Documents — two bundles that did NOT survive their audits**, retained banner-marked:
**ADR-0185-core** (failed, parked) and **ADR-0188** (audited, then **Rejected**, three grounds
recorded). With them: four lens reports + an adjudication for each, and
**`docs/plans/sweep-evidence/meta-analysis-audit-finding-rate.md`**.

**Gates:** `go test -race ./...` EXIT=0 zero failures · `golangci-lint run ./...` 0 issues ·
touched packages 95.1 % / 92.6 % / 100 % · **`/code-review high` 8 findings (4 MEDIUM, 4 LOW), all
reproduced, NONE a false positive, all fixed and folded** · **`/security-review` 0 findings**.

### ⭐⭐⭐ READ THIS BEFORE READING ANY AUDIT RESULT — the count is an INSTRUMENT READING

Full analysis: **`docs/plans/sweep-evidence/meta-analysis-audit-finding-rate.md`** (193 accepted
findings classified across 10 rounds; arithmetic controller-verified).

**Seven four-lens rounds spanning a 12× swing in artifact size returned 15.14 ± 0.83 findings per
lens — CV 5.5 %, range 14.00–16.25.** Findings correlate with **lens count r = 0.855** and with
scope **not at all**. `reaudit-0187` is the natural experiment: 2 lenses → 34 findings = 17.0/lens,
*above* average.

⇒ **"~58 findings again" NEVER meant "no progress". It meant "four agents were pointed at it
again".** ⛔ **Never report a raw total as a quality signal. Report Criticals per lens** — that
number moved (**8.25 → 3.50**). Noise is NOT the explanation: cosmetic findings are **8.3 %** and
**65 of 67 raw Criticals corpus-wide were accepted (97 %)**. The findings are real; they are
inexhaustible.
⚠ ADR-0188's own audit came in at **11.0 findings / 3.75 Criticals per lens** — the first round
*below* the 15.14 ± 0.83 band. One round does not refute the model; do not over-read it either.

**Root causes over 193 accepted findings: ~82 % design-process, ~10 % architectural, ~8 % cosmetic.**
⚠ The architectural share is **ONE LINEAGE**: ADR-0186 **4.6 %** · ADR-0187 **6.3 %** ·
**ADR-0185 authz-identity 22.6 % → 35.3 %** — all tracing to eligibility having several
unreconciled representations.
⚠⚠ **The largest recurring PROCESS cause was diagnosed and never adopted.** Bucket D (an enumeration
built with the **wrong grep net**) is **25.4 % of accepted findings, non-zero in ALL TEN rounds**;
`reaudit-0186` prescribed the fix — **derive enumerations with `go/parser` tooling, not prose** —
and nobody implemented it. ⚠ Corrected denominator: 25.4 % is of the **193 accepted**, not of the
554 raw (that share is 8.8 %).

### ⚠⚠ THE REPRESENTATION TRAP REMAINS — ADR-0185 D3's plan must carry this warning

ADR-0188 would have guarded it and was rejected (zero user-facing value; and execution proved its
guards checked field **names** where the defect is field **copies**). So eligibility is still
declared in **5 types** and copied by hand at **5 sites**, cited by SYMBOL because line numbers rot:
`fromNodeYAML` (`definition/model/yaml.go`), `FromWire` and `ToWire`
(`definition/activity/activity.go`), `userTaskStrategy.enter` (`engine/step_nodes.go`), and
`HumanTask.Clone` (`humantask/humantask.go`). **A miss at `yaml.go`'s mapping or at the mint site is
SILENT**, and the mint-site miss is **fail-open**. Reviving ADR-0188's two *working* guards
(`nodeYAML` coverage and the eligibility correspondence) is cheap and available if it bites again.

## What the sweep actually established

The **triage evidence is now in-repo** at `docs/plans/sweep-evidence/` — four `triage-*.md` files
covering all 119 items with package attribution, tier, fix sketch, a falsifiable-test note and a
verification status, plus six `fix-*.md` files with the observed RED text for every change. **Read the
triage file for an item before acting on it.** It is more reliable than the one-line statements below
and than `AUDIT.md`.

### ⚠⚠ Backlog statements refuted by execution — do NOT act on the old wording

- **Blocker 8** — "the `forceTerminate` → `endInstance` boundary sweep is entirely uncovered" is
  **FALSE**. `endInstance` and `cancelAllScheduledWork` were already **100 %**. The real gaps were the
  `ArmedEvents` loop and `forceTerminate`'s empty-reason default. **Both now closed.**
- **Backlog 20** — `service` "53.9 %" is the **raw** figure; filtered it is **93.5 %**. Not a defect.
  (Both numbers are real and measure different things — `go test`'s per-package number includes the
  generated mocks; `scripts/coverage.sh` excludes them.)
- **Backlog 18** — both bounds were closed at ADR-0171's own delivery gate. **Not open.**
- **Backlog 8** — ADR-0174 rejected it with a measurement. **Not fixable as filed.**
- **Backlog 97** — the audit's own proposed fix had already shipped.
- **Backlog 10** — the filed harm **does not exist**: `drive()` runs `defForScope` before any strategy
  and hard-errors on an unknown scope, so the branch is unreachable. The prescribed WARN was written,
  *proved unreachable, and deleted* rather than shipped as an untestable branch. ⚠ It has **four**
  callers, not three, and the omitted one (`cursorRecords`) is the one **not** gated — see item 133.
- **Backlog 26** — the "404 ms, not a hang" reading was **wrong**: that was one point on a straight
  line. `Monthly(1044480,{31})` measures **3.568 s plain / 27.159 s `-race`**. Fixed properly with an
  arithmetic grid jump.
- **Backlog 122** — its own correction was false. `DrainOnce` **does** have per-row backoff
  (`relay.go:296`). Neither the original comment nor the "correction" was restated.
- **Backlog 61** — **not a consumer break**: zero Go code outside `engine/` names those five types.
- **Backlog 62** — `kernel.DefinitionLister.ListDefinitions` **does** exist; it is merely unexposed.
- **Backlog 99** — the audit's `MaxNodes` fix is **INVERTED** and would not have stopped the stall it
  is named for (that expression is 11 AST nodes). Do not apply it.

### ⚠⚠ Enumerations that rotted — every count below was re-derived

| item | filed | actual |
|---|---|---|
| 31 dangling ADR citations | 3 | **14 repo-wide** (2 in `engine/`, 2 in `scheduler/` fixed; ~10 unowned) |
| 118 unmapped conflict sites | 8 in `Commit` | **6**, plus 2 more, plus a **5th** branch (`Create`'s `maybeNotify`) nobody listed; `Commit` has **10** error returns |
| 117 godoc examples missing `busy_timeout` | 1 | **11** |
| 9 terminal sites | 8 | **10** — now **machine-checked**, so it cannot rot again |
| 51 tests pinning old authz behaviour | 2 lines | **23 pins / 9 files / 5 packages** — ⚠ the triage TABLE summed to 23 while its own summary sentence said 21 (a recap failure); and **"zero `examples/`" is FALSE** — `production_wiring`, `sqlite_wiring` and `mysql_wiring` all mount `TaskRoutes` via `stdlib.Mount` |
| 59 driver instruments | 13 | **12** |
| 116 exported symbols leaking `internal/` | 2 | **3** — see item 128 |
| 44 vacuous `Never` sites | 16 | **7** (5 already had preconditions, 3 hung) |
| node kinds (README/memory) | "19" / "18 with impls" | **18 constants = 17 authorable**, **16** with strategies; `KindBoundaryEvent` intentionally unregistered |

## ▶ NEXT WORK — **backlog 51: the actor must not be self-asserted**

### The defect, in one paragraph

`transport/http/httpcore` builds `authz.Actor` **from the request body** at
`endpoints.go:119,132,150` — the only three `authz.Actor{…}` constructions in `transport/`,
non-test. Any caller can post `{"actor":{"id":"alice","roles":["manager"]}}` and be believed.
`CustomizeConfig` declares no identity seam, so a consumer's authentication middleware has **no
supported way** to override it. `Actor.Attributes` is dropped at all three sites, so attribute
predicates over actor attributes cannot be satisfied over HTTP at all.

**This is the most directly exploitable open item in the repo.** It is D1 of the parked ADR-0185.

### ⚠⚠ START HERE, AND MIND THE PROVENANCE

The design below comes from **`docs/adr/0185-authorization-identity-is-not-self-asserted.md`, a
bundle that FAILED its rule-#9 audit three times**. ⛔ **Do NOT inherit D2 (52) or D3 (53)** — both
were refuted. **D1 survived nearly intact** (one Critical, named below), and the facts here were
**re-derived or confirmed by that audit**, not merely restated. Adjudication:
`docs/plans/sweep-evidence/audit-0185core-adjudication.md`. Read it before writing anything.

### Facts CONFIRMED by execution during the ADR-0185 audit — reuse, but re-verify anchors

- **29 pin sites / 9 files / 5 packages** assert the body-derived contract — httpcore 11 (5 in
  `dto_test.go`, 6 literals in `endpoints_test.go`), gin 7, fiber 5, stdlib 5, parity 1. The
  counting lens attacked this and found it **exact**. The net is closed **by construction**:
  `httpcore/dto.go` declares exactly three Actor-bearing fields (`ClaimInput.Actor`,
  `CompleteInput.Actor`, `ReassignInput.By`). ⚠ One occurrence is deliberately excluded —
  `validate_test.go`'s `httpcore.Validate(httpcore.ClaimInput{})` survives field removal.
- ⚠⚠ **CORRECTION the audit forced:** the two pins that assert a **vacuous 403** are **BOTH in
  `stdlib`** (`errors_test.go:158` and `:187`). `gin/gin_coverage_test.go:244` asserts **404**, not
  403, and **gin has no 403 assertion at all**. The ADR's earlier "one stdlib, one gin" was wrong.
  Those two must be **rewritten**, not recompiled — after D1 they would still return 403 *from the
  zero actor*, passing while testing nothing.
- **All three adapters tolerate unknown body keys**, so *"a body still carrying `actor`/`by` is
  IGNORED, not rejected"* is **correct** — ADR-0167's strictness does not reach the DTO decode path.
  This was an open question in the failed plan; the execution lens answered it.
- **fiber propagates via `c.SetContext`, NOT `c.Locals`** — verified. A consumer following fiber's
  most idiomatic path gets a **silently unauthenticated** request. `SECURITY.md` and the examples
  must show `SetContext`.
- **`WithActorResolver` is already taken THREE times** for the opposite concept
  (`service/options.go:99`, `runtime/task/service.go:113`, `processtest/harness.go:104` — "who
  *could* act"). Use **`httpcore.WithRequestActor`**.
- **ADR-0186's option-alias convention already exists and must be reused**: the generic
  `httpcore.With…[R any]` form **cannot infer `R`**, so each adapter carries a non-generic alias
  (`stdlib`/`gin`/`fiber` `options.go`, see `WithMaxBodyBytes`). ⇒ **2 new options × 3 adapters = 6
  aliases.** ⚠ Read each adapter's `options.go` first; they do not all carry the same set today.
- **Three `examples/` mains** mount task routes via `stdlib.Mount` with **no authentication**:
  `production_wiring`, `sqlite_wiring`, `mysql_wiring`.

### ⚠ The one Critical against D1, which MUST be designed out

**`WithAnonymousActorAllowed()` and the empty-`Actor.ID` rejection void each other** — the three
demo mains would be unable to claim. The anonymous opt-in must synthesize a **non-empty sentinel
identity**, and the ADR must say which.

### ⚠ Three more corrections D1 owes

1. **Delete** *"`Actor.Attributes` reaches the authorizer — closing finding 4's second leg for
   free."* It was refuted (`actor` is a struct; `Attributes` always exists at depth-1) **and** its
   referent (D4) is deferred.
2. ⚠ **D1 makes backlog 103 MORE reachable, and 103 is deferred.** Today all three endpoints drop
   `Attributes`, so `actor.Attributes.*` predicates fail closed *vacuously*; once the actor arrives
   whole they go live with nothing bounding them. This belongs in **Consequences/Negative** as a
   cost of shipping D1 alone, not in a follow-ups list.
3. **Re-derive the empty-`Actor.ID` rule's rationale.** It was justified by the deferred D5's
   `"" == ""` degeneracy. It survives on independent grounds (the audit trail must not record `""`;
   a caller past the 401 has an ID) — but write *those*, or it is a dangling citation.

### ⚠ A residual D1 may NOT claim away

`ProcessDriver.ApplyTrigger` **bypasses authorization by design** and says so in its own godoc;
`engine.NewHumanCompleted` is likewise exported module-root API. A D1 delivery covers the four
`runtime/task` verbs. **Do not claim the chain is closed.**

### Then, in order

**(2) backlog 52** — ⚠ needs a design increment first: *"when human tasks are configured"* **is not a
state that exists** (a bare `service.NewProcessEngine()` already serves human tasks on a defaulted
`MemTaskStore` + `AllowAll`). **(3) 53** — ⚠ re-derive its migration from scratch; the three durable
copies have **different shapes**, the prescribed SQL corrupts the definitions copy, and
`TestMigrations_OneFilePerDialect` forbids a `0002` file. **(4)** the deferred B3 slices — §4XX
(104), §READ-PATH (54), §SSRF (65), §BOUND (99). **(5) B4–B7. (6) blocker 5.**
⚠ **Backlog 103 and 124 need their own ADRs** — they left ADR-0185 with their designs **refuted**.
⚠ **144 is open and deliberately deferred**: YAML cannot author the nested trigger forms
(`TriggerWire` has json tags only across 11 fields incl. a custom `schedule.ClockTime`), so it is a
serialization-contract change, not a bug fix.

### ⭐ How to run the audit — this dispatch works, reuse it verbatim

Four Opus lenses (**execution / failure-modes / counting / interaction**), **detached worktrees at
the bundle commit**, a **step-0 bundle-presence check stated explicitly in every brief**, and
**"append findings per finding, before the next probe"** (a mid-run kill cost three lenses their
work once; the second time 2,418 of 3,717 lines were already on disk).
⚠ Brief the **counting** lens that the failure mode is **the net, the anchor, and the SCOPE — not
the arithmetic**: every sum across seven rounds was right, and that lens found the decisive Critical
in six consecutive bundles.
⚠ Brief the **interaction** lens with the **explicit list of what changed**; its question is *"what
does this decision assume someone else will hand it, and who agreed to that?"*
⚠⚠ **A REMOVAL is a change and generates its own grid** — when you cut N decisions out, derive the
survivor×removed pairs explicitly; it is not smaller than the grid you deleted.
⚠ **The evidence file is an INPUT to the audit, not a conclusion of it** — attack it too. Findings
landed inside the bundle's own evidence file in two separate rounds, and in one the author's own
probe refuted a real audit finding and was itself wrong.

- **B4 durability/reconciliation** — 66 (the class), 67, 24, 29, 37, 39, 57, 63, 76, 77, 81.
- **B5 engine core** — 55, 56, 70, 71, 72, **73** (⚠ its guard, 114, is now shipped — do not delete the
  `cloneState` deep-copy without reading the comment now on that line), 11, 12, 13, 17.
- **B6 public API, window-limited (cheap only before v0.1.0)** — 61, 62, 92, **128**, **130**, 88, 91,
  32. ⚠ **68 (multi-module split) is DEFERRED by owner decision**; `CLAUDE.md` locks one `go.mod` at
  the root, so changing it needs its own ADR.
- **B7 observability** — 59, 60, 106, 110, 111, 112-adjacent, plus **108** (now unblocked: 116 shipped).

### Open decisions that are the owner's, not an agent's

1. **Item 120 — `samber/do`** is locked in `STABILITY.md` **and `CLAUDE.md`'s tech-stack table**, absent
   from `go.mod`, imported by zero files. Adopt it, or strike it from both. Untouched deliberately.
2. **Should casbin's `Enforce` fail CLOSED on stale policy?** Today a revoked permission still returns
   `true, err=nil` after a failed reload. The sweep shipped the ERROR log + failure counter but did
   **not** change the failure mode — it is an availability/security trade-off. Pairs with item 106.
3. **Item 109 — should `OpenSQLite` REJECT an unsafe pool, or only warn?** Warn shipped; reject is
   breaking and is recorded as an open question in ADR-0082 §2.

## Backlog

⚠ **Detail lives in `docs/plans/sweep-evidence/triage-*.md`.** These lines are labels, not statements.

**Closed by the sweep (merged as `020af37b`):** 3b, 3d, 3e, 9, 10, 15, 26, 28, 30, 31 (engine+scheduler
halves), 34, 38, 40, 44, 48, 49, 58, 74, 75, 84, 87, 89, 95, 102, 107, 112, 113, 114, 115, 116, 117,
118, 119, 121, 122, 123, 125, 127, blockers 7 and 8.
**Closed by ADR-0186 (merged as `13b3bfb0`):** **98** (no request body cap).
**Closed by `b5fe7272`:** **143** (boundary options unreachable from YAML — the user-facing one) and **141** (`SECURITY.md` understated the policy-at-rest locations), plus the `humantask.Clone` false-safety comment and `gen-at-rest.sh`'s one-test verification.
**Rejected, not built:** **ADR-0188** (machine-checked reconciliation) — see its ADR for the three grounds. ⚠ The trap it would have guarded is still open; see the State section.
**Adjudicated, not defects:** 8, 18, 20, 97, 126, 3f, 6, 35, 36, 37, 45, 46.

**Still open — Design tier:** 4, 5, 7, 11, 12, 13, 17, 19/41, 24, 27, 29, 32, 33, 39, 47, 50, 51, 52,
53, 54, 55, 56, 57, 59, 60, 61, 62, 63, 64, 65, 66, 67, 69, 70, 71, 72, 73, 76, 77, 78, 79, 80, 81,
82, 83, 85, 86, 88, 90, 91, 92, 93, 94, 96, 99, 100, 101, 103, 104, 105, 106, 109 (reject leg),
110, 111, 124. **68 deferred.**

⭐ **90 (silent claim theft)** remains the sharpest small-ish item and is Design tier only because it
needs a new `ErrTaskAlreadyClaimed` sentinel: any eligible actor takes a task another holds, `err=nil`,
bypassing the guard `Reassign` has twelve lines below it.

**🆕 New defects found during the sweep, in no prior backlog:**

- **140** — 🆕 **found by ADR-0187's round-2 audit, PRE-EXISTING and unrelated to that delivery.**
  MySQL's `-- +goose Down` drops **8** tables where Postgres and SQLite drop **9**: `wrkflw_outbox`
  is created and **never dropped**, so a MySQL rollback leaves the table behind. Verified:
  `grep -c "^DROP TABLE"` → postgres 9, mysql 8, sqlite 9. Small.
- **143** — ✅ **CLOSED by `b5fe7272`.** Found while deriving ADR-0188's YAML exception list; PRE-EXISTING.
  **`event.WithBoundaryAction` and `event.WithBoundaryErrorExpr` are unreachable from YAML.**
  `nodeYAML` carries `AttachedTo` (`yaml.go:63`, mapped `:141`) so a boundary event **can** be
  declared in YAML, but `BoundaryAction`/`BoundaryErrorExpr` are absent from `nodeYAML` entirely —
  `grep -in boundary definition/model/yaml.go` returns **nothing**. Both are fully supported
  elsewhere: public options (`definition/event/options.go:268,283`), wire mapping
  (`event/event.go:388-389`, `:398`) and a dedicated example (`examples/scenarios/boundary_action/`).
  ⇒ a YAML author can attach a boundary and cannot give it an action or an error predicate. **Not a
  deliberate scope limit.** ADR-0188 declares and guards it; it does **not** fix it. Small.
- **144** — ⬜ **OPEN, deliberately deferred** (serialization-contract change, not a bug fix). Same origin. YAML cannot author the **canonical nested trigger forms**
  (`TimerTrigger`, `WaitTrigger`, `DeadlineTrigger`); `nodeYAML` carries only the legacy flat
  `TimerDuration`/`WaitEvery`/`DeadlineDuration`, which `NodeWire.Wait()`/`ReadTrigger`
  (`node_wire.go:119`) decode. ⇒ **reduced expressiveness, not absence** — a YAML author gets
  durations but not the calendar/timezone triggers of ADR-0136/0137. Lower severity than 143
  because a path exists. Small.
- **141** — ✅ **CLOSED by `b5fe7272`.** Found while designing ADR-0185-core; PRE-EXISTING and unrelated to it.
  `wrkflw_instances.snapshot` carries the **full `AuthzSpec`** (`InstanceState.Tasks[].Eligibility`
  → `store_core.go:81` `json.Marshal`) — **executed**, spec §2.1 — but is **absent from
  `atrest.PolicyAtRestLocations`**, so `SECURITY.md`'s published "policy is durable at rest in N
  places" undercounts by one. ⚠ **ADR-0187's guard structurally cannot see it**: `render.go:404-414`
  fails only for a **`ClassPolicy`** column, and this one is `ClassFreeform` (`classification.go:106`)
  — the *identical* case `wrkflw_definitions.definition` was hand-added for. **ADR-0187's own lesson
  #2 recurring one level up.** Fix is scheduled in the 0185-core plan, Task 15. Small.
- **142** — ⬜ **OPEN.** Same origin. There are **FIVE** `Authorizer` implementations, not the two ADR-0185's
  earlier drafts reasoned over: `authz.AllowAll`, `authz.RoleAuthorizer`,
  `internal/authz/casbin.Authorizer`, **`casbinauthz.Authorizer`** (public root-package delegate —
  the one a consumer actually wires, since casbin is the baseline) and **`processtest.SpyAuthorizer`**
  (public, and it **ALLOWS when `decide == nil`** — a public allow-by-default authorizer). The
  SpyAuthorizer half is deliberately NOT fixed in 0185-core; it is reported, not changed. Small.
- **128** — `persistence.NewSchedulerLocker(dl dialect.Locker)` leaks an `internal/` type through an
  exported signature; its doc comment invites consumers to supply a type they cannot name. Parked in a
  **self-cleaning** `knownOpenInternalLeaks` allow-list — a stale entry FAILS the test. → **B6**
- **130** — `timerJobKind` is unexported, so a consumer injecting their own scheduler **silently loses
  timer durability**. → **B6**
- **132** — `closeScope` has item 3b's exact nil→empty drift, on `s.Scopes`. Small.
- **133** — `cursorRecords` / `retainedRecordPrefix` are **not** gated by `defForScope`, so the
  closed-scope conflation refuted for item 10 may be genuinely reachable there. Unproven.
- **134** — 14 dangling ADR-section citations repo-wide; ~10 unowned. **A `scripts/` citation checker
  is the fix that stops the class** — proposed, not built.
- **135** — a refused advisory `Lock` would **leak a pooled connection** if the contention branch is
  refactored: it also carries `closeConn()`. Correct today, undocumented coupling. Found by mutation.
- **136** — `persistence/scheduler_locker_test.go` is a **cited-but-not-covering** test (in-memory fake
  cannot observe whether the lock is session-scoped). Third instance of this class.
- **137** — trap: `SetMaxOpenConns(0)` means **unlimited** and is the default, so a `maxOpen > 1`
  pool-safety predicate misses the widest pool. The shipped check uses `maxOpen != 1`.
- **139** — 🆕 **from ADR-0186**: on `fiber`, a compressed body **under** the cap is decompressed
  **twice** — once by the size pre-check, once by `c.Bind().JSON`. Bounded, but measurable. Removing
  it means feeding the decoded bytes to the binder across all 13 decode sites and would change bind
  error text, so it was deliberately not attempted in that delivery.
- **138** — 1 of 16 `Never` sites (myelector heartbeat) is **deliberately unhardened**: the natural
  barrier still PASSED when ablated, because clockwork's `fakeTicker` re-arms from inside `Advance`.
  Needs a connection-sever like the pgelector sibling.

## Pre-v0.1.0 blockers

1. ✅ CLOSED by ADR-0167. ⚠ Its tail (fail-open `AuthzSpec`) is **backlog 53**, still open → B3.
   🚨 Before DEPLOYING ADR-0167: audit stored rows for pre-ADR-0144 camelCase keys — **38, not 5**.
2. ✅ / 2b. ✅ CLOSED by ADR-0176. 3. ✅ CLOSED by ADR-0183. 4. ✅ CLOSED. 6. ✅ CLOSED by ADR-0166.
5. **`TestPgxNotifierListenDrainsBeforePollInterval`** — ⚠⚠ its "load-flaky" label is **UNVERIFIED**
   and triage found **three distinct failure modes** in the file. Backlog 42 carried the identical
   inherited label and was **wrong**. **Reproduce under contention and read the failure text AND its
   duration before designing anything.** A test that fails in 0.00 s is not waiting out a timeout.
7. ✅ CLOSED by the sweep. 8. ✅ CLOSED by the sweep (against its corrected statement). 9. ✅ ADR-0177.

## Standing constraints

- **Docker: standing permission for the Verification coverage + no-regressions runs only.** Probe
  first; if down, say so and label any container-free subset as the partial result it is.
- **`golangci-lint`: probe and run; if absent, offer to install or skip** — never substitute `go vet`,
  never claim "lint clean" for a run that did not execute. ⚠ `run ./pkg/...` is **not** `run ./...`.
- **Container-free**: `engine`, `runtime/{calllink,signal,task}`, `service`, `processtest`,
  `transport/http`. ⚠ `./runtime/...` as a whole is NOT; `internal/persistence/store` is NOT — **but
  `dbtest.RunTestSQLite` is pure Go and starts nothing.**
- **Judge a test run by its exit code, never a pipeline tail**; use `-count=1`.
- **Fan out subagents BY GO PACKAGE.** Concurrent agents in one package break each other's compile.
- ⚠⚠ **A mutation ablation CANNOT run in a shared working tree.** A previous session lost ~40 minutes
  to a "hang" that was one agent's live ablation observed by another. Give the ablating agent a
  worktree. ⚠ **ADR-0186 violated this and got away with it only because the agents were careful** —
  three implementation agents ran mutations in the shared tree; one observed another's transient
  uncompilable `seam.go` for ~24 s and correctly waited it out instead of reporting a failure. **That
  was their discipline, not the briefing's.** Brief the worktree next time.
- ⭐⭐ **A REVIEW FINDING IS A CLAIM AND NEEDS EXECUTION.** On ADR-0186 a `/code-review` finding's
  stated **mechanism was refuted** while its conclusion held, and it **missed the fact that made a
  clean fix possible** (fiber already sets 413 itself). Brief fix agents to **reproduce before
  fixing**, and to say so with the measurement if a finding is a false positive rather than silently
  skipping it.
- ⭐⭐ **A residual you wrote down is still a defect you shipped.** ADR-0186 *documented* two hazards
  it introduced instead of mitigating them; `/code-review` refused that distinction and both became
  MEDIUM findings. If a change introduces a hazard, the change mitigates it.
- ⚠ **`clockwork.NewFakeClock()` seeds from WALL TIME** — use `NewFakeClockAt(<fixed instant>)` or a
  clock-injection test cannot fail.
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- `/code-review` and `/security-review` are **owner-invoked only**.
- Push on merge (standing preference).

## Process lessons — the 2026-08-20 backlog sweep

⚠ **ADR-0186's lessons are NOT here** — they are in the State section above, because they are the
more transferable set (the finding-rate trend, the narrow-fixture bias, the boundary-asserted-one-
level-up shape, and the four conventions the repo already had).

- ⭐⭐ **An enumeration rots faster than anyone re-counts it.** Item 118's site list was wrong three
  times *in one session* — filed, re-counted by triage, and *still* short when the fix agent found a
  fifth branch. Prefer a **machine-checked invariant** (item 9's fix) over a number in prose.
- ⭐⭐ **A cost bound cannot catch a correctness bug that makes things faster.** The scheduler agent's
  own grid-jump off-by-one made `Monthly(2,{1})` report never-due, and the **entire `TestTrigger_*`
  suite stayed green** — including ADR-0176's reconciliation test. Only a differential probe against a
  brute-force reference caught it.
- ⭐ **An observable emitted by more than one path proves nothing until the others are excluded.** A
  first attempt at item 15 asserted a `CancelTimer` that came from `endInstance`'s terminal sweep, not
  the line under test; the mutation returned EXIT=0 and exposed it.
- ⭐ **A unit test that calls a constructor directly cannot fail when the WIRING is reverted.** Item 102
  needed a second, seam-level test to be worth anything — and reverting the wiring proved it: the seam
  test failed, the unit test still passed.
- ⭐ **Refuse an instruction that measurement contradicts.** The controller told one agent to downgrade
  item 26 to a non-issue on a 404 ms datapoint; the agent measured the curve and refused. It was right.
