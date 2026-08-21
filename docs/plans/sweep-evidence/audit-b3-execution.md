# Audit lens B3 — EXECUTION — 2026-08-20 authz/security bundle @ 3f317b63

Step 0: bundle PRESENT in worktree (spec, ADR-0185, ADR-0186, plan). Verified.

Findings below, appended as probes complete.

---

## F1 — MAJOR — `WithActorResolver` is an EXISTING public symbol with an unrelated meaning

**Claim attacked** (ADR-0185 D1, spec §4.1 Option C): *"Add
`httpcore.WithActorResolver(func(context.Context) (authz.Actor, error))` as an override…"*
The bundle introduces this name as if it were new. Spec §2.2 enumerates
`grep -n "^func With" service/options.go → 10 options` and concludes only that
none is a *standalone authorizer* setter — it read the list and did not notice
what else was in it.

**Probe:**
```
$ grep -n "^func With" service/options.go
 39:func WithProcessDriver   48:func WithInstanceStore   57:func WithDefinitions
 67:func WithLister          77:func WithHumanTasks      99:func WithActorResolver
109:func WithClock          120:func WithIDGenerator    146:func WithoutEmbeddedDefinition
169:func WithDurableStore
$ sed -n '99p' service/options.go
func WithActorResolver(r humantask.ActorResolver) Option {
$ grep -n "ActorResolver" humantask/humantask.go
170:// ActorResolver expands an eligibility spec together with process variables into
176:type ActorResolver interface {
```

**Observed:** `service.WithActorResolver` exists TODAY (`service/options.go:99`) and
takes `humantask.ActorResolver` — the port that expands an eligibility spec into
**candidate** actors (`humantask/humantask.go:170-176`), a *projection* concern.
It is documented in `CHANGELOG.md:498` and `INTERACTIONS.md:50,82,93`.
`authz/authz.go:34`'s own godoc already tells consumers to *"populate Attributes
from your [ActorResolver]"*, meaning the existing symbol.

**Verdict: CONFIRMED** — the bundle names an existing, semantically different
public symbol. Two exported `WithActorResolver` in one library, one meaning
"who *could* act" (candidates) and one meaning "who *is* acting" (the
authenticated principal), is a consumer-facing trap in exactly the API surface
CLAUDE.md calls the product.

**Proposed fix:** rename the new seam to something that cannot be confused with
candidate expansion — e.g. `httpcore.WithPrincipalResolver` /
`authz.PrincipalFromContext`, or `httpcore.WithActorFromRequest`. Whichever is
chosen, ADR-0185 must state the collision and why the chosen name is safe, and
spec §2.2 must stop implying `service/options.go` was read for anything but
authorizer setters.

---

## F2 — CRITICAL — `has(vars, "k")` DOES NOT EXIST in expr v1.17.8; the prescribed escape hatch always DENIES

**Claim attacked** (ADR-0185 Decision 4, verbatim): *"A key guarded by an
existence test (`"k" in vars`, `has(vars, "k")`) is treated as optional, so the
legitimate 'optional attribute' idiom survives."* Repeated in spec §4.3 Option A
and prescribed as a test in plan phase 1 test 3 and implied in phase 4.

**Probe** (`auditprobe/e_test.go`, compiled exactly as `internal/expreval` does —
`expr.Compile(code, expr.AllowUndefinedVariables())` then `expr.Run`, env
`{"vars": {"tier":"gold"}, "actor": Actor{ID:"a"}}`):

```
E1| has(vars, "tier")            out=<nil> err=invalid operation: cannot call nil (1:1)
E1| "tier" in vars               out=true  err=<nil>
E1| vars?.tier == "gold"         out=true  err=<nil>
E1| get(vars, "tier")            out=gold  err=<nil>
E1| vars.tier ?? "none"          out=gold  err=<nil>
```

**Observed:** `has` is not a builtin. `AllowUndefinedVariables()` resolves the
identifier `has` to nil, compilation succeeds, and the call fails at **run**
time. `RoleAuthorizer.Authorize` wraps any run error as `ErrNotAuthorized`
(`authz/authz.go:136-141`), so a predicate written to the ADR's own prescription
**denies every actor, permanently, with a message naming the predicate source.**

**Verdict: CONFIRMED — the ADR prescribes an escape hatch that does not exist,
and plan phase 1 test 3 as written CANNOT PASS.** `has(vars,"tier") and …`
errors before any strict-reference logic is reached. This is the ADR-0165-class
failure the audit brief warns about: a decision whose predicate refuses the
useful case.

**Proposed fix:** delete `has(vars, "k")` from ADR-0185 Decision 4, spec §4.3 and
plan phase 1 test 3. The forms that actually exist in v1.17.8 and were executed
here are `"k" in vars`, `vars?.k`, `vars.k ?? default` and `get(vars, "k")`.
Pick the guard set from *that* list, record the probe output in the spec, and
have phase 1 test 3 table exactly those.

---

## F3 — MAJOR — static reference extraction is DEPTH-1; nested absence still fails open

**Claim attacked** (ADR-0185 D4): *"the predicate's `vars.*` / `actor.*`
references are extracted statically … and evaluation denies when a referenced
key is absent from the env"* — stated as closing the class.

**Probe** (`ast.Walk` over `parser.Parse`, collecting `MemberNode` whose base is
the `vars`/`actor` identifier — the exact mechanism spec probe `[10]`
demonstrated):

```
E2| vars.order.total > 100                    refs=[vars.order]
E2| actor.attributes.clearance > 3            refs=[actor.attributes]
E2| vars[actor.ID] == "x"                     refs=[actor.ID vars.<dynamic:*ast.MemberNode>]
E2| len(vars) == 0 or vars.status != "blocked" refs=[vars.status]
E2| (vars | first()) != "blocked"             refs=[]
```

**Observed:** only the **first** level under `vars`/`actor` is extracted.
`vars.order.total` yields `vars.order`, so a deny-list predicate over a nested
absent key (`vars.order.total != "blocked"` with `vars.order` present but empty)
is invisible to the check and still fails open. Dynamic keys yield an
unresolvable reference the design does not say what to do with, and a
pipe/builtin form yields **zero** references, making the strict check a complete
no-op for that predicate.

**Verdict: CONFIRMED.** The mechanism is a heuristic over a syntactic subset,
not a closure of the class. ADR-0185 D4 and spec §2.4.1's *"the only mechanism
that works"* both over-claim.

**Proposed fix:** state the closed set the check actually covers (depth-1
`vars.<ident>` / `vars["literal"]` and the `actor` equivalents) and decide
explicitly what happens for the three residual shapes — nested chains, dynamic
keys, and zero-reference predicates. Recommended: deny on a **dynamic** or
**zero-reference** `vars`/`actor` predicate rather than allow, since "the
extractor could not prove what this reads" is the fail-closed answer; and either
walk chains to full depth or say in the ADR that nested absence is out of scope.

---

## F4 — MAJOR — the 400 arm the bundle deliberately PRESERVES echoes submitted variable VALUES

**Claim attacked:** spec §4.7's *"⚠ Open check for the audit, not resolved here:
`validation.ErrInvalidInput` reaches the 400 arm. Whether its message can contain
submitted variable **values** was **not** verified. `ASSUMPTION (unverified)`"*,
and ADR-0186 Decision 5's table which nonetheless blesses **400 / 404 / 409 / 422
unchanged (these are the actionable ones)** and Consequences' *"the four that
carry useful information keep it"*.

**Probe** (`auditprobe/v_test.go`, real `validation.NewGate()` + the repo's own
`jsonschema` strategy, input `{"ssn": "123-45-6789"}`):

```
V2b| workflow-validation: invalid input: workflow-validation/jsonschema:
     jsonschema validation failed with 'mem://schema.json#'
     - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'
V2c| - at '/ssn': maxLength: got 11, want 3
V2a| - at '/ssn': value must be one of 'a', 'b'
V1|  - at '/ssn': got string, want integer
```

**Observed:** the `pattern` constraint reproduces the **submitted value
verbatim** (`'123-45-6789'`) inside the message that `ClassifyError` copies into
`ErrorBody.Message` for 400 (`transport/http/httpcore/errors.go:50`). `enum`
additionally discloses the schema's allowed set. `type` and `maxLength` do not
echo the value.

**Verdict: CONFIRMED — the ASSUMPTION resolves AGAINST the bundle.** ADR-0186
Decision 5 keeps the 400 message on the reasoning that it is "actionable", and
the only leak it closes is 403's. A `pattern`-constrained field (exactly the
constraint used for national-ID / card / account-number shapes) round-trips its
value into an HTTP error body. Worse, plan phase 7 test 3's *"Second control:
assert 400 and 409 messages are still present, so the fix cannot over-blank"*
would **pin this leak into the test suite**.

**Proposed fix:** ADR-0186 Decision 5 must decide 400 on evidence, not on the
unverified assumption. Options to adjudicate: (a) keep 400's *structure* but
route validation failures through a value-free rendering (path + constraint
name, dropping the offending value); (b) treat validation failures as their own
class with a static message plus the correlation id, keeping the raw text in the
`Logger` sink like 403; (c) accept it explicitly and say so in `SECURITY.md`.
Whichever is chosen, phase 7 test 3's second control must assert on
**`Error` (the code)** rather than on message presence, or it locks in the leak.

---

## F5 — MAJOR — plan phase 9's actor work is REDUNDANT: the request ctx already reaches `httpcore` in all three adapters

**Claim attacked:** plan phase 9, for each of stdlib/gin/fiber: *"call
`cfg.ActorResolver(c.Request.Context())` (or the framework equivalent) and place
it via `authz.ContextWithActor` before delegating to `httpcore`."* Also spec
§4.1 Option B's rejection reasoning — *"a resolver that must read headers needs
the adapters to stuff the request into ctx first"*.

**Probe** (`auditprobe/ctx_test.go`): mounted the **real** `TaskRoutes` of all
three adapters against a `service.Service` stub that reports the ctx value it
received in `ClaimTask`, with consumer middleware placing a value in the request
context:

```
CTX| stdlib  status=500 ctxValue="STDLIB-ACTOR"
CTX| gin     status=500 ctxValue="GIN-ACTOR"
CTX| fiber   status=500 ctxValue="FIBER-SETCONTEXT"
CTX| fiberL  status=500 ctxValue=""          <-- c.Locals does NOT propagate
```

**Observed:** a consumer-middleware context value **already** reaches
`httpcore.ClaimTask`'s `ctx` today, in all three adapters, with **zero adapter
changes** (`stdlib/groups.go:146` `req.Context()`, `gin/groups.go:155`
`gc.Request.Context()`, `fiber/groups.go:139` `c.Context()` — and fiber v3's
`DefaultCtx.Context()` returns the user-set context, `ctx.go:134-144`).

**Verdict: CONFIRMED / REFUTED-IN-PART.** Option C is *more* viable than the
bundle claims — good — but plan phase 9's actor half is unnecessary work in
three places, and doing it would duplicate resolver invocation and error
classification across three adapters instead of once in `httpcore` (phase 7).

Two consequences the bundle does not carry:

1. **`c.Locals` is the idiomatic fiber way and it does NOT work.** A fiber
   consumer's auth middleware will reach for `c.Locals("actor", …)`; the value
   never reaches `httpcore`, so the actor is the zero actor and the call is
   silently denied. ADR-0185 Decision 1's *"any middleware, any framework"* must
   name `c.SetContext` explicitly for fiber, and `SECURITY.md`/the examples must
   show it.
2. **There is no 401 arm.** `ClassifyError` (`errors.go:26-60`) has no
   `Unauthorized` case and ADR-0186 Decision 5 adds only 413. With a fail-closed
   identity model, "no actor in context" and "the `ActorResolver` returned an
   error" both collapse into a 403 from the authorizer — indistinguishable from
   "authenticated but not permitted". The plan never states what status a
   resolver error yields.

**Proposed fix:** delete the actor half of phase 9 (keep the body cap half),
resolve the actor once in `httpcore`, and add to ADR-0185 Decision 1: the fiber
middleware idiom is `c.SetContext`, and an `ActorResolver` error maps to a new
401 arm (or an explicitly-decided 403) in `ClassifyError`.

---

## F6 — CRITICAL — `ConditionEvaluator`+ctx checks the WRONG invariant, and costs ~10x on the engine's hottest eval path

**Claim attacked** (ADR-0186 Decision 2 / plan phase 3a): *"`Step` already carries
a ctx and the engine core already imports `context`, so core purity
(`engine/purity_test.go`, which forbids OTel and clockwork imports) is
unaffected — a `ctx` is not a wall clock."* Plan 3a: *"confirm the purity test
still passes rather than assuming it."*

**Probe 1 — read the invariant that is actually locked** (`engine/conditions.go:29-43`,
verbatim):

```
// The wall-clock evaluation guard (expreval.WithTimeout, ADR-0049) is explicitly
// DISABLED here: the engine core must stay wall-clock-free and SIDE-EFFECT-FREE
// (locked invariant, ADR-0003), so the default Step never spawns the guard's
// goroutine/timer.
// A consumer that needs the DoS guard ... supplies its own timeout-capable
// ConditionEvaluator ... that is an explicit opt-in TRADING THE
// DETERMINISTIC-REPLAY GUARANTEE for DoS protection.
var conditions = expreval.New(expreval.WithTimeout(0))
```

`engine/purity_test.go` checks the **import list** (`clockwork`, OTel, :170) and
`time.` calls (:204). It cannot observe determinism. So "purity_test passes" is
not evidence for the property ADR-0003/0049/0056 actually locked.

**Probe 2 — the mechanism.** `expreval.run` (`internal/expreval/expreval.go:74-76`):
`if e.timeout <= 0 { return expr.Run(p, env) }` — the engine default is a
**synchronous** call with no goroutine and no select. There is no mechanism by
which a `ctx` cancellation can interrupt it. Plan phase 1 test 1
(`TestEvalBoolContextHonoursCancellation`) can therefore only pass if
`EvalBoolContext` **always** takes the goroutine path.

**Probe 3 — the cost of taking it** (`auditprobe/bench_test.go`, Apple M4 Pro,
predicate `vars.amount > 100`, i.e. an ordinary gateway condition):

```
BenchmarkEngineDefaultSynchronous-14   11995002    99.43 ns/op    64 B/op   3 allocs/op
BenchmarkGuardedGoroutinePath-14        1257207   965.20 ns/op   488 B/op   9 allocs/op
```

**~9.7x slower and 7.6x the allocations, per gateway condition, on the token
step loop** — CLAUDE.md's named hot path.

**Verdict: CONFIRMED.** ADR-0186 Decision 2 justifies itself against an
invariant (`imports`) that is not the one at risk (`determinism`, `no
goroutine`), and never states that honouring a ctx forces the engine's default
evaluator onto the goroutine path, at measured ~10x. It also silently reverses
ADR-0056's explicit trade ("deterministic replay OR DoS protection, consumer
chooses") into "everyone gets DoS protection and nobody gets deterministic
replay" — without amending ADR-0003/0049/0056, which are only listed under
"Relates to".

**Proposed fix:** decide this explicitly rather than by side effect. Either
(a) `EvalBoolContext` keeps the synchronous path when `timeout <= 0` **and** the
ctx has no deadline — then say plainly that the ctx bounds nothing on the engine
default path, and rewrite plan phase 1 test 1 (as written it would be testing a
path the engine never takes: a **vacuity** risk, since it would pass against the
guarded evaluator while the default stays unbounded); or (b) accept the
goroutine path, record the 99 ns -> 965 ns measurement in ADR-0186, and amend
ADR-0003/0049/0056 in this bundle per rule #11. Option (b) also needs the
deterministic-replay consequence written down.

---

## F7 — CRITICAL — the claimant guard (D5) is bypassed by Reassign→Complete, and the ADR names Reassign as a MITIGATION without noticing it is the BYPASS

**Claim attacked** (ADR-0185 Decision 5 + Consequences): *"A claimed task may only
be completed by its claimant … The claimant guard can strand a task whose
claimant has left the organisation. Mitigation: `Reassign` already exists and is
authorized separately; the guard deliberately does not touch it."*

**Probe — source-executed trace of the reassign path:**

```
engine/step_triggers.go:643
    task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}, At: t.OccurredAt()}

runtime/task/service.go:227-238  (Reassign)
    var claimant string; if task.Claim != nil { claimant = task.Claim.Actor.ID }
    if from != claimant { ...error... }
    if err := s.authz.Authorize(ctx, task.Eligibility, by, task.Vars); err != nil { ... }
    return engine.NewHumanReassigned(..., taskID, from, to, by), nil

service/request.go:77-87  (ReassignTaskRequest)
    From string   // body
    To   string   // body, UNVALIDATED
    By   authz.Actor  // becomes context-derived under D1
```

**Observed:** `Reassign` authorizes `By` against **`task.Eligibility` only** —
the exact set-membership check ADR-0185's own Context §5 says *"cannot
distinguish the claimant from any other eligible actor"*. `To` is an arbitrary
unchecked string, and the resulting `Claim.Actor` is `{ID: t.To}` with no roles
and no attributes.

**The bypass:** mallory, eligible but not the claimant, issues
`POST /tasks/{tok}/reassign {"from":"alice","to":"mallory"}` then
`POST /tasks/{tok}/complete`. `task.Claim.Actor.ID` is now `"mallory"`, so D5's
comparison **passes**. The only input mallory needs is the current claimant's
id — which the instance/task read path discloses by design (ADR-0147,
`handleHumanClaimed:585-587` *"the instance view renders who claimed and when"*),
i.e. backlog **54**, an item in this very bundle, supplies the bypass parameter.

**Verdict: CONFIRMED.** Decision 5 closes the direct path and leaves a two-call
path open, and the ADR's Consequences section presents the second call as the
*mitigation* for the stranded-task risk without noticing it is also the
escalation. Scoping backlog **90** out (spec §7, ADR §Decision 5) covers the
*claim* path; nothing in the bundle covers the *reassign* path.

**Proposed fix:** ADR-0185 Decision 5 must state the residual path explicitly and
choose one of: (a) restrict `Reassign` to the current claimant or a
privileged reassigner role/privilege (the same seam Decision 3's `Privileges`
leg opens); (b) record `HumanReassigned` provenance so a completion by an actor
who reassigned the task to themselves is refused or flagged; (c) accept it and
say so in `SECURITY.md` and the ADR Consequences, with the disclosure
dependency on item 54 named. Silence is not an adjudication. Plan phase 3b's
three-case table must gain a fourth case — reassign-to-self then complete —
whichever way it is adjudicated.

---

## F8 — MINOR — confirmed-as-stated claims (recorded so the controller can see what held up)

Executed and **CONFIRMED**, no action needed:

- **51 / D1 premise.** Three body-derived actor sites at
  `transport/http/httpcore/endpoints.go:119,132,150` (verbatim match).
  `CustomizeConfig` (`seam.go:19-33`) declares exactly `BasePath`, `Wrap`,
  `InstanceMapper`, `Logger`, `TracerProvider`, `MeterProvider` — no identity
  seam. `Attributes` dropped at all three sites.
- **52.** `service/service.go:199-200` `if c.authz == nil { c.authz = authz.AllowAll{} }`;
  summary logged at `slog.LevelDebug`; `service/options.go` has exactly 10
  `With*` options and none is a standalone authorizer setter.
- **53 / D2.** Probe A: zero spec, `Roles: []string{}`, `Roles: nil`, and
  `Privileges`-only **all** return `err=<nil>` for the **zero actor**. The
  `Privileges`-only leg is real.
- **103 / D3 premise.** Probe B: all five deny-list forms ALLOW with
  `vars = map[string]any{}`; `vars.region == "eu"` correctly DENIES;
  `actor.attributes.clearance > 3` DENIES *with* the predicate source in the
  message.
- **§2.4.1 refutation.** Probe C: with `expr.Env(...)` and **no**
  `AllowUndefinedVariables`, all five deny-list forms still return
  `out=true err=<nil>`. The spec's refutation of the triage's fix holds.
  ⚠ One row the spec's table omitted: with `expr.Env`,
  `actor.attributes.clearance > 3` becomes a **compile** error
  (`type authz.Actor has no field attributes`) rather than a run error — a
  behaviour change the "identical verdicts" summary does not cover.
- **99 / MaxNodes inversion.** Probe F:
  `MaxNodes NEVER called -> exceeds maximum allowed nodes (1:10002)`;
  `MaxNodes(0) -> err=<nil>`; `MaxNodes(50) -> exceeds … (1:52)`. Inversion
  CONFIRMED; `DefaultMaxNodes=1e4` is active today.
- **99 / two evaluator surfaces.** `authz/authz.go:23` `expreval.New()` ->
  `timeout = DefaultTimeout = 5s` (`internal/expreval/expreval.go:25,61`);
  `engine/conditions.go:43` `expreval.New(expreval.WithTimeout(0))` -> guard off.
  Two distinct surfaces, as the spec corrects the triage.
- **104.** `transport/http/httpcore/errors.go` echoes `err.Error()` on exactly
  five 4xx arms — 404 `:31`, 403 `:33`, 409 `:35`, 400 `:50`, 422 `:56` — and
  blanks 500 `:58`. Probe G1 reproduced a 403 carrying the predicate source
  verbatim inside `%q` plus expr's own snippet.
- **98 counts.** `stdlib` 13 `json.NewDecoder`, `gin` 13 `ShouldBindJSON`,
  `fiber` 13 `c.Bind().JSON`, `httpcore` 0 — 39 total, all in `groups.go`;
  `grep -rnE "MaxBytesReader|BodyLimit" transport/` (non-test) -> **0**.
  `BodyParser` -> 0 hits (name rot confirmed). fiber's 4 MiB is
  `fiber/v3@v3.4.0/app.go:585 DefaultBodyLimit = 4*1024*1024`, applied at
  `app.go:709-710,1516` as `server.MaxRequestBodySize` — the framework's, not
  wrkflw's. CONFIRMED.
- **54.** `transport/http/httpcore/view.go:31` `Variables: st.Variables` — alias,
  not a copy. CONFIRMED.

---

## F9 — MINOR — citation drift and an over-tight enumeration on the 124 premise

- `handleHumanCompleted` is declared at `engine/step_triggers.go:**849**`, not
  `:839` (spec §2.5, ADR-0185 finding 5, plan phase 3b all say 839). Its body
  ends at `:973`, so the spec's re-derivation window
  `awk 'NR>=839 && NR<=960'` both starts 10 lines early and **stops 13 lines
  short of the function's end**.
- Re-run over the true body (`NR>=849 && NR<=973`): still **one** hit for
  `Claim|Candidates|Eligibility` and it is the comment, and `Actor: t.Actor` is
  the only actor write. **The conclusion survives**; the window used to
  establish it did not cover the function.
- `authz.AuthzSpec` godoc is at `authz/authz.go:79-86` ✓; `Privileges` note at
  `:119-120` ✓; `RoleAuthorizer.Authorize` at `:124` ✓ — those held.

**Proposed fix:** correct `:839` -> `:849` in three documents and re-state the
window as the function's true extent, per Premise Discipline ("prefer symbol
names over line numbers").

---

## F10 — MINOR — ADR-0185 D2's `WithDurableStore` change contradicts a DOCUMENTED precedence contract, for six leaves

**Claim attacked** (spec §2.2): *"`WithDurableStore` sets `c.taskStore` from the
provider, so the durable wiring must be written `WithDurableStore(p)` **then**
`WithHumanTasks(nil, az)`; the reverse order loses one or the other depending on
option order. **This ordering trap is the real ergonomics defect and is absent
from the backlog text.**"* And ADR-0185 D2: *"the provider's leaves are applied
as defaults for leaves the consumer did not set explicitly, so option order
stops changing the outcome."*

**Probe (source):**
```
$ grep -rn "c\.authz *=" service/*.go | grep -v _test
service/options.go:83:   c.authz = az        (inside WithHumanTasks, guarded by az != nil)
service/service.go:200:  c.authz = authz.AllowAll{}
$ sed -n '157,161p' service/options.go
// Precedence is last-writer-wins in option order: a finer per-leaf override
// (e.g. WithInstanceStore) placed AFTER WithDurableStore replaces that single
// leaf; placed before, it is overwritten by the provider. A nil provider is
// ignored.
```

**Observed:** `WithDurableStore` **never writes `c.authz`** — the only writers
are `WithHumanTasks` (nil-guarded) and the `AllowAll` default. So the authorizer
is *not* lost to option order in either direction; the "loses one or the other"
half of the spec's sentence is **REFUTED**. Only `c.taskStore` competes, and the
competition is an **explicitly documented, intentional** last-writer-wins
contract covering all six provider leaves.

**Verdict: PARTLY REFUTED.** Calling it a "trap absent from the backlog text" is
wrong: it is written down at `service/options.go:157-160`. More importantly,
ADR-0185 D2's fix changes that documented contract for **all six leaves**, so
`WithInstanceStore` placed *before* `WithDurableStore` would start winning where
it is documented to lose — a behavioural break for existing consumers that is
**not** in ADR-0185's four-item BREAKING list, and plan phase 5 test 3's name
(`TestDurableStoreOptionOrderIsIrrelevant`) asserts exactly the general
proposition that breaks the documented per-leaf-override idiom.

**Proposed fix:** either scope the change to `taskStore`+authorizer only and say
so, or list the precedence change as a fifth breaking change, update
`WithDurableStore`'s godoc in the same bundle, and rename phase 5 test 3 to the
narrow proposition it actually intends.

---

## F11 — CRITICAL — D3 strands every IN-FLIGHT human task on upgrade; the bundle's migration story covers definitions only

**Claim attacked** (ADR-0185 Consequences): *"**Every existing definition with no
eligibility becomes invalid** until it declares `open: true` or a dimension.
This is the intended cost … `CHANGELOG.md` must carry it with a copy-pasteable
diff."* And the deployment-order gate (spec §7, plan §4), which considers only
the **older-binary-reads-newer-row** direction.

**Probe (source-executed trace):**

```
engine/step_nodes.go:723-730   -- eligibility is FROZEN into the task record at creation
    spec := authz.AuthzSpec{Roles: ut.EligibleRoles, Privileges: ut.EligiblePrivileges,
                            Attribute: ut.EligibleExpr}
    ht := humantask.HumanTask{ ..., Eligibility: spec, State: humantask.Unclaimed, ... }

humantask/humantask.go:96-97   -- persisted, NO json tags
    Eligibility authz.AuthzSpec

runtime/task/service.go:199,234,255,306   -- all four Authorize sites read the STORED task
    s.authz.Authorize(ctx, task.Eligibility, actor, task.Vars)
```

**Observed:** the four authorization sites never re-derive eligibility from the
definition; they read `task.Eligibility` off the persisted `HumanTask`. The field
has no json tags and `AuthzSpec.Open` is a `bool`, so a **newer** binary
rehydrating a **pre-upgrade** row decodes `Open == false`.

**Consequence the bundle does not carry:** under Decision 3, every human task
that was open at upgrade time and was authored under the (formerly legal,
ADR-0117-blessed) no-eligibility shape becomes **permanently deniable** — not
claimable, not completable, and not reassignable, since `Reassign` authorizes
against the same frozen spec (`:234`). Re-authoring the definition with
`open: true` does **not** repair them: the spec was snapshotted at task creation.
The bundle's mitigation ("declare `open: true` and ship a CHANGELOG diff")
addresses definitions, which are re-authorable, and says nothing about in-flight
instances, which are not.

**Verdict: CONFIRMED — this is the migration direction that strands live work,
and it is the opposite of the one plan §4 gates.**

**Proposed fix (adjudicate one):**
1. Make `Open` a **tri-state** in the persisted shape (`*bool`, or an
   `Unstated|Open|Restricted` enum) so "absent in the row" is distinguishable
   from "explicitly not open", and treat *absent* as open for rows written by a
   pre-upgrade binary. This is the only option that needs no operator action.
2. Ship a migration that rewrites `Open` into existing open-task snapshots —
   requires touching every dialect and every stored row, and the plan has no
   persistence phase at all (phase table §2 lists no `internal/persistence/*`).
3. Keep the boolean and state the operational contract: **drain all open human
   tasks before upgrading.** For a workflow engine whose human tasks are
   explicitly long-lived (`DueAt`, working-day deadlines, in-wait reminders),
   this is close to unshippable and must be labelled as such.

Whichever is chosen, plan §4's gate must cover **both** directions, and the
phase table needs a persistence/migration phase or an explicit "none needed,
because …".

---

# RANKED INDEX

| # | sev | finding | verdict |
|---|---|---|---|
| F2 | **Critical** | `has(vars,"k")` is not an expr v1.17.8 builtin — the prescribed escape hatch errors at run time and DENIES everyone; plan phase 1 test 3 cannot pass | CONFIRMED |
| F7 | **Critical** | D5's claimant guard is bypassed by Reassign→Complete; the ADR names Reassign as the *mitigation* without noticing it is the *bypass* | CONFIRMED |
| F11 | **Critical** | D3 permanently strands every in-flight human task on upgrade (eligibility is frozen at task creation, `Open bool` decodes false); plan §4 gates the opposite direction | CONFIRMED |
| F6 | **Critical** | `ConditionEvaluator`+ctx justified against the wrong invariant (imports, not determinism) and forces the engine default onto a goroutine path measured at **99 ns → 965 ns, 3 → 9 allocs** | CONFIRMED |
| F1 | Major | `WithActorResolver` already exists in `service` with an unrelated meaning (candidate expansion) | CONFIRMED |
| F3 | Major | static reference extraction is depth-1; nested absence, dynamic keys and zero-reference predicates all still fail open | CONFIRMED |
| F4 | Major | the 400 arm the bundle deliberately preserves echoes **submitted values** (`pattern` constraint); phase 7 test 3's control would pin the leak in | CONFIRMED (assumption resolved against the bundle) |
| F5 | Major | plan phase 9's actor work is redundant — the request ctx already reaches `httpcore` in all three adapters; `c.Locals` silently does not; no 401 arm exists | CONFIRMED / partly REFUTED |
| F10 | Minor | D2's `WithDurableStore` change breaks a *documented* six-leaf precedence contract; the "loses the authorizer" half of the premise is refuted | PARTLY REFUTED |
| F9 | Minor | `handleHumanCompleted` is at `:849` not `:839`; the re-derivation window missed 13 lines of the body (conclusion survives) | CONFIRMED w/ correction |
| F8 | — | everything else executed held exactly as written (51, 52, 53, 103 premises; §2.4.1 refutation; MaxNodes inversion; two-evaluator correction; 104's five arms; 98's 13/13/13/0 and fiber's own 4 MiB; 54's alias) | CONFIRMED |

**Probes run:** `auditprobe/{a,e,g,v,ctx,bench}_test.go` (throwaway package inside
the worktree, deleted after transcription — all outputs pasted verbatim above).
No repo file was modified. Container-free throughout; no Docker used.
