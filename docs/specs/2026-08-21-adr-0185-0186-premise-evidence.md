# Premise evidence — ADR-0185 / ADR-0186 revision (2026-08-21)

Executed evidence for the claims the **revised** B3 bundle asserts. Every row below
was run on this machine against the repo at the revision commit; nothing here is
inherited from the 2026-08-20 draft or from its audit reports.

- Probe module: throwaway `module probe` with
  `replace github.com/kartaladev/wrkflw => /Users/zakyalvan/Documents/RND/wrkflw`,
  run with `go run .` **outside** the repo. No repo `.go` file was created or modified.
- Vendor pins that the results depend on: `github.com/expr-lang/expr v1.17.8`,
  `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2` (`go.mod`).
- Companion: `docs/plans/sweep-evidence/audit-b3-adjudication.md` (the audit this
  revision answers) and the three lens reports beside it.

> ⚠ **Why this file exists.** The 2026-08-20 draft's audit found that a decision had
> been written around `has(vars, "k")`, a function that does not exist. The audit
> proposed four "working replacements"; executing them shows **two of the four** are
> wrong as a *guard set* — `vars.k ?? d` does not parse unparenthesised (§2) and
> `get(vars,"k")` extracts zero references, making it a bypass (§3). `"k" in vars`
> and `vars?.k` hold. Separately, the audit's fix never states that a guard must
> **dominate** its use, and the naive reading reopens the hole (§4).
> Restating an inherited fix without executing it is the same failure one level down.

---

## 1. Guard forms that exist in expr v1.17.8

Compiled exactly as `internal/expreval` does — `expr.Compile(code, expr.AllowUndefinedVariables())`
then `expr.Run` — with `vars = {"tier": "gold"}`.

```
has(vars, "tier")              out=<nil>   err=invalid operation: cannot call nil (1:1)
 | has(vars, "tier")
 | ^
"tier" in vars                 out=true    err=<nil>
vars?.tier == "gold"           out=true    err=<nil>
get(vars, "tier")              out=gold    err=<nil>
vars.tier ?? "none"            out=gold    err=<nil>
"absent" in vars               out=false   err=<nil>
vars?.absent == "gold"         out=false   err=<nil>
get(vars, "absent")            out=<nil>   err=<nil>
vars.absent ?? "none"          out=none    err=<nil>
```

**`has` is not a builtin.** `AllowUndefinedVariables()` resolves the identifier to
nil, so compilation *succeeds* and the call fails at **run** time;
`RoleAuthorizer.Authorize` wraps a run error as `ErrNotAuthorized`, so a predicate
written to the 2026-08-20 draft's prescription **denies every actor, permanently**.
Confirms the audit's Critical A.

## 2. The `??` form does not parse in the predicate shape a policy author would write

```
vars.tier ?? "none" == "gold"        COMPILE FAILS
  Operator (==) and coalesce expressions (??) cannot be mixed. Wrap either by parentheses. (1:21)
vars.tier ?? "none" != "blocked"     COMPILE FAILS
(vars.tier ?? "none") == "gold"      out=false  err=<nil>
(vars.blocked ?? false) == true      out=false  err=<nil>
(vars.status ?? "") != "blocked"     out=true   err=<nil>
```

⚠ **New, and not in the audit.** The audit's proposed replacement list names
`vars.k ?? default` as a working form. It works as an *expression* but **only
parenthesised**; the natural predicate shape is a compile error. Any documentation
that offers `??` must show the parentheses.

## 3. Static reference extraction — the closed set it actually covers

`parser.Parse` + `ast.Walk` collecting `MemberNode`s whose base is the `vars` /
`actor` identifier. (`ast.Walk` takes an `ast.Visitor` interface, not a bare func.)

```
vars.status != "blocked"                    vars.status   {member}
vars.tier == nil or vars.tier == "gold"     vars.tier ×2  {member}
"tier" in vars and vars.tier == "gold"      vars.tier     {member, in-guarded}      guard=TRUE
vars?.tier == "gold"                        vars.tier     {member, optional ?.}     guard=TRUE
get(vars, "tier") == "gold"                 refs=[]                                 ZERO REFERENCES
vars["dept"] == "fin"                       vars.dept     {member}
vars.order.total > 100                      vars.order    {member}                  depth-1 only
actor.attributes.clearance > 3              actor.attributes {member}               depth-1 only
vars[actor.ID] == "x"                       actor.ID {member}, vars.<dynamic>        UNRESOLVABLE
len(vars) == 0 or vars.status != "blocked"  vars.status   {member}
(vars | first()) != "blocked"               refs=[]                                 ZERO REFERENCES
not vars.blocked                            vars.blocked  {member}
vars.a == vars.b                            vars.a, vars.b {member}
```

Three residual shapes the extractor cannot resolve, each of which must have a
stated verdict or the check is a no-op for it:

1. **Nested chains** — `vars.order.total` yields `vars.order` only. Depth-1 is not
   an accident to apologise for: `humantask.HumanTask.Vars`' own godoc already says
   the snapshot is a shallow `maps.Clone` and that *"eligibility predicates should
   rely on top-level scalar variables only"*. Depth-1 is exactly the documented
   supported surface.
2. **Dynamic keys** — `vars[actor.ID]` yields an unresolvable reference.
3. **Zero-reference predicates** — `get(vars, "k")` and pipe/builtin forms
   (`vars | first()`) extract **nothing**.

⚠ **`get()` is a bypass, not a guard.** The audit proposed `get(vars,"k")` as a
working replacement for `has`. It evaluates correctly, but it makes the predicate
invisible to the extractor — so a policy written with `get()` would skip the strict
check entirely. It must be handled by the zero-reference rule, not offered as an
escape hatch.

## 4. A tree-wide `in` guard is UNSOUND — the guard must dominate the use

`vars` is **empty** in every row. "extractor-says-guarded" is a naive implementation
that marks a key optional whenever `"k" in vars` appears anywhere in the tree.

```
"tier" in vars and vars.tier == "gold"      evaluates=false  extractor-says-guarded=true
"tier" in vars or  vars.tier != "blocked"   evaluates=TRUE   extractor-says-guarded=true   <-- UNSOUND
not ("tier" in vars) or vars.tier == "x"    evaluates=TRUE   extractor-says-guarded=true   <-- UNSOUND
```

⚠ **New, and in neither the bundle nor the audit.** Rows 2 and 3 **allow on an
absent key** — precisely the class the strict rule exists to close — while a
tree-wide guard collector calls them guarded. Guard recognition is only sound when
the existence test **dominates** the use (the left operand of `and`, or the
condition of a ternary whose consequent holds the use). A naive implementation
reopens the hole it was written to close, and the three rows above are the
falsifying table for it.

## 5. Tri-state `Open` across the upgrade, through the real snapshot codec

The instance snapshot is `json.Marshal`ed (`internal/persistence/store/store_core.go:81`,
also `:231`) and read back with a plain `json.Unmarshal` (`:174`) — **no**
`DisallowUnknownFields`. `authz.AuthzSpec` has no json tags, so Go's default
field-name marshalling applies.

```
old row  {"Roles":null,"Privileges":null,"Attribute":""}              -> new binary: Open=<nil>  (nil? true)
new row  {"Roles":null,"Privileges":null,"Attribute":"","Open":null}  -> round-trip Open=nil
new row  {"Roles":null,"Privileges":null,"Attribute":"","Open":false} -> round-trip Open=false
new row  {"Roles":null,"Privileges":null,"Attribute":"","Open":true}  -> round-trip Open=true
new row  {"Roles":null,"Privileges":null,"Attribute":"","Open":true}  -> OLD binary: err=<nil>, Open silently dropped
```

**`Open *bool` gives a genuine tri-state through the real codec**: a pre-upgrade row
decodes to `nil` ("written before `Open` existed"), distinguishable from an explicit
`false`. This is what makes grandfathering in-flight tasks possible without a
mixed-version deployment, and it is the executed basis for ADR-0185 Decision 3.

The last row confirms the *other* direction the 2026-08-20 plan gated: an older
binary reading a newer row drops `Open` with no error. That direction requires a
mixed-version deployment, which this repo already declares out of contract.

## 6. A value-free rendering of a jsonschema validation failure is available

Schema `{"ssn": {"type":"string","pattern":"^[0-9]{3}$","maxLength":3}}`,
input `{"ssn": "123-45-6789"}`.

```
err.Error() — what ships today, and what ClassifyError copies into the 400 body:
    jsonschema validation failed with 'mem://schema.json#'
    - at '/ssn': maxLength: got 11, want 3
    - at '/ssn': '123-45-6789' does not match pattern '^[0-9]{3}$'

structured leaves via *jsonschema.ValidationError:
    InstanceLocation=[ssn]  ErrorKind.KeywordPath()=[maxLength]  ->  "at '/ssn': violates maxLength"
    InstanceLocation=[ssn]  ErrorKind.KeywordPath()=[pattern]    ->  "at '/ssn': violates pattern"
```

The `pattern` leaf reproduces the **submitted value verbatim** in `Error()` —
confirming the audit's F4 and resolving spec §4.7's `ASSUMPTION (unverified)`
against the 2026-08-20 draft. `*jsonschema.ValidationError` exposes
`InstanceLocation []string` and `ErrorKind.KeywordPath() []string`, so a rendering
carrying **path + violated keyword and no value** is constructible from the public
API. ADR-0186 Decision 5's value-free 400 is therefore feasible, not aspirational.

⚠ Note the two leaves: `maxLength` reports `got 11, want 3` — a **length**, which
is a weak disclosure but not the value. Only the `pattern` leaf echoes the string.
A fix that special-cases `pattern` alone would still leak lengths; the rendering
must be built from the structured leaves for **every** keyword.

---

## 7. Re-derived enumerations (all at the revision commit)

| claim | command | result |
|---|---|---|
| body-actor test pins | `grep -rnE 'httpcore\.Actor\{\|"actor"\|"by"' transport/ --include='*_test.go'` | **29** — httpcore 11, gin 7, fiber 5, stdlib 5, parity 1 |
| …the 2026-08-20 net | `grep -rnE 'httpcore\.Actor\{\|"actor"' …` | 23 (misses `ReassignInput.By`, tagged `"by"` at `dto.go:66`) |
| `handleHumanCompleted` | `grep -n "^func handleHuman" engine/step_triggers.go` | **`:849`** (draft said `:839`); `Completion` write at **`:941`** (draft said `:931-936`) |
| …the claim comparison | `awk '/^func handleHumanCompleted/,/^}/' … \| grep -c "Candidates\|Eligibility\|Claim"` | **0** over the true body — conclusion stronger than the draft's "one hit, a comment" |
| allow-all log level | `grep -n "LevelDebug\|authzLabel" service/service.go` | level at **`:323`**; label computed at `:315-317`; one `LogAttrs` record carries 4 unrelated attrs |
| `expreval.New(` instances | `grep -rn "expreval\.New(" --include='*.go' . \| grep -v _test` | **4** — `authz/authz.go:23`, `internal/authz/casbin/authorizer.go:30`, `engine/conditions.go:43`, `runtime/processdriver_options.go:200` |
| "engine gate is open" godocs | `grep -rn "engine gate is open" --include='*.go' .` | **2** — `definition/activity/activity.go:159` (on `NewUserTask`), `options.go:221` |
| `NewUserTask(` sites | `grep -rn "NewUserTask(" --include='*.go' . \| wc -l` | **274** |
| examples calling `runtime.WithHumanTasks` | `grep -rln "runtime\.WithHumanTasks" examples/` | **16** — 12 under `scenarios/`, 4 `*_wiring` (`cache`, `mysql`, `production`, `sqlite`) |
| `stdlib.Mount` callers | `grep -rn "stdlib\.Mount(" --include='*.go' . \| grep -v _test` | **3** — `mysql_wiring:262`, `sqlite_wiring:278`, `production_wiring:264` |

Each of these confirms a correction the counting lens made to the 2026-08-20 draft.
They are re-derived here rather than inherited, because the draft's own failure mode
was restating a citation whose anchor had moved.

---

## 8. What is still NOT executed

Labelled so the re-audit can attack the boundary rather than re-derive it:

- `ASSUMPTION (unverified)`: the fiber `len(c.Body())` body-cap mechanism. Source
  reasoning only (`BodyLimit` is a `fiber.Config` field set on `fiber.New`,
  `fiber/v3@v3.4.0/app.go:710`, which a mounted route group does not own).
- `ASSUMPTION (unverified)`: the 1 MiB body default. It is a judgement call.
- `ASSUMPTION (unverified)`: that the request context reaches `httpcore` unmodified
  in all three adapters, and that fiber's `c.Locals` does **not** propagate. This was
  executed **by the audit's execution lens**, not re-executed here
  (`audit-b3-execution.md` F5: stdlib/gin/fiber all carried a middleware ctx value;
  `c.Locals` returned empty). It is load-bearing for dropping the actor half of the
  adapter phase — **the re-audit should re-run it.**
- The env-cardinality bound's chosen number is extrapolated from the 2026-08-20
  O(n²) ladder, not re-measured here. See ADR-0186 Decision 2.
