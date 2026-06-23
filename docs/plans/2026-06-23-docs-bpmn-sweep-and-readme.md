# Docs: BPMN-Claim Sweep + README (FOLLOWUPS ⑤ & ⑥) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** (⑤) Remove BPMN *compatibility claims* from Go doc comments while keeping BPMN-derived *domain vocabulary*; (⑥) write a complete, correct README for both library consumers and maintainers.

**Architecture:** Two doc-only tasks. The sweep edits comment text only — no code, no behavior, no tests. The README is a new top-level file documenting the final public surface (after sub-projects 1–3 land). No ADR (docs-only).

**Tech Stack:** Markdown + Go doc comments. Verified via `go build ./...` and `golangci-lint run ./...` (godoc/comment lints) — no test changes.

## Global Constraints

- Go 1.25.7; module `github.com/zakyalvan/krtlwrkflw`; PostgreSQL 17; flat root layout (ADR-0004).
- ⑤ scope is **Go code doc comments only**. `REQUIREMENTS.md` and `CLAUDE.md` deliberately document the BPMN inspiration as design intent and are **out of scope** — do not edit them.
- Keep domain vocabulary: "gateway", "sequence flow", "compensation", "boundary event", "error code", "node". Remove only phrasing that asserts conformance/compatibility ("BPMN-flavored", "the BPMN default", "implements BPMN error propagation", "per the BPMN spec").
- Conventional Commits `docs(model)` / `docs(readme)`. Ask before committing.
- This sub-project executes **last** — the README must reflect the post-1/2/3 API (front-door `doc.go`, 14 public packages, `model.NewDefinition`/constructors/`ParseYAML`, `runtime.InstanceSnapshot`/`ActionableView`).

## BPMN mention inventory (from code audit)

29 mentions across 11 Go files. Classification: **1 compatibility claim**, the rest vocabulary/attribution. Targets to reword:

- **`model/definition.go:1`** (the one clear claim): `// Package model defines the in-memory BPMN-flavored process-definition types.` → reword (Task 1, Step 1).
- **Soften (optional, assertive spec-reference phrasing), keeping the concept:**
  - `engine/step.go:1825` `// propagateError implements BPMN error propagation for a thrown errorCode.`
  - `model/definition.go:139`, `engine/state.go:93,123`, `model/definition_test.go:108`, `model/validate_test.go:251` — occurrences of "the BPMN default" → "the default (interrupting)".
- **Keep as-is** (pure vocabulary): all "BPMN node id/ID", "BPMN error code", "BPMN compensation boundary/throw event" mentions in `engine/state.go`, `engine/command.go`, `engine/trigger.go`, `model/validate.go`, `humantask/humantask.go`, `model/nodekind_json.go` ("BPMN2 lowerCamelCase convention" is a naming-convention attribution — keep). Note: `model/definition.go` lines/wording shift after sub-project 2 rewrites that file; re-grep before editing (Task 1, Step 1).

## File map

| Path | Action | Responsibility |
|---|---|---|
| `model/node.go` / `model/definition.go` | Modify | Reword the package doc + soften "the BPMN default" phrasings. |
| `engine/step.go` | Modify | Soften the `propagateError` comment. |
| `README.md` | Create | Full project documentation. |

---

### Task 1: BPMN compatibility-claim sweep (Go doc comments)

**Files:** Modify the package doc comment for `model` (in whichever file holds it after sub-project 2 — likely `model/node.go` or `model/definition.go`) and the soften-targets above.

**Interfaces:** none (doc-only).

- [ ] **Step 1: Re-grep to get current line numbers**

Run: `grep -rin "bpmn" --include="*.go" . | grep -v "_test.go"` and `grep -rin "bpmn" --include="*_test.go" .`
Expected: the up-to-date list (sub-project 2 will have moved `model/definition.go` content). Use this to locate the exact edit sites.

- [ ] **Step 2: Reword the `model` package doc (the compatibility claim)**

Change the package comment from:
```go
// Package model defines the in-memory BPMN-flavored process-definition types.
// It is pure data plus validation; it imports only the standard library.
```
to:
```go
// Package model defines the in-memory process-definition types: nodes,
// gateways, sequence flows, and the ProcessDefinition template. The concepts
// are inspired by BPMN, but this is NOT a BPMN-compatible implementation and
// does not aim to load or round-trip arbitrary BPMN2 documents. It is pure data
// plus validation; it imports only the standard library.
```

- [ ] **Step 3: Soften the assertive spec-reference phrasings**

- `engine/step.go` `propagateError` comment → `// propagateError propagates a thrown errorCode to the nearest matching boundary error handler (BPMN-style error propagation).`
- Every `// ... the BPMN default` → `// ... the default (interrupting)` (keeps meaning; drops the conformance implication). Apply in `engine/state.go` (2 sites), `model` (1), and the 2 test files.
- Leave all "BPMN node ID", "BPMN error code", "BPMN compensation … event" vocabulary untouched.

- [ ] **Step 4: Verify nothing broke**

Run: `go build ./... && golangci-lint run ./model/... ./engine/...`
Expected: build clean; no godoc/comment lint findings. (No tests change; the test-file comment edits are cosmetic.)

- [ ] **Step 5: Confirm the claim is gone**

Run: `grep -rin "bpmn-flavored\|fully bpmn\|bpmn compatible\|bpmn-compatible" --include="*.go" . || echo CLEAN`
Expected: `CLEAN`.

- [ ] **Step 6: Commit** `docs(model,engine): drop BPMN compatibility claims, keep domain vocabulary (FOLLOWUPS ⑤)`.

---

### Task 2: Write `README.md`

**Files:** Create `README.md` at the repo root.

**Interfaces:** none (doc-only). Content must reference the real, post-1/2/3 API.

- [ ] **Step 1: Gather the known-correct usage snippets**

Read these Example tests for accurate, compiling usage to adapt into the README (do NOT invent API): `runtime/caching_store_example_test.go`, `runtime/observability_example_test.go`, `eventing/eventing_example_test.go`. Read the root `doc.go` (from sub-project 1) for the package map and the elevator framing.

- [ ] **Step 2: Write the README with these sections (each must be concrete, no TODOs):**

1. **Title + one-line tagline** — "wrkflw — an embeddable Go workflow engine (library, not a daemon)."
2. **What it is** — library-first: a single importable module (`github.com/zakyalvan/krtlwrkflw`) a consumer embeds in their own app; transports (REST/gRPC) are *mountable*, not shipped binaries; no owned `main`. Token-based execution over process definitions; concepts inspired by BPMN but not BPMN-compatible. (Pull the elevator pitch from REQUIREMENTS.md §1.)
3. **Requirements** — Go 1.25, PostgreSQL 17, Docker (only for running the testcontainers-based tests).
4. **Install** — `go get github.com/zakyalvan/krtlwrkflw`.
5. **Quickstart** — two sub-sections:
   - *Define a process* — a compiling snippet using the sub-project-2 API: `model.NewDefinition("order",1).Add(model.NewStartEvent("s")).Add(model.NewServiceTask("charge","charge-card", model.WithCompensation("refund-card"))).Add(model.NewEndEvent("e")).Connect("s","charge").Connect("charge","e").Build()`.
   - *Or author in YAML* — show the `testdata/order.yaml` shape and `model.ParseYAML(data)`.
   - *Run it* — adapt the runner construction + drive loop from `runtime/caching_store_example_test.go` (use the actual constructor/Run signatures found there; do not fabricate).
6. **Authoring forms** — Go builders/constructors (preferred), YAML loader, BPMN2 XML (loadable, not the preferred form).
7. **Package map** — a table of the 14 public packages (mirror `doc.go`): `model`, `runtime`, `engine`, `action`, `humantask`, `authz`, `casbinauthz`, `transport`, `persistence`, `eventing`, `scheduling`, `observability`, `clock`, `service`. One line each. Note `internal/` is off-limits to consumers.
8. **Mounting transports** — how a consumer mounts the REST `http.Handler` / gRPC `ServiceRegistrar` from `transport/rest` and `transport/grpc` in their own server.
9. **Reading instance state** — the serialization contract from sub-project 3: `runtime.NewInstanceSnapshot` (full) and `runtime.NewActionableView` (open human tasks + allowed next actions), and the REST `/instances/{id}/snapshot` + `/actionable` endpoints — for front-ends rendering process history and driving human tasks.
10. **Authorization** — pluggable `authz.Authorizer` (role / resource / attribute-based), casbin baseline via `casbinauthz`.
11. **Scheduling & waits** — timers, SLA deadlines, in-wait reminder actions (gocron behind `scheduling`).
12. **Compensation, resilience, observability** — per-node compensation actions; retry/recovery/DLQ; metrics, traces, `slog` via `observability`.
13. **Testing** — `go test ./...`; testcontainers needs Docker; the shared `internal/database.RunTestDatabase` helper (note it moved to `internal/` in sub-project 1).
14. **For maintainers** — repo layout (single module, root packages = product, `internal/` = impl, `examples/` = reference wiring), ADRs in `docs/adr/` (Nygard), specs in `docs/specs/`, plans in `docs/plans/`, TDD discipline, the locked tech stack.
15. **License** — state the project license (confirm from an existing `LICENSE` file; if none, mark "License: TBD by owner" and flag it — do not invent one).

- [ ] **Step 3: Verify the quickstart compiles**

Extract the README's Go quickstart into a scratch `_test.go` (or an `examples/` file) and run `go build` / `go vet` on it to confirm it compiles against the real API. Fix the README until it does. (Optional but strongly recommended — a README snippet that doesn't compile is a defect.)

- [ ] **Step 4: Lint the prose**

Run: `golangci-lint run ./...` (ensures no doc references broke anything) and a markdown link check by eye.
Expected: clean.

- [ ] **Step 5: Commit** `docs(readme): comprehensive project README (FOLLOWUPS ⑥)`.

---

## Verification checklist (whole sub-project)

- [ ] `grep -rin "bpmn-flavored\|bpmn compatible\|fully bpmn" --include="*.go" .` returns nothing.
- [ ] Domain vocabulary ("gateway", "compensation", "boundary event", "BPMN node id" attributions) is intact — only conformance claims were removed.
- [ ] `REQUIREMENTS.md` and `CLAUDE.md` are unedited.
- [ ] `README.md` exists, covers all 15 sections, and its quickstart compiles against the real API.
- [ ] `go build ./...` clean; `golangci-lint run ./...` clean.

## Dependency note

Executes **last**: the README documents the front-door `doc.go` (sub-project 1), the authoring API (sub-project 2), and the serialization DTOs (sub-project 3). Run only after those merge.

## Out of scope

- Editing `REQUIREMENTS.md`/`CLAUDE.md` (intentional design-intent docs).
- Renaming BPMN-derived concept names (vocabulary stays).
- A `docs/` site or `llms.txt` (not requested).
