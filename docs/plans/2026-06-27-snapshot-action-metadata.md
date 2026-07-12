# Plan — T1: Surface action metadata in snapshots + gRPC snapshot RPCs

Spec: `docs/specs/2026-06-27-snapshot-action-metadata-design.md`. ADR: `docs/adr/0068-snapshot-action-metadata.md`.
Branch: `feat/snapshot-action-metadata`. Module: `github.com/kartaladev/wrkflw`.

## Global Constraints (binding — copy to reviewers verbatim)

- Strict TDD: every new symbol/behaviour preceded by a visible failing `go test` run.
- `engine/` must stay untouched (import-purity preserved). `model/` change is limited to the
  single additive `ScopedActionNames()` accessor + its builder population; no behaviour change.
- DTO field JSON tags exactly: `scoped_actions`, `action_bindings`; `ActionBindingView` tags
  `node_id`, `node_kind`, `action` (omitempty), `inline`.
- `NodeKind` strings exactly `"serviceTask"` and `"businessRuleTask"`.
- `ScopedActionNames()` returns a **sorted** slice, nil when empty. `ActionBindings` sorted by NodeID.
- `Action` empty means default-by-id; do NOT substitute the NodeID — leave it empty.
- gRPC RPC names exactly `GetInstanceSnapshot`, `GetActionableView`; reuse `GetInstanceRequest`;
  wrap responses as `InstanceSnapshotResponse` / `ActionableViewResponse`.
- Per-package gate: ≥85% line coverage on touched packages, `go test -race ./...` green,
  `golangci-lint run ./...` clean, gofmt clean.
- Project skills: table-test (assert-closure form, `t.Context()`), use-mockgen, black-box
  `_test` packages where practical, testable examples for public API.

## Task 1 — `model.ProcessDefinition.ScopedActionNames()`

Files: `model/definition.go`, `model/builder.go`, `model/definition_test.go` (or `builder_test.go`).
- Red: test that a def built with `RegisterAction("b",…)`, `RegisterActionFunc("a",…)` exposes
  `ScopedActionNames() == ["a","b"]` (sorted); a def with no scoped actions returns nil.
- Green: store `scopedNames []string` on `ProcessDefinition` at `Build()` (sorted keys of
  `b.actions`); add the accessor. Keep `ScopedCatalog()` as-is.

## Task 2 — runtime DTO enrichment

Files: `runtime/instance_snapshot.go`, `runtime/instance_snapshot_test.go`.
- Red: test `NewInstanceSnapshot(st, def)` where `def` has (a) a ServiceTask with `WithActionName("x")`,
  (b) a ServiceTask with `WithAction(inline)`, (c) a ServiceTask with no name (default-by-id),
  (d) a BusinessRuleTask, and a scoped catalog `{"x"}`. Assert `ScopedActions == ["x"]` and
  `ActionBindings` has the 4 entries with correct `NodeKind`/`Action`/`Inline`, sorted by NodeID.
  Assert `def == nil` => both fields empty/nil.
- Green: add `ActionBindingView` type + the two fields; populate from `def.Nodes` (type-switch
  on `model.ServiceTask`/`model.BusinessRuleTask`), `model.ActionOf`, `model.InlineActionOf`,
  `def.ScopedActionNames()`.

## Task 3 — REST snapshot test (no handler change expected)

Files: `transport/rest/snapshot_test.go` (extend).
- The handlers already pass `def`; verify enriched JSON. Red: assert the snapshot HTTP response
  body contains `scoped_actions` and `action_bindings` for a started instance whose def has a
  scoped/inline service task. Green: only if a wiring gap is found (expected: none).

## Task 4 — gRPC proto + regeneration

Files: `transport/grpc/proto/workflow.proto`, regenerated `transport/grpc/workflowpb/*.pb.go`.
- Add proto messages (`TokenView`, `NodeVisitView`, `TaskView`, `IncidentView`,
  `ActionBindingView`, `InstanceSnapshot`, `NextAction`, `ActionableTask`, `ActionableView`,
  `InstanceSnapshotResponse`, `ActionableViewResponse`) and the two RPCs.
- Install plugins (`go install …/protoc-gen-go@latest`, `…/protoc-gen-go-grpc@latest`),
  regenerate with the `protoc` command from `buf.gen.yaml`'s comment, commit generated files.
- This task is mechanical (no Go logic); verify `go build ./...` compiles the new stubs.

## Task 5 — gRPC server impl

Files: `transport/grpc/server.go`, `transport/grpc/server_test.go` (or snapshot-specific test).
- Red: bufconn test calling `GetInstanceSnapshot`/`GetActionableView` on a started instance;
  assert mapped fields incl. `action_bindings`/`scoped_actions`; not-found => `codes.NotFound`.
- Green: implement both RPCs using `svc.GetInstanceWithDefinition`, mapping
  `runtime.NewInstanceSnapshot`/`NewActionableView` → proto (reuse `structToMap`/timestamp helpers).

## Verification checklist

- [ ] T1 model test red→green; sorted/nil cases covered.
- [ ] T2 runtime DTO red→green; inline/named/default-by-id/business-rule + nil-def covered.
- [ ] T3 REST JSON includes new fields.
- [ ] T4 proto regenerated, committed, compiles.
- [ ] T5 gRPC RPCs implemented + bufconn tests incl. NotFound.
- [ ] `go test -race ./...` green; touched pkgs ≥85%; lint 0; gofmt clean; engine untouched.
- [ ] ADR-0068 + spec committed; HANDOVER resume point updated; whole-branch opus review clean.
