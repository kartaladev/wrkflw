# Proposal

## Why

Godoc and contributor prose in this repo point readers at things that are not there: doc links in root `doc.go` render into Go's standard-library `runtime` package, a sentinel's godoc names three import paths that never existed, and descriptions of the repo's own machinery drift out of truth with nothing to catch them. `scripts/check-doc-refs.sh` guards only whether a fixed set of deleted documents is cited from Go source. It cannot see a doc link that misresolves, a package path written as prose, or a description that has stopped being true. Fix the live instances now, and decide which of these classes deserve a guard.

## What Changes

Every premise below was re-measured on `main` at `4763d36e`. Doc links were measured by rendering each package's doc comments through `go/doc`'s own parser and printer, the resolver pkgsite uses, and reading every emitted `href`. `go doc` in a terminal strips the brackets and cannot show a broken link. No version of the module is published, so nothing was checked live on pkg.go.dev.

- **kartaladev/wrkflw#130 (absorbed, premise partly stale).** Root `doc.go` emits 14 hrefs. 7 are correct (the full-import-path links, such as `[github.com/jonboulle/clockwork.Clock]`), and 7 point into stdlib `runtime`: `[runtime.ProcessDriver]` twice, plus `NewProcessDriver`, `WithActionCatalog`, `WithInstanceStore`, `ProcessDriver.Drive` and `ProcessDriver.ApplyTrigger`. 9 more short-form links (`[engine]`, `[engine.Step]`, `[persistence]`, `[model.ProcessDefinition]`, `[definition.NewBuilder]`, `[definition.NewLoader]`, `[model.DefinitionLoader.Build]`, `[action.DefaultCatalog]`, `[kernel.NewMemInstanceStore]`) resolve to nothing and render as literal bracketed text. Rewrite all 16 in full-import-path form.
  - **Stale:** the `[httpcore.RouteCustomizer][*http.ServeMux]` form is not in root `doc.go`. It is in `transport/http/stdlib`, at 6 sites (`groups.go:26,113,147,249,521`, `write.go:2`), and renders as two adjacent links, `httpcore.RouteCustomizer` and `*http.ServeMux`. Rewrite those 6. `doc.go` is now 236 lines, not 101.
  - **The issue's tree-wide sweep, measured.** Outside root `doc.go`, 2 more links misresolve: `eventing/message.go:10` `[runtime.ProcessDriver.DeliverMessage]` (stdlib `runtime`), and `examples/scenarios/event_start/main.go:21`'s package-doc link `[runtime/chain.Chainer]` (stdlib-style path `/runtime/chain`, which does not exist). 20 more package-qualified links render as literal text: `[gin.IRouter]` ×7 and `[authz.ContextWithActor]` in `transport/http/gin`, `[fiber.Router]` ×6 in `transport/http/fiber`, `[dbtest.RunTestSQLite]` ×2 in `persistence`, `[validate.ValidationStrategy]` in `examples/scenarios/input_validation`, and 3 in `internal/persistence/{store,dialect}`. Fix the 2 misresolving links and the 17 exported-package ones. The 3 in `internal/` are fixed only if they are touched anyway (design D2).
  - 72 further bracket pairs render as literal text because they name unexported identifiers, type parameters (`RouteCustomizer[R]`) or builtins. They are not links that point anywhere wrong. They are out of scope (design D2).
- **kartaladev/wrkflw#123 (absorbed, holds).** The `ErrUnserializableValidation` godoc (`definition/model/validation_wire.go:10-16`; the issue's `:13-16` has drifted) names `validation/callback`, `validation/expr` and `validation/jsonschema`. The strategies live at `definition/model/validate/{callback,expr,jsonschema}`, and `runtime/validation` holds only `gate.go`. Correct the three paths.
  - **Same class, found by measurement:** `runtime/driver_shutdown.go:15` and `runtime/kernel/shutdown.go:9` name a downstream consumer `eventing/chain`. No such package exists or has ever existed (`git log --all` is empty for it); the chainer lives in `runtime/chain`. Correct both.
- **kartaladev/wrkflw#91 (absorbed; its four rows are stale, the remainder is folded in).** All four dangling-reference rows are gone on `main`. `CLAUDE.md:15`, `CONTRIBUTING.md:43` and `docs/agents/domain.md` were fixed by #95, and `scripts/gen-at-rest.sh` was deleted by #52 (PR #192). The issue's open question (fix the references or reinstate the documents) was settled by #52: retire. What remains is the guard's own header. The `:28-29` claim the 2026-09-10 comment quoted ("three of those files still carry live references") was removed by #242. `scripts/check-doc-refs.sh:29-35` now says prose outside Go source is "the maintainer's call" and that "the tree is … NOT at zero overall". The second claim is literally true, but measurement shows why: every non-Go match is legitimate. They are the rule's own self-description (`.github/workflows/ci.yml:150`, the script itself), the OpenSpec workflow docs that forbid citing a change path (`CLAUDE.md`, `CONTRIBUTING.md`, two `.claude/` files), and OpenSpec change artifacts recording history (one archived proposal, three files of `document-stability-policy`). None of them is a dangling reference waiting on a decision. Rewrite the header so that the reason it gives is the true one.
- **kartaladev/wrkflw#100 (absorbed).** All six of the issue's instances were re-checked. The four that live in the tree are fixed on `main`. The other two live in issue text and cannot be fixed in the tree. A seventh, live instance was found: `docs/agents/test-deadlines.md:11` says its counts are "derived from source, never written down here", while `:6`, `:29`, `:70` and the table write down 45 deadlines across 27 files, and `main` has 58 across 32. Re-deriving those counts belongs to `size-io-test-deadlines`. This change owns the **rule** for keeping descriptions and figures in prose true, and that rule is a decision (below).

**Decisions this change cannot be finished without** (options, measurements and recommendations are in design.md, Open Questions). The instance fixes above depend on none of them.
1. #130 and #123: whether a CI guard renders every doc comment and fails when a doc link misresolves. Recommended: yes, as an offline Go test. Whether comment-named package paths written as plain text get a guard. Recommended: no, because the measured false-positive rate is about 75%.
2. #100: which mechanism keeps descriptions true. The candidates are the issue's options 1–4, scored against the six instances plus the seventh found here. Recommended: two written conventions, one description per script that the other copies link to (option 2) and a figure in prose that carries its measurement commit or the command that derives it (option 3, as a convention), plus a stated "review is the guard" note (option 4). Recommended against: a mechanical check. The duplication check catches 1 of 7, and a check for written-down numbers is too noisy to ship.
3. #91: whether `check-doc-refs.sh` widens from `*.go` to the whole tree. Recommended: no. Widening fails on day one against at least 10 legitimate files.

**Out of scope**
- Godoc that misdescribes **behavior** (for example `WithChainLinks`, `ErrCancelNotApplicable` and `MaxInterval`) is owned by `align-godoc-with-behavior`. This change fixes only references: links, paths and descriptions of repo machinery.
- Re-deriving the counts and the Timeout "Wants to be" cell in `docs/agents/test-deadlines.md` is owned by `size-io-test-deadlines`. This change edits that file only if decision 2 changes how its figures are written, and then it touches only the prose rule, not the counts.
- Wiring any new guard into CI jobs, and guard wiring hygiene generally, is owned by `harden-ci-guard-wiring`. This change adds the guard itself (if decided) and a single invocation.
- Root `doc.go` content changes, such as the Trust boundary, casbin and stability sections, are owned by `define-access-and-disclosure`, `remove-casbin`, `replace-casbin-with-composed-deciders`, `add-instance-access-hook` and `document-stability-policy`. This change rewrites only link syntax.
- Restoring `SECURITY.md`, `STABILITY.md` or ADRs. #52 retired them. If `document-stability-policy` chooses a `STABILITY.md` file, the guard's `stability-md` row narrows itself with a notice, and that change owns the follow-up.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected. The changes are comments, contributor and agent docs, and possibly a test-only guard.

**Coordination**
- Size: M
- Depends on: nothing
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. The change edits comments, contributor and agent docs, and possibly a test-only guard. No library behavior changes, so `skip_specs: true` is set.

## Impact

- **Comments only:** `doc.go`, `transport/http/stdlib/{groups,write}.go`, `transport/http/gin`, `transport/http/fiber`, `persistence`, `eventing/message.go`, `definition/model/validation_wire.go`, `runtime/driver_shutdown.go`, `runtime/kernel/shutdown.go`, `examples/scenarios/{event_start,input_validation}/main.go`.
- **Scripts and docs:** the `scripts/check-doc-refs.sh` header, and `docs/agents/` plus the CLAUDE.md agent-docs index if decision 2 adds a convention.
- **Possibly new:** a test-only doc-link guard (decision 1), plus its invocation from CI.
- **Shared files:** root `doc.go` is edited by five other active changes. This change rewrites link syntax only, so whichever change lands second rebases. There is no ordering dependency.
- **Not affected:** exported identifiers, runtime behavior, persisted formats, the default security posture, any consumer's `go.mod`.
