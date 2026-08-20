# Fix evidence — `definition/model` wave 1

## Item 115 — duplicate node IDs build clean

**Status: DONE.** Branch `main`, working tree (uncommitted; the controller commits).

### Files changed

- `definition/model/validate.go` — two new exported sentinels (`ErrDuplicateNodeID`,
  `ErrDuplicateFlowID`) with house-style doc comments, plus the two enforcement passes at
  the top of `validateStructure`.
- `definition/model/validate_test.go` — six new cases folded into the existing
  `TestValidate` table (assert-closure form, per the `table-test` skill).

### 1. Premise probe (executed, then deleted)

Throwaway `definition/model/zz_probe_115_test.go`, run with
`go test -count=1 -run '^TestProbe115$' -v ./definition/model/` (EXIT=0), observed output
**before** any change:

```
PROBE-A nodes=4 err=<nil>
PROBE-A Node(charge) ok=true action=...ServiceTask{Base:{id:"charge"...}, TaskAction:{Action:"a"}}
PROBE-B empty-flow-id  err=<nil>
PROBE-C dup-flow-id    err=<nil>
PROBE-D node/flow same id err=<nil>
PROBE-E empty node id  err=<nil>
```

Reading, in order:

- **A** confirms the triage: `nodes=4`, `Validate` returns `nil`, and `Node("charge")`
  resolves to the **first** declaration (`Action:"a"`) — the second is unreachable.
- **B** is the load-bearing one for the design: **flow IDs ARE optional today.** Two flows
  with blank IDs validate clean. ⇒ N blank flow IDs must **not** be reported as N-1
  duplicates.
- **C** duplicate flow IDs validated clean before the change.
- **D** a node and a flow sharing the string `"start"` validated clean ⇒ node IDs and flow
  IDs are **separate namespaces**; keep it that way.
- **E** a node with a **blank** ID validates clean today. Blank node IDs are therefore not
  independently rejected; the new rule only fires when **two** nodes are blank, which is the
  same shadowing hazard as any other duplicate.

### 2. Are flow IDs optional? — YES

Three independent confirmations:

- `definition/flow/flow.go:10` — `ID string \`json:"id" yaml:"id"\`` with no `omitempty` and
  no non-blank constraint anywhere; `flow.New` synthesises `"from->to"`, but a struct literal
  may omit it and dozens of in-repo fixtures do.
- Probe **B** above: a definition with two blank-ID flows validates clean.
- Nothing resolves a flow **by ID** on the execution path. The only ID-keyed reads are
  authoring-time diagnostics and `RecoveryFlow` (`validate.go:592`, `f.ID == rf`) /
  `DeadlineFlow`.

⇒ the flow pass skips `f.ID == ""`. Node IDs get **no** such exemption: the node ID *is* the
lookup key (`ProcessDefinition.Node`, first-wins linear scan, `definition.go:74-81`).

### 3. Scope decision — per definition, not global

`validateStructure` recurses into nested sub-process definitions (`validate.go:566`) with a
`seen` cycle-guard, and every lookup (`Node`/`Outgoing`/`Incoming`) is scoped to one
`*ProcessDefinition`. Uniqueness is therefore enforced **per definition**: a nested definition
may legitimately reuse an outer ID, and a duplicate **inside** a nested definition is still
rejected. Both are asserted (`"an ID reused across the outer and a nested definition is
accepted"`, `"duplicate node ID inside a sub-process is rejected (recursion)"`).

### 4. Observed RED

**RED 1 — compile failure** (`go test -count=1 ./definition/... `, `EXIT=1`):

```
# github.com/kartaladev/wrkflw/definition/model_test [.../definition/model.test]
definition/model/validate_test.go:892:35: undefined: model.ErrDuplicateNodeID
definition/model/validate_test.go:922:35: undefined: model.ErrDuplicateNodeID
definition/model/validate_test.go:970:35: undefined: model.ErrDuplicateFlowID
FAIL	github.com/kartaladev/wrkflw/definition/model [build failed]
```

**RED 2 — assertion failure**, after adding the sentinels but **not** the checks (`EXIT=1`):

```
--- FAIL: TestValidate/duplicate_node_ID_is_rejected
    Error: Expected error with "workflow-definition: duplicate node id" in chain but got nil.
--- FAIL: TestValidate/duplicate_node_ID_inside_a_sub-process_is_rejected_(recursion)
    Error: Expected error with "workflow-definition: duplicate node id" in chain but got nil.
--- FAIL: TestValidate/duplicate_flow_ID_is_rejected
    Error: Expected error with "workflow-definition: duplicate flow id" in chain but got nil.
```

The three **control** cases (blank flow IDs, node/flow ID sharing, cross-definition ID reuse)
passed in RED 2 — they are controls against over-rejection, not the rule under test.

### 5. GREEN

`go test -count=1 ./definition/... ` → **EXIT=0**, all 14 `definition/…` packages `ok`.

`go test -count=1 -race ./definition/... ` → EXIT=0;
`scripts/coverage.sh` over that profile: **93.9 %** (floor is 85 %).

`golangci-lint run ./definition/... ` → EXIT=0, `0 issues.` (package-scoped run — the
repo-wide lint is the controller's gate).

### 6. Mutation-ablation (three, each restored from a `cp` backup)

Backup: `cp definition/model/validate.go <scratchpad>/validate.go.bak` — **never**
`git checkout <path>`.

| Ablation | Result |
|---|---|
| `if false && nodeIDs[id]` (kill the node rule) | EXIT=1 — exactly the **2** node cases fail; the flow case still passes ⇒ the two rules are independently covered |
| `if false && flowIDs[f.ID]` (kill the flow rule) | EXIT=1 — exactly the **1** flow case fails |
| `if false && f.ID == ""` (kill the blank-ID exemption) | EXIT=1 — `blank_flow_IDs_are_not_duplicates_of_each_other` fails ⇒ that control is **not vacuous**; it really pins the "flow IDs are optional" decision |

Restore: `cp <scratchpad>/validate.go.bak definition/model/validate.go`;
`diff` → **DIFF_EXIT=0** (byte-identical); re-run → EXIT=0.

### 7. Repo-wide impact — the tightening newly rejects NOTHING

`go test -count=1 ./... > /tmp/all.log 2>&1` → `EXIT=1`, with Docker up (standing permission,
Verification item 2). **Exactly two failures, neither caused by this change:**

| Failure | Owner | Why it is not mine |
|---|---|---|
| `persistence [build failed]` — `undefined: persistence.NewPoolStatsCollector`, `WithPoolStatsMeterProvider`, `NewPostgresPoolStatsCollector` | another concurrent agent | the failing file `persistence/pool_stats_test.go` is **untracked** (`git status` `??`), i.e. created in this session by the `persistence` agent; a RED state of theirs |
| `runtime` → `TestSchedulerWithoutLocationCapabilityWarnsOnce` (`"[]" should have 1 item(s), but has 0`) | another concurrent agent | the failing file `runtime/timerops_location_warn_test.go` is likewise **untracked**; it is about scheduler location capability, nothing reads node/flow identity |

**No test anywhere in the repo is newly rejected by the duplicate-ID rule.** Corroborating
scan: every `testdata` YAML was checked for repeated `id:` values — **zero** hits.

The structural reason it broke nothing: `model.Validate` has exactly **one** non-test caller
inside the library, `definitionCore.build()` (`definition/model/builder.go:133`), plus the
three HTTP `Validate(&in)` request-DTO calls in `transport/http/httpcore/endpoints.go` (a
*different* `Validate` — request validation, not this one). Fixtures built as struct literals
— which is nearly every engine/runtime test — never reach `model.Validate` at all.

### 8. Effect on backlog item 27 (store round-trips semantically invalid definitions)

**Not fixed, and not made worse.** `internal/persistence/store/definitions.go` has no
`Validate` call on either side: `PutDefinition` (`:116`) marshals and writes, `GetDefinition`
(`:138`) / `Lookup` (`:166`) `json.Unmarshal` (`:154`, `:190`) and return. A definition with
duplicate node IDs that is already stored, or is written through the store rather than through
`Build()`, still round-trips unvalidated. Item 27 remains open; item 115 closes the
*authoring* hole only. When item 27 is fixed by calling `Validate` on the store path, the
pre-ADR-0144 stored-row audit already queued for ADR-0167 should be extended to duplicate IDs
as well.

### 9. ⚠ Handover to the controller — a comment in `engine/` is now FALSE

`engine/state_arms.go:274` (inside the `armIDsBySignal` doc block) states:

> `DE-DUPLICATION, FIRST WINS. model.Validate accepts duplicate node ids, two flows between
> one pair, and duplicate flow ids, so two arms can collide on one identity.`

After this change `model.Validate` rejects **duplicate node ids** and **duplicate flow ids**;
only "two flows between one pair" (same source/target, distinct IDs) remains accepted.

- The **code** is still correct and must stay: de-duplication is a defensive measure on a
  path whose definitions mostly never pass through `Validate` (see §7).
- The **comment** needs its enumeration narrowed. I did **not** touch it — `engine/` is owned
  by a concurrent agent. Controller: route this to the `engine` agent, or fold it into the
  bundle before the Delivery Gate (gate item 2, "documents describe what shipped", extends to
  comments).
