# Engine Simplification — Phase B (Arm-Model Unification) Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.
> Spec: `docs/specs/2026-07-13-engine-simplification-program.md` (Phase B section).

**Goal:** Kill the deepest structural duplication in the engine — the three near-identical "arm"
families (`armedEvent`, `boundaryArm`, `eventTriggeredSubprocessArm`) and their ~13 cloned
scan/remove methods — by extracting a shared embedded `triggerMatch` and generic scan helpers,
**without changing the persisted wire format** and without regression.

**Architecture / key insight:** `InstanceState` is persisted via plain `json.Marshal`
(`internal/persistence/store/store_core.go:77,219`). Go promotes the fields of an **anonymous
embedded struct** to the parent JSON object. Therefore embedding a shared
`triggerMatch{TimerID, Signal, Message, MessageKey string}` into each arm type keeps every arm's
serialized JSON shape **byte-identical** (the four fields stay at the same top level; nothing is
nested or added). This makes the unification **wire-safe** — proven by a round-trip parity test,
not a breaking migration. This refines the umbrella spec, which assumed a wire break: we do NOT
collapse the three types into one fat union (that would carry every owner field on every arm and
read worse); instead each type keeps only its own owner/routing fields plus the shared embed.

**Tech Stack:** Go 1.25 (generics), standard library. No new deps.

## Global Constraints

- Go 1.25; module `github.com/kartaladev/wrkflw`.
- **No regression is a hard requirement.** After every task: `go test ./...` green from repo
  root (incl. testcontainers persistence), `golangci-lint run ./...` clean, `engine` coverage
  ≥85%.
- **Wire-format parity is the central gate.** The persisted JSON of `InstanceState` — including
  `ArmedEvents`, `Boundaries`, `EventTriggeredSubprocesses` — MUST remain byte-identical in field
  names/nesting. A hand-authored OLD-shape JSON snapshot for each arm type MUST unmarshal
  correctly into the new-code `InstanceState` (parity test). The persistence round-trip suite
  (`go test ./persistence/... ./internal/persistence/...`) MUST stay green.
- Engine core stays pure (hardened `purity_test.go`); no wall-clock, no denied vendor. `context`
  and `log/slog` remain allowed (added in Phase C).
- B1/B2/B3 are behavior-preserving: existing arm/boundary/event-sub/gateway tests are the oracle;
  green before AND after; add NO new test except the wire-parity test (B1) and any generic-helper
  unit test. Do not edit existing tests except mechanical references.
- `go doc ./engine` must stay identical (all arm types + methods are unexported): diff vs the
  snapshot captured at task start; empty.
- Error sentinels keep the `workflow-engine:` prefix. One commit per task; Conventional Commits
  scoped `refactor(engine):` / `docs(adr):`; `Co-Authored-By: Claude Opus 4.8 (1M context)
  <noreply@anthropic.com>` trailer.

---

### Task B0: ADR for the arm-model unification

**Files:** Create `docs/adr/0131-arm-model-unification.md` (Nygard: Status/Date, Context,
Decision, Consequences).

- [ ] **Step 1:** Write the ADR recording: the three arm families share a trigger-correlation
  quartet; we extract a shared **embedded** `triggerMatch` (wire-safe via JSON field promotion —
  cite `store_core.go` json.Marshal) and unify the ~13 duplicated scan/remove accessors behind
  generic helpers; we explicitly REJECT the fat single-type union (worse cognitive load; every
  owner field on every arm). Note the parity round-trip test as the wire safeguard and that NO
  migration is required because the serialized shape is unchanged.
- [ ] **Step 2: Commit** `docs(adr): arm-model unification via embedded triggerMatch (ADR-0131)`.

---

### Task B1: Extract embedded `triggerMatch` (wire-safe, parity-gated)

**Files:** Modify `engine/state_arms.go`; add a wire-parity test `engine/state_arms_wire_test.go`;
possibly touch `engine/step_state.go` (`cloneState`) if it lists arm fields.

**Interfaces:**
- Produces: `type triggerMatch struct { TimerID, Signal, Message, MessageKey string }` embedded
  ANONYMOUSLY in `armedEvent`, `boundaryArm`, `eventTriggeredSubprocessArm`. Each arm type LOSES
  its own `TimerID`/`Signal`/`Message`/`MessageKey` field declarations (now provided by the
  embed) and KEEPS its owner/routing fields (`armedEvent`: GatewayToken/CatchNode/Flow;
  `boundaryArm`: HostToken/HostNode/BoundaryNode/Flow/NonInterrupting/Action;
  `eventTriggeredSubprocessArm`: EnclosingScopeID/EventSubprocessNode/NonInterrupting). Field
  ACCESS (`arm.TimerID`, `arm.Signal`, …) continues to work via Go field promotion — call sites
  need NO change.

- [ ] **Step 1: Capture doc baseline + confirm green.** `go doc ./engine > /tmp/engine-doc-b.txt`;
  `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Write the WIRE-PARITY test FIRST** (`engine/state_arms_wire_test.go`, black-box
  `engine_test` using `export_test.go` seams if needed): for each arm type, hand-author the
  EXACT current JSON (marshal a fully-populated arm value of the CURRENT type and copy the JSON
  string into the test as the golden fixture BEFORE the refactor). Assert: (a) `json.Unmarshal`
  of that golden fixture into the arm type yields the fully-populated value (all fields incl. the
  promoted trigger fields); (b) `json.Marshal` of a populated arm value equals the golden fixture
  (byte-identical field set — order-insensitive compare via unmarshal-both-and-DeepEqual, or a
  normalized compare). Run it against the CURRENT (pre-embed) code → it passes (establishing the
  golden). This test is the parity gate that must STILL pass after embedding.
- [ ] **Step 3: Introduce `triggerMatch` and embed it** anonymously in the three arm types,
  removing the four now-duplicated field declarations from each. Update `cloneState`
  (`engine/step_state.go`) ONLY if it constructs arms field-by-field (embedding is copied by
  value with the struct — a shallow slice copy still works since all fields are strings/bools; if
  cloneState does `armedEvent{TimerID: x.TimerID, ...}` field-lists, update to
  `armedEvent{..., triggerMatch: x.triggerMatch}` or just `x` copy).
- [ ] **Step 4: Verify wire parity + green.** The B1 parity test MUST still pass (proving the
  JSON shape is unchanged). Then: `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -race ./engine/...`; **`go test ./persistence/... ./internal/persistence/... -count=1`
  → `ok`** (the real persistence round-trip, the ultimate wire gate); `golangci-lint run
  ./engine/...` clean; `gofmt -l engine/` empty; `go doc ./engine` diff vs `/tmp/engine-doc-b.txt`
  empty.
- [ ] **Step 5: Commit** `refactor(engine): extract embedded triggerMatch shared by the 3 arm types (wire-safe, ADR-0131)`.

**STOP and escalate if:** embedding changes the marshaled JSON (parity test fails) — that would
mean a non-anonymous embed or a json-tag conflict; report it, do NOT force a wire change.

---

### Task B2: Unify the arm scan/remove accessors via generics

**Files:** Modify `engine/state_arms.go` (and wherever the accessors live after Phase-A split).

**Interfaces:**
- Produces: generic helper(s) collapsing the ~13 duplicated linear scans. The three families each
  have a `byTimer`/`bySignal`/`byMessage` trio plus `removeForX` filters that differ only in the
  slice and the owner-key. Two viable shapes (pick the clearer):
  1. Free generic funcs keyed on the embedded match, e.g.
     `func armByTimer[T any](arms []T, timerID string, m func(*T) *triggerMatch) *T` — returns the
     matching element pointer (preserving the current pointer-return-for-mutation contract), and
     analogous `armBySignal`/`armByMessage`.
  2. A small `hasTriggerMatch` interface (`triggerMatchPtr() *triggerMatch`) implemented once per
     arm type (trivial via the embed) so one function serves all three.
  Keep the pointer-return semantics EXACTLY (callers mutate `&slice[i]`). Keep the `removeForX`
  filters (they key on the OWNER field — GatewayToken/HostToken/EnclosingScopeID — not the match,
  so they may stay per-type or take an owner-key accessor; unify only what is genuinely common).

- [ ] **Step 1: Confirm green baseline.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`.
- [ ] **Step 2: Inventory the accessors.** List every `*ByTimer`/`*BySignal`/`*ByMessage` and
  `removeFor*` method on the three arm families (grep `state_arms.go`). Note return type
  (pointer vs value) and the owner-key each filter uses.
- [ ] **Step 3: Introduce the generic scan helpers** and replace the byTimer/bySignal/byMessage
  trios. Preserve pointer-return + slice-order semantics EXACTLY. For the `removeForX` filters,
  unify only if a generic keyed on an owner-accessor reads clearly; otherwise leave them.
  **If the generic version reads WORSE than the duplication, extract less and report** — the goal
  is lower cognitive load, not fewer lines at any cost.
- [ ] **Step 4: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  the arm/boundary/event-sub/gateway suites: `go test -run 'Boundary|EventSub|EventBased|Gateway|Arm|Signal|Message|Timer' ./engine/... -count=1`;
  `go test -race ./engine/...`; persistence round-trip green; `golangci-lint run ./engine/...`
  clean; `go doc ./engine` diff empty.
- [ ] **Step 5: Commit** `refactor(engine): unify arm scan accessors via generics over triggerMatch`.

---

### Task B3: Unify the arm→fire→cancel skeleton where genuinely common (best-effort)

**Files:** Modify `engine/step_boundaries.go`, `engine/step_eventsubprocess.go`.

**Interfaces:**
- Produces: a shared helper for the interrupting/non-interrupting fire skeleton common to boundary
  and event-subprocess arms (the fire-once action via `emitFireOnceAction`, the late/stale-fire
  no-op guard, the interrupting consume-host / cancel-scope path via `cancelTokenWaits`, the
  non-interrupting repeatable-arm spawn, then drive). Boundary is host-token-keyed; ESP is
  scope-keyed — extract ONLY the genuinely-common part and leave the keyed differences at the call
  sites (the pattern used successfully by Phase-A's A4 `dispatchArmCascade`).

- [ ] **Step 1: Confirm green baseline + compare the two fire funcs.** Read `fireBoundaryArm` and
  `fireEventTriggeredSubprocessArm`. Table their steps side by side in your report; identify the
  truly-common sub-sequence vs the host-vs-scope-keyed differences.
- [ ] **Step 2: Extract the common skeleton** (reusing `emitFireOnceAction`, `cancelTokenWaits`),
  leaving the keyed differences specialized. **If a clean shared helper would obscure more than it
  clarifies or risks behavior change, do LESS — extract only the fire-once + late-fire-guard, or
  nothing — and report the judgment.** Behavior must be identical (command order, interrupting vs
  non-interrupting semantics, repeatable-arm ADR-0124 behavior).
- [ ] **Step 3: Verify green.** `go test ./engine/... -count=1 2>&1 | tail -1` → `ok`;
  `go test -run 'Boundary|EventSub|Interrupt|Reminder|Repeatable' ./engine/... -count=1`;
  `go test -race ./engine/...`; persistence round-trip green; `golangci-lint run ./engine/...`
  clean; `go doc ./engine` diff empty.
- [ ] **Step 4: Commit** `refactor(engine): unify boundary/event-sub fire skeleton where common`
  (adjust message to state precisely what was unified vs left specialized).

---

## Phase B Self-Review (control gate before ship)

- [ ] `go test ./... 2>&1 | tail -5` from repo root — all `ok` (incl testcontainers persistence
  + runtime).
- [ ] **Wire parity: the B1 golden-JSON test + the persistence round-trip suite both green** —
  the definitive proof the serialized format is unchanged.
- [ ] `go test -race ./engine/... -count=1` green. Coverage ≥85%.
- [ ] `golangci-lint run ./...` clean; `gofmt -l engine/` empty.
- [ ] `go doc ./engine` identical to the phase-start snapshot.
- [ ] ADR-0131 committed (Nygard).
- [ ] `/code-review` (whole Phase B diff vs `main`) — all findings adjudicated & resolved.
- [ ] `/security-review` — clean.
- [ ] `--no-ff` merge to `main` + push.
```
