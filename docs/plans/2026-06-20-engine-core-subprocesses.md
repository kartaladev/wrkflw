# Engine Core — Sub-Processes & Call Activity (Plan 7 of 8) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development (or executing-plans). Checkbox steps.
>
> **Handover note:** Targets the design spec (§3/§4/§5) and assumes Plans 1–6 merged. Contracts fixed by the spec; ground exact edits against current code. SDD review loop is the safety net. This plan introduces the **scope tree** (`InstanceState.Scopes`) the spec reserved in Plan 1 and that compensation (Plan 8) builds on.

**Goal:** Add Embedded Sub-Processes, Event Sub-Processes, and Call Activities, with a scope tree so tokens, boundary events, and (later) compensation are correctly contained.

**Architecture:** Entering a sub-process opens a `Scope` (a node in `InstanceState.Scopes`, parented to the enclosing scope) and places a token on the sub-process's start node, tagged with `ScopeID`. The sub-process completes when its scope has no remaining tokens (or a sub-process end is reached); the engine then consumes the scope and advances the sub-process activity's outgoing flow. Call Activity starts a **separate child instance** via the `StartSubInstance` command; the child's completion returns as `SubInstanceCompleted`/`SubInstanceFailed`.

**Tech Stack:** Go 1.25, `testify`.

## Global Constraints

- Go **1.25**; root packages; engine pure (no transport/storage/bus/time-vendor; no `time.Now()`). `Step` deterministic + pure; signature unchanged. Black-box tests, `assert`-closure tables, `t.Context()`. Coverage ≥ 85% touched; `-race` green; lint clean. Conventional Commits; commit per green step.

## Prerequisite & contracts

- `InstanceState.Scopes []Scope` where `Scope{ID, NodeID, ParentID string; Compensations []CompensationRecord}` (spec §4). `Token.ScopeID` already exists.
- Command (sealed): `StartSubInstance{CommandID string; DefRef string; Input map[string]any}` (spec §5).
- Trigger (sealed): `SubInstanceCompleted{CommandID string; Output map[string]any}`, `SubInstanceFailed{CommandID string; Err string}` (spec §5).
- model `Node`: `KindSubProcess`/`KindEventSubProcess`/`KindCallActivity`. A sub-process owns inner nodes/flows — model this as either (a) a nested `*ProcessDefinition` on the node (`Subprocess *ProcessDefinition`), or (b) inner nodes carrying a `ParentNode` id. **Decision:** use nested `Subprocess *ProcessDefinition` for embedded/event sub-processes (clean containment, reuses `Validate`); Call Activity uses `DefRef string` (a name resolved by the runtime to another top-level definition).
- Deterministic `ScopeID` = `<instanceID>-s<seq>` (`ScopeSeq` counter).

---

## File Structure

```
model/definition.go        # MODIFY: Subprocess *ProcessDefinition (embedded/event), DefRef (call activity), event-subprocess trigger fields; nested Validate
engine/command.go          # MODIFY: StartSubInstance
engine/trigger.go          # MODIFY: SubInstanceCompleted/Failed
engine/state.go            # MODIFY: Scope helpers (open/close/find), ScopeSeq
engine/step.go             # MODIFY: sub-process entry/exit; event sub-process; call activity; scope-aware drive
engine/step_subprocess_test.go
runtime/runner.go          # MODIFY: perform StartSubInstance (run child instance, return Sub* trigger); definition registry
runtime/subprocess_example_test.go
```

---

### Task 1: model nesting + validation

- [ ] **RED:** `model` tests — a `KindSubProcess` node with a nested `Subprocess` (its own start/end); a `KindCallActivity` with `DefRef`. Invalid: embedded sub-process whose nested definition fails `Validate` (propagate as a wrapped error); call activity with empty `DefRef` (`ErrMissingDefRef`). Run → fails.
- [ ] **GREEN:** add `Subprocess *ProcessDefinition`, `DefRef string`, and event-subprocess trigger fields to `Node`; make `Validate` recurse into `Subprocess` (prefixing errors with the node id); add sentinels. Run → pass.
- [ ] Commit `feat(model): nested sub-process definitions, call-activity ref, recursive validation`.

---

### Task 2: scope helpers + commands/triggers

- [ ] **RED:** extend `engine/state.go` (via a test) for `openScope(nodeID, parentScopeID) string`, `tokensInScope(scopeID) int`, `closeScope(scopeID)`; extend command/trigger tests for `StartSubInstance`/`SubInstanceCompleted`/`SubInstanceFailed`. Run → fails.
- [ ] **GREEN:** implement scope helpers (with `ScopeSeq` deterministic IDs) + the command/trigger types/constructors; extend `cloneState` to copy `Scopes` (and each scope's `Compensations`). Run → pass.
- [ ] Commit `feat(engine): scope tree helpers and sub-instance commands/triggers`.

---

### Task 3: Embedded Sub-Process entry/exit

**Behavior:** `drive` `KindSubProcess`: open a `Scope` (parented to the token's current `ScopeID`), place a token on the nested `Subprocess` start node tagged with the new `ScopeID`, and **consume** the sub-process activity token (it is "inside" now). Sub-process inner nodes drive normally but `drive` must resolve nodes/flows against the **nested** definition for tokens whose `ScopeID` maps to that sub-process. A sub-process **end** inside the scope consumes the inner token; when `tokensInScope(scopeID)==0`, close the scope and place a token on the sub-process activity's **outgoing** flow in the parent scope (i.e., the sub-process completes and execution continues after it).

> Implementation note: `drive` currently takes one `def`. To resolve inner nodes, either (a) thread the active definition per token (look up the scope's definition), or (b) keep a `scopeID → *ProcessDefinition` map built from the top definition. Prefer (b): a helper `defForScope(top, scope)` returns the nested definition; `drive` resolves `node`/`Outgoing` via the token's scope definition.

- [ ] **RED:** `step_subprocess_test.go` — `TestEmbeddedSubProcessRunsAndContinues`: start→sub[ start→svc→end ]→end. Entering opens a scope, the inner service action fires; completing it ends the inner scope and the outer flow proceeds to the outer end → `CompleteInstance`. Run → fails.
- [ ] **GREEN:** implement entry/exit + scope-aware node/flow resolution. Run → pass.
- [ ] Commit `feat(engine): embedded sub-process entry, scoped execution, and exit`.

---

### Task 4: Event Sub-Process (interrupting + non-interrupting)

**Behavior:** an event sub-process sits inside a parent (sub-)process scope and is triggered by an event (signal/timer/message) while that scope is active. Interrupting: on trigger, cancel all tokens in the parent scope and run the event sub-process to completion (which then completes the parent scope). Non-interrupting: spawn the event sub-process in a child scope alongside the still-running parent scope. Reuses Plan-6 event arming, scoped to the parent.

- [ ] **RED:** `TestInterruptingEventSubprocessCancelsParentScope` and `TestNonInterruptingEventSubprocessRunsAlongside`. Run → fails.
- [ ] **GREEN:** arm the event sub-process trigger when its parent scope opens; on fire, apply interrupting/non-interrupting semantics over the scope's tokens. Run → pass.
- [ ] Commit `feat(engine): event sub-process (interrupting + non-interrupting)`.

---

### Task 5: Call Activity (separate child instance)

**Behavior:** `drive` `KindCallActivity`: emit `StartSubInstance{CommandID, DefRef:node.DefRef, Input:<mapped vars>}` and park the token (`AwaitCommand=CommandID`). `Step` `SubInstanceCompleted{CommandID,Output}`: merge `Output` into vars, advance the token; `SubInstanceFailed`: route to error handling (boundary error if present — coordinate with Plan 8 — else `FailInstance`). The **runtime** `perform StartSubInstance` resolves `DefRef` via a definition registry, runs the child to completion via the same `Runner`, and returns `SubInstanceCompleted/Failed`.

- [ ] **RED:** `step_subprocess_test.go` — `TestCallActivityEmitsStartSubInstanceAndResumes`. runtime e2e (`subprocess_example_test.go`): a parent process calls a child definition; both complete; parent vars include the child output. Run → fails (`StartSubInstance` handled by `default`; runtime registry missing).
- [ ] **GREEN:** implement the call-activity case + handlers; add a `DefinitionRegistry` to the runtime; `perform StartSubInstance` runs the child. `-race`, coverage, lint green.
- [ ] Commit `feat(engine,runtime): call activity via child instance`.

---

## Verification Checklist (Plan 7)

- [ ] Embedded sub-process opens a scope, runs inner nodes against the nested definition, and continues the outer flow only after the inner scope drains.
- [ ] Event sub-process: interrupting cancels the parent scope; non-interrupting runs alongside.
- [ ] Call activity starts a child instance and resumes the parent on `SubInstanceCompleted`; failure routes to error handling/`FailInstance`.
- [ ] Scope tree is correct (parenting, `ScopeID` on tokens) and `cloneState` copies it; `Step` deterministic + pure.
- [ ] `-race` green; coverage ≥ 85% touched; lint clean.

## Self-Review Notes

- **Spec coverage:** §3 SubProcess/EventSubProcess/CallActivity; §4 Scopes; §5 StartSubInstance + SubInstanceCompleted/Failed. Compensation per-scope `CompensationRecord` is populated here (record completed compensable activities) and consumed in Plan 8.
- **Key design choice:** nested `*ProcessDefinition` for embedded/event sub-processes (containment + reuses `Validate`); separate child instance for call activity (isolation, independent lifecycle) — exactly the spec's §10 decision ("StartSubInstance + scope tree, not inlining a child engine").
- **drive scope-awareness:** the one structural change is resolving `node`/`Outgoing`/`Incoming` against the token's scope definition; isolate it behind `defForScope` so gateway/event/timer cases keep working unchanged.
- **Grounding required:** read merged `engine/step.go` and `runtime/runner.go`; the scope-aware `drive` touches every node case — re-run the full engine suite after the refactor before adding sub-process cases.
- **Boundary interaction:** boundary events on a sub-process activity (cancel the whole scope) build on Plan-6 boundaries + this scope model; covered where boundaries attach to `KindSubProcess`.
