# Spec — authorization & security hardening (backlog 51, 52, 53, 54, 65, 98, 99, 100, 101, 103, 104, 124, + parked 102)

> ## ⚠ SUPERSEDED IN PART — 2026-08-21. The ADR-0186 half has MOVED to its own delivery.
>
> B3 was re-cut into three deliveries (owner decision, 2026-08-21). The
> untrusted-input-and-disclosure half now lives in its own bundle and **that is the
> authoritative version of it**:
> `docs/specs/2026-08-21-untrusted-input-and-disclosure.md` +
> `docs/adr/0186-untrusted-input-and-disclosure-posture.md` +
> `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`.
> ⚠ **Do NOT implement ADR-0186 material from this document** — six re-audit findings
> were folded into the new bundle and are not reflected here.
>
> What remains live here is the **ADR-0185 (identity) material**, which is **NOT yet
> re-cut** and still carries both failed audits' findings unresolved — in particular
> D3's two confirmed defects (`AuthzSpec` is durable in TWO places, and `Open *bool`
> makes a PUBLIC struct's zero value fail-OPEN) and the deferral of D4 (backlog 103)
> and D5 (backlog 124) to their own bundles.

> ## ⛔ RE-AUDIT FAILED — 2026-08-21. NOT an input to implementation.
>
> This is the **second** failed audit for this bundle. The 2026-08-20 draft failed on
> individual decisions; this revision fixed those and **failed on the interactions
> between the decisions it rewrote** — ~13 distinct Criticals across three lenses, two
> of them found independently by two lenses each. D4's strict-reference mechanism is
> the wrong *shape* (three rounds, three disjoint hole sets); D5's privilege token
> bricks all four verbs because one spec serves four; D3 targets the wrong one of two
> durable locations and its `*bool` makes a **public** struct's zero value fail-OPEN;
> ADR-0186 D5's value-free 400 is not implementable where prescribed.
> ⚠ Three findings are in the bundle's own evidence file.
> See `docs/plans/sweep-evidence/reaudit-b3-adjudication.md`.
> **A scope decision is pending before any further revision.**
>
> The 2026-08-20 draft failed its rule-#9 audit: **58 findings, 12 Critical**, all
> accepted (`docs/plans/sweep-evidence/audit-b3-adjudication.md`, with three lens
> reports beside it). **Four ADR Decisions changed**, so per rule #9 this bundle has
> **not** been audited and is **not** an input to implementation. The next action is
> the re-audit; `docs/plans/2026-08-20-authz-security-hardening.md` §0 lists what it
> must attack.
>
> Executed evidence for everything the revision newly asserts:
> **`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`**.

- Date: 2026-08-20, revised 2026-08-21
- **Anchor: this bundle's own commit** on `design/authz-security-b3`, rebased onto
  `main` after the backlog sweep merged. ⚠ The draft declared a base of `70a631e9`
  and then cited two revisions with no marker — §2.10's symbols did not exist at the
  declared base at all — which is what rotted its `engine/step_triggers.go`
  citations. **Every line citation below was re-derived at the revision commit**, and
  where a file is volatile the citation is a **symbol**, not a line.
- Bundle: this spec + `docs/adr/0185-authorization-identity-is-not-self-asserted.md`
  + `docs/adr/0186-untrusted-input-and-disclosure-posture.md`
  + `docs/plans/2026-08-20-authz-security-hardening.md`
  + `docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`

---

## 1. Problem

`wrkflw` ships an authorization model (roles, resource privileges, attribute
predicates over process variables) and an HTTP transport surface a consumer mounts
into their own server. Thirteen triaged backlog items say, in aggregate, that the
model is **fail-open at every layer it passes through**, and that the transport is
unbounded on the way in and over-disclosing on the way out.

The failure is not one bug. It is a chain in which each link independently
neutralises the next:

| layer | today | item |
|---|---|---|
| who is the actor? | whatever the **request body** says | **51** |
| which authorizer runs? | `authz.AllowAll{}` by default, logged at DEBUG | **52** |
| what does the spec mean? | an empty spec means **allow-all**; a `Privileges`-only spec is *also* allow-all; and a `{Roles, Privileges}` spec **silently drops the privilege** | **53** |
| what does a deny-list predicate do on a missing variable? | **allows** | **103** |
| does completion check the claimant? | **no** — `t.Actor` is written straight through | **124** |
| …and if it did? | `Reassign` overwrites the claim on the **same** eligibility check | **124** (found by the audit) |
| what comes back out? | variables aliased, not copied; a 403 echoing the predicate source; a 400 echoing **submitted values** | **54**, **104** |
| what goes in? | no body cap, no variable cap, no evaluation-input bound, no SSRF guard | **98**, **99**, **65** |
| what is on disk? | plaintext snapshot + plaintext journal, no integrity chain | **100**, **101** |
| and if you follow our own advice? | the **baseline** authorizer has none of the fixes | **(found by the audit)** |

Fixing any single link leaves the chain intact. **51 + 52 + 53 must ship as a set**,
and 103 + 124 belong with them.

### 1.1 What this bundle is not

- Not a claim that `wrkflw` is exploitable **as shipped in-repo today** over HTTP.
  There is **no definition-deploy route** (verified: 26 routes = 9 non-admin + 15
  admin + 2 health in `transport/http/stdlib/groups.go`, none accepting a process
  definition), so expression *source* is not attacker-supplied over the wire. The
  expression hazards (99, 103, 65) are reachable by an author, not by an anonymous
  caller.
- Not the data-protection delivery. 100 (at-rest codec) and 101 (tamper-evident
  journal) are **decided as a posture** and their *mechanisms* deliberately deferred
  — ADR-0186 Decision 6.

---

## 2. Verified current behaviour

Everything in this section was **executed** or read from source **at the revision
commit**. Claims that could not be executed are labelled `ASSUMPTION (unverified)`.

### 2.1 The principal is self-asserted (51)

`transport/http/httpcore/endpoints.go` builds `authz.Actor` at exactly **three**
sites, each from the request DTO — and they are the only three `authz.Actor{…}`
constructions in `transport/`, non-test:

```
:119  Actor: authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles}   // claim
:132  Actor: authz.Actor{ID: in.Actor.ID, Roles: in.Actor.Roles}   // complete
:150  By:    authz.Actor{ID: in.By.ID,    Roles: in.By.Roles}      // reassign
```

`httpcore.CustomizeConfig` (`seam.go:19-33`) declares exactly `BasePath`, `Wrap`,
`InstanceMapper`, `Logger`, `TracerProvider`, `MeterProvider` — six fields, **no
identity seam** — so consumer middleware has no supported way to supply one.
`authz.Actor.Attributes` (`authz/authz.go:38`) exists but is dropped at all three
sites, so ABAC predicates over actor attributes can never be satisfied over HTTP
(103's second leg).

⚠ **The request context, however, already arrives intact.** The audit's execution
lens mounted the real `TaskRoutes` of all three adapters behind consumer middleware
and observed the value reaching `httpcore.ClaimTask`'s ctx with **zero adapter
changes** (`stdlib` `req.Context()`, `gin` `gc.Request.Context()`, `fiber`
`c.Context()`) — and that fiber's `c.Locals` does **not** propagate. This is
load-bearing for deleting the adapter half of the identity work.
`ASSUMPTION (unverified)` **here**: it was executed by the audit, not re-executed for
this revision. The re-audit must re-run it.

### 2.2 The default authorizer is `AllowAll` (52)

- `service/service.go:199-200` — `if c.authz == nil { c.authz = authz.AllowAll{} }`.
- ⚠ **The level is at `service/service.go:323`** —
  `slog.Default().LogAttrs(ctx, slog.LevelDebug, …)`. The draft cited `:315-317`,
  which computes the allow-all **label** (`authzLabel := "custom"` …). That is the
  dangerous kind of rotted citation: it lands on plausible, related, wrong code.
  The one record at `:323` carries store, definitions, taskStore, authz and a hint,
  so "log allow-all at WARN" **cannot** be done by changing that constant.
- `service/durable.go:17-24` — `DurableProvider` has **six** methods and no
  `Authorizer()`.
- `grep -n "^func With" service/options.go` → **10** options, none a standalone
  authorizer setter. The only way in is `WithHumanTasks(taskStore, az)` (`:77`).
- ⚠ **The "ordering trap" is narrower than the draft claimed.** `WithDurableStore`
  (`options.go:169-181`) **never writes `c.authz`** — the only writers are
  `WithHumanTasks` (nil-guarded, `:83`) and the `AllowAll` default — so the wiring
  the draft named, `WithHumanTasks(nil, az)`, is **already order-independent** in
  both directions. The real trap is `WithHumanTasks(myStore, az)` written *before*
  `WithDurableStore(p)`, whose `myStore` is silently replaced. And the precedence it
  would change is **documented, intentional** last-writer-wins covering all six
  provider leaves (`options.go:157-160`), so the fix must be scoped to `taskStore`
  or it becomes a fifth breaking change.

### 2.3 An empty spec is allow-all — and so is a privileges-only spec, and a mixed one drops the privilege (53)

`authz/authz.go:79-86` godoc says verbatim *"An empty spec means allow-all."*
`RoleAuthorizer.Authorize` (`:124`) short-circuits on `len(spec.Roles) == 0` and
returns `nil` when `spec.Attribute == ""`. `AuthzSpec.Privileges` is documented at
`:119-120` as reserved and **not evaluated**; `grep -rn 'spec\.Privileges'` finds no
reader in `authz` at all.

**Executed** — all four shapes admit the **zero actor**:

```
zero spec                actor=<zero> -> err=<nil>
Roles: []string{}        actor=<zero> -> err=<nil>
Roles: nil               actor=<zero> -> err=<nil>
Privileges-only          actor=<zero> -> err=<nil>
```

⚠ **A fifth shape, and it is worse.** `{Roles:["manager"], Privileges:["finance-task
approve"]}` carries a dimension `RoleAuthorizer` *can* evaluate, so it passes the
role check and **silently discards the privilege requirement**. Any manager clears a
gate the author wrote to require an explicit grant. The draft's fix was worded *"a
spec whose **only** dimension is `Privileges` denies"* — that word left this shape
fail-open, and it is the shape that *looks configured*. ADR-0185 D3 now denies on
**any** unevaluatable dimension.

This behaviour is not accidental: **ADR-0117 decided it, in two places.** Decision 1
says *"with none set, the engine gate is open"*; ⚠ **Decision 3 independently says
"All three dimensions are co-equal and independently optional. Any combination
(including none) is valid."* The draft named only Decision 1 as amended, which would
have left live ADR text asserting the proposition ADR-0185 overturns.

### 2.4 Deny-list ABAC predicates allow on a missing variable (103)

**Executed**, `vars = map[string]any{}`, actor `{ID:"mallory", Roles:["manager"]}`:

| predicate | verdict |
|---|---|
| `vars.status != "blocked"` | **ALLOW** |
| `vars.blocked != true` | **ALLOW** |
| `!(vars.blocked == true)` | **ALLOW** |
| `vars.tier == nil or vars.tier == "gold"` | **ALLOW** |
| `vars.status == "ok" or "manager" in actor.Roles` | **ALLOW** |
| `vars.region == "eu"` (positive control) | DENY |
| `actor.attributes.clearance > 3` (the audit's own exemplar) | DENY, **with a leak** |

⚠ **These five are a SAMPLE, not the class.** The draft's recap called the class
*"five predicate forms wide"*. A missing map key returns `reflect.Zero`
**unconditionally** (`expr@v1.17.8/vm/runtime/runtime.go:58-70`), so the class is
*every predicate that evaluates true when a referenced key resolves to nil* — it is
unbounded. Trivial further members: `not vars.blocked`, `vars.owner != actor.ID`,
`vars.deleted == nil`, `vars.a == vars.b` (both absent ⇒ `nil == nil` ⇒ true).
Damage is bounded because the chosen mechanism is **key-based, not form-based**, so
it closes the whole class — but a reviewer reading "five forms wide" could accept a
form-matching implementation as sufficient.

> ### ⚠ 2.4.1 The triage's proposed fix for 103 is REFUTED by execution
>
> Triage `tier1a` §103 fix sketch (a): *"Compile ABAC predicates **without**
> `expr.AllowUndefinedVariables()` (or pre-declare the env schema)."*
>
> **This does not work, and it was run.** Compiling each predicate with
> `expr.Env(map[string]any{"vars": map[string]any{}, "actor": …})` and **no**
> `AllowUndefinedVariables` gives identical verdicts:
>
> ```
> vars.status != "blocked"     out=true err=<nil>
> vars.blocked != true         out=true err=<nil>
> !(vars.blocked == true)      out=true err=<nil>
> vars.region == "eu"          out=false err=<nil>
> ```
>
> The reason is in the vendor: for a `reflect.Map`, a missing key returns
> `reflect.Zero(elem)` unconditionally. Compile-time env declaration constrains the
> *shape* of `vars`, not its *keys*.
> ⚠ One row the draft's "identical verdicts" summary did not cover: with
> `expr.Env`, `actor.attributes.clearance > 3` becomes a **compile** error
> (`type authz.Actor has no field attributes`) rather than a run error.
>
> A custom `Fetch`-implementing map type also fails: **expr v1.17.8 has no `Fetcher`
> interface** (`grep -rn "Fetcher"` over the module → zero non-test hits).
>
> The only mechanism that works is **static reference extraction**.

> ### ⚠⚠ 2.4.2 The draft's OWN escape hatch did not exist, and two of the audit's replacements are also wrong
>
> This is the finding that failed the bundle, and the revision's most important
> section. Full outputs: `2026-08-21-adr-0185-0186-premise-evidence.md` §1–4.
>
> **`has(vars, "k")` is not a function in expr v1.17.8.** Executed:
> `has(vars,"tier")` → `invalid operation: cannot call nil (1:1)`.
> `AllowUndefinedVariables` resolves `has` to nil, so it **compiles** and fails at
> **run** time — and `RoleAuthorizer` wraps run errors as `ErrNotAuthorized`
> (`authz/authz.go:136-141`). **A predicate written to the draft's own prescription
> denies everyone, permanently.** The draft prescribed it in the ADR, in §4.3 here,
> and as a plan test that therefore could not pass.
>
> The audit proposed four replacements. **Two of them do not survive execution
> either**, and restating them without running them would have been the same failure
> one level down:
>
> | form | evaluates? | usable as a guard? |
> |---|---|---|
> | `"k" in vars` | ✅ | ✅ recommended |
> | `vars?.k` | ✅ | ✅ (`MemberNode.Optional`) |
> | `vars.k ?? d` | ⚠ **only parenthesised** | ✅ once parenthesised |
> | `get(vars,"k")` | ✅ | ❌ **extracts ZERO references — a bypass** |
> | `has(vars,"k")` | ❌ | — |
>
> - ⚠ `vars.tier ?? "none" == "gold"` is a **compile error**: *"Operator (==) and
>   coalesce expressions (??) cannot be mixed. Wrap either by parentheses."* Only
>   `(vars.tier ?? "none") == "gold"` parses. Any documentation offering `??` must
>   show the parentheses.
> - ⚠ `get(vars,"tier") == "gold"` yields **no extracted references at all**, so a
>   policy written with `get()` would skip the strict check entirely. It is a
>   **bypass**, handled by the zero-reference deny rule — not an escape hatch.
>
> **And guard recognition has a soundness requirement nobody had stated.** A naive
> collector that marks a key optional whenever `"k" in vars` appears anywhere in the
> tree is **unsound**. Executed with `vars` empty:
>
> ```
> "tier" in vars and vars.tier == "gold"      evaluates=false   naive: "guarded"
> "tier" in vars or  vars.tier != "blocked"   evaluates=TRUE    naive: "guarded"  <-- HOLE
> not ("tier" in vars) or vars.tier == "x"    evaluates=TRUE    naive: "guarded"  <-- HOLE
> ```
>
> Rows 2 and 3 **allow on an absent key** — the class the rule exists to close. A
> guard counts only when the existence test **dominates** the use. Those three rows
> are the falsifying table the implementation must be tested against, and they are
> prescribed as plan phase 1 test 3.
>
> **The extractor's closed set**, executed: depth-1 `vars.<ident>` and
> `vars["literal"]` (and the `actor` equivalents). `vars.order.total` yields
> `vars.order` only. Depth-1 is **exactly the documented supported surface** —
> `humantask.HumanTask.Vars`' own godoc says the snapshot is a shallow `maps.Clone`
> and that *"eligibility predicates should rely on top-level scalar variables only"*.
> Three residual shapes get explicit fail-closed verdicts: **nested** (checked at
> depth 1, deeper absence out of scope and stated), **dynamic key**
> (`vars[actor.ID]` → deny), **zero-reference** (`get()`, `vars | first()` → deny).

### 2.5 Completion never checks who claimed (124)

`handleHumanCompleted` in `engine/step_triggers.go` writes
`task.Completion = &humantask.Completion{Actor: t.Actor, …}` — straight off the
trigger.

⚠ **Cite the symbol, not the line.** Re-derived over the **whole function body**:

```
$ awk '/^func handleHumanCompleted/,/^}/' engine/step_triggers.go \
    | grep -c "Candidates\|Eligibility\|Claim"
0
```

**Zero hits.** The draft reported *"one hit, and it is a comment"* from an
`awk 'NR>=839 && NR<=960'` window that started 10 lines *before* the function
(its lone hit was a godoc line) and ended 23 lines short of its end. The conclusion
survives — in fact it is stronger — but the measurement offered for it did not
measure the function. At the revision commit `handleHumanCompleted` is at **`:849`**
and the `Completion` write at **`:941`**; the draft's `:839` lands inside
`applyOutcomeExposure`, a *different* function.

The authorization that *does* run is in `runtime/task/service.go` — exactly **four**
`Authorize` call sites (`:199` Claim, `:234` Reassign, `:255` Complete, `:306`
RefreshCandidates), all evaluating `task.Eligibility`. Eligibility is set membership;
it cannot distinguish the claimant from any other eligible actor.

> ⚠ **`Candidates` is the wrong comparison target.** `RefreshCandidates`' godoc
> states *"Candidates are a projection, not an access-control list."* `Claim.Actor`
> is the only defensible target.

> ### ⚠⚠ 2.5.1 `Reassign` is a two-hop bypass of the claimant guard
>
> Found independently by two audit lenses — the strongest signal a design audit
> produces. `Reassign` authorizes `by` against `task.Eligibility`, **the same check
> as `Claim`** by the repo's own godoc (`runtime/task/service.go:206-217`: *"the
> reassigner (by) must satisfy the task's eligibility spec — the same check as
> Claim"*), and `handleHumanReassigned` then overwrites the claim
> (`engine/step_triggers.go:643`,
> `task.Claim = &humantask.Claim{Actor: authz.Actor{ID: t.To}}`) with an unvalidated
> body string.
>
> So mallory, merely *eligible* — exactly the actor the guard exists to stop —
> reassigns alice's task to herself and completes as claimant. The one input she
> needs, the current claimant's id, is disclosed by design (ADR-0147) — i.e.
> **backlog 54, an item in this same bundle, supplies the bypass parameter.**
>
> ⚠ The draft named `Reassign` as the **mitigation** for the stranded-claimant risk
> and did not notice it is the escalation, making its Consequences sentence *"can no
> longer complete a task somebody else holds"* **false as written**.

### 2.6 The read path aliases, and discloses (54)

- `transport/http/httpcore/view.go:31` — `Variables: st.Variables`, an alias.
- ⚠ **The draft's consequence claim is WITHDRAWN.** It asserted *"anything mutating
  the view mutates instance state"* without executing it, in the position that
  justified calling this a live bug. The read path contradicts it: the cached path
  hands out a clone (`persistence/caching_instance_store.go:73-76` →
  `State.Clone()`; `engine/step_state.go:361-363` `cloneState` → `copyVars`) and the
  uncached path decodes a fresh snapshot from the row, so the aliased map is a
  **per-request value**. `ASSUMPTION (unverified)` in **both** directions — neither
  the draft nor this revision may assert it. What remains, and is enough, is a
  **convention violation**: every other escape boundary in this repo clones
  (`HumanTask.Clone`, `Actor.Clone`, `ActiveTasks`).
- No redaction hook exists anywhere on `CustomizeConfig`.
- ⚠ **And the natural place to put one is bypassable.** `CustomizeConfig.InstanceMapper`
  (`seam.go:26-28`, defaulted at `:41`) replaces the default view **wholesale** and
  receives the raw `engine.InstanceState` (`endpoints.go:124,140,156`). A redaction
  hook inside `NewInstanceView` — where the draft put it — is disabled by the
  documented, encouraged response-customization feature, with no diagnostic. It must
  run in `mapInstance`, **before** the mapper.
- The `SECURITY:` caveat exists at exactly **three** non-test sites, all on the
  **admin** group: `transport/http/{stdlib:189,gin:204,fiber:209}/groups.go`.

### 2.7 4xx bodies echo internals (104)

`transport/http/httpcore/errors.go`'s `ClassifyError` has exactly **six** arms; five
echo `err.Error()` — 404 (`:31`), 403 (`:33`), 409 (`:35`), 400 (`:50`), 422 (`:56`)
— and 500 (`:58`) correctly blanks. The set is closed.

**Executed:**

```
[3] status=403 message="workflow-authz: not authorized: attribute predicate: workflow-expreval: run
    \"vars.internalApprovalLimit > actor.attributes.tier\": cannot fetch attributes
    from authz.Actor (1:36)\n | vars.internalApprovalLimit > actor.attributes.tier\n
    | ...................................^"
[4] status=403 message="workflow-authz: not authorized"       (bare deny — no leak)
```

The predicate source appears **twice** — once from `%q` in
`internal/expreval/expreval.go:135`, once inside expr's own snippet.
⚠ The draft added *"plus a caret line rendering it again"*; that third line is dots
and a `^`, **no source text**. The count "twice" that preceded it was right; the
recap appended to it was not.

> ### ⚠⚠ 2.7.1 The 400 arm leaks too — this spec's own open question, resolved against it
>
> §4.7 of the draft recorded: *"`ASSUMPTION (unverified)` — whether
> `validation.ErrInvalidInput` messages can contain submitted variable **values** was
> not verified."* It has now been executed against the repo's own jsonschema
> strategy, input `{"ssn":"123-45-6789"}`:
>
> ```
> - at '/ssn': maxLength: got 11, want 3
> - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'
> ```
>
> The `pattern` leaf reproduces the **submitted value verbatim** into the arm the
> draft deliberately preserved as "actionable" — for exactly the constraint used to
> shape national-ID / card / account-number fields. The `maxLength` leaf discloses a
> length, so a `pattern`-only fix is insufficient.
>
> ⚠ Worse, the draft's plan phase 7 test 3 prescribed a *"second control: assert 400
> and 409 messages are still present, so the fix cannot over-blank"* — which would
> have **pinned the leak into the test suite**.
>
> **A value-free rendering is available.** `*jsonschema.ValidationError` exposes
> `InstanceLocation []string` and `ErrorKind.KeywordPath() []string`, so
> `at '/ssn': violates pattern` is constructible from the public API — executed. It
> must be built from the structured leaves for **every** keyword, not by
> special-casing `pattern`.

### 2.8 Unbounded input (98, 99, 65)

**Body decode sites, non-test:** `stdlib` **13** `json.NewDecoder`, `gin` **13**
`ShouldBindJSON`, `fiber` **13** `c.Bind().JSON`, `httpcore` **0** — **39**, all in
each package's `groups.go`. ⚠ The triage names fiber's idiom `BodyParser`; that
symbol has **zero** hits repo-wide. The count is right, the name rotted.

⚠ **`grep -rnE`, with the `-E`.** The draft wrote
`grep -rn "MaxBytesReader|BodyLimit" transport/` and
`grep -rn "CheckRedirect|expreval" action/httpcall/` **without** `-E`, so `|` was a
literal and both commands return 0 for **any** repository — evidence that cannot
falsify the claim it is offered for. Re-run correctly, both are genuinely **0**.

Fiber's 4 MiB is `fiber.DefaultBodyLimit` (`fiber/v3@v3.4.0/app.go:585`, applied in
`New()` at `:710`) — the framework's, not ours. So 26 of 39 sites, exactly two
thirds, have no cap.

**Expression cost (99).** The `MaxNodes` inversion is **executed and confirmed**, and
⚠ the vendor states it outright in its own godoc (`expr@v1.17.8/expr.go:221`: *"If
MaxNodes is set to 0, the node budget check is disabled"*) — a reader can check that
in five seconds and the probe in five minutes:

```
compile(20 000-node expr), MaxNodes NEVER called -> err=…exceeds maximum allowed nodes (1:10002)
compile(same, MaxNodes(0))                       -> err=<nil>
```

`DefaultMaxNodes = 1e4` is **active**; a node cap is not the missing lever. The
missing lever is **caller-supplied array size**. Measured with the predicate
`count(vars.rows, {let x = #; count(vars.rows, {# == x}) == 1}) == len(vars.rows)`
— ⚠ **80 characters** (`wc -c`), not the 44 the draft stated three times; the
argument that it is far under a 1e4-node budget is unaffected, which is why nobody
checked it:

| n | elapsed | | n | extrapolated |
|---|---|---|---|---|
| 1 000 | 25 ms | | 5 000 | ~610 ms |
| 2 000 | 98 ms | | **10 000** | **~2.4 s** ← the chosen bound |
| 4 000 | 391 ms | | 43 000 | ~45 s |
| 8 000 | 1.563 s | | 50 000 | ~61 s |

Clean 4× per doubling — O(n²), invisible to any node cap.

⚠ **This refutes the draft's own default, and a number the audit proposed.** 256 KiB
of JSON integers admits ~40–50 k elements ⇒ **~45–60 s** of unpreemptible CPU per
evaluation, so `MaxVariableBytes` is not the CPU mitigation the draft called it. And
the audit's suggested replacements — *"5 000 elements ≈ 40 ms, 10 000 ≈ 150 ms"* —
are wrong by ~15× against the same ladder. An inherited number restated without
re-deriving it is precisely the failure this bundle already made once.
`ASSUMPTION (unverified)`: the extrapolations above are arithmetic on the measured
ladder, not fresh measurements; the plan asks the re-audit to re-measure.

⚠ **Two evaluator surfaces, not one.** `authz`'s is `expreval.New()` — `DefaultTimeout
= 5 s` **is** enabled. Only the *engine gateway* surface
(`engine/conditions.go:43`, `expreval.WithTimeout(0)`) is wall-clock unbounded, and
that is a deliberate ADR-0003/0049/0056 trade recorded in that file's godoc.

**SSRF (65).** `WithURLExpr` (`action/httpcall/httpcall.go:125-134`) calls raw
`expr.Compile` — not `internal/expreval` — so it has neither the memoising cache nor
the timeout guard. The default client is `&http.Client{Timeout: 30s}` with no
`CheckRedirect` and no allowlist. The hazard **is** documented in the godoc
(`:119-123`), which makes this a posture question rather than an oversight.

### 2.9 At rest (100, 101)

`grep -rniE "encrypt|redact" persistence/ internal/persistence/ engine/` (non-test)
→ **0**. Plaintext columns verified in
`internal/persistence/store/migrations/sqlite/0001_init.sql`:
`wrkflw_instances.snapshot TEXT NOT NULL` (`:25`), `wrkflw_journal.trigger TEXT NOT
NULL` (`:40`). `wrkflw_journal` is **6** columns — no hash, no prev-hash, no
signature. `engine.NodeVisit` (`engine/state.go:248-262`) has **no actor field**, by
ADR-0145 design.

### 2.10 The parked 102 decision

102's logging half is already on `main`: `internal/authz/casbin/db.go:76-99`
`newPolicyReloadCallback` logs at ERROR and increments
`wrkflw_authz_policy_reload_failures_total`; the comment at `:159-161` records the
deferral. ⚠ These symbols did **not** exist at the draft's declared base of
`70a631e9` — which is what falsified its two blanket "everything at `70a631e9`"
quantifiers and split its citations across two revisions with no marker. They exist
at the revision commit, which is what this spec is anchored on.

The **parked owner decision** is therefore live: after a failed cross-node reload, a
**revoked** permission still returns `true, err=nil` indefinitely.

### 2.11 ⚠ There is a SECOND `Authorizer`, and it is the baseline

Found by the audit; absent from the draft entirely.

| # | ABAC site | evaluator | fixed by the draft? |
|---|---|---|---|
| 1 | `authz/authz.go:136` (`RoleAuthorizer.Authorize`) | `attrEval = expreval.New()` (`:23`) | ✅ |
| 2 | **`internal/authz/casbin/authorizer.go:68`** | **its own** `expreval.New()` (`:30`) | ❌ |

`internal/authz/casbin/authorizer.go:33` carries its own *"An empty spec allows"*
godoc and returns `nil` for the empty spec at `:76`. So for a casbin-wired consumer,
findings 3 **and** 4 were both unfixed — while **ADR-0185 Decision 3 told consumers
"a consumer who wants privileges evaluated wires the casbin authorizer"**, and
CLAUDE.md makes casbin the baseline. The draft's fix moved the security-conscious
deployment from the hardened authorizer to the unhardened one.

⚠ The draft also called D4's scoping *"structural rather than conventional"*, reasoning
over **two** `expreval.New(` instances. There are **four**, non-test:
`authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`,
`engine/conditions.go:43`, `runtime/processdriver_options.go:200`. Strictness is
**opt-in per instance**; package boundaries confer nothing.

---

## 3. Scope

**In scope:** 51, 52, 53, 54, 65, 98, 99, 103, 104, 124, and the parked 102
fail-closed decision. Posture-only for 100 and 101.

**Out of scope (named so the audit can check the boundary):** backlog **90** (silent
claim theft on the *claim* path — same seam as 124, distinct defect; §7),
**62**/**54(c)**, **32**, **60**/**91**, **106**.

---

## 4. Load-bearing decisions

The full decisions are in ADR-0185 and ADR-0186. This section records the option
space and what the audit changed.

### 4.1 D1 — How does the principal reach the authorizer? (51)

Option A (keep body-derived, document it) documents a privilege escalation rather
than closing it — rejected. Option B (a resolver on `CustomizeConfig` reading
headers) is Option C wearing a config field.

**★ Option C — the actor travels in the `context.Context`, and `authz` owns the key.**
`authz.ContextWithActor` / `ActorFromContext` in the public root package; the three
DTO actor fields removed; `httpcore` reads the context and only the context.
*For:* library-first, framework-neutral, reusable by a future non-HTTP transport,
fail-closed by construction. *Against:* **BREAKING** — **29** pins and three
`examples/` mains.

**What the audit changed:**
- ⚠ **Renamed to `httpcore.WithRequestActor`.** `WithActorResolver` is already
  exported by **three** packages (`service/options.go:99`,
  `runtime/task/service.go:113`, `processtest/harness.go:104`) taking a
  `humantask.ActorResolver` — *candidate expansion*, the opposite direction of data
  flow. `authz/authz.go:34`'s godoc already links `[ActorResolver]` meaning that one
  (and links it from a package where the symbol does not exist — a broken link
  today).
- ⚠ **The adapters do NOT resolve the actor.** The request ctx already reaches
  `httpcore` in all three (§2.1), so resolution happens once. The draft had all
  three adapters doing it.
- ⚠ **A decision for "nothing authenticated the caller", which the draft lacked.**
  Manufacturing a zero actor is not fail-closed: `Open` admits it, `AllowAll` admits
  it, the audit record becomes `Actor{ID:""}` (no empty-ID guard exists anywhere in
  the repo), and D5's guard degenerates to `"" == ""` so any anonymous caller
  completes any other anonymous caller's task. **401** when nothing resolved;
  `WithAnonymousActorAllowed()` for examples; **503** on a resolver error, never a
  downgrade; and an empty `Actor.ID` rejected as a claimant identity.
- ⚠ **fiber's idiom is `c.SetContext`, not `c.Locals`** — the latter does not
  propagate.

**Contested sub-decision, unchanged:** a body still carrying `"actor"`/`"by"` is
**ignored silently**, not 400'd — the field is out of contract and a 400 would break
rollout windows for no security gain.

### 4.2 D2 — Does an empty spec deny, or must "open" be stated? (53)

Option A (zero spec denies, no marker) leaves "the author forgot" and "the author
meant open" indistinguishable — rejected. Option C (a policy field on the authorizer)
leaves the *definition* silent and makes one definition mean different things in two
deployments — rejected.

**★ Option B — an explicit marker, a zero spec denies, authoring-time validation.**

**What the audit changed:**
- ⚠ **`Open` is a tri-state `*bool`, not a `bool`.** Eligibility is a **stored** field
  frozen at task creation; all four `Authorize` sites read the stored spec. With a
  `bool`, a new binary decodes every pre-upgrade row as `false` ⇒ every human task
  open at upgrade becomes unclaimable, uncompletable **and** unreassignable, with no
  repair verb, and re-authoring the definition does not fix it. Executed through the
  real codec (`json.Marshal` at `store_core.go:81`, plain `json.Unmarshal` at
  `:174`): a pre-upgrade row decodes to `nil`, distinguishable from an explicit
  `false`, and all three states round-trip. `nil` ⇒ grandfathered open.
- ⚠ **A migration phase.** The draft's phase table had no `internal/persistence/*`
  phase at all.
- ⚠ **Deny on ANY unevaluatable dimension**, not only a sole one (§2.3).
- ⚠ **The blast radius was never counted and its one quantifier was false.**
  "Every existing definition with no eligibility becomes invalid": re-derived,
  **274** `NewUserTask` sites, **128** with no eligibility dimension, but only **5**
  reach `model.Validate` (one non-test caller, `definition/model/builder.go`).
  Struct-literal definitions — the dominant idiom in `engine`'s tests — are never
  validated. So the *authoring* gate touches ~5 sites and the *runtime* rule touches
  all 128, across four packages the draft gave no phase.
- ⚠ **ADR-0117 Decisions 1 AND 3** are amended, and **two** godocs state the open
  default as fact (`activity.go:159` on `NewUserTask`, `options.go:221`).

### 4.3 D3 — Do ABAC missing variables fail open or closed? (103)

⚠ Read §2.4.1 and §2.4.2 first. Errors **already** fail closed; the hole is
*absence-without-error*, and the draft's escape hatch did not exist.

**★ Option A + a mint-time diagnostic.** Static reference extraction, deny on absence,
`"k" in vars` / `vars?.k` as guards **where the guard dominates the use**, deny on
dynamic-key and zero-reference predicates.

Option B (authoring-time rejection only) protects **nothing already stored** and does
not reach the casbin path — rejected as the sole mechanism.

**What the audit changed:**
- The escape hatch, the dominance requirement, the `get()` bypass and the `??`
  parenthesisation (§2.4.2).
- ⚠ **Applied at BOTH ABAC sites** (§2.11), and the "structural scoping" claim
  dropped.
- ⚠ **The mint-time check moved from `model.Validate` to task creation.**
  `HumanTask.Vars` is frozen at mint time and never refreshed, so a predicate over a
  variable written later by a parallel/boundary/timer path references a key absent
  for the task's whole life. Today it silently allows; under the runtime rule alone
  it would silently **deny forever with no repair verb**. `model.Validate` cannot see
  process variables, so the check belongs where the snapshot exists. The draft's
  non-fatal `model.Validate` diagnostic is **dropped** (its own test was flagged
  vacuity-risk because no warning channel was ever verified to exist).
- ⚠ **The spec-shape gate is hoisted** into `runtime/task` before all four
  `Authorize` sites, so a consumer's own `Authorizer` inherits it too.

**Second leg, uncontested:** carry `Attributes` through — free under D1.

### 4.4 D4 — Where does redaction belong? (54, 100)

**★ Option A — a transport-level hook**, default shallow copy. Option B (sensitivity
declared in the definition) is too large and would put a policy concept in the engine
core. Option C (a persistence codec) needs a key-rotation and key-loss story the
library must not own — deferred to its own ADR as a *decision*, not silence.

**What the audit changed:** ⚠ the hook runs in **`mapInstance`, before the mapper** —
inside `NewInstanceView` it is bypassed wholesale by `CustomizeConfig.InstanceMapper`
(§2.6). And the "mutates instance state" justification is **withdrawn**.

### 4.5 D5 — How is expression cost bounded? (99)

The draft chose **A + B**: a `ctx` on `ConditionEvaluator` *and* an env input bound,
"both, or the fix is theatre".

**⚠ The audit killed A, and the owner adjudicated it out.** The ctx was justified
against `engine/purity_test.go` — which checks the **import list**, not the
**deterministic-replay** invariant `engine/conditions.go:29-43` actually locks per
ADR-0003/0049/0056. `expreval.run` is synchronous when `timeout <= 0`, so honouring a
ctx forces the goroutine path: measured **99.43 → 965.2 ns/op, 3 → 9 allocs** on an
ordinary gateway condition, on the token step loop. Either the default honours it —
converting ADR-0056's *explicit opt-in* trade into everyone's default without
amending three ADRs — or it discards the parameter, which is verbatim why the draft
rejected the `ContextConditionEvaluator` alternative.

**★ B alone, plus real plumbing.** `expreval.WithMaxEnvElements(n)` and
`runtime.WithMaxEvalElements(n)` constructing the driver's evaluator, default
**10 000** (~2.4 s at the measured curve). An input bound is **deterministic**, so no
locked invariant is traded. ⚠ The draft's *"reusing Decision 1's variable cap as the
same knob"* was a **zombie**: two knobs, two packages, two units, no plumbing.

⚠ **Do NOT implement the audit's `MaxNodes` fix** — §2.8 shows it inverted and
already in force.

### 4.6 D6 — SSRF posture for `httpcall` (65)

**★ Option C — default-deny only for expression-derived URLs.** `WithBaseURL`
unchanged: a URL the author typed is not attacker-controlled. Unchanged by the audit.

### 4.7 D7 — What may a 4xx body say? (104)

**★ Option B — per-class policy.** 403 static; 404/409/422 unchanged; **401**, **413**
and **503** added (⚠ the draft's §4.7 enumeration omitted all three that its own ADR
and plan added); correlation id on every body.

**What the audit changed:** ⚠ **400 gets a value-free rendering** rather than being
kept verbatim — this spec's own `ASSUMPTION (unverified)` resolved against it
(§2.7.1). ⚠ The correlation id's **source** is now decided (OTel span id when
recording, else a random hex id) and `ErrorBody` is added to the breaking-change
list, which the draft omitted. ⚠ **413's mapping is named** — a `MaxBytesReader`
failure surfaces as a decode error, so an `ErrBodyTooLarge` sentinel plus per-adapter
conversion is required; the draft asserted the status in three documents without a
mechanism.

### 4.8 D8 — What does a failed cross-node policy reload do? (parked 102)

**★ Option B — a staleness budget plus a readiness signal.** Health check enabled,
deny-budget disabled: shed first, deny only if the operator asks. Unchanged by the
audit.

### 4.9 D9 — Body and variable size caps (98)

`MaxBodyBytes` default **1 MiB** (`ASSUMPTION (unverified)`, a judgement call);
`WithMaxVariableBytes` default **256 KiB**, ⚠ now documented as a **payload/storage**
bound rather than the CPU mitigation (§2.8 refutes that framing).
⚠ `ASSUMPTION (unverified)`: the fiber `len(c.Body())` mechanism, and — conceded in
ADR-0186 D1 — it is a **rejection, not a prevention**, since the body is already
buffered.

---

## 5. Why two ADRs

| ADR | question | items |
|---|---|---|
| **0185 — authorization identity is not self-asserted** | *Who is the actor, and what does a spec mean?* | 51, 52, 53, 103, 124, parked 102 |
| **0186 — untrusted-input and disclosure posture** | *What does the library accept, and what does it hand back?* | 98, 99, 65, 54, 104, 100/101 posture |

They are separable from each other but **not** further separable: 0185's items
compose into one chain (§1) and splitting them ships a fix that changes nothing.
The owner confirmed on 2026-08-21 that the two ship as **one bundle**, on that
composition argument.

---

## 6. Enumerations (re-derived at the revision commit)

Raw commands and outputs: `2026-08-21-adr-0185-0186-premise-evidence.md` §7.

### 6.1 What D1 breaks — ⚠ it is **29**, not 23

```
$ grep -rnE 'httpcore\.Actor\{|"actor"|"by"' transport/ --include='*_test.go' | wc -l
29
```

⚠ The draft's grep matched only `"actor"` and `httpcore.Actor{`, and returned 23.
**`ReassignInput.By` is tagged `"by"`** (`httpcore/dto.go:66`), so every reassign
body was invisible to it. Files stay 9 and packages stay 5, because all six missed
lines land in files already listed — which is exactly why the miss did not change the
table's shape. **The arithmetic was right; the net was wrong.**

| package | draft | actual | files |
|---|---|---|---|
| `httpcore` | (not given) | **11** | `endpoints_test.go` 6, `dto_test.go` 5 |
| `gin` | 5 | **7** | `gin_test.go` 4, `gin_coverage_test.go` 3 |
| `fiber` | 4 | **5** | `fiber_test.go` 5 |
| `stdlib` | 3 | **5** | `coverage_test.go` 2, `errors_test.go` 2, `stdlib_test.go` 1 |
| `parity` | (not given) | **1** | `parity_test.go` 1 |

⚠ **Two of the missed pins assert a 403** — `stdlib/errors_test.go:187` and
`gin_coverage_test.go:244`. After D1 they still return 403, **from the zero actor**,
so they pass while testing nothing. They must be rewritten, not recompiled. Nothing
in the draft told an agent to look at them.

Production source: `httpcore/endpoints.go:119,132,150`. DTO declarations:
`httpcore/dto.go:12,44,50,66`.

### 6.2 `examples/` — ⚠ 12 scenarios, not 13, plus 4 wiring mains

```
$ grep -rln "runtime\.WithHumanTasks" examples/            | wc -l   → 16
$ grep -rln "runtime\.WithHumanTasks" examples/scenarios/  | wc -l   → 12
```

The four outside `scenarios/` are `cache_wiring`, `mysql_wiring`,
`production_wiring`, `sqlite_wiring` — excluded by the draft's `scenarios/`-scoped
sentence but carrying `UserTask`s all the same. **Three mount the task routes** via
`stdlib.Mount` (`production_wiring:264`, `sqlite_wiring:278`, `mysql_wiring:262`),
which registers `TaskRoutes` (`stdlib/mount.go:17-21`). No `gin.Mount`/`fiber.Mount`
caller exists outside tests.

⚠ The triage's *"no example mounts the task routes"* is **false**; the draft caught
that and then mis-stated the scenario count in the same paragraph, while telling the
agent *"enumerate them mechanically; do not guess"*.

### 6.3 The four `Authorize` call sites

`runtime/task/service.go:199` Claim · `:234` Reassign · `:255` Complete · `:306`
RefreshCandidates. All four pass `task.Eligibility`; **none** compares the acting
actor to `task.Claim.Actor`. (Repo-wide there are five `.Authorize(` non-test; the
fifth, `casbinauthz/casbinauthz.go:163`, is a delegation.)

### 6.4 The three `SECURITY:` caveat sites (all admin-only)

`transport/http/stdlib/groups.go:189` · `gin/groups.go:204` · `fiber/groups.go:209`.

### 6.5 The 26 stdlib routes

9 non-admin, 15 admin, 2 health. **No definition-deploy route** — which is what keeps
99 and 103 author-reachable rather than anonymous-reachable.

### 6.6 Counts the audit confirmed as written

10 `service` options · 6 `DurableProvider` methods · 39 = 13×3 decode sites ·
26/39 = two thirds uncapped · 5 echoing 4xx arms + 1 blanking · 6 `wrkflw_journal`
columns · `DefaultMaxNodes = 1e4` · 3 `stdlib.Mount` callers · 3 `SECURITY:` sites ·
4 `Authorize` sites · ADR numbers 0185/0186 genuinely free · and 28 further
line/symbol citations. ⚠ **The bundle's arithmetic was almost never the problem.**
What failed was the **net** (§6.1's grep, §2.11's two-element enumeration) and the
**anchor** (§2.5's and §2.10's base commit).

---

## 7. Interactions with out-of-scope items

- **90 (silent claim theft)** shares 124's seam and stays open. ⚠ After ADR-0185 D5
  the honest property is *"a non-eligible, unauthenticated, or non-privileged actor
  cannot complete a task somebody else holds"* — not the unqualified claim the draft
  made. **Do not let implementation quietly absorb 90.**
- **106 (readiness surface)** is where D8's health check must eventually plug in.
- **62 / 54(c)** needs a principal to check against — D1 supplies it, so 62 becomes
  possible *after* this bundle.
- **32** (snapshot versioning): `AuthzSpec.Open` is a new field inside the persisted
  snapshot. The tri-state handles the forward direction by design (§4.2); the reverse
  (old binary, new row) drops `Open` silently and needs a mixed-version deployment,
  already out of contract.

---

## 8. Non-goals

- No BPMN2 XML, no new transport, no gRPC.
- No DI requirement: every new seam is a plain constructor argument or an exported
  option func.
- No key management. The library will not hold, rotate, or derive encryption keys.

---

## 9. What was executed

Full transcripts: **`docs/specs/2026-08-21-adr-0185-0186-premise-evidence.md`**.
Probe module: throwaway `module probe` with a `replace` onto the working tree, run
outside the repo; no repo `.go` file was created or modified.

**Executed for the 2026-08-20 draft and re-confirmed:** the four allow-all spec
shapes; the five deny-list predicate verdicts; §2.4.1's refutation of the triage fix;
the `MaxNodes` inversion; the two evaluator surfaces; the 403 leak and the bare-deny
control; the O(n²) ladder; the 13/13/13/0 decode counts.

**Executed for this revision (new):** the guard-form table (`has` fails; `in`, `?.`,
`get`, `??` behaviours); the `??` parenthesisation constraint; the `get()`
zero-reference bypass; the guard-**dominance** unsoundness table; the depth-1 /
dynamic / zero-reference extraction shapes; the `Open *bool` tri-state round-trip
through `encoding/json`; the jsonschema value echo **and** the availability of a
value-free rendering; and every enumeration in §6.

**Executed by the audit, NOT re-executed here** — `ASSUMPTION (unverified)` for this
revision, and flagged for the re-audit: the request ctx reaching `httpcore` in all
three adapters and `c.Locals` not propagating (§2.1); the
99.43 → 965.2 ns/op benchmark (§4.5).

**Not executed at all** (labelled where used): the 256 MiB body probe and its heap
figures; the 37.66 s stall figure; the fiber body-cap mechanism; the 1 MiB default;
the element-bound extrapolations beyond n = 8 000.

---

## 10. Corrections this spec makes to its inputs

To the **triage**:

1. 103's proposed fix is refuted (§2.4.1) — neither dropping
   `AllowUndefinedVariables()` nor a `Fetch` hook changes any verdict.
2. "`examples/` callers: ZERO" is false (§6.2) — three mount the task routes.
3. fiber's decode idiom is `c.Bind().JSON`, not `BodyParser` (§2.8).
4. The ABAC evaluator is already timeout-bounded; only the engine's gateway
   evaluator is not (§2.8).
5. `Candidates` is the wrong comparison target for 124 (§2.5).
6. 53 reverses a decision, not an oversight (§2.3).
7. The `MaxNodes` fix is inverted (§2.8).

To the **2026-08-20 draft of this bundle**, i.e. what the audit caught:

8. ⚠ `has(vars,"k")` **does not exist**; the prescribed escape hatch denied everyone
   (§2.4.2).
9. ⚠ `Reassign` → `Complete` **bypasses** the claimant guard; the draft named it as
   the mitigation (§2.5.1).
10. ⚠ `Open bool` **strands every in-flight human task** on upgrade (§4.2).
11. ⚠ There is a **second `Authorizer`** — the baseline one — and the draft fixed
    neither, while routing consumers to it (§2.11).
12. ⚠ The `ctx` on `ConditionEvaluator` checks the wrong invariant and costs ~10×
    (§4.5).
13. ⚠ The pin count is **29**, not 23 — the grep missed `"by"` (§6.1).
14. ⚠ `handleHumanCompleted` is at `:849`, and the draft's measurement window
    straddled the function (§2.5).
15. ⚠ The 400 arm echoes **submitted values**; the draft's own open question resolved
    against it, and its prescribed control would have pinned the leak in (§2.7.1).
16. ⚠ `WithActorResolver` **collides** with three existing exports meaning the
    opposite (§4.1).
17. ⚠ `RedactVariables` is **bypassed wholesale** by `InstanceMapper` (§2.6).
18. ⚠ The allow-all log **level** is at `:323`, not `:315-317` (§2.2).
19. ⚠ The `WithDurableStore` "ordering trap" premise is **half refuted**, and the
    prescribed test could not fail (§2.2).
20. ⚠ The mixed `{Roles, Privileges}` spec stays fail-open under *"whose **only**
    dimension"* (§2.3).
21. ⚠ ADR-0117 **Decision 3** is amended too; **two** godocs state the open default
    (§4.2).
22. ⚠ Blast radius is **274 / 128 / 5**, and "every existing definition" is false
    (§4.2).
23. ⚠ "Five predicate forms wide" describes an **unbounded** class as closed (§2.4).
24. ⚠ The caret line does **not** reprint the source (§2.7); the predicate is **80**
    characters, not 44 (§2.8); two greps were written without `-E` and could not
    falsify their claims (§2.8); the `examples/scenarios` count is **12**, not 13
    (§6.2); §4.7 omitted the 401/413/503 arms its own ADR added.

To the **audit itself** — restating an inherited fix without executing it is the same
failure one level down:

25. ⚠ `vars.k ?? d` is offered as a working guard form; it **does not parse**
    unparenthesised (§2.4.2).
26. ⚠ `get(vars,"k")` is offered as a working guard form; it extracts **zero
    references** and is a **bypass** (§2.4.2).
27. ⚠ A guard collected tree-wide is **unsound** — the audit's proposed fix did not
    state the dominance requirement, and the naive reading reopens the hole (§2.4.2).
28. ⚠ The proposed element bounds — *"5 000 ≈ 40 ms, 10 000 ≈ 150 ms"* — are wrong by
    ~15× against the ladder they were derived from (§2.8).
