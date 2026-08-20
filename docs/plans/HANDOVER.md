# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack and was silently abandoned for 45 ADRs —
> see `docs/plans/HANDOVER-archive.md`. Per-delivery detail belongs in that delivery's plan under
> a `▶ Progress` block. This file carries only: where `main` is, what is unmerged, and what next.

## State — updated 2026-08-21 (B3: two failed audits, scope decision pending)

**`main` is PUSHED and clean.** ⚠ Re-derive it (`git rev-parse --short refs/heads/main`); anchor on
**merge** SHAs, which never move: the backlog sweep **`020af37b`** (latest shipped code; everything
after it on `main` is docs-only), 0184 `be6e6b55`, 0183 `a7575ed5`, 0179 `962aeb25`,
0181/0182 `1ac140f6`, 0177/0178/0180 `a5b33e4c`, 0176 `52bf0f80`, 0175 `6e4addc8`.

**The 43-item backlog sweep MERGED and PUSHED** (`020af37b`); both gates passed (`/code-review`
7 findings all fixed, `/security-review` 0). Its detail is in the memory topic file — do not
re-inline it here.

**▶ ONE DELIVERY IN FLIGHT: `design/authz-security-b3` — ⛔ its RE-AUDIT ALSO FAILED. A SCOPE
DECISION IS PENDING and no further revision should start until it is made.**

⚠ **Second failed audit.** The 2026-08-20 draft failed on individual decisions (58 findings, 12
Critical); the 2026-08-21 revision fixed those and **failed on the interactions between the decisions
it rewrote** — **38 findings, ~13 distinct Criticals**, two found independently by two lenses each,
and **five are holes the revision's own fixes opened in each other**. Adjudication:
`docs/plans/sweep-evidence/reaudit-b3-adjudication.md`, with three lens reports beside it (2,631
lines). **Read it before touching this bundle.**

⚠⚠ **Three findings are in the bundle's OWN evidence file** — the one written to stop unexecuted
claims. Most sharply: the `274/128/5` triple was **inherited verbatim** from the previous audit under
a caption claiming nothing was inherited. `274` was re-run and matched, so the other two were never
checked; all three are wrong (**273 / 121+6 / ≥13**), because `grep NewUserTask(` is **one of three
authoring forms** — `build.Builder.AddUserTask` and YAML `kind: userTask` are invisible to it.
**This is the `"by"`-grep failure repeating one round later.** ⭐ **Re-running a command is not
re-deriving a claim when the command is the wrong net.**

⭐ **The other repeated shape:** a load-bearing claim evidenced against **the vendor or a stand-in**
where the decision acts on **the repo's wrapper one layer down**. It happened twice — the jsonschema
probe called the library instead of `runtime/validation.Gate` (which flattens the typed error with
`%s`, so `errors.As` is false at `ClassifyError`), and the tri-state was evidenced against
`store_core.go`'s instance snapshot instead of `humantask_store.go`'s `eligibility` column, **which is
the copy all four `Authorize` sites actually read.**

**Root causes (the useful output of this round):**
1. **D4 is a syntax problem that cannot be solved with syntax.** Three rounds, three disjoint hole
   sets — dominance admits `not ("k" in vars) and …`, `== false`, and the ternary *alternate* (which
   matches D4's own wording verbatim); it also **denies** a correct predicate because `and` is
   left-associative; the zero-reference rule is **disarmed by any one ordinary reference**; and the
   `actor` axis gets **zero** protection because `Attributes` is a struct field that always exists.
2. **D5 needs a per-verb authorization model that does not exist.** One `Eligibility` spec serves
   four verbs and casbin applies `Privileges` unconditionally, so a `reassign` token bricks Claim,
   Complete and RefreshCandidates too.
3. **D3's mechanism is sound, its surface was under-modelled** — two durable locations, and
   `Open *bool` makes the zero value of the **public** `authz.AuthzSpec` fail-**open**.
4. **Bundle size is the multiplier.** Both failures were interaction failures.

⭐ **What HELD — do not re-litigate:** the revision fixed **both** structural defects the first audit
found (every citation resolves at the bundle commit; the pin net is closed *by construction*), and
**all four of its corrections against the previous audit are confirmed right** (`has()` absent, `??`
unparenthesised, `get()` zero-ref, the ~15× bound error — the counting lens adjudicated the last
formally: 5 000 = 610 ms, 10 000 = 2.442 s). `WithMaxEvalElements`'s plumbing is real, not a zombie.
The ctx-propagation `ASSUMPTION` is **true in all four legs and can be discharged**.

Local only, unmerged, unpushed. Docs-only. Carries the B3 bundle for backlog
51/52/53/54/65/98/99/100/101/103/104/124 (+ parked 102): spec + **ADR-0185** + **ADR-0186** + plan +
**`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`** (the executed evidence for the
revision). ⚠ Do not quote its SHA — it is amended on every revision.

**Chronology.** Drafted 2026-08-20 → audit #1 failed (58 findings, 12 Critical) → revised
2026-08-21 with four Decisions changed and every new claim executed → audit #2 failed (38 findings,
~13 Critical). Both audits used three Opus lenses in three detached worktrees at the bundle commit,
with the step-0 presence check passing in all six. ⚠ **The two adjudication records are the
authoritative account** — `audit-b3-adjudication.md` then `reaudit-b3-adjudication.md`. Do **not**
reconstruct the decision history from the ADR banners, and do **not** treat the revision's own
evidence file as settled: three of audit #2's findings are in it.

### ▶ OWNER DECISIONS TAKEN 2026-08-21 — B3 is re-cut into THREE deliveries

1. **ADR-0186 — untrusted input & disclosure** (backlog 54 variables disclosed/aliased on the read
   path, 65 SSRF via expression-derived URLs, 98 no body cap, 99 expression cost unbounded in input,
   104 4xx bodies echoing internals, + posture for 100/101): body caps, an SSRF-safe default for
   `httpcall`, variable redaction, and what a 4xx body may say. **Ships FIRST.**
   ⛔ **STATUS: its first standalone audit FAILED — 63 findings, 33 Critical, four lenses, 4,020
   lines.** Adjudication: `docs/plans/sweep-evidence/audit-0186-adjudication.md`.
   ⚠⚠ **But read the adjudication before despairing: the failure is a PLAN defect, not a mechanism
   defect.** Three of the four lenses independently said so — *"six Criticals share one root cause:
   a decision stated in the ADR whose realisation lands in a package no phase assigns it to"*
   (failure-modes); *"all four Criticals are one-line fixes, not design increments"* (execution);
   *"five of seven Criticals are a decision assuming a channel another decision owns and does not
   provide"* (interaction). **Nothing here needs a design increment**, unlike the deferred
   backlog-103/124 work.
   ⭐ **NEXT ACTION — one change closes ~7 findings: move the element bound from EVALUATION to
   ADMISSION** (bound the variable map when it enters the instance, not when a predicate reads it).
   Then delete D2's "count once per env" mandate — it is **both unimplementable and unnecessary**.
   Then rework the phase table so every decision's realisation has a package. Then re-derive three
   enumerations: decode sites (**36 of 39**, not 39 — three *discard* the decode error and return
   2xx), read paths (**11**, not 8), plaintext columns (**six**, not two).
   ⚠ **`WithAllowedHosts` is unimplementable as designed** — a `net.Dialer.Control` hook only ever
   sees the resolved `IP:port`, never a hostname. And **D3 collides with the existing
   `WithHTTPClient`** — the same collision class the bundle flags for `runtime` and missed here.
2. **ADR-0185-core** — D1 (the actor travels in `context.Context`, not the request body),
   D2 (constructing a `ProcessEngine` without an authorizer is an error) and D3 (an eligibility spec
   that states nothing denies). Backlog 51/52/53 — the chain the spec says *"must ship as a set"*.
   D3 still needs its two confirmed defects fixed: `AuthzSpec` is durable in **two** places
   (`wrkflw_human_task.eligibility` is the one all four `Authorize` sites read), and `Open *bool`
   makes the zero value of the **public** `authz.AuthzSpec` fail-**open**.
3. **DEFERRED to their own bundles** — D4 (backlog 103: deny-list ABAC predicates allow when the
   variable is missing) and D5 (backlog 124: completion never checks who claimed). Each needs a
   design increment that does not exist yet.
   ⚠ **D4's replacement shape is decided: declare required/optional keys ON THE SPEC.** No syntax
   inference — `AuthzSpec` gains an explicit key declaration and evaluation denies when a
   declared-required key is absent. Chosen because syntactic guard inference produced **three
   disjoint hole sets in three rounds**. ⚠ The `actor.Attributes` depth-2 case still needs its own
   rule: `Attributes` is a struct field that always exists, so reference-presence checking is
   vacuous there.
   ⚠ **D5 needs a per-verb authorization model.** One `Eligibility` spec serves four verbs and
   casbin applies `Privileges` unconditionally, so any per-verb token gates all four.

**▶ NEXT ACTION: fold ADR-0186's audit — start with the admission-boundary move, which collapses the largest cluster — then re-audit.** A bundle whose decisions changed has not been audited.
three detached worktrees at the bundle commit, step-0 presence check).** Do not carry ADR-0185
material into it. The two adjudication records —
`docs/plans/sweep-evidence/{audit,reaudit}-b3-adjudication.md` — are the authoritative account of
what was decided and why; the ADR banners are a chronology, not a spec.

### ⚠ What `/code-review` found, and why it matters procedurally

**7 findings, all fixed: 3 HIGH, 2 MEDIUM, 2 LOW.** All three HIGHs were in **blocker 7** — the
shared-test-database change, which had no design bundle and no audit *because triage tiered it
"Small"*. It was small in lines and large in blast radius. **Triage graded effort and was read as
grading risk; nothing in the process distinguished the two.** Fix that before the next sweep.

- **1/2/3 (HIGH)** — the per-test DB name came from a **package-level** counter, so every
  concurrently-running `go test` binary started at 1 and collided on one shared server. Postgres
  failed loudly; **MySQL corrupted silently** (`CREATE … IF NOT EXISTS` succeeded, two packages
  shared one DB, then one dropped it under the other). CI exported both DSNs unconditionally, so
  this was **CI's default path**. Fixed: `wrkflw_test_p<pid>_<12 hex>_<counter>`, `IF NOT EXISTS`
  removed so a residual collision fails loudly, and cleanup refuses to drop a database not carrying
  this process's tag.
- **4 (MEDIUM)** — understated: the `persistence.Option` **type alias itself** published an
  `internal/` type as the public contract. Aliases replaced with package-owned types.
- **5 (MEDIUM)** — understated: **four** `docs/observability.md` recipes did not compile, one
  importing `wrkflw/rest` which has never existed. Now all four compile **verbatim from the
  markdown** in an external module. ⇒ **backlog 108 closed for the document.**
- **6 (LOW)** — resolved doc-only, by measurement: honouring the new `ok` return is
  `reflect.DeepEqual`-identical in state and commands at both consumers, so wiring it up would be
  theatre. ⇒ **backlog 133 SHARPENED — re-file it as a *visibility* question** (should a walk whose
  record source vanished raise a WARN/incident?), which is reachable and testable.
- **7 (LOW)** — CHANGELOG now covers this commit's own new public surface.

⚠ **`docs/architecture-audit` (`9769a8e5`) still exists on this machine ONLY** and cannot be pushed
(public repo, working exploit chains). The *defect statements* are safe — they are in the backlog below.

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

## ▶ NEXT WORK

**Order: (1) RE-AUDIT B3, then implement it. (2) B4–B7. (3) blocker 5.**

The remaining **~65 Design-tier items** are grouped into five bundles. Each needs a spec + ADR + plan
and **ONE** rule-#9 adversarial audit before implementation. **Next free ADR = 0187.**

- **B3 authz/security** — 51, 52, 53, 54, 65, 98, 99, 100, 101, 103, 104, 124 (+ parked 102). ⛔ **TWO failed audits; a scope decision is pending — see State above.**
  Its next action is an **owner scope decision**, not another revision.
  See the State section above for what changed and why — do not re-derive it, and do **not** read the
  four documents as if the 2026-08-20 draft's decisions still stand.
  ⚠ **Dispatch the re-audit exactly as the first one was dispatched** — it worked: three Opus lenses
  (execution / failure-modes / **counting**), three **detached worktrees at the bundle commit**, and
  a **step-0 bundle-presence check that every brief states explicitly**. The counting lens again
  found what the others could not (the wrong-net grep, the stale anchor), for the second bundle
  running. Brief it that the failure mode is **the net and the anchor, not the arithmetic**.
  ⚠ The re-audit is over the **revised** bundle, so `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`
  is an input to it, not a conclusion of it — attack the evidence file too.
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
**Adjudicated, not defects:** 8, 18, 20, 97, 126, 3f, 6, 35, 36, 37, 45, 46.

**Still open — Design tier:** 4, 5, 7, 11, 12, 13, 17, 19/41, 24, 27, 29, 32, 33, 39, 47, 50, 51, 52,
53, 54, 55, 56, 57, 59, 60, 61, 62, 63, 64, 65, 66, 67, 69, 70, 71, 72, 73, 76, 77, 78, 79, 80, 81,
82, 83, 85, 86, 88, 90, 91, 92, 93, 94, 96, 98, 99, 100, 101, 103, 104, 105, 106, 109 (reject leg),
110, 111, 124. **68 deferred.**

⭐ **90 (silent claim theft)** remains the sharpest small-ish item and is Design tier only because it
needs a new `ErrTaskAlreadyClaimed` sentinel: any eligible actor takes a task another holds, `err=nil`,
bypassing the guard `Reassign` has twelve lines below it.

**🆕 New defects found during the sweep, in no prior backlog:**

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
- ⚠⚠ **A mutation ablation CANNOT run in a shared working tree.** This session lost ~40 minutes to a
  "hang" that was one agent's live ablation observed by another. Give the ablating agent a worktree.
- ⚠ **`clockwork.NewFakeClock()` seeds from WALL TIME** — use `NewFakeClockAt(<fixed instant>)` or a
  clock-injection test cannot fail.
- ⚠ **Restore a mutation from a `cp` backup, never `git checkout <path>`.**
- `/code-review` and `/security-review` are **owner-invoked only**.
- Push on merge (standing preference).

## Process lessons this sweep earned

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
