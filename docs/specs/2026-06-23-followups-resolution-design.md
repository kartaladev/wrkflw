# FOLLOWUPS.md Resolution — Design & Decisions

- Status: Approved (brainstorming), implementation deferred
- Date: 2026-06-23
- Source: `FOLLOWUPS.md` (uncommitted discussion doc)
- Method: senior-architect devil's-advocate review of each item against the
  existing codebase and ADRs, then collaborative decision per item.

## Purpose

`FOLLOWUPS.md` is a discussion doc that bundles six follow-up items of very
different risk classes under one flat list. This spec records the analysis,
the **decisions** (including two outright rejections), and a dependency-ordered
**decomposition** into sub-projects. Each sub-project gets its own
spec → plan → implement cycle; this document is the umbrella that explains the
*why*, especially the rejections.

## Evidence gathered during review

- The repo already implements the **façade pattern**: `internal/` holds the
  concrete impls (`authz`, `eventing`, `observability`, `persistence`,
  `scheduling`); the matching root packages are explicit "consumer-facing
  façades." Plumbing is therefore **already hidden** — the root is not leaking
  implementation, with one exception (below).
- `database/` is a single `testutils.go` importing **testcontainers**, used
  only by in-module test code (`casbinauthz`, `internal/*`, `persistence`). It
  is test plumbing parked in the public API surface — the one genuine leak.
- `expreval` exposes only `New()` + `EvalBool/EvalDuration/EvalString(code,
  env)` — a fixed evaluator the engine drives with process variables. **No**
  consumer extension surface (no custom-function registration, no options).
- There is **no `ProcessInstance` type**; runtime execution state is
  `engine.InstanceState` (~25 fields), of which only ~1/3 are consumer-relevant.
  The rest is engine bookkeeping, much of it **unexported types**
  (`timerRecord`, `armedEvent`, `compensationCursor`) that will not marshal.
- `model.Node` is a **35-field god-struct**; most fields are valid for only one
  `NodeKind`. `model/nodekind_json.go` already maps `NodeKind` ↔ string.
- `model.ProcessDefinition` **is** persisted: `internal/persistence/postgres/
  definitions.go:56` does `json.Marshal(def)` into a JSONB column and
  `json.Unmarshal` back. So the definition (and its `[]Node`) round-trips
  through JSON.
- Engine/runtime read `model.Node` fields at **131 sites** (22 `.Kind` +
  109 field reads) concentrated in 4 production files: `engine/step.go`,
  `runtime/runner.go`, `runtime/timerops.go`, `service/service.go`.

## Decisions (per item)

### ① "Introduce `pkg/`; root only workflow code" — **Rejected as written**

The literal `pkg/` move collides head-on with **ADR-0004** (flat root layout,
an explicit owner decision rejecting `pkg/`). Reversing it would need a new
superseding ADR, would **break every moved package's import path** (the precise
harm library-first exists to prevent), and `pkg/X` is *still public* — so it
would not even encapsulate anything Go-semantically. The façade + `internal/`
split already solves ~80% of the stated pain (noise, public/private leak,
unclear entry points).

**Decision — surgical reorg instead:**

1. Move `database/` → `internal/database` (test helper; all importers in-module).
   Removes testcontainers from any consumer's import graph.
2. Move `expreval/` → `internal/expreval` (no consumer extension surface;
   `internal/` remains importable by `engine`/`authz` in the same module).
3. Keep every other root package flat — they are legitimate public façades/core.
4. Add a root `doc.go` "start here" overview; the README (⑥) reinforces it.
5. New ADR: evict test/internal-only packages from the public root; **reaffirm**
   the flat layout (ADR-0004); **reject** `pkg/`.

Net: 16 → 14 public packages, the one real leak gone, "clutter" reframed as a
navigation/docs problem (front door), no breaking import paths for public core.

### ② "Make `model.Node` an interface" + DSL/YAML — **Accepted (full interface, eyes open)**

The smell is real (35-field god-struct). The cost was quantified and accepted:

- **Engine migration:** 131 field-read sites across 4 production files (incl. the
  core state machine `engine/step.go`) convert from `node.Kind` switches + field
  reads to type switches/assertions. Behavior-preserving, TDD-guarded.
- **Discriminated JSON:** `json.Unmarshal` cannot decode into an interface, so a
  hand-written `UnmarshalJSON` with a `kind` discriminator is required for
  `ProcessDefinition` to keep round-tripping through the JSONB `DefinitionStore`.
  Ongoing tax: every future node kind must be registered in the unmarshaller
  (and the YAML loader).

A lower-cost alternative (typed authoring layer that *lowers* to a flat runtime
`Node`) was offered and **declined** in favor of full purity, since for this
library the model *is* the product.

**Decision — Variant A, full interface:**

- `model.Node` becomes an interface (minimal method set, at least
  `Kind() NodeKind` and node identity; common accessors as needed).
- One concrete type per kind (`StartEvent`, `EndEvent`, `TerminateEndEvent`,
  `ErrorEndEvent`, `ServiceTask`, `UserTask`, `ReceiveTask`, `SendTask`,
  `BusinessRuleTask`, `SubProcess`, `CallActivity`, `EventSubProcess`,
  `IntermediateCatchEvent`, `IntermediateThrowEvent`, `BoundaryEvent`,
  `ExclusiveGateway`, `ParallelGateway`, `InclusiveGateway`,
  `EventBasedGateway`), each carrying **only** its valid fields.
- A per-kind **constructor** for each (functional options for optional fields:
  retry, recovery flow, compensation, SLA, reminders, correlation, etc.).
- `ProcessDefinition.Nodes []Node` (interface slice).
- Custom discriminated `MarshalJSON`/`UnmarshalJSON` (or a slice-wrapper) so the
  JSONB `DefinitionStore` round-trip is preserved.
- A fluent `DefinitionBuilder` (add nodes, `Connect(from,to,cond)`, build →
  validated `ProcessDefinition`).
- A **YAML loader** decoding into the same concrete types via the same `kind`
  discriminator — one validation path, three authoring front-ends (Go builders,
  YAML, existing XML).
- Per-kind validation: today's `model/validate.go` switch becomes per-type
  validation.
- New ADR for the Node-as-interface model.

### ③ "Serialize ProcessInstance to JSON" — **Accepted; dedicated DTO + mapper**

Must **not** mean `json.Marshal(engine.InstanceState)`: that leaks engine
internals into a public wire contract (every engine change breaks the FE) and
silently drops unexported fields.

**Decision — public view contract + explicit mapper:**

- New public DTO type(s) — a stable contract decoupled from `InstanceState`:
  - **Full snapshot:** all consumer-relevant fields (status, variables, tokens,
    history, tasks, incidents, timing); internal bookkeeping excluded or
    summarized, never raw cursors/counters.
  - **Curated "actionable" view:** status + each open human task + its allowed
    next actions (derived from the task node's outgoing flows + their
    conditions/labels in the definition).
- An explicit mapper `InstanceState (+ ProcessDefinition) → DTO`. The curated
  view is why the definition is needed, not just the state.
- Honors the existing "API response customization / v1 compatibility"
  requirement; keeps the FE contract stable across engine refactors.
- New ADR for the instance DTO/view contract.

### ④ "Rename `engine` → `exec`" — **Dropped**

Breaking change to a public package, `exec` is *less* descriptive (collides with
`os/exec`; `engine` accurately names the token state machine), it would compound
②'s already-large engine migration, and it solves **no stated problem**. The
"where's the entry point" goal is met by ①'s front-door `doc.go` + README
without breaking any consumer.

### ⑤ "Remove BPMN wording from Go docs" — **Accepted, scoped**

29 mentions across 11 files. Target is **dropping compatibility claims**, not
renaming domain vocabulary — "gateway," "compensation," "sequence flow,"
"boundary event" remain the clearest names. Keep the concepts; remove any
implication of full BPMN compatibility. Doc-only, no ADR.

### ⑥ "Write README" — **Accepted, last**

Depends on ①/②/③/⑤ settling (it documents the final package map, the new
authoring API, and the serialization contract). Doc-only, no ADR.

## Decomposition & sequencing

Too large for one implementation plan. Split dependency-first; each is its own
spec → plan → implement cycle:

1. **Layout hygiene (①)** — move `database` + `expreval` to `internal/`, add
   root `doc.go`, ADR reaffirming flat layout. Low risk; derisks the repo map
   before the README. *First.*
1.5. **engine/step.go decomposition** — a late addition (not a FOLLOWUPS.md
   item; full design in `docs/specs/2026-06-23-engine-step-decomposition-design.md`,
   ADR-0044). Pure refactor of the 3251-line god file into a node-kind strategy
   registry + extracted trigger handlers + collaborator files. Inserted **between
   ① and ②** so ②'s engine migration edits small per-kind files, not the monolith.
   *Second.*
2. **Node model redesign (②)** — the big, risky thread; foundational. Its plan
   phases as: interface + concrete types → discriminated JSON → engine/runtime
   migration (131 sites) → `DefinitionBuilder` → YAML loader → per-kind
   validation. Gates ③ and ⑤. *Second.*
3. **Instance serialization DTO (③)** — public DTO (full + curated) + mapper.
   Loosely depends on ② (reads node kinds/flows). *Third.*
4. **Docs (⑤ + ⑥)** — BPMN-claim sweep, then README. *Last, after the surface
   stabilizes.*

### ADRs to author

- ① — reaffirm flat layout, reject `pkg/`, evict test/internal-only packages.
- ② — Node-as-interface process-definition model.
- ③ — instance DTO/view serialization contract.
- ⑤/⑥ — none (doc-only).

## Out of scope / explicitly not doing

- Introducing a `pkg/` directory (①, rejected).
- Renaming `engine` (④, dropped).
- Turning the flat `Node` into a typed *authoring-only* layer that lowers to a
  flat runtime node (the lower-cost ② alternative was declined).
- Renaming BPMN-derived domain vocabulary (⑤ is claim-removal only).

## Verification (per sub-project, at implementation time)

Standard project gates apply to each sub-project: TDD red→green per new symbol,
touched-package coverage ≥ 85%, `go test ./...` clean, `golangci-lint run ./...`
clean, and an ADR recorded before/with the change where listed above.
