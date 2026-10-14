# Tasks

Written for the recommended answers in design.md (D1 to D5), **pending the maintainer's answers to Open Questions Q1 to Q5**. If an answer differs, revise these tasks with `/opsx:update` before starting. No group starts before `remove-casbin` is archived. This change has no spec scenarios (`skip_specs: true`), so each task names its own verification.

## 0. Gate

- [ ] 0.1 Record the maintainer's answers to Q1 to Q5 in design.md, replacing "Recommendation" with "Decided", and set Readiness to agent in proposal.md. Verify with `openspec validate decide-module-denylist-policy --strict`.
- [ ] 0.2 Confirm `remove-casbin` is archived and that casbin, govaluate and doublestar are absent. Verify that `GOTOOLCHAIN=go1.26.9 go list -m all | grep -E 'casbin|doublestar'` prints nothing.

## 1. Banned-module list (D2, D4), test first

- [ ] 1.1 Write the matcher's test first. It is table-driven per the `table-test` skill. A planted list naming a present module (`github.com/expr-lang/expr`) must report it, a list naming only absent modules must report nothing, and `github.com/casbin/casbinX` must not match the banned `github.com/casbin/casbin` (path-boundary control). Watch it go red against a stub. Verify with `GOTOOLCHAIN=go1.26.9 go test -run TestBannedModule ./`.
- [ ] 1.2 Implement the root-module test over `go list -m all`. Each entry holds a path, the removing issue or change, and a reason. The entries are goavro (kartaladev/wrkflw#113), watermill (#45/#131), casbin, govaluate and doublestar (`remove-casbin`). The test fails, rather than passing, when workspace mode lists fewer than two `github.com/kartaladev/wrkflw` modules, and logs that it skipped the floor when `go env GOWORK` is `off`. Verify that `GOTOOLCHAIN=go1.26.9 go test -run TestBannedModule ./` passes, and that it goes red when `github.com/linkedin/goavro/v2` is temporarily required in `examples/go.mod`. Revert that edit afterwards.
- [ ] 1.3 Write the list's doc comment. It states the D1/D4 policy, says the list covers the whole workspace module graph, and names `TestEventingDependencyGraphIsMessagingVendorFree` as the spec-backed eventing-scoped check (cite `openspec/specs/event-delivery/spec.md`, never a change path). Verify that `bash scripts/check-doc-refs.sh` passes.

## 2. Engine-import deny-list (D1, D3)

- [ ] 2.1 Remove `"watermill"` and `"casbin"` from `deniedEngineImports` in `engine/purity_test.go`. Rewrite the doc comment at `:159`: the list guards layering and present vendors only, and a removed dependency's entry moves to the banned-module list. Verify that `GOTOOLCHAIN=go1.26.9 go test -run TestCorePurity ./engine/...` passes, and that adding a temporary `import _ "github.com/kartaladev/wrkflw/transport/http/httpcore"` to an engine file still fails it. Revert that edit afterwards.
- [ ] 2.2 Add a one-line comment to `eventing/vendorfree_test.go` pointing to the banned-module list and stating the scope difference (D3). Verify that `GOTOOLCHAIN=go1.26.9 go test -run TestEventingDependencyGraph ./eventing/...` passes.
- [ ] 2.3 Edit the *Engine purity* bullet in `CONTRIBUTING.md:122-123`. Strike casbin from "Never import …", and add one sentence on the policy: removed dependencies are banned by the module-graph list, which records the reason for each entry. Verify that `git grep -n -i casbin -- ':!openspec'` prints only the banned-list entry and its comment, and that `bash scripts/check-doc-refs.sh` passes.

## 3. Integration

- [ ] 3.1 Run the whole workspace. Verify that `GOTOOLCHAIN=go1.26.9 go test $(bash scripts/modules.sh)` and `bash scripts/lint.sh` pass.

## Workflow follow-up

- The PR body names `decide-module-denylist-policy`. The last commit runs `openspec archive decide-module-denylist-policy`.
- After merge, comment on and close kartaladev/wrkflw#124 and #125, naming this change.
