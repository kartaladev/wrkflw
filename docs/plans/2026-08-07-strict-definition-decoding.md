# Strict Definition Decoding — Implementation Plan

> **▶ Progress — IMPLEMENTED 2026-08-08, awaiting the owner-only Delivery Gate**
>
> - **Branch:** `feat/strict-definition-decoding`. Do not quote this bundle's own
>   SHA here — the delivery amend changes it.
> - ⚠ **This branch IS pushed, deliberately**, so the bundle survives the loss of
>   this machine. Folding implementation into the bundle commit therefore needs
>   `git push --force-with-lease`. Acceptable **only** because the branch is
>   unmerged, untracked by anyone else and never on `main`. Do not read the push
>   as delivery.
> - **Status: all 10 tasks landed**, plus seven fixes from the adversarial
>   stand-in reviews. Production diff is **91 inserted lines** across
>   `definition/model/yaml.go` (58) and `node_wire.go` (36) — comments included;
>   the logic itself is small. Design (D1, D2, D3, D3a, D4) survived
>   implementation unchanged; D3's "preserve existing error shapes" is what drove
>   two of the review fixes.
> - **Rule-#9 audit ran 2026-08-08** (two Opus auditors, separate worktrees, both
>   briefed to EXECUTE): 24 findings, 2 CRITICAL + 9 MAJOR, verdict *not safe to
>   implement as written*. All CRITICAL/MAJOR accepted and folded before any code.
>
> **Verification — all green, run on the implemented tree:**
>
> | gate | result |
> |---|---|
> | `go test -race -coverprofile=cover.out ./...` | **EXIT=0**, 64 packages, 0 races, 0 failures |
> | `scripts/coverage.sh cover.out` | repo **73.9 %** — a shade above the 73.8 % baseline |
> | `definition/model` coverage | **94.9 %** (floor 85; audit predicted 93.8) |
> | `go vet ./...` | EXIT=0 — compiles Docker-gated test files too |
> | `golangci-lint run ./...` | EXIT=0, 0 issues |
> | mutations | **15 applied, 15 caught**, every one compiled and restored `diff`-clean |
>
> ⚠ Docker was **explicitly approved by the owner for this run**; that approval
> does not carry over to the next session.
>
> **Delivery Gate: PASSED 4/4.** `/code-review` returned **6 findings — all fixed
> and folded** (see spec §7); `/security-review` returned **0 vulnerabilities**,
> with fail-open, field-dropping, error-leakage and YAML-deserialization each
> checked by execution. **Only the merge `--no-ff` to `main` and the push remain**,
> and those wait on owner confirmation per the standing cadence.
>
> **Adversarial Opus stand-ins ran first** (two, separate `git worktree`s, both
> briefed to EXECUTE against a `main` baseline). Every finding was independently
> reproduced by the controller before being acted on. Full adjudication in spec §6.
> Headline: **one HIGH finding was a live instance of the very bypass this ADR
> closes** — YAML strictness stopped at the first document, so a `---` overlay
> declaring `eligible_roles` parsed clean and built a task with none. Fixed.
> Also fixed: a bare `io.EOF` escaping from empty JSON input; a prefix-collision
> hole in the all-tags guard that let `action2`/`idX` through unchecked; a parse
> error ~2.4x the size of its own input; and **two false claims I had written into
> `CHANGELOG.md` while correcting someone else's rotted enumeration**.
>
> Five findings were adjudicated **record-not-fix** and filed in `HANDOVER.md`
> under "Follow-ups opened by ADR-0167's adversarial review" — each executed, none
> caused by this ADR alone, each left out to keep an already-breaking change
> bounded. The sharpest is that an undecodable stored definition degrades
> **silently**: `runtime/jobstore.go` logs "definition not found" and skips every
> armed timer for it, forever.
>
> **Four ways implementation corrected the design** — full detail in spec §8:
>
> 1. ⚠⚠ **The audit's README enumeration had rotted: 7 camelCase lines, not the
>    4 it named.** `deadlineDuration`, `deadlineFlow` and `deadlineAction`
>    (869–871) were missed, plus the nested `retryPolicy` sub-keys. Following the
>    plan literally would have shipped a README that still did not parse. This is
>    the ADR-0159 lesson verbatim — **re-count an inherited enumeration, never
>    restate it.**
> 2. **A second, independent README defect, found by execution:** `errorEndEvent`
>    was documented as a valid `kind` and has never been registered (fails on
>    `main` too). The other 17 documented kinds were each probed and parse.
> 3. **T8 shipped self-maintaining**, deriving the tag list from source at test
>    time instead of pasting an `awk` result, and widened to the nested inline
>    types (`RetryPolicy`, `ValidationDescriptor`, `SequenceFlow`).
> 4. **T9 was strengthened** to carry `scoped_actions` — the one marshal-only key,
>    and therefore the only part of the round trip that is not trivially symmetric.
>
> **One ADR sentence was false as shipped and is corrected in the ADR:** "only 22
> [of 43 YAML keys] appear anywhere in the repo; 21 never appear at all" described
> the pre-change repo; `allFieldsYAML` now exercises all 43.
>
> **A test added beyond the plan:** `TestREADMEYAMLBlocksParseUnderStrictDecoding`
> parses and builds every ```yaml block in `README.md`, so the README/example
> drift the audit found cannot silently return.

> **For agentic workers:** REQUIRED SUB-SKILL: use
> `superpowers:subagent-driven-development` (recommended) or
> `superpowers:executing-plans`. Steps use `- [ ]` for tracking.

**Goal:** Make both process-definition decoders reject unknown fields, closing
the typo route to a silent authorization bypass (pre-v0.1.0 blocker 1).

**Architecture:** Two call sites in `definition/model`. `ParseYAML` swaps
`yaml.Unmarshal` for a `yaml.Decoder` with `KnownFields(true)`, mapping `io.EOF`
back to today's empty-document behaviour. `ProcessDefinition.UnmarshalJSON`
applies `DisallowUnknownFields()` to its **internal** decode — the only place it
survives, because an outer decoder's setting is discarded by a custom
unmarshaler. No opt-out, no new sentinel, no new package.

**Tech Stack:** Go 1.25, `gopkg.in/yaml.v3`, `encoding/json`, testify.

## Global Constraints

- **TDD strict.** No production code before an observed failing test. A `Write`
  of `foo_test.go` followed by a `Write` of `foo.go` with no `go test` between
  them is a discipline failure, not a shortcut.
- **Judge a test run by its exit code**, never a pipeline:
  `go test ./definition/... > /tmp/def.log 2>&1; echo "EXIT=$?"` then read the
  log. A `| grep | head` tail once reported green here while 14 tests failed.
- `go test -run` on a **nonexistent name exits 0**. Prove a test ran with `-v`
  and `=== RUN`.
- **Black-box tests**: `package model_test`. `definition/model/yaml_test.go` is
  already `package model_test` — verified by `head -1`.
- **Table tests** follow the project `table-test` skill: an `assert` closure per
  case, **not** `want`/`wantErr` fields; `t.Context()` over
  `context.Background()`.
- **Docker, precisely.** The implementation and all of its own tests are
  container-free — `definition/` imports no `dbtest`/`testcontainers` anywhere.
  ⚠ But Task 6 Step 1's repo-wide `go test -race ./...` **does** require a Docker
  daemon (`internal/database/…`, `internal/persistence/store`), and this project
  has a standing **ask-before-using-Docker** rule. Run the container-free proxy
  first and request the full run from the owner:

  ```bash
  go vet ./... && go test ./definition/... ./engine/... ./service/... \
      ./processtest/... ./transport/http/... ./examples/...
  ```

  The audit ran exactly that set with the change applied: EXIT=0.
- Error sentinel prefix convention is `workflow-<pkg>: …`, but **no new sentinel
  is introduced** (ADR D3).
- Coverage floor 85 % on `definition/model`, and it is a floor, not the target.

## File Structure

| file | responsibility | change |
|---|---|---|
| `definition/model/yaml.go` | YAML → `definitionYAML` → core | Modify `ParseYAML` |
| `definition/model/node_wire.go` | JSON ↔ `ProcessDefinition` | Modify `UnmarshalJSON` |
| `definition/model/strict_decoding_test.go` | all new tests | Create |
| `definition/model/testdata/order.yaml` | fixture | Verify only; fix if it carries an unknown key |

No new files beyond the test. No new package.

---

### Task 1: YAML decoder rejects unknown fields

**Files:**
- Modify: `definition/model/yaml.go` — `ParseYAML`
- Test: `definition/model/strict_decoding_test.go` (create)

**Interfaces:**
- Consumes: `model.ParseYAML(r io.Reader, opts ...LoaderOption) (DefinitionLoader, error)` — signature **unchanged**.
- Produces: nothing new. Behaviour change only.

**Baseline — the exact current body:**

```go
func ParseYAML(r io.Reader, opts ...LoaderOption) (DefinitionLoader, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("workflow-definition: read YAML: %w", err)
	}
	var dy definitionYAML
	if err := yaml.Unmarshal(data, &dy); err != nil {
		return nil, fmt.Errorf("workflow-definition: parse YAML: %w", err)
	}
	core, err := coreFromYAML(&dy)
	...
```

- [ ] **Step 1: Write the failing tests**

Create `definition/model/strict_decoding_test.go`:

```go
package model_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/definition/model"
)

const validYAML = `
id: approval-process
version: 1
nodes:
  - id: start
    kind: startEvent
  - id: approve
    kind: userTask
    eligible_roles: ["manager"]
  - id: end
    kind: endEvent
flows:
  - { id: f1, source: start, target: approve }
  - { id: f2, source: approve, target: end }
`

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, ld model.DefinitionLoader, err error)
	}

	cases := []testCase{
		{
			name: "unknown key at top level",
			yaml: validYAML + "bogus_top_level: 42\n",
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_top_level")
			},
		},
		{
			name: "misspelled eligible_roles on a node",
			yaml: strings.Replace(validYAML, "eligible_roles", "eligable_roles", 1),
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
			},
		},
		{
			name: "unknown key inside a flow",
			yaml: strings.Replace(validYAML,
				"{ id: f1, source: start, target: approve }",
				"{ id: f1, source: start, target: approve, bogus_flow_key: 1 }", 1),
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_flow_key")
			},
		},
		{
			name: "every unknown key is reported",
			yaml: strings.Replace(validYAML, "eligible_roles", "eligable_roles", 1) +
				"bogus_top_level: 42\n",
			assert: func(t *testing.T, _ model.DefinitionLoader, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
				assert.Contains(t, err.Error(), "bogus_top_level")
			},
		},
		{
			name: "valid definition still parses",
			yaml: validYAML,
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				require.NotNil(t, ld)
				def, buildErr := ld.Build()
				require.NoError(t, buildErr)
				assert.Equal(t, "approval-process", def.ID)
				assert.Len(t, def.Nodes, 3)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ld, err := model.ParseYAML(strings.NewReader(tc.yaml))
			tc.assert(t, ld, err)
		})
	}
}
```

**Why each case fails today:** cases 1–4 currently return `err=nil` and a usable
loader — measured in spec §2.1/§2.4. Case 5 ("valid definition still parses")
passes today and is a **guard against over-strictness**, not a RED case; it is
labelled so it is not miscounted as falsifiable.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test -v -run '^TestParseYAMLRejectsUnknownFields$' ./definition/model/ > /tmp/t1.log 2>&1; echo "EXIT=$?"
```

Expected: `EXIT=1`, with the four unknown-field subtests failing on
`Error(...)`: an error is expected but got nil. Confirm all four appear as
`=== RUN` in the log — a missing subtest means the name filter silently matched
nothing.

- [ ] **Step 3: Implement**

In `definition/model/yaml.go`, replace the `yaml.Unmarshal` block. Add `"bytes"`
and `"errors"` to the imports (`io` and `fmt` are already imported):

```go
	var dy definitionYAML
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&dy); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("workflow-definition: parse YAML: %w", err)
	}
```

`io.EOF` is deliberately tolerated: `yaml.Unmarshal` returns `nil` for an empty
or comment-only document while `Decode` returns `io.EOF`, so without this the
change would silently also make empty input a parse error (ADR D3a). `dy` keeps
its zero value in that case, exactly as today.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test -v -run '^TestParseYAMLRejectsUnknownFields$' ./definition/model/ > /tmp/t1.log 2>&1; echo "EXIT=$?"
```

Expected: `EXIT=0`, all five subtests `--- PASS`.

- [ ] **Step 5: Verify the whole package still passes**

```bash
go test ./definition/... > /tmp/def.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=0`. **If anything fails here it is a real migration hit** — a
fixture carrying an unknown key (spec §2.7 lists the candidates). Fix the
fixture, do not loosen the decoder.

- [ ] **Step 6: Do not commit yet** — Task 2 is part of the same feature bundle.
  This repo bundles one deliverable feature into one commit (no micro-commits).

---

### Task 2: Empty-document behaviour is pinned

**Files:**
- Test: `definition/model/strict_decoding_test.go` (extend)

**Interfaces:** none new.

This is a **regression guard for Task 1's `io.EOF` branch**, written second
because it cannot go RED before that branch exists.

- [ ] **Step 1: Write the test**

Append to `definition/model/strict_decoding_test.go`:

```go
func TestParseYAMLEmptyDocumentIsNotAnError(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name   string
		yaml   string
		assert func(t *testing.T, ld model.DefinitionLoader, err error)
	}

	cases := []testCase{
		{
			name: "empty string",
			yaml: "",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
		{
			name: "newline only",
			yaml: "\n",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
		{
			name: "comment only",
			yaml: "# nothing here\n",
			assert: func(t *testing.T, ld model.DefinitionLoader, err error) {
				require.NoError(t, err)
				assert.NotNil(t, ld)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ld, err := model.ParseYAML(strings.NewReader(tc.yaml))
			tc.assert(t, ld, err)
		})
	}
}
```

- [ ] **Step 2: Run it — it must PASS immediately**

```bash
go test -v -run '^TestParseYAMLEmptyDocumentIsNotAnError$' ./definition/model/ > /tmp/t2.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=0`. This test is **not** RED-first and must not be reported as
such.

- [ ] **Step 3: Prove it can fail (inverse mutation)**

⚠ **Do NOT delete `&& !errors.Is(err, io.EOF)`.** `errors` is imported solely for
that call, so deleting it leaves `"errors" imported and not used` — a **compile
error**, which `go test` reports as `MUTATED_EXIT=1`. The exit code is
indistinguishable from a real RED, and an engineer following the instruction
literally would record a failure that never happened. This was a CRITICAL audit
finding against an earlier revision of this plan.

Instead **swap the sentinel**, which keeps the import live and the build valid:

```bash
cp definition/model/yaml.go /tmp/yaml.go.snapshot
# edit: !errors.Is(err, io.EOF)  ->  !errors.Is(err, io.ErrUnexpectedEOF)
go test -v -run '^TestParseYAMLEmptyDocumentIsNotAnError$' ./definition/model/ > /tmp/t2m.log 2>&1; echo "MUTATED_EXIT=$?"
grep -q "build failed" /tmp/t2m.log && echo "INVALID MUTATION — build failed, this is NOT a RED"
cp /tmp/yaml.go.snapshot definition/model/yaml.go
diff /tmp/yaml.go.snapshot definition/model/yaml.go && echo "RESTORED CLEAN"
```

Expected `MUTATED_EXIT=1`, **no `build failed` in the log**, and all three
subtests `--- FAIL` on an unexpected `EOF` error. Verified working by the audit.
A mutation that fails to compile is not a RED — check the log, not just the code.

---

### Task 3: JSON decoder rejects unknown fields

**Files:**
- Modify: `definition/model/node_wire.go` — `ProcessDefinition.UnmarshalJSON`
- Test: `definition/model/strict_decoding_test.go` (extend)

**Interfaces:**
- Consumes: `(*ProcessDefinition).UnmarshalJSON(data []byte) error` — signature unchanged.
- Produces: nothing new.

**Baseline — the exact current body:**

```go
func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	if err := json.Unmarshal(data, &dw); err != nil {
		return err
	}
```

Note it returns `err` **unwrapped**. Task 3 preserves that (ADR D3).

- [ ] **Step 1: Write the failing tests**

Append to `definition/model/strict_decoding_test.go`:

```go
func TestProcessDefinitionUnmarshalJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	const validJSON = `{"id":"p","version":1,` +
		`"nodes":[{"id":"a","kind":"userTask","eligible_roles":["manager"]}],"flows":[]}`

	type testCase struct {
		name   string
		json   string
		assert func(t *testing.T, def model.ProcessDefinition, err error)
	}

	cases := []testCase{
		{
			name: "unknown key at top level",
			json: `{"id":"p","version":1,"nodes":[],"flows":[],"bogus_top":9}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_top")
			},
		},
		{
			name: "unknown key inside a node",
			json: `{"id":"p","version":1,` +
				`"nodes":[{"id":"a","kind":"userTask","bogus_node_key":1}],"flows":[]}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "bogus_node_key")
			},
		},
		{
			name: "misspelled eligible_roles is rejected, not silently dropped",
			json: `{"id":"p","version":1,` +
				`"nodes":[{"id":"a","kind":"userTask","eligable_roles":["manager"]}],"flows":[]}`,
			assert: func(t *testing.T, _ model.ProcessDefinition, err error) {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "eligable_roles")
			},
		},
		{
			name: "valid definition still decodes",
			json: validJSON,
			assert: func(t *testing.T, def model.ProcessDefinition, err error) {
				require.NoError(t, err)
				assert.Equal(t, "p", def.ID)
				require.Len(t, def.Nodes, 1)
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var def model.ProcessDefinition
			err := def.UnmarshalJSON([]byte(tc.json))
			tc.assert(t, def, err)
		})
	}
}
```

**Why each fails today:** measured in spec §2.2 — all three unknown-field cases
return `err=<nil>` today, including `totally_unknown_field`.

- [ ] **Step 2: Run to verify they fail**

```bash
go test -v -run '^TestProcessDefinitionUnmarshalJSONRejectsUnknownFields$' ./definition/model/ > /tmp/t3.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=1`, three subtests failing on a nil error; the fourth passes.

- [ ] **Step 3: Implement**

In `definition/model/node_wire.go`, replace the inner unmarshal. Add `"bytes"`
to the imports (`encoding/json` is already imported):

```go
func (d *ProcessDefinition) UnmarshalJSON(data []byte) error {
	var dw definitionWire
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&dw); err != nil {
		return err
	}
```

The strictness must be applied **here**, inside the custom unmarshaler: an outer
`json.Decoder` with `DisallowUnknownFields()` is discarded by this method,
measured in spec §2.3. The error stays unwrapped, as today.

- [ ] **Step 4: Run to verify they pass**

```bash
go test -v -run '^TestProcessDefinitionUnmarshalJSONRejectsUnknownFields$' ./definition/model/ > /tmp/t3.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=0`, all four `--- PASS`.

- [ ] **Step 5: Full package**

```bash
go test ./definition/... > /tmp/def.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=0`.

---

### Task 4: Pin what strictness does NOT catch

**Files:**
- Test: `definition/model/strict_decoding_test.go` (extend)

Spec §2.6: `nodeYAML` / `NodeWire` are flat unions, so a kind-inappropriate
field is a *known* field and stays legal. This test pins that as deliberate, so
a future reader does not "fix" it as a bug — and so the ADR's limitation claim
is executable rather than asserted.

- [ ] **Step 1: Write the test**

```go
func TestStrictDecodingDoesNotRejectKindInappropriateFields(t *testing.T) {
	t.Parallel()

	// timer_duration belongs to timer nodes, but NodeWire/nodeYAML are flat
	// unions over all node kinds, so it is a KNOWN field and survives strict
	// decoding on a userTask. ADR-0167 records this limitation deliberately.
	yamlSrc := strings.Replace(validYAML,
		`    eligible_roles: ["manager"]`,
		"    eligible_roles: [\"manager\"]\n    timer_duration: \"PT5M\"", 1)

	ld, err := model.ParseYAML(strings.NewReader(yamlSrc))
	require.NoError(t, err)
	def, err := ld.Build()
	require.NoError(t, err)
	assert.Equal(t, "approval-process", def.ID)
}
```

- [ ] **Step 2: Run it**

```bash
go test -v -run '^TestStrictDecodingDoesNotRejectKindInappropriateFields$' ./definition/model/ > /tmp/t4.log 2>&1; echo "EXIT=$?"
```

Expected `EXIT=0`. If it FAILS, the union assumption in spec §2.6 is wrong and
**the ADR must be amended** before proceeding — do not delete the test.

---

### Task 5: Mutation verification of the RED-first tests

Every assertion claimed as falsifiable must be shown to actually fail.

- [ ] **Step 1: Snapshot both files**

```bash
cp definition/model/yaml.go /tmp/yaml.go.snap
cp definition/model/node_wire.go /tmp/node_wire.go.snap
```

- [ ] **Step 2: Mutate YAML strictness off**

Change `dec.KnownFields(true)` to `dec.KnownFields(false)`.

```bash
go test -v -run '^TestParseYAMLRejectsUnknownFields$' ./definition/model/ > /tmp/m1.log 2>&1; echo "MUTATED_EXIT=$?"
```

Expected `MUTATED_EXIT=1`, the four unknown-field subtests RED, the
"valid definition still parses" subtest still green (proving the mutation
discriminates rather than breaking everything).

- [ ] **Step 3: Restore and confirm**

```bash
cp /tmp/yaml.go.snap definition/model/yaml.go
diff /tmp/yaml.go.snap definition/model/yaml.go && echo "RESTORED CLEAN"
```

- [ ] **Step 4: Mutate JSON strictness off**

Delete the `dec.DisallowUnknownFields()` line.

```bash
go test -v -run '^TestProcessDefinitionUnmarshalJSONRejectsUnknownFields$' ./definition/model/ > /tmp/m2.log 2>&1; echo "MUTATED_EXIT=$?"
```

Expected `MUTATED_EXIT=1`, three subtests RED, the valid one green.

- [ ] **Step 5: Restore and confirm**

```bash
cp /tmp/node_wire.go.snap definition/model/node_wire.go
diff /tmp/node_wire.go.snap definition/model/node_wire.go && echo "RESTORED CLEAN"
```

- [ ] **Step 6: Record the mutation table** in this plan's `▶ Progress` block:
  which mutation, which tests went RED, which stayed green, and that the build
  compiled in each case. A mutation that fails to compile is not a RED.

---

### Task 6: Verification and delivery

- [ ] **Step 1: Full suite by exit code**

```bash
go test -race -coverprofile=cover.out ./... > /tmp/all.log 2>&1; echo "EXIT=$?"
scripts/coverage.sh cover.out
```

Expected `EXIT=0`; `definition/model` ≥ 85 %; no repo-wide regression from the
73.8 % baseline.

- [ ] **Step 2: `go vet` — compiles every test file, including Docker-gated ones**

```bash
go vet ./... > /tmp/vet.log 2>&1; echo "EXIT=$?"
```

This is the cheap proof that no hidden consumer of the decoders breaks.

- [ ] **Step 3: Lint**

```bash
golangci-lint run ./... > /tmp/lint.log 2>&1; echo "EXIT=$?"
```

- [ ] **Step 4: Confirm every §2.7 definition still parses** — `definition/model/testdata/order.yaml`, `definition/example_test.go`, `definition/model/yaml_test.go`, `definition/model/validation_wire_test.go`, `definition/build/build_test.go`, `examples/readme_quickstart/main.go`. Steps 1–2 cover these; name any that needed a fixture fix in the `▶ Progress` block.

- [ ] **Step 5: Documents describe what shipped** — re-read spec, ADR-0167 and
  this plan against the built code. Correct every divergence, especially any
  behaviour the ADR promises that implementation changed. Sweep the diff's own
  comments for unexecuted claims and over-reaching quantifiers.

- [ ] **Step 6: Commit as ONE feature bundle**

⚠ The last two breaking bundles (ADR-0165 `ec25ffd`, ADR-0166 `c009fd3`) both
carried `CHANGELOG.md` **and** `docs/plans/HANDOVER.md`, and rule #10 requires
the handover to ride in the feature-bundle commit. `CHANGELOG.md` has a live
"### Breaking changes (pre-v0.1.0 — no stability promise)" section and this is a
`feat(definition)!:` change. Both were missing from an earlier revision of this
step.

```bash
git add definition/model/ README.md CHANGELOG.md docs/plans/HANDOVER.md \
        docs/specs/2026-08-07-strict-definition-decoding.md \
        docs/adr/0167-definition-decoding-rejects-unknown-fields.md \
        docs/plans/2026-08-07-strict-definition-decoding.md
git commit -m "feat(definition)!: reject unknown fields when decoding definitions (ADR-0167)"
```

- [ ] **Step 7: Delivery Gate** — `/code-review` then `/security-review`
  (owner-invoked only). Fix all findings and fold them via `git commit --amend`;
  never stack fixup commits. Re-run the suite after any fix, on the merged tree.
  Adjudicate any finding you reject, with the reason stated.

---

### Task 7 (audit-added): JSON trailing-data rejection must survive

`json.Unmarshal` validates the whole input; `Decoder.Decode` reads one value and
stops. Without this the delivery *loosens* a check. Measured baseline vs patched
in ADR "The JSON decoder must not lose trailing-data rejection".

- [ ] **Step 1:** add a RED case to Task 3's table — input
  `{"id":"p","version":1,"nodes":[],"flows":[]} trailing garbage`, asserting an
  error. It is RED against the *patched-without-this-fix* build, and green on
  today's baseline, so run it **after** Task 3 Step 3 and confirm it fails there.
- [ ] **Step 2:** implement — after `dec.Decode(&dw)`, confirm nothing follows:

```go
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("workflow-definition: unexpected trailing data after definition")
	}
```

  Add `"errors"`, `"io"` and `"fmt"` to `node_wire.go`'s imports as needed —
  **verify which are already present before editing**, do not assume.
- [ ] **Step 3:** re-run Task 3's whole table; all cases green.

### Task 8 (audit-added): the over-strictness guard must enumerate every tag

Spec T8 is the only defence against a missing `yaml:"…"` tag turning a legitimate
definition into a hard parse error. `nodeYAML` + `definitionYAML` declare **43**
distinct keys; the `validYAML` fixture exercises **6**, and **21 appear nowhere
in the repo**.

- [ ] **Step 1:** build an `allFieldsYAML` fixture enumerating every YAML tag
  across the node kinds that accept them. Derive the list mechanically, do not
  hand-copy:

```bash
awk '/^type (nodeYAML|definitionYAML) struct/,/^}/' definition/model/yaml.go \
  | grep -o 'yaml:"[a-z_]*' | sed 's/yaml:"//' | sort -u
```

- [ ] **Step 2:** assert it parses with `require.NoError` and builds. This test
  passes today and is a **regression guard**, not a RED case.
- [ ] **Step 3:** mutation — delete one `yaml:"…"` tag from `nodeYAML`, confirm
  the fixture goes RED, restore, `diff`.

### Task 9 (audit-added): persisted definitions still load

`DefinitionStore.GetDefinition`/`Lookup` decode stored blobs through the strict
`UnmarshalJSON`. Container-free proxy for that path:

- [ ] **Step 1:** round-trip a multi-kind definition —
  YAML → `Build()` → `json.Marshal` → `UnmarshalJSON` — asserting `NoError` and
  that node count and kinds survive. Regression guard, not a RED case.

### Task 10 (audit-added): fix `README.md`

- [ ] **Step 1:** correct the camelCase keys at `README.md:144,864,865,868` to
  the declared snake_case tags (`compensate_action`, `retry_policy`,
  `eligible_roles`, and the nested `retryPolicy` sub-keys).
- [ ] **Step 2:** confirm each README YAML block parses under the new decoder.
  ⚠ Without this the repo's own quickstart stops parsing the moment this ships.

## Verification Checklist

- [ ] `go test ./definition/...` EXIT=0
- [ ] `go test -race ./...` EXIT=0, 0 races
- [ ] `definition/model` coverage ≥ 85 %
- [ ] `go vet ./...` EXIT=0
- [ ] `golangci-lint run ./...` EXIT=0
- [ ] All RED-first tests observed RED before implementation
- [ ] All mutations observed RED, restored, and `diff`-confirmed clean
- [ ] T10/Task 2 explicitly labelled a regression guard, not a RED case
- [ ] Spec, ADR and plan match what shipped
- [ ] `/code-review` findings: all fixed or adjudicated
- [ ] `/security-review` findings: all fixed or adjudicated

## Self-Review

- **Spec coverage:** D1 → Tasks 1 and 3. D2 (no opt-out) → nothing to build; no
  option is added, and the absence is visible in the diff. D3 (error shapes) →
  Task 3 Step 3 preserves the unwrapped return. D3a (empty input) → Task 2.
  D4 (asymmetry) → documented only, no code. §2.6 limitation → Task 4.
  §2.7 migration → Task 6 Step 4.
- **Placeholders:** none — every step carries runnable code or a real command.
- **Type consistency:** no new types or signatures are introduced anywhere in
  this plan, so there is nothing to drift.
- **Known gap:** Task 4 asserts a *limitation*. If it fails, the ADR is wrong and
  must be amended rather than the test deleted — stated in the task.
