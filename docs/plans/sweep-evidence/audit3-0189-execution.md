# ADR-0189 round 3 — EXECUTION lens

Worktree `wt3-execution`, detached at `3e96e836`. Step 0 PASSED: spec, ADR, plan and
`audit2-0189-removal-grid.md` all present.
No Docker. All probes SQLite / pure-Go. Every finding below was RUN, not read.

---

### F1 — the ROUND-TRIP guard still admits an input that bricks `HumanTaskStore.Get` forever, because the store wraps the attributes in one more object than the guard validates — [CRITICAL]

**Bundle text attacked:** spec §3.5 — *"marshal **and unmarshal**, reject on either, and **bound
the marshalled size**"*; plan Task 4 Step 3's `resolveRequestActor`, which round-trips
`json.Marshal(a.Attributes)` → `json.Unmarshal(blob, &round)` into a `map[string]any`; and spec
§5 row 10, *"⭐ a **20000-deep** attribute ⇒ 503 … the fixture that proves the round-trip guard is
the guard"*.

**What I ran:** the guard **verbatim** from plan Task 4 Step 3 (round trip + a 1 MiB size bound, so
size cannot be doing the work), then a real `store.NewHumanTaskStore(dbtest.RunTestSQLite(t),
dialect.NewSQLite())` Upsert/Get. Payload: `map[string]any{"a": [[[…1…]]]}` with **9999 nested
arrays** — attributes-document depth 10000, **20005 bytes**.

```
PROBE marshalled attribute bytes = 20005
PROBE ADR-0189 round-trip guard verdict = ""  ("" means ADMITTED)
PROBE Upsert: OK — the row is written durably
PROBE Get -> err = workflow-store: get task zz-brick: unmarshal claim_actor for task zz-brick:
                   invalid character '[' exceeded max depth
PROBE ⚠⚠ CONFIRMED: the guard passed, the write landed, and Get fails FOREVER
PROBE AssignedTo -> n=1 err=<nil>       # degrades silently
PROBE AssignedTo degraded actor attributes = map[]
--- PASS: TestZZ_RoundTripGuardStillBricksTheStore (0.02s)
```

**Mechanism, measured separately (binary search over depth):**

```
PROBE max depth the GUARD admits: 9999          # json.Unmarshal(blob, &map[string]any)
PROBE max depth the STORE admits: 9998          # json.Unmarshal into htActorRemainder
PROBE ⚠ BYPASS at depth=9999: guard="" storeErr=invalid character '{' exceeded max depth
PROBE ⚠⚠ GAP SIZE = 1 depth admitted by the guard and REJECTED by the store
```

`internal/persistence/store/humantask_store.go:552-566` stores the actor as
`htActorRemainder{Roles, Attributes}` ⇒ the durable document is `{"attributes":{…}}`, **exactly one
object deeper than what the guard validated**. `encoding/json`'s decoder cap is 10000 for the whole
document, so the guard's ceiling is off by the wrapper depth. The guard is **not wrong in kind — it
is wrong by one level**, and one level is all it takes.

**Why the size bound does not save it:** the array form costs ~2 bytes per level. The bypass is
**20005 bytes**. Any bound large enough to admit a legitimate attribute map (the bundle never
states a value — see F2) admits this. The spec's own 20000-deep *object* fixture marshals to
**120010 bytes**, so a bound anywhere in 20 KB–120 KB makes §5 row 10 pass on the **size** arm while
the round-trip arm stays unexercised — the fixture the spec calls *"what proves the guard is the
guard"* would then prove nothing.

**Verdict:** the Critical that failed round 2 (A1) is **STILL LIVE** against the round-2 fix, with
the identical failure text (`… exceeded max depth`) from the identical call (`HumanTaskStore.Get`).
Spec §3.5's lesson — *"a guard tested with a fixture from the half that works is not tested"* —
recurs a third time: the round-trip guard was fixtured only at 20000, which is deep in the rejected
region, and never at the boundary where the wrapper depth matters.

**Concrete fix — do not round-trip the attributes, round-trip THE DOCUMENT THE STORE WRITES.**
Replace the `map[string]any` target with the store's own shape, or better, bound depth explicitly:

```go
// The durable form is {"roles":…,"attributes":{…}} (store.htActorRemainder), one
// object deeper than the attributes alone, and encoding/json's DECODER caps the
// whole document at 10000. Validate the shape that is actually stored.
blob, mErr := json.Marshal(struct {
    Roles      []string       `json:"roles,omitempty"`
    Attributes map[string]any `json:"attributes,omitempty"`
}{Roles: a.Roles, Attributes: a.Attributes})
```
and add spec §5 row 10b: **an attribute nested exactly 9999 arrays deep (20005 bytes) ⇒ 503**, with
the note that the fix is what makes it fail. ⚠ A depth bound well below 10000 (e.g. 100) is the
more honest guard: it does not depend on `encoding/json`'s internal constant, which is not part of
Go's compatibility promise. ⚠ Whatever is chosen, the prescribed fixture must sit at the
**boundary**, not at 20000.

---

### F2 — `maxActorAttributeBytes` is used but never given a value anywhere in the bundle, and a plausible value makes the load-bearing fixture pass for the WRONG REASON — [MAJOR]

**Bundle text attacked:** plan Task 4 Step 3 uses `maxActorAttributeBytes` twice; the plan's own
test grid prescribes *"`Attributes` marshalling above the size bound ⇒ `ErrIdentityUnavailable`"*
(plan:224) and spec §5 row 11 *"an oversize attribute payload ⇒ 503 | no size bound exists"*.

**What I ran:**
```
$ grep -rn "maxActorAttributeBytes\|size bound" docs/specs/2026-08-25-*.md \
      docs/adr/0189-*.md docs/plans/2026-08-25-*.md
docs/adr/0189-…:182:### 5. `Actor.Attributes` flows, behind a ROUND-TRIP guard and a size bound
docs/specs/…:548:| 11 | an oversize attribute payload ⇒ 503 | no size bound exists |
docs/specs/…:589:… (c) a size bound was added; …
docs/plans/…:224:| `Attributes` marshalling above the size bound | `ErrIdentityUnavailable` |
docs/plans/…:270:		if len(blob) > maxActorAttributeBytes {
docs/plans/…:271:			… ErrIdentityUnavailable, maxActorAttributeBytes)
```
**Observed:** six mentions, **zero values**. The bundle nowhere states a number, a default, an
option to change it, or whether it is configurable at all. The implementing subagent invents it.

**Why this is not cosmetic** — measured:
```
PROBE spec fixture: 20000-deep marshals to 120010 bytes
PROBE bypass fixture (9999 nested arrays): 20005 bytes
```
Any invented bound in **20 KB … 120 KB** (64 KiB is the obvious guess, and it is what
ADR-0186's neighbourhood would suggest) makes spec §5 row 10 — the fixture the spec calls
*"what proves the round-trip guard is the guard"* — **pass on the size arm**, leaving the
round-trip arm with no covering fixture at all. That is the round-2 failure repeating one level
up: the fixture is again taken from the half that works.

**Verdict:** an unspecified security-relevant constant, in a bundle whose entire subject is that
unstated things get invented wrongly. Compounds F1: neither bound is derived from the thing that
actually breaks (the store's decoder depth cap).

**Concrete fix:** state the value and its derivation in spec §3.5 and ADR Decision 5 (a size bound
AND an explicit depth bound, with the depth bound justified against
`internal/persistence/store`'s `htActorRemainder` wrapper — see F1). Add to §5 row 10 that the
size bound must be verified NOT to be what rejects the depth fixture — the test must assert on the
error's wording, not merely on 503.

---

### F3 — the dimension rule achieves NEITHER of the two properties §3.3 justifies it with; measured, four admitted shapes are exactly as bad as the one it refuses — [CRITICAL]

**Bundle text attacked:** spec §3.3: *"Round 2 accepted everything, including `Actor{}`. Measured:
that turns the commonest middleware bug … into **fail-open**; the claim is durably unattributable
and invisible to `AssignedTo("")` … And `Actor{}` carries no attributes, so a deny-list
`actor.Attributes.*` predicate **ALLOWs** ⇒ round 2's fix **reopened the fail-open §3.5 closes**."*
⇒ *"**refuse an actor with NO DIMENSIONS AT ALL.** The kiosk shape survives; the zero-value bug does
not."*

**What I ran:** the rule verbatim (`a.ID == "" && len(a.Roles) == 0 && len(a.Attributes) == 0`)
against the real `authz.RoleAuthorizer` with the spec's own deny-list predicate
`actor.Attributes.status != "blocked"`, plus the attributability property
(`AssignedTo` matches on `Actor.ID`, `humantask.go:205-213`).

```
PROBE Actor{} — the zero value the rule exists to refuse  REFUSED 401  attributable=false  denyList=ALLOW ← fail-open
PROBE {Roles:[kiosk]} — the blessed kiosk shape           PASSES       attributable=false  denyList=ALLOW ← fail-open
PROBE {Roles:[""]} — one EMPTY role                       PASSES       attributable=false  denyList=ALLOW ← fail-open
PROBE {Roles:[]} non-nil empty                            REFUSED 401  attributable=false  denyList=ALLOW ← fail-open
PROBE {Attributes:{x:nil}} — one NIL attribute            PASSES       attributable=false  denyList=ALLOW ← fail-open
PROBE {Attributes:{"":""}} — empty key, empty value       PASSES       attributable=false  denyList=ALLOW ← fail-open
PROBE {ID:alice} — id only, no attributes                 PASSES       attributable=true   denyList=ALLOW ← fail-open
PROBE {ID:alice, Attributes:{status:blocked}}             PASSES       attributable=true   denyList=DENY
```

**Verdict — both justification legs are FALSE, and they were measurable before the ADR was written.**

1. **Attributability.** `{Roles:[""]}`, `{Attributes:{"x":nil}}` and `{Attributes:{"":""}}` all pass
   and are **exactly as** durably-unattributable and `AssignedTo`-invisible as `Actor{}`. Worse,
   the shape the record *deliberately blesses* — the kiosk claimant `{Roles:["kiosk"]}` — is
   itself unattributable. So unattributability **cannot** be the harm that distinguishes `Actor{}`,
   because the rule's headline exemption has the same property. §3.3's argument refutes its own
   exemption.
2. **The attribute fail-open.** The rule closes it in **1 of 8** shapes — and the one it closes is
   the one that carries the key, which the rule has nothing to do with. `{ID:"alice"}` — a
   perfectly ordinary authenticated actor — still ALLOWs the deny-list predicate. §3.3's claim that
   round 2's acceptance *"reopened the fail-open §3.5 closes"* is true only of the single shape
   `Actor{}`, and §3.5's own honest sentence (*"5 of 6 shapes still ALLOW"*) already says the fix
   is not closure. The two sections are arguing opposite things about the same measurement.
3. **Bonus incoherence, measured:** `Roles: []string{}` (non-nil, empty) is **REFUSED** while
   `Roles: []string{""}` (one empty string) is **ADMITTED**. `len()` cannot tell a dimension from
   a placeholder, and the difference between those two literals is invisible in any consumer's
   resolver code.

This is a **local defect** (the rule's stated rationale), not an inter-fix hole — but it is
Critical because §3.3 is the whole of the surviving decision's refusal semantics, it was reversed
once already in each direction, and round 3 is the last audit. Shipping it means shipping a
security rule whose recorded justification does not survive its first probe.

**Concrete fix — pick ONE and say which property it buys:**
- (a) **Keep the rule, fix the rationale.** The only property it actually has is *"the resolver
  returned something rather than nothing"* — a **liveness/wiring** check that catches
  `actor, _ := authenticate(r)`, and nothing else. Rewrite §3.3's two-leg justification down to
  that one sentence, delete the attributability and fail-open legs, and add the measured table
  above to §2 as the evidence. Add §5 rows pinning `{Roles:[""]}` and `{Attributes:{"x":nil}}` as
  **admitted**, so nobody later "tightens" the rule believing it is a security boundary.
- (b) **Make it mean what §3.3 says** — require a non-empty `ID` **or** a non-empty role string —
  which restores the kiosk shape and refuses the placeholder shapes. Then `{Attributes:{x:nil}}`
  is refused and leg 1 becomes true. Leg 2 stays false either way and must be deleted.

⚠ Whichever is chosen, **delete the sentence *"round 2's fix reopened the fail-open §3.5 closes"***.
Measured, §3.5's fix is reopened by 7 of the 8 shapes, six of which this rule admits.

---

### F4 — the guard validates serialisability but the durable record is NOT what the authorizer evaluated; five value classes diverge silently — [MINOR]

**Bundle text attacked:** spec §3.5's guard, and ADR-0147's *"human-task audit records render
actors by faithful passthrough"* (`authz/authz.go:30-33`).

**What I ran:** each attribute shape through the verbatim guard, then through
`htActorRemainder` marshal→unmarshal (what `claim_actor` actually stores).

```
PROBE json.RawMessage valid       guard=ADMITTED  stored=map[x:map[k:1]]                 ⚠ STORED != EVALUATED
PROBE custom MarshalJSON          guard=ADMITTED  stored=map[x:REDACTED]                 ⚠ STORED != EVALUATED
PROBE uint64 max                  guard=ADMITTED  stored=map[x:1.8446744073709552e+19]   ⚠ STORED != EVALUATED
PROBE int64 max                   guard=ADMITTED  stored=map[x:9.223372036854776e+18]    ⚠ STORED != EVALUATED
PROBE invalid UTF-8 in KEY        guard=ADMITTED  stored=map[a<U+FFFD>:EVIL]             ⚠ STORED != EVALUATED
                                  (two distinct keys "a\xff" and "a\xfe" COLLAPSE to one)
PROBE invalid UTF-8 in VALUE      guard=ADMITTED  stored=map[x:tok<U+FFFD>en]            ⚠ STORED != EVALUATED
```
Confirmations (the guard and the store agree, no gap): NaN, +Inf, cyclic pointer struct, invalid
`json.RawMessage`, `chan int` — all rejected by both.

**Verdict:** MINOR and honestly pre-existing (§2.9 is right that embedded consumers already persist
attributes), but the bundle presents the round-trip guard as making the flow safe, and what it
guarantees is *decodability*, not *fidelity*. `int64` precision loss and the UTF-8 key collapse
mean the audit record can differ from the value the authorizer decided on — in a record whose
stated purpose is audit.

**Concrete fix:** one sentence in §3.5 and one residual in §4: *"the guard proves the attributes
decode, not that they decode to the same values; integer precision, custom `MarshalJSON`, and
invalid UTF-8 in keys or values change on the way to the audit column."* No code change needed.

---

### F5 — tests A and B are both TRUE as stated; but test B as PRESCRIBED can silently pass for the wrong reason, and the plan does not close that — [MAJOR] (with two CONFIRMATIONS)

**Bundle text attacked:** plan Task 3: *"test A: a ctx-**honouring** resolver that blocks past the
bound ⇒ `ErrIdentityUnavailable`; test B: a ctx-**ignoring** resolver ⇒ pinned as **still
succeeding**, documenting the narrowed guarantee."* Spec §5 row 12 and §4 residual 5.

**What I ran:** `resolveRequestActor` verbatim (plan Task 4 Step 3), bound 200 ms, resolver sleep
1500 ms.

```
PROBE test A (ctx-HONOURING, bound=200ms): elapsed=202ms err=…identity unavailable: context deadline exceeded  isIdentityUnavailable=true
PROBE test B (ctx-IGNORING,  bound=200ms): elapsed=1.502s err=<nil> actor={ID:alice …}
PROBE test B ⇒ the handler was held 1.302s past its 200ms bound and SUCCEEDED
PROBE test B' (ctx-IGNORING, bound=10s DEFAULT): elapsed=1.502s err=<nil> — PASSES for the WRONG REASON
```

**✅ CONFIRMATION (A):** test A is true and the 503 arrives at the bound. **✅ CONFIRMATION (B):**
test B is true — §4 residual 5 (*"a resolver ignoring ctx runs past the bound and succeeds"*) is
**exactly right**, and the spec's refusal to restate the precedent's hedge as a guarantee is
correct. Both survive.

**Verdict — the defect is in HOW test B is prescribed.** The plan states the resolver shape
(ctx-ignoring) but **not the fixture invariant that gives the test discriminating power**: that the
resolver's block must EXCEED the configured bound, and that the test must assert the elapsed time
did. With the **default 10 s** bound, the identical resolver returns in 1.5 s and the assertion
`require.NoError` passes having exercised nothing — line B' above. Round 2's own lesson was *"check
the FIXTURE, not the assertion line"*, and test B's fixture spec is precisely what is missing.
Nor does spec §5 row 12's *"what makes it fail today"* cell answer the question it is titled with —
it says *"round 2's test used a ctx-honouring resolver"*, which is a description of round 2, not a
falsification condition.

**Concrete fix:** plan Task 3, test B must read: *"a ctx-ignoring resolver that sleeps `4×` an
**explicitly configured** short bound (e.g. `WithRequestActorTimeout(50*time.Millisecond)`, sleep
200 ms); assert `NoError` **and** `elapsed >= 4×bound`, so the pin fails if the resolver ever
returns early or if the bound ever starts being enforced."* Fill §5 row 12's cell with:
*"nothing — this is a characterization pin; it fails only if a post-resolve `ctx.Err()` check is
added, which is the change it exists to catch."* Saying that plainly is honest; leaving the cell
looking like a falsification condition is not.

---

### F6 — a client that hangs up mid-request is classified as an identity OUTAGE (503), not a cancellation — [MAJOR]

**Bundle text attacked:** spec §3.3's table, *"the resolver returns an error ⇒ **503**
`ErrIdentityUnavailable`, wrapping it"*, and plan Task 4 Step 3's `case err != nil:` arm, which has
no cancellation branch.

**What I ran:** the verbatim helper with an already-cancelled incoming request context — what
`req.Context()` is after the client disconnects.

```
PROBE client-disconnect ctx: err=workflow-httpcore: identity unavailable: context canceled
                             isIdentityUnavailable=true ⇒ classifies 503
```

**Verdict:** every abandoned request whose resolver honours `ctx` (i.e. every *correctly written*
resolver — the shape the bundle asks consumers to write) is recorded as a **503 identity
outage**. The bundle's stated meaning for 503 is *"the consumer's identity provider is
unavailable"*; a client pressing Stop is neither. This inflates exactly the signal an operator
would page on, and it will be the **dominant** contributor to it under normal traffic. It also
lands on the `ErrIdentityUnavailable` arm that §3.4 deliberately hoisted above every other arm, so
nothing downstream can reclassify it.

**Concrete fix:** add a branch before the generic error arm —
`case errors.Is(err, context.Canceled) && ctx.Err() != nil: return authz.Actor{}, err` — and let
the existing transport-level handling deal with a dead client (the response is unwritable anyway).
Distinguish it from `context.DeadlineExceeded`, which **is** a genuine 503. Add a §5 row: *"a
resolver returning `context.Canceled` because the CLIENT hung up ⇒ not classified 503"*, failing
today because no such branch exists.

---

### F7 — a panicking `RequestActorFunc` is consumer-supplied code in the request path with no recovery, against an established repo convention, and it behaves THREE different ways across the three adapters — [MAJOR]

**Bundle text attacked:** the whole of §3.3/§3.4 and plan Task 4 Step 3 — the bundle enumerates the
resolver's outcomes as *authenticated / unauthenticated / error / (narrowed) hang* and never names
**panic**. §3.3: *"Without it the promise 'never an open door' has an unnamed third state — hang"*.
There is an unnamed **fourth**.

**What I ran:** (a) the verbatim helper with a panicking resolver; (b) a panicking handler through
each adapter's real router shape.

```
PROBE panicking resolver: recovered at the TEST boundary = resolver blew up  (nothing in the bundle recovers it)
PROBE stdlib:        client got TRANSPORT ERROR (no HTTP response): Post "http://…/p": EOF
PROBE gin.Default(): status=500 body=""
PROBE gin.New():     PANIC ESCAPED the router: resolver blew up
```

**The convention it violates, source-verified:** this repo already wraps consumer-supplied code in
`recover`. `action/wrap.go:65-77` — *"Do runs the bare action, recovering a panic into an error"* —
plus `runtime/processdriver_action.go:156`, `internal/expreval/expreval.go:85`,
`definition/model/validate/jsonschema/jsonschema.go:44`. `RequestActorFunc` is the newest
consumer-supplied callback in the repo and the only one on the request hot path, and it is the
only one with no policy.

**Verdict:** three adapters, three outcomes — a dropped connection, a 500, and an escaped panic
that takes down whatever the consumer mounted the router in. `transport/http/parity` exists to stop
exactly this. The bundle's headline promise is *"refuses rather than downgrades"*; a panic is
neither a refusal nor a downgrade, and on `gin.New()` it is a crash.

**Concrete fix:** wrap the call in plan Task 4 Step 3:
```go
a, err := func() (a authz.Actor, err error) {
    defer func() {
        if rec := recover(); rec != nil {
            err = fmt.Errorf("%w: the request-actor resolver panicked: %v", ErrIdentityUnavailable, rec)
        }
    }()
    return resolve(ctx)
}()
```
citing `action/wrap.go`'s precedent, and add §5 row: *"a resolver that panics ⇒ 503, and the
process survives"* — failing today because the panic escapes. Add a `parity` case so the three
adapters are pinned identical.

---

### F8 — CONFIRMATION with a citation correction: the precedent's ctx-cancellation caveat is at `runtime/task/service.go:139`, not `:154` — [MINOR]

**Bundle text attacked:** plan Task 3 and spec §3.3: *"The precedent carries that caveat in its own
godoc (`runtime/task/service.go:154`)"*.

**What I ran:**
```
$ grep -rn "honour ctx cancellation" runtime/
runtime/task/service.go:139:// The resolver's Candidates must honour ctx cancellation for the timeout to take
runtime/processdriver_options.go:78:// The resolver's Candidates must honour ctx cancellation for the timeout to take
$ sed -n '153,158p' runtime/task/service.go
// NewTaskService constructs a TaskService with the given task store, …
```
**Verdict:** the caveat **exists and says what the bundle says** — the substantive claim holds and
is confirmed. The line number is wrong by 15 and points into `NewTaskService`'s godoc. Also, the
caveat appears **twice** (`WithCandidateResolveTimeout` in both `runtime/task` and
`runtime/processdriver_options.go`), which the bundle's singular *"the precedent"* elides.

**Concrete fix:** cite the symbol (`runtime/task.WithCandidateResolveTimeout`'s godoc) rather than
the line — the repo's own standing lesson from *"an audited bundle decays when its base moves"* —
and say "both `WithCandidateResolveTimeout` godocs".

---

### F9 — the removal grid's central claim RE-EXECUTED: the compile half is EXACT, but the member set is **50 lines, not 48** — two unnamed assertions flip 400→401 at Task 6 — [MAJOR]

**Bundle text attacked:** `audit2-0189-removal-grid.md` §"Blast radius": *"⇒ the member set reverts
to spec §2.6's **48 lines / 13 files / 6 packages** … ⚠ **This is a claim, and it must be
re-executed after the re-cut, not assumed**"*; plan's closing line: *"⛔ That reversion is a claim
and Task 5 Step 4 must re-execute it."*

**What I ran — the two-change ablation, both changes modelled**, in this worktree: deleted
`httpcore.Actor` + `ClaimInput.Actor` + `CompleteInput.Actor` + `ReassignInput.By`, and added
`actor RequestActorFunc` as the last parameter of `ClaimTask`/`CompleteTask`/`ReassignTask`.

```
$ go build ./...                                  BUILD_EXIT=1
$ go test -count=1 -gcflags=-e -run '^$' ./...    TEST_EXIT=1
```

**✅ CONFIRMATION — the compile-breaking half re-derives EXACT, line for line:**
```
transport/http/stdlib/groups.go:140,154,168        (3)
transport/http/gin/groups.go:172,192,212           (3)
transport/http/fiber/groups.go:151,168,185         (3)
transport/http/httpcore/dto_test.go:47,62,73,84,153        (5)
transport/http/httpcore/endpoints_test.go:405,422,436,466,485,499,531,560,575  (9)
⇒ 23 unique file:line, 5 files, 4 packages — identical to spec §2.6's table.
[build failed]: httpcore, stdlib, gin, fiber, parity, examples/{production,sqlite,mysql}_wiring
  — parity and the three examples TRANSITIVELY, exactly as §2.6 states.
```
**✅ CONFIRMATION — the runtime half's body-key net re-derives EXACT:** 18 non-`httpcore` lines
(`fiber_test` 563,585,592,615,624 · `gin_test` 413,421,443,453 · `gin_coverage_test`
192,218,244 · `stdlib/errors_test` 155,187 · `stdlib_test` 471 · `stdlib/coverage_test` 92,126 ·
`parity_test` 518) + 5 `httpcore/dto_test` fixtures = **23**. Every line number matches.

**❌ BUT the body-key net is the WRONG NET, and two members escape it.** A test breaks if it calls
a task route **at all** without a resolver — it need not carry an `"actor"` key. Enumerating
`/tasks/` across `transport/**/*_test.go` surfaces two such lines that no bundle document names:

| unnamed member | asserts today | after the bundle | why |
|---|---|---|---|
| `transport/gin/gin_coverage_test.go:183` (`TestTaskRoutes_Claim_BadJSON`, assertion `:184-186`, literal `want 400, got %d`) | **400** on `post(…, "/tasks/tok/claim", "not-json")` with a bare `TaskRoutes{Svc: svc}.Customize(r)` — no resolver | **401** | Task 6 makes the claim decode OPTIONAL ⇒ the 400 never happens ⇒ resolution runs ⇒ no actor |
| `transport/stdlib/coverage_test.go:148` (`TestTaskRoutes_Claim_BadJSON`, assertion `:156-158`, literal `want 400 for bad JSON, got %d`) | **400** on an `errReader{}` body to `/tasks/{id}/claim` via bare `stdlib.Mount(mux, svc)` | **401** | same |

Both are **claim-route only**; the sibling `complete`/`reassign` bad-JSON tests
(`gin_coverage_test.go:209,235`, `stdlib/coverage_test.go:172,196`) keep required bodies and stay
400, so they are correctly **not** members. `fiber` has no bad-JSON task test at all
(`grep -rn 'not-json\|BadJSON' transport/http/fiber/*_test.go` ⇒ no matches), so the asymmetry is
real and per-adapter.

**⇒ the member set is 50 lines / 13 files / 6 packages.** File and package counts are unchanged
(both files are already members at other lines), which is exactly why the totals-based check
missed it — the round-1 lesson *"paste the list, not the total"* applied at the wrong granularity:
§2.6 pasted the list **of one net**.

**Compounding, and this is the operational half:** these two do not break at Task 5, they break at
**Task 6**, whose entire step list is *"POST the claim route with no body ⇒ 200"*. Task 5 Step 5's
instruction — *"confirm the PLANNED red … check it against 23 + parity"* — will therefore reconcile
cleanly and give false assurance, and Task 6's agent meets two unexplained 401s in files it was
never told it owns. Plan Task 8 says stdlib has *"5 runtime pins"* (it has 6) and Task 9 says gin
has *"7"* (it has 8).

**Also: the plan cites the WRONG STEP for its own re-execution instruction.** Task 5 **Step 4** is
`go build ./...` + `go vet ./...` — a compile check that by construction cannot see a runtime
assertion. The member-set reversion the plan orders re-executed at Step 4 is re-executable only at
**Step 5**, and only against the runtime net.

**Concrete fix:**
- Add both lines to spec §2.6's runtime table, retotal to **50 / 13 / 6**, and add the
  `/tasks/` net beside the `"actor"` net as a second derivation — with the note that the body-key
  net cannot see a route call that carries no actor key.
- Move the *"must re-execute the reversion"* instruction from Task 5 Step 4 to **Task 6**, and make
  it `grep -rn '/tasks/' transport/ --include='*_test.go'`, not a total comparison.
- Update Task 8 to 6 pins and Task 9 to 8 pins; add both rewrites explicitly (each must install a
  resolver and then assert what it now means — the claim route no longer 400s on a malformed body).
- Add spec §5 row 14's counterpart per adapter, and note fiber has no such test to migrate.

---

### F10 — §4 residual 8's *"the optional claim decoder swallows EVERY decode error"* is false as written; measured, the 413 survives — [MAJOR]

**Bundle text attacked:** spec §4 residual 8: *"the optional claim decoder swallows **every** decode
error, so a **malformed** claim answers 401 rather than 400"*; §3.8/§3.6's *"ADR-0186's 400/413
responses stay reachable without a credential"*.

**What I ran:** the existing `decodeOptionalRequestBody` route
(`POST /admin/instances/{id}/incidents/{incidentID}/resolve`, `stdlib/groups.go:234`) with a 64-byte
cap, exercising all three failure shapes:

```
PROBE optional route + errReader body      -> status=404  (swallowed; request PROCEEDED to the service)
PROBE optional route + malformed JSON      -> status=404  (swallowed; request PROCEEDED)
PROBE optional route + 510-byte oversize   -> status=413 {"error":"request_too_large",…}
```

**✅ CONFIRMATION — there is NO ADR-0186 regression.** `stdlib/body.go`'s
`decodeOptionalRequestBody` godoc already says it: *"Decode failures stay ignored … but an OVERSIZE
body is still refused with 413. Ignoring that too would leave one route reading an unbounded body
into memory."* Plan Task 6 Step 3 correctly repeats the requirement for gin and fiber. The
413 half of the ADR-0186 contract is safe.

**❌ The residual's quantifier is still wrong, and it is load-bearing.** It swallows the JSON
decode error and the body-read error; it does **not** swallow the oversize refusal, and it does not
swallow the read-deadline path. A reader of §4 — including the agent implementing gin's and fiber's
new helpers, who will read the spec before the plan — is told *every*. This is the exact
recap-overreach class CLAUDE.md's Premise Discipline names, in the residual list the spec introduces
with *"a documented residual is still a shipped defect"*.

**Concrete fix:** *"the optional claim decoder swallows the **JSON-decode and body-read** errors —
not the oversize refusal, which still answers 413 (`stdlib/body.go`'s
`decodeOptionalRequestBody`) — so a **malformed** claim answers 401 rather than 400, while an
**oversize** one still answers 413."* Add spec §5 row 14b: *"an oversize claim body ⇒ 413, not
401"*, per adapter — which is also the only thing that would catch a gin/fiber helper written from
the §4 sentence rather than the Task 6 sentence.

---

### F11 — spec §2.6's runtime sub-header says *"23 lines / 7 files / 4 packages"* over a table listing 8 files and 5 packages; this was an ACCEPTED round-2 MAJOR that was never folded — [MINOR]

**Bundle text attacked:** spec §2.6 `#### Runtime-only — 23 lines / 7 files / 4 packages`.

**What I ran:** counted the table it heads.
```
dto_test 5 + gin_test 4 + gin_coverage 3 + fiber_test 5 + errors_test 2 + stdlib_test 1
  + coverage_test 2 + parity_test 1 = 23 lines   ✓ the LINE count is right
files:    dto_test, gin_test, gin_coverage_test, fiber_test, errors_test, stdlib_test,
          coverage_test, parity_test                                   = 8, not 7
packages: httpcore, gin, fiber, stdlib, parity                         = 5, not 4
```
Neither reading rescues it: as *new* members it would be 7 files but **1** package
(only `parity` is new); as absolute it is 8 and 5.

**Verdict:** `audit2-0189-adjudication.md` §B lists this verbatim as an accepted MAJOR — *"§2.6
internal contradiction: the runtime sub-header says 7 files / 4 packages over a table listing 8
files / 5 packages"* — and it is **still present at `3e96e836`**. An accepted finding that survived
the revision. The **grand** total (48 / 13 / 6) is arithmetically correct from the tables, so
nothing downstream is wrong — but per F9 the 48 is now 50 anyway.

**Concrete fix:** `#### Runtime-only — 25 lines / 8 files / 5 packages` (25 after F9), and retotal
to **50 lines · 13 files · 6 packages**.

---

### F12 — plan Task 1 re-prescribes the EXACT test round 2 mutation-proved vacuous; I ran both mutations and the OUT-clone deletion is still GREEN — [CRITICAL]

**Bundle text attacked:** plan Task 1 Step 1's verbatim `TestActorContextCloneDepth`, whose own
comment reads *"**FAILS WITHOUT THE CLONE**: drop `a.Clone()` in `ContextWithActor` and the
top-level Roles mutation becomes visible"*; and Step 4b: *"⚠ **MUTATION, MANDATORY.** Round 2
**mutation-proved** that the round-1 version of this test stayed GREEN when the OUT clone was
deleted … delete the clone in `ActorFromContext`, **observe RED again**."*

**What I ran:** implemented `authz/context.go` exactly as spec §3.1 and `authz/context_test.go`
exactly as plan Task 1 Step 1 (plus the round-trip / bare-context clause it names). Baseline green.
Then both mutations, `cp`-restored between, `diff`-verified clean.

```
BASELINE_EXIT=0

########## MUTATION A: delete the IN clone (ContextWithActor) ##########
MUTATION_A_EXIT=1
--- FAIL: TestActorContextCloneDepth   context_test.go:40
    expected: []string{"manager"}
    actual  : []string{"admin"}
RESTORED-A-clean

########## MUTATION B: delete the OUT clone (ActorFromContext) ##########
MUTATION_B_EXIT=0
--- PASS: TestActorContextCloneDepth (0.00s)
--- PASS: TestActorContextRoundTrip (0.00s)
ok  github.com/kartaladev/wrkflw/authz  0.416s
RESTORED-B-clean
```

**Verdict:** mutation A is RED, mutation B is **GREEN**. The plan's Step 4b acceptance criterion is
*"If either deletion leaves the suite GREEN, the test is vacuous"* — so **the plan fails its own
mandatory check on the test it prescribes.** And it cannot be otherwise: every prescribed
assertion mutates the **input** (`roles[0]="admin"`, `nested["team"]="hacked"`) and then reads
once. A single read cannot observe whether the read cloned — you need a **second** read after
mutating what the **first read returned**. The plan knows the OUT clone is the contract (§3.1 and
§5 row 1 both say "both directions"), knows round 2 proved this exact test blind to it, and
re-prescribes the same fixture with the mutation step as the only net. That is the repo's
documented failure — *"check the FIXTURE, not the assertion line"* — reproduced in the task written
to prevent it.

Round 2's lesson was recorded as *a guard tested with a fixture from the half that works is not
tested*. Here it is: the fixture exercises the half that works (IN) and the plan's prose asserts
the other half (OUT).

**I verified the corrective fixture works** — added to the same file, it fails under mutation B and
passes clean:
```
CLEAN_EXIT=0
MUTATION_B_WITH_FIX_EXIT=1
    expected: []string{"manager"}   actual: []string{"admin"}
    expected: "gold"                actual: "platinum"
```

**Concrete fix — add this to plan Task 1 Step 1 verbatim, and say it is the OUT-clone fixture:**
```go
// TestActorContextClonesOnTheWayOUT. FAILS WITHOUT THE OUT CLONE: drop a.Clone()
// in ActorFromContext and one reader's mutation reaches the next reader.
func TestActorContextClonesOnTheWayOUT(t *testing.T) {
	t.Parallel()
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{
		ID: "alice", Roles: []string{"manager"}, Attributes: map[string]any{"tier": "gold"}})

	first, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	first.Roles[0] = "admin"
	first.Attributes["tier"] = "platinum"

	second, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"manager"}, second.Roles)
	assert.Equal(t, "gold", second.Attributes["tier"])
}
```
and correct §5 row 1's *"what makes it fail today"* to name **two** fixtures, one per direction —
the single row is what let one direction go uncovered twice.

---

### F13 — the plan's self-review claims §5's attributes row is *"asserted on the object the endpoint builds"*; no such assertion is prescribed and no capturing double exists — [MAJOR]

**Bundle text attacked:** plan's Self-review: *"**Gaps found and closed during this self-review:**
§5 row 7 (attributes reach `service.ClaimTaskRequest`) is asserted in Task 4 **on the object the
endpoint builds**, not on a view — round 1 asserted it on the wrong object and its self-review
wrongly claimed it closed."* Spec §5 row 8: *"attributes reach
`service.ClaimTaskRequest.Actor.Attributes` — asserted **on the request the endpoint builds**."*

**What I ran:**
```
$ sed -n '390,440p' transport/http/httpcore/endpoints_test.go
  … h, svc := transporttest.NewHarness(t, def)          # a REAL service.Service
  … status, body, err := httpcore.ClaimTask(t.Context(), svc, token, tc.in, nil)
$ grep -rn "service.Service = |_ service.Service" internal/transporttest/*.go transport/http/httpcore/*_test.go
  (only `func(_ service.Service)` setup callbacks — no double)
$ grep -rn "^func " internal/transporttest/*.go
  NewHarness, LinearProcess, ApprovalProcess, SignalProcess, MessageProcess, StartedApprovalInstance
```

**Verdict — three defects in one sentence:**
1. **No capturing `service.Service` double exists.** `httpcore`'s endpoint tests drive a *real*
   service through `transporttest.NewHarness`, so `service.ClaimTaskRequest` is never observable.
   Asserting on "the request the endpoint builds" needs infrastructure **no task creates**.
2. **Task 4 does not do it.** Task 4's grid tests `resolveRequestActor`, an unexported helper whose
   return type is `authz.Actor` — not a `service.ClaimTaskRequest`. Its last row is *"a whole actor
   | returned whole, Attributes included"*, which asserts on the **resolver's return**. So the
   self-review's "closed" is the **same wrong-object claim round 1 made**, moved one hop.
3. **It cites the wrong row.** The attributes row is **§5 row 8**; §5 row 7 is *"the two new arms
   co-match each other"*.

**Concrete fix:** either (a) add a capturing double to Task 5 — a `service.Service` stub recording
the `ClaimTaskRequest`, and assert `.Actor.Attributes` on it — naming the file it lives in; or
(b) change §5 row 8 to an **end-to-end** assertion through the real harness (claim with attributes,
then read the task back and assert `Claim.Actor.Attributes`), which the existing infrastructure
does support. Then fix the row number and delete the "gap closed" sentence until it is true.

---

### F14 — the plan's self-review table still maps the THREE REMOVED decisions to tasks, and names §3.5's guard by the round-2 term the re-cut refuted — [MINOR]

**Bundle text attacked:** plan §Self-review:
```
| §3.6 group authentication, Health exempt, placement asymmetry | 8, 9–11 |
| §3.7 opt-in admin role gate                                   | 8, 9–11 |
| §3.5 marshalability pre-check                                 | 4       |
| §2.6 member set — 23 compile + 23 runtime + 2 comments        | 5 (28) · 8–10 (17) · 11 (3) |
```
**What I ran:** read spec §3.5/§3.6/§3.7 at `3e96e836` and plan Tasks 8–11.
**Observed:** spec §3.6 is now *"The claim route accepts an ABSENT body"* and §3.7 is *"Examples and
documentation"*. Route-group authentication, the `HealthRoutes` exemption, the placement asymmetry
and the admin role gate are **decisions C, D and G — removed** (removal grid). Tasks 8–11 are
per-adapter **test migration**; none of them implements group authentication or an admin gate. The
table also carries *separate*, correct rows for the real §3.6 and §3.7, so four rows cover two
sections and two of them are round-2 residue. And *"marshalability pre-check"* is the round-2
name for the guard that round 2 **refuted** (A1); §3.5 now calls it a round-trip guard.

**Verdict:** a stale self-review is how a subagent gets dispatched against a deleted decision. It
also inflates the plan's apparent coverage: two rows claim §3.6/§3.7 are covered by tasks that do
something else. Per F9 the member-set row is also now `23 compile + 25 runtime + 2 comments`.

**Concrete fix:** delete both stale rows; rename the §3.5 row to *"round-trip guard + size bound"*;
retotal the member-set row.

---

### F15 — §2.7's measured RED text is stdlib's, restated per-adapter; fiber's differs — [MINOR] (with a CONFIRMATION)

**Bundle text attacked:** plan Task 6 Step 1: *"POST the claim route with **no body at all** ⇒ 200
… **RED today: `400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}`** —
measured, spec §2.7"*, prescribed *"per adapter"*. Spec §2.7 states it was *"Executed against a real
mounted **stdlib** route"*.

**What I ran:** a bodyless and an empty-object POST to the claim route on **gin** and **fiber**
today.
```
PROBE gin   claim, no body at all    -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
PROBE gin   claim, empty JSON object -> 403 (proceeds to authorization)
PROBE fiber claim, no body at all    -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: bind from body: unexpected end of JSON input"}
PROBE fiber claim, empty JSON object -> 403 (proceeds to authorization)
```
**✅ CONFIRMATION:** §2.7's substance holds on all three adapters — a correctly-migrated client
sending no body gets **400 today**, and `{}` proceeds to authorization. §5 row 13's falsifier is
real everywhere.

**❌** The quoted *message* is stdlib's and gin's; **fiber's is `bind from body: unexpected end of
JSON input`**. An agent following Task 6 Step 1 literally and asserting that body text on fiber
records a false RED (or writes an assertion that fails for the wrong reason). Same class as F5 and
F12: a measurement taken in one context, restated as if general.

**Concrete fix:** Task 6 Step 1: *"RED today: **400** on all three; the message is
`workflow-httpcore: bad input: EOF` on stdlib and gin and `workflow-httpcore: bad input: bind from
body: unexpected end of JSON input` on fiber — assert the **status**, not the text."*

---

## Verdict

| severity | count | findings |
|---|---|---|
| **CRITICAL** | **3** | F1, F3, F12 |
| MAJOR | 7 | F2, F5, F6, F7, F9, F10, F13 |
| MINOR | 5 | F4, F8, F11, F14, F15 |
| **total** | **15** | plus **6 embedded CONFIRMATIONS** (F5×2, F8, F9×2, F10, F15) |

**Criticals per lens (this lens): 3.0.**

### Each Critical, classified per the pre-registered rule

- **F1 — the round-trip guard still bricks the store at depth 9999/10000.** **LOCAL DEFECT — a
  guard.** The guard validates `json.Marshal(a.Attributes)`; the durable document is
  `{"attributes":{…}}` (`store.htActorRemainder`), one object deeper, and `encoding/json`'s
  decoder caps the whole document at 10000. It is wrong on its own terms against a **pre-existing**
  store shape — nothing another fix did caused it. Reproduced end-to-end against a real SQLite
  store with A1's identical failure text.
- **F3 — the dimension rule achieves neither property §3.3 justifies it with.** **LOCAL DEFECT —
  the rule's recorded rationale**, not the rule's mechanics. ⚠ Reported honestly: **leg 2 is a
  cross-reference between the two decisions this revision changed** — §3.3 claims the rule closes
  a fail-open that §3.5's own measured sentence (*"5 of 6 shapes still ALLOW"*) says is not closed.
  I classify it **local** because the fix is to correct §3.3's prose, not to redesign either
  decision, and because §3.5's measurement is itself correct. A reader who wants to count it as an
  inter-fix inconsistency has a defensible case; I am flagging it rather than deciding it.
- **F12 — plan Task 1 re-prescribes the test round 2 mutation-proved vacuous.** **LOCAL DEFECT — a
  test.** Both mutations run: IN-clone deletion RED, OUT-clone deletion **GREEN**. The plan's own
  Step 4b acceptance criterion fails on the plan's own fixture. Corrective fixture written and
  verified RED under the same mutation.

**None of my three Criticals is an inter-fix hole** in the sense the rule names — a fix opening a
hole in another fix. Two are guards/tests that do not cover the half they were written for (F1,
F12) and one is a false rationale (F3). That is the **same root cause round 2 named as its #2**:
*a guard tested with a fixture from the half that works is not tested* — recurring for the third
consecutive round, now in the very artifacts written to stop it.

### What HELD — do not re-litigate

- ⭐ **The compile-breaking member set re-derives EXACT** under the honest two-change ablation: 23
  lines, every line number, 5 files, 4 packages, and `parity` + the three example mains failing
  **transitively** exactly as §2.6 says. The `"actor"`-key runtime net also re-derives exact (23).
  §2.6's method — paste the member set — works; F9 is a defect in the **net**, not the method.
- ⭐ **Both resolver-timeout claims are TRUE as stated.** Test A: 503 at 202 ms against a 200 ms
  bound. Test B: a ctx-ignoring resolver held the handler 1.302 s past the bound and **succeeded**.
  §4 residual 5 is exactly right and the bundle's refusal to restate the precedent as a guarantee
  is correct. F5 attacks how test B is *prescribed*, not the claim.
- ⭐ **No ADR-0186 regression.** `decodeOptionalRequestBody` swallows the decode and body-read
  errors but **not** the oversize refusal — measured 413. Plan Task 6 Step 3 already requires gin
  and fiber to match.
- ⭐ **§2.7 holds on all three adapters** — a bodyless claim is 400 today, `{}` proceeds to
  authorization.
- ⭐ **The marshal arm of the guard is exactly aligned with the store's marshal**: NaN, +Inf, cyclic
  pointer structs, invalid `json.RawMessage` and `chan int` are rejected by both. There is no
  marshal-side gap — the whole gap is on the decode side (F1).
- ⭐ **§3.3's provenance correction is source-verified true**: `humantask/validate.go:24` blesses the
  empty-ID kiosk claimant in those words, and `validate_test.go`'s pinned fixture is
  `Claim{Actor: authz.Actor{Roles: []string{"kiosk"}}}`. The bundle's insistence on *"a resolved
  actor carrying at least one dimension"* over *"an identified principal"* is right.

### Probe hygiene

Every mutation restored from a `cp` backup and `diff`-verified; every probe file deleted.
`git status --short` at the end of this lens: **empty**. No Docker was started; all store probes
ran on `dbtest.RunTestSQLite` (pure Go).
