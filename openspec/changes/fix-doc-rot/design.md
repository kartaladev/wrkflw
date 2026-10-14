# Design

## Context

See proposal.md (Why, and What Changes) for the instances and how each was measured. Four facts shape the approach.

- **Doc-link resolution is per package, through its imports.** `go/doc`'s parser, which pkgsite uses, resolves `[pkg.Sym]` by checking whether `pkg` names one of the package's imports. Failing that, it treats `pkg` as a standard-library path. Root `doc.go` imports nothing, so `[runtime.X]` becomes stdlib `runtime` and `[engine]` becomes nothing. The full import path form (`[github.com/kartaladev/wrkflw/runtime.ProcessDriver]`) always resolves. It is already the form for 7 of root `doc.go`'s 14 hrefs.
- **A package-qualified link inside a package can fail for a reason of its own.** `transport/http/gin` imports gin as `ginlib`, so `[gin.IRouter]` resolves to nothing. That is 7 of the 20 literal-text links outside `doc.go`.
- **`scripts/check-doc-refs.sh` guards five deleted-document names plus `openspec/changes/` in `*.go` only.** Its header is the documented place where the scope decision is argued.
- **The existing derive-don't-describe precedent.** `scripts/check-test-timeout.sh` prints its counts from source, and `docs/agents/test-deadlines.md:11` says so. The same document then writes counts down at `:6`, `:29`, `:70` and in its table, and they are stale: 45 across 27 files written, 58 across 32 measured (figures from `size-io-test-deadlines`, re-checked here).

## Goals / Non-Goals

**Goals:**
- Every doc link in an exported package's doc comments resolves to the package its author meant.
- Every package path named in prose in a Go comment, for the instances measured here, exists.
- The `check-doc-refs.sh` header states the true reason for its scope.
- The questions from #100, #130 and #91 are put to the maintainer with measurements, rather than answered by default.

**Non-Goals:**
- Converting bracketed unexported identifiers, type parameters or builtins (72 sites) into links, or stripping their brackets. See D2.
- Fixing godoc that misdescribes behavior. `align-godoc-with-behavior` owns that.
- Verifying links live on pkg.go.dev. No module version is published (`go list -m -versions` lists none), so the `go/doc` render is the strongest check available offline.

## Decisions

### D1. Full import path form for every cross-package doc link that does not resolve through an import

The options were:
- (a) the full import path, `[github.com/kartaladev/wrkflw/runtime.ProcessDriver]`;
- (b) adding a blank or underscore import so that short names resolve;
- (c) dropping the brackets and writing plain text.

Chosen: (a). It is the form root `doc.go` already uses correctly at 7 sites, and the measurement renders it right. (b) adds an import to a package that exports nothing, purely to satisfy the doc renderer, and an unused import does not compile without `_`. Even with `_`, the name it binds is the package name, so the approach is fragile. (c) loses the link, which is the thing #130 asks to keep.

For gin and fiber, where the package is imported under an alias, the link uses the alias the file imports (`[ginlib.IRouter]`) or the full path. The PR picks one per package and states the choice in its description. The rendered href is the evidence either way.

For `[httpcore.RouteCustomizer][*http.ServeMux]`, the rendered text is two adjacent links. The rewrite is `[httpcore.RouteCustomizer] over [*http.ServeMux]` or equivalent prose, so that each link stands alone.

### D2. Scope of the sweep: misresolving and unresolved package-qualified links in exported packages

The measured classes, with counts at `4763d36e`:

| Class | Count | Disposition |
| --- | --- | --- |
| Misresolves to a stdlib or non-existent path | 9 (7 root `doc.go`, `eventing/message.go:10`, `examples/scenarios/event_start/main.go:21`) | fix |
| Package-qualified, resolves to nothing, renders as `[pkg.X]` | 9 root `doc.go` + 17 in exported packages and examples | fix |
| Same, in `internal/` | 3 | fix only if the file is touched anyway. pkg.go.dev renders `internal/` for this module, but no consumer reads it there. |
| Adjacent links that read as one (`][`) | 6 (`transport/http/stdlib`) | fix |
| Bracketed unexported identifier, type parameter or builtin | 72 | out of scope. It renders as the literal text the author wrote and points nowhere wrong. |

Rejected: sweeping the 72. They are not the defect #130 describes (a link to the wrong package), and converting them is a style decision with no measured reader harm.

The counts come from a throwaway `go/doc` render over every non-test package. The tasks re-run the render rather than trust the table.

### D3. Comment-named import paths: fix the instances, no guard (pending Open Question 1b)

A scan of every Go comment for repo-rooted slash paths (`definition/…`, `runtime/…` and so on) found 160 distinct tokens, 19 of them naming no existing directory. Only 4 of those 19 are real path claims: `validation/{expr,jsonschema,callback}` (#123) and `eventing/chain` (×2). The other 15 are prose ("the definition/engine core", "an engine/runtime one"), test-file names, or placeholders in fixtures. That is roughly a 75% false-positive rate, so the instances get fixed by hand and the guard question goes to the maintainer with that number.

### D4. `check-doc-refs.sh` header: state the measured reason for the scope

Today's header (`:29-35`) says that non-Go prose is "the maintainer's call" and that the tree "is NOT at zero overall". Measured: every non-Go match is legitimate naming. It falls into three groups:
- the rule's self-description (`.github/workflows/ci.yml:150`, the script);
- the workflow docs that tell authors not to cite a change path (`CLAUDE.md`, `CONTRIBUTING.md`, `.claude/commands/opsx/explore.md`, `.claude/skills/openspec-explore/SKILL.md`);
- OpenSpec change artifacts recording history (`openspec/changes/archive/2026-10-14-adopt-openspec-workflow/proposal.md`, and three files of `document-stability-policy`, which name `STABILITY.md` because restoring it is that change's subject).

So the scope is Go-only because non-Go files legitimately *name* these things. It is not Go-only because dangling references are waiting outside Go. The rewrite says that, lists the three groups, and keeps the `NARROWABLE, NOT DELETABLE` paragraph. If Open Question 3 is answered "widen", this decision is replaced by the widening design and the header is rewritten for that instead.

### D5. Cross-change coupling

- **Root `doc.go`** is shared with `define-access-and-disclosure`, `remove-casbin`, `replace-casbin-with-composed-deciders`, `add-instance-access-hook` and `document-stability-policy`, which adds a `# Stability and upgrades` section. This change edits only link syntax. There is no ordering dependency, and whichever lands second rebases. A section a sibling adds later should use the D1 form. If the guard from Open Question 1a lands first, it enforces that.
- **`docs/agents/test-deadlines.md`** is shared with `size-io-test-deadlines`, which owns the counts and the Timeout "Wants to be" cell. This change owns the rule for written figures (Open Question 2). If that rule requires a figure to carry its measurement commit, `size-io-test-deadlines`'s re-derived counts must comply. Whichever change lands second conforms. Neither depends on the other.
- **CI wiring.** A new guard (Open Question 1a) is invoked from the existing lint or test job. Wiring hygiene, such as job names, required checks and duplicate invocations, is owned by `harden-ci-guard-wiring`.
- **`transport/http/{stdlib,gin,fiber}`** comments are rewritten wholesale by `unify-http-routes-as-data`. If that change lands first, re-run the render on its output instead of patching the old text.
- **`split-persistence-modules`** may move `persistence` packages, which changes their import paths. Full-path links written here then need updating, and the guard from Open Question 1a would catch any that were missed.

## Risks / Trade-offs

- [Full import paths make doc comments long] → Only cross-package links need them. A short local link (`[ProcessDriver]` inside `runtime`) stays short.
- [The render measurement is not pkg.go.dev itself] → It runs the same `go/doc` parser and the same default URL scheme. Once `cut-v0-release` publishes a version, one spot check on pkg.go.dev of root `doc.go` confirms it, and that check is a task.
- [Conventions without a check rot too] → This is the trade #100 itself names. The recommendation accepts it on the measured evidence that no candidate check scores above 2 of 7.

## Open Questions

These are the maintainer's decisions. None of them blocks the instance fixes (tasks 1–4), and each gates one task group.

1. **(#130, #123) Doc-reference guards.**
   - (1a) A doc-link guard. The options are:
     - (i) a Go test in the root module that renders every package's doc comments with `go/doc` and fails on any href whose import path is neither in `go list std` nor in the module's own package list (offline, no network);
     - (ii) the same as a `scripts/` program;
     - (iii) no guard, plus a note in `docs/agents/` on how to check a doc link by rendering it.

     **Recommendation: (i).** Measured against this change's own instances, it would have caught all 9 misresolving links and all 29 package-qualified unresolved links: 38 of 38. It would not catch the `][` adjacency, so the test also flags adjacent doc links. The examples module needs its own invocation.
   - (1b) A guard for comment-named paths written as plain text. **Recommendation: no.** D3 measured about 75% false positives.
2. **(#100) Keeping descriptions true.** The issue's options are scored here against the six instances plus the seventh found in `test-deadlines.md`. "Caught" means the option would have flagged or prevented the instance in the tree. Two of the six (the "every category is now at zero" and "13 are on ordinary assertions" claims) live in issue text, and no option can reach them.

   | Option | Caught | Which |
   | --- | --- | --- |
   | 1. duplication check | 1/7 | the three-copy job-name description |
   | 2. one description per script; other copies link | 2/7, by prevention | the `eventually-waits.md` scope claim, the three-copy description |
   | 3. derive figures; a written figure is a defect | 2/7 | "125 ADRs", the 45/27 counts in `test-deadlines.md` |
   | 4. accept and document | 0/7 | none |

   **Recommendation:** adopt 2 and 3 as **written conventions** in `docs/agents/`, indexed from CLAUDE.md "Agent docs". Under 3, a figure in prose either carries the commit or date it was measured at, or is replaced by the command that derives it. Also write 4's note that review is the guard for descriptions. Ship no mechanical check. Option 1 alone scores 1/7, which is below the issue's own bar, and a numeral detector over prose is noisy enough to be suppressed on arrival, which #91 warns against.
3. **(#91) Widen `check-doc-refs.sh` beyond `*.go`.** The options are: (a) widen to the whole tree, with explicit exclusions; (b) keep Go-only and correct the header (D4). **Recommendation: (b).** Widening fails on day one against at least 10 legitimate files (D4). Every new OpenSpec change that discusses a deleted document would need an exclusion, and the openspec workflow docs must name `openspec/changes/` to forbid citing it.
