# ADR-0189 — rule-#9 audit adjudication, ROUND 2

**Date:** 2026-08-25 · **Bundle audited:** `37d77a34` · **Round 1:** `7fa756d0`, see
`audit-0189-adjudication.md`. Four Opus lenses, detached worktrees at the bundle commit,
step-0 presence check passed in all four, reports beside this file as `audit2-0189-*.md`.

## ⛔ VERDICT: FAILS AGAIN. Not an input to implementation.

| lens | findings | C | M | Mi |
|---|---|---|---|---|
| execution | 14 (+4 confirmations) | 2 | 9 | 3 |
| failure-modes | 16 | 4 | 10 | 2 |
| counting | 11 (+2 confirmations) | 2 | 4 | 5 |
| interaction | 17 (+2 confirmations) | 7 | 8 | 2 |
| **total** | **58** | **15** | **31** | **12** |

## ⭐⭐⭐ THE FINDING IS THE TREND, AND IT POINTS AT THE SCOPE DECISION

| round | decisions in bundle | findings / lens | **Criticals / lens** |
|---|---|---|---|
| 1 (`7fa756d0`) | 2 | 12.0 | **1.75** |
| 2 (`37d77a34`) | 9 | 14.5 | **3.75** |

Total findings per lens moved 12.0 → 14.5, inside the repo's established
15.14 ± 0.83 band and consistent with *"the count is a function of lens count, not scope"*.
**Criticals per lens more than DOUBLED.** That is the metric this repo adopted precisely because
it does *not* track lens count — and it tracked scope.

Three lenses said so independently, unprompted:

- **interaction:** *"Five of the seven Criticals are holes the revision's own fixes opened in each
  other."* Same ratio that killed ADR-0185 three times.
- **failure-modes:** *"Two of my four Criticals live entirely inside the added decisions."*
- **counting:** the entire blast-radius miss — 186 assertions, 13 files, **7 named nowhere** — is
  the added decision's.
- **execution:** *"None of this reopens a decision. The decisions survived; the **verification
  layer** failed — in a bundle whose stated purpose was to stop shipping exactly that."*

⇒ **The round-1 adjudication's recommendation (a one-decision bundle) was overridden by owner
decision D-3, and round 2 is the measurement of that override.** This is recorded as evidence, not
as blame: the owner's reasoning — that fixing the front door while `/admin/role-bindings` is open
is not a coherent delivery — was sound on the facts available. Two of those facts turned out to be
wrong, and both were the controller's to get right (see §D).

## A. Accepted CRITICALs

### A1 — the marshalability pre-check does not prevent the brick it exists to prevent (execution F4)

`encoding/json`'s **encoder has no nesting limit; its decoder caps at 10000**. A 20000-deep
attribute passes `json.Marshal`, writes durably, and then `HumanTaskStore.Get` fails **forever**.
Executed end-to-end against a real SQLite store:
`unmarshal claim_actor for task …: invalid character '{' exceeded max depth`.

That is spec §3.5's own phrase — *"fail-open at write, fail-closed forever at read"* — reproduced
verbatim by the guard written to stop it, and **worse** than the instance-view case it is scoped
to, because `Get` is fail-loud. Plan Task 4's only fixture is `chan int`, the arm that works.

**Fix (3 lines): round-trip, do not merely encode.** `json.Marshal` then `json.Unmarshal` into
`map[string]any`, and reject on either error.
⭐ **Lesson: a guard tested with a fixture from the half that works is not tested.**

### A2 — the refusal must land at 63 handler sites, 33 of them conditional; the plan enumerates none (execution F12, counting F1/F4)

Per adapter: Instance 5 + Message 1 + Admin 15 (**4 always-registered + 11 conditional**) +
Task 3 + Health 2 = 26 routes × 3 adapters. **`POST /admin/role-bindings` — the route that
justified widening scope — is inside the invisible 11**, and the test harness supplies no optional
deps, so a missed gate there leaves **every prescribed test GREEN**. That is ADR-0188's exact
fail-open shape.

⚠ Task 5 — the *non*-security task — gets its 23 lines listed file-and-line. Tasks 9/10/11, which
carry the security change, get one sentence each.

### A3 — the blast radius was never re-derived after D-3 (counting F1/F4)

Spec §2.6's 48/13/6 member set is a good derivation **of the round-1 change only**. D-3 landed
after it, and the author's own interaction grid **explicitly exempted the blast-radius row**
(*"G, H, I, J, K … none interacts"*). Measured consequence: **186 further failing assertion lines
across 13 test files in 4 packages, disjoint from the 48** — stdlib 42, gin 44, fiber 35, parity
13 — with **seven files named in no bundle document**: `stdlib/maxbody_test.go`,
`stdlib/bodydeadline_test.go`, `gin/gin_admin_test.go`, `gin/gin_admin_errors_test.go`,
`gin/gin_bodycap_test.go`, `gin/gin_bodydeadline_test.go`, `fiber/bodylimit_test.go`.
Controller-verified: all seven exist, 51 test functions between them.

⇒ the plan's *"planned red = 23 runtime pins"* is really **~209**, and Tasks 12/13/14 — dispatched
to parallel subagents told *"you may not touch any file outside your package"* — are under-scoped
about tenfold.

⭐ **Lesson, one level above round 1's:** *a count is a derived quantity of every decision in the
set, so it can never be exempt from the interaction pass.* The grid that says "a removal generates
its own grid" exempted the one row that measures scope.

### A4 — the bundle contradicts ADR-0095, the one ADR it never opened (interaction F2, controller-confirmed)

**ADR-0095 §"Admin-by-composition (default-absent)"** states: *"Default-absent replaces the old
**default-deny (403)**: admin endpoints simply do not exist in a deployment that does not mount
`AdminRoutes`. This is safer than a built-in default-deny gate."* ADR-0095 also names *"a single
option applying equally to every admin endpoint"* as a friction point it **removed**.

Decision 7 reintroduces exactly that. The bundle cites `stdlib.Mount`'s **godoc** — the derived
artifact that happens to support the choice — and `grep 0095` over the bundle returns **one** hit,
glossed "(mountable transports)". `SECURITY.md:37-39` becomes false and no task touches it.

⚠ **Same shape as round 1's ADR-0148 finding: a transport-level gate contradicting a shipped ADR
the bundle never cited.** Twice in two rounds.

### A5 — Decision 6 breaks the ADR-0095-compliant consumer, which is in this repo (interaction F1, failure-modes F11, controller-confirmed)

`examples/production_wiring/main.go:273-275` mounts `AdminRoutes` on its own mux behind
`requireAdminToken`, a real, deliberately fail-closed guard that **never calls
`ContextWithActor`**. After Decision 6 every admin request 401s despite a correct token.

⇒ the grid's ⚠5 conclusion *"No existing correct wiring breaks"* is **false**, and Decision 7's
opt-in-ness buys nothing because the ADR orders 401 before 403. Task 16 Step 3 verifies only
"a clean start", so no prescribed step detects it. It also falsifies the plan's instruction that
the mains "must not mount `AdminRoutes`".

### A6 — the empty-actor rule was removed WHOLESALE where the repo blesses only the kiosk shape (failure-modes F7, interaction F8, F6; controller-verified)

Controller's own probe:
- `docs/adr/0148-*.md` contains **no** "kiosk" and no "anonymous" — the citation was inherited and
  restated, exactly the failure this repo documents.
- But `humantask/validate.go:24` says: *"A Claim whose `Actor.ID` is empty is deliberately
  accepted: that is ADR-0148 amendment 1 §4's kiosk claimant, **anonymous but carrying roles**."*
- The pinned fixture (`validate_test.go:45-47`) is
  `Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}}` — **empty ID, NON-EMPTY roles**.

⇒ round 1 refused **too much**; round 2 accepts **too much**. Admitting the fully-zero `Actor{}`
turns the commonest middleware bug — `actor, _ := authenticate(r)` — from fail-closed into
**fail-open on every route group**; the resulting claim is durably unattributable and invisible to
`AssignedTo("")`, whose godoc names unresolved actor IDs as the hazard it defends against.
And interaction F6 closes the loop by execution: `Actor{}` carries no attributes, so a deny-list
`actor.Attributes.*` predicate **ALLOWs** ⇒ **the D-2 fix reopens the fail-open D-1 was kept to
close.**

**Adjudicated fix, and it is narrower than either previous version:** refuse an actor with **no
dimensions at all** — no ID, no roles, no attributes. Accepts the kiosk shape; closes both holes.

### A7 — the resolver timeout does not close the hang (failure-modes F1, execution F9)

Executed against the plan's own shape: a ctx-ignoring resolver ran **1.5 s against a 200 ms
bound and returned `err == nil`**, so the request proceeds with an actor obtained after the
deadline. The precedent the bundle cites (`runtime/task/service.go:154`) carries the caveat *"the
resolver's Candidates must honour ctx cancellation for the timeout to take effect"* — **the bundle
restated the precedent and stripped the hedge**, the same mechanism that produced A6.
Task 3's prescribed test is satisfiable only by a ctx-honouring resolver: **it cannot fail on the
real hazard.**

### A8 — the admin role gate has two silent fail-opens (failure-modes F3)

Both executed. `WithAdminRoles(cfg.Roles...)` with an empty config **silently disables** the gate;
and `strings.Split("", ",")` returns `[""]` (len **1**), so an operator with an unset env var
declares a gate that *looks enabled*, while a caller presenting an absent roles header carries
`[""]` — **they match, and an anonymous caller clears the admin gate.** No prescribed test covers
either.

## B. Accepted MAJORs — grouped, not enumerated

- **The verification layer, repeatedly.** Five prescribed tests cannot fail, including plan Task
  1's, which execution **mutation-proved**: deleting the OUT clone leaves it GREEN while §3.1 and
  §5 row 1 both call "both directions clone" the contract.
- **§3.5's table is exact on both signs; the sentence built on it over-reaches.** Measured, the
  deny-list predicate still ALLOWs in **5 of 6** attribute shapes after the change: the bundle
  makes it *satisfiable*, not *closed*. And *"not backlog 103"* distinguishes the **root**, not the
  **mechanism** — `vars.status` with empty vars and `actor.Attributes.status` with the key absent
  are byte-identical ALLOWs.
- **Decision 5 owed four mitigations; the size bound was dropped with no adjudication**, and the
  accepted round-1 "unbounded durable writes" leg never reached §4's residual list.
- **Classification.** The marshalability failure is a **consumer-resolver** fault classified 400,
  where `errors.go` documents twice that a caller-uncorrectable fault stays 5xx. And the optional
  claim decoder swallows *every* error, so a malformed claim answers **401**, not the 400 §3.8
  calls "unchanged from today".
- **§3.8's "unchanged, not a regression"** is true of the three task routes and **inverts** on the
  other three groups, silently narrowing ADR-0186's 400/413 contract to 401.
- **Residual honesty.** Four of §4's nine residuals are findings rather than absolutions; residuals
  4 and 6 describe a **state-changing** gap (`POST /instances`, `/signals`, `/messages`) as a
  **read** gap. And *"the unauthenticated read surface closes"* is the overreach the bundle's own
  ⛔ forbids.
- **Doc reach.** Five live doc sites plus the README's headline `stdlib.Mount(mux, svc)` become
  false, and Task 17's `grep '"actor"'` sweep can reach **none** of them.
- **§2.6 internal contradiction:** the runtime sub-header says *7 files / 4 packages* over a table
  listing **8 files / 5 packages**. The author grid still states **37 lines** — the exact figure
  the spec and ADR both call wrong.
- **Enumerations:** Task 7 titled "six adapter option aliases" produces **nine**; §2.5 and Task 16
  say three example mount sites, there are **four**; §5 row 10 claims 9 cases, the plan delivers 1.
- **Round-1 counting F9 was never adjudicated and never folded** — the false sentence about fiber's
  option set still stands, inside the paragraph warning against exactly that error.

## C. What HELD — do not re-litigate

- ⭐ **The controller's refutation of round 1's ADR-0187 CRITICAL is CORRECT**, re-executed
  independently by the execution lens: `svc.ClaimTask` with attributes does persist
  `claim.actor.attributes`; `claim_actor`/`completion_actor`/`candidates` are already `ClassActor`
  and already published in `SECURITY.md`. **ADR-0187 needs no amendment.**
- ⭐ Round-1 failure-modes F2's rejection **on provenance** was right — confirmed independently.
  ⚠ But the ADR's *use* of it is not earned: the pre-existing channel needs an opt-in
  `humantask.ActorResolver`; the new one is fed by `RequestActorFunc`, which **every** HTTP
  consumer must configure. Same provenance, materially different population rate. §2.9 runs
  "not our fault" and "therefore not a cost" together; only the first half holds.
- ⭐ **All 54 anchors resolve and say what the bundle says** — the best anchor hygiene in the repo,
  two rounds running.
- ⭐ Ten inherited figures re-derived **exact**: 3 actor sites, 8 `CustomizeConfig` fields, 9 call
  sites, the 14+5 line split, 3 `WithActorResolver` exports, the 10 s default, the 37→48
  arithmetic, ADR-0183's citation, the CHANGELOG/STABILITY anchors, and five symmetric route
  groups per adapter.
- ⭐ §3.5's measured table is **exact on both signs**. §3.4's arms-first ordering and the
  503-not-403 choice survived a second round unchallenged.
- ⭐ `HealthRoutes`' exemption is **structurally guaranteed**.

## D. Two controller errors that shaped the owner's D-3 decision

Recorded because the decision was made on them.

1. **"`stdlib.Mount` excludes `AdminRoutes`, so admin is unprotected by default."** True as far as
   it went, and it *understated* the design: ADR-0095 deliberately chose **default-absent over
   default-deny**, and `production_wiring` implements the composition posture with a fail-closed
   token guard. The premise for "admin is open" was weaker than presented.
2. **"There are four route groups."** There are **five**; `HealthRoutes` was missed in the sentence
   asking the owner to widen scope. Corrected before the revision, but the widening question was
   already asked.

## E. Root causes, stated once

1. **A count was exempted from the interaction pass** (A3). Scope is a derived quantity of every
   decision.
2. **A guard was tested with a fixture from the half that works** (A1, A7, and the five vacuous
   tests). Twice the guard was refuted by the *other* half of its own input space.
3. **An inherited citation was restated with its hedge stripped** — A6 (ADR-0148) and A7
   (`WithCandidateResolveTimeout`'s godoc caveat). This is the repo's oldest documented failure and
   it produced two Criticals in one round.
4. **A contradicting ADR was never opened** (A4). Round 1: ADR-0148. Round 2: ADR-0095. In both
   cases the bundle cited a *derived* artifact that supported the choice.
5. **Security-carrying tasks got less enumeration than mechanical ones** (A2).
