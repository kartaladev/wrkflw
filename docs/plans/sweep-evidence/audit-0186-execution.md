# ADR-0186 audit — EXECUTION lens

- **Date:** 2026-08-21
- **Bundle commit:** `32f4e3e5` (detached worktree `a186-exec`)
- **Step 0:** PASSED — all three bundle files present
  (`docs/specs/2026-08-21-untrusted-input-and-disclosure.md`,
  `docs/adr/0186-untrusted-input-and-disclosure-posture.md`,
  `docs/plans/2026-08-21-untrusted-input-and-disclosure.md`).
- **Machine:** darwin/arm64, go1.26.6, Apple M4 Pro. All probes container-free.
- **Scope:** ADR-0186 only. Identity material (ADR-0185) explicitly out of scope.

---

## HELD — H1. The O(n²) ladder reproduces AND the n = 10 000 extrapolation is correct

**Claim attacked** (spec §7): *"`ASSUMPTION (unverified)`: the element-bound
extrapolations beyond n = 8 000 are arithmetic on the measured ladder, not fresh
measurements. **The audit should re-measure at n = 10 000.**"* and ADR-0186 D2's table
row *"| 10 000 | **~2.4 s** | the default |"*.

**Probe** — `zzprobe/ladder_test.go`, the bundle's own 80-byte predicate,
`expreval.New(expreval.WithTimeout(0))`:

```
go test -run '^TestLadderIncluding10000$' -v -count=1 ./zzprobe/
```

**Observed** (verbatim):

```
=== RUN   TestLadderIncluding10000
    ladder_test.go:23: predicate len = 80 bytes
    ladder_test.go:33: n=1000   elapsed=25ms       out=true
    ladder_test.go:33: n=2000   elapsed=98ms       out=true
    ladder_test.go:33: n=4000   elapsed=394ms      out=true
    ladder_test.go:33: n=8000   elapsed=1.57s      out=true
    ladder_test.go:33: n=10000  elapsed=2.458s     out=true
--- PASS: TestLadderIncluding10000 (4.55s)
```

**Verdict: HELD.** Predicted 2.442 s, measured **2.458 s** — 0.65 % error. The
predicate is confirmed 80 bytes. The ladder matches the spec's own 25/98/391/1563 ms
to within noise. **The `ASSUMPTION (unverified)` on the n = 10 000 extrapolation can
be DISCHARGED** and the ADR's derivation of the 10 000 default stands.


---

## E1, **CRITICAL** — "the bound is computed ONCE PER ENV" is NOT REACHABLE at the seam ADR-0186 D2 puts it behind

**Claim attacked** — ADR-0186 D2:

> **The bound is computed ONCE PER ENV, not per evaluation — or it costs more than it
> saves.** … A token step evaluates many gateway conditions against the **same** variable
> map, so the count is a property of the env, not of the predicate. **It is computed when
> the variable map changes and carried alongside it; evaluation compares a number.**
> State this in the implementation, or the mechanism is a regression wearing a
> mitigation's clothes

and plan phase 1:

> - `func WithMaxEnvElements(n int) Option` … - The count is **supplied with the env, not
>   computed per evaluation** (see below).
> ⚠ **No `ctx` methods.** … **the existing three methods keep their signatures**

**Probe** — `zzprobe/reach_test.go::TestSeamShape`, reflecting the real interface:

```
go test -run 'TestSeamShape' -v -count=1 ./zzprobe/
```

**Observed** (verbatim):

```
=== RUN   TestSeamShape
    reach_test.go:30: ConditionEvaluator.EvalBoolfunc(string, map[string]interface {}) (bool, error)
    reach_test.go:30: ConditionEvaluator.EvalDurationfunc(string, map[string]interface {}) (time.Duration, error)
    reach_test.go:30: ConditionEvaluator.EvalStringfunc(string, map[string]interface {}) (string, error)
--- PASS: TestSeamShape (0.00s)
```

Source-verified corroboration — every non-test caller passes a bare map and **nothing
else**:

```
$ grep -rn "EvalBool(\|EvalString(\|EvalDuration(" --include="*.go" . | grep -v _test.go
engine/step_gateways.go:41:  ok, err := eval.EvalBool(f.Condition, s.Variables)
engine/step_gateways.go:185: ok, err := eval.EvalBool(f.Condition, s.Variables)
engine/step_errors.go:52:    return eval.EvalBool(n.ErrorExpr, env)
engine/step_boundaries.go:63:      resolvedKey, err := eval.EvalString(n.CorrelationKey, s.Variables)
engine/step_eventsubprocess.go:104:resolvedKey, err := eval.EvalString(se.CorrelationKey, s.Variables)
engine/step_nodes.go:90,121,858,1008: c.pol.eval.EvalString(..., c.s.Variables)
engine/trigger_resolve.go:19: d, err := eval.EvalDuration(code, env)
authz/authz.go:136:          ok, err := attrEval.EvalBool(spec.Attribute, env)
internal/authz/casbin/authorizer.go:68: ok, err := a.attrEval.EvalBool(spec.Attribute, map[string]any{"actor": actor, "vars": vars})
```

**Verdict: CONFIRMED.** `WithMaxEnvElements` is an option on `*expreval.Evaluator`;
the evaluator learns about an env **only** through the `env map[string]any` parameter
of the three methods. There is **no channel** through which a precomputed count can
be "carried alongside" the env:

- a `map[string]any` is not comparable, so it cannot key a memo table;
- the three signatures are explicitly frozen by the plan ("the existing three methods
  keep their signatures"), so no `EvalBoolWithCount(code, env, n)` may be added;
- the map is not a named type, so no method/field can hang off it;
- smuggling the count as a reserved key inside the env pollutes the expression
  namespace the definition author writes against (and `expr.AllowUndefinedVariables()`
  means a colliding author key silently wins).

The only remaining handle is `reflect.ValueOf(env).Pointer()` — see **E2**, which is
unsound.

⇒ **D2 as written is unimplementable.** An implementer following the plan literally
must either (a) count per evaluation, which the ADR itself calls *"a regression
wearing a mitigation's clothes"* and grounds for escalation (plan phase-1 test 4:
*"If it does, stop and escalate — the decision is wrong, not the code"*), or (b)
change the frozen signatures, which the plan forbids. Phase 1 is the controller's own
inline phase and phases 3, 4, 6 all block on it.

**Proposed fix** — pick one and write it into D2:

- **(preferred, and E3 shows it is sufficient)** Delete the once-per-env mandate.
  Count per evaluation **with an early exit at the budget**, so the walk is
  `O(min(elements, n))` and never worse than the bound. E3 measures this and shows the
  ADR's own objection does not survive.
- Or move the bound out of `internal/expreval` to the caller that owns the map's
  lifetime (`engine`/`runtime`), and drop `WithMaxEnvElements` — but then D2's stated
  symbol does not exist and phase 1 has no content.
- Or introduce a named env type (`expreval.Env`) carrying the count, and accept that
  this **is** a `ConditionEvaluator` signature change — which is a bigger breaking
  change than the `ctx` D2 rejected, and must be argued as such.

---

## E2, **CRITICAL** — the only available identity handle for a memoized count is UNSOUND: Go recycles map addresses, and a stale small count admits an oversize env

**Claim attacked** — the same D2 sentence: *"It is computed when the variable map
changes and carried alongside it"*. Since the count cannot be a parameter (E1), any
implementation of "once per env" inside `expreval` must key on map identity via
`reflect.ValueOf(env).Pointer()`.

**Probe** — `zzprobe/reach_test.go::TestMapPointerIdentityIsRecycled` and
`::TestRecycledPointerCarriesStaleCount`. The second allocates 50 000 one-key maps
holding a 1-element slice (memoizing count = 2 under each pointer), forces GC, then
allocates one-key maps holding a **50 000**-element slice — same map-header size
class, wildly different count.

**Observed** (verbatim):

```
=== RUN   TestMapPointerIdentityIsRecycled
    reach_test.go:59: distinct maps=200000  distinct pointers=82473  ADDRESS COLLISIONS=117527
--- PASS: TestMapPointerIdentityIsRecycled (0.05s)

=== RUN   TestRecycledPointerCarriesStaleCount
    reach_test.go:78: UNSOUND: ptr 0x4880e51ba450 memoized count=2; it now backs a map of bounded count=50001 (iteration 0)
    reach_test.go:80:   => a bound of 10000 would ADMIT this env on the memoized number
--- PASS: TestRecycledPointerCarriesStaleCount (0.01s)
```

**Verdict: CONFIRMED, on the first iteration.** 200 000 distinct maps produced only
82 473 distinct addresses — **59 % of them collided**. A memo table keyed on the map
address returns a count belonging to a *freed, different* map. The failure direction
is fail-**open**: a memoized `count=2` admits an env whose real bounded count is
50 001, i.e. **exactly the attack D2 exists to refuse**, and — per H1 — an env of that
size costs seconds of unpreemptible CPU.

This is not theoretical tuning: it is the standard Go hazard that
`runtime.SetFinalizer`/`weak.Pointer` exist to manage, and neither is compatible with
"compare a number" on a 100 ns hot path.

**Proposed fix:** state in D2 that **map-identity memoization is forbidden**, with
this measurement, and adopt E1's preferred fix (per-evaluation count with early exit).
If some form of caching is still wanted, it must key on a value the caller owns and
mutates deliberately (e.g. a counter maintained by `engine` when `State.Variables` is
written) — which is E1's option 2 and lives outside `expreval`.

---

## E3, **MAJOR** — D2's "20–60× worse than the cost the decision refused" is a WORST-CASE number stated as the general one; on a typical env a per-evaluation count is ~12× CHEAPER than the ctx it refused

**Claim attacked** — ADR-0186 D2, verbatim:

> Measured on this machine: **~84 ns/op** on a typical few-variable env and **~19 µs at
> the 10 000 default** … **Counting per *evaluation* would therefore be 20–60× worse
> than the cost the decision refused, which would make Decision 2 self-defeating.**

and spec §5's interaction row: *"The bound must be cheaper than the cost it replaces,
or D2 is self-defeating (866 ns saved, ~19 µs spent at the default)."*

**Probe** — `zzprobe/bound_test.go` (Go benchmarks) and
`zzprobe/reach_test.go::TestCountCostCurve` (the cost curve the ADR never drew).

```
go test -bench=. -run '^$' -benchtime=2000x -count=3 ./zzprobe/
go test -run 'TestCountCostCurve' -v -count=1 ./zzprobe/
```

**Observed** (verbatim, best of 3):

```
BenchmarkEvalBoolSync-14                        2000    101.9 ns/op    68 B/op   3 allocs/op
BenchmarkEvalBoolGoroutinePath-14               2000    932.6 ns/op   493 B/op   9 allocs/op
BenchmarkCountTypicalEnv-14                     2000     65.77 ns/op    0 B/op   0 allocs/op
BenchmarkCountEnvAt10000-14                     2000   16490   ns/op    0 B/op   0 allocs/op
BenchmarkEvalBoolSyncPlusPerEvalCount-14        2000    175.9 ns/op    68 B/op   3 allocs/op
BenchmarkEvalBoolSyncPlusPerEvalCountAt10000-14 2000  16918   ns/op    69 B/op   3 allocs/op
```

```
=== RUN   TestCountCostCurve
    env elements=3      count cost=60ns          vs 866ns ctx: cheaper than the refused ctx
    env elements=10     count cost=66ns          vs 866ns ctx: cheaper than the refused ctx
    env elements=50     count cost=137ns         vs 866ns ctx: cheaper than the refused ctx
    env elements=100    count cost=219ns         vs 866ns ctx: cheaper than the refused ctx
    env elements=250    count cost=452ns         vs 866ns ctx: cheaper than the refused ctx
    env elements=500    count cost=859ns         vs 866ns ctx: cheaper than the refused ctx
    env elements=1000   count cost=1.68µs        vs 866ns ctx: MORE expensive than the refused ctx
    env elements=10000  count cost=16.502µs      vs 866ns ctx: MORE expensive than the refused ctx
```

**Verdict: PARTLY REFUTED — the numbers reproduce, the conclusion drawn from them does
not.**

*What HELD:* the ctx benchmark reproduces (bundle 99.43 → 965.2 ns/op, 3 → 9 allocs;
here **101.9 → 932.6 ns/op, 3 → 9 allocs**). The count costs reproduce (bundle ~84 ns
typical / ~19 µs at 10 000; here **65.8 ns / 16.5 µs**).

*What is REFUTED:* the sentence *"Counting per evaluation would therefore be 20–60×
worse than the cost the decision refused"* is unqualified and is true only at the
bound. The **crossover is ~500 elements**. Below it — which is every ordinary process
instance, and the "typical few-variable env" the ADR measured 84 ns on in the same
sentence — a per-evaluation count is **cheaper than the 866 ns ctx by ~12×**. The
whole-operation benchmark makes it concrete: 101.9 → 175.9 ns/op, **+74 ns, zero extra
allocations**, versus the +830 ns / +6 allocs the ctx would have cost.

*And the worst case is not a cost, it is a saving.* The 16.5 µs is paid on an env of
10 000 elements — an env the bound then **rejects**, replacing an evaluation that H1
measured at **2.458 s**. That is a **~149 000× saving**, not a 20× regression. The
ADR compares the cost of *rejecting* a hostile env against the cost of *evaluating* a
benign one, and the two are not the same transaction.

⇒ The once-per-env mandate — which E1 shows is unimplementable and E2 shows is unsound
if forced — is also **unnecessary**. Removing it dissolves E1 and E2 at no cost.

**Proposed fix:** replace the "ONCE PER ENV" block in D2 with:

> The count walks the env per evaluation, **with an early exit at the budget**, so it is
> `O(min(elements, n))` and can never exceed the bound's own cost. Measured: +74 ns/op
> and **0 extra allocations** on a typical gateway condition (101.9 → 175.9 ns/op),
> against the +830 ns/op and +6 allocs the rejected `ctx` would have cost. The walk
> becomes more expensive than the refused ctx only above **~500 elements**; at the
> 10 000 default it costs 16.5 µs and refuses an evaluation measured at 2.458 s.

and rewrite plan phase-1 test 4 accordingly — as written
(*"The benchmark must show the bound adds no per-evaluation walk"*) it **mandates the
unimplementable design and will fail every correct implementation**. It must instead
assert the early exit: *a benchmark over an env 100× the bound must cost no more than
one over an env at the bound.*


---

## HELD — H2. Phase 2's core premise: `Gate` DOES flatten the typed error, and `errors.As` fails at the transport

**Claim attacked** — plan phase 2 / ADR-0186 D5 / spec §2:
*"`runtime/validation/gate.go:45` is `fmt.Errorf("%w: %s", ErrInvalidInput, err.Error())` —
`%s`, not `%w`. The structured error is flattened to a **string** two layers before the
transport, so `errors.As(err, **jsonschema.ValidationError)` is **false** at
`ClassifyError`."*

**Probe** — `zzprobe/gate_test.go::TestAllFourStrategiesThroughTheGate`, calling the
**real** `validation.NewGate()` (not the vendor).

**Observed** (verbatim):

```
################ jsonschema/pattern ################
RAW strategy error   : workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'
    - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'
RAW unwrapped type   : *jsonschema.ValidationError
RAW errors.As(*jsonschema.ValidationError) = true
AFTER Gate.Validate  : workflow-validation: invalid input: workflow-validation/jsonschema: ...
AFTER errors.As(*jsonschema.ValidationError) = false
AFTER errors.Is(ErrInvalidInput)             = true
```

**Verdict: HELD, exactly as stated.** `true` before the gate, `false` after. The
submitted value `123-45-6789` is confirmed present in the 400 body. Phase 2's reason
for existing is sound and the `maxLength` sibling leak reproduces
(`at '/name': maxLength: got 9, want 3`).

---

## E4, **CRITICAL** — the "value-free" allow-listed rendering is NOT value-free: `InstanceLocation` carries caller-supplied data verbatim

**Claim attacked** — ADR-0186 D5's allow-list table:

> | `jsonschema` strategy | structured leaves: `at '/ssn': violates pattern` — built for
> **every** keyword, not just `pattern` |

and plan phase 2: *"structured leaves for `jsonschema` (`InstanceLocation` +
`ErrorKind.KeywordPath()` ⇒ `at '/ssn': violates pattern`)"*, and phase-2 test 1:
*"the message contains `"ssn"` and `"pattern"` and **not** `"123-45-6789"`"*.

The claim rests on `InstanceLocation` being schema-derived. It is **instance**-derived:
`jsonschema/v6@v6.0.2 validator.go:962-963` — *"location of the JSON value **within the
instance** being validated"*.

**Probe** — `zzprobe/gate_test.go::TestPrescribedRenderingLeaksViaInstanceLocation`.
A schema that admits caller-chosen keys (`additionalProperties` + `propertyNames`), with
the caller submitting a card number **as a key**. `leaves()` renders exactly the
prescribed `at '/<InstanceLocation>': violates <KeywordPath>` form.

**Observed** (verbatim):

```
=== RUN   TestPrescribedRenderingLeaksViaInstanceLocation
    RAW: workflow-validation/jsonschema: jsonschema validation failed with 'mem://schema.json#'
        - at '/4111-1111-1111-1111': got string, want number
        - at '': invalid propertyName '4111-1111-1111-1111'
          - at '': maxLength: got 19, want 8
    PRESCRIBED RENDERING: at '/': violates
      PRESCRIBED RENDERING: at '/4111-1111-1111-1111': violates type
      PRESCRIBED RENDERING: at '/': violates propertyNames
        PRESCRIBED RENDERING: at '/': violates maxLength
--- PASS
```

**Verdict: CONFIRMED.** The prescribed, allow-listed, "value-free" rendering emits
`at '/4111-1111-1111-1111': violates type` — the submitted value, verbatim, in the arm
D5 declares fixed. This is the **same failure shape the ADR itself diagnoses** for the
draft (*"the two 4xx arms proven to leak stop leaking"* being true of one strategy) and
for `expr` (*"the same disclosure … inside the arm the draft declared fixed"*), now
recurring one layer down inside the replacement fix.

It is not exotic: any schema without `"additionalProperties": false`, any map-valued
field, any `patternProperties`, and every array index puts instance-derived text into
the pointer. The `propertyNames` keyword makes the key itself the validated value.

⚠ **Phase-2 test 1 as prescribed cannot catch this.** Its fixture is
`{"ssn":"123-45-6789"}` against a closed `properties` schema, where `InstanceLocation`
is always a schema-declared name. The test is green against the leaking implementation.

**Proposed fix** (concrete, pick one):

1. **Render the KEYWORD PATH and the SCHEMA location, not the instance location.**
   `ValidationError.SchemaURL` + `KeywordLocation` (`/properties/ssn/pattern`, observed
   above) are entirely schema-derived. Message becomes
   `invalid input at schema location '/properties/ssn/pattern'`. Value-free by
   construction, and still actionable.
2. Or keep `InstanceLocation` but **allow-list each segment against the schema's
   declared property names**, emitting a placeholder (`/<key>`, `/[i]`) for any segment
   not declared. This preserves the ergonomics D5 wants while making the value-free
   property checkable.

and **add the falsifying fixture to phase-2 test 1**: an `additionalProperties` /
`propertyNames` schema with the secret submitted as a key, asserting the rendered
message does **not** contain it. State its falsifier: *it fails against an
implementation that renders `InstanceLocation`.*

---

## E5, **MAJOR** — the prescribed rendering emits an empty keyword for the ROOT error; "structured leaves" requires a recursion the plan never states

**Claim attacked** — plan phase 2: *"structured leaves for `jsonschema`
(`InstanceLocation` + `ErrorKind.KeywordPath()` ⇒ `at '/ssn': violates pattern`)"*.

**Probe** — same test; `leaves()` applies the prescription to the error `errors.As`
returns (the **root**), then recurses.

**Observed** (verbatim):

```
PRESCRIBED RENDERING: at '/': violates
  PRESCRIBED RENDERING: at '/ssn': violates pattern
```

**Verdict: CONFIRMED.** The object `errors.As` yields is the **root**
`*jsonschema.ValidationError`, whose `ErrorKind.KeywordPath()` is empty — the literal
prescription produces `at '/': violates ` with a trailing blank. The usable leaves live
in `.Causes`, recursively (depth 2 in the `propertyNames` case above). An implementer
following the plan word-for-word ships a useless message and every prescribed
assertion (`contains "ssn"`, `contains "pattern"`, `not contains "123-45-6789"`) is
**satisfied by that useless message** — it contains neither the value nor anything
else.

**Proposed fix:** the plan must name the traversal. The vendor already provides it:
`ValidationError.BasicOutput().Errors` is a flat leaf list with `InstanceLocation` and
`KeywordLocation` as ready strings (observed:
`instanceLocation="/ssn" keywordLocation="/properties/ssn/pattern"`). Prescribe
`BasicOutput()` explicitly, and add a positive assertion that the rendering is
non-empty and names a keyword.

---

## E6, **MAJOR** — `avro` echoes the submitted value verbatim; the bundle never says so, and phase 2 has no `avro` test

**Claim attacked** — spec §2 / plan §4: *"only `jsonschema` yields structured leaves"*
and ADR-0186 D5's row *"`avro`, `callback`, and the other seven sentinels | static
`"invalid input"`"*. The bundle establishes the leak for `jsonschema` and `expr` only.

**Probe** — `zzprobe/avro_test.go::TestAvroErrorShapes`, five avro schema shapes
through the real strategy.

**Observed** (verbatim):

```
enum     type=*fmt.wrapError  err=workflow-validation/avro: does not conform to avro schema: cannot encode binary record "R" field "status": value does not match its schema: cannot encode binary enum "S": value ought to be member of symbols: [ok bad]; "4111-1111-1111-1111"
fixed    type=*fmt.wrapError  err=workflow-validation/avro: ... cannot encode binary fixed "F": datum size ought to equal schema size: 11 != 4
union    type=*fmt.wrapError  err=workflow-validation/avro: ... received: string
missing  type=*fmt.wrapError  err=workflow-validation/avro: ... schema does not specify default value and no value provided
extra    type=<nil>           err=<nil>
```

**Verdict: PARTLY REFUTED.**

- *HELD:* avro carries **no structure** — `*fmt.wrapError` over a plain
  `*errors.errorString` (goavro exports no error type; `grep '^type.*Error' goavro/*.go`
  → 0 hits). The claim *"only `jsonschema` yields structured leaves"* stands.
- *NEW:* avro **does leak the submitted value verbatim** on the `enum` path
  (`"4111-1111-1111-1111"`) and leaks a **length** on the `fixed` path (`11 != 4`) —
  the identical disclosure class the ADR flags for `jsonschema`'s `maxLength`. The
  bundle's evidence for routing avro to static text is *absence of structure*; the real
  and stronger reason is that it **leaks**, and that is nowhere recorded.
- *Also new:* avro **silently accepts undeclared extra fields** (`extra` → `nil`). A
  consumer choosing avro for input validation gets no protection against additional
  properties. Out of scope for this ADR but worth a backlog line.

**Proposed fix:** state the avro leak in ADR-0186 Context §5 with this transcript, and
add an `avro`/`enum` row to plan phase-2 test 3 (currently `callback`-only) whose
falsifier is: *it fails against an implementation that passes non-`jsonschema` strategy
messages through*.

---

## E7, **MINOR** — `ErrorKind.LocalizedString(nil)` PANICS, and rendering messages promotes an indirect dependency to direct

**Claim attacked** — implicit in plan phase 2's rendering prescription, which names
`KeywordPath()` but not how any human-readable text is produced.

**Probe** — `zzprobe/gate_test.go::TestLocalizedStringNilPrinterPanics`.

**Observed** (verbatim):

```
=== RUN   TestLocalizedStringNilPrinterPanics
    PANIC from ErrorKind.LocalizedString(nil): runtime error: invalid memory address or nil pointer dereference
```

Stack (from the first run, before the recover was added):
`golang.org/x/text/message.newPrinter(0x0) … kind.(*Pattern).LocalizedString`.

**Verdict: CONFIRMED.** The obvious call for an implementer rendering a leaf message
panics on a nil printer, **inside the 400 error path** — turning a client's malformed
request into a server panic. And using a real printer requires
`golang.org/x/text/message`, currently `go.mod:152 golang.org/x/text v0.38.0 //
indirect` — a **new direct dependency** the bundle never lists.

**Proposed fix:** prescribe `BasicOutput()` (E5) or `KeywordPath()` only, and add an
explicit line to phase 2: *do not call `LocalizedString`; it requires an
`x/text` printer and panics on nil.* If a printer is genuinely wanted, promote
`golang.org/x/text` to a direct requirement and say so in the ADR's cost list.


---

## HELD — H3. The 413/400 ordering defect is real, and the 400 arm carries exactly EIGHT sentinels

**Claim attacked** — ADR-0186 D1: *"An error wrapping **both** `ErrBadInput` and
`ErrBodyTooLarge` matches the 400 arm first, so an implementer who follows the
conversion instruction literally … ships **400**"*; and plan §4:
*"sentinels in the 400 arm | **8**, across 5 `errors.Is` groups"*.

**Probe** — `zzprobe/classify_test.go`, calling the **real** `httpcore.ClassifyError`.

**Observed** (verbatim):

```
=== RUN   TestClassifyOrderingWithBothSentinels
    errors.Is(both, ErrBadInput)      = true
    errors.Is(both, errBodyTooLarge)  = true
    ClassifyError(both) -> 400 {Error:bad_request Message:workflow-httpcore: bad input: workflow-httpcore: request body too large}
    ClassifyError(bare oversize sentinel, TODAY) -> 500 {Error:internal_error Message:}

=== RUN   TestMaxBytesErrorShape
    type=*http.MaxBytesError errors.As=true Error()="http: request body too large"
    wrapped errors.As(*http.MaxBytesError)=true  ClassifyError->400

=== RUN   TestFourHundredArmSentinelCount
    kernel.ErrBadCursor                -> 400
    kernel.ErrBadArmedTimerCursor      -> 400
    httpcore.ErrBadInput               -> 400
    validation.ErrInvalidInput         -> 400
    engine.ErrInvalidOutcome           -> 400
    engine.ErrOutcomeRequired          -> 400
    engine.ErrEmptyTriggerKey          -> 400
    engine.ErrEmptyReassignTarget      -> 400
    SENTINELS CLASSIFYING AS 400 = 8
```

**Verdict: HELD.** The double-wrapped oversize error classifies **400**, confirming the
defect D1 exists to avoid. Both the bare-sentinel mandate and the arm ordering are
justified, and phase-5's stated falsifier (*"it also fails against an implementation
that keeps the `ErrBadInput` wrapper"*) is real.

⚠ **One internal contradiction, executed:** the arm carries **8** sentinels, matching
ADR-0186 D5's body (*"eight sentinels across five `errors.Is` groups"*) and plan §4.
**ADR-0186's own banner at line 17 says "nine sentinels"** — inherited verbatim from
`reaudit-b3-adjudication.md` finding 10 (*"It carries **9** sentinels"*), which is wrong.
Fix the banner to eight. (This is the counting lens's territory but it was established
here by execution.)

⚠ **Sequencing hazard (MINOR):** today the bare sentinel classifies **500 with an empty
body**. Phase 5 depends on phase 4 in the plan's table, so the order is right — but if
any adapter ships its bare sentinel before `ClassifyError` gains the 413 arm, oversize
becomes a silent 500. Worth one sentence in phase 5's brief.

---

## HELD — H4. stdlib and gin both preserve `*http.MaxBytesError` through their decoders

**Claim attacked** — ADR-0186 D1: *"Go returns `*http.MaxBytesError` (needing
`errors.As`), gin wraps it again, and fiber's pre-check produces a home-grown error.
Three adapters, three error shapes."*

**Probe** — `zzprobe/transport_test.go`, real `httptest` servers, real
`json.NewDecoder(...).Decode` and real `gin.Context.ShouldBindJSON`, 2 MiB body against
a 1 MiB `MaxBytesReader`.

**Observed** (verbatim):

```
STDLIB oversize: err=http: request body too large|type=*http.MaxBytesError|errorsAs=true
GIN    oversize: err=http: request body too large|type=*http.MaxBytesError|errorsAs=true
```

**Verdict: PARTLY REFUTED.** `errors.As` works for both — the prescribed fix is
correct. But *"gin wraps it again"* is **FALSE**: gin returns the bare
`*http.MaxBytesError`, concrete type identical to stdlib's. Two adapters, **one**
error shape. Minor, but it is an unexecuted claim about current behaviour in an ADR,
which Premise Discipline forbids; correct it to *"stdlib and gin both surface the bare
`*http.MaxBytesError`; fiber's pre-check produces a home-grown error. Two shapes, not
three."*

---

## E8, **CRITICAL** — fiber's `c.Body()` DECOMPRESSES, so the prescribed pre-check returns **400, not 413**, on exactly the amplification case, and materialises the expansion before it can reject it

**Claim attacked** — ADR-0186 D1:

> a `len(c.Body())` pre-check for fiber, because `BodyLimit` is a `fiber.Config` field
> set on `fiber.New` — the **app**, which a mounted route group does not own.
> ⚠ Conceded plainly: fiber's pre-check is a **rejection, not a prevention** — the
> body is already buffered by the time it runs, and `fiber.DefaultBodyLimit` is what
> actually prevents the amplification there.

and plan phase 5: *"`fiber` — a `len(c.Body())` pre-check before `c.Bind().JSON`"*,
`TestOversizedBodyReturns413`.

Vendor source, `fiber/v3@v3.4.0/req.go:146-147` (godoc on `Body()`):

> *"This method will **decompress the body** if the 'Content-Encoding' header is
> provided."*

and `req.go:185-197` — on a decode error `Body()` **writes a response itself**
(`SendStatus(413/415/501)`) and **returns `[]byte(err.Error())`**, i.e. the error
message *as the body*.

**Probe** — `zzprobe/fiberbody_test.go::TestPrescribedFiberPreCheckAgainstEncodedBodies`,
which implements the prescription verbatim (pre-check → `Bind().JSON` → error) on a
**mounted `app.Group("/api")`** and reports what the client receives.

**Observed** (verbatim):

```
=== RUN   TestPrescribedFiberPreCheckAgainstEncodedBodies
 gzip/64MiB-over-fiber-limit    wire=65263   CLIENT SEES status=400 body="400 from OUR decode (preLen=33 bodyPrefix=\"body size exceeds the given limit\" decodeErr=bind from body: invalid character 'b' looking for beginning of value)"
 gzip/2MiB-over-our-1MiB-cap    wire=2081    CLIENT SEES status=413 body="413 from OUR pre-check (len=2097151)"
 unsupported-encoding           wire=7       CLIENT SEES status=400 body="400 from OUR decode (preLen=15 bodyPrefix=\"Not Implemented\" decodeErr=bind from body: invalid character 'N' looking for beginning of value)"
 unknown-encoding               wire=7       CLIENT SEES status=400 body="400 from OUR decode (preLen=22 bodyPrefix=\"Unsupported Media Type\" decodeErr=bind from body: invalid character 'U' looking for beginning of value)"
```

and, from `zzprobe/zipbomb_test.go`:

```
gzip wire size = 65263 bytes (63.7 KiB); decompressed = 67108864 bytes (64 MiB); ratio = 1028:1
fiber.DefaultBodyLimit = 4194304 (4 MiB) — the WIRE body is true under it
FIBER  status=413  len(c.Body())=33 over1MiB=false allocDelta=16MiB
```

**Verdict: CONFIRMED — the prescribed mechanism fails on the case it is for.**

1. **Row 1: a 63.7 KiB request that decompresses to 64 MiB gets a 400, not a 413.**
   `c.Body()` fails its bounded gunzip, sets 413 internally, and returns the 33-byte
   string `"body size exceeds the given limit"`. `len(...) = 33` is **under** the cap,
   so the pre-check passes it through, `Bind().JSON` chokes on `'b'`, and the library
   writes a **400 over fiber's 413**. `TestOversizedBodyReturns413` as prescribed uses
   an uncompressed body and is **green against this**.
2. **Row 2: 2 081 wire bytes cause a 2 097 151-byte allocation** before the pre-check
   can see it — a **~1000:1 amplification** that `MaxBodyBytes` does not bound. The
   ADR's concession covers only *"the body is already buffered"* (i.e. the wire body);
   it never says the **decompressed** body is materialised, bounded by
   `app.config.BodyLimit` (`req.go:105`, default 4 MiB) — the *app's* limit, which the
   ADR itself says a mounted group does not own. So the effective ceiling on a fiber
   request is **4 MiB decompressed regardless of `MaxBodyBytes = 1 MiB`**, and the
   16 MiB `allocDelta` measured above shows the peak is higher still.
3. **Rows 3–4 are PRE-EXISTING, not introduced** — `Bind().JSON` already calls
   `ctx.Body()` (`fiber/v3 bind.go:308`), so a `Content-Encoding` fiber cannot handle
   already yields a garbage 400 over fiber's 415/501 today. Recorded as a backlog line,
   **not** charged to this bundle. But the pre-check adds a *second* `Body()` call site
   with the same trap.

**Proposed fix** (concrete):

- Pre-check **`len(c.BodyRaw())`**, not `len(c.Body())`. `BodyRaw()` is `getBody()`
  with no decompression (`req.go:92-96`) and no response side effect — it measures the
  wire bytes, which is what `MaxBodyBytes` means in the other two adapters and is what
  makes the three agree.
- Then **additionally** check `len(c.Body())` after decompression against the same cap,
  so a compressed payload cannot exceed the declared limit — and guard it:
  if `c.Response().StatusCode()` was already set by `Body()`, return the bare
  `ErrBodyTooLarge` rather than falling through to the decode.
- Add the **falsifying fixture** to phase-5's fiber test: a gzip body whose wire size is
  under the cap and whose decompressed size is over it, asserting **413**. State its
  falsifier: *it fails against a `len(c.Body())` pre-check, which returns 33.*
- ADR-0186 D1 must record that fiber's decompression ceiling is `app.config.BodyLimit`,
  not `MaxBodyBytes`, and that this is a **third** thing the mounted group does not own.

---

## E9, **MAJOR** — the three adapters DISAGREE on a compressed body, so phase 8's parity assertion cannot hold as written

**Claim attacked** — plan phase 8: *"Add parity cases asserting all three adapters agree
on **413** for an oversize body"*, and D1's *"`httpcore.CustomizeConfig.MaxBodyBytes`,
default 1 MiB, honoured by every adapter through its own idiom"*.

**Probe** — `zzprobe/zipbomb_test.go::TestStdlibAndGinSeeTheWireSizeOnly`, the **same**
gzip request sent to stdlib and gin with `MaxBytesReader`.

**Observed** (verbatim):

```
STDLIB  wire=65263 bytes (under the 1 MiB cap: true) -> decodeErr=invalid character '\x1f' looking for beginning of value|isMaxBytes=false
GIN     wire=65263 bytes (under the 1 MiB cap: true) -> decodeErr=invalid character '\x1f' looking for beginning of value
```

**Verdict: CONFIRMED.** `net/http` does **not** auto-decompress request bodies, so
stdlib and gin see 63.7 KiB of gzip and return **400** (`'\x1f'` is the gzip magic
byte). Fiber decompresses and — for the 2 MiB case in E8 row 2 — returns **413**. Same
request, same config, **two different statuses**. `MaxBodyBytes` therefore does not mean
the same thing in the three adapters: wire bytes in two, decompressed bytes in one.

**Proposed fix:** decide and write down which one `MaxBodyBytes` means (E8 recommends
wire bytes, with decompressed size as a second, separately-named bound), and give phase
8 a compressed-body parity case. If the answer is "wire bytes", phase 8's case is
satisfiable; as the plan stands the parity suite will either fail or silently be written
to only use uncompressed bodies — which is the *"a test with exactly the fix's own
coverage"* failure ADR-0186 D5 warns about, in a different phase.

---

## HELD — H5. The fiber `ASSUMPTION (unverified)` is DISCHARGEABLE in its narrow form: `BodyLimit` really is unreachable from a mounted group, and `len(c.Body())` really is reachable

**Claim attacked** — spec §7: *"`ASSUMPTION (unverified)`: the **fiber body-cap
mechanism** … reasoned from source (`BodyLimit` is a `fiber.Config` field set on
`fiber.New`, `fiber/v3@v3.4.0/app.go:710`, which a mounted route group does not own)."*

**Probe** — `zzprobe/transport_test.go::TestFiberBodyPreCheck` /
`TestFiberBodyLimitReachability`, against `app.Group("/api")` — the exact type
`transport/http/fiber/mount.go:15 Mount(r fiberlib.Router, …)` accepts.

**Observed** (verbatim):

```
under-1MiB               sent=524296  status=200 body=len(c.Body())=524296 cap=1048576 over=false
over-1MiB-under-4MiB     sent=2097160 status=200 body=len(c.Body())=2097160 cap=1048576 over=true
over-fiber-default-4MiB  app.Test error: body size exceeds the given limit
fiber.Router concrete type: *fiber.Group
```

**Verdict: HELD for uncompressed bodies.** `len(c.Body())` is reachable from a mounted
group and correctly discriminates at 1 MiB; `fiber.DefaultBodyLimit = 4 MiB` rejects
before the handler; and fiber v3 ships **no `BodyLimit` middleware** (`ls
fiber/v3@v3.4.0/middleware/` — 31 middlewares, none of them body-limit), so the ADR's
reasoning that a mounted group cannot set it is correct. **Discharge the assumption for
the uncompressed case only** — E8 shows the compressed case defeats it.

