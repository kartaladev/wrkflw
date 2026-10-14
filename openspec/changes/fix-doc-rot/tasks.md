# Tasks

This change has no specs (`skip_specs: true`). The defects are in rendered documentation, so their red step is a render that shows the wrong href: the guard test, if Open Question 1a is answered (i), or otherwise the throwaway `go/doc` render. Run every Go command per module, as CONTRIBUTING.md describes. A bare `./...` covers only the root module.

## 1. Record the decisions

- [ ] 1.1 Get the maintainer's answers to the three questions in design.md, Open Questions. Write them into design.md as decisions D6–D8, each with the options rejected, and move each question out of Open Questions. Verify: design.md has no Open Questions section left, `openspec validate fix-doc-rot --strict` passes, and the proposal's Coordination block says `Readiness: agent`.

## 2. Doc-link guard, red first (only if 1.1 chose a guard for Open Question 1a; otherwise skip to 3 and use the throwaway render as the red step)

- [ ] 2.1 Write the guard as decided: by default a Go test that renders every package's doc comments with `go/doc` and fails on (a) an href whose import path is in neither `go list std` nor the module's own packages, (b) a package-qualified bracket pair that renders as literal text, and (c) two adjacent doc links (`][`). It needs no network. Include a self-test that plants each form in a fixture and asserts that it is reported, plus a clean fixture that reports nothing. Verify: `GOTOOLCHAIN=go1.26.9 go test -run <GuardName> .` fails, listing the 9 misresolving links, the 29 package-qualified literal-text links (26 in exported packages and examples, plus 3 in `internal/`, which a guard forces 3.5 to fix too), and the 6 `][` sites (design D2 counts, re-measured). The self-test passes.
- [ ] 2.2 Add the same invocation for the `examples` module and wire both into the existing CI test job, leaving job naming to `harden-ci-guard-wiring`. Verify: CI runs the guard for both modules, and it is red until group 3 lands.

## 3. Fix doc links (#130)

- [ ] 3.1 Re-run the `go/doc` render over every non-test package in both modules, outside the repo, and confirm the D2 table against `main` as it is when this starts. Verify: the PR description lists each class with its count and names any drift from `4763d36e`.
- [ ] 3.2 Rewrite the 16 short-form cross-package links in root `doc.go` in full import path form (design D1). Verify: the render of the root package emits 23 hrefs, all under `/github.com/kartaladev/wrkflw/…` or `/github.com/jonboulle/clockwork`, with none under `/runtime`. The guard from 2.1, if present, no longer reports root `doc.go`.
- [ ] 3.3 Fix `eventing/message.go:10` (`[runtime.ProcessDriver.DeliverMessage]`) and `examples/scenarios/event_start/main.go:21` (`[runtime/chain.Chainer]`). Verify: the render emits `/github.com/kartaladev/wrkflw/runtime#ProcessDriver.DeliverMessage` and `/github.com/kartaladev/wrkflw/runtime/chain#Chainer`.
- [ ] 3.4 Fix the 6 `[httpcore.RouteCustomizer][*http.ServeMux]` sites in `transport/http/stdlib`. Verify: each renders as two links separated by prose, with no adjacent `</a><a`.
- [ ] 3.5 Fix the package-qualified literal-text links in `transport/http/gin` (8), `transport/http/fiber` (6), `persistence` (2) and `examples/scenarios/input_validation` (1), and, if the guard exists, the 3 in `internal/persistence/{store,dialect}`. Verify: the render emits an href for each, and `GOTOOLCHAIN=go1.26.9 go vet` passes for each touched package. If the guard exists, `GOTOOLCHAIN=go1.26.9 go test -run <GuardName> .` and its `examples` counterpart now pass.

## 4. Fix comment-named package paths (#123)

- [ ] 4.1 In the `ErrUnserializableValidation` godoc (`definition/model/validation_wire.go`), replace `validation/callback`, `validation/expr` and `validation/jsonschema` with `definition/model/validate/{callback,expr,jsonschema}` (or the `validate/…` short form that `validate/registry.go` uses). Verify: `git grep -nE '\bvalidation/(expr|jsonschema|callback)\b' -- '*.go'` matches only the `workflow-validation/…` error-message prefixes, and each named directory exists.
- [ ] 4.2 Replace `eventing/chain` in `runtime/driver_shutdown.go` and `runtime/kernel/shutdown.go` with the package that actually classifies the shutdown sentinel. Read the code to find it rather than assuming `runtime/chain`. Verify: `git grep -n 'eventing/chain' -- '*.go'` is empty, and the named package's source does reference `ErrDriverShuttingDown` (or its `kernel` original).

## 5. Correct the `check-doc-refs.sh` scope header (#91)

- [ ] 5.1 Apply the 1.1 decision for Open Question 3. If it stays Go-only (the recommendation), rewrite the SCOPE paragraph so that it gives design D4's measured reason, naming the three groups of legitimate non-Go naming, and keep `NARROWABLE, NOT DELETABLE`. If it widens, implement the decided scope with explicit exclusions, and extend `assert_covers_tree` and the self-test to the new file set. Verify: `scripts/check-doc-refs.sh` and `scripts/check-doc-refs.sh --self-test` pass. For Go-only, every claim in the header holds against a fresh `git grep -lE 'docs/adr|docs/specs|SECURITY\.md|STABILITY\.md|ADR-[0-9]|openspec/changes/'` run over non-Go files. For widening, planting a `docs/adr/0001.md` citation in a non-Go fixture is reported.

## 6. Conventions for keeping descriptions true (#100)

- [ ] 6.1 Write the conventions decided in 1.1 for Open Question 2 into `docs/agents/`, and add the index line to the CLAUDE.md "Agent docs" section. Recommended content: a script's behavior is described once, in its header, and other copies link to it; a figure in prose carries its measurement commit or date, or is replaced by the command that derives it; descriptions are guarded by review, and no check exists. Do not edit the counts or the Timeout cell in `docs/agents/test-deadlines.md`, which `size-io-test-deadlines` owns. Verify: the doc scores itself against the seven instances in design.md, Open Question 2, and reproduces the table, and `scripts/check-doc-refs.sh` passes.
- [ ] 6.2 Apply the new figure convention to the prose figures this change touched (the `check-doc-refs.sh` header's "190 ADRs" and "~2,240"). Each gets a measurement anchor, such as "deleted in 12630621", or is cut. Verify: every figure in the files changed by this PR carries an anchor or is derived by a command the text names.

## 7. Integration

- [ ] 7.1 Run the repo guards and the per-module checks. Verify: `scripts/check-doc-refs.sh`, `scripts/check-openspec.sh`, and `GOTOOLCHAIN=go1.26.9 go vet` plus `GOTOOLCHAIN=go1.26.9 go test` for the root and `examples` modules (`scripts/modules.sh`) all pass. `git diff --stat` shows only comments, `scripts/check-doc-refs.sh`, `docs/agents/`, `CLAUDE.md`, the guard (if decided) and this change's directory.

## Workflow follow-up

- The PR body names `fix-doc-rot`. Its last commit runs `openspec archive fix-doc-rot --yes`.
- After `cut-v0-release` publishes a version, spot-check root `doc.go` on pkg.go.dev, confirming that the `runtime` links go to `github.com/kartaladev/wrkflw/runtime`.
