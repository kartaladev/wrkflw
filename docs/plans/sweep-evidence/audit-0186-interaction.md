# ADR-0186 rule-#9 audit — INTERACTION lens

**Bundle commit:** `32f4e3e5` (`design/authz-security-b3`, re-cut standalone ADR-0186 delivery)
**Worktree:** detached at the bundle commit. Step-0 presence check: **PASSED** — all three
bundle files present (`docs/specs/2026-08-21-untrusted-input-and-disclosure.md`,
`docs/adr/0186-untrusted-input-and-disclosure-posture.md`,
`docs/plans/2026-08-21-untrusted-input-and-disclosure.md`).

**Lens:** pairwise interaction. The six decisions are taken **pairwise** and each is asked what
it does to the *other's premises*. The spec's §5 is the author's own pairwise table; it is
attacked here, and the pairs it omits are derived.

**Method:** every citation re-derived by execution in this worktree. Where something could not
be executed it is labelled `ASSUMPTION (unverified)` on the auditor's own side.

---

## I-1, CRITICAL — the oversize→413 chain is unreachable at three decode sites, and the three adapters diverge there

**The pair:** D1 (body cap, bare `ErrBodyTooLarge`) × D5 (classifier owns the 413 arm).
Secondary leg: D1 × ADR-0095 (admin routes are default-absent).

**The interaction.** D1's mechanism is *"each adapter converts its own oversize signal into that
sentinel **before** calling `ClassifyError`"*, and D5 maps the sentinel → 413. Both halves
presuppose that every decode site's error **reaches** `ClassifyError`. At three of the 39 sites
it does not: the decode error is deliberately **discarded**, because the body is optional.

```
$ grep -nE "Body is optional|_ = |ignore parse" transport/http/{stdlib,gin,fiber}/groups.go
stdlib/groups.go:238:  _ = json.NewDecoder(req.Body).Decode(&in) // body is optional
gin/groups.go:264:     // Body is optional; ignore parse error for an empty body.
gin/groups.go:265:     _ = gc.ShouldBindJSON(&in)
fiber/groups.go:254:   // Body is optional — ignore decode errors (defaults to zero AddAttempts).
fiber/groups.go:255:   _ = c.Bind().JSON(&in)
```

All three are the **same route** — `POST /admin/instances/{id}/incidents/{incidentID}/resolve`
(stdlib `groups.go:233`, gin `:258`, fiber `:248`). Consequence, per adapter, if phase 5's
instruction (*"cap the body at all 13 decode sites"*) is applied mechanically:

| adapter | D1's prescribed mechanism | behaviour on an oversize body at this site |
|---|---|---|
| stdlib | `http.MaxBytesReader` before `json.NewDecoder` | read fails → **error discarded** → handler proceeds with a zero-valued `ResolveIncidentInput` → **200**, never 413 |
| gin | `MaxBytesReader` on `gc.Request.Body` before `ShouldBindJSON` | identical — **200** |
| fiber | `len(c.Body())` pre-check *before* the bind | the pre-check is a guard that returns, independent of the discarded error → **413** |

So D1's *"oversize has ONE status"* is false, **and** the three adapters disagree — the exact
property ADR-0186's own Consequences claims ("39 sites, one policy, one status").

**Why no test in the bundle can see it.** Phase 8's parity case asserts *"all three adapters
agree on 413"*, but the parity suite structurally cannot reach this route:

```
$ sed -n '655,665p' transport/http/parity/parity_test.go
# "Admin routes are deliberately NOT part of Mount — a consumer opts into them" (ADR-0095)
```

The suite's route inventory is `POST /instances`, `GET /instances/{id}`, `POST /signals`,
`POST /messages`, `POST /tasks/{id}/claim`, `GET /readyz` — all non-admin. Phase 5's
`TestOversizedBodyReturns413` will be written against a required-body route, pass in all three
packages, and the divergence ships.

**Verdict:** the spec's §5 row *"D1 × D5 … ✅ resolved"* is resolved only for the 36 sites whose
decode error is propagated. The remaining three are a hole the pair opened in each other:
D1 assumed a signal path D5 owns, and D5's arm ordering fix says nothing about a site that
never classifies.

**Proposed fix.** In ADR-0186 D1, state the covered set rather than "39 sites": *36 decode sites
propagate; three optional-body sites discard.* For those three, prescribe explicitly that the
oversize signal is **not** optional — i.e. the site becomes

```go
var in httpcore.ResolveIncidentInput
if err := decode(&in); err != nil && errors.Is(err, httpcore.ErrBodyTooLarge) {
    writeErr(cfg, …, err)   // bare sentinel → 413
    return
}
// any other decode error stays ignored: the body is optional
```

and add a phase-5 test row on the **admin** route for each adapter, with the falsifier stated:
*it fails against an implementation that keeps `_ = decode(&in)`*. Phase 8 cannot be the net,
because ADR-0095 keeps admin routes out of `Mount`.

---

## I-2, CRITICAL — D2's two halves are jointly unsatisfiable: "signatures unchanged" and "count once per env" cannot both hold at the prescribed surface

**The pair:** D2 (no `ctx` on `ConditionEvaluator`; signatures frozen) × D2's own once-per-env cost
fix folded from re-audit #2. Both were *corrections*; each is right alone; together they have no
implementation.

**The interaction.** Two constraints, both stated as binding:

- ADR-0186 D2 + plan phase 1: *"the existing three methods keep their signatures, and `runtime`'s
  two exported options keep theirs."* `expreval.Evaluator`'s three methods **are**
  `engine.ConditionEvaluator`'s three methods, and the engine's package default is assigned
  through that interface (`engine/conditions.go:43`, `resolveEvaluator` `:49-54`), so `expreval`
  cannot change them without breaking the interface.
- ADR-0186 D2 (folded Major from re-audit #2) + plan phase 1: *"The count is **supplied with the
  env**, not computed per evaluation."*

Re-derived from source:

```
$ sed -n '20,27p' engine/conditions.go
type ConditionEvaluator interface {
	EvalBool(code string, env map[string]any) (bool, error)
	EvalDuration(code string, env map[string]any) (time.Duration, error)
	EvalString(code string, env map[string]any) (string, error)
}
```

The env arrives as a **bare `map[string]any`** with no identity handle, no lifetime, and no
companion parameter. There is nowhere to "supply" a count. The three escape routes and why each
is closed:

1. **Add a count parameter / a new method** ⇒ changes `ConditionEvaluator` ⇒ forbidden by D2, and
   is the same category of change D2 spent its entire argument rejecting for `ctx`.
2. **Memoise the count against the map's identity** ⇒ Go maps are not comparable, and the engine
   **mutates the live map in place** between evaluations:
   ```
   $ grep -rn "\.Variables\[" engine/ runtime/ service/ --include="*.go" | grep -v _test
   engine/step_triggers.go:515:  s.Variables["_errorMessage"] = t.Err
   engine/step_triggers.go:517:  s.Variables["_errorAttempts"] = tok.RetryAttempts + 1
   ```
   so a cached count is stale by construction and the evaluator cannot know.
3. **Count outside `expreval`, at the point variables change** ⇒ then
   `expreval.WithMaxEnvElements` is a knob that never fires, and D2's *"the plumbing is real, not
   a zombie"* claim (the explicit anti-ADR-0162 promise) is false again.

Therefore the only implementable form is a **per-evaluation walk**, which is the form re-audit #2
called self-defeating.

**Evidence — executed in this worktree** (`internal/expreval/zzprobe_test.go`, throwaway, deleted;
Apple M4 Pro, `-benchtime=200000x`):

```
BenchmarkProbe_EvalBool_Baseline-14              68.96 ns/op   48 B/op  2 allocs/op
BenchmarkProbe_CountTypicalEnv-14                41.08 ns/op    0 B/op  0 allocs/op
BenchmarkProbe_CountAtDefaultBound-14            17478  ns/op    0 B/op  0 allocs/op
BenchmarkProbe_EvalBool_WithPerEvalCount-14     113.3  ns/op   48 B/op  2 allocs/op
```

The ADR's two numbers **reproduce**: ~84 ns on a typical env (mine 41 ns, same order) and ~19 µs
at the 10 000 default (mine **17.5 µs**). So the ADR's cost premise is sound and the conclusion
drawn from it — *"count once per env"* — is the right conclusion; it simply has no landing site.

⚠ And the executed numbers **refute one of the ADR's own quantifiers**. D2 says a per-evaluation
count *"would be 20–60× worse than the cost the decision refused"*. On a **typical** env it is
+44.3 ns/op (113.3 − 68.96), i.e. **1.6×** the baseline and ~**5% of the 866 ns/op the ctx would
have cost** — cheaper than what D2 refused, not 20–60× worse. The 20–60× figure holds only for an
env at the bound. The ADR generalises a boundary measurement to the whole path.

**And that is the sharper half of the interaction.** With a per-evaluation walk the count cost is
**proportional to the env**, so the worst case is exactly the case the bound exists to catch: a
caller supplying 9 999 elements sits *under* the bound, is admitted, and pays ~17.5 µs of walk
**per gateway condition evaluated**. D2 replaces an O(n²) evaluation cost with an O(n)
per-evaluation counting cost that is itself unmetered up to the bound — a smaller amplification
the ADR never discusses because it only ever measured the bound as a one-off.

**Verdict:** spec §5's row *"D2 × the engine hot path … ✅ Counted once per env"* is **not
resolved**. It is marked resolved on the strength of a sentence that cannot be implemented under
the constraint the same decision imposes two paragraphs earlier. The plan is honest about the
risk (phase 1 item 4, *"if it does, stop and escalate — the decision is wrong, not the code"*) but
routes the discovery to implementation, which rule #9 exists to prevent.

**Proposed fix (pick one, in the ADR, before phase 1):**

- **(a) Bound at the boundary, not at the evaluator.** Move the element check to where a variable
  map is *admitted* — the same seam D1's `service.WithMaxVariableBytes` uses — and keep
  `expreval` unchanged. One count per variable-map mutation, zero per evaluation, no signature
  change, and it composes with D1 instead of duplicating it (see I-5). `expreval` then gains
  nothing and `WithMaxEnvElements` is dropped.
- **(b) Accept the per-evaluation walk and re-derive the trade honestly.** Keep
  `WithMaxEnvElements` inside `EvalBool`, replace the "once per env" sentence with the measured
  +44 ns typical / 17.5 µs at-bound figures, and state that the count cost is proportional to the
  admitted env. Then phase 1's benchmark asserts a *ceiling*, not "no per-evaluation walk".
- **(c)** If neither is acceptable, D2's element bound does not survive and the CPU axis stays
  open — which is a legitimate outcome, but must be written down rather than discovered in
  phase 1.

Whichever is chosen, delete the "supplied with the env" sentence from plan phase 1: no reader can
implement it.

---

## I-3, CRITICAL — D2's default-on bound is silently discarded by `WithExpressionTimeout`, i.e. exactly for the untrusted-definition consumer

**The pair:** D2 (element bound, default 10 000, plumbed via `runtime.WithMaxEvalElements`) ×
the **shipped** `runtime` options ADR-0056 established. Spec §5 marks this **OPEN** and asks the
audit to pick "compose or refuse". Picking that is necessary but **not sufficient** — the failure
is in the *old* option, and the bundle's framing does not reach it.

**The interaction.** Re-derived:

```
$ sed -n '196,223p' runtime/processdriver_options.go
// WithExpressionTimeout and [WithConditionEvaluator] set the same field; the last
// option wins.
func WithExpressionTimeout(d time.Duration) Option {
	return func(driver *ProcessDriver) {
		driver.conditionEval = expreval.New(expreval.WithTimeout(d))   // :200
	}
}
...
func WithConditionEvaluator(eval engine.ConditionEvaluator) Option {
	return func(driver *ProcessDriver) {
		if eval != nil { driver.conditionEval = eval }                  // :220
	}
}
```

⚠ First correction to the bundle: the ADR calls last-writer-wins here *"silent"*. It is not — it
is a **documented shipped contract**, stated verbatim in **both** godocs (`:196-197`, `:216-217`).
The ADR's premise for demanding a change is therefore wrong as stated.

The real defect is what D2's **default** does to it. D2 makes the element bound default-on
(plan phase-3 test 2: *"a driver built with no options still refuses an oversize env"*), which
means the driver constructs a bounded evaluator and assigns `driver.conditionEval`. A consumer who
then takes the library's own documented untrusted-definition opt-in —

```go
runtime.NewProcessDriver(..., runtime.WithExpressionTimeout(5*time.Second))
```

— has `:200` **overwrite** that evaluator with `expreval.New(expreval.WithTimeout(d))`, which
carries **no element bound**. The consumer evaluating untrusted definitions is the only consumer
who needs the untrusted-**input** bound, and they are the one option-ordering silently removes it
from. This is a fail-open opened by D2's own default meeting a shipped option — no new option
"composes or refuses" its way out of it, because `WithMaxEvalElements` is not the option being
called.

`WithConditionEvaluator` has the same shape but is defensible (the consumer supplied the object);
`WithExpressionTimeout` is not, because the library builds that evaluator itself and could bound it.

**Two further consequences of default-on, both undisclosed:**

1. `runtime/processdriver.go:440` logs `slog.Bool("conditionEval", driver.conditionEval != nil)`
   as a startup diagnostic. Default-on makes it a **constant `true`** — a dead attribute, the
   "log attribute lost when guards collapse" shape rule #11 names.
2. `engine/conditions.go:43`'s package-level `conditions` is a **process-wide shared compile
   cache**. Default-on means `StepOptions.Evaluator` is never nil on the runtime path
   (`processdriver.go:674`), so `resolveEvaluator` (`:49-54`) never returns the shared default and
   every driver gets its **own** compile cache. `resolveEvaluator`'s own godoc — *"the default path
   stays byte-identical to the pre-injection behaviour"* — becomes false, and ADR-0186's
   Consequences claim *"ADR-0003/0049/0056 need no amendment"* needs re-examination: the seam
   ADR-0056 documents as an *explicit opt-in* becomes the default path.

**Verdict:** spec §5's **OPEN** row understates the problem. Answering "compose or refuse" for the
new option leaves the fail-open intact.

**Proposed fix.** Make the element bound a **property of the driver, applied to whichever
evaluator ends up in the field**, not a property of one constructor call:

```go
// resolved once, after all options have run
if driver.maxEvalElements > 0 {
    driver.conditionEval = expreval.Bounded(driver.conditionEval, driver.maxEvalElements)
}
```

so `WithExpressionTimeout` and `WithConditionEvaluator` keep their documented last-wins contract
untouched and the bound survives all orderings. Then: (i) `WithMaxEvalElements(0)` is the explicit
opt-out; (ii) both existing godocs gain a sentence saying the element bound is applied on top;
(iii) plan phase-3 test 3 becomes `TestExpressionTimeoutDoesNotDropTheElementBound`, whose
falsifier is *it fails against an implementation that assigns the bounded evaluator inside
`WithMaxEvalElements`*; (iv) the ADR drops the word "silently" and cites the two godocs; (v) the
ADR states what default-on does to the `slog` attribute and to the shared compile cache, or keeps
`resolveEvaluator`'s fallback by leaving `conditionEval` nil and carrying the bound separately.

⚠ Note this fix is **incompatible with I-2 option (a)**: if the bound moves to the variable-map
boundary there is no driver evaluator to wrap. Adjudicate I-2 first.

---

## I-4, CRITICAL — the D2×D3 OPEN question resolves **NO**, and it is unwireable: the one expression surface the bundle calls attacker-influenced is the one the bound structurally cannot reach

**The pair:** D2 (element bound, plumbed by `runtime.WithMaxEvalElements` → `driver.conditionEval`)
× D3 (`WithURLExpr` routed through `internal/expreval`). Spec §5 marks it **OPEN**: *"the env
there is process variables, so the bound should apply, but neither decision says. The audit must
settle it."* Plan phase 6: *"The audit settles it; if yes, this phase wires it."*

**Settled: the answer is NO, and "yes" is not implementable as the bundle is drawn.**

**Step 1 — the spec's premise is TRUE.** `httpcall.Do(ctx, in)` does receive process variables:

```
$ grep -rn "invokeActionDo(" runtime/*.go | grep -v _test
runtime/processdriver_action.go:396:  out, err := invokeActionDo(tctx, bare, cmd.Input, recoverPanics)
runtime/processdriver_action.go:443:  _, err := invokeActionDo(cctx, bare, cmd.Input, true)

$ grep -rn "Input:" engine/*.go | grep -v _test
engine/step_nodes.go:52:      Input:     serviceActionInput(c.s, node),
engine/step_cancel.go:128:    Input:         copyVars(s.Variables),
engine/step_nodes.go:1040:    Input:     copyVars(c.s.Variables),
engine/step_triggers.go:817:  Input:     copyVars(s.Variables),      (and :181, :205, step_compensation.go:676)
```

and `httpcall.go:235` runs the URL program against exactly that map (`expr.Run(h.urlExprProg, in)`).

**Step 2 — there is no channel from the bound to the action.** Re-derived:

```
$ grep -rn "type Action interface" -A 3 action/action.go
type Action interface {
	Do(ctx context.Context, in map[string]any) (out map[string]any, err error)
}

$ grep -rn "ConditionEvaluator\|Evaluator" action/ | grep -v _test
(no output)
```

An `Action` receives `(ctx, in)` and nothing else. D2's plumbing assigns `driver.conditionEval`
(`runtime/processdriver_options.go:200/:220`), which reaches the engine through
`StepOptions.Evaluator` (`runtime/processdriver.go:674`) — a path that terminates at the engine
and never touches the action catalog. `runtime.WithMaxEvalElements` therefore **cannot** bound
`httpcall`'s URL evaluation, by construction, and phase 6's *"if yes, this phase wires it"* is a
dead branch an implementer will only discover mid-phase.

**Step 3 — why this matters more than the pair it is grouped with.** ADR-0186's own Context §2
argues the gateway path is *"serious rather than critical"* because expression **source** is
author-supplied and there is no definition-deploy route. That argument applies verbatim to
`WithURLExpr`. But D3 exists precisely because the httpcall URL is the **one** expression site the
bundle names as reachable from untrusted data (*"an SSRF primitive reachable from process
variables"*). The bundle therefore adds an input bound that covers the surface it calls lower-risk
and excludes the surface it calls higher-risk — and says neither.

**Step 4 — routing through `expreval` is not semantics-preserving, and D3 does not say so.**
Executed in this worktree (throwaway `internal/expreval/zzprobe2_test.go`, deleted):

```
B: EvalString(1 + 1     ) -> "2"          err=<nil>
B: httpcall Run(1 + 1   ) -> 2            isString=false   (→ NonRetryable "result is int, want string")
B: EvalString({"a":1}   ) -> "map[a:1]"   err=<nil>
B: httpcall Run({"a":1} ) -> map[...]     isString=false   (→ NonRetryable)
B: EvalString(nil       ) -> "<nil>"      err=<nil>
B: httpcall Run(nil     ) -> <nil>        isString=false   (→ NonRetryable)
B: EvalString(["x"]     ) -> "[x]"        err=<nil>
C: EvalString("")       -> ""             err=<nil>
C: httpcall Compile("") err=unexpected token EOF
```

`httpcall.go:239-242` **rejects** a non-string URL-expr result with a non-retryable error and never
issues a request. `expreval.EvalString` **coerces** it — `expreval.go:224-227` is
`fmt.Sprintf("%v", out)`. So D3's mechanism converts a hard rejection into a coerced string that
becomes the request URL. In the decision whose entire purpose is *"stop being an SSRF primitive"*,
the prescribed refactor makes the URL **type discipline weaker**, turning "refuse" into "coerce and
dial". Two further unstated changes: compilation moves from option time (a stored `*vm.Program`,
`httpcall.go:127-132`) to per-call, mutex-guarded cache lookup (`expreval.go:103-104`
`e.mu.Lock()`); and if phase 6 uses `expreval.New()` — the idiom `authz/authz.go:23` and
`internal/authz/casbin/authorizer.go:30` both use — the 5 s `DefaultTimeout` is **enabled**, taking
`run`'s goroutine+timer path (`expreval.go:78-99`) on every URL evaluation instead of a direct
`expr.Run`.

**Verdict:** spec §5's OPEN row is answered NO. Plan phase 6's conditional wiring is unreachable.
D3's chosen mechanism silently relaxes the very check it is meant to tighten.

**Proposed fix.**

1. State the answer in ADR-0186 D2: *the element bound does not reach `action/httpcall`; the
   `Action` interface carries no evaluator.* Then either accept it explicitly as a scope
   limitation (and say so in Consequences and `SECURITY.md`), or give `httpcall` its **own**
   option — `httpcall.WithMaxEvalElements(n)` / `httpcall.WithEvaluator(e)` — and wire the default
   in phase 6. Do not leave phase 6 a conditional.
2. In D3, add: *`WithURLExpr` keeps its string-result requirement.* The refactor must call
   `EvalString` **and then re-assert the type**, or use a new `expreval` entry point that does not
   coerce. Prescribe `TestURLExprRejectsNonStringResult` with the falsifier stated: *it fails
   against an implementation that returns `expreval.EvalString`'s coerced value.* Without this row,
   phase 6's four listed tests are all green against the regression.
3. Record the compile-time→run-time and timeout-path changes in D3's Consequences.

---

## I-5, MAJOR — `ErrBodyTooLarge` already exists in this repo, and this bundle creates a second one, in two packages two parallel agents edit

**The pair:** D1 (new `httpcore.ErrBodyTooLarge` + the 413 arm) × D3 (phase 6 edits
`action/httpcall`). Neither decision mentions the other's sentinel; spec §5 has **no D1 × D3 row
at all**.

**The interaction.** Re-derived:

```
$ grep -rn "^var Err" action/httpcall/*.go | grep -v _test
action/httpcall/httpcall.go:94:var ErrBodyTooLarge = errors.New("workflow-httpcall: body exceeds max size")
```

`action/httpcall.ErrBodyTooLarge` is **shipped, exported, module-root API**, returned by
`WithMaxResponseSize` when an *outbound* response exceeds the cap
(`httpcall.go:186-189`). D1 creates `httpcore.ErrBodyTooLarge` for an *inbound* request that
exceeds the cap. Same identifier, opposite direction, one repo.

Consequences the bundle does not consider:

1. **Phase 4 and phase 6 run in parallel** (plan §2: *"Phases 4, 6 and 7 are disjoint and may run
   concurrently"*), on two agents, each creating/handling an `ErrBodyTooLarge`. Phase 4's
   `ClassifyError` arm is `errors.Is(err, ErrBodyTooLarge)` written inside package `httpcore`,
   where the bare identifier resolves to the new one — but the same line copied into a doc, a
   review comment or the parity suite is ambiguous, and an import of the wrong package compiles
   silently and classifies an *outbound* failure as a client 413.
2. **`SECURITY.md` (phase 9)** must describe both a request cap and a response cap under one name.
3. The 413 semantics are inverted between them: `httpcore.ErrBodyTooLarge` means *the caller sent
   too much*; `httpcall.ErrBodyTooLarge` means *a third-party server sent us too much* — which is a
   **500**, not a 413. Today it correctly falls to `ClassifyError`'s default 500 arm; nothing in
   the bundle prevents a future arm from catching it.

**Verdict:** a name the bundle treats as new is not new, and spec §5 has no row for the pair whose
two phases collide on it.

**Proposed fix.** Name D1's sentinel for the direction it describes —
`httpcore.ErrRequestBodyTooLarge` (or `ErrPayloadTooLarge`) — and add a one-line note in D1
recording that `action/httpcall.ErrBodyTooLarge` already exists and is a **500**, not a 413. Add
the D1 × D3 row to spec §5. Give phase 4's brief the explicit instruction that the 413 arm must
match only the `httpcore` sentinel, with a test asserting an `httpcall.ErrBodyTooLarge` still
classifies **500**.

---

## I-6, CRITICAL — D5 moves submitted values off the wire and onto `slog.Default()`, by default, into a sink D4 cannot redact and D6's closed enumeration excludes

**The pair:** D5 (403/400 raw errors → `CustomizeConfig.Logger`) × D6 (at-rest posture) and
× D4 (redaction hook). Spec §5's row *"D5 × D3/D6 | none — different surfaces | ✅ none"* is the
claim under attack.

**The interaction.** Today the logger receives **only** 5xx. Re-derived, all three adapters:

```
$ grep -n "func writeErr" -A 6 transport/http/{stdlib,gin,fiber}/write.go
stdlib/write.go:30: status, body := httpcore.ClassifyError(err)
stdlib/write.go:32:   if status >= 500 {
stdlib/write.go:33:       cfg.Logger.ErrorContext(r.Context(), "rest: internal error", "err", err)
gin/write.go:13:      if status >= 500 { cfg.Logger.ErrorContext(..., "err", err) }
fiber/write.go:13:    if status >= 500 { cfg.Logger.ErrorContext(..., "err", err) }
```

and the logger defaults to the process logger with no consumer action:

```
$ sed -n '40,58p' transport/http/httpcore/seam.go
cfg := CustomizeConfig[R]{ ... Logger: slog.Default(), ... }
if cfg.Logger == nil { cfg.Logger = slog.Default() }
```

D5 widens that guard to 400 and 403 (*"raw error logged at the transport seam"*; *"raw error to
`CustomizeConfig.Logger`"*; and it corrects the `Logger` godoc for exactly this). The consequence
the bundle does not draw:

> The **400** raw error is the one ADR-0186's own Context §5 executed and showed to be
> `- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'`.

So the delivery's headline outcome — *"the submitted value stops being disclosed"* — is, at the
default configuration, **a relocation**: the value leaves the HTTP body and starts being written,
where it previously was not written at all, to `slog.Default()`, which in a typical deployment
ships to a log aggregator with a longer retention and a wider audience than the API response ever
had. Three legs:

1. **D4 does not cover it.** `RedactVariables` is defined on the `ProcessInstance` → **response**
   boundary. The log line is emitted in `writeErr`, in each adapter, from the raw `error`. There is
   no hook, and D4 never claims one — which is exactly why the pair had to be derived.
2. **D6's posture is a closed enumeration that is now wrong.** D6 commits `SECURITY.md` to naming
   *"the two plaintext columns (`wrkflw_instances.snapshot`, `wrkflw_journal.trigger`)"*. After D5
   there is a third at-rest plaintext location holding process-variable-derived data, created by
   this same bundle, and a `SECURITY.md` that enumerates two would be **false by omission** on the
   day it ships.
3. **It is default-on and silent.** Nothing in D5 makes the widened logging opt-in, and
   `slog.Default()` is the default sink.

**Verdict:** spec §5's *"D5 × D6 … none — different surfaces"* is false. They are the same surface
viewed at two times: D5 writes the data, D6 describes where written data lives.

**Proposed fix.**

- Gate the widened logging: log the raw error for 400/403 **only** when the consumer opts in
  (`httpcore.WithVerboseErrorLogging(true)`, default **off**), or log a redacted rendering by
  default and the raw error only under the opt-in. The correlation id is what makes this workable
  — the operator joins the blanked response to a log line that carries the id and the *class*,
  and opts into the payload when they need it.
- If the widened logging stays default-on, D6's `SECURITY.md` text must name the **third** sink and
  say the library writes rejected request payloads to the configured `slog.Logger`; and D4 must
  state that `RedactVariables` does **not** apply to it.
- Add a phase-4/5 test with the falsifier stated: *`TestRejected400PayloadIsNotWrittenToTheDefaultLogger`
  fails against an implementation that widens the `status >= 500` guard unconditionally.*
- Amend spec §5's D5 × D6 row from "none" to the finding.

---

## I-7, MAJOR — D4 × D5 are not disjoint: the error path renders metadata about the very variables the success path redacts, and the two controls share no configuration

**The pair:** D4 (redaction at the response boundary) × D5 (400 allow-list). Spec §5 asserts
*"Disjoint paths (success vs error) — no interaction found. ✅ none"*. Three interactions exist.

1. **The 400 structured rendering names the redacted key.** D5's allow-list emits
   `at '/ssn': violates pattern` — the JSON pointer of the failing field. A consumer who configures
   `RedactVariables` to drop `ssn` from every success body has, by that act, declared the key
   sensitive; the 400 body still names it. The two controls are keyed on the same namespace
   (variable names / instance-document paths) and have no shared configuration, so a deployment
   cannot express "this key is sensitive" once.
2. **The allow-list is explicitly instructed to render value-derived metadata.** ADR-0186 D5's
   table says the structured leaves are *"built for **every** keyword, not just `pattern` (the
   `maxLength` leaf discloses `got 11, want 3`, a length)"*. Read as an implementer will read it,
   this instructs covering `maxLength` — but does **not** say whether `got 11` is kept or stripped.
   If kept, the 400 body discloses the **length** of a value D4 was configured to remove entirely.
   The sentence is load-bearing and ambiguous.
3. **They sit on opposite sides of a layer boundary and cannot be composed.** D5's rendering moves
   into `runtime/validation` (plan phase 2), **below** the transport; D4's hook lives on
   `httpcore.CustomizeConfig`, **at** the transport. A future consumer asking "apply my redaction
   policy to validation errors too" has no seam, and the bundle closes the door on one without
   noticing.

**Verdict:** the "no interaction" row is wrong. The paths are disjoint; the **namespace** is shared.

**Proposed fix.** In D5, state explicitly that the structured 400 leaf carries the instance
location and the keyword **and nothing derived from the value** — no lengths, no counts, no
enumerated allowed values — and prescribe `TestMaxLengthLeafDisclosesNoLength` with its falsifier
(*it fails against a rendering that forwards the vendor's `got 11, want 3`*). In D4, state that
redaction does not extend to validation errors and that the two namespaces are independent by
design. Replace spec §5's D4 × D5 row.

---

## I-8, MAJOR — D4 and D5 each force a breaking change to exported `httpcore` function signatures; the bundle's breaking-change list names only `ErrorBody`

**The pair:** D4 (redaction must reach the two mapper-less endpoints) × D5 (correlation id must be
an OTel span id) — converging on the same omission in the migration list D5 owns.

**The interaction.** ADR-0186 Negative lists exactly one breaking change (*"`ErrorBody`'s message
content and shape change … Consumers matching on `ErrorBody.Message` break"*), and plan phase 9
repeats it. Two more are forced by the bundle and unlisted:

1. **D4.** `GetInstanceSnapshot(ctx, svc, id)` and `GetActionableView(ctx, svc, id)`
   (`endpoints.go:60`, `:72`) take no config and no mapper — the fact D4 is built on. To apply
   redaction they must take one. Six further endpoints take `mapper func(engine.InstanceState) any`
   and would need the redaction argument threaded too:
   ```
   $ grep -n "^func [A-Z].*mapper func(engine.InstanceState) any" transport/http/httpcore/endpoints.go
   :25 StartInstance   :47 GetInstance   :82 DeliverSignal
   :116 ClaimTask      :129 CompleteTask :145 ReassignTask
   ```
   All eight are **exported module-root API** and all are called from the three adapters'
   `groups.go` — i.e. the same functions a consumer who assembled their own route group calls.
2. **D5.** The correlation id is specified as *"the OTel span id when a span is recording"*, but
   `ClassifyError(err error) (int, ErrorBody)` takes **no `ctx`**, so it cannot reach the span.
   Either its signature changes — exported, and called from all three adapters
   (`stdlib/write.go:31`, `gin/write.go:12`, `fiber/write.go:12`) plus tests in `service` — or the
   id is minted in `writeErr` and `ClassifyError` never sees it.

**And the second horn breaks a prescribed test.** Plan phase-4 test 6,
`TestCorrelationIDInBodyMatchesTheLogRecord`, is listed under **phase 4 (`httpcore`)** — but
`httpcore` does not emit the log line; the three `writeErr` functions do, in phase 5's packages.
As written the test cannot exist where the plan puts it.

**Verdict:** the migration surface is larger than the bundle's own list, and one prescribed test is
in the wrong package because of it.

**Proposed fix.** Add both to ADR-0186 Negative and to plan phase 9's `CHANGELOG`/`STABILITY`
item. Decide the `ClassifyError` shape explicitly — recommended: keep `ClassifyError` pure and mint
the correlation id in each adapter's `writeErr` (which already holds a `ctx`), passing it into the
body; then move `TestCorrelationIDInBodyMatchesTheLogRecord` to **phase 5**, one per adapter, and
give phase 8's parity suite the id-shape case instead. For D4, prefer a single
`httpcore.ResponsePolicy` (or passing `CustomizeConfig`) threaded into the eight endpoints in one
edit rather than eight ad-hoc parameters — and state it as the breaking change it is.

---

## I-9, CRITICAL — D1's byte cap admits states D2's element cap then refuses to execute: the pair is relabelled, not reconciled, and the gap strands the instance

**The pair:** D1 (`service.WithMaxVariableBytes`, 256 KiB, refused **at admission**) × D2
(`WithMaxEvalElements`, 10 000, refused **at evaluation**). Spec §5 declares this resolved:
*"✅ Two knobs, two stated jobs: elements bound evaluation, bytes bound payload. D1 explicitly
disclaims the CPU rationale."* That is a **relabelling**, and it leaves the two bounds ordered the
wrong way round.

**The interaction.** The two bounds are enforced at different times, in different packages, in
different units, with **no cross-check** — and the admission bound is the **looser** one. Executed
in this worktree (throwaway, deleted):

```
n=5000   json=  23901 bytes  under256KiB=true   underElemCap=true
n=10000  json=  48901 bytes  under256KiB=true   underElemCap=true
n=20000  json= 108901 bytes  under256KiB=true   underElemCap=false
n=30000  json= 168901 bytes  under256KiB=true   underElemCap=false
n=40000  json= 228901 bytes  under256KiB=true   underElemCap=false
n=50000  json= 288901 bytes  under256KiB=false  underElemCap=false
MAX elements admitted by the 256 KiB byte cap: 45540   (element cap is 10000)
```

(The ADR's own *"~40–50 k"* is confirmed; the exact figure at the defaults is **45 540**, i.e.
**4.55×** the element cap.)

**So the window 10 001 … 45 540 elements is admitted, validated and persisted by D1, and then
refused by D2 at every subsequent evaluation.** Consequence:

- The instance is created (201) and durably stored.
- The first gateway/timer/correlation evaluation returns `ErrEnvTooLarge`
  (`engine/step_gateways.go:41,:185`, `step_boundaries.go:63`, `step_nodes.go:90,121,858,1008`,
  `trigger_resolve.go:19` — every one of these paths reads `s.Variables`).
- Retrying re-reads the same persisted variables and fails identically. The token cannot advance.
- **There is no repair verb.** Re-derived from the route table, no route mutates process
  variables:
  ```
  $ grep -rn "rt.POST(bp\|rt.PUT(bp\|rt.PATCH(bp" transport/http/gin/groups.go
  /instances · /instances/:id/signals · /messages · /tasks/:token/{claim,complete,reassign}
  /admin/instances/:id/incidents/:incidentID/resolve · …/compensation/resolve-stall
  /admin/instances/:id/cancel · /admin/dead-letters/redrive · /admin/policies · /admin/role-bindings
  ```
  The only exit is `POST /admin/instances/:id/cancel`. The instance is unadvanceable for its
  lifetime.

This is the **same failure shape** re-audit #2 rejected in ADR-0185 D4 — a runtime rule that makes
already-persisted state permanently unusable with no migration and no repair verb — reproduced in
the other half of the bundle, by two bounds that were each declared safe in isolation.

**And the defaults make the window the common case, not the corner.** 45 540 small integers is
~223 KiB — a perfectly ordinary "list of ids" payload, well under a 256 KiB storage bound a
consumer would consider generous. Any n between 10 001 and 45 540 lands in it.

**Verdict:** spec §5's D1 × D2 row is **not** resolved. Two knobs with two stated jobs is a
description, not a reconciliation; the jobs are ordered such that the looser gate runs first.

**Proposed fix (any one, stated in the ADR):**

- **(a) Order them correctly.** Enforce the element bound at **admission**, alongside the byte
  bound, in `service` — same seam, same sentinel, same 413. Then nothing is ever persisted that
  cannot be evaluated. This is also I-2's option (a) and resolves both findings with one change; it
  is the recommendation.
- **(b) Make the element bound non-fatal at evaluation.** Define `ErrEnvTooLarge` on the engine
  path as producing an incident an operator can act on, and add the repair verb (a variable-trim
  admin route) the bundle currently lacks.
- **(c) Derive the two defaults from each other** so the byte cap cannot admit more elements than
  the element cap allows (at the defaults, 10 000 elements of small ints ≈ **48.9 KiB**, so a byte
  cap of ~64 KiB would be consistent). This changes the 256 KiB judgement call, which the ADR
  already labels `ASSUMPTION (unverified)`.

Whichever is chosen, add a test with the falsifier stated:
*`TestVariablesAdmittedByTheByteCapAreAlwaysEvaluable` fails against the current defaults at
n = 20 000.*

---

## I-10, CRITICAL — variables grow **during execution**, so D1's cap fires with no HTTP caller and D5's static "request too large" is false

**The pair:** D1 (variable cap *"refused before persist with a sentinel"*, mapped to 413) × D5
(413 renders the static message `"request too large"`). Spec §5 has **no row** for this leg; it
only considers D1's *body* cap meeting D5.

**The interaction.** D1's own text assumes the oversize variable map arrives in a request. It does
not have to. The variable map is grown by `mergeVars` from three distinct non-request sources:

```
$ grep -rn "mergeVars(s," engine/step_triggers.go
engine/step_triggers.go:161:   mergeVars(s, t.Output)   // ActionCompleted — a service action's output
engine/step_triggers.go:936:   mergeVars(s, t.Output)   // human-task completion output
engine/step_triggers.go:1208:  mergeVars(s, t.Output)   // the mirror path (message/callback)
```

plus the engine's own writes (`engine/step_triggers.go:515,:517` inject `_errorMessage` /
`_errorAttempts`). So a 200-byte `POST /instances` can start an instance whose variables exceed
256 KiB three nodes later, because a service action returned a large payload.

Three consequences, none stated:

1. **The 413 has no caller.** An `ActionCompleted` trigger is applied by the driver's background
   loop (`runtime/processdriver_action.go:396`), not by an HTTP request. Classifying its refusal as
   413 is meaningless there; the error goes to the incident/retry machinery, where a sentinel named
   "body too large" is actively misleading in the incident record.
2. **When it *does* reach a caller, the message is false.** A `POST /tasks/{token}/complete` whose
   own body is 300 bytes, completing a task whose output pushes the map past the cap, returns
   **413 "request too large"**. D5 blanks the detail, so the operator cannot tell the difference —
   and D5's table gives 413 no logger entry at all, unlike 400 and 403, so the raw error is not
   even in the log.
3. **It is another stranding path.** A service action that has *already run* (the outbound HTTP
   call happened, the side effect landed) cannot have its output persisted. Retrying re-invokes the
   action. There is no repair verb (see I-9).

**Verdict:** D1's cap is specified as an admission control and behaves as a runtime invariant; D5's
static message is written for the admission case only.

**Proposed fix.** In D1, split the concept explicitly:

- **Request-path refusal** — the request body cap and the *inbound* variable map on
  `POST /instances`: `ErrRequestBodyTooLarge` → 413 `"request too large"`.
- **Runtime refusal** — the variable map exceeding the bound as a result of `mergeVars`: a
  **different** sentinel (`service.ErrVariablesTooLarge`), which is **not** 413. Decide its
  disposition explicitly: reject the merge and raise an incident (recommended — the instance stays
  advanceable and an operator sees it), or refuse the commit (and then I-9's repair verb becomes
  mandatory).

Add to D5's table a 413 row stating whether the raw error is logged. Add a phase-7 test with the
falsifier stated: *`TestActionOutputExceedingTheVariableCapDoesNotClassifyAs413` fails against an
implementation that maps the single sentinel to 413.*

---

## I-11, CRITICAL — D4's redaction hook plus D4's prescribed *shallow* copy: redacting a nested secret mutates shared cached instance state

**The pair:** D4's copy fix × D4's redaction hook — two bullets of one decision, written as though
independent. The copy was sized for the *aliasing* defect at `view.go:31`; the hook is a **mutation
callback**, and nobody re-derived what the hook does through the copy.

**The interaction.** D4 prescribes, verbatim: *"The **default** is a shallow copy, which fixes the
aliasing defect at `view.go:31`."* And it adds
`RedactVariables func(map[string]any) map[string]any`, whose purpose is to remove sensitive keys.
The ADR's own worked example of a sensitive field is `ssn` — which in any realistic payload is
**nested** (`vars["applicant"]["ssn"]`), because that is the shape `jsonschema` validates with
`at '/ssn'` pointers.

The clone chain the ADR relies on is shallow all the way down:

```
$ sed -n '325p' engine/step_state.go
func copyVars(in map[string]any) map[string]any { return maps.Clone(in) }
```

`maps.Clone` is shallow. `cloneState` (`engine/step_state.go:361-363`) calls it; `State.Clone()`
calls `cloneState`; `cloneInstanceEntry` (`persistence/caching_instance_store.go`) calls
`State.Clone()` — under a godoc that says *"**deep-copies** an entry so cached live values … can
never be aliased by a caller."*

**Executed in this worktree** (`engine/zzprobe_clone_test.go`, throwaway, deleted):

```
after delete on the CLONE, SOURCE applicant = map[string]interface {}{"name":"ada"}
top-level delete isolation check: source still has 'tags' = true
```

Deleting `ssn` from the **clone's** nested map removed it from the **source**. Top-level deletes
are isolated; nested ones are not.

**Consequences:**

1. A consumer writing the obvious `RedactVariables` for a nested secret —
   `delete(m["applicant"].(map[string]any), "ssn")` — **mutates the live cached instance entry**.
   The next reader of that instance, and the next *persist*, sees the variable gone. A security
   control silently becomes destructive data loss.
2. If the consumer instead rebuilds the top level, the nested secret is **not redacted** and ships
   in the response. Both natural implementations are wrong, in opposite directions, and D4 gives no
   guidance because it never considered nesting.
3. **This resurrects the claim the ADR withdrew.** ADR-0186 §4 withdrew *"anything mutating the
   view mutates instance state"* as `ASSUMPTION (unverified)` **in both directions**, on the
   grounds that *"the cached path hands out a clone"*. The clone is shallow; for nested values the
   withdrawn claim is **TRUE**, and the withdrawal is what removed the reason anyone would check.
   `cloneInstanceEntry`'s "deep-copies" godoc is a false comment in shipped code.

**Verdict:** the pair inside D4 is unresolved, and the bundle's stated basis for treating aliasing
as a mere "convention violation" does not hold for the nested case the hook is for.

**Proposed fix.**

- Make the redaction boundary copy **deep** for the map it hands to `RedactVariables` (a
  JSON-shaped deep copy over `map[string]any` / `[]any`), and say so in D4. A shallow copy is
  sufficient for the *aliasing* defect and insufficient for the *hook*; D4 must specify the copy
  the hook needs, not the one `view.go:31` needed.
- Amend §4: the withdrawn claim is true for nested values; record the executed output above.
- Fix `cloneInstanceEntry`'s "deep-copies" godoc in phase 4 or 9 — it is a false comment reachable
  from this bundle's diff, which the Delivery Gate item 2 requires killing.
- Prescribe `TestRedactionOfANestedKeyDoesNotMutateTheSource` with the falsifier stated: *it fails
  against a `maps.Clone`-based copy* — and ⚠ **check the fixture**: a fixture whose variables are
  all top-level scalars cannot fail for the reason the test names.

---

## I-12, MAJOR — D2 mints `ErrEnvTooLarge`; D5 owns the classifier and never classifies it, so a caller-caused refusal returns a blank 500

**The pair:** D2 (new `ErrEnvTooLarge` sentinel) × D5 (`ClassifyError`'s closed, ordered switch).
Spec §5 has **no D2 × D5 row**.

**The interaction.** D2 introduces `expreval.ErrEnvTooLarge` and D5 rewrites `ClassifyError` in the
same bundle. Re-derived, the switch is closed and exhaustive with a `default`:

```
$ sed -n '26,59p' transport/http/httpcore/errors.go
switch {
case … kernel.ErrInstanceNotFound / ErrDefinitionNotFound / humantask.ErrTaskNotFound: 404  (:28)
case … authz.ErrNotAuthorized:                                                          403  (:32)
case … kernel.ErrConcurrentUpdate:                                                      409  (:34)
case … ErrBadCursor, ErrBadArmedTimerCursor, ErrBadInput, validation.ErrInvalidInput,
       engine.ErrInvalidOutcome, ErrOutcomeRequired, ErrEmptyTriggerKey,
       ErrEmptyReassignTarget:                                                          400  (:36-50)
case … service.ErrConflict, engine.ErrInvalidTransition, humantask.ErrInvalidTask:      422  (:51)
default:                                                                                500  (:57)
}
```

`ErrEnvTooLarge` matches nothing ⇒ **500 `{"error":"internal_error"}` with no message**. So the
one thing the caller could act on — *"your variable payload has too many elements"* — is rendered
as a server fault with a blank body, in the same delivery that adds a correlation id specifically
so blanked responses remain diagnosable. The bundle adds a sentinel and forgets to route it, in
the decision whose whole subject is routing sentinels.

⚠ Note the same omission applies to D1's `service` variable-cap sentinel on its **runtime** leg
(see I-10) — the bundle names 413 for the request leg and nothing for the other.

⚠ Also, a **counting discrepancy inside the ADR** worth handing to the counting lens: the ADR's
own banner says the 400 arm *"turns out to carry **nine** sentinels and four validation
strategies"*, while Decision 5's body and plan §4 both say **eight**. Re-derived from the switch:
**8** sentinels in 5 line-groups. The banner is wrong.

**Verdict:** unrouted new sentinel; no §5 row for the pair.

**Proposed fix.** Add to D5's table an explicit row for `ErrEnvTooLarge` and for the runtime
variable-cap sentinel, with the chosen status and message, and note in D5 that
`ClassifyError`'s `default` is a **500**, so *any* sentinel this bundle mints and does not route
becomes an internal error. Add a phase-4 test row asserting the status for each new sentinel, with
the falsifier stated: *it fails against an implementation that only adds the 413 arm.*

---

## I-13, MAJOR — "`internal/expreval` becomes, again, the ONLY wrapper over the expression vendor" is false after this bundle, and D5 edits one of the remaining violators without fixing it

**The pair:** D3 (routes `WithURLExpr` through `internal/expreval`, and states the single-wrapper
rule as its rationale) × D5 (edits `definition/model/validate/expr/expr.go` to stop echoing the
predicate source). Spec §5's row *"D5 × D3/D6 | none — different surfaces"* is again the claim
under attack.

**The interaction.** ADR-0186 Consequences (Positive) asserts:
*"`internal/expreval` becomes, again, the only wrapper over the expression vendor."* Context §3
frames `WithURLExpr` as **the** violation (*"against the repo's own rule that `internal/expreval`
is the single vendor wrapper"*). Re-derived by import line, not by keyword:

```
$ grep -rn '"github.com/expr-lang/expr' --include="*.go" . | grep -v "_test.go"
internal/expreval/expreval.go:14,15,16          ← the sanctioned wrapper
action/httpcall/httpcall.go:51,52               ← D3 fixes this one
action/transform/transform.go:12,13             ← NOT addressed
definition/model/validate/expr/expr.go:12,13    ← NOT addressed, and D5 EDITS THIS FILE
```

**Four** non-test files import the vendor; **three** are violators; D3 fixes **one**. After the
bundle ships, two remain — so the Consequences sentence is false on the day it is written.

⚠ Auditor's own correction, recorded because it is the failure mode this repo keeps making: a first
pass of mine using `grep -rln "expr-lang/expr"` returned **five** files, because
`definition/flow/flow.go:42` contains the string in a **comment**. Re-derived with the import-line
net it is four. The keyword grep and the import grep disagree; only the second answers the question.

**Two consequences beyond the false sentence:**

1. **`action/transform` is a second unbounded expression surface over process variables**, and the
   bundle never names it. `transform.Do(ctx, in)` receives `in = copyVars(s.Variables)` (same as
   `httpcall`, see I-4) and runs `expr.Run(b.prog, env)` (`transform.go:148`) with no timeout, no
   memoising wrapper, and — by I-4's argument — no reachable element bound. D2's mitigation covers
   neither of the two action-side expression surfaces.
2. **D5 has the file open.** Phase 2 edits `definition/model/validate/expr/expr.go:64,68` to stop
   `%q`-ing `v.source[i]` (confirmed at the anchor: `fmt.Errorf("workflow-validation/expr:
   predicate %q: %w", v.source[i], err)` and `"… predicate %q not satisfied"`). An implementer with
   that file open, told the repo rule is "one wrapper", will either route it through `expreval`
   unasked — changing validation semantics mid-phase — or leave a violation the ADR says does not
   exist.

**Verdict:** an over-reaching quantifier in Consequences, plus an unenumerated expression surface.

**Proposed fix.** Replace the Consequences sentence with the closed set: *"after this delivery, two
direct vendor importers remain — `action/transform` and `definition/model/validate/expr` — and
they are out of scope; `internal/expreval` is the only wrapper on the **engine** path."* Add
`action/transform` to spec §3's **Out** list by name (it is currently invisible), or open a backlog
item for it. Give phase 2's brief an explicit instruction not to re-route the validator through
`expreval`.

---

## I-14, MAJOR — D2's default-on element bound strands **already-persisted** instances on upgrade; the bundle's migration list covers only the body cap

**The pair:** D2 (element bound, default 10 000, on by default) × the shipped ADR-0049
deterministic-replay guarantee, which ADR-0186 D2 explicitly claims is *"untouched"*.

**The interaction.** D2's determinism argument is sound as far as it goes — a count over a map is
order-independent, so the same env yields the same verdict, and Go's randomized map iteration does
not perturb it. I confirm that claim **holds**. But determinism of the *verdict* is not the same as
preservation of the *result*, and D2 conflates them:

- An instance persisted **before** the upgrade with 20 000 variable elements evaluated fine.
- After the upgrade, the default-on bound refuses that env at every evaluation
  (`ErrEnvTooLarge`), so the instance stops advancing — and, by I-12, surfaces as a **blank 500**.
- Replaying that instance's history (ADR-0049) now produces a different outcome than it did
  originally. That is precisely a replay divergence, introduced by a change whose stated headline is
  *"this is the property the ctx did not have"*.
- Per I-9 there is **no repair verb**: no route mutates process variables; only
  `POST /admin/instances/:id/cancel` exits.

The bundle's Negative section acknowledges only the forward-looking half —
*"Default-on caps will reject payloads that work today"* — which reads as being about the 1 MiB
**body** cap, and is silent on instances already in the database.

**Verdict:** the same upgrade-stranding shape re-audit #2 rejected in ADR-0185 D4 (*"a pre-upgrade
task … becomes unclaimable, uncompletable, unreassignable and unrefreshable, forever, with no
migration and no repair verb"*), reproduced by D2 in the half of the bundle that was re-cut to
escape it.

**Proposed fix.** State the upgrade contract in D2 and in plan phase 9's migration item. Options,
in preference order: (a) make the element bound **advisory on rehydrated instances** — enforce at
admission only (which is I-2 option (a) and I-9 option (a), and closes all three findings with one
change); (b) ship the bound **default-off for one minor version** with a warn-level log, then
default-on; (c) keep default-on and provide the repair verb. Whichever is chosen, add the
pre-flight audit to phase 9 — *count instances whose persisted variable map exceeds 10 000
elements before deploying* — mirroring the ADR-0167 camelCase precedent the memory index records.

---

## I-15, MINOR — D4's redaction is a *display* control and D3's allowlist a *destination* control; neither stops a redacted variable leaving via an allowed host

**The pair:** D3 (SSRF allowlist) × D4 (variable redaction). Spec §5 has **no D3 × D4 row.**

**The interaction.** A consumer configures `RedactVariables` to strip `ssn` and reasonably concludes
the value is protected. It is not: `httpcall.Do` receives `in = copyVars(s.Variables)` — the
**unredacted** map (I-4, step 1) — so a definition author can write
`WithURLExpr('https://reports.example.com/?q=' + vars.ssn)` and, if `reports.example.com` is
reachable (public, or opted in via `WithAllowedHosts`), D3's dialer control and `CheckRedirect`
permit it: they filter by network location, never by payload. The same is true of `WithBodyKey` /
`WithHeaderFunc`, which are not in scope at all.

This is a documentation gap rather than a defect — the URL expression is author-supplied, and the
bundle correctly relies on that everywhere else. But D4 and D3 ship in one delivery whose
`SECURITY.md` (phase 9) will describe both, and a reader will compose them into a guarantee neither
makes.

**Proposed fix.** One sentence in phase 9's `SECURITY.md` item and in D4: *`RedactVariables`
controls what the HTTP **read** endpoints disclose. It does not constrain what definition-authored
actions do with the same variables; `action/httpcall` and `action/transform` receive the
unredacted map.*

---

## I-16, MINOR — D5 hardens the ordered switch for 413 and leaves no rule for ADR-0185's 401/503 arms, which the spec says were removed from *this* record but not from the roadmap

**The pair:** D5 (ordered `ClassifyError`) × the deferred ADR-0185 delivery. **Leakage check
result: clean** — a targeted grep for identity symbols across all three bundle files finds
`Eligibility`, `CheckSpecStated`, `Open *bool`, `Privileges`, `RefreshCandidates` and `claimant`
**nowhere** in ADR-0186 or the plan; the only mentions are the spec's §0/§3 roadmap prose, which is
descriptive. The severance is real and no other dependency exists.

The residual item is forward-facing. D1/D5 spend a full page establishing that
`ClassifyError`'s arms are **order-dependent** and that ignoring it ships a 400 where a 413 was
intended. ADR-0185 will add **401** and **503** arms to the same switch — 401 in particular sits
near the 403 arm D5 rewrites. Nothing in ADR-0186 records the ordering rule as an invariant the
*next* delivery must honour, so the lesson dies with the bundle that learned it.

**Proposed fix.** Add to D5, next to the 413-ordering comment requirement: *"`ClassifyError`'s
arms are order-dependent by construction. Any future arm — including ADR-0185's 401/503 — must
state its position relative to the existing arms and carry a test asserting an error matching two
arms resolves to the intended one."* One sentence; it is the entire content of I-1 and of
re-audit #2's finding 9, made durable.

---

# Complete pairwise matrix — all 15 pairs of D1…D6

Every cell was derived. No cell is unchecked.

| pair | verdict | detail |
|---|---|---|
| **D1 × D2** | **FINDING — Critical** | **I-9.** The byte cap (256 KiB) admits **45 540** elements, 4.55× the element cap (10 000), measured. The window 10 001–45 540 is persisted then unevaluable; no repair verb. Spec §5's "two knobs, two stated jobs" is a relabelling. |
| **D1 × D3** | **FINDING — Major** | **I-5.** `action/httpcall.ErrBodyTooLarge` already exists (`httpcall.go:94`) and means a **500**; D1 mints a second `ErrBodyTooLarge` meaning **413**, in a phase running in parallel with phase 6. Spec §5 has no D1 × D3 row. |
| **D1 × D4** | benign (note) | Opposite legs: the cap is inbound (`service`, pre-persist), redaction outbound (`httpcore`, pre-marshal); no ordering conflict. Note: there is **no response-side size bound**, so a `RedactVariables` that substitutes longer placeholders can grow a response past any inbound cap. Phase 5 (3 agents) and phase 9 (controller) both edit `groups.go`, but phase 9 depends on 8 depends on 5 — serialised, no collision. |
| **D1 × D5** | **FINDING — Critical ×2** | **I-1** (three optional-body decode sites discard the error, so the 413 chain never fires and the three adapters diverge on an admin route the parity suite structurally cannot reach) and **I-10** (variables grow via `mergeVars` during execution, so the cap fires with no HTTP caller and the static "request too large" is false). |
| **D1 × D6** | benign (positive) | Both caps reduce the volume of plaintext reaching `wrkflw_instances.snapshot`, which D6 names. No premise of either is disturbed. D6 should mention that the caps bound, but do not protect, at-rest data. |
| **D2 × D3** | **FINDING — Critical** | **I-4.** The OPEN question resolves **NO** and is unwireable (`Action` carries no evaluator); and routing `WithURLExpr` through `expreval.EvalString` replaces a non-string **rejection** with a **coercion** (executed: `nil`→`"<nil>"`, `1+1`→`"2"`, `{"a":1}`→`"map[a:1]"`). |
| **D2 × D4** | benign | The element bound counts the env on the way *in* to evaluation; redaction transforms the map on the way *out* to a response. `RedactVariables`'s output never re-enters an env. No shared state. (The nested-copy hazard is real but is D4-internal — see I-11.) |
| **D2 × D5** | **FINDING — Major** | **I-12.** `ErrEnvTooLarge` is minted by D2 and routed by nobody; `ClassifyError`'s `default` makes it a blank **500** — a caller-actionable refusal rendered as a server fault, in the delivery whose subject is routing sentinels. Also records the ADR-internal 8-vs-9 sentinel discrepancy. |
| **D2 × D6** | benign | The element bound never touches persistence; D6 defers all mechanism. No interaction. (D2's *upgrade* interaction is with ADR-0049, not D6 — see I-14.) |
| **D3 × D4** | **FINDING — Minor** | **I-15.** Redaction is a display control, the allowlist a destination control; `httpcall`/`transform` receive the **unredacted** variable map. Composed in one `SECURITY.md`, a reader will infer a guarantee neither makes. |
| **D3 × D5** | **FINDING — Major** | **I-13.** D3's stated rationale is the single-vendor-wrapper rule; re-derived by import line there are **4** importers, **3** violators, and D3 fixes **one**. D5 *edits* one of the survivors (`definition/model/validate/expr/expr.go`) and leaves the import, so the ADR's "the only wrapper" Consequence is false on the day it ships. Surfaces `action/transform` as a second unenumerated expression surface over process variables. |
| **D3 × D6** | benign (note) | Disjoint mechanisms. Note for phase 9: D6's `SECURITY.md` section and D3's "how to opt out of the SSRF default" land in the same document and must not be written as one posture — one is about data the library stores, the other about connections it makes. |
| **D4 × D5** | **FINDING — Major ×2** | **I-7** (spec §5's "disjoint, no interaction" is false: the 400 rendering names the redacted key, is instructed to cover `maxLength` whose leaf discloses a value **length**, and the two controls sit on opposite sides of a layer boundary with no shared config) and **I-8** (both force unlisted breaking changes to exported `httpcore` signatures — 8 endpoint functions for D4, `ClassifyError` for D5 — and phase-4 test 6 is in the wrong package as a result). |
| **D4 × D6** | benign (note) | Complementary and non-conflicting: D4 protects in flight, D6 concedes nothing at rest. Note: phase 9 must not let the redaction feature imply at-rest protection — the redacted value is still plaintext in `wrkflw_instances.snapshot`. One sentence. |
| **D5 × D6** | **FINDING — Critical** | **I-6.** D5 widens the `Logger` sink from `status >= 500` to 400 and 403, default-on to `slog.Default()`, carrying the raw validation error the ADR itself executed and showed contains `'123-45-6789'`. D4's hook does not reach it; D6's closed two-column enumeration excludes it. Spec §5's "D5 × D6 — none, different surfaces" is false. |

## Findings outside the 6×6 grid (a decision against something already shipped, or against itself)

| pair | verdict | detail |
|---|---|---|
| D2 × **itself** | **Critical** | **I-2.** "Signatures unchanged" and "count once per env" are jointly unsatisfiable. Executed: baseline 68.96 ns/op, count-typical 41.08 ns/op, count-at-bound **17.5 µs**, per-eval-count total 113.3 ns/op. The ADR's *"20–60× worse"* is refuted at the typical env (it is 1.6×, ~5% of the 866 ns refused) and holds only at the bound. |
| D2 × **shipped `runtime` options** | **Critical** | **I-3.** Default-on + `WithExpressionTimeout`'s unconditional assignment (`processdriver_options.go:200`) silently drops the element bound for the untrusted-definition consumer. Also: last-writer-wins is a **documented** contract (both godocs, `:196-197`, `:216-217`), not "silent" as the ADR says; default-on kills the `slog.Bool("conditionEval", …)` diagnostic (`processdriver.go:440`) and replaces the process-wide shared compile cache with a per-driver one. |
| D2 × **ADR-0049 replay** | **Major** | **I-14.** Default-on strands already-persisted instances above 10 000 elements, with no repair verb and no pre-flight audit — the ADR-0185-D4 upgrade-stranding shape reproduced in the half of the bundle re-cut to escape it. D2's *determinism* claim itself **holds** (a count is order-independent). |
| D4 × **itself** | **Critical** | **I-11.** The prescribed **shallow** copy plus a mutation hook: executed, deleting a nested key from `State.Clone()`'s result deletes it from the **source**. Both natural `RedactVariables` implementations are wrong, in opposite directions. Resurrects the claim §4 withdrew, and falsifies `cloneInstanceEntry`'s "deep-copies" godoc. |
| D5 × **deferred ADR-0185** | **Minor** | **I-16.** Leakage check **clean** — no ADR-0185 symbol appears in ADR-0186 or the plan. Residual: the order-dependence lesson is not recorded as an invariant for the 401/503 arms ADR-0185 will add. |
| any × **ADR-0095** | folded into I-1 | Admin routes are default-absent (`parity_test.go:658-660`), which is why phase 8's parity suite cannot see I-1's divergence. |
| any × **ADR-0145/0147** | no interaction | The audit model is touched only by D6, which explicitly defers and cites ADR-0145 for why `engine.NodeVisit` is not the place for a chain. Re-read at the anchor; nothing in D1–D5 writes to `wrkflw_journal` or `NodeVisit`. |
| D2 × **ADR-0003/0056 purity** | held | `engine/purity_test.go` constrains imports and `time.` calls; D2 edits neither `engine` nor its import list. The claim *"ADR-0003/0049/0056 need no amendment"* survives on the purity axis and fails on the replay axis (I-14) and the default-path axis (I-3). |

## Summary

**16 findings: 7 Critical, 6 Major, 3 Minor. All 15 D×D pairs derived; 8 further cross-cutting
pairs derived.**

**Spec §5's own table is wrong in five of its eight rows.** Both rows it marks **OPEN** resolve
against the bundle (I-3, I-4). Three rows it marks **✅ resolved** are not: D1 × D5 (I-1, I-10),
D2 × D1 (I-9), D4 × D5 (I-7, I-8), and D5 × D6 (I-6). Two rows are absent entirely: **D1 × D3**
(I-5) and **D3 × D4** (I-15). Only *D2 × the engine hot path* and *D4 × the response-customization
feature* survive — and the first survives only because I-2 recasts it as unimplementable rather
than unresolved.

**The recurring shape, stated once.** Five of the seven Criticals are a decision assuming a
*channel* that another decision owns and does not provide: D1 assumes every decode error reaches
D5's classifier (I-1); D2 assumes an env has a lifetime the interface does not expose (I-2); D2
assumes its plumbing reaches an action (I-4); D2 assumes admission and evaluation share an ordering
(I-9); D4 assumes a copy deep enough for a hook it added later (I-11). The bundle's §5 asks *"do
these two decisions conflict?"* — the question that finds these is **"what does this decision
assume someone else will hand it, and who agreed to?"**

**One change closes three Criticals.** Moving the element bound from evaluation to **admission**
(I-2 option (a) = I-9 option (a) = I-14 option (a)) resolves I-2, I-9 and I-14 together, and makes
I-3 moot. It is the single highest-value adjudication in this report.
