# wrkflw — Handover

Current state and the next work, for a session with zero prior context. Read it
top to bottom; it is meant to stay short enough that you can.

> **Maintenance rule: rewrite this file IN PLACE. Never append.**
>
> Its predecessor became a 2057-line append-only stack and was silently abandoned for 45 ADRs —
> see `docs/plans/HANDOVER-archive.md`. Per-delivery detail belongs in that delivery's plan under
> a `▶ Progress` block. This file carries only: where `main` is, what is unmerged, and what next.

## State — updated 2026-08-20 (backlog sweep)

**`main` is `70a631e9`, PUSHED and clean.** ⚠ Re-derive it (`git rev-parse --short refs/heads/main`);
anchor on **merge** SHAs, which never move: 0184 `be6e6b55`, 0183 `a7575ed5`, 0179 `962aeb25`,
0181/0182 `1ac140f6`, 0177/0178/0180 `a5b33e4c`, 0176 `52bf0f80`, 0175 `6e4addc8`.

**▶ ONE DELIVERY IS IN FLIGHT, UNMERGED AND UNPUSHED.**

| | |
|---|---|
| branch | **`feat/backlog-sweep-small-tier`** — ⚠ re-derive the SHA, it has been amended twice |
| what | closes **43** Small-tier backlog defects + **12** adjudications, in one bundle |
| gates passed | `go build ./...` **0** · `go vet ./...` **0** · `golangci-lint run ./...` **repo-wide 0 issues** · `go test -race -coverprofile ./...` **EXIT=0, zero `--- FAIL`** · **`/code-review` — 7 findings, ALL 7 FIXED and folded** |
| `/security-review` | **0 findings.** Confidence 0.85 in the empty result. Checked and cleared: the new `requireAdminToken` guard in `examples/production_wiring` (**probed, not read** — fails **closed** at 503 when `ADMIN_TOKEN` is unset, 403 on wrong/short header, `subtle.ConstantTimeCompare`, guard runs before the inner mux, and path-normalisation tricks `//admin`, `/foo/../admin`, `/admin%2f` cannot bypass it); `internal/dbtest/dbname.go`'s identifier splice (derived only from PID + `crypto/rand` + counter, `[a-z0-9_]` only, no external input); and the casbin reload change (fail-open **preserved exactly, not worsened**; nothing sensitive newly logged). Two changes were assessed as **security improvements**: `engine/step_gateways.go`'s identity resolution closes a token-confusion route open to a consumer `IDGenerator` minting `evtgw:<id>`, and the example now mounts `AdminRoutes` behind a guard where it previously mounted nothing. Sub-threshold, not a finding: `internal/dbtest/dsn.go:78` echoes a full DSN incl. password in its "no host" error (test-only, malformed-config path). |
| coverage | touched packages: `engine` 93.1 · `runtime` 93.7 · `runtime/monitor` 87.9 · `scheduler` 93.4 · `scheduler/internal/gocron` 86.2 · `persistence` **87.5** · `definition/model` 95.1 · `internal/persistence/store` 88.1 · `internal/authz/casbin` 87.1. ⚠ **`internal/dbtest` is 42.2 % — BELOW the 85 % floor and it WAS touched.** It rose from 39.8 %; it is test scaffolding whose container-boot branches run only in the fallback path. **This is a known, deliberate exception, not a pass.** Repo total 75.0 %; `definition` 33.3, `service` 53.9, `cachetest` 73.1 are untouched and pre-existing |

**▶ A SECOND BRANCH:** `design/authz-security-b3` carries the B3 design bundle (spec + ADR-0185 +
ADR-0186 + plan). It is cut from the sweep branch and was **rebased** after the review fixes were
folded. ⛔ **NOT AUDITED — not an input to implementation.**

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

**Order: (1) run the two gates on `4661ac45` and merge it. (2) audit and implement B3. (3) B4–B7.**

The remaining **~65 Design-tier items** are grouped into five bundles. Each needs a spec + ADR + plan
and **ONE** rule-#9 adversarial audit before implementation. **Next free ADR = 0185.**

- **B3 authz/security** — 51, 52, 53, 54, 65, 98, 99, 100, 101, 103, 104, 124. A draft bundle was
  being written when this session ended; if `docs/specs/2026-08-20-authz-security-hardening.md`
  exists it is **NOT AUDITED and is not an input to implementation**. ⚠ 51/52/53 **compose** — fixing
  one alone leaves the path open. 53 is ONE item (= blocker 1's tail), with an extra leg found in
  triage: `Privileges` is documented as **not evaluated**, so a privileges-only spec is also allow-all.
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

**Closed by the sweep (branch `4661ac45`):** 3b, 3d, 3e, 9, 10, 15, 26, 28, 30, 31 (engine+scheduler
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
