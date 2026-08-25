# ADR-0189 rule-#9 adversarial audit — EXECUTION lens

Worktree: `.../scratchpad/wt-execution`, detached at `7fa756d0`.
Step 0: all three bundle documents PRESENT and read.

Bundle:
- `docs/specs/2026-08-25-request-actor-identity.md`
- `docs/adr/0189-the-http-transport-does-not-accept-a-self-asserted-actor.md`
- `docs/plans/2026-08-25-request-actor-identity.md`

---

### F1 — ADR-0147 amendment #5 contains a factual claim this bundle FALSIFIES, and ADR-0189 says "Amends: nothing" — [MAJOR]

**Bundle text attacked:** `docs/adr/0189-*.md` header, *"**Amends:** nothing. **Relates to:** …
ADR-0147 (the human-task audit model, whose *"the engine guarantees nothing beyond `id`"* is
why an empty actor ID is refused)"*, and Consequences/Neutral, which discusses only ADR-0117:
*"ADR-0117 needs no amendment."* No sentence anywhere in the bundle says ADR-0147 needs one.

**What I ran:**
```
$ grep -rn "httpcore\.Actor" . | grep -v docs/plans/sweep-evidence
$ sed -n '185,200p' docs/adr/0147-humantask-audit-model.md
```

**Observed:**
```
docs/adr/0147-humantask-audit-model.md:195:  `httpcore.Actor` is `{id, roles}` only — so over HTTP those two slots can never
```
Full text (ADR-0147 §"Caveats accepted during the rule-#9 re-audit", bullet 1):
> **Actor fidelity differs by slot.** `candidates[]` are resolver-sourced and carry whatever
> `Attributes` the resolver populated. `claim.actor` and `completion.actor` come from the
> acting caller, and the HTTP transport's `httpcore.Actor` is `{id, roles}` only — **so over
> HTTP those two slots can never carry attributes.** Passthrough is faithful to what the
> engine observed; it does not promise the same richness in every slot. **Phase 8's
> whole-document test must therefore build its fixture through the Go API, not the transport.**

**Why the bundle is wrong:** ADR-0189 §3.2 deletes `httpcore.Actor` and lets the whole
`authz.Actor` flow, so ADR-0147's caveat becomes false in **both** of its clauses: the type it
names ceases to exist, and "over HTTP those two slots can never carry attributes" is exactly
what ADR-0189 reverses (it is listed as a *Positive* consequence). ADR-0189 cites ADR-0147
twice as a supporting authority (the empty-ID rule) and never notices that the same document
carries a claim it invalidates. This is the "documents describe what shipped" gate (CLAUDE.md
Delivery Gate item 2) failing *at design time*, on the one ADR the bundle leans on hardest.

**Concrete fix:** change ADR-0189's header to **`Amends: ADR-0147 (Caveats #5, first bullet)`**
and add to Consequences/Neutral: *"ADR-0147's caveat that `claim.actor`/`completion.actor` can
never carry attributes over HTTP is amended by this record — the type it names is deleted and
the whole `authz.Actor` flows. `docs/adr/0147-humantask-audit-model.md:193-197` is edited in
this bundle."* Add a plan Task 12 step to make that edit.

---

### F2 — the compile ablation cannot see stale COMMENTS, and two are in `service`, a package the "5 packages" blast radius does not list — [MAJOR]

**Bundle text attacked:** spec §2.6, *"the blast radius, derived by MACHINE … total 29 / 9
files / 5 packages"*, and *"per package: httpcore 11, gin 7, fiber 5, stdlib 5, parity 1"*.
Also the audit brief's own question: *"what does a compile ablation structurally fail to see?"*

**What I ran:**
```
$ grep -rn "httpcore\.Actor" . | grep -v '^docs/'
```

**Observed:**
```
service/instance_test.go:1090:// purpose: httpcore.Actor is {id, roles} only, so claim.actor / completion.actor
service/instance_test.go:1128:// reproducible; the transport is deliberately bypassed because httpcore.Actor
```
Both are inside doc comments, so they compile fine and the ablation is blind to them. Their
full text:
- `:1088-1091` — *"The fixture is built through the Go API rather than the HTTP transport on
  purpose: httpcore.Actor is {id, roles} only, so claim.actor / completion.actor could never
  carry the attributes the sample shows (ADR-0147 amendment #5)."*
- `:1127-1129` — *"the transport is deliberately bypassed because httpcore.Actor cannot carry
  actor attributes (ADR-0147 amendment #5)."*

**Why the bundle is wrong:** the ablation answers "what stops compiling", which is a strict
subset of "what stops being true". Two committed comments in `service` — a **sixth** package,
outside the 5 the spec enumerates — state as fact a limitation this bundle removes, and cite
the ADR-0147 caveat F1 shows also becomes false. `29 / 9 / 5` is exact **as a count of code
sites**; the spec presents it as the blast radius, which it is not. This is the same class of
error the spec itself flags ("it already missed 18 of 29 once") — the ablation missed a
category, not an arithmetic step, again.

**Concrete fix:** (a) add to spec §2.6 an explicit paragraph: *"⚠ What a compile ablation
structurally cannot see: stale prose. A `grep -rn 'httpcore\.Actor' --include='*.go'` over the
repo finds two doc comments in `service/instance_test.go` (:1090, :1128) that assert the
limitation this record removes; neither breaks the build. Prose sites are counted separately
and are NOT in the 29."* (b) Add both to plan **Task 12 Step 3**'s stale-reference sweep, and
widen that grep from `'"actor"'` to also cover `httpcore\.Actor` and the phrase "can never
carry attributes". (c) State the package count as **"5 code packages + 2 prose sites in
`service`"**, not "5 packages".

---

### F3 — §3.3 rule 3's durable-record premise is TRUE (re-executed independently) — [confirmation, no severity]

**Bundle text attacked:** spec §3.3 rule 3 / ADR Decision 3, *"Admitting `Actor{ID: ""}` …
**breaks the single guarantee ADR-0147 makes**, and it does so in a durable record
(`wrkflw_human_task`, and `wrkflw_instances.snapshot` via `InstanceState.Tasks`)."*
The audit brief asks whether the replacement rationale is load-bearing or "a rule without a
reason". I attacked it and it survived.

**What I ran:** `transport/http/httpcore/zzprobe_test.go` (deleted after the run). A user task
with **no** eligibility (`activity.NewUserTask("approve")` ⇒ `AuthzSpec{}` ⇒ `RoleAuthorizer`
open access, `authz/authz.go:124` `if len(spec.Roles) > 0 && …`), driven to park, then today's
exact HTTP path with a totally empty body:
```go
status, _, err := httpcore.ClaimTask(t.Context(), svc, token, httpcore.ClaimInput{}, nil)
ht, _ := h.TaskStore.Get(t.Context(), token)
pi, _ := svc.GetInstance(t.Context(), "probe-open-1")
```

**Observed:**
```
PROBE claim with EMPTY actor: status=200 err=<nil>
PROBE humantask.Claim JSON = {"actor":{"id":""},"timestamp":"2026-08-25T16:03:33.31536+07:00"}
PROBE instance snapshot doc = {…,"tasks":[{"task_id":"…","node_id":"approve","state":"claimed",
  "claim":{"actor":{"id":""},"timestamp":"…"},"created_at":"…"}],…}
--- PASS: TestZZProbeEmptyActorIDReachesDurableRecord (0.00s)
```

**Why the bundle is right:** an empty actor ID is admitted today (200), and `{"id":""}` lands
verbatim in **both** durable copies. Nothing in `engine`/`service`/`humantask` refuses it —
`engine.ErrEmptyTriggerKey` (`engine/trigger_validate.go:160-181`) validates `HumanClaimed.TaskID`,
not the actor, so the sibling sentinel a reader might expect to already cover this does not.
`authz.Actor.ID` has no `omitempty` (`authz/authz.go:35`), so the empty string is rendered
rather than dropped. Rule 3 is load-bearing and its stated reason is real. **No change needed
— but see F4, which is what this same probe exposes.**

---

### F4 — the empty-ID rule protects an ENGINE-level guarantee at the TRANSPORT only, and the bundle's own §4.1 residual does not cover this leg — [MAJOR]

**Bundle text attacked:** spec §4.1, *"This design covers the four `runtime/task` verbs as
reached over HTTP. An embedded consumer driving the engine directly is unaffected, **by
design**."* — and ADR Decision 3's justification, which is stated as an **invariant of the
audit model** (*"breaks the single guarantee ADR-0147 makes, durably"*), not as an HTTP concern.

**What I ran:** the same probe as F3, plus the post-change shape via the module-root API:
```go
_, err = svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskID: token, Actor: authz.Actor{}})
```
**Observed:** identical — `err=<nil>`, `claim":{"actor":{"id":""}` in both stores.

**Why the bundle is wrong (in scope, not in mechanism):** §4.1's residual is written entirely
about **authorization bypass** (`ApplyTrigger` "bypasses authorization entirely",
`engine.NewHumanCompleted`). The empty-ID rule is **not** an authorization rule — the ADR
justifies it by data integrity in the audit record — so §4.1 does not actually cover it, yet
after this ships the identical corruption remains one line away through
`service.ClaimTaskRequest`, which is **module-root public API** and which CLAUDE.md says wins
every tie against transport convenience ("When a design choice trades library ergonomics for
server convenience, library ergonomics win"). The bundle argues an engine-level invariant and
then enforces it in exactly the one place a library consumer does not go through.

This is not an argument to drop the rule (F3 shows it is real). It is an argument that the
bundle **states the wrong scope** for it, and that a reader will reasonably conclude ADR-0147's
guarantee is now upheld when it is not.

**Concrete fix:** either
(a) **preferred** — move the check where the guarantee lives: reject `Actor.ID == ""` in
`service.ClaimTask`/`CompleteTask`/`ReassignTask` (or in the `humantask` claim invariant
ADR-0183 already installed pre-commit), returning `engine.ErrEmptyTriggerKey`-style 400, and
keep the httpcore 401 as the *transport-shaped* rendering of the same refusal; or
(b) if that is out of scope for this delivery, add an explicit residual to spec §4 and ADR
Consequences/Negative: *"⚠ The empty-`Actor.ID` refusal is enforced in the transport only.
`service.ClaimTaskRequest{Actor: authz.Actor{}}` still writes `{"id":""}` into
`wrkflw_human_task` and `wrkflw_instances.snapshot` — EXECUTED. The ADR-0147 guarantee this
rule cites is therefore narrowed, not restored. New backlog item."* Option (b) must not be
folded into §4.1, which is about authorization and does not cover it.

---

### F5 — "the actor is CLONED so a later mutation by the caller cannot reach the engine" is FALSE for nested attribute values, and the plan's own test cannot detect it — [MAJOR]

**Bundle text attacked:** ADR Decision 1, *"`ContextWithActor` stores `a.Clone()` under an
unexported struct key, so **a later mutation by the caller cannot reach the engine**"*; spec
§3.1 godoc, *"The actor is cloned on the way in, so a later mutation by the caller cannot reach
the engine."*; plan Task 1 Step 3 godoc, *"The actor is CLONED on the way in, so a later
mutation by the caller cannot reach the engine or the human-task audit record."* All three are
unqualified.

**What I ran:**
```go
nested := map[string]any{"tenant": "acme"}
a := authz.Actor{ID: "alice", Attributes: map[string]any{"profile": nested}}
c := a.Clone()
nested["tenant"] = "HACKED"                 // mutate INSIDE an attribute value
got := c.Attributes["profile"].(map[string]any)["tenant"]
```

**Observed:**
```
PROBE after Clone(), nested mutation visible through the clone? tenant=HACKED
PROBE top-level Roles mutation visible through the clone? roles=[manager]
--- PASS: TestZZProbeCloneIsShallowForNestedAttributes (0.00s)
```

**Why the bundle is wrong:** `authz.Actor.Clone`'s **own godoc** already says it
(`authz/authz.go:41-46`): *"Attributes are cloned **one level deep**: nested maps and slices
inside an attribute value **remain shared**."* The bundle inherited the guarantee and restated
it with the hedge stripped — the exact Premise-Discipline failure CLAUDE.md names
("Restating strips the hedge; the sentence stops looking contingent and nobody checks it
again"). It matters more here than usual because (i) this is a *security* ADR, (ii) the
sentence ships as **public godoc** on a public root-package function, and (iii) F6 below shows
attributes now flow all the way into the durable audit record, so a shared nested map is a
live write-after-audit channel: a consumer's middleware that reuses one attributes map across
requests (a cached directory profile is the obvious case) has the audit record mutate
underneath it.

⚠ **And the plan's test cannot catch it.** `TestContextWithActorClonesTheActor` (plan Task 1
Step 1) mutates `roles[0]` and `attrs["dept"]` — both **top level**. Both are genuinely
cloned, so the test passes while the sentence it is nominally pinning is false. Check the
fixture, not the assertion text: this is a test that *can* fail (its stated RED is real) but
that is pinned to a strictly weaker claim than the prose beside it.

**Concrete fix:**
1. Requalify all three prose sites: *"The actor is cloned one level deep on the way in (see
   [Actor.Clone]), so a later mutation of the caller's Roles slice or Attributes map cannot
   reach the engine. ⚠ Values **nested inside** an attribute remain shared — do not hand
   `ContextWithActor` an attributes map whose values you keep mutating."*
2. Add a case to plan Task 1's test asserting the **documented** shallow behaviour, so the
   depth is pinned rather than left ambiguous: nested mutation IS visible, and if a future
   change makes `Clone` deep, the test tells you the godoc must change too.
3. Consider whether `ContextWithActor` should deep-copy instead. Cheap and it would make the
   unqualified sentence true — but it diverges from `Actor.Clone`'s repo-wide contract, so it
   is a decision, not a detail. State whichever you pick.

---

### F6 — attributes now flow into TWO durable stores with NO bound, and neither ADR-0186's body cap nor §4.2's backlog-103 residual bounds them — [MAJOR]

**Bundle text attacked:** ADR Consequences/Positive, *"**`Actor.Attributes` reaches the
authorizer**, so attribute-based authorization over actor attributes becomes possible over HTTP
for the first time."* — and spec §4.2, which names **only** backlog 103 (deny-list predicates
allow on a missing variable) as the cost of that.

**What I ran:** the post-change shape, through the service API the endpoints will call:
```go
svc.ClaimTask(ctx, service.ClaimTaskRequest{TaskID: token, Actor: authz.Actor{
    ID: "alice", Roles: []string{"manager"},
    Attributes: map[string]any{"dept": "finance", "clearance": 7}}})
```
**Observed:**
```
PROBE humantask.Claim WITH ATTRIBUTES =
 {"actor":{"id":"alice","roles":["manager"],"attributes":{"clearance":7,"dept":"finance"}},"timestamp":"…"}
PROBE snapshot tasks = {…"claim":{"actor":{…,"attributes":{"clearance":7,"dept":"finance"}},…}…}
```
i.e. attributes reach **`wrkflw_human_task` AND `wrkflw_instances.snapshot`**, not only the
authorizer.

**Why the bundle is wrong:** the Positive consequence is written as if the destination were the
`Authorizer`. It is not — ADR-0147's passthrough carries the same actor into two durable
copies **per claim and per completion**. So the change adds an **unbounded, consumer-supplied
`map[string]any` to a durable column**, and:
- **ADR-0186's `MaxBodyBytes` does not bound it.** That cap is on the *request body*; a
  `RequestActorFunc`'s output is consumer code (typically decoded from a JWT in a *header*,
  which `MaxBodyBytes` never sees). The bundle reuses ADR-0186's option-alias convention and
  inherits none of its bound.
- The repo has already written this down and the bundle did not carry it forward:
  `docs/plans/sweep-evidence/reaudit-0186-counting.md:436` filed exactly this as **MAJOR**,
  scoped as *"not exploitable over HTTP today — `httpcore.Actor` carries `ID` and `Roles` only,
  no attributes"*. **ADR-0189 removes precisely that scoping clause** and does not cite the
  finding. This is an inherited hedge whose predicate the new bundle deletes.

**Concrete fix:** add to ADR Consequences/Negative and spec §4: *"⚠ Actor attributes are an
unbounded consumer-supplied `map[string]any` that now reaches two DURABLE stores
(`wrkflw_human_task`, `wrkflw_instances.snapshot`) on every claim and completion — EXECUTED.
ADR-0186's `MaxBodyBytes` does not bound it: the resolver's output is not body-derived. The
prior finding at `docs/plans/sweep-evidence/reaudit-0186-counting.md:436` scoped itself
'not exploitable over HTTP today because `httpcore.Actor` has no Attributes'; this record
removes that scoping. New backlog item: bound `Actor.Attributes` at the authz/service seam."*
Also list it in the ADR's "Explicitly still open" backlog line, which today names 52/53/90/103/124
and would otherwise be read as exhaustive.

---

### F7 — a non-JSON-marshalable actor attribute PERMANENTLY POISONS the instance view, and this failure mode becomes reachable over HTTP only because of this bundle — [MAJOR]

**Bundle text attacked:** the audit brief's target #5 (*"does anything marshal it into a durable
column that could now fail…?"*) — which the bundle does not address anywhere. ADR
Consequences/Negative lists three costs; none is this.

**What I ran:**
```go
_, err = svc.ClaimTask(t.Context(), service.ClaimTaskRequest{TaskID: token,
    Actor: authz.Actor{ID: "alice", Attributes: map[string]any{"session": make(chan int)}}})
pi, _ := svc.GetInstance(t.Context(), "probe-poison-1")
doc, merr := json.Marshal(pi)
```

**Observed:**
```
PROBE claim with unmarshalable attribute: err=<nil>
PROBE GetInstance marshal err=json: error calling MarshalJSON for type service.processInstance:
      json: unsupported type: chan int  (len doc=0)
--- PASS: TestZZProbeUnmarshalableAttributePoisonsTheView (0.00s)
```

**Why this is a real new failure mode:** the claim **succeeds** — nothing validates the
attribute map — and the poison is written into the task record. Every subsequent read of that
instance's view then fails to marshal, i.e. the instance is bricked for the API surface
(ADR-0147's whole point) with a **500 and an empty body**, for the life of the record. Today
this is unreachable over HTTP because all three endpoints drop `Attributes`; ADR-0189 is what
opens it. The trigger is ordinary consumer code — a `RequestActorFunc` stashing a
`*jwt.Token`, a `context.CancelFunc`, an `io.Reader`, a channel — in a `map[string]any` whose
type gives no warning.

⚠ Note the asymmetry with the rest of the design: every other new path in this bundle is
carefully **fail-closed at request time**; this one fails *open at write time* and *closed
forever at read time*, on a different request, to a different caller.

**Concrete fix (pick one, but state it):**
1. **Validate at the seam** — in `resolveRequestActor`, after the empty-ID check, verify the
   resolved actor marshals: `if _, err := json.Marshal(a.Attributes); err != nil { return
   authz.Actor{}, fmt.Errorf("%w: actor attributes are not JSON-serialisable: %w",
   ErrIdentityUnavailable, err) }`. Costs one marshal per task request on a path that already
   does several; makes the failure a **503 at the moment the consumer's resolver misbehaves**,
   naming the resolver, instead of a 500 on someone else's later GET. This is the fix that
   matches the rest of the design's posture.
2. Or declare it explicitly out of scope in ADR Consequences/Negative with the observed output
   above, plus a `SECURITY.md` sentence: *"a `RequestActorFunc` must return only
   JSON-serialisable attribute values; the value is persisted verbatim (ADR-0147 passthrough)."*
   Silence is not an adjudication.
3. Either way add a plan test: resolver returns an unmarshalable attribute ⇒ assert the chosen
   behaviour. Its RED today: no such path exists.

---

### F8 — §2.2's SetContext-vs-Locals claim RE-EXECUTES TRUE, and adds two facts the author did not check — [confirmation + see F9]

**Bundle text attacked:** spec §2.2 / ADR Decision 4, *"`c.SetContext` reaches `httpcore`
(`from-middleware`); **`c.Locals` does not** (`<nil>`)."*

**What I ran:** `transport/http/fiber/zzprobe_test.go` (deleted after the run), five middleware
shapes, logging exactly what `fiber/groups.go:151` hands `httpcore` (`c.Context()`):
```go
app.Get("/p", func(c fiberlib.Ctx) error {
    ctx := c.Context()
    dl, hasDL := ctx.Deadline()
    t.Logf("… c.Context().Value=%v c.Value=%v ctxType=%T ctx==Background:%v Done==nil:%v Deadline:(%v,%v) Err:%v",
        ctx.Value(probeKey{}), c.Value(probeKey{}), ctx, ctx == context.Background(),
        ctx.Done() == nil, dl, hasDL, ctx.Err())
    return c.SendString("ok")
})
```

**Observed (fiber v3.4.0, `go.mod:14`):**
```
PROBE none                 c.Context().Value=<nil>             c.Value=<nil>            ctxType=context.backgroundCtx ctx==Background:true  Done==nil:true Deadline:(…,false) Err:<nil>
PROBE SetContext           c.Context().Value=from-middleware   c.Value=<nil>            ctxType=*context.valueCtx     ctx==Background:false Done==nil:true Deadline:(…,false) Err:<nil>
PROBE Locals               c.Context().Value=<nil>             c.Value=from-middleware  ctxType=context.backgroundCtx ctx==Background:true  Done==nil:true Deadline:(…,false) Err:<nil>
PROBE LocalsThenSetContext c.Context().Value=setcontext-second c.Value=locals-first     ctxType=*context.valueCtx     ctx==Background:false Done==nil:true …
PROBE SetContextThenLocals c.Context().Value=setcontext-first  c.Value=locals-second    ctxType=*context.valueCtx     ctx==Background:false Done==nil:true …
--- PASS: TestZZProbeFiberContextPropagation (0.00s)
```

**Verdict on §2.2:** CONFIRMED, exactly as written, at v3.4.0. Also answering the two questions
the author did not ask:
- **`c.Locals` set BEFORE a `SetContext`** does not merge and is not clobbered — the two live in
  separate objects (`LocalsThenSetContext`: context sees `setcontext-second`, `c.Value` sees
  `locals-first`). So the trap is symmetric and order-independent: a consumer who sets Locals
  and *also* SetContexts still authenticates only via the SetContext leg.
- **With no middleware, `c.Context()` is literally `context.Background()`** (`ctx ==
  context.Background()` is `true`). That is the finding — see F9.

---

### F9 — on fiber the resolver runs on `context.Background()`: no deadline, no cancellation. The design's "503, never an open door" has an unnamed third state on one adapter — HANG — [MAJOR]

**Bundle text attacked:** spec §3.2, *"the request context already reaches `httpcore`
**unmodified** in all three"*; §3.3 rule 2, *"A resolver ERROR ⇒ 503, never a downgrade. A
transient identity-provider failure must not become an open door."*; and ADR Decision 2's
justification for resolving in `httpcore` rather than per adapter.

**What I ran (same probe, plus):**
```go
req := httptest.NewRequest("GET", "/p", nil)
reqCtx, cancel := context.WithTimeout(req.Context(), time.Millisecond); defer cancel()
req = req.WithContext(reqCtx)
time.Sleep(3 * time.Millisecond) // the inbound request context is ALREADY dead
t.Logf("PROBE inbound req.Context().Err() before Test = %v", req.Context().Err())
app.Test(req)
```

**Observed:**
```
PROBE inbound req.Context().Err() before Test = context deadline exceeded
PROBE no-mw   ctx.Done()==nil? true  err=<nil>
```
The handler's context is alive and undeadlined even though the caller's context was already
cancelled. Combined with `ctx == context.Background(): true` above.

**Why the bundle is wrong:**
1. **"the request context already reaches `httpcore` unmodified in all three" is false as
   stated for fiber.** It is not "the request context" — it is `context.Background()` unless a
   middleware replaced it, and it carries neither the client's cancellation nor any deadline.
   `stdlib` (`req.Context()`) and `gin` (`gc.Request.Context()`) both carry both. The sentence
   is the premise the "resolve once in httpcore" decision rests on, and it over-generalises.
   (The *value* propagation the design needs does hold — F8 — so the decision survives; the
   sentence does not.)
2. **This bundle is what first puts consumer I/O on that context.** Before ADR-0189 nothing on
   the fiber task path called out to a consumer-supplied function with `ctx`. A
   `RequestActorFunc` is exactly that, and the realistic implementation does network I/O (a
   JWKS fetch, an LDAP/directory lookup, a token-introspection call). On fiber that call gets a
   context that **can never be cancelled and never expires**. So a hung identity provider does
   not produce the 503 §3.3 rule 2 promises — it produces a **hung fiber handler**, held open
   after the client is long gone, until the provider's own client-side timeout (if any) fires.
   The design enumerates 401 / 503 / 200 and never enumerates *hang*.
3. **The parity suite asserts the three adapters answer identically** (`parity/parity_test.go`),
   and plan Task 10 adds a parity case for the 401. It cannot express this divergence, so the
   asymmetry ships behind a suite whose whole purpose is to deny asymmetries exist.
4. ⚠ The recommended idiom does not fix it: `c.SetContext(authz.ContextWithActor(c.Context(),
   a))` — plan Task 9 Step 3, spec §3.6 — derives from `c.Context()`, i.e. from
   `context.Background()`. The example the bundle ships teaches the Background-rooted form.

**Concrete fix:**
1. Correct §3.2's sentence to what was measured: *"the request context reaches `httpcore`
   unmodified on `stdlib` and `gin`. ⚠ On fiber `c.Context()` is `context.Background()` unless
   middleware called `SetContext` — EXECUTED: `ctx == context.Background()` is true, `Done()`
   is nil, no deadline. Values propagate (which is what this design needs); cancellation and
   deadlines do not."*
2. Add a Consequences/Negative bullet naming the third state: *"⚠ On fiber a resolver that
   blocks cannot be cancelled by the client disconnecting or by a deadline, because
   `c.Context()` is Background-rooted. A hung identity provider hangs the handler rather than
   returning 503."*
3. Give the contract a mitigation the library can actually enforce, and pin it: either document
   in `RequestActorFunc`'s godoc that **the implementation owns its own timeout** (`⚠ ctx may
   carry no deadline — on the fiber adapter it is Background-rooted. Apply your own timeout.`),
   or wrap the call in `httpcore` with a bounded `context.WithTimeout` on a new
   `CustomizeConfig.RequestActorTimeout` (defaulted), which makes the promised 503 real on all
   three adapters. The second is the one consistent with §3.3 rule 2's wording.
4. `examples/authenticated_tasks`'s fiber section must show the timeout, not just `SetContext`.

---

### F10 — §2.3's "a stale actor body is IGNORED" RE-EXECUTES TRUE on all three adapters, via real mounted routes — [confirmation]

**Bundle text attacked:** spec §2.3 / ADR Decision 1, *"⇒ *ignored, not rejected* is correct
for all three."* The spec proved `stdlib` and `gin` **by reading** (`grep` for
`DisallowUnknownFields`, plus a claim about `ShouldBindJSON`) and only executed fiber, and its
fiber probe was a bare `c.Bind().JSON` call, not a mounted route.

**What I ran:** real `Mount`ed routes on all three, POSTing a key no DTO declares — the exact
shape of a post-removal stale body:
```go
newPostRequest(t, "/tasks/"+taskID+"/claim", map[string]any{
    "totally_unknown_key_xyz": map[string]any{"id": "alice", "roles": []string{"manager"}}})
```

**Observed:**
```
PROBE stdlib unknown key => status=403 body={"error":"forbidden","message":"…not authorized"}
PROBE gin    unknown key => status=403
PROBE fiber  unknown key => status=403 body={"error":"forbidden","message":"…not authorized"}
```
403, not 400, on all three ⇒ the unknown key is decoded away silently and the empty actor then
fails the role check. §2.3 holds end-to-end. `stdlib/body.go:143` `json.NewDecoder(body).Decode`,
`gin/groups.go:168` `gc.ShouldBindJSON`, `fiber/groups.go:148` `c.Bind().JSON` — none strict.

---

### F11 — after this change the CORRECT claim request has no body, and all three adapters answer 400 to a bodyless claim — [CRITICAL]

**Bundle text attacked:** plan Task 5 Step 3, *"leave `ClaimInput` as an empty struct with a
comment saying why it still exists … `type ClaimInput struct{}`"*; ADR Decision 1, *"A body
still carrying `"actor"` or `"by"` is IGNORED, not rejected … would break consumers' rollout
windows."*; spec §2.3's whole migration argument.

**What I ran:** real mounted routes, three body shapes, all three adapters. ⚠ Note the gin test
helper `post()` (`gin_test.go:42-55`) does `json.Marshal(nil)` ⇒ it sends the four bytes `null`,
**not** an absent body — so a naive probe through it reports the wrong answer. I built the
requests by hand:
```go
{"ABSENT body", http.NoBody}, {"literal null", strings.NewReader("null")}, {"empty object", strings.NewReader("{}")}
```

**Observed:**
```
PROBE stdlib claim ABSENT body   => status=400 body={"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
PROBE stdlib claim empty object  => status=403
PROBE gin    claim ABSENT body   => status=400 body={"error":"bad_request","message":"workflow-httpcore: bad input: EOF"}
PROBE gin    claim literal null  => status=403
PROBE gin    claim empty object  => status=403
PROBE fiber  claim ABSENT body   => status=400 body={"error":"bad_request","message":"workflow-httpcore: bad input: bind from body: unexpected end of JSON input"}
PROBE fiber  claim empty object  => status=403
```

**Why the bundle is wrong:** ADR-0189 empties `ClaimInput` completely. The whole point of the
migration it asks consumers to perform is *"stop sending `actor`"* — and a client that does
exactly that, on a request type with **zero remaining fields**, naturally sends no body at all.
**All three adapters answer 400 `bad input: EOF`.** To claim a task after this ships, a client
must transmit `{}`: a body that carries nothing, exists only to satisfy a decoder, and is
documented nowhere in the bundle.

The migration story covers the wrong client. §2.3 proves the *lagging* client (still sending
`actor`) is fine; the *correctly updated* client is the one that breaks. And nothing in the plan
can detect it: every prescribed endpoint test calls
`httpcore.ClaimTask(t.Context(), svc, token, httpcore.ClaimInput{}, nil, tc.actor)` (Task 5
Step 1) — the Go struct, **bypassing the decode path entirely** — and Tasks 7/8/9 only *delete
keys from* existing bodies, leaving `map[string]any{}` behind, which serialises to `{}` and
passes. The prescribed suite is green in exactly the case the real client fails.

⚠ Root cause is `stdlib/body.go:132` `decodeRequestBody` ("decodes a **REQUIRED** JSON body")
being used for claim, while a sibling `decodeOptionalRequestBody` already exists in the same
file for precisely this shape.

**Concrete fix:**
1. Make the claim route's body **optional** on all three adapters, since after this change it
   has no required content: `stdlib` switches to the existing `decodeOptionalRequestBody`;
   `gin` and `fiber` guard the bind (`if gc.Request.ContentLength != 0 { … }` /
   `if len(c.Body()) > 0 { … }`) or ignore an EOF-class bind error for `ClaimInput` specifically.
   ⚠ Do **not** blanket-ignore bind errors — `CompleteInput` and `ReassignInput` still carry
   real fields and must keep rejecting malformed bodies.
2. Add a **parity** case (plan Task 10): `POST /tasks/{token}/claim` with **no body** returns the
   same status on all three adapters — with an authenticated resolver, **200**. Its RED today:
   400 on all three.
3. Reconsider deleting `ClaimInput` rather than emptying it. The plan keeps it "so a body posted
   by a pre-ADR-0189 client still decodes to a no-op" — but F10 shows unknown keys are already
   ignored, so an empty struct is not what buys that; *any* decode target does, including none.
   If the route stops decoding a body at all, the stale client and the updated client both work
   and the empty-struct type disappears from the public API. State whichever you choose and why.
4. Whatever is chosen, `SECURITY.md`/README must show the post-change claim request verbatim.

---

### F12 — the "11 compile-breaking lines" ablation is UNDERCOUNTED: it is 14. The spec's ablation modelled only ONE of the bundle's two breaking changes — [CRITICAL]

**Bundle text attacked:** spec §2.6, *"the three fields and the `httpcore.Actor` type were
deleted in a detached worktree at `9789ebcc`, **the three `endpoints.go` sites stubbed to
`authz.Actor{}`**, and every package including test packages compiled"* → *"Compile-breaking:
**11** (17 errors) … **Compile-breaking, exhaustive**: `httpcore/dto_test.go` lines 47, 62, 73,
84, 153; `httpcore/endpoints_test.go` lines 405, 422, 466, 485, 531, 560."* And the totals row
**29 / 9 / 5**, restated in the ADR's Negative consequences and in the plan's Task 5 file list
and self-review table.

**What I ran:** I built the **actual** change in my worktree — `authz/context.go`,
`httpcore` sentinels + first arms, `RequestActorFunc` + `CustomizeConfig.RequestActor` +
`WithRequestActor` + the `ResolveConfig` post-loop default, `resolveRequestActor`, the three
DTO fields and `httpcore.Actor` removed, **the three endpoint signatures gaining the
`actor RequestActorFunc` parameter**, and the nine adapter sites passing `cfg.RequestActor` —
then:
```
$ go build ./...                                   # BUILD_EXIT=0
$ go test -count=1 -gcflags=-e -run '^$' ./... > /tmp/abl.log 2>&1; echo "EXIT=$?"
```

**Observed:**
```
EXIT=1
  15 transport/http/httpcore/endpoints_test.go
   5 transport/http/httpcore/dto_test.go
=== distinct error LINES === 14
transport/http/httpcore/dto_test.go:47,62,73,84,153
transport/http/httpcore/endpoints_test.go:405,422,436,466,485,499,531,560,575
```
The three the spec's list does not contain:
```
transport/http/httpcore/endpoints_test.go:436:76: not enough arguments in call to httpcore.ClaimTask
transport/http/httpcore/endpoints_test.go:499:79: not enough arguments in call to httpcore.CompleteTask
transport/http/httpcore/endpoints_test.go:575:79: not enough arguments in call to httpcore.ReassignTask
```
```go
:436  status, body, err := httpcore.ClaimTask(t.Context(), svc, token, tc.in, nil)
:499  status, body, err := httpcore.CompleteTask(t.Context(), svc, token, tc.in, nil)
:575  status, body, err := httpcore.ReassignTask(t.Context(), svc, token, tc.in, nil)
```

**Why the bundle is wrong:** the ablation **stubbed `endpoints.go`** instead of changing its
signatures — the spec says so in its own methodology sentence. ADR-0189 makes **two** breaking
changes: (1) three DTO fields + a type are removed, and (2) *"the three task endpoints gain a
parameter, which breaks any consumer-written adapter calling them"* (ADR Negative, in the same
bullet as the 29). The ablation modelled (1) and stubbed (2) out of existence, so its result is
the blast radius of **half the change**, presented as the machine-derived blast radius of the
whole one. That is worse than a miscount: it is a methodology that *cannot* see the change the
ADR itself calls the breaking one for third-party adapters.

Corrected figures: **compile-breaking 14** (not 11), **total distinct lines 32** (not 29); files
stay **9** and code packages stay **5** (the three new lines are in an already-counted file).
The word **"exhaustive"** on the 11-line list is false.

⚠ This lands on the spec's own boast — *"the net is closed **by construction**, not by the grep …
§2.6's compile ablation is the machine check on that"* — and on the audit brief's instruction to
*"attack the ablation's completeness, not its arithmetic"*. The arithmetic was fine; the ablation
was incomplete. And §2.6 explicitly says the prior bundle's `29 / 9 / 5` is *"exact at today's
base — re-derived, not restated"*: the re-derivation reproduced the inherited number because it
reproduced the inherited method's blind spot.

**Concrete fix:**
1. Re-run the ablation **with the real signatures**, not stubs: apply the parameter to
   `ClaimTask`/`CompleteTask`/`ReassignTask` before compiling. Record 14 / 32.
2. Correct every restatement: spec §2.6 table + exhaustive list + the per-package line
   (`httpcore` becomes **14**, total **32**); ADR Negative (*"29 pin sites"* → 32, *"only 11 …
   break the build"* → 14); plan Task 5's file list (add `endpoints_test.go` 436/499/575) and the
   self-review table row *"§2.6 the 29 pins | 5 (11 compile) · 7/8/9 (17) · 10 (1)"*.
3. Add a methodology sentence to §2.6: *"⚠ An ablation must apply EVERY breaking change in the
   bundle. Stubbing one out to keep the tree compiling measures a change that is not the one
   being shipped."*

---

### F13 — §2.6's starred PREDICTION is CONFIRMED: both `stdlib` 403 pins fail LOUDLY with 401 — [confirmation]

**Bundle text attacked:** spec §2.6, *"⭐ **Dropping the anonymous opt-in converts both from
vacuous to loud** … both requests resolve no identity and get **401**, so `401 != 403` fails.
⚠ **PREDICTION, not yet executed**"*; plan Task 5 Step 5, *"If they PASS, stop: the prediction
is wrong and the design's fail-closed claim needs re-deriving."*

**What I ran (against the built change described in F12):**
```
$ go test -count=1 -v -run 'TestTaskRoutes_Complete_ServiceError|TestTaskRoutes_Reassign_ServiceError' \
    ./transport/http/stdlib/... > /tmp/pred.log 2>&1; echo "EXIT=$?"
```

**Observed:**
```
EXIT=1
=== RUN   TestTaskRoutes_Complete_ServiceError
=== RUN   TestTaskRoutes_Reassign_ServiceError
    errors_test.go:191: want 403 reassign forbidden, got 401 (body={"error":"unauthenticated","message":"the request carries no authenticated actor"})
    errors_test.go:159: want 403 complete forbidden, got 401 (body={"error":"unauthenticated","message":"the request carries no authenticated actor"})
--- FAIL: TestTaskRoutes_Reassign_ServiceError (0.00s)
--- FAIL: TestTaskRoutes_Complete_ServiceError (0.00s)
```

**Verdict:** the prediction holds exactly. Both pins become loud, with the predicted status and
the predicted mechanism (no identity ⇒ 401, never a zero actor ⇒ never an accidental 403). The
design's fail-closed claim does **not** need re-deriving. The plan's mandated mutation on these
two rewrites is worth doing anyway, but the RED it depends on is now observed rather than
predicted — spec §2.6's ⚠ PREDICTION marker can be replaced with this output.

⚠ **Anchor correction:** the spec cites the assertions as `errors_test.go:158` and `:190`; the
lines that actually *report* are **:159** and **:191** (`:158`/`:190` are the `if rr.Code !=
http.StatusForbidden {` guards, `:159`/`:191` the `t.Fatalf`). Plan Task 7 Step 2/3 tells an
agent to work at `:158`/`:190`. Prefer the test NAMES —
`TestTaskRoutes_Complete_ServiceError`, `TestTaskRoutes_Reassign_ServiceError` — which the plan
already uses at Task 5 Step 5 and which do not drift.

---

### F14 — 401 now PRECEDES the task lookup, so an unauthenticated request for a NONEXISTENT task returns 401 instead of 404. Unstated, and it breaks a test the plan explicitly tells the gin agent NOT to worry about — [MAJOR]

**Bundle text attacked:** plan Task 8, *"⚠ `gin_coverage_test.go:244` asserts **404** on a
nonexistent token, not 403. **gin has no 403 assertion at all** — do not go looking for the
'gin vacuous 403 pin'"*; spec §2.6, same claim. Also the ADR Decision 3 table, which enumerates
four refusal conditions and says nothing about ordering against the existing 404 arm.

**What I ran (built change, full adapter suites):**
```
$ go test -count=1 ./transport/http/{stdlib,gin,fiber,parity}/... > /tmp/red.log 2>&1; echo "EXIT=$?"
$ grep -c '^--- FAIL' /tmp/red.log
```
**Observed:** `EXIT=1`, **14** failing tests. Per-adapter failing assertion lines:
```
stdlib: coverage_test.go:98, coverage_test.go:131, errors_test.go:159, errors_test.go:191, stdlib_test.go:476
gin:    gin_test.go:416, gin_test.go:446, gin_coverage_test.go:195, gin_coverage_test.go:221, gin_coverage_test.go:247
fiber:  fiber_test.go:567, fiber_test.go:588, fiber_test.go:618
parity: parity_test.go:532
```
`gin_coverage_test.go:247` — the assertion for the body at :244 — **fails**, and it is the
404-on-nonexistent-token pin.

**Why the bundle is wrong:** `resolveRequestActor` runs **first** in each endpoint, before
`svc.ClaimTask` ever looks the token up. So for an unauthenticated caller, *every* task route
answers 401 regardless of whether the token exists. That is defensible — arguably correct, since
404-vs-401 on an unauthenticated request leaks task existence — but it is a **behaviour change
the ADR does not enumerate**, on a status code the repo has an existing pin for, and the bundle
states the opposite expectation twice: the spec/plan present `:244` as a pin that merely needs
its stale key deleted (Task 8 Step 1) and warn the agent *away* from it. An agent that follows
Task 8 Steps 1–2 literally and then hits Step 4's *"expect EXIT=0"* will be looking at a failure
the plan told it not to expect.

**Concrete fix:**
1. Add to ADR Decision 3, under the refusal table: *"⚠ Resolution precedes the task lookup, so
   an unauthenticated request for a **nonexistent** task returns 401, not 404. This is
   deliberate — 404-vs-401 to an unauthenticated caller discloses whether a task id exists."*
   Put it in Consequences/Positive too; it is a small security win the bundle is getting for free
   and not claiming.
2. Correct plan Task 8: `gin_coverage_test.go:244`'s pin **does** change — it needs a
   `gin.WithRequestActor` mount to still reach the 404 — and add a new test asserting the
   ordering: *unauthenticated + nonexistent token ⇒ 401*. Its RED today: today it is 404.
3. Give the same ordering pin to `stdlib` and `parity` so all three adapters agree — the parity
   suite is the natural home.

---

### F15 — the ADR's "the raw error is logged by the adapter's `writeErr`" is TRUE, but it is logged under the message "internal error" — [MINOR]

**Bundle text attacked:** spec §3.4, *"`ErrIdentityUnavailable` is 5xx, so `ErrorBody.Message`
is empty and the raw error is logged by the adapter's `writeErr`, never sent to the client."*

**What I ran:** read all three (non-test) `writeErr` implementations.
**Observed:**
```
stdlib/write.go:32  if status >= 500 { cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err) }
gin/write.go:13     if status >= 500 { cfg.Logger.ErrorContext(gc.Request.Context(), "gin: internal error", "err", err) }
fiber/write.go:13   if status >= 500 { cfg.Logger.ErrorContext(c.Context(), "fiber: internal error", "err", err) }
```
**Verdict:** the guard is `>= 500`, so 503 is logged with the wrapped cause on all three, and
`ClassifyError` leaves `Message` empty for the 503 arm, so nothing leaks. The claim holds.

**The MINOR:** the log line reads *"internal error"* for what is by construction **not** an
internal error — it is a consumer-side identity-provider fault, and it is the one 5xx in this
package whose cause is third-party code. An operator grepping for identity failures finds
`"rest: internal error"`. Also `fiber/write.go:14` logs to `c.Context()`, which F9 shows is
`context.Background()` — any log handler doing context-based correlation (trace id, request id)
gets nothing on fiber.

**Concrete fix:** either accept it and say so in §3.4 (*"it is logged under the adapters'
existing generic 5xx message; identity failures are distinguishable only by the `err` attribute"*),
or add an `errors.Is(err, ErrIdentityUnavailable)` branch logging `"identity resolution failed"`.
The second is three lines per adapter and makes a genuinely operational failure mode greppable.
State whichever you pick; silence here is what turns a residual into a review finding later.

---

### F16 — the two NEW arms can co-match EACH OTHER, and the ADR's own standing invariant demands a test for exactly that. The plan has none — [MAJOR]

**Bundle text attacked:** ADR Decision 3 / spec §3.4, which invoke `httpcore/errors.go`'s
standing invariant as authority: *"for any arm added to this switch, state its position relative
to the arms it can co-match, **and carry a test asserting an error matching two arms resolves to
the intended one**."* The bundle then reasons **only** about new-arm-vs-existing-arm ordering
(*"it can co-match any arm … for an arbitrary payload that means first"*) and Task 2's test has
four cases, all new-vs-existing.

**What I ran (built change):**
```go
both := fmt.Errorf("%w: %w", httpcore.ErrIdentityUnavailable, httpcore.ErrUnauthenticated)
s, b := httpcore.ClassifyError(both)
```
**Observed:**
```
PROBE identity wrapping ErrNotAuthorized         => status=503 body={Error:identity_unavailable Message:}
PROBE identity wrapping ErrInstanceNotFound      => status=503 body={Error:identity_unavailable Message:}
PROBE identity wrapping ErrBadInput              => status=503 body={Error:identity_unavailable Message:}
PROBE identity wrapping ErrRequestBodyTooLarge   => status=503 body={Error:identity_unavailable Message:}
PROBE bare unauthenticated                       => status=401 body={Error:unauthenticated Message:the request carries no authenticated actor}
PROBE identity wrapping ErrUnauthenticated       => status=401 ...  (401 arm is FIRST so it wins)
PROBE errors.Is(both, ErrIdentityUnavailable)=true errors.Is(both, ErrUnauthenticated)=true
```

**Why the bundle is wrong:** `ErrIdentityUnavailable` wraps **arbitrary consumer code** — that
is the bundle's own stated reason for hoisting it to the top. Arbitrary consumer code includes
code that wraps `httpcore.ErrUnauthenticated`, which is **exported** and which
`RequestActorFunc`'s documented contract explicitly tells consumers to return. So the two new
sentinels co-match, `errors.Is` confirms it (`true`/`true`), and which one wins is decided
purely by their relative order — a decision the bundle never states and never tests. The
invariant it cites as its authority is violated by the very arms it adds.

The behaviour is arguably right (401 wins ⇒ an absent credential is not reported as an outage,
matching `resolveRequestActor`'s own `case errors.Is(err, ErrUnauthenticated)` first arm) — but
"arguably right and unstated and unpinned" is what the invariant exists to prevent, and a later
edit reordering the two would flip a 401 to a 503 with the whole suite green.

**Concrete fix:**
1. Add one sentence to ADR Decision 3 / spec §3.4: *"Between the two new arms, `ErrUnauthenticated`
   precedes `ErrIdentityUnavailable`: a resolver error that wraps `ErrUnauthenticated` is an
   absent credential (401), not an outage (503). This mirrors `resolveRequestActor`, whose first
   switch arm makes the same call."*
2. Add the fifth case to plan Task 2 Step 1's table:
   `"identity failure wrapping ErrUnauthenticated → 401, NOT 503"` with
   `fmt.Errorf("%w: %w", httpcore.ErrIdentityUnavailable, httpcore.ErrUnauthenticated)`.
   **What makes it fail:** swap the two arms and it reports 503. Its RED today: both sentinels
   are undefined.
3. Extend Task 2 Step 5's mutation to include that swap, not only "move both below the 404 arm".

---

### F17 — §3.5's "the per-adapter aliases are REQUIRED, not cosmetic" is CONFIRMED, error text and all — [confirmation]

**Bundle text attacked:** spec §3.5 / ADR Decision 2, *"R appears only in the result type, so
`httpcore.WithRequestActor(fn)` does not compile (\"cannot infer R\")"*.

**What I ran:** a throwaway `main` package in the built tree calling the generic form with no
type argument:
```go
var o httpcore.CustomizeOption[*http.ServeMux] = httpcore.WithRequestActor(fn)
```
**Observed:**
```
$ go vet ./zzinf/ ; EXIT=1
vet: zzinf/main.go:16:51: in call to httpcore.WithRequestActor, cannot infer R
     (declared at ./transport/http/httpcore/seam.go:146:1)
```
And the explicit form `httpcore.WithRequestActor[*http.ServeMux](fn)` compiles.

**Verdict:** exact, including the quoted error string. The three aliases are load-bearing. No
change needed. (Worth keeping: this is one of the bundle's claims that a reader would be most
tempted to take on faith and it is precisely right.)

---

### F18 — §5 row 8 — "Attributes reach `service.ClaimTaskRequest.Actor.Attributes`" — is NOT TESTED ANYWHERE the plan prescribes, despite the plan's self-review claiming it was closed — [MAJOR]

**Bundle text attacked:** spec §5 test table row 8, *"actor `Attributes` set on the context reach
`service.ClaimTaskRequest.Actor.Attributes` | today they are dropped at all three sites"*; and
the plan's closing section, *"**Gaps found and closed during review:** §5's row 8 (Attributes
reach `service.ClaimTaskRequest`) had no task — **folded into Task 4 Step 1's final case and
Task 5's endpoint tests.**"*

**What I ran:** read both destinations against the claim.
**Observed:**
- **Task 4 Step 1's final case** (`"a resolved actor arrives WHOLE, attributes included"`)
  asserts on the return value of **`resolveRequestActor`**:
  `assert.Equal(t, "finance", got.Attributes["dept"])`. That proves the *helper* does not drop
  attributes. It says nothing about `service.ClaimTaskRequest` — `resolveRequestActor` does not
  construct one.
- **Task 5 Step 1's endpoint tests** assert `http.StatusOK` / `authz.ErrNotAuthorized` /
  `httpcore.ErrUnauthenticated`. **No attribute assertion appears in any of the three cases.**

So the claim "folded into … Task 5's endpoint tests" is false as written: nothing in the plan
asserts an attribute crossing the `httpcore → service` boundary. The one assertion that exists
stops one function short of the seam row 8 names.

**Why it matters more than a bookkeeping slip:** this is the single **non-refusal** behaviour
change in the bundle (spec §6 target 4 says so), and F6/F7 show its destination is two durable
stores, not just the authorizer. It is the leg most worth pinning and the only one where a
half-implementation (endpoint keeps rebuilding `authz.Actor{ID:…, Roles:…}` from the resolved
actor instead of passing it whole) would be **silent**: every prescribed test still passes,
because every prescribed test checks only ID/roles-driven authorization outcomes.

**Concrete fix:** add a real case to plan Task 5 Step 1, asserting at the service boundary. Its
RED today: today the endpoints construct `authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles}`
(`endpoints.go:119,132,150`) and `Attributes` is dropped, so the assertion fails.
```go
"resolved attributes reach the service request": {
    setupToken: …,
    actor: func(context.Context) (authz.Actor, error) {
        return authz.Actor{ID: "alice", Roles: []string{"manager"},
            Attributes: map[string]any{"dept": "finance"}}, nil
    },
    assert: func(t *testing.T, …) {
        // read it back where it lands: the human-task claim record
        ht, err := h.TaskStore.Get(t.Context(), token)
        require.NoError(t, err)
        require.NotNil(t, ht.Claim)
        assert.Equal(t, "finance", ht.Claim.Actor.Attributes["dept"])
    },
},
```
I executed this shape in F6 through `svc.ClaimTask` and it produces
`{"actor":{"id":"alice","roles":["manager"],"attributes":{"clearance":7,"dept":"finance"}},…}`,
so the assertion is reachable. Also correct the plan's self-review line — it currently records a
gap as closed that is not.

---

### F19 — after this ships, the DEFAULT `Mount` still starts process instances and delivers messages with NO identity, while the task routes 401. The ADR's residual list does not mention it — [MAJOR]

**Bundle text attacked:** ADR Consequences/Negative, *"⚠ **Backlog 52 and 53 remain open, so this
is a narrowing and not a closure.** … The exposure goes from *anyone can be anyone* to **anyone
authenticated can be anyone the configured authorizer permits**. Backlog **90** and **124** are
untouched."* — presented as the complete statement of residual exposure, and the ADR's header
line *"Explicitly still open: **52**, **53**, **90**, **103**, **124**"*, which reads as
exhaustive. Also spec §4.1, whose only "not closed" clause is about the **engine** (`ApplyTrigger`,
`NewHumanCompleted`), i.e. about consumers who bypass HTTP entirely.

**What I ran (built change), against the *default* `stdlib.Mount(mux, svc)` with no resolver and
no middleware:**
```go
{"POST /instances (START a process)", "/instances", map[string]any{"def_ref": "approval"}},
{"POST /messages (DELIVER a message)", "/messages", map[string]any{"name": "x"}},
{"POST /tasks/{t}/claim", "/tasks/nonexistent/claim", map[string]any{}},
```
**Observed:**
```
PROBE bare mount, NO identity: POST /instances (START a process)    => status=201
PROBE bare mount, NO identity: POST /messages (DELIVER a message)   => status=202
PROBE bare mount, NO identity: POST /tasks/{t}/claim                => status=401
```

**Why the bundle is wrong:** the residual sentence *"anyone authenticated can be anyone the
configured authorizer permits"* describes the task routes and is silently generalised to the
mount. `stdlib.Mount` (`stdlib/mount.go:17-21`) registers `InstanceRoutes`, `TaskRoutes` **and**
`MessageRoutes`; of the routes it exposes, exactly three gain an identity. An unauthenticated
caller can still **start arbitrary process instances**, **deliver arbitrary signals** to running
instances (`POST /instances/{id}/signals`) and **deliver arbitrary messages** — including
message-start deliveries that create instances. So the post-change deployment is internally
inconsistent in a way a reader of the Consequences will not expect: the transport authenticates
*acting on* a task but not *creating the work*.

⚠ Separately: `AdminRoutes` is opt-in (not in `Mount`), but when a consumer mounts it, `POST
/admin/policies` and `POST /admin/role-bindings` write the **authorization policy itself** with
no identity — which would let an unauthenticated caller grant a role to a principal they *can*
authenticate as, and so re-open the task routes through the front door. I did not execute this
leg (it needs a `Policies` implementation wired), so I mark it
**ASSUMPTION (unverified): the admin policy/role-binding routes carry no identity check** —
derived from reading `stdlib/groups.go:324,362` and the absence of any actor plumbing there.

None of this makes ADR-0189 wrong — it closes what it claims to close, and F13 shows it closes
it properly. It makes the ADR's **statement of the remaining exposure** materially incomplete,
which is the thing CLAUDE.md's rule about residuals exists to prevent ("a documented residual is
still a shipped defect" — and an *undocumented* one is worse).

**Concrete fix:**
1. Add to ADR Consequences/Negative: *"⚠ Only the three task routes gain an identity.
   `stdlib.Mount`/`gin.Mount`/`fiber.Mount` also register `InstanceRoutes` and `MessageRoutes`,
   and after this record a bare mount still answers **201** to `POST /instances` and **202** to
   `POST /messages` with no identity at all — EXECUTED. Starting work is unauthenticated;
   acting on it is not. Authenticating the instance and message routes is a separate decision
   (they have no `Actor` today, so it is a new one, not a fix)."*
2. Add the corresponding sentence about `AdminRoutes` once verified, or file it as a new backlog
   item with the ASSUMPTION marker above.
3. Widen the ADR header's "Explicitly still open" line, or reword it to *"still open among the
   authorization backlog: 52, 53, 90, 103, 124"* so it stops reading as the full residual set.
4. `SECURITY.md` (plan Task 12 Step 1) must state which routes the library authenticates and
   which it does not — otherwise a consumer reads "ADR-0189 fixed the actor problem" and mounts
   the rest open.

**⚠ SELF-CORRECTION to F19's admin leg, before anyone acts on it.** I re-executed rather than
leaving the ASSUMPTION standing:
```
$ grep -n "AdminRoutes\|stdlib.Mount" examples/production_wiring/main.go
267:	// AdminRoutes has NO built-in authentication (ADR-0095: admin-by-composition).
274:	stdlib.AdminRoutes{Svc: svc}.Customize(adminMux, …)
```
The unauthenticated admin surface is an **existing, deliberate, documented** decision
(ADR-0095, "admin-by-composition"), already called out in `production_wiring/main.go:67,267`
and mounted on a separate mux. So it is **not** a gap this bundle opens or hides, and F19's
admin paragraph should be dropped rather than actioned — I withdraw it.

**F19's main point stands unchanged and is if anything sharper for the contrast:**
`InstanceRoutes` and `MessageRoutes` are in the **default `Mount`**, have no "by composition"
doctrine anywhere, and answer 201/202 to an unauthenticated caller after this record ships.
Fixes 1, 3 and 4 above still apply; fix 2 does not.

**Also re-verified while there — spec §2.5 holds:**
```
$ grep -in "ClaimTask\|CompleteTask\|ReassignTask" examples/{production,sqlite,mysql}_wiring/main.go
(no output)
```
None of the three wiring mains calls a task verb, so §2.5's "the three wiring mains never claim
a task" is correct and its downgrade of ADR-0185's failure-modes finding F5 is justified.
`production_wiring/main.go:264` already passes
`httpcore.WithMeterProvider[*http.ServeMux](meterProvider)`, so plan Task 11 Step 2's warning to
**append rather than replace** is right and load-bearing.

---

## Verdict

**This bundle does NOT survive as an input to implementation in its current form.** It is a
strong bundle — its two most-doubted claims (the fiber propagation trap, the "vacuous 403 pins
become loud" prediction) both re-executed **exactly right**, and its central design is sound and
genuinely fail-closed. But three findings must be resolved before code is written:

- **F12 (CRITICAL)** — the machine-derived blast radius is wrong because the ablation stubbed out
  one of the bundle's two breaking changes. 11 → **14** compile-breaking lines, 29 → **32** total.
  The word "exhaustive" is false, and the number is restated in three documents. An implementer
  following Task 5's file list will hit three compile errors the plan does not list.
- **F11 (CRITICAL)** — the bundle empties `ClaimInput` to a zero-field struct but leaves the
  claim route on a **required-body** decode. The correctly-migrated client, which sends no body,
  gets **400 on all three adapters** (executed). The prescribed tests cannot see it because they
  call the endpoint function directly and leave `map[string]any{}` behind in the adapter tests.
- **F18 (MAJOR)** — the plan's self-review records §5 row 8 (attributes reach the service
  request) as closed; it is not tested anywhere, and it is the one leg whose half-implementation
  would be silent.

The remaining MAJORs are statement-of-scope and residual failures rather than design errors —
but this repo's own history (0186's "a residual you wrote down is still a defect you shipped",
0187's "a parked residual is not a safe residual") says they become review findings if left:
F1/F2 (ADR-0147 and two `service` comments become false; "5 packages" is code-only), F4 (the
empty-ID rule is argued at engine level, enforced at transport level only), F5 (the clone
guarantee is over-claimed and its test is pinned to a weaker claim), F6/F7 (attributes now reach
two durable stores unbounded, and an unmarshalable attribute permanently bricks the instance
view), F9 (on fiber the resolver runs on `context.Background()`, so the promised 503 has an
unnamed third state — hang), F14 (401 now precedes 404; unstated, and it breaks a pin the plan
tells the gin agent to ignore), F16 (the two new arms co-match each other, untested, violating
the very invariant the ADR cites), F19 (start/message routes stay unauthenticated in the default
mount, absent from the residual list).

Four of the bundle's claims were attacked and **held exactly**: §2.2 fiber propagation (F8),
§2.3 unknown-key tolerance on real mounted routes (F10), §2.6's starred prediction (F13), §3.5's
"cannot infer R" (F17), plus §3.3 rule 3's durable-record premise (F3) and §2.5 (above). The
spec's §2 is honest work; its one methodological failure is F12, and it is the one place the
spec claimed to be "closed by construction".

### Findings by severity

| severity | count | ids |
|---|---|---|
| **CRITICAL** | 2 | F11, F12 |
| **MAJOR** | 9 | F1, F2, F4, F5, F6, F7, F9, F14, F16, F18, F19 → **11**, see note |
| **MINOR** | 1 | F15 |
| **confirmation (no severity)** | 5 | F3, F8, F10, F13, F17 |
| **withdrawn** | 1 | F19's admin-routes paragraph (self-corrected above) |

⚠ Count correction, applying this repo's own re-count rule to my own table: the MAJOR row is
**11**, not 9 — F1, F2, F4, F5, F6, F7, F9, F14, F16, F18, F19. Total findings **19**, of which
**14 are actionable** (2 CRITICAL + 11 MAJOR + 1 MINOR) and 5 are confirmations.

### Notes on method, for the adjudicator

- Everything above was **run** in the worktree at `7fa756d0`, not read. Where I confirm a bundle
  claim I re-derived it independently rather than reproducing the spec's probe.
- **I built the actual change** (`authz/context.go`, sentinels + first arms, `RequestActorFunc`
  + config field + option + `ResolveConfig` default, `resolveRequestActor`, the three DTO fields
  and `httpcore.Actor` removed, the three signatures, the nine adapter sites) to run F12/F13/F14/
  F16/F19. `go build ./...` was clean. This is throwaway exploratory code in a throwaway
  worktree — **it is not an implementation and must not be reused as one** (no tests, no TDD
  cycle, no godoc).
- One trap worth passing on: `transport/http/gin/gin_test.go:42`'s `post()` helper does
  `json.Marshal(nil)` and therefore sends the literal bytes `null`, **not** an absent body. A
  probe routed through it reports 403 where a real bodyless request reports 400. F11 was nearly
  a false negative because of it.
