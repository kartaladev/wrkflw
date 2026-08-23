# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack and was silently abandoned for 45 ADRs —
> see `docs/plans/HANDOVER-archive.md`. Per-delivery detail belongs in that delivery's plan under
> a `▶ Progress` block. This file carries only: where `main` is, what is unmerged, and what next.

## State — updated 2026-08-23 (**ADR-0187 MERGED and PUSHED**; nothing in flight)

**`main` is PUSHED and clean.** ⚠ Re-derive it (`git rev-parse --short refs/heads/main`); anchor on
**merge** SHAs, which never move: **ADR-0187 at-rest posture `4e2c0af4` (latest shipped)**,
ADR-0186 body caps `13b3bfb0`, the backlog
sweep `020af37b`, 0184 `be6e6b55`, 0183 `a7575ed5`, 0179 `962aeb25`, 0181/0182 `1ac140f6`,
0177/0178/0180 `a5b33e4c`, 0176 `52bf0f80`, 0175 `6e4addc8`.

**▶ NOTHING IS IN FLIGHT.** `design/at-rest-posture` merged `--no-ff` as **`4e2c0af4`** and pushed;
branch deleted; worktrees clean.

### ✅🚚 ADR-0187 — the at-rest posture is stated once and machine-checked (backlog 100/101)

Merge **`4e2c0af4`**. Both Delivery Gates passed: `/code-review` **16 findings, none a false
positive**, all fixed and folded; `/security-review` **0 findings**. Post-merge on `main`:
`go test -race ./...` EXIT=0 zero FAIL, `golangci-lint run ./...` 0 issues, `gen-at-rest.sh`
round-trips with a clean tree. `internal/atrest` **92.6%**, `internal/persistence/store` **88.1%**.

**What shipped.** `SECURITY.md` gains a **generated** `## Data at rest` section — all **87** stored
columns (79 `wrkflw_*` + 8 `casbin_rule`) classified by logical role, with per-dialect physical type
and a `keyed` **lower bound** (postgres 29 / mysql 28 / sqlite 28). No encryption mechanism: that
deferral is the decision. `internal/atrest` holds the DDL reader, rule-based migration discovery,
the classification, and the renderer; `scripts/gen-at-rest.sh` regenerates; guards fail the build on
an unclassified column, a stale entry, drift, cross-dialect key-set disagreement, or a parse that
disagrees with a live database. Detail + residuals: `docs/plans/2026-08-22-at-rest-posture.md`
`▶ Progress`. Audit records: `docs/plans/sweep-evidence/{audit,reaudit}-0187-*.md`.

### ⭐⭐⭐ READ BEFORE THE NEXT DELIVERY — what this one actually established

1. **EXECUTION FOUND EVERY REAL DEFECT; READING FOUND NONE.** Two design audit rounds (64 findings
   / 17 Critical, then 34 / 11), eight task reviews, a whole-branch review, then the owner gate —
   and everything that mattered came from RUNNING something.
2. ⭐⭐ **A GUARD CAN BE BLIND TO THE CATEGORY OF CLAIM IT WAS BUILT TO POLICE.** `Render` retyped a
   class claim as prose, and the drift guard compares `SECURITY.md` to `Render`'s **own output** —
   so that sentence was only ever compared against itself. A reciprocal class swap left the WHOLE
   SUITE GREEN while the document contradicted its own table.
3. ⚠⚠ **A FALSE STATEMENT HAD ALREADY SHIPPED, TWICE.** The published table carried `BIGINT` where
   MySQL declares `BIGINT AUTO_INCREMENT`, and a MySQL column name (`trigger`) that **does not
   exist** (MySQL declares `trigger_`) — the latter because D2b's set-comparison normalization
   leaked into rendering.
4. ⚠⚠⚠ **"Authorization policy is durable in TWO places" was FALSE — there are THREE.** Per-node
   `eligible_roles`/`eligible_privileges`/`eligible_expr` serialize into
   `wrkflw_definitions.definition` (`definition/model/node_wire.go:27-29` →
   `internal/persistence/store/definitions.go:120`). It was restated **NINE** times, including in
   **E8, the evidence record the ADR cites as proof for that very decision**. Now derived from
   `atrest.PolicyAtRestLocations` and validated both ways. **⚠ This premise ALSO fed ADR-0185-core's
   D3 in this file — corrected in place; do NOT design 0185 against "two".**
5. ⭐⭐ **A PARKED RESIDUAL IS NOT A SAFE RESIDUAL.** I parked "two places" as *retyped-not-derived,
   true today, unguarded*. It was already false. **Treating "unguarded" as the risk while the claim
   itself is wrong is the same error one level up. Re-derive a residual when you park it.**
6. **TWELVE tests-that-cannot-fail now; THREE caught here** — a brief-prescribed reconciliation test
   that checked only the test side, a **liveness guard** that passed while the function it guarded
   ignored its own parameter (⚠ the mechanism adopted to stop shipping unfalsifiable tests had
   become one), and the drift guard above.
7. ⭐ **Ask what SHAPE a guard detects, not whether it fails once.** A re-reviewer mapped a new
   guard's reach: catches `(E3)`, `(E12)`; misses bare `E3`, `see E3`, `(e3)`.
8. **An enumeration rotted AGAIN** — implementation found a **fourth** parser trap where the bundle
   said three, after two audit rounds one of which was a lens dedicated to re-counting.
9. ⭐ **`go test -run` on a nonexistent name exits 0 — and ANCHORING THE REGEX DOES NOT HELP.** I
   shipped `-run '^TestSecurityMdInSync$'` in `gen-at-rest.sh` calling it safe against renames.
   Anchoring stops it matching the WRONG test; it does nothing about matching NOTHING. The gate
   caught it in the one guard `SECURITY.md` tells readers protects them.

⚠ **Residuals parked** (plan `▶ Progress`): `keyed`'s UNIQUE/index facts are now cross-checked, but
three published sentences remain retyped-not-derived; and a `CREATE INDEX` naming a table from a
different migration file derives no key silently — latent until a `0002_*.sql` lands, and stated in
the published caveat rather than denied by it.

`design/authz-security-b3` was merged `--no-ff` and deleted. Only `docs/architecture-audit`
(`9769a8e5`, local-only, unpushable — working exploit chains in a public repo) remains unmerged.

### ✅🚚 ADR-0186 — request bodies are capped before they are parsed (backlog 98)

Merge **`13b3bfb0`**. Both Delivery Gates passed: `/code-review` 4 findings (2 MEDIUM, 2 LOW) all
fixed and folded via `--amend`; `/security-review` **0 findings**. Post-merge on `main`:
`go test -race ./...` EXIT=0, `golangci-lint run ./...` 0 issues, `go vet`/`go build ./examples/...`
clean. Detail: [[adr-0186-body-caps-shipped]].

**What shipped.** Cap the **READ**, leave each adapter's **PARSE** untouched. `MaxBodyBytes int64`,
default 1 MiB in `ResolveConfig`'s **struct literal** (not the post-loop guard, which would erase an
explicit `0`); **`n <= 0` disables**. Bare `httpcore.ErrRequestBodyTooLarge` → **413**, arm
**before** the ordered 400 arm, **static body naming no limit**. `BodyReadTimeout` 30 s, armed only
when the cap is active. **Non-generic per-adapter aliases** for both options — the generic form
**cannot infer `R`**. ⚠ **The cap is PER ROUTE GROUP**: `Mount` covers **6 of 13** sites; **21 of 39
repo-wide sit behind `AdminRoutes`**; `MountHealth` forwards no options. Five residuals are in
`SECURITY.md`.

### ⭐⭐⭐ READ THIS BEFORE DESIGNING ANYTHING — the trend was the finding

ADR-0186 was **audited SEVEN times and never passed**. Scope was cut ~12× and the count never moved:

| round | scope | findings | Critical |
|---|---|---|---|
| 1–2 | B3, 12 items | 58, 38 | 12, ~13 |
| 3–4 | 6 decisions | 63, 56 | 33, 28 |
| 5 | 3 decisions | 65 | 20 |
| 6 | **1 decision** | 61 | 24 |
| 7 | **1 decision, stripped** | 57 | 14 |

**Round 6 is the control experiment: it failed at a bundle size of ONE decision.** Splitting was
exhausted there, stripping at round 7. ⇒ **the finding rate is a property of the PROCESS, not the
bundle.** It shipped under **rule #11 as a deliberate, recorded exception to rule #9** — ADR-0186's
`Status` line, its banner and the merge commit all say so, so it is never mistaken for a bundle that
passed. ⚠ **That precedent is available for a future bundle that stops converging. It is NOT a
licence to skip audits.**

**Two characterisations to carry into every future bundle:**
1. ⭐⭐⭐ *"The bundle's probes are narrow in a consistent direction: **toward the fixture that
   demonstrates the fix**."* ⚠⚠ **Execution catches a false PREMISE, not a NARROW FIXTURE** — the
   probe passes. ⚠ Round 7's bundle **quoted this in its own banner and then reproduced it in its
   central evidence section.**
2. ⭐⭐⭐ *"A boundary derived correctly at one level, then **asserted one level up without
   re-derivation**."*

⭐⭐⭐ **FOUR times this lineage claimed a gap the repo had ALREADY FILLED** —
`runtime/kernel/cursorcodec.go:27-28` (trailing data, ADR-0160) · `action/httpcall.go:186-194` (the
cap convention) · `wrkflw_rest_requests_total{http.status_code}` (already counts every 413) ·
`httpcall.go:209` (`30 * time.Second`). ⇒ **"Search the repo for an existing convention BEFORE
writing a new symbol" is now a step in the plan's fan-out rules.**

⭐⭐ **18 design corrections came from IMPLEMENTATION, not from seven audits** — incl. a mutation
that **refuted the stated reason** for the bare sentinel, a prescribed falsifier that **did not
falsify what it claimed**, prescribed tests that left **11 of 13 sites unverified**, and an
implementer who **deleted a vacuous test of their own** (the seventh test-that-cannot-fail in this
repo, avoided unprompted).
⚠⚠ **Both `/code-review` MEDIUMs were regressions this delivery INTRODUCED and had DOCUMENTED
rather than MITIGATED.** The review refused that distinction, correctly. **A residual you wrote
down is still a defect you shipped.**

**Seven adjudications + 28 lens reports** are on `main` in `docs/plans/sweep-evidence/`: `audit-b3`,
`reaudit-b3`, `audit-0186`, `reaudit-0186`, `audit3-0186`, `audit4-0186`, `audit5-0186`.

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

**Order: (1) the four REMAINING deferred B3 deliveries — ⚠ §AT-REST is DONE (ADR-0187, merge
`4e2c0af4`); **ADR-0185-core is next**. (2) B4–B7. (3) blocker 5.**

The remaining **~65 Design-tier items** are grouped into bundles. Each needs a spec + ADR + plan and
**ONE** rule-#9 adversarial audit before implementation. **Next free ADR = 0188.**

### B3 authz/security — **four** deliveries left (§AT-REST shipped), all held in one file

⚠ **Read `docs/specs/2026-08-21-untrusted-input-deferred-slices.md` before starting any of them.**
It carries **every finding their audits established** and the design increment each still owes.
⚠ The 0187–0191 numbering in that file is a **reservation, not a record** — 0187 is now USED (§AT-REST); next free is 0188.
⚠⚠ **That file is now PUBLIC on `main`** (owner-authorised) and is a roadmap of five *unfixed*
holes. **Treat them as time-sensitive.**

1. ~~**§AT-REST** (backlog 100/101)~~ — ✅ **SHIPPED as ADR-0187, merge `4e2c0af4`.** See the State
   section above. ⚠ **Do not restart it from the deferred-slices file** — ADR-0187 supersedes that
   section's design and re-derived its claims rather than inheriting them. ⚠ Two of that section's
   own prescriptions turned out WRONG and are recorded corrected in ADR-0187: the glob
   `**/migrations/*.sql` matches **1 of 4** files (discovery is a stated RULE plus a two-way
   declaration, because dialect cannot be inferred from a path), and the classification is
   **dialect-INVARIANT by role** — the 48-vs-67 divergence is *entirely* the `TIMESTAMPTZ`→`TEXT`
   mapping, so only `keyed` is per dialect.

2. **ADR-0185-core** (51/52/53) — actor in `context.Context`; constructing a `ProcessEngine` without
   an authorizer is an error; an eligibility spec that states nothing denies. ⚠ Its D3 carries two
   confirmed defects: `AuthzSpec` is durable in **THREE** places — ⚠⚠ **CORRECTED 2026-08-23 by
   ADR-0187's `/code-review` gate; this line previously said "two" and that premise was FALSE.**
   The third is **`wrkflw_definitions.definition`**: `definition/model/node_wire.go:27-29` declares
   `EligibleRoles`/`EligiblePrivileges`/`EligibleExpr` with json tags and
   `internal/persistence/store/definitions.go:120` marshals whole definitions into that column.
   ⇒ **do NOT design ADR-0185-core against "two".** (`wrkflw_human_task.eligibility` is
   the one all four `Authorize` sites read), and `Open *bool` makes the zero value of the **public**
   `authz.AuthzSpec` fail-**OPEN**.
3. **§4XX** (104) — the largest and least settled; needs real design, not a fold.
4. **§READ-PATH** (54) · 5. **§SSRF** (65) · 6. **§BOUND** (99).

**Also carried, not yet scheduled:** backlog **103** (deny-list ABAC predicates allow on a missing
variable) is a **syntax problem that cannot be solved with syntax** — decided shape is *declare
required/optional keys ON THE SPEC*, no inference; ⚠ `actor.Attributes` needs its own rule because
it is a struct field that always exists, so reference-presence checking is vacuous there. Backlog
**124** (completion never checks the claimant) needs a **per-verb authorization model that does not
exist** — one `Eligibility` spec serves four verbs and casbin applies `Privileges` unconditionally.

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
