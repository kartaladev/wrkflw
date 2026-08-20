# 186. The library's untrusted-input and disclosure posture

> ## ⚠ RE-CUT 2026-08-21 into its own delivery. Awaiting its FIRST audit as a standalone bundle.
>
> This record was half of the B3 bundle, which **failed two rule-#9 audits** — the second on the
> interactions between the four decisions its revision rewrote. Owner decision, 2026-08-21: B3 is
> re-cut into **three deliveries**, and this is the first, because the spec's own §5 says
> *"0186's decisions hold whether or not 0185 ships, and vice versa"* and its remaining findings are
> narrow and local. The other two are ADR-0185-core (D1+D2+D3, backlog 51/52/53) and the deferred
> D4/D5 (backlog 103/124), each of which needs a design increment this record does not.
>
> **Folded from re-audit #2** (`docs/plans/sweep-evidence/reaudit-b3-adjudication.md`):
> D5's value-free 400 was **not implementable where prescribed** and moves into
> `runtime/validation.Gate`; the 400 arm turns out to carry **eight sentinels and four validation
> strategies**, one of which leaks a predicate source, so D5 becomes **allow-list** rendering with a
> static default; **413 would have shipped as 400** because every adapter decode site already wraps
> in `ErrBadInput` and `ClassifyError`'s arms are ordered; redaction in `mapInstance` **misses two
> non-admin endpoints** that take no mapper; and D2's replacement bound costs more than the cost it
> refused unless it is computed once per env.
>
> ⚠ **Not yet audited as a standalone bundle.** Not an input to implementation.

- Status: **Proposed** (pending rule-#9 audit as a standalone delivery)

- Date: 2026-08-20, revised 2026-08-21
- Relates to: ADR-0003 / ADR-0049 / ADR-0056 (evaluator purity, deterministic
  replay, and the opt-in timeout — **not amended**, see Decision 2), ADR-0081,
  ADR-0095, ADR-0145/0147. ⚠ **ADR-0185 (identity) is a SEPARATE, LATER delivery** —
  this record must not depend on any symbol it introduces.
- Backlog: 54, 65, 98, 99, 104, and the posture for 100 / 101

## Context

ADR-0185 will answer *who the actor is*; it is a **separate, later delivery** and
nothing here depends on it. This record answers the other half: **what the library
accepts from a caller, and what it hands back**. Six verified findings, plus
two where the honest decision is to decide a posture and defer the mechanism.

1. **No body cap anywhere.** Re-counted, non-test: `transport/http/stdlib` has **13**
   `json.NewDecoder` sites, `gin` **13** `ShouldBindJSON`, `fiber` **13**
   `c.Bind().JSON`, `httpcore` **0** — **39 across three idioms**, all in each
   package's `groups.go`. `grep -rnE "MaxBytesReader|BodyLimit" transport/` → **0**.
   ⚠ Note the `-E`: the draft wrote this and one other grep **without** it, so `|`
   was a literal and the command returned 0 for *any* repository — evidence that
   could not falsify the claim it was offered for. Re-run correctly, the claims hold.
   Fiber's 4 MiB rejection is `fiber.DefaultBodyLimit` (`fiber/v3@v3.4.0/app.go:585`,
   applied in `New()` at `:710`), i.e. the framework's, not ours, so 26 of the 39
   sites — exactly two thirds — have no cap and the third that appears to have one
   did not get it from us. There is no process-variable size limit either.

2. **Expression cost is unbounded in its input.** ⚠ **The audit's `MaxNodes` fix is
   inverted, and this was executed** — and the vendor says so outright in its own
   godoc (`expr@v1.17.8/expr.go:221`: *"If MaxNodes is set to 0, the node budget
   check is disabled"*). `expr.MaxNodes(0)` is what *disables* the check; never
   calling it leaves `DefaultMaxNodes = 1e4` **active**, and a 20 000-node expression
   already fails to compile. The unmetered axis is **caller-supplied array size**.
   Measured with an **80-character** predicate (⚠ the draft said 44, three times;
   `wc -c` says 80 — the argument that it is far under a 1e4-node budget is
   unaffected, which is why nobody checked it):
   25 ms → 98 ms → 391 ms → 1.563 s at n = 1 000 / 2 000 / 4 000 / 8 000. Clean
   O(n²), invisible to any node cap.
   ⚠ Two evaluator **surfaces**, not one: `authz`'s is `expreval.New()`, i.e.
   `DefaultTimeout = 5 s` **is** enabled; only the engine's gateway evaluator
   (`engine/conditions.go:43`, `expreval.WithTimeout(0)`) is wall-clock unbounded,
   and that is a deliberate ADR-0003/0049/0056 trade, not an oversight.

3. **`httpcall` is an SSRF primitive reachable from process variables.**
   `WithURLExpr` (`action/httpcall/httpcall.go:125-134`) calls raw `expr.Compile` —
   **not** `internal/expreval` — so it has neither the memoising cache nor the
   timeout guard, against the repo's own rule that `internal/expreval` is the single
   vendor wrapper. `grep -rnE "CheckRedirect|expreval" action/httpcall/` → **0**. The
   default client is `&http.Client{Timeout: 30s}` with no `CheckRedirect` and no
   allowlist. The hazard **is** documented in the godoc at `:119-123`, which makes
   this a posture question — *should the library ship a safe default?* — rather than
   an oversight.

4. **The instance read path aliases, and discloses.**
   `transport/http/httpcore/view.go:31` assigns `Variables: st.Variables` — an alias
   of the caller's map, not a copy.
   ⚠ **The draft's consequence claim is withdrawn.** It said *"anything mutating the
   view mutates instance state"*, entered without execution, in the position that
   justified calling this a live bug. The read path contradicts it: the cached path
   hands out a clone (`persistence/caching_instance_store.go:73-76` →
   `cloneInstanceEntry` → `State.Clone()`; `engine/step_state.go:361-363`
   `cloneState` calls `copyVars`), and the uncached path decodes a fresh snapshot
   from the row. So the aliased map is a **per-request value**, and no path from
   mutating it to persisted state has been demonstrated. `ASSUMPTION (unverified)`
   in **both** directions — neither the draft nor this record may assert it. What
   remains is a real convention violation: every other escape boundary in this repo
   clones (`HumanTask.Clone`, `Actor.Clone`, `ActiveTasks`), and this one does not.
   There is no redaction hook anywhere on `CustomizeConfig`, and the `SECURITY:`
   caveat exists at exactly **three** non-test sites (`stdlib/groups.go:189`,
   `gin:204`, `fiber:209`), all on the **admin** group; the instance and task groups
   carry none.

5. **Five 4xx classes echo `err.Error()` verbatim** —
   `transport/http/httpcore/errors.go` at 404 (`:31`), 403 (`:33`), 409 (`:35`),
   400 (`:50`), 422 (`:56`); 500 (`:58`) correctly blanks. The switch has exactly six
   arms and the set is closed.
   Executed: a 403 produced by an ABAC evaluation error returns the predicate source
   **twice** — once from `%q` in `internal/expreval/expreval.go:135`, once inside
   expr's own snippet — carrying whatever process-variable and actor-attribute names
   the deployment's policy names. ⚠ The draft added *"plus a caret line rendering it
   again"*; that third line is dots and a `^`, no source text. The count "twice" that
   preceded it was right and the recap appended to it was not. A **bare** deny
   returns only `"workflow-authz: not authorized"` and leaks nothing, so the leak is
   confined to the eval-error arm of 403.
   ⚠ **And 400 leaks too — the draft's own open question resolved against it.**
   Spec §4.7 recorded `ASSUMPTION (unverified)`: whether `validation.ErrInvalidInput`
   messages can contain submitted **values**. Executed against the repo's own
   jsonschema strategy with input `{"ssn":"123-45-6789"}`:
   `- at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'` — the submitted
   value, verbatim, in the arm the draft deliberately preserved as "actionable", for
   exactly the constraint used to shape national-ID / card / account-number fields.
   A sibling leaf reports `maxLength: got 11, want 3`, disclosing a length.

6. **Nothing is protected at rest, and nothing is tamper-evident.**
   `grep -rniE "encrypt|redact"` over `persistence/`, `internal/persistence/` and
   `engine/` (non-test) → **0**. `wrkflw_instances.snapshot` and
   `wrkflw_journal.trigger` are plaintext `TEXT NOT NULL`
   (`…/migrations/sqlite/0001_init.sql:25,40`). `wrkflw_journal` is **6** columns —
   no hash, no prev-hash, no signature. `engine.NodeVisit` carries no actor field, by
   ADR-0145 design; the actor's real homes are the task record and the journal's
   `trigger` payload.

The system-level shape matters: there is **no definition-deploy route** among the 26
HTTP routes (9 non-admin + 15 admin + 2 health), so expression *source* (2, 3) is
author-supplied, not anonymous-caller-supplied. That is what keeps 2 and 3 serious
rather than critical — and it is a property of today's route table, not a guarantee.

## Decision

### 1. Bodies and variables are capped, by default, and oversize has ONE status

- `httpcore.CustomizeConfig.MaxBodyBytes`, default **1 MiB**, honoured by every
  adapter through its own idiom: `http.MaxBytesReader` for stdlib; the same wrapper
  applied to `c.Request.Body` *before* `ShouldBindJSON` for gin; and a
  `len(c.Body())` pre-check for fiber, because `BodyLimit` is a `fiber.Config` field
  set on `fiber.New` — the **app**, which a mounted route group does not own.
  ⚠ Conceded plainly: fiber's pre-check is a **rejection, not a prevention** — the
  body is already buffered by the time it runs, and `fiber.DefaultBodyLimit` is what
  actually prevents the amplification there.
- `service.WithMaxVariableBytes`, default **256 KiB** for an instance's variable map,
  refused before persist with a sentinel.
  ⚠ **256 KiB is a judgement call and is labelled as one.** The revision's claim that
  *"the 256 KiB and 10 000 numbers are now derived"* is false for 256 KiB: nothing
  derives it, and this record's own Decision 2 **refutes its only stated rationale**
  by showing that 256 KiB of JSON integers admits ~40–50 k elements ⇒ ~45–60 s of
  CPU. It is a **payload/storage** bound with no CPU claim attached, and it stays
  `ASSUMPTION (unverified)` until a consumer reports a real workload.

**Oversize is a 413, and the mapping is named rather than assumed.** ⚠ The draft
asserted 413 in three documents without saying how any adapter produces it.
`http.MaxBytesReader` does not produce a status — it makes the next `Read` fail,
surfacing inside `json.NewDecoder(...).Decode(...)` as an error the 400 arm would
classify; Go returns `*http.MaxBytesError` (needing `errors.As`), gin wraps it again,
and fiber's pre-check produces a home-grown error. Three adapters, three error
shapes. Therefore:

- a new `httpcore.ErrBodyTooLarge` sentinel;
- each adapter converts its own oversize signal into that sentinel **before**
  calling `ClassifyError` (`errors.As` for stdlib and gin; the pre-check for fiber);
- `ClassifyError` maps the sentinel → **413**.

⚠⚠ **And the oversize error must NOT carry `ErrBadInput`, or it ships as 400.**
Re-audit #2 caught this and it was confirmed against source. Every one of the 39
decode sites *already* double-wraps —
`writeErr(cfg, gc, fmt.Errorf("%w: %w", httpcore.ErrBadInput, err))`
(`transport/http/gin/groups.go:33-35`, and the stdlib/fiber equivalents) — and
`ClassifyError` is an **ordered** `switch`: 404 `:28`, 403 `:32`, 409 `:34`,
**400 `:36-50`**, 422 `:51`, default 500. An error wrapping *both* `ErrBadInput` and
`ErrBodyTooLarge` matches the 400 arm first, so an implementer who follows the
conversion instruction literally — converting the signal while keeping the existing
wrapper — ships **400**, and the 413 assertion lives two phases downstream where the
failure is hardest to attribute. Therefore:

- the adapter returns the **bare** `ErrBodyTooLarge` on the oversize path; the
  `ErrBadInput` wrap is for **decode** failures only;
- the **413 arm is placed before the 400 arm** in `ClassifyError`, with a comment
  saying why (the arms are order-dependent);
- every adapter phase — not just `httpcore` and the parity suite — carries a
  `TestOversizedBodyReturns413` whose falsifier is stated: *it fails against an
  implementation that keeps the `ErrBadInput` wrapper.*

Both caps default **on**. Pre-v0.1.0 is the window in which a fail-closed default is
cheap; after it, it never gets one.

### 2. Evaluation input is bounded. `ConditionEvaluator` does NOT gain a `ctx`.

**The `ctx` half of the draft's Decision 2 is dropped.** The reasoning it was
justified by does not survive:

- The draft argued *"core purity (`engine/purity_test.go`, which forbids OTel and
  clockwork imports) is unaffected — a `ctx` is not a wall clock."* True, and
  irrelevant. `purity_test` checks the **import list** and `time.` calls; it cannot
  observe the invariant actually at risk. `engine/conditions.go:29-43` states that
  one in the very file the change would edit: the guard is *"explicitly DISABLED …
  the engine core must stay wall-clock-free and SIDE-EFFECT-FREE (locked invariant,
  ADR-0003), so the default Step never spawns the guard's goroutine/timer"*, and a
  consumer opting into a timeout-capable evaluator is *"an explicit opt-in TRADING
  THE DETERMINISTIC-REPLAY GUARANTEE for DoS protection"* (ADR-0056).
- `expreval.run` is **synchronous** when `timeout <= 0` (`internal/expreval/expreval.go:74-76`),
  so there is no mechanism by which a ctx cancellation interrupts it. Honouring a ctx
  requires taking the goroutine path — measured on an ordinary gateway condition
  (`vars.amount > 100`): **99.43 ns/op / 3 allocs → 965.20 ns/op / 9 allocs**, ~9.7×,
  on CLAUDE.md's named hot path.
- So the change was a dilemma the draft never posed: either the default evaluator
  honours the ctx, silently converting ADR-0056's *explicit opt-in* trade into
  everyone's default — retiring deterministic replay repo-wide without amending
  ADR-0003/0049/0056 — or it discards the parameter, which is verbatim the reason
  the draft gave for rejecting the `ContextConditionEvaluator` alternative.

Dropping it is not a retreat, because **the ctx was never the mitigation**. As
`expreval`'s own `ErrEvalTimeout` godoc says, Go cannot preempt a running goroutine:
a wall-clock guard bounds *latency*, not CPU. What bounds CPU is the input:

- `internal/expreval` gains `WithMaxEnvElements(n int)`: evaluation is refused, with
  a new `ErrEnvTooLarge`, when the bounded element count reachable from the env
  exceeds `n`.
- `runtime.WithMaxEvalElements(n int)` is the plumbing, and it is **real**:
  it constructs the driver's evaluator
  (`expreval.New(expreval.WithTimeout(0), expreval.WithMaxEnvElements(n))`) and
  assigns `driver.conditionEval`, which reaches the engine through the existing
  `StepOptions.Evaluator` seam (ADR-0056). ⚠ The draft's *"reusing Decision 1's
  variable cap as the same knob"* was a **zombie**: the plan built
  `WithMaxEnvElements` (`int` elements, `internal/expreval`) and
  `WithMaxVariableBytes` (`int64` bytes, `service`) as two unconnected halves with
  nothing plumbing either into `engine/conditions.go`. They are now two knobs with
  two stated jobs: elements bound **evaluation**, bytes bound **payload/storage**.
- An input bound is **deterministic** — the same env yields the same verdict — so
  ADR-0003/0049/0056 are untouched. This is the property the ctx did not have.

**The default is 10 000 elements, and the number is derived, not asserted.**
Extrapolating the measured O(n²) ladder (1.563 s at n = 8 000):

| n | extrapolated | note |
|---|---|---|
| 2 000 | ~100 ms | a tight bound for latency-sensitive deployments |
| 10 000 | **~2.4 s** | the default |
| 43 000 | ~45 s | what 256 KiB of JSON integers admits |
| 50 000 | ~61 s | " |

⚠ This also refutes the draft's `MaxVariableBytes` framing **and** a number the
audit itself proposed. The draft called 256 KiB the CPU mitigation; its own table
falsifies that, since 256 KiB admits ~40–50 k elements ⇒ ~45–60 s of unpreemptible
CPU per evaluation. And the audit's suggested replacements — *"5 000 elements ≈
40 ms, 10 000 ≈ 150 ms"* — are wrong by roughly 15×: re-derived from the same ladder,
5 000 ≈ 610 ms and 10 000 ≈ 2.4 s. An inherited number restated without re-deriving
it is the failure this bundle already made once.

**The bound is computed ONCE PER ENV, not per evaluation — or it costs more than it
saves.** ⚠ Re-audit #2 caught this and it is the sharpest objection to Decision 2 as
a whole: this record drops the `ctx` to avoid **866 ns/op** on a gateway condition,
then substitutes a mechanism that must walk the env to count elements. Measured on
this machine: **~84 ns/op** on a typical few-variable env and **~19 µs at the 10 000
default** (a re-audit lens measured ~52 µs with a different implementation — same
order, same conclusion). Counting per *evaluation* would therefore be **20–60× worse
than the cost the decision refused**, which would make Decision 2 self-defeating.

A token step evaluates many gateway conditions against the **same** variable map, so
the count is a property of the env, not of the predicate. It is computed when the
variable map changes and carried alongside it; evaluation compares a number. State
this in the implementation, or the mechanism is a regression wearing a mitigation's
clothes — the same shape this record accuses the draft's `MaxNodes` fix of.

⚠ **`runtime.WithMaxEvalElements` collides with two existing options.**
`runtime.WithExpressionTimeout` (`runtime/processdriver_options.go:198`) and
`runtime.WithConditionEvaluator` (`:217`) both assign `driver.conditionEval` — the
same field. Three options writing one field is last-writer-wins, silently. The
option must **compose** with a consumer-supplied evaluator (wrap it) or **refuse**
the combination at construction with a named error; it must not quietly overwrite.
This is not decided here beyond "it must not be silent" — the plan carries it as a
task with both alternatives.

**What this does NOT bound, stated rather than implied:** the curve is for one
measured quadratic predicate. A higher-degree *predicate* over a bounded input is
still expensive, and on the engine's gateway path there is no wall-clock backstop by
ADR-0056's deliberate trade (the ABAC path retains its 5 s `DefaultTimeout`). What
the bound removes is the **caller-supplied-input** axis; the predicate-complexity
axis remains author-supplied, and there is no definition-deploy route.

⚠ **Do NOT implement the `MaxNodes` fix** — Context §2 shows it is inverted and the
check is already in force.

### 3. Expression-derived URLs are restricted by default; author-typed URLs are not

- `WithURLExpr` is routed through `internal/expreval`, so it inherits the bounded,
  memoising evaluator like every other expression site in the repo.
- When a URL is **expression-derived**, the default transport refuses loopback,
  link-local (`169.254.0.0/16`, `fe80::/10`), RFC1918/ULA and cloud metadata
  addresses via a `net.Dialer.Control` hook, and `CheckRedirect` refuses a redirect
  whose host leaves the allowlist.
- `WithAllowedHosts([]string)` opts specific hosts back in.
  `WithUnrestrictedTransport()` makes the current permissive behaviour explicit.
- `WithBaseURL` is **unchanged**: a URL the definition author typed is not
  attacker-controlled, and default-denying it would break every existing user for no
  gain.

### 4. Redaction runs at the response boundary, ABOVE `InstanceMapper` — and the view copies

- `httpcore.CustomizeConfig.RedactVariables func(map[string]any) map[string]any`.
- ⚠ **It runs above `InstanceMapper`, which bypasses it wholesale.** The draft placed
  it inside `NewInstanceView`, where `CustomizeConfig.InstanceMapper` replaces the
  default mapper and receives the raw `engine.InstanceState` (`seam.go:26-28`, `:41`;
  `endpoints.go:124,140,156`). The seam CLAUDE.md lists as a product feature —
  *"the API surface must allow customizing the `ProcessInstance` response shape"* —
  would silently disable the security control the same ADR adds.
- ⚠⚠ **But `mapInstance` is not the boundary either.** Re-audit #2, confirmed against
  source: `httpcore` exports **two** instance-read endpoints that take no mapper at
  all and never reach `mapInstance` —
  `GetInstanceSnapshot` (`endpoints.go:60`) returns the raw
  `service.ProcessInstance`, whose JSON projection carries variables verbatim
  (`service/instance.go:125` `json:"variables,omitempty"`, assigned at `:344`); and
  `GetActionableView` (`endpoints.go:72`) renders open human tasks, whose
  `HumanTask.Vars` is the per-task variable snapshot. **Both are non-admin routes** —
  precisely the groups Context §4 flags as carrying no `SECURITY:` caveat. A fix
  confined to `mapInstance` leaves `GET …/snapshot` returning unredacted process
  variables.
  ⇒ **Redaction is applied at the `ProcessInstance` → response boundary**, in a
  helper every read path calls, not in `mapInstance`. `AdminListInstances` was
  checked and is clean — it projects no variables (`admin_endpoints.go:81-95`).
  The Consequences must name the covered set rather than claim closure.
- The **default** is a shallow copy, which fixes the aliasing defect at `view.go:31`
  whether or not a consumer redacts anything, and restores the repo's
  clone-on-escape convention.
- The `SECURITY:` caveat is added to the instance and task route groups in all three
  adapters, so the three admin-only occurrences stop implying the others are safe by
  omission.

### 5. A 403 says nothing, 400 says everything except the value, 5xx unchanged

`ClassifyError` gains a per-class message policy:

| status | message |
|---|---|
| 403 | static `"not authorized"` — raw error logged at the transport seam |
| **400** | **value-free rendering** — see below |
| 404, 409, 422 | unchanged |
| **413** (new) | static `"request too large"`, via `ErrBodyTooLarge` (Decision 1) |
| 5xx | unchanged (already blank) |

**400 is an ALLOW-LIST: structured renderings are enumerated, everything else is
static.** Blanking 400 wholesale was rejected — a consumer needs to know *which
field* failed *which constraint* to fix their request. But two re-audit findings
reshape how that is delivered:

⚠⚠ **It cannot be done at `ClassifyError`, because the typed error never gets
there.** `runtime/validation/gate.go:45` is
`fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` — `%s`, not `%w`. The structured
error is flattened to a **string** two layers before the transport, so
`errors.As(err, **jsonschema.ValidationError)` is **false** at `ClassifyError`. The
draft's probe called the vendor directly and never went through `Gate`. **The
rendering therefore lives in `runtime/validation`, at the point the strategy's error
is still typed**, and the transport renders what it is given.

⚠⚠ **And the 400 arm is far wider than one strategy.** Re-derived: the arm matches
**eight sentinels across five `errors.Is` groups** (`kernel.ErrBadCursor`,
`kernel.ErrBadArmedTimerCursor`, `ErrBadInput`, `validation.ErrInvalidInput`,
`engine.ErrInvalidOutcome`, `engine.ErrOutcomeRequired`, `engine.ErrEmptyTriggerKey`,
`engine.ErrEmptyReassignTarget`), all rendered by the single `Message: err.Error()`
at `:50`. And `validation.ErrInvalidInput` wraps **four** strategies —
`jsonschema`, `expr`, `avro`, `callback` — of which only `jsonschema` yields a
structured leaf. ⚠ **`expr` echoes the predicate source** into the 400 body
(`definition/model/validate/expr/expr.go:64,68`, `%q` on `v.source[i]`) — *the same
disclosure* Context §5 establishes for the 403 eval-error arm, inside the arm the
draft declared fixed. `callback` emits whatever a consumer's validator writes.

So the policy is **deny-by-default text with an allow-list of structured renderings**:

| 400 source | message |
|---|---|
| `jsonschema` strategy | structured leaves: `at '/ssn': violates pattern` — built for **every** keyword, not just `pattern` (the `maxLength` leaf discloses `got 11, want 3`, a length) |
| `expr` strategy | static — and `expr.go:64,68` stops echoing `v.source[i]` on the runtime path |
| `avro`, `callback`, and the other seven sentinels | static `"invalid input"` + the correlation id; raw error to `CustomizeConfig.Logger` |

⚠ The draft's Consequences said *"the two 4xx arms proven to leak stop leaking"*.
That was true for one strategy of four and one sentinel group of five. The allow-list
is what makes it true, and the tests must cover the **uncovered** cases — a test over
`jsonschema` alone has exactly the fix's own coverage and can never reveal the gap.

**Every error body gains a correlation id**, echoed in the log line, so an operator
can join a blanked 403 to its cause. ⚠ The draft never said where the value comes
from. It is the **OTel span id when a span is recording** — `CustomizeConfig` already
carries `TracerProvider`/`MeterProvider` (`seam.go:30-31`), so no new dependency —
falling back to a random hex id otherwise. `CustomizeConfig.Logger`'s godoc says
*"receives 5xx raw error details (never sent to clients)"*; that widens to **400 and
403** and must be corrected in place.

⚠ `ErrorBody` is a **breaking wire change** and belongs in the migration list: the
403 message changes, the 400 message changes shape, and a field is added. The draft
listed four breaking changes and this was not among them.

### 6. At rest: the posture is documented, the mechanism is deferred — deliberately

`wrkflw` **does not** encrypt process variables at rest and **does not** claim a
tamper-evident audit trail. `SECURITY.md` says so explicitly, names the two plaintext
columns (`wrkflw_instances.snapshot`, `wrkflw_journal.trigger`), and states what the
consumer owns (database-level encryption, grants, backup handling).

The mechanisms are deferred to their own future ADR, and the deferral is the
decision, not an omission:

- A `persistence.VariableCodec` without a **key-rotation and key-loss** story is
  worse than none: a consumer who rotates a key makes every stored instance
  unreadable, and the library must not own key management.
- A hash-chained `wrkflw_journal` whose chain head lives in the same database the
  attacker already writes to is security theatre. Tamper-*evidence* requires
  externalising the head, a deployment contract the library cannot impose.
  `engine.NodeVisit` is explicitly **not** the place for it (ADR-0145).

Recording "we do not do this, and here is why doing it badly is worse" is a decision
a consumer can act on. Silence is not.

## Consequences

### Positive

- The unbounded-input surface closes on both axes that were measured: body size
  (39 sites, one policy, one status) and evaluation input (the O(n²) axis a node cap
  cannot see) — and the second is bounded **deterministically**, so no locked
  invariant is traded to get it.
- `ConditionEvaluator` keeps its signature. The engine default stays synchronous at
  ~99 ns/op, `runtime.WithConditionEvaluator` stays source-compatible, and
  ADR-0003/0049/0056 need no amendment.
- `httpcall` stops being an SSRF primitive on its untrusted axis while staying
  ergonomic on its trusted one.
- Both 4xx arms proven to leak stop leaking, **and the claim is scoped to what the fix
  covers**: 403 becomes static; 400 becomes an **allow-list** — structured rendering
  for the one strategy that yields structured leaves (`jsonschema`), static text for
  the other three strategies and the seven non-validation sentinels. ⚠ The draft's
  *"the four that carry useful information keep it"* was true for one strategy of four
  and one sentinel group of five.
- Redaction is applied at the `ProcessInstance` → response boundary, so it covers the
  two mapper-less non-admin read endpoints (`GetInstanceSnapshot`,
  `GetActionableView`) as well as the six that go through `mapInstance`. ⚠ The draft
  covered only the latter, and its *"cannot be bypassed"* sentence was true of the
  mapper and false of the endpoints.
- `internal/expreval` becomes, again, the only wrapper over the expression vendor.
- The at-rest posture becomes a written statement a consumer can hold the library to.

### Negative / costs

- **BREAKING**: `ErrorBody`'s message content and shape change for 400 and 403, and
  a correlation-id field is added. Consumers matching on `ErrorBody.Message` break.
- Default-on caps will reject payloads that work today. 1 MiB is still a
  **judgement call**, explicitly `ASSUMPTION (unverified)`; the 256 KiB and 10 000
  numbers are now derived, and the derivation is written down so the next reader can
  check it rather than inherit it.
- A consumer whose `httpcall` node legitimately targets an internal `10.x` address
  from a variable-derived URL must now say so explicitly.
- The engine's gateway evaluation remains wall-clock unbounded for a pathological
  **predicate**. That is ADR-0056's standing trade, restated rather than quietly
  reversed, and this record does not close it.
- 100 and 101 stay **open**. A consumer with a regulatory at-rest requirement gets a
  documented "no", not a solution.

### Neutral / follow-ups opened

- The three adapters now share a per-request policy (body cap, correlation id) with
  no shared route table: backlog **96**'s blind parity suite becomes more expensive to live
  without, not less.
- Backlog **60**/**91** (trace and schema envelopes on the outbox) share the journal
  column a future integrity chain would use; design them together, not as three
  migrations of one table.
- Whether the engine's gateway path should get a *deterministic* cost bound (an
  instruction budget rather than a wall clock) is now the open question ADR-0056's
  trade leaves behind. It is not decided here.
