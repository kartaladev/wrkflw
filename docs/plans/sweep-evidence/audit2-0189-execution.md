# Audit round 2 — EXECUTION lens — ADR-0189 request-actor identity

Worktree: wt2-execution, detached at `37d77a34`.
Bundle verified present (step 0 OK): spec, ADR-0189, plan, author interaction grid.

Findings appended below as they are produced.

### F1 — §2.9's refutation of the ADR-0187 amendment CRITICAL is CORRECT — [CONFIRMATION]

**Bundle text attacked:** spec §2.9 —
> `svc.ClaimTask(ctx, service.ClaimTaskRequest{TaskID: id, Actor: authz.Actor{ID: "alice", Attributes: …}})` → `claim.actor.attributes = map[bearer:tok_secret home_address:...]`
> ⇒ **an embedded consumer already persists actor attributes today, with no ADR-0189.**
> ⇒ **ADR-0187's at-rest classification needs no amendment** … Interaction-lens F11 is **refuted**.

**What I ran:** `service/zzprobe_test.go` (throwaway, deleted) — a real `service.ProcessEngine`
over `kernel.NewMemInstanceStore` + `humantask.NewMemTaskStore`, resolver-populated candidate
carrying **no** attributes, claimant carrying two. `go test -count=1 -v -run '^TestZZProbeClaimActorAttributes$' ./service/...`

**Observed:**
```
EXIT=0
PROBE claim      = {"actor":{"attributes":{"bearer":"tok_secret","home_address":"1 Main St"},"id":"alice","roles":["manager"]},"timestamp":"2026-08-25T10:00:00Z"}
PROBE candidates = [{"id":"alice","roles":["manager"]}]
PROBE humantask  = {"TaskID":…,"Claim":{"actor":{"id":"alice","roles":["manager"],"attributes":{"bearer":"tok_secret","home_address":"1 Main St"}},…}}
--- PASS: TestZZProbeClaimActorAttributes (0.00s)
```
Corroborated at rest: `internal/persistence/store/humantask_store.go:169` `json.Marshal(t.Candidates)`
and `htClaimBinds` marshal `Claim` into `claim_actor`; `internal/atrest/classification.go:200,202,203`
class `claim_actor`, `completion_actor` and `candidates` all `ClassActor`; the generated
`SECURITY.md:125` says *"**`actor`** — identifies a human principal. Treat as personal data."* and
:173-179 already lists `candidates`/`claim_actor`/`completion_actor` as JSONB/JSON/TEXT `actor`
columns. `service/instance_test.go:1226-1231` already pins a resolver-sourced candidate rendering
`attributes` verbatim.

**Why the bundle is right:** the payload class ADR-0189 newly admits over HTTP is already carried
at rest under the same class by two adjacent paths — the embedded `ClaimTask` (proved above) and
`ActorResolver`-populated `candidates` (already pinned by a shipped test). No column is added, no
column changes class, and `TestClassificationPerClassCounts`' per-class totals are untouched.
**Interaction F11 was correctly refuted.** The controller's execution was sound; the round-1
CRITICAL was dismissed on GOOD evidence.

**Concrete fix:** none. ⚠ One wording nit: §2.9 says candidates carry attributes *"whenever the
consumer's `ActorResolver` populated them"* — true, and my probe shows the converse cleanly (a
bare resolver actor renders `[{"id":"alice","roles":["manager"]}]`, no `attributes` key). Keep it.

---

### F2 — §3.5's measured table is CORRECT as printed — [CONFIRMATION]

**Bundle text attacked:** spec §3.5 / ADR Negative —
> | deny-list `actor.Attributes.status != "blocked"` | **ALLOW** ← live fail-open at `main` | **DENY** |
> | allow-list `actor.Attributes.status == "active"` | DENY (vacuously) | satisfiable |

**What I ran:** `authz/zzprobe_test.go` (throwaway, deleted) — `authz.RoleAuthorizer{}.Authorize`
over the full cross-product of {dropped, blocked, active} actors × {nil, empty, blocked} vars ×
5 predicates, including the `vars` root and the lowercase `actor.attributes` spelling.

**Observed (excerpt, EXIT=0):**
```
pred=actor.Attributes.status != "blocked"  actor=dropped  vars-*  -> ALLOW
pred=actor.Attributes.status != "blocked"  actor=blocked  vars-*  -> DENY (workflow-authz: not authorized)
pred=actor.Attributes.status != "blocked"  actor=active   vars-*  -> ALLOW
pred=actor.Attributes.status == "active"   actor=dropped  vars-*  -> DENY (workflow-authz: not authorized)
pred=actor.Attributes.status == "active"   actor=active   vars-*  -> ALLOW
pred=actor.attributes.status != "blocked"  (lowercase)    -> DENY … cannot fetch attributes from authz.Actor (1:7)
pred=vars.status != "blocked"   vars-nil/empty -> ALLOW ;  vars-blocked -> DENY
```

**Why the bundle is right:** both rows reproduce exactly, on both signs, and the verdict is
independent of `vars`. The Go-field spelling (`Attributes`, capital A) is load-bearing — the
JSON-tag spelling errors out — and the bundle uses the correct one throughout. **The round-1
inversion was correctly identified and correctly re-signed.**

**Concrete fix:** none for the table itself. ⚠ But see **F3**, which attacks the *sentence built
on top of the table*, not the table.

---

### F3 — "this CLOSES a live fail-open" is FALSE as a flat claim: ADR-0189 makes the deny-list predicate SATISFIABLE, it does not close the fail-open, and the mechanism IS backlog 103's — [MAJOR]

**Bundle text attacked:** spec §3.5 heading and body —
> ### 3.5 `Attributes` flow, with a marshalability pre-check — **and this CLOSES a hole**
> ⚠ Round 1 called this a **cost**. For the deny-list class — the exact class it was writing about —
> it is a **fix**. The live fail-open is filed as its own backlog item; it is **not** backlog 103,
> which was executed over the `vars` root.

and ADR-0189's matching Positive, and spec §1.2's row
> flowing `Attributes` **CLOSES a live fail-open**.

**What I ran:** `authz/zzprobe2_test.go` (throwaway, deleted) — the *same* deny-list predicate
against actors whose `Attributes` map **exists but does not carry the key**, i.e. the
post-ADR-0189 world with a resolver that returns `{ID, Roles}` or `{ID, Roles, dept}`.

**Observed:**
```
EXIT=0
PROBE Attributes present, key ABSENT (post-0189)    -> ALLOW  <-- fail-open
PROBE Attributes present, key EMPTY STRING          -> ALLOW  <-- fail-open
PROBE Attributes present, key nil                   -> ALLOW  <-- fail-open
PROBE empty (non-nil) Attributes map                -> ALLOW  <-- fail-open
PROBE nil Attributes (today over HTTP)              -> ALLOW  <-- fail-open
PROBE Attributes present, status=blocked            -> DENY (workflow-authz: not authorized)
--- PASS: TestZZProbeDenyListAfterAdr0189 (0.00s)
```

**Why the bundle is wrong — three ways:**

1. **The fail-open survives ADR-0189 in five of six shapes.** The predicate denies only when the
   resolver populates `status` **with the literal value `"blocked"`**. ADR-0189 removes exactly one
   of the two ways the key goes missing — the transport dropping the whole map — and leaves the
   other, the consumer's resolver simply not populating that key, completely untouched. The bundle
   imposes **no obligation whatsoever** on what a `RequestActorFunc` returns; spec §3.3's own
   refusal table accepts `Actor{ID: ""}`, so an actor with *no attributes at all* is an explicitly
   blessed shape. ⇒ "CLOSES a live fail-open" is a claim about a **capability**, restated as a
   claim about a **state**. It is the identical shape as ADR-0187's parked-residual lesson
   (*"a parked residual is not a safe residual"*), inverted: a *fixed* hole that is only fixable.

2. **The distinction from backlog 103 is a distinction of ROOT, not of MECHANISM, and the bundle
   asserts the stronger one.** Row `vars.status != "blocked"` with `vars-nil` and `vars-empty`
   returns **ALLOW** (F2's log) — byte-for-byte the same behaviour as `actor.Attributes.status`
   with the key absent. Both are `expr`'s missing-key→`nil`, `nil != "blocked"`→`true`. Saying it
   is *"**not** backlog 103, which was executed over the `vars` root"* is true only of the
   *measurement's* root; a reader files a second backlog item and a second fix, and the two fixes
   are the same fix. This is precisely the error the adjudication's root cause 3 names — *"a
   measurement carried across roots"* — committed a second time, in the opposite direction:
   round 1 carried a `vars` result onto `actor` and got the sign wrong; the revision now insists
   the two roots are unrelated when the underlying defect is one defect.

3. **The consequence is signed for the wrong counterfactual.** §3.5's "today" column measures
   *the HTTP path with attributes dropped*. But the sentence in §1.2 and the ADR reads as a claim
   about the change's net effect. Under owner decision D-1's rejected alternative — flow
   `{ID, Roles}` only — the deny-list row is **ALLOW / ALLOW**, i.e. unchanged; under D-1 as
   chosen it is **ALLOW / ALLOW-unless-the-resolver-says-blocked**. Neither is "closed".

**Concrete fix:**
- Retitle §3.5 to *"`Attributes` flow, with a marshalability pre-check — this makes actor-attribute
  predicates SATISFIABLE"* and replace the two "CLOSES a live fail-open" sentences (spec §3.5,
  spec §1.2 row 3, and the ADR's matching Positive) with the measured, quantified form:

  > Today every `actor.Attributes.*` predicate sees a nil map over HTTP, so the deny-list class
  > **allows unconditionally**. After this record it allows or denies **according to what the
  > consumer's `RequestActorFunc` returned**. ⚠ It is **not closed**: measured, the predicate
  > still ALLOWs for an actor whose `Attributes` map is absent, empty, or missing the key
  > (5 of 6 shapes) — this record imposes no obligation on the resolver's payload.
- Add a third row to §3.5's table: `deny-list, resolver omits the key | ALLOW | ALLOW` — the row
  that makes the residual unmissable.
- Rewrite the backlog-filing sentence to say the new item is **backlog 103's mechanism at the
  `actor` root**, and that a fix for one is a candidate fix for the other (both are `expr`'s
  missing-key→nil under a `!=` predicate) — rather than asserting they are different.
- Add to §4 Residuals: *"an `actor.Attributes` deny-list predicate still fails open whenever the
  consumer's resolver does not populate the key; this record makes the predicate satisfiable, not
  safe."*

### F4 — the marshalability pre-check DOES NOT prevent the brick it exists to prevent: `encoding/json`'s ENCODER has no depth limit, its DECODER has a hard one — [CRITICAL]

**Bundle text attacked:** spec §3.5 —
> **The pre-check:** the seam rejects an actor whose `Attributes` do not `json.Marshal`, with
> `ErrBadInput` (400). Without it a `chan int` attribute **permanently bricks the instance view** —
> fail-open at write, fail-closed forever at read.

and plan Task 4 Step 3's implementation:
```go
if len(a.Attributes) > 0 {
    if _, mErr := json.Marshal(a.Attributes); mErr != nil {
        // Without this, a non-marshalable attribute is written durably and then
        // PERMANENTLY bricks the instance view - fail-open at write, fail-closed
        // forever at read.
        return authz.Actor{}, fmt.Errorf("%w: actor attributes are not JSON-serialisable: %w", ErrBadInput, mErr)
    }
}
```
and plan Task 4 Step 1's test table row `| Attributes containing chan int | ErrBadInput |`,
and spec §5 row 8 *"a non-marshalable attribute ⇒ 400, and the instance view still renders"*.

**What I ran — two probes.**

(1) `/tmp/zz_marshal_probe.go`, a standalone `go run` over ten candidate values, printing whether
`json.Marshal` (the pre-check) accepts and then whether `json.Unmarshal` (every read path) accepts:

```
PRECHECK-REJECT chan int                  err=json: unsupported type: chan int
PRECHECK-REJECT func                      err=json: unsupported type: func()
PRECHECK-REJECT NaN                       err=json: unsupported value: NaN
PRECHECK-REJECT +Inf                      err=json: unsupported value: +Inf
PRECHECK-REJECT cyclic pointer struct     err=json: unsupported value: encountered a cycle via *main.cyc
PRECHECK-REJECT cyclic map                err=json: unsupported value: encountered a cycle via map[string]interface {}
PRECHECK-REJECT nested chan (depth 2)     err=json: unsupported type: chan int
PRECHECK-PASS   NUL byte in string        marshal={"s":"a b"}        unmarshal=<nil>
PRECHECK-PASS   invalid UTF-8             marshal={"s":"��"}    unmarshal=<nil>
PRECHECK-PASS   20000-deep nesting        marshal={"d":{"n":{"n":...      unmarshal=invalid character '{' exceeded max depth
PRECHECK-PASS   32 MiB string             marshal={"s":"xxxx...           unmarshal=<nil>
```

(2) The end-to-end durable proof, `internal/persistence/store/zzprobe_test.go` (throwaway,
deleted) — a **real `store.HumanTaskStore` over `dbtest.RunTestSQLite` (pure Go, no Docker)**,
upserting a `humantask.HumanTask` whose `Claim.Actor.Attributes` carry each pre-check-passing value,
then reading it back:

```
EXIT=0
PROBE 20000-deep nesting   WRITE OK, READ FAILED: workflow-store: get task tok-zz-20000-deep nesting:
                           unmarshal claim_actor for task tok-zz-20000-deep nesting:
                           invalid character '{' exceeded max depth   <-- BRICKED
PROBE NUL byte in string   WRITE OK, READ OK (claim actor id="alice" attrs keys=1)
PROBE 1 MiB string         WRITE OK, READ OK (claim actor id="alice" attrs keys=1)
--- PASS: TestZZProbeAttributeDurableRoundTrip (0.07s)
```

**Why the bundle is wrong:** the pre-check is **asymmetric with the read path it protects**.
`encoding/json`'s encoder imposes **no nesting limit**; its scanner imposes a hard
`maxNestingDepth = 10000` on *decode*. So a nested-map attribute deeper than 10000 satisfies
`json.Marshal` exactly, is written durably by `htMarshalActorRemainder`
(`internal/persistence/store/humantask_store.go:565`, plain `json.Marshal`), and is then
**unreadable forever** by `htDecodeActor` (`:579`, plain `json.Unmarshal`).

That is not a variant of the failure mode — it is *the* failure mode, verbatim: **fail-open at
write, fail-closed forever at read**, the exact sentence the pre-check's own comment claims to
prevent. And it is **worse than the "instance view" the spec scopes it to**: `HumanTaskStore.Get`
is fail-loud by design ("unlike the list queries it does not degrade around an unreadable audit
column", `humantask_store.go:196-199`), so the poisoned task can never again be fetched, claimed,
completed or reassigned. One request permanently destroys one durable task record.

⚠ **The prescribed test cannot detect this.** Plan Task 4's only pre-check case is `chan int`,
and spec §5 row 8 asserts "a non-marshalable attribute ⇒ 400" — both test the arm that *works*.
A test whose fixture is `chan int` proves nothing about a fixture the pre-check admits. This is
the repo's *"a CITED test is not a COVERING test"* / *"check the fixture, not the assertion"*
lesson, live in a prescribed test.

**Concrete fix — the pre-check must round-trip, not merely encode:**
```go
if len(a.Attributes) > 0 {
    encoded, mErr := json.Marshal(a.Attributes)
    if mErr != nil {
        return authz.Actor{}, fmt.Errorf("%w: actor attributes are not JSON-serialisable: %w", ErrBadInput, mErr)
    }
    // encoding/json's ENCODER has no nesting limit; its DECODER caps at 10000.
    // Marshal alone therefore admits a value that every read path will refuse
    // forever - measured: a 20000-deep attribute writes and then fails Get with
    // "invalid character '{' exceeded max depth". The pre-check must exercise
    // the DECODE side, which is the side that fails.
    var back map[string]any
    if uErr := json.Unmarshal(encoded, &back); uErr != nil {
        return authz.Actor{}, fmt.Errorf("%w: actor attributes do not survive a JSON round trip: %w", ErrBadInput, uErr)
    }
}
```
- Add to plan Task 4's table a case **`Attributes nested 20000 deep ⇒ ErrBadInput`**, and state
  in the plan what makes it fail today: *"`json.Marshal` accepts it; the round-trip guard is what
  rejects it. Mutation: delete the `Unmarshal` leg and the case must go GREEN-to-RED."*
- Rewrite spec §5 row 8 to name **both** fixtures — `chan int` (encode-side) and 20000-deep
  (decode-side) — because a single row inviting one fixture is how round 1 shipped this.
- ⚠ Also correct the scope word in §3.5 and in the code comment: the brick is not confined to
  "the instance view"; `HumanTaskStore.Get` is fail-loud, so the **task record itself** becomes
  permanently unreadable.

---

### F5 — the SIZE BOUND the adjudication named as a required mitigation was silently dropped; a 32 MiB attribute passes every gate — [MAJOR]

**Bundle text attacked:** `audit-0189-adjudication.md` §D-1, the owner decision the revision
implements —
> **Recommendation: CUT IT**, and give the `Attributes` flow its own ADR carrying its own
> mitigations (**a size bound**, a marshalability pre-check, the ADR-0187 reclassification, and a
> position on the unauthenticated read routes).

and spec §1.2's row recording the owner's contrary choice —
> flowing `Attributes` **CLOSES a live fail-open**; **a marshalability pre-check is added** |
> owner D-1 = keep.

**What I ran:** probe (1) above, last row, plus probe (2)'s `1 MiB string` row.

**Observed:**
```
PRECHECK-PASS   32 MiB string   marshal={"s":"xxxxxxxx...   unmarshal=<nil>
PROBE 1 MiB string   WRITE OK, READ OK (claim actor id="alice" attrs keys=1)
```

**Why the bundle is wrong:** the owner kept `Attributes` **in this bundle** rather than deferring
them to their own ADR, which means the four mitigations the adjudication attached to that decision
come with it. Three arrived — the marshalability pre-check (§3.5), the ADR-0187 question (§2.9,
correctly refuted per **F1**), and a position on the unauthenticated read routes (§3.6). **The size
bound did not, and nothing in the spec, ADR or plan mentions it.** Round-1 failure-modes F4 /
execution F6 ("unbounded durable writes... ADR-0186's body cap no longer bounds this path — the
attributes arrive from the consumer's resolver, not the body") is therefore **accepted and then
un-implemented**, with no adjudication saying it was dropped. Per CLAUDE.md's Delivery Gate,
*silence is not an adjudication*.

Note the interaction with **F4's** fix: a round-trip pre-check bounds *depth*, not *size*. The two
are independent limits and both are missing.

**Concrete fix:** either
- (a) add `CustomizeConfig.MaxActorAttributeBytes` (default: reuse `MaxBodyBytes`' default so the
  actor path is bounded by the same order as the body path), enforced on `len(encoded)` inside the
  same pre-check, with `ErrBadInput`; or
- (b) explicitly adjudicate it into §4 Residuals with the reason and a backlog item — e.g.
  *"`Attributes` are unbounded in size; ADR-0186's cap does not reach this path because the payload
  comes from the consumer's resolver, not the request body. A consumer whose resolver copies an
  unbounded token claim can write an arbitrarily large `claim_actor`. Filed as backlog NNN."*

⚠ Do not leave it unstated in either direction — that is the shape ADR-0186 was penalised for at
`/code-review` (*"a residual you wrote down is still a defect you shipped"* — and one you did NOT
write down is worse).

---

### F6 — the pre-check is TOCTOU against §3.1's own one-level clone — [MAJOR]

**Bundle text attacked:** spec §3.1 —
> ⚠ **The isolation guarantee is ONE LEVEL DEEP, and this record says so.** `Actor.Clone`'s own
> godoc states `Attributes` are cloned one level: a nested map or slice inside an attribute value
> stays shared.

against spec §3.5's pre-check and plan Task 4's `resolveRequestActor`.

**What I ran:** `authz/authz.go:49-57` — the contract is `a.Attributes = maps.Clone(a.Attributes)`,
a shallow copy — combined with the F4 probe's demonstration that a **nested** value alone decides
round-trippability (`nested chan (depth 2)` is rejected only because Marshal walks into it; the
20000-deep case shows the walk's result is not the same as the decoder's verdict).

**Observed / derived:** `resolveRequestActor` calls the consumer's `RequestActorFunc`, then
`json.Marshal`s the returned `Attributes`. Both the value it validates and the value later written
durably are **the same nested objects** — `Actor.Clone` copies only the top-level map. So for a
consumer whose resolver hands back a map it retains a reference to (a cached identity object, a
pooled claims map, a struct shared with a background refresh goroutine), the sequence
*validate → ...engine... → durable `json.Marshal`* has a window in which the nested value can change
to a non-marshalable or non-round-trippable one. The pre-check then certifies a value that is not
the value written.

This is not hypothetical for the resolver shape the bundle actively encourages:
`examples/authenticated_tasks` (plan Task 16) is prescribed to build an actor from a verified
credential, and the natural implementation caches the parsed identity per session.

**Why the bundle is wrong:** §3.1 states the one-level clone as an *honesty* note about caller
mutation reaching the engine, and §3.5 states the pre-check as a *guarantee* about marshalability.
Neither section references the other, and together they do not compose: a guarantee validated on a
shared object is a guarantee about a moment, not about a value. The author's interaction grid
covers neither this pair (§3.1 × §3.5) nor the §3.5 × durable-write pair.

**Concrete fix:** make the pre-check the **normalization**, not a test — replace the resolved
`Attributes` with the round-tripped copy, which is deep by construction and is exactly the value
the store will write:
```go
var normalized map[string]any
if err := json.Unmarshal(encoded, &normalized); err != nil { /* ...ErrBadInput... */ }
a.Attributes = normalized   // deep, owned, and provably the value that was validated
```
This closes **F4** and **F6** with one change and costs one allocation on a path that already
marshals. ⚠ State the behavioural consequence in the spec: attribute values are normalized to
their JSON forms (`int` → `float64`, `time.Time` → RFC3339 string, custom `MarshalJSON` types →
their encoded shape) before reaching the engine — which is what the durable record has always held
anyway, so it makes the in-memory value agree with the at-rest one. ⚠ It also **supersedes spec
§3.1's one-level-clone honesty note for `Attributes`**, so §3.1 and plan Task 1's
`TestActorContextCloneDepth` must be re-derived together rather than left contradicting §3.5.

---
### F7 — §3.3's fiber premise is TRUE: `c.Context()` is literally `context.Background()` — [CONFIRMATION]

**Bundle text attacked:** spec §3.3 —
> on fiber that is not hypothetical: `c.Context()` is `context.Background()` when no middleware
> set one, so the resolver runs with no deadline and no cancellation at all.

and plan Task 3's repetition of the same sentence.

**What I ran:** `transport/http/fiber/zzprobe_test.go` (throwaway, deleted), a real
`fiberlib.New()` app + `app.Test(httptest.NewRequest(...))`, asserting **pointer identity** against
`context.Background()` — not merely "it has no deadline".

**Observed:**
```
EXIT=0
PROBE bare        c.Context()==context.Background() -> true
PROBE bare        type=context.backgroundCtx  Done()==nil -> true  Deadline set -> false  Err=<nil>
PROBE afterset    c.Context()==context.Background() -> false  value=x
--- PASS: TestZZProbeFiberBareContextIdentity (0.00s)
```

**Why the bundle is right:** identity, not resemblance — fiber v3.4.0 hands back the *singleton*
`context.backgroundCtx`, whose `Done()` is `nil`, so a `select` on it blocks forever and
`context.WithTimeout` on it is the only deadline in the entire chain. The premise is exact and the
timeout decision it motivates is correctly motivated.

⚠ **One consequence the bundle does not state and should:** because `Done() == nil`, on fiber
there is **no client-disconnect cancellation for the service call either** — `httpcore.ClaimTask`'s
`svc.ClaimTask(ctx, …)` runs uncancellable. The resolver timeout will therefore be the *only*
bound on a fiber request after this ships, which is worth one sentence in §4 Residuals so nobody
reads "a resolver timeout was adopted" as "fiber requests are now bounded".

**Concrete fix:** none for the premise. Add the residual sentence above.

---

### F8 — the resolver timeout does NOT remove the "hang" state, and the bundle drops the very caveat its cited precedent carries in its own godoc — [MAJOR]

**Bundle text attacked:** spec §3.3 —
> ⚠ **A resolver-call timeout is adopted**, mirroring `WithCandidateResolveTimeout` (default 10 s,
> non-positive disables). **Without it the promise *"503, never an open door"* has an unnamed third
> state — hang** — and on fiber that is not hypothetical…

plan Task 3's step-1 case *"a resolver that blocks past the timeout ⇒ `ErrIdentityUnavailable`"*,
plan Task 4's table row *"resolver blocks past the timeout | `ErrIdentityUnavailable`"*, and plan
Task 3's ⚠ note *"without a bound the resolver has no deadline and no cancellation and '503, never
an open door' gains a third state: hang."*

**What I ran:** `transport/http/httpcore/zzprobe_test.go` (throwaway, deleted) containing
`resolveRequestActor` **verbatim from plan Task 4 Step 3** (sentinels renamed to locals so it
compiles against today's `httpcore`), driven by two resolvers — one that selects on `ctx.Done()`,
one that does not — with a 100 ms timeout.

**Observed:**
```
EXIT=0
PROBE honours-ctx   elapsed=101ms  err=workflow-httpcore: identity unavailable: context deadline exceeded  identity=true
PROBE ignores-ctx   STILL BLOCKED after 1s, timeout was 100ms  <-- the HANG the timeout claims to remove
PROBE ignores-ctx   elapsed=3.001s  err=<nil>
--- PASS: TestZZProbeResolverTimeout/resolver_HONOURS_ctx (0.10s)
--- PASS: TestZZProbeResolverTimeout/resolver_IGNORES_ctx (3.00s)
```

**Why the bundle is wrong — and it is wrong in a way the repo has already written down.**

1. **The claim over-reaches.** `resolveRequestActor` calls `resolve(ctx)` **synchronously**.
   `context.WithTimeout` cancels a context; it cannot unblock a function that never reads it. The
   "unnamed third state — hang" is removed **only for resolvers that honour `ctx`**, which is
   precisely the population that would not have hung. Measured: a resolver ignoring `ctx` blocks
   the handler for its full 3 s against a 100 ms bound. The prescribed test (a blocking resolver
   that *does* select on `ctx.Done()`) passes, so **nothing in the plan can detect this** — the
   fixture is chosen from the half that works, the same shape as **F4**.

2. **The precedent it names carries the caveat one line away, and the bundle strips it.** Both
   sibling options state it explicitly in their own godoc:
   - `runtime/processdriver_options.go:79-81` (`WithCandidateResolveTimeout`): *"The resolver's
     `Candidates` **must honour ctx cancellation for the timeout to take effect**; a timed-out
     resolution fails the step before anything is committed…"*
   - `runtime/processdriver_options.go:56-58` (`WithActionTimeout`): *"The action's `Do` **must
     honour ctx cancellation for the timeout to take effect**…"*

   This is the repo's documented lesson verbatim — *"a measurement/claim inherited from a sibling
   context must be re-derived in the target context"*, and *"restating strips the hedge; the
   sentence stops looking contingent and nobody checks it again."* The bundle inherits the
   mechanism and discards the hedge that the mechanism's own author wrote.

3. **A resolver that ignores `ctx` and then SUCCEEDS is admitted late, not refused.** The
   `ignores-ctx` row returned `err=<nil>` with the actor after 3 s. Nothing re-checks `ctx.Err()`
   after `resolve` returns, so the request is served on an identity resolved past its own deadline.
   For a resolver whose backing directory is *slow* rather than *unresponsive* this is the common
   case, and it is not one of §3.3's four rows.

**Concrete fix:**
- Add the caveat to `WithRequestActorTimeout`'s godoc, in the *same words* the two siblings use:
  *"The `RequestActorFunc` must honour ctx cancellation for the timeout to take effect."*
- Correct §3.3 and plan Task 3's note. The true sentence is: *"the timeout bounds a
  cancellation-honouring resolver and converts its expiry into a 503. A resolver that ignores
  `ctx` still hangs the handler — measured: 3 s against a 100 ms bound — and that residual is
  stated, not designed away."* Add it to §4 Residuals.
- Add a **second** prescribed case to plan Tasks 3 and 4: *"a resolver that does NOT select on
  `ctx.Done()` blocks past the timeout — pinned with a short sleep, asserting the elapsed time
  exceeds the bound, so the residual is a contract and not a surprise."* State what makes it fail
  today: nothing — it is a characterization test, and it must be labelled as one.
- Decide and state the leg-3 behaviour: after `resolve` returns, either re-check `ctx.Err()` and
  return `ErrIdentityUnavailable` for a late success (fail-closed, consistent with the design's
  posture), or document that a late success is honoured. Silence here is the third refusal-table
  row that does not exist.

---
### F9 — §2.7's bodyless-claim RED is TRUE on all three adapters, but the plan restates a stdlib-only ERROR STRING as the per-adapter RED and it is WRONG for fiber — [MINOR]

**Bundle text attacked:** spec §2.7 —
> Executed against a real mounted `stdlib` route:
> ```
> no body at all    -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
> ```

and plan Task 6 Step 1, which promotes that one adapter's measurement to all three:
> **Step 1 — failing test, per adapter:** POST the claim route with **no body at all** ⇒ 200
> (with an authenticated resolver). **RED today: `400 {"error":"bad_request","message":
> "workflow-httpcore: bad input: EOF"}`** — measured, spec §2.7.

**What I ran:** three throwaway probes (all deleted) — `transport/http/stdlib/zzbody_test.go`
(real `stdlib.Mount` on a `http.ServeMux` + `transporttest.NewHarness`/`StartedApprovalInstance`),
`transport/http/gin/zzbody_test.go` (real `ginadapter.Mount` behind `httptest.NewServer`),
`transport/http/fiber/zzbody_test.go` (real `fiberadapter.Mount` + `app.Test`). Each posts six /
four body shapes to `POST /tasks/{token}/claim`.

**Observed (EXIT=0 on all three):**
```
stdlib claim  ABSENT (http.NoBody) -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
gin    claim  ABSENT (nil reader)  -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
fiber  claim  ABSENT (nil reader)  -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: bind from body: unexpected end of JSON input"}
```

**Why the bundle is partly wrong:** the *behaviour* (400 on an absent body) re-derives on all three
adapters — §2.7's premise is sound and Task 6 is genuinely necessary three times over. But the
plan pastes stdlib's **exact message text** as the RED an implementing agent should expect on each,
and fiber's differs: `bind from body: unexpected end of JSON input`, not `EOF`. An agent following
the plan literally on fiber sees a message that does not match its brief, which per this repo's own
history is how a "planned red" gets mis-diagnosed as a wiring error.

**Concrete fix:** in plan Task 6 Step 1, replace the single pasted string with the per-adapter
table:

| adapter | absent-body RED today |
|---|---|
| stdlib | `400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}` |
| gin | `400 {"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}` |
| fiber | `400 {"error":"bad_request","message":"workflow-httpcore: bad input: bind from body: unexpected end of JSON input"}` |

and in spec §2.7 change *"Executed against a real mounted `stdlib` route"* to name all three, since
the section is cited by a per-adapter task.

---

### F10 — the prescribed bodyless-claim test PASSES WITHOUT TASK 6's FIX if it is written with the adapter's own `post(…, nil)` helper — a test that cannot fail, and round 1 flagged exactly this and the revision did not carry the warning — [MAJOR]

**Bundle text attacked:** plan Task 6 Step 1 (*"POST the claim route with **no body at all**"*) and
plan Task 13's gin brief, neither of which warns about the helper.

**What I ran:** `transport/http/gin/gin_test.go:42-55` read —
```go
func post(t *testing.T, srv *httptest.Server, path string, body any) httpResp {
	b, err := json.Marshal(body)          // json.Marshal(nil) == []byte("null")
	resp, err := srv.Client().Post(srv.URL+path, "application/json", bytes.NewReader(b))
```
— then the three body-shape probes above, adding `literal null` and `empty object {}` rows.

**Observed:**
```
stdlib claim  literal null      -> 403 forbidden … workflow-authz: not authorized
stdlib claim  empty object {}   -> 403 forbidden … workflow-authz: not authorized
gin    claim  literal null      -> 403 forbidden … workflow-authz: not authorized
gin    claim  empty object {}   -> 403 forbidden … workflow-authz: not authorized
fiber  claim  literal null      -> 403 forbidden … workflow-authz: not authorized
fiber  claim  empty object {}   -> 403 forbidden … workflow-authz: not authorized
```
versus the absent-body row's **400** in F9.

**Why the bundle is wrong:** `post(t, srv, path, nil)` — the obvious way to write "no body" with
gin's existing helper — sends the four bytes `null`, which is **valid JSON that the REQUIRED
decoder already accepts**. So a Task 6 test written that way:
- is RED today (403, not the 200 it wants) — so the agent will observe a red state and believe the
  cycle worked;
- and is **GREEN after Task 5 alone**, because with a resolver installed `null` decodes to the zero
  `ClaimInput` and the endpoint proceeds. ⇒ **Task 6's optional-decode change could be omitted
  entirely for gin and the prescribed test would still pass.**

This is the repo's *"a matching line of test text proves nothing about whether an assertion can
fail — check the FIXTURE, not the line"* rule, and it is the specific trap round 1's execution lens
already reported. The adjudication accepted A3 (the optional body) and the revision implemented
§3.8, but **the warning about how the test must be built was not carried into the plan** — a
warning lost between rounds is the same class of loss as an accepted-then-unimplemented mitigation
(**F5**).

**Concrete fix:** add to plan Task 6 Step 1, verbatim:

> ⛔ **Do not use gin's `post(t, srv, path, nil)` helper for this case.** It `json.Marshal`s nil to
> the literal `null`, which the REQUIRED decoder already accepts — measured, `null` answers 403
> today and 200 after Task 5 alone, so the test would be GREEN without Task 6's fix. Build the
> request by hand with a **nil body reader** (`http.NewRequest(http.MethodPost, url, nil)`) —
> measured to answer 400 today on all three adapters. stdlib's `newPostRequest(t, path, nil)` is
> already correct (it uses `http.NoBody`); gin's `post` is not.

and add the same ⛔ to plan Task 13's gin brief, where the seam/trap tests are written.
Additionally add a **second** prescribed case per adapter — `literal null ⇒ 200` — labelled as the
*characterization* companion, so the two shapes are never conflated again.

---

### F11 — §3.8 contradicts itself: adopting `decodeOptionalRequestBody` on the claim route means a malformed-JSON claim NO LONGER returns 400, which the same section asserts is "unchanged from today" — [MAJOR]

**Bundle text attacked:** spec §3.8, both halves of one section —
> `ClaimInput` becomes zero-field ⇒ the claim route decodes an **optional** body (§2.7).
> …
> ⚠ **Authentication on the three task routes resolves BEHIND the adapter's capped body read.**
> ADR-0186's measured read window (1 MiB / 30 s) and its 400/413 responses stay reachable without a
> credential, and **a malformed-JSON claim returns 400 before 401. Unchanged from today, not a
> regression** — but stated…

and plan Task 6, which names `stdlib`'s existing `decodeOptionalRequestBody` as the mechanism.

**What I ran:** the stdlib probe above, driving **both** decoders on a live mux — the claim route
(required decode) and `POST /admin/instances/{id}/incidents/{incidentID}/resolve`, the one existing
caller of `decodeOptionalRequestBody` (`stdlib/groups.go:234`).

**Observed:**
```
PROBE claim(required)   malformed {          -> 400 {"error":"bad_request","message":"workflow-httpcore: bad input: unexpected EOF"}
PROBE resolve(optional) malformed {          -> 200 {"instance_id":"i-zz",…}
PROBE resolve(optional) ABSENT (http.NoBody) -> 200 {"instance_id":"i-zz",…}
```
Source confirms the mechanism — `stdlib/body.go:167`, `_ = json.NewDecoder(body).Decode(dst) // body is optional` — the helper discards **every** decode error, not only `EOF`.

**Why the bundle is wrong:** §3.8's second paragraph is a statement about the world **after** its
own first paragraph lands, and it is false there. Once the claim route uses the optional decoder,
a malformed claim body is swallowed, the endpoint runs, `resolveRequestActor` refuses, and the
answer is **401 — not 400**. So:
- *"a malformed-JSON claim returns 400 before 401"* → false after this bundle;
- *"Unchanged from today, not a regression"* → false; it **is** a change (and a favourable one:
  one unauthenticated error-oracle response disappears from the claim route);
- *"its 400/413 responses stay reachable without a credential"* → only **413** stays reachable on
  the claim route; the **400** does not. (Both stay on complete/reassign, which keep required
  bodies per §3.8's own scoping.)

This is the residual section stating a *pre-change* fact about a *post-change* world — the same
shape as the parked-residual lesson from ADR-0187 (*"I parked 'two places' as true today,
unguarded; it was already FALSE"*), here inside the very section that introduces the change.

⚠ Second-order consequence the bundle should decide deliberately, not inherit: swallowing decode
errors is defensible for `resolve-incident`, whose body is genuinely optional *content*. On claim
the body is not optional-content but **vestigial**, and silently accepting `{"outcome":"approve"}`
posted to `/claim` (a plausible client bug) as a successful claim is a worse contract than 400.

**Concrete fix:**
1. Rewrite §3.8's residual paragraph to the measured post-change truth:
   > ⚠ Authentication on the three task routes resolves BEHIND the adapter's capped body read, so
   > ADR-0186's read window (1 MiB / 30 s) and its **413** stay reachable without a credential on
   > all three. ⚠ The claim route's **400** does not: its body becomes optional, so a malformed
   > claim body is discarded and the request answers **401**. Complete and reassign keep required
   > bodies and keep their unauthenticated 400.
2. Decide the swallow question explicitly. Recommended: give the claim route a helper that treats
   **only an empty/absent body** as the zero value and still returns 400 for a *present but
   malformed* one — i.e. distinguish `io.EOF` at offset 0 from every other decode error. That
   preserves the client-bug signal, keeps the 413, and is what "optional body" should mean here.
   If instead the existing swallow-everything helper is reused, say so in §3.8 with the reason.
3. ✅ **Verified for the plan:** `decodeOptionalRequestBody` **does** honour the ADR-0186 cap — it
   routes through `requestBodyReader` (`body.go:162`) and returns 413 on `*http.MaxBytesError`
   before touching the decoder. Plan Task 6's ⚠ *"gin and fiber need an equivalent that … still
   honours the size cap"* is correctly scoped: gin's cap lives in `gin/bodycap.go` and fiber's in
   `oversizeBody(cfg, c)` (`fiber/groups.go:144`), so each new helper must call its own adapter's
   guard, not a copy of stdlib's.

---
### F12 — §3.6's refusal must land at **63 handler sites, 33 of them behind a nil-guard**, and the plan gives Tasks 9/10/11 NO member set at all — while Task 5, the non-security task, gets one line by line — [CRITICAL]

**Bundle text attacked:** spec §3.6 —
> `InstanceRoutes`, `MessageRoutes`, `TaskRoutes`, `AdminRoutes` ⇒ **401** when the resolver reports
> no actor. … `InstanceRoutes`, `MessageRoutes` and `AdminRoutes` run the refusal in the **route
> handler, BEFORE the body is decoded**.

and plan Tasks 9/10/11, whose entire implementation instruction is:
> **Step 3 — implement.** ⚠ The refusal runs **in the route handler, BEFORE the body decode**
> for these three groups — unlike the task routes… **Put the reason in a comment at the seam**.

contrasted with plan Task 5, which lists **every one of its 23 lines by file and line number**
"because round 1 conflated the two nets".

**What I ran:** handler-closure counts per group per adapter, derived from the source rather than
from the documents —
```
awk '/^func \([a-z] [A-Za-z]*Routes\) Customize/{grp=$3} /func\(w http.ResponseWriter, req \*http.Request\)/{c[grp]++}' transport/http/stdlib/groups.go
(and the gin `func(gc *ginlib.Context)` / fiber `func(c fiberlib.Ctx) error {` equivalents)
```
plus a structural listing of `AdminRoutes` marking each `if c.X != nil` guard.

**Observed — identical on all three adapters:**
```
stdlib / gin / fiber:
  InstanceRoutes   5 handlers
  MessageRoutes    1 handlers
  TaskRoutes       3 handlers
  AdminRoutes     15 handlers
  HealthRoutes     2 handlers
```
`AdminRoutes`' 15 decompose as **4 always-present** + **11 conditional**:
```
ALWAYS   GET  /admin/instances
ALWAYS   POST /admin/instances/{id}/incidents/{incidentID}/resolve
ALWAYS   POST /admin/instances/{id}/compensation/resolve-stall
ALWAYS   POST /admin/instances/{id}/cancel
c.DeadLetters != nil  -> GET /admin/dead-letters · POST /admin/dead-letters/redrive                      (2)
c.Policies    != nil  -> GET|POST|DELETE /admin/policies · GET|POST|DELETE /admin/role-bindings          (6)
c.RelayStats  != nil  -> GET /admin/relay-stats                                                          (1)
c.Timers      != nil  -> GET /admin/timers                                                               (1)
c.Lineage     != nil  -> GET /admin/instances/{id}/lineage                                               (1)
```

**Why the bundle is wrong:**

- **The site count is (5 + 1 + 15) × 3 adapters = 63 handlers** that must each gain the refusal,
  plus **45** (`AdminRoutes` × 3) that must each additionally gain §3.7's role gate. The plan
  names none of them. Task 5's member set — the one the plan enumerates exhaustively — is
  **23 lines**; the unenumerated security-critical set is nearly three times larger.
- **33 of the 63 sites are behind a `!= nil` guard** and are therefore *invisible in the default
  test fixture*. `transporttest.NewHarness` supplies no `DeadLetters`/`Policies`/`RelayStats`/
  `Timers`/`Lineage`, so an agent following Tasks 9/10/11 — whose prescribed test is only
  *"`AdminRoutes` ⇒ **401** unauthenticated"* — will naturally exercise **one** always-present route
  (`GET /admin/instances`), see GREEN, and leave the other 14 per adapter unverified.
- **A missed site is SILENT and FAIL-OPEN.** `POST /admin/role-bindings` — the route round-1's
  failure-modes F7 named as the reason `AdminRoutes` came into scope at all — is in the
  `c.Policies != nil` block, i.e. in the invisible 11. The bundle's headline justification for
  widening scope sits in the part of the surface its tests do not reach.
- This is the repo's own most recently learned lesson, verbatim from ADR-0188's post-mortem:
  *"two lenses implemented the change it protects, omitted one site, and every guard stayed GREEN,
  fail-open."* ADR-0189 reproduces the setup exactly.

**Concrete fix — three parts, all cheap:**
1. **Paste the member set into the plan**, per adapter, exactly as Task 5 does. 21 route lines per
   adapter, with the five conditional blocks called out by their guard so nobody skips them.
2. **Prefer one enforcement site per group over 21.** Every adapter already funnels registration
   through a single helper — stdlib's `handle(mux, inst, cfg, method, pattern, h)`
   (`stdlib/groups.go:14`), gin's and fiber's `observed(inst, …)` wrappers. Threading a
   `requireActor bool` (or a `handleAuthed` sibling) through that helper makes the refusal
   **structural**: a newly added admin route inherits it, where the per-handler shape means every
   future route is a fresh chance to fail open. That is a strictly better answer to §3.6's own
   worry that *"the next reader collapses [the asymmetry] into one shape"* — a wrapper makes the
   asymmetry a declared property of the group, not a convention repeated 21 times.
3. **Add a machine-checked completeness guard**, in the spirit of ADR-0187's drift test: a table
   test that mounts each group with **every optional dep populated** (a stub `service.PolicyAdmin`,
   `DeadLetterAdmin`, `RelayStatsAdmin`, `TimerAdmin`, `LineageAdmin`) and asserts **every**
   registered route answers 401 unauthenticated. Enumerating the routes from the mux/router rather
   than from a hand-written list is what stops the enumeration rotting — the repo has now rotted
   eleven enumerations in one session and the stated fix is *"a machine-checked invariant, not more
   careful counting."*
   ⚠ **What makes it fail today:** nothing authenticates any group, so it is RED at every route
   before Tasks 9/10/11 and RED at any route an implementer misses afterwards.

---

### F13 — `HealthRoutes`' exemption is sound, but the plan's way of testing *"it must call no resolver at all"* CANNOT FAIL through `MountHealth`, which discards every option — [MAJOR]

**Bundle text attacked:** spec §3.6 —
> **`HealthRoutes` is exempt and calls no resolver** — a load balancer probing `/healthz` has no
> credential.

and plan Tasks 9/10/11 Step 1:
> **`HealthRoutes` ⇒ 200** (exempt, **and it must call no resolver at all**).

and spec §5 row 12, *"… **`HealthRoutes` ⇒ 200**"*.

**What I ran:** `transport/http/stdlib/zzhealth_test.go` (throwaway, deleted) — `MountHealth` with a
real `httpcore.HealthCheckFunc`, then `HealthRoutes{}.Customize` called **directly** with
`stdlib.WithBasePath("/api")` as an observable proxy for "an option reached this group", then
`MountHealth` again with no option.

**Observed:**
```
EXIT=0
PROBE MountHealth      /healthz      -> 200 {"status":"ok"}
PROBE MountHealth      /readyz       -> 200 {"checks":{"db":"ok"},"status":"ok"}
PROBE direct+BasePath  /healthz      -> 404
PROBE direct+BasePath  /api/healthz  -> 200      <- options DO reach HealthRoutes on the direct path
PROBE MountHealth-only /healthz      -> 200
PROBE MountHealth-only /api/healthz  -> 404      <- MountHealth discards options entirely
```
Source confirms: `stdlib/mount.go:26-28`, `MountHealth(mux, checks...)` calls
`HealthRoutes{Checks: checks}.Customize(mux)` — **no `opts` parameter exists on `MountHealth` at
all**, so nothing a consumer configures can reach it.

**Why the bundle is right about the exemption and wrong about how to prove it:**
- The exemption itself is **doubly guaranteed** and better than the spec claims: `HealthRoutes`
  will call no resolver *and*, on the `MountHealth` path, could not receive one if it tried. Worth
  stating, because it makes the exemption structural rather than a convention.
- But the prescribed assertion *"it must call no resolver at all"* is **untestable through
  `MountHealth`**: a test that calls `stdlib.MountHealth(mux, …)` and expects 200 while a
  `WithRequestActor` that `t.Fatal`s is "installed" is **vacuous — the option is discarded before
  `ResolveConfig` ever sees it**, so the test is GREEN whether or not `HealthRoutes` consults a
  resolver. It cannot distinguish the two worlds it exists to distinguish. That is the repo's
  *"check the FIXTURE, not the assertion"* failure, and `MountHealth` is the fixture an agent
  reading Tasks 9/10/11 will reach for first, because it is the API the spec names.
- The **non-vacuous** form is the direct call, which the probe proves does carry options:
  `stdlib.HealthRoutes{}.Customize(mux, stdlib.WithRequestActor(func(context.Context) (authz.Actor, error) { t.Fatal("HealthRoutes must not resolve an actor"); return authz.Actor{}, nil }))`,
  then GET `/healthz` and `/readyz` ⇒ 200 with the resolver never entered.

**Concrete fix:**
- In plan Tasks 9/10/11 Step 1, replace the bare *"HealthRoutes ⇒ 200 (exempt, and it must call no
  resolver at all)"* with the exact fixture, and state what makes it fail: *"install a
  `WithRequestActor` that calls `t.Fatal`; it must never be entered. ⛔ This must be built with
  `HealthRoutes{}.Customize(mux, opts...)` — **not** `MountHealth`, which takes no options and
  would make the test unable to fail (measured: `WithBasePath` reaches the direct call and is
  discarded by `MountHealth`). Mutation: add a `RequireActor` call to the `/healthz` handler and
  the test must go RED."*
- In spec §3.6, add the sentence: *"On the `MountHealth` path the exemption is structural —
  `MountHealth` accepts no `CustomizeOption`, so no resolver can reach `HealthRoutes` at all. On a
  direct `HealthRoutes{}.Customize(mux, opts...)` call options do arrive and are simply unused."*
- ⚠ Note for the counting lens: the same "no options" property means `stdlib.MountHealth` also
  ignores `WithBasePath` today — pre-existing, out of scope for ADR-0189, but it should be filed
  rather than discovered again by the next reader.

---
### F14 — plan Task 1's prescribed test CANNOT FAIL on the OUT clone, which §3.1 and §5 row 1 both name as load-bearing — proved by mutation — [MAJOR]

**Bundle text attacked:** spec §3.1's implementation, whose only inline comment marks the OUT leg —
```go
func ActorFromContext(ctx context.Context) (Actor, bool) {
	…
	return a.Clone(), true   // clone on the way OUT as well as IN
}
```
spec §5 row 1 — *"`ContextWithActor` → `ActorFromContext` round-trips; **both directions clone**"* —
plan Task 1 Step 3 — *"implement exactly the code in spec §3.1 (**clone on the way in and out**)"* —
and plan Task 1 Step 1's test, whose own comment claims to state its failure mode:
> // FAILS WITHOUT THE CLONE: drop a.Clone() in ContextWithActor and the top-level
> // Roles mutation becomes visible.

**What I ran:** built `authz/context.go` **exactly as spec §3.1 prints it** and
`authz/context_test.go` containing `TestActorContextCloneDepth` **verbatim from plan Task 1
Step 1** plus the round-trip half of §5 row 1. Baseline green, then two ablations with a `cp`
backup (never `git checkout`), restoring and `diff`ing between each.

**Observed:**
```
BASELINE                  EXIT=0   --- PASS: TestActorContextCloneDepth
                                   --- PASS: TestActorContextRoundTrip

MUTATION 1 — delete the OUT clone (ActorFromContext returns `a`, not `a.Clone()`):
                          EXIT=0   --- PASS: TestActorContextCloneDepth
                                   --- PASS: TestActorContextRoundTrip     <-- STILL GREEN

MUTATION 2 — delete the IN clone (ContextWithActor stores `a`, not `a.Clone()`):
                          EXIT=1   --- FAIL: TestActorContextCloneDepth
                                   Error: Not equal: expected: []string{"manager"}
                                                     actual  : []string{"admin"}
RESTORED clean (diff empty)
```

**Why the bundle is wrong:** the prescribed test pins **one** of the two clones. Its own comment
is accurate about the IN leg and silent about the OUT leg, and §5 row 1's *"both directions clone"*
is the summary sentence that over-generalises it — the exact recap-overreach shape CLAUDE.md's
Premise Discipline section is about. Deleting the OUT clone leaves the suite green, and per this
repo's standing lesson **"deleted it and the suite stayed green" means UNTESTED before it means
REDUNDANT**.

The OUT clone is not decoration. Without it every `ActorFromContext` call in a request returns the
**same** `Roles` slice and `Attributes` map — shared across the request's goroutines, since a
context is shared — so a handler that appends a role, or the marshalability pre-check's own
normalization (see **F6**), mutates what every later reader and the durable record see.

**Concrete fix — add the discriminating case to plan Task 1 Step 1. I wrote and mutation-verified
it; it is four lines and it fails exactly when it should:**
```go
// TestActorFromContextClonesOut pins the OUT leg. TestActorContextCloneDepth
// mutates its inputs BEFORE retrieval and therefore proves only the IN clone —
// MEASURED: deleting a.Clone() from ActorFromContext leaves it GREEN.
func TestActorFromContextClonesOut(t *testing.T) {
	t.Parallel()
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{
		ID: "alice", Roles: []string{"manager"}, Attributes: map[string]any{"dept": "fin"},
	})
	first, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	first.Roles[0] = "admin"          // mutate what was HANDED BACK
	first.Attributes["dept"] = "hacked"

	second, ok := authz.ActorFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, []string{"manager"}, second.Roles, "a mutation of a RETRIEVED actor must not reach the context")
	assert.Equal(t, "fin", second.Attributes["dept"], "same, for top-level attributes")
}
```
Verified both ways:
```
with the OUT clone   EXIT=0  --- PASS: TestZZActorFromContextClonesOut
OUT clone deleted    EXIT=1  --- FAIL: expected []string{"manager"} actual []string{"admin"}
                                       expected "fin"              actual "hacked"
```
- Add the **mandatory mutation step** to plan Task 1, as Task 2 already has one: *"Step 5 —
  MUTATION: `cp authz/context.go /tmp/`; drop `.Clone()` from `ActorFromContext`; expect EXIT=1
  naming `TestActorFromContextClonesOut`; `cp` back; `diff`."* Task 1 is currently the only
  behaviour-introducing task in the plan with **no** mutation step, and it is the task whose test
  turned out to be half-vacuous.
- Correct §5 row 1's wording, or split it into two rows — one per direction — so the quantifier
  *"both directions"* is carried by two fixtures rather than one.

---
### F15 — spec §5 row 7 is assigned to a task whose SUT CANNOT BUILD the object row 7 says to assert on — round 1's exact defect, repeated one level down, and again declared closed by the self-review — [MAJOR]

**Bundle text attacked:** spec §5 row 7 —
> | 7 | attributes reach `service.ClaimTaskRequest.Actor.Attributes` — asserted **on the request the
> endpoint builds**, not on a view | today they are dropped at all three sites |

and the plan's self-review, which claims it closed:
> **Gaps found and closed during this self-review:** §5 row 7 (attributes reach
> `service.ClaimTaskRequest`) is asserted in **Task 4** **on the object the endpoint builds**, not on
> a view — round 1 asserted it on the wrong object and its self-review wrongly claimed it closed.

**What I ran:** read Task 4's scope — *"**Files:** modify `transport/http/httpcore/endpoints.go`;
create `resolve_actor_internal_test.go`"*, producing `resolveRequestActor(ctx, resolve, timeout)
(authz.Actor, error)` — and its full prescribed table, whose last row is *"a whole actor | returned
whole, **Attributes included**"*. Then confirmed by execution that `httpcore/endpoints_test.go`
uses the **real** `transporttest.NewHarness` service (`endpoints_test.go:433`,
`httpcore.ClaimTask(t.Context(), svc, token, tc.in, nil)`) and that the repo contains **no
`service.Service` mock at all**:
```
$ grep -rln "MockService\b" --include='*.go' .      -> (no matches)
$ grep -n "go:generate.*mockgen" service/service.go -> (no matches)
```
`service.Service` embeds five interfaces (`InstanceStarter` 1, `InstanceReader` 2, `TaskManager` 4,
`Messaging` 2, `InstanceOps` 3 = **12 methods**).

**Why the bundle is wrong:** `resolveRequestActor` returns an `authz.Actor`. It **never constructs
a `service.ClaimTaskRequest`** — that happens in `httpcore.ClaimTask`, which is **Task 5**, and
nothing in Task 5's step list asserts on it. So Task 4's last table row proves only that the
*helper* passes attributes through; row 7's stated object — `service.ClaimTaskRequest.Actor.Attributes`
— is observed by no prescribed test. The self-review sentence is therefore **false in the same
shape it was written to correct**: round 1 "asserted it on the wrong object and its self-review
wrongly claimed it closed"; round 2 assigns it to a task that cannot observe the object and its
self-review claims it closed.

**Concrete fix — I wrote and ran the mechanism; it needs no mockgen and is eight lines.** Move
row 7 to **Task 5** with this fixture:
```go
// capturingSvc records the ClaimTaskRequest the ENDPOINT BUILDS, which is the
// object spec §5 row 7 names. service.Service has 12 methods across 5 embedded
// interfaces and no generated mock; embedding the real harness service and
// overriding one method is the whole cost.
type capturingSvc struct {
	service.Service
	got service.ClaimTaskRequest
}

func (c *capturingSvc) ClaimTask(ctx context.Context, req service.ClaimTaskRequest) (service.ProcessInstance, error) {
	c.got = req
	return c.Service.ClaimTask(ctx, req)
}
```
Verified against today's tree (probe deleted):
```
EXIT=0
PROBE captured ClaimTaskRequest.Actor = {ID:alice Roles:[manager] Attributes:map[]}
--- PASS: TestZZRow7CaptureShape (0.00s)
```
(`Attributes:map[]` is the nil map's `%+v` rendering — the attributes are dropped, which is exactly
the state row 7 must flip.)

**What makes the new test fail today:** `httpcore.Actor` carries no `Attributes` field, so no value
a caller supplies can reach `cap.got.Actor.Attributes`; after Task 5 the resolved actor flows whole
and the assertion inverts. State that in the plan — Task 5 currently states a failure mode for its
401 case and for the `dto_test.go` change, but not for row 7.

---

### F16 — §3.4's *"must precede EVERY arm its payload could match"* is pinned against 3 of the 6 arms, and the co-match pair it names is unreachable from the production path — [MINOR]

**Bundle text attacked:** spec §3.4 —
> ⇒ **an arm whose sentinel wraps caller-supplied errors must precede every arm its payload could
> match.**
> ⚠ **The two new arms co-match EACH OTHER** — an `ErrUnauthenticated` wrapped by a resolver error
> satisfies both.

spec §5 row 5 — *"the 503 arm outranks 404, 403, 400 — one case each"* — and plan Task 2's five
prescribed cases.

**What I ran:** read `transport/http/httpcore/errors.go:34-88` and counted the non-default arms;
read plan Task 4's `resolveRequestActor` control flow.

**Observed:** `ClassifyError`'s ordered switch has **six** non-default arms, not three:

| # | arm | status |
|---|---|---|
| 1 | `kernel.ErrInstanceNotFound` / `ErrDefinitionNotFound` / `humantask.ErrTaskNotFound` | 404 |
| 2 | `authz.ErrNotAuthorized` | 403 |
| 3 | `kernel.ErrConcurrentUpdate` | **409** |
| 4 | `ErrRequestBodyTooLarge` | **413** |
| 5 | `ErrBadCursor` / `ErrBadArmedTimerCursor` / `ErrBadInput` / `validation.ErrInvalidInput` / `ErrInvalidOutcome` / `ErrOutcomeRequired` / `ErrEmptyTriggerKey` / `ErrEmptyReassignTarget` | 400 |
| 6 | `service.ErrConflict` / `engine.ErrInvalidTransition` / `humantask.ErrInvalidTask` | **422** |

Plan Task 2 pins wrapping cases for arms **1, 2 and 5** only. Arms **3 (409), 4 (413) and 6 (422)**
have no case — yet a consumer's resolver hitting its own store can plausibly return
`kernel.ErrConcurrentUpdate` or `service.ErrConflict`, both of which `ErrIdentityUnavailable` would
wrap. So the universal quantifier *"every arm its payload could match"* is asserted and half-tested.
Note the arm the switch's own standing invariant comment was written for (413, `errors.go:44-54`)
is one of the untested three.

Separately, on reachability: Task 4's `resolveRequestActor` handles
`case errors.Is(err, ErrUnauthenticated): return authz.Actor{}, err` **before** the wrap arm, so it
**never** produces an `ErrIdentityUnavailable` wrapping `ErrUnauthenticated`. §3.4's sentence
describes a construction the production path structurally prevents. The test is still correct and
still required by `errors.go`'s standing invariant — it is a *contract* test on `ClassifyError`,
not a reachability test — but the spec should say so, otherwise a future reader deletes it as
testing an impossible state, or (worse) `RequireActor` (Task 8) is written with a different error
shape that *does* produce the pair and nobody notices the difference.

**Concrete fix:**
- Turn plan Task 2's cases into a table over **all six** arms — one `ErrIdentityUnavailable`-wrapping
  case per arm — and correct §5 row 5 to *"the 503 arm outranks all six other arms — one case each"*.
  The extra three cases are three table rows.
- Reword §3.4's co-match note: *"the two new arms co-match each other by construction, so the pair
  is pinned; note that `resolveRequestActor` cannot produce it — it returns an `ErrUnauthenticated`
  BARE — so the test guards `ClassifyError`'s contract and any future producer (`RequireActor`,
  adapter code), not today's path."*
- Add to plan Task 8 the explicit requirement that `RequireActor` uses **the same error shapes as
  `resolveRequestActor`** (bare `ErrUnauthenticated`, wrapped `ErrIdentityUnavailable`), since two
  helpers now produce these sentinels and nothing in the plan says they must agree.

---

### F17 — §2.6's two vacuous 403 pins and §2.3's unknown-key tolerance both re-derive exactly — [CONFIRMATION]

**Bundle text attacked:** spec §2.6 —
> `stdlib/errors_test.go:155` (assertion at **:158**) and `:187` (assertion at **:190**) both assert
> 403 from a `viewer`.

and §2.3 — *"⇒ *ignored, not rejected* is correct for all three."*

**What I ran / observed:**

(a) `sed -n '148,195p' transport/http/stdlib/errors_test.go`. Line 153-155 posts
`{"actor":{"id":"bob","roles":["viewer"]}}` to `/tasks/{token}/complete`; **:158** is
`t.Fatalf("want 403 complete forbidden, got %d …")`. Line 185-187 posts
`{"from":"alice","to":"carol","by":{"id":"bob","roles":["viewer"]}}` to `/reassign`; **:190** is
`t.Fatalf("want 403 reassign forbidden, got %d …")`. **Both line numbers and both assertion
offsets are exact**, and both actors are `viewer`s. The bundle's anchors have not rotted.

(b) Unknown-key tolerance re-run on **real mounted routes** for the two non-obvious adapters
(throwaway probes, deleted):
```
EXIT=0
PROBE gin   unknown-key {"zzunknown":1}                    -> 403 {"error":"forbidden",…}
PROBE gin   unknown-key {"actor":{"id":"x"},"zzunknown":1} -> 403 {"error":"forbidden",…}
PROBE fiber unknown-key {"zzunknown":1}                    -> 403 {"error":"forbidden",…}
PROBE fiber unknown-key {"actor":{"id":"x"},"zzunknown":1} -> 403 {"error":"forbidden",…}
```
An unknown key is ignored and the request proceeds to authorization on both — a 403, never a 400.
`stdlib`'s plain `json.NewDecoder(body).Decode(dst)` (`body.go:143`) is tolerant by construction.
**§2.3's migration premise holds for all three adapters.**

**Concrete fix:** none.

---

### F18 — residual 7 is right about `MountGroups` and incomplete: it also cannot carry `WithAdminRoles`, so §3.7's gate is unreachable through the documented extension seam — [MINOR]

**Bundle text attacked:** spec §4 residual 7 —
> **`httpcore.MountGroups` passes no options**, so groups mounted through it rely on the context
> seam. Workable — the default resolver reads the context — but its godoc must say so.

**What I ran:** `transport/http/httpcore/seam.go:208-212` —
```go
func MountGroups[R any](r R, groups ...RouteCustomizer[R]) {
	for _, g := range groups {
		g.Customize(r)
	}
}
```
There is **no variadic `opts` parameter at all** — options cannot be threaded through even if a
caller wanted to.

**Why the bundle is incomplete:** the residual reasons only about `RequestActor`, where the
context-reading default saves it. But `CustomizeConfig.AdminRoles` (§3.7) has **no safe default** —
its default is "no gate" — so an `AdminRoutes` mounted through `MountGroups` can **never** have the
role gate applied. Since `MountGroups` is documented as the consumer extension seam, §3.7's opt-in
is unreachable for exactly the consumers most likely to be composing custom group sets.

Same for `RequestActorTimeout` (§3.3): a `MountGroups` mount gets the 10 s default and cannot
change it.

**Concrete fix:** widen residual 7 to name all three (`RequestActor` — saved by its default;
`AdminRoles` and `RequestActorTimeout` — **not** configurable through this seam), and either
(a) add `opts ...CustomizeOption[R]` to `MountGroups` and forward them — a two-line, additive,
non-breaking signature change that would make the residual disappear — or (b) state in
`MountGroups`' godoc that a consumer needing `WithAdminRoles` must call `AdminRoutes{}.Customize`
directly. (a) is strictly better and costs one plan bullet in Task 8.

---
## Verdict

### ⛔ The bundle does NOT survive as an input to implementation.

Two Criticals block it. Both are the same failure the bundle was revised to eliminate: **a guard
that does not guard, whose prescribed test uses a fixture from the half that works.**

| severity | n | findings |
|---|---|---|
| CRITICAL | **2** | F4, F12 |
| MAJOR | **9** | F3, F5, F6, F8, F10, F11, F13, F14, F15 |
| MINOR | **3** | F9, F16, F18 |
| CONFIRMATION | 4 | F1, F2, F7, F17 |
| **actionable total** | **14** | |

### The two Criticals

- **F4 — the marshalability pre-check does not prevent the brick it exists to prevent.**
  `encoding/json`'s encoder has **no** nesting limit; its decoder caps at 10000. A 20000-deep
  attribute passes `json.Marshal`, writes durably, and then **`HumanTaskStore.Get` fails forever**:
  measured end-to-end against a real SQLite-backed store —
  `unmarshal claim_actor for task …: invalid character '{' exceeded max depth`. That is §3.5's own
  sentence *"fail-open at write, fail-closed forever at read"*, verbatim, unmitigated — and worse
  than the "instance view" it is scoped to, because `Get` is fail-loud, so the task can never again
  be fetched, claimed, completed or reassigned. **Fix is three lines: round-trip, don't just
  encode** (and per **F6**, keep the round-tripped value, which closes the TOCTOU too).

- **F12 — §3.6's refusal must land at 63 handler sites, 33 of them behind a `!= nil` guard, and
  the plan enumerates none of them.** Measured per adapter: Instance 5 + Message 1 + Admin 15
  (4 always-present + **11 conditional**) + Task 3 + Health 2. Task 5 — the *non*-security task —
  gets its 23 lines listed file-and-line "because round 1 conflated the two nets"; Tasks 9/10/11
  get one sentence. `POST /admin/role-bindings`, the route whose exposure justified widening scope
  at all, sits inside the invisible 11, and `transporttest.NewHarness` supplies none of the
  optional deps, so the prescribed *"AdminRoutes ⇒ 401"* test will be satisfied by one
  always-present route while 14 stay unverified. **A missed site is silent and fail-open** — the
  ADR-0188 post-mortem's exact setup.

### The pattern across the Majors

Five of the nine are the same defect at different sites: **a claim whose supporting fixture cannot
discriminate.**
- **F14** — mutation-proved: deleting the OUT clone leaves plan Task 1's prescribed test GREEN,
  while §3.1 and §5 row 1 both call "both directions clone" the contract. Task 1 is also the only
  behaviour-introducing task with no mutation step.
- **F10** — a bodyless-claim test written with gin's own `post(…, nil)` helper sends the literal
  `null`, which the *required* decoder already accepts. Measured: `null` → 403 today, 200 after
  Task 5 alone ⇒ **Task 6 could be omitted for gin and the test would still pass.** Round 1's
  execution lens flagged this and the warning was not carried into the revision.
- **F13** — the prescribed *"HealthRoutes must call no resolver at all"* is untestable through
  `MountHealth`, which takes **no options at all** (measured: `WithBasePath` reaches
  `HealthRoutes{}.Customize` and is discarded by `MountHealth`).
- **F15** — §5 row 7 is assigned to Task 4, whose SUT returns an `authz.Actor` and never builds a
  `service.ClaimTaskRequest`. This is round 1's own adjudicated defect repeated one level down, and
  the self-review declares it closed in the same sentence that describes round 1 doing so.
- **F4/F16** — the pre-check tested only with `chan int`; the arm-ordering quantifier
  *"every arm it could match"* pinned against 3 of `ClassifyError`'s 6 arms.

The other four are over-claims and a dropped mitigation:
- **F3** — *"this CLOSES a live fail-open"*. Measured: the deny-list predicate still ALLOWs in
  **5 of 6** attribute shapes after ADR-0189; the bundle makes it *satisfiable*, not closed. And
  the *"not backlog 103"* claim distinguishes the **root**, not the **mechanism** — `vars.status`
  with empty vars and `actor.Attributes.status` with the key absent produce byte-identical ALLOWs.
- **F5** — the **size bound** the adjudication named as one of D-1's four required mitigations was
  dropped with no adjudication. A 32 MiB attribute passes every gate; ADR-0186's cap does not reach
  this path.
- **F8** — the resolver timeout removes the hang **only for resolvers that honour `ctx`**.
  Measured: a resolver ignoring `ctx` blocks 3 s against a 100 ms bound and then **succeeds**. The
  precedent it cites, `WithCandidateResolveTimeout`, carries that caveat in its own godoc
  (`processdriver_options.go:79-81`) and the bundle strips it.
- **F11** — §3.8 contradicts itself inside one section: adopting `decodeOptionalRequestBody`
  (which discards **every** decode error — measured: malformed body → 200 on the existing
  resolve-incident route) means a malformed claim answers 401, not the 400 the same section calls
  *"unchanged from today, not a regression"*.

### Round-1 fixes I VERIFIED AS ACTUALLY WORKING

| round-1 item | verdict | evidence |
|---|---|---|
| **A4 / interaction F11 — ADR-0187 needs amending** | ✅ **correctly REFUTED; the CRITICAL was dismissed on GOOD evidence** | **F1**. `svc.ClaimTask` with attributes → `claim.actor.attributes` persisted, executed on a real engine; `claim_actor`/`completion_actor`/`candidates` are all already `ClassActor` (`classification.go:200,202,203`) and already published as such in `SECURITY.md:125,173-179`; a shipped test already pins candidates rendering `attributes` verbatim. No column added, none reclassified. |
| **A1 — §4.2's backlog-103 consequence was INVERTED** | ✅ **the inversion is genuinely fixed; the table is exact on both signs** | **F2**. Full cross-product re-derived independently, including the `vars` root and the lowercase `actor.attributes` spelling (which errors: *cannot fetch attributes from authz.Actor*). ⚠ but the **sentence built on the table** now over-reaches — **F3**. |
| **ex F9 / fm F3 — fiber has no deadline; adopt a timeout** | ✅ **premise exact**, ⚠ **fix partial** | **F7**: `c.Context() == context.Background()` by *pointer identity*, `Done()==nil`. **F8**: the timeout fires and produces 503 for a cooperative resolver (101 ms, `identity unavailable: context deadline exceeded`) but not otherwise. |
| **A3 — the correctly-migrated client is broken (bodyless claim)** | ✅ **diagnosis re-derives on all three adapters** | **F9**: stdlib/gin/fiber all 400 on an absent body. ⚠ fiber's message differs from the one the plan pastes; ⚠ **F10** the prescribed test can be written so it cannot detect the fix. |
| **§2.6's member-set method change; the two vacuous 403 pins** | ✅ **anchors exact, no rot** | **F17**: `errors_test.go:155/:158` and `:187/:190` are exactly as claimed, both `viewer` actors. |
| **§2.3 — a stale `"actor"` body is ignored, not rejected** | ✅ **re-derived on real mounted gin and fiber routes** | **F17(b)**: unknown keys → 403, never 400, on both. |
| **fm F7 / D-3 — admin routes come into scope** | ⚠ **decision sound, execution unscoped** | **F12**: the design is right; the plan cannot deliver it safely as written. |
| **B8 — `Actor.Clone` is one level deep and the round-1 clone test could not detect it** | ⚠ **half fixed** | **F14**: the *nested-sharing* leg is now pinned, but the **OUT clone** the same section introduces is not — mutation-proved GREEN. |

### What I did NOT execute (stated, so nobody reads silence as coverage)

- §3.7's admin role gate — there is no code to run; it is design-only, and the spec itself nominates
  it as the thing to attack hardest. I addressed only its *reachability* (**F12**, **F18**), not
  whether a transport-level `actor.Roles` test is defensible against the pluggable-`Authorizer`
  architecture. That is the interaction lens's question and it remains open.
- gin's `gc.Set` trap (§2.8) — round 1 executed it; I did not re-run it.
- §2.5's labelled `ASSUMPTION (unverified)` that the three wiring mains still start — correctly
  labelled, and plan Task 16 Step 3 owns it.
- Anything requiring Docker. All probes above ran container-free (`authz`, `service`,
  `transport/http/{httpcore,stdlib,gin,fiber}`) or on `dbtest.RunTestSQLite`, which is pure Go.

### Minimum to make this bundle implementable

1. **F4** — round-trip pre-check, and keep the normalized value (**F6**). Add the 20000-deep
   fixture to plan Task 4 with its mutation.
2. **F12** — paste the 63-site member set into Tasks 9/10/11, move enforcement into each adapter's
   single registration helper, and add a mount-everything completeness guard that enumerates routes
   from the router rather than from a list.
3. **F3, F5, F8, F11** — four sentences corrected and two residuals added (attribute size bound;
   resolver-must-honour-ctx). None is a design change.
4. **F10, F13, F14, F15, F16** — five prescribed tests given fixtures that can fail, each with its
   *"what makes this fail today"* stated. All five fixtures are written out in this report and four
   of them were executed here.

Nothing in this list reopens a decision. The **decisions** survived this lens — the seam, the
context-only read, the fail-closed default, the empty-ID removal, the `Attributes` flow and the
group widening are all defensible and three of their load-bearing premises re-derived exactly.
What failed is the **verification layer**: two guards that do not guard and five tests that cannot
fail, in a bundle whose stated purpose was to stop shipping exactly those.
