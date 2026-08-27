# ADR-0190 Phase 1 — disclosure control: implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: `superpowers:subagent-driven-development`.
> **TDD is strict** (CLAUDE.md rule #6): every RED must be *observed in a Bash call* before
> the implementation is written. A transcript with no visible red state fails review.

**Goal:** Close the unauthenticated disclosure at all **11** render entry points at once, by
projecting instance state through an allow-list whenever no actor is present on the request.

**Architecture:** Disclosure control lives **entirely in the transport** —
`transport/http/{httpcore,stdlib,gin,fiber}` plus a projection helper in `runtime/view`.
`service` is **not modified**. The signal is ADR-0189's per-request actor: present ⇒ full
fidelity, absent ⇒ the public projection. The projection is built as a **fresh** struct
carrying only allow-listed fields, so a field added tomorrow is withheld by default.

**Tech Stack:** Go 1.25, stdlib `reflect` (guard test only), `encoding/json`. No new deps.

**Spec:** `docs/specs/2026-08-26-route-group-authorization-posture-design.md`
**ADR:** `docs/adr/0190-authorization-is-gated-by-policy-not-by-authentication.md`
**Round-1 audit:** `docs/specs/2026-08-26-adr-0190-audit-{execution,counting,failuremode,interaction}.md`

---

## ▶ Progress

- **Branch:** `design/route-group-authz-posture`, base `main` at merge `7be335fb`.
  **NOT pushed, NOT merged.**
- **Status:** ✅ **PHASE 1 SHIPPED — both delivery gates passed.**
  `/code-review high`: **7 findings (4 Major, 3 Minor), NONE a false positive**, all fixed and
  folded. `/security-review`: **1 finding**, adjudicated defense-in-depth at 6/10 (unreachable
  through any built-in renderer) and **fixed anyway**, because it defeated `DisclosingMapper`'s
  own written contract and `SECURITY.md` recommends the setting that exposes it.
- **Verification, measured 2026-08-27 with Docker up:**
  `go test ./...` **EXIT=0, 65 ok / 0 FAIL** · `golangci-lint run ./...` **repo-wide, 0 issues**
  · `gofmt` clean.
- **Audit history:** revision 1 failed (~72 findings, 17 Critical); revision 2 failed
  (~50 findings, 16 Critical); owner then directed fold-and-implement rather than a third
  round. All eight lens reports are committed in `docs/specs/2026-08-2{6,7}-adr-0190-audit*`.

### Commits (oldest first)

| commit | what |
|---|---|
| `69393f56` | design bundle rev 2 + round-2 fixes + both audit evidence sets |
| `204494d9` | `authz` — disclosure vocabulary (allow-list) + the repo's second purity guard |
| `22cb9230` | `runtime/view` — `PublicState` + classification-totality guard |
| `96d6223d` | `service` — `ProjectFor` |
| `969e9a31` | `transport` — all 11 render sites wired, 3 adapters |
| `5d53d24e` | parity tests for gin and fiber |

### What implementation changed in the design — folded, not left in the transcript

1. **`service` IS modified.** The ADR claimed otherwise. `/snapshot` returns the
   self-marshalling `ProcessInstance` and only `service` can build one; rebuilding via
   `NewProcessInstance` **re-embeds the definition**, undoing `WithoutEmbeddedDefinition`.
   Hence `service.ProjectFor`, which may only ADD an omission.
2. **"Actor present" became "actor with a non-empty ID".** Reusing ADR-0189's `isZeroActor`
   handed full variables to the blessed kiosk claimant. The two guards answer different
   questions — *may this actor ACT* versus *may this caller SEE EVERYTHING*.
3. **The API break is 5 signatures, not 11.** Six render sites already accept
   `cfg.InstanceMapper`, so the adapters pass a per-request WRAPPED mapper and those
   endpoints never learn disclosure exists. Round 2 predicted "~70 call sites across 4
   packages"; the measured figure is 7 test call sites.

### ⚠ Test-quality lessons from this implementation

- **My own drift guard rested on a false premise.** It assumed the fixture populated every
  public field; `Status: StatusRunning` is **iota 0**, so a dropped `Status` compared equal
  to a kept one. The explicit `FIXTURE GAP` arm that fixed it then found **13 more**
  unpoliced fields.
- **Five withheld fields CANNOT be populated from an external test package** (unexported
  element types). Declared in `unpoliceableByFixture` with reasons rather than silently
  unpoliced.
- **A mutation that fails to COMPILE is not a RED.** Deleting a term from `ProjectFor` left a
  variable unused; `||`→`&&` was the valid mutation.
- **An ablation that stays green means UNTESTED before it means REDUNDANT.** The first fiber
  ablation hit only `StartInstance`, which those tests never exercise.
- **A vacuous case can hide inside a discriminating table.** With all nine fiber sites
  ablated, three of four went RED and `/actionable` stayed green — that view renders no
  variables, so the assertion could not fail. Fixed by claiming the task with an actor whose
  ID is the canary.

### What the gates caught that TWO four-lens audits did not

1. ⚠⚠ **`DisclosingMapper` was NESTED INSIDE ITSELF at all nine human-task sites**, from a
   scripted edit whose second pattern matched the text the first had inserted. **Every test
   passed** — projecting twice is idempotent, so the defect was invisible to every correctness
   assertion, including the mutation-verified ones. ⇒ **An idempotent defect needs a
   call-count or cost assertion.** One now exists.
2. ⚠⚠ **Identity was resolved 2–3× per request**, and `/snapshot`/`/actionable` fed two
   SEPARATE resolutions into two halves of ONE decision — a non-repeatable resolver could
   project the state while still emitting the definition. ADR-0190 Decision 7 said "decides
   once per request"; the code did not.
3. ⚠⚠ **`DiscloseNotes` was inert without `DiscloseActors`**, contradicting both godocs. The
   categories-are-independent table tested actors, actors+notes, variables and policy — **never
   notes ALONE**, the one failing case.
4. ⚠⚠ **The projection removed ADR-0175's operator escape hatch.**
5. ⚠⚠⚠ **`/security-review` found a leak in code written to fix finding 4** — the new
   `DiscloseOperations` copied the compensation cursor wholesale, carrying `Records[].Input`
   (a FIFTH variables snapshot) and `FinalErr` (**the same string** as `PendingFinalErr`, which
   is withheld under every category, and the same `errorCode` as `Incident.Error`, which is
   blanked). One value, three dispositions.
   ⚠⚠⚠ **The guard was blind BECAUSE I HAD TOLD IT TO BE.** `Compensating` sat in
   `unpoliceableByFixture` as "type unexported" — **false**: the type is unexported but all
   **20 fields are exported**, so the black-box package can populate and project it. ⇒ **A
   declared exemption is a place the guard cannot look; an over-broad one is worse than none.**

### Remaining

Merge `--no-ff` to `main` and push. Phases 2 and 3 deferred, constraints recorded in the spec.

## Global Constraints

- **Go 1.25.** No new module dependencies.
- **TDD strict.** No production code before an *observed* failing test, in a Bash call.
- **Judge a run by its exit code**, never a pipeline tail:
  `go test -count=1 ./pkg/... > /tmp/x.log 2>&1; echo "EXIT=$?"`, then read the log.
- **`go test -run` on a nonexistent name EXITS 0.** Anchoring does not help. Confirm a test
  *ran* with `grep -q '^--- PASS: <Name>'`.
- **Black-box tests** (`package <pkg>_test`). ⚠ `head -1` any existing test file first.
- **Table tests** per the project `table-test` skill: a **slice** of cases, `assert` closures,
  `t.Context()`.
- **Restore a mutation from a `cp` backup**, never `git checkout <path>`.
- **Baseline IS green: 65 ok / 0 FAIL, EXIT=0** — measured 2026-08-27 with Docker up.
  ⚠⚠ An earlier draft of this plan said "59 ok / 2 FAIL (`internal/database`,
  `internal/dbtest`, pre-existing)" and told you three times to ignore failures there. **That
  was false** — both pass (33.1 s, 26.1 s). It was inherited from a round-1 audit finding and
  restated without execution. **Treat ANY failure in those packages as a regression you
  caused.**
- **Container-free** (all packages this plan touches): `engine`, `service`, `transport/http`,
  `runtime/view`, `authz`, `humantask`.
- **Fan out BY GO PACKAGE.** Tasks below are grouped so no two concurrent agents share one.

### ⚠ Fixture traps measured in revision 1 — three prescribed assertions could not fail

`internal/transporttest`'s actor is **`{ID: "alice"}`** — *not* `alice@corp.example` — and
carries **no `Attributes`**. `ApprovalProcess`'s flow `f2` has **no `Condition`**. Any
assertion naming those values is vacuous. Every test below either names a value the harness
actually produces, or builds its own fixture and says so.

---

## The classification — derived mechanically, and it IS the design

Every field below was enumerated by `sed`/`grep` over the struct, not from memory. Task 2's
guard asserts this table is total: **a field in neither column fails the build.**

### `engine.InstanceState` — 31 exported fields

| public (11) | withheld (20) |
|---|---|
| `InstanceID`, `DefID`, `DefVersion`, `Status`, `StartedAt`, `EndedAt`, `PendingCancel`, `PendingFinalStatus`, `Tokens`†, `Tasks`†, `History`† | `Variables`, `StartVariables`, `Incidents`, `PendingFinalErr`, `RootCompensations`, `Scopes`, `ArchivedCompensations`, `Compensating`, `Timers`, `ArmedEvents`, `Boundaries`, `EventTriggeredSubprocesses`, `DeferredCompensationThrows`, `RecentCompensationCmdIDs`, `CmdSeq`, `TokenSeq`, `TaskSeq`, `TimerSeq`, `ScopeSeq`, `IncidentSeq` |

† projected element-wise through their own tables below, never copied wholesale.

⚠ `RootCompensations`, `Scopes` and `ArchivedCompensations` are withheld **because each
carries `CompensationRecord.Input`**, documented as *"a snapshot of the instance variables at
invocation time"*. Revision 1's deny-list missed all three.

### `engine.Token` — 13 exported fields

| public (7) | withheld (6) |
|---|---|
| `ID`, `NodeID`, `ScopeID`, `State`, `EnteredAt`, `RetryAttempts`, `RetryStartedAt` | `Payload`, `AwaitCommand`, `AwaitSignal`, `AwaitMessage`, `AwaitMessageKey`, `AwaitTimer` |

⚠ The `Await*` fields are withheld not for variables but because a correlation key or signal
name is a business identifier. No prior document considered them.

### `humantask.HumanTask` — 11 exported fields

| public (6) | withheld (5) |
|---|---|
| `TaskID`, `NodeID`, `InstanceID`, `State`, `CreatedAt`, `DueAt` | `Vars`, `Eligibility`, `Claim`, `Completion`, `Candidates` |

⚠ Withholding `Claim` and `Completion` **wholesale** loses no discriminator: `State` already
carries `claimed`/`completed`. So an unidentified caller still learns *whether* a task is
claimed, only never *by whom* — which satisfies ADR-0152 more cleanly than revision 1's
zeroed-actor-inside-a-kept-claim did.

### `engine.NodeVisit` — 6 exported fields, all public

`NodeID`, `TokenID`, `EnteredAt`, `LeftAt`, `TaskID`, `CloseKind`. Nothing withheld; recorded
explicitly so the guard has a declared answer for each.

---

## Task 1: the disclosure vocabulary (package `authz`)

**Files:** create `authz/disclosure.go`, `authz/disclosure_test.go`, `authz/purity_test.go`

**Produces:** `authz.DisclosureCategory` (string); `DiscloseVariables`, `DiscloseActors`,
`DiscloseNotes`, `DisclosePolicy`; `authz.DisclosureSet` with
`NewDisclosureSet(...)` and `Has(...)`. Tasks 2 and 3 consume `Has`.

⚠ Polarity: this is an **allow**-list. The zero set discloses **nothing** beyond the
structural baseline. Revision 1's type meant the opposite; do not copy its semantics.

- [ ] **Step 1: write the failing test**

```go
// authz/disclosure_test.go
package authz_test

import (
	"testing"

	"github.com/kartaladev/wrkflw/authz"
)

func TestZeroDisclosureSet_DisclosesNothing(t *testing.T) {
	t.Parallel()

	var zero authz.DisclosureSet // the zero value must be the CLOSED posture
	for _, c := range []authz.DisclosureCategory{
		authz.DiscloseVariables, authz.DiscloseActors,
		authz.DiscloseNotes, authz.DisclosePolicy,
	} {
		if zero.Has(c) {
			t.Errorf("zero DisclosureSet must not disclose %q", c)
		}
	}
}

func TestNewDisclosureSet_WidensExplicitly(t *testing.T) {
	t.Parallel()

	s := authz.NewDisclosureSet(authz.DiscloseVariables)
	if !s.Has(authz.DiscloseVariables) {
		t.Error("explicitly requested category not disclosed")
	}
	if s.Has(authz.DiscloseActors) {
		t.Error("unrequested category disclosed — the set must widen, never default open")
	}
}
```

- [ ] **Step 2: run and OBSERVE FAIL**

```bash
go test -count=1 ./authz/... > /tmp/t1.log 2>&1; echo "EXIT=$?"; cat /tmp/t1.log
```
Expected: `undefined: authz.DisclosureSet`. That compile error is the valid RED.

- [ ] **Step 3: implement**

```go
// authz/disclosure.go
package authz

// DisclosureCategory names a class of field a mount may CHOOSE to disclose to a caller
// the transport could not identify.
//
// ⚠ This is an ALLOW-list. The zero DisclosureSet discloses nothing beyond the structural
// baseline, so a category nobody thought about is withheld rather than exposed. A field
// added to the engine's state tomorrow is likewise withheld until classified — see
// runtime/view.PublicState and its classification guard.
type DisclosureCategory string

const (
	// DiscloseVariables permits process variables and every snapshot of them.
	DiscloseVariables DisclosureCategory = "variables"
	// DiscloseActors permits actor identity and attributes: candidates, claims, completions.
	DiscloseActors DisclosureCategory = "actors"
	// DiscloseNotes permits free-text completion notes.
	DiscloseNotes DisclosureCategory = "notes"
	// DisclosePolicy permits authorization policy and routing expressions: the embedded
	// definition and flow conditions.
	DisclosePolicy DisclosureCategory = "policy"
)

// DisclosureSet is a membership test. Its ZERO VALUE is the closed posture.
type DisclosureSet map[DisclosureCategory]struct{}

// NewDisclosureSet widens disclosure to exactly cats.
func NewDisclosureSet(cats ...DisclosureCategory) DisclosureSet {
	s := make(DisclosureSet, len(cats))
	for _, c := range cats {
		s[c] = struct{}{}
	}
	return s
}

// Has reports whether c may be disclosed. A nil set discloses nothing.
func (s DisclosureSet) Has(c DisclosureCategory) bool {
	_, ok := s[c]
	return ok
}
```

- [ ] **Step 4: run and OBSERVE PASS**

```bash
go test -count=1 -v ./authz/... > /tmp/t1.log 2>&1; echo "EXIT=$?"
grep -q '^--- PASS: TestZeroDisclosureSet_DisclosesNothing' /tmp/t1.log && echo RAN || echo "DID NOT RUN"
```

- [ ] **Step 5: the purity guard, ablated NON-CYCLICALLY**

⚠ Revision 1 prescribed ablating with `import _ ".../engine"`. That is an **import cycle**
(`engine` imports `authz`): the package never compiles, the test never runs, and
`[setup failed]` is **not a RED**. Ablate with `definition/model` instead, which is a real
forbidden import that compiles.

```go
// authz/purity_test.go
package authz_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAuthzPurity pins that authz depends on nothing in-repo but internal/expreval.
// authz is imported by engine, so anything added here propagates into the engine core.
func TestAuthzPurity(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	const allowed = "github.com/kartaladev/wrkflw/internal/expreval"
	for _, imp := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if !strings.HasPrefix(imp, "github.com/kartaladev/wrkflw/") {
			continue
		}
		if imp != allowed {
			t.Errorf("authz must not import %q — only %q is permitted", imp, allowed)
		}
	}
}
```

Ablate: add `_ "github.com/kartaladev/wrkflw/definition/model"` to `authz/disclosure.go`, run,
**observe a real RED** (not `[setup failed]`), restore from a `cp` backup, `diff`.

- [ ] **Step 6: commit**

```bash
git add authz/disclosure.go authz/disclosure_test.go authz/purity_test.go
git commit -m "feat(authz): disclosure vocabulary (allow-list) and a purity guard"
```

---

## Task 2: the projection and its classification guard (package `runtime/view`)

**Files:** create `runtime/view/public.go`, `runtime/view/public_test.go`,
`runtime/view/classification_test.go`

**Consumes:** `authz.DisclosureSet` (Task 1).
**Produces:** `view.PublicState(st engine.InstanceState, d authz.DisclosureSet) engine.InstanceState`.
Task 3 consumes it.

- [ ] **Step 1: write the failing tests — fixture MUST populate all seven variables sites**

⚠ This is the task's whole point. Revision 1's redactor leaked four of seven and its test
passed, because the fixture only populated `Variables`.

```go
// runtime/view/public_test.go
package view_test

func TestPublicState_WithholdsEverySnapshotOfVariables(t *testing.T) {
	t.Parallel()

	const secret = "111-22-3333"
	// Fixture populates ALL SEVEN measured variables sites.
	st := stateWithSecretEverywhere(t, secret) // helper below
	got := view.PublicState(st, authz.DisclosureSet{})

	blob, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), secret) {
		t.Errorf("public projection leaks %q\n%s", secret, blob)
	}
}

// stateWithSecretEverywhere populates: Variables, StartVariables, Tokens[].Payload,
// Tasks[].Vars, RootCompensations[].Input, Scopes[].Compensations[].Input,
// ArchivedCompensations[k][].Input. Any site left empty makes the test vacuous for it.

func TestPublicState_KeepsStructuralFields(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t, "x")
	got := view.PublicState(st, authz.DisclosureSet{})

	if got.InstanceID != st.InstanceID || got.Status != st.Status {
		t.Error("structural fields must survive the projection")
	}
	if len(got.Tokens) != len(st.Tokens) || len(got.Tasks) != len(st.Tasks) {
		t.Error("token and task COUNTS are structural and must survive")
	}
	if got.Tasks[0].State != st.Tasks[0].State {
		t.Error("task State is the claimed/completed discriminator and must survive")
	}
}

func TestPublicState_DoesNotMutateInput(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t, "111-22-3333")
	_ = view.PublicState(st, authz.DisclosureSet{})

	if st.Variables["ssn"] != "111-22-3333" || st.Tasks[0].Vars == nil {
		t.Error("PublicState mutated its input; in-process State() fidelity is broken")
	}
}

func TestPublicState_WidensOnDisclosure(t *testing.T) {
	t.Parallel()

	st := stateWithSecretEverywhere(t, "111-22-3333")
	got := view.PublicState(st, authz.NewDisclosureSet(authz.DiscloseVariables))

	if got.Variables["ssn"] != "111-22-3333" {
		t.Error("DiscloseVariables must restore variables")
	}
	if got.Tasks[0].Claim != nil {
		t.Error("DiscloseVariables must NOT restore actors — categories are independent")
	}
}
```

- [ ] **Step 2: run and OBSERVE FAIL**

```bash
go test -count=1 ./runtime/view/... > /tmp/t2.log 2>&1; echo "EXIT=$?"; tail -30 /tmp/t2.log
```

- [ ] **Step 3: implement as a FRESH literal, never a copy**

⚠ Build a new struct. Do **not** write `out := st` — that is revision 1's mistake and it
carries every unlisted field forward. A fresh keyed literal zeroes what it does not name,
which is measured-legal despite `InstanceState`'s unexported fields (spec §2.6).

```go
// runtime/view/public.go

// PublicState returns a fresh projection of st carrying only fields classified public,
// widened by d.
//
// ⚠ It BUILDS A NEW STRUCT rather than copying st. Anything not named below is absent by
// construction, so a field added to engine.InstanceState tomorrow is withheld without
// anyone remembering to withhold it. TestClassification_IsTotal fails until such a field
// is classified.
//
// ⚠ RENDER-ONLY. The projection drops engine.InstanceState's unexported id source and
// sequence counters. It must never be fed back into the engine.
//
// ⚠ It never mutates st: callers hold state obtained from ProcessInstance.State(), which
// in-process consumers rely on for full fidelity.
func PublicState(st engine.InstanceState, d authz.DisclosureSet) engine.InstanceState {
	out := engine.InstanceState{
		InstanceID:         st.InstanceID,
		DefID:              st.DefID,
		DefVersion:         st.DefVersion,
		Status:             st.Status,
		StartedAt:          st.StartedAt,
		EndedAt:            st.EndedAt,
		PendingCancel:      st.PendingCancel,
		PendingFinalStatus: st.PendingFinalStatus,
		History:            slices.Clone(st.History), // all six fields are public
	}

	out.Tokens = make([]engine.Token, len(st.Tokens))
	for i, t := range st.Tokens {
		out.Tokens[i] = engine.Token{
			ID: t.ID, NodeID: t.NodeID, ScopeID: t.ScopeID, State: t.State,
			EnteredAt: t.EnteredAt, RetryAttempts: t.RetryAttempts,
			RetryStartedAt: t.RetryStartedAt,
		}
		if d.Has(authz.DiscloseVariables) {
			out.Tokens[i].Payload = t.Payload
		}
	}

	out.Tasks = make([]humantask.HumanTask, len(st.Tasks))
	for i, tk := range st.Tasks {
		out.Tasks[i] = humantask.HumanTask{
			TaskID: tk.TaskID, NodeID: tk.NodeID, InstanceID: tk.InstanceID,
			State: tk.State, CreatedAt: tk.CreatedAt, DueAt: tk.DueAt,
		}
		if d.Has(authz.DiscloseVariables) {
			out.Tasks[i].Vars = tk.Vars
		}
		if d.Has(authz.DiscloseActors) {
			out.Tasks[i].Candidates = tk.Candidates
			out.Tasks[i].Claim = tk.Claim
			out.Tasks[i].Completion = tk.Completion
		}
		if d.Has(authz.DisclosePolicy) {
			out.Tasks[i].Eligibility = tk.Eligibility
		}
	}

	if d.Has(authz.DiscloseVariables) {
		out.Variables = st.Variables
		out.StartVariables = st.StartVariables
		out.RootCompensations = st.RootCompensations
		out.Scopes = st.Scopes
		out.ArchivedCompensations = st.ArchivedCompensations
	}
	return out
}
```

⚠⚠ **ROUND-2 CORRECTION (E7/F2).** `DiscloseActors` restores `Completion`, which carries
`Note`. Blanking it after the assignment **writes through a shared pointer**: `Completion` is
a `*Completion`, and two `pi.State()` calls return the same one. **Copy before blanking:**

```go
if d.Has(authz.DiscloseActors) {
    out.Tasks[i].Candidates = tk.Candidates
    out.Tasks[i].Claim = tk.Claim
    if c := tk.Completion; c != nil {
        cc := *c // COPY — blanking through tk.Completion mutates the caller's state
        if !d.Has(authz.DiscloseNotes) {
            cc.Note = ""
        }
        out.Tasks[i].Completion = &cc
    }
}
```

⚠⚠ And the earlier draft claimed *"categories are independent and a test asserts it"* — **no
prescribed test touched `DiscloseActors` or `DiscloseNotes` at all**, and
`TestPublicState_DoesNotMutateInput` used the ZERO set, which never enters this branch. Add
both: one asserting `DiscloseActors` alone does **not** leak a note, one asserting the input's
`Completion.Note` survives the call.

- [ ] **Step 4: the classification guard — the invariant that replaces the enumeration**

```go
// runtime/view/classification_test.go
package view_test

// publicFields / withheldFields transcribe the plan's classification table.
// TestClassification_IsTotal asserts they PARTITION each struct's exported fields, so a
// field added tomorrow belongs to neither and FAILS HERE, naming itself.
func TestClassification_IsTotal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		typ      reflect.Type
		public   []string
		withheld []string
	}{
		{"InstanceState", reflect.TypeOf(engine.InstanceState{}), publicInstanceState, withheldInstanceState},
		{"Token", reflect.TypeOf(engine.Token{}), publicToken, withheldToken},
		{"HumanTask", reflect.TypeOf(humantask.HumanTask{}), publicHumanTask, withheldHumanTask},
		{"NodeVisit", reflect.TypeOf(engine.NodeVisit{}), publicNodeVisit, nil},
		// ⚠⚠ ROUND-2 (E8/FM-11): the projection copies these WHOLESALE, so
		// "withheld by construction" does NOT hold one level down. Every type the
		// projection copies must be classified, or a field added to Completion is
		// disclosed under DiscloseActors with the whole suite green.
		{"Claim", reflect.TypeOf(humantask.Claim{}), publicClaim, withheldClaim},
		{"Completion", reflect.TypeOf(humantask.Completion{}), publicCompletion, withheldCompletion},
		{"Actor", reflect.TypeOf(authz.Actor{}), publicActor, withheldActor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared := map[string]int{}
			for _, f := range tc.public {
				declared[f]++
			}
			for _, f := range tc.withheld {
				declared[f]++
			}
			for i := range tc.typ.NumField() {
				f := tc.typ.Field(i)
				if !f.IsExported() {
					continue
				}
				switch declared[f.Name] {
				case 0:
					t.Errorf("%s.%s is UNCLASSIFIED — add it to publicX or withheldX in "+
						"this file AND to PublicState if public", tc.name, f.Name)
				case 1: // correct
				default:
					t.Errorf("%s.%s is in BOTH sets", tc.name, f.Name)
				}
			}
			for name := range declared {
				if _, ok := tc.typ.FieldByName(name); !ok {
					t.Errorf("%s.%s is classified but no longer exists — stale entry",
						tc.name, name)
				}
			}
		})
	}
}
```

⚠ The stale-entry arm is not decoration: without it, a removed field leaves a lingering
classification and the guard silently over-reports coverage.

- [ ] **Step 5: ABLATE THE GUARD BY ADDING A FIELD, not by editing a list**

Add `Scratch map[string]any` to `engine.InstanceState`. Run. **`TestClassification_IsTotal`
must fail naming `InstanceState.Scratch`.** Then confirm `TestPublicState_*` still pass —
proving the new field was withheld *by construction*.

⚠⚠ **ROUND-2 (E8): ablate ONE LEVEL DOWN TOO.** Add `Scratch string` to
`humantask.Completion` and re-run. `InstanceState` is the level that works; `Completion` is
copied wholesale, so the "by construction" property does **not** hold there and the guard is
the only thing standing. If the guard does not fail, the extension above is wrong.

Restore from a `cp` backup and `diff` after each.

- [ ] **Step 6: commit**

```bash
git add runtime/view/public.go runtime/view/public_test.go runtime/view/classification_test.go
git commit -m "feat(view): allow-listed public state projection with a totality guard"
```

---

## Task 3: the transport decision point (package `transport/http/httpcore`)

**Files:** modify `transport/http/httpcore/seam.go`; create
`transport/http/httpcore/disclosure.go`, `transport/http/httpcore/disclosure_test.go`

**Consumes:** `view.PublicState` (Task 2), `httpcore.RequestActor` (ADR-0189, existing).
**Produces:** `httpcore.WithDisclosure(cats ...authz.DisclosureCategory) CustomizeOption[R]`;
`httpcore.DiscloseAll`; and the internal `renderState(ctx, cfg, pi) engine.InstanceState`
that Task 4 calls at every site.

⚠ `CustomizeConfig.Disclosure` needs the same "was it set" treatment as `MaxBodyBytes`: a
map has a nil, but `WithDisclosure()` with no arguments is a *meaningful* closed set, so it
must be distinguishable from never calling it. Both resolve to the closed posture here, so a
plain map suffices — **state that in the doc comment** rather than leaving the reader to infer
it, because the sibling options solve it differently.

- [ ] **Step 1: write the failing test**

```go
// transport/http/httpcore/disclosure_test.go
package httpcore_test

func TestRenderState_NoActorYieldsPublicProjection(t *testing.T) {
	t.Parallel()
	// ctx WITHOUT authz.ContextWithActor
	got := httpcore.RenderStateForTest(t.Context(), cfgClosed(t), instanceWithSecret(t))
	if got.Variables != nil {
		t.Errorf("no actor on ctx must yield the public projection, got %v", got.Variables)
	}
}

func TestRenderState_ActorPresentYieldsFullFidelity(t *testing.T) {
	t.Parallel()
	ctx := authz.ContextWithActor(t.Context(), authz.Actor{ID: "alice", Roles: []string{"manager"}})
	got := httpcore.RenderStateForTest(ctx, cfgClosed(t), instanceWithSecret(t))
	if got.Variables["ssn"] == nil {
		t.Error("an identified caller must receive full fidelity")
	}
}

func TestDiscloseAll_RestoresPriorShape(t *testing.T) {
	t.Parallel()
	got := httpcore.RenderStateForTest(t.Context(), cfgDiscloseAll(t), instanceWithSecret(t))
	if got.Variables["ssn"] == nil || got.Tasks[0].Vars == nil {
		t.Error("DiscloseAll must restore the pre-ADR-0190 shape")
	}
}
```

- [ ] **Step 2: run and OBSERVE FAIL**, then **Step 3: implement**

```go
// transport/http/httpcore/disclosure.go

// renderState decides ONCE per request what an instance's state may disclose.
//
// The signal is ACTOR PRESENCE, not the outcome of any authorization check: ADR-0189
// resolves the actor from the request context, and a caller the transport could not
// identify receives view.PublicState.
//
// ⚠ It is deliberately NOT keyed on an authorization result. An empty authz.AuthzSpec
// ALLOWS the zero actor (measured), so keying on "was this allowed" would let a permissive
// read policy re-open the very disclosure this closes.
func renderState(ctx context.Context, resolve RequestActorFunc, d authz.DisclosureSet, pi service.ProcessInstance) engine.InstanceState {
	if identified(ctx, resolve) {
		return pi.State()
	}
	return view.PublicState(pi.State(), d)
}

// identified reports whether the request carries a REAL principal.
//
// ⚠ It resolves through the CONFIGURED resolver, not authz.ActorFromContext. MEASURED:
// no code in transport/http ever calls authz.ContextWithActor — ADR-0189 passes the actor
// as a function ARGUMENT. A consumer using WithRequestActor ("identity lives somewhere the
// context does not reach") would otherwise be projected on all 11 sites INCLUDING for
// authenticated callers.
//
// ⚠ It rejects the ZERO actor via the existing isZeroActor. "Present" must mean IDENTIFIED,
// not merely non-nil: ADR-0189 blesses a kiosk claimant {ID:"", Roles:["kiosk"]}, and an
// empty AuthzSpec allows the zero actor. isZeroActor lives three lines away in this package.
func identified(ctx context.Context, resolve RequestActorFunc) bool {
	if resolve == nil {
		return false
	}
	a, err := resolve(ctx)
	return err == nil && !isZeroActor(a)
}
```

⚠⚠ **ROUND-2 CORRECTION (E11/FM-2/I2-C1).** An earlier draft said to use
`authz.ActorFromContext` directly. **That is wrong and was measured wrong:** nothing in
`transport/http` ever calls `authz.ContextWithActor`, so it is ALWAYS false and every
authenticated caller would be projected. Resolve through the configured `RequestActorFunc`.

⚠ It must still never turn an unidentified read into a 401 — D1 keeps these routes
reachable. Swallow the resolver error into `false`.

⚠⚠ **The other half of E11**: the three task handlers must ALSO put the resolved actor on
the context with `authz.ContextWithActor`, so anything downstream reading the seam agrees
with what `renderState` decided. Either half alone leaves a hole.

- [ ] **Step 4: run and OBSERVE PASS**; **Step 5: commit**

---

## Task 4: wire all eleven entry points (package `transport/http/httpcore`)

**Files:** modify `transport/http/httpcore/endpoints.go`, `admin_endpoints.go`; create
`transport/http/httpcore/disclosure_endpoints_test.go`

⚠ **All eleven, in one task, deliberately.** A per-endpoint rollout is what let `/signals`
survive revision 1. The sites, mechanically derived:

| mechanism | sites |
|---|---|
| `mapInstance` | `endpoints.go:42, 52, 94, 133, 158, 182` |
| `NewInstanceView` direct | `admin_endpoints.go:111, 121, 514` |
| self-marshalling `pi` | `endpoints.go:65` |
| `NewActionableView` | `endpoints.go:77` |

- [ ] **Step 1: write the failing test — one subtest PER ENTRY POINT**

Table-driven over all eleven, each asserting the response contains none of the secrets. ⚠ Use
`"111-22-3333"` in `Vars` and a **self-built** actor `alice@corp.example` with attributes —
the standard harness's `alice` has none, which is what made three revision-1 assertions
vacuous.

- [ ] **Step 2: run and OBSERVE FAIL** — expect failures at every one of the eleven.

- [ ] **Step 3: implement.** Replace `pi.State()` with the helper at all ten state-passing
      sites.

⚠⚠ **ROUND-2 CORRECTION (E4).** This task was scoped to 2 files. It is not: threading the
disclosure set and resolver reaches **11 exported `httpcore` signatures and ~70 call sites
across 4 packages** (33 production adapter sites, 13 adapter-test, 24 httpcore-test). It
therefore **breaks Task 5's packages**, so the by-package fan-out does not apply here —
**Tasks 4 and 5 are ONE SERIAL TASK, done by one agent.** Committing Task 4 alone leaves a red
tree. The public-API break must also be declared in the ADR's Consequences.

⚠⚠ **ROUND-2 CORRECTION (E6/I2-C2) — `/actionable` also needs the DEFINITION gated.** The
helper carries *state*; `allowed_actions[].condition` comes from the **definition**. Measured
leaking `"condition":"vars.salary > 100000 && actor.dept == \"SECRET-DEPT\""` to an anonymous
caller while variables and candidates were correctly closed — invisible to a secret-string
grep. Gate `def` at BOTH definition-consuming sites:

```go
func publicDef(ctx context.Context, resolve RequestActorFunc, d authz.DisclosureSet,
	def *model.ProcessDefinition) *model.ProcessDefinition {
	if identified(ctx, resolve) || d.Has(authz.DisclosePolicy) {
		return def
	}
	return nil // carries every node's eligibility spec AND every flow condition
}
```

⚠ The fixture MUST declare a flow condition. `ApprovalProcess`'s `f2` has none, so an
assertion against it is vacuous — the trap that made revision 1's T1 useless here.

⚠⚠ **ROUND-2 CORRECTION (E1/FM-4) — do NOT reconstruct `/snapshot` via
`service.NewProcessInstance`.** It hardcodes `omitDefinition=false` (its godoc says *"always
embeds"*), and the plan forbids touching `service`. Measured: the rebuild went **781 → 1068
bytes**, **re-embedding a definition `WithoutEmbeddedDefinition` had suppressed** — carrying
`nodes[].eligible_roles`. A disclosure fix that *widens* disclosure, and it hits **identified**
callers too.

Render `/snapshot` the same way as every other site instead: project the state, and let the
existing marshalling path run unchanged. If that proves impossible without a `service` change,
**make the change and retract the ADR's "service is not modified" claim** — do not ship the
reconstruction.

- [ ] **Step 4: run and OBSERVE PASS (all eleven)**; **Step 5: mutation-verify** by reverting
      `endpoints.go:94` (`DeliverSignal`) alone and confirming **only** its subtest goes RED —
      proving the table discriminates per site rather than passing wholesale.

- [ ] **Step 6: commit**

---

## Task 5: parity, opt-out fidelity, and docs (packages `transport/http/{gin,fiber}`, docs)

- [ ] **Step 1:** parity tests asserting gin and fiber match stdlib on the eleven sites. All
      three dispatch to an identical 29-member set of `httpcore` functions, so this should
      pass once Task 4 lands — **if it does not, the shared-core assumption is wrong and that
      is a finding.**
- [ ] **Step 2:** a byte-comparison test that `WithDisclosure(DiscloseAll...)` reproduces the
      pre-change body exactly, on `/snapshot` especially, where Task 4 *reconstructs* the
      document rather than passing it through.
- [ ] **Step 3:** `SECURITY.md` — the posture, and the **breaking change** as a headline: all
      eleven entry points change shape for unidentified callers; `DiscloseAll` is the
      one-call opt-out; a custom `InstanceMapper` now receives the projection.
- [ ] **Step 4:** re-read the ADR and spec against what shipped and correct every divergence
      (CLAUDE.md rule #11). **Then squash Phase 1 into one feature bundle** with the spec, ADR,
      plan and the four audit records.

---

## Accepted residuals — decided 2026-08-27, NOT fixed in phase 1

Recorded because a residual you write down is still a defect you shipped; each is stated with
the reason it is not being closed rather than left implicit.

- **The structural oracle (FM-7 + FM-6).** Kept structural fields leak withheld ones:
  `history[].node_id` reveals which gateway branch ran, and a branch is a function of the
  process variables, so an attacker infers variable values from structure. Closing it means
  withholding history, which destroys the view's usefulness for the legitimate reader.
  **Accepted; must appear in `SECURITY.md`, not only here.**
- **Error bodies are out of scope (FM-8).** `ClassifyError`'s 403 arm ships `err.Error()`,
  which for an ABAC failure is the entire predicate source, twice, with a caret diagram.
  **Adjudicated lower severity:** the 403 arm fires only for `authz.ErrNotAuthorized`, which
  today occurs only on the three human-task verbs — so the reader is already authenticated.
  Scoping `ClassifyError` in would widen this bundle again. **Accepted.**
- **The projection answers 17 exported methods falsely (I2-C9).** `InstanceState` has 17
  exported methods (`HasArmedTimers`, `SignalWaiters`, `TaskByID`, …) that answer from
  whatever state they are on, so a consumer's `InstanceMapper` receiving the projection gets
  *false statements*, not merely fewer fields.
  ⚠ **A distinct projection type was recommended and then REJECTED on cost:** `InstanceMapper`
  is `func(engine.InstanceState) any`, a documented public seam, so a new type breaks every
  consumer's mapper — *"does it prevent users using our library?"* decides it. **Accepted, with
  two obligations:** `PublicState`'s godoc must state the hazard prominently, and a test must
  pin it so the behaviour is at least known and asserted rather than discovered.

---

## Phase 1 verification checklist

- [ ] `go test -race -coverprofile=cover.out ./... && scripts/coverage.sh cover.out` — touched
      packages ≥ 85 %. Probe Docker first; **baseline is 65 ok / 0 FAIL** (measured
      2026-08-27) — so **every** failure is yours, and none may be waved off as pre-existing.
- [ ] `go test ./...` — **re-derive the breakage net; do not inherit revision 1's.** Its
      8-file prediction was wrong both ways: measured **18 failures in 3 packages**, 6 of 8
      predicted files unaffected, and `transport/http/stdlib/maxbody_test.go` breaks and was
      unnamed. Every break must be adjudicated as *correct* rather than patched away.
- [ ] `golangci-lint run ./...` repo-wide. Probe with `command -v`; never substitute `go vet`.
- [ ] `go vet ./...` — cheap proof no Docker-only test package depends on a changed signature.
- [ ] Mutation evidence recorded for Tasks 1, 2, 4 (the guard ablation in Task 2 step 5 is the
      load-bearing one: **add a field**, do not edit a list).
- [ ] `/code-review` and `/security-review` (owner-invoked), findings folded via `--amend`.

---

## Phases 2 and 3 — deferred, with constraints recorded

Each gets **its own spec, ADR and rule-#9 audit**, written against the tree after phase 1
lands. Writing them out in revision 1 is what produced its Criticals. The audit-derived
constraints are in spec §3 D2 and D7 — the load-bearing ones: a service-layer gate cannot
precede the body decode; the `AllowAll` type check is defeated by wrapping; the
"Privileges-only" rule is wrong because `{Roles, Privileges}` also allows; the no-instance
operation set was wrong in both directions; and `service` has no `WithAuthorizer`.

---

## Self-review notes

- **Spec coverage.** Tasks 1–5 cover spec D6 and tests T1–T11 in full. D2/D7 are deferred by
  design and named as such.
- **Type consistency.** `authz.DisclosureSet` is produced in Task 1 and consumed under that
  name in Tasks 2 and 3. `view.PublicState(st, d)` is defined in Task 2 and called in Task 3.
  `renderState` is defined in Task 3 and called in Task 4.
- **Placeholder scan.** Clean. Task 4's per-endpoint table is described by shape rather than
  written out for all eleven rows; the eleven sites are enumerated explicitly in the table
  above, so the implementer has the exact list rather than a direction to go find it.
- **Known asymmetry, stated.** Task 2's `PublicState` restores `Completion` under
  `DiscloseActors`, which also carries `Note`. The note must be blanked unless `DiscloseNotes`
  is set. This is the one place two categories touch one field.
