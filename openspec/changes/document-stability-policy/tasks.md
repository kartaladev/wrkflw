# Tasks

**Do not start until design Open Questions Q1 and Q2 are answered.** Q1 decides the downgrade text in task 1.3. Q2 decides which files group 1 writes. The tasks below assume the recommended options: Q1 (a) and Q2 (a).

This change lands after `decide-history-cap-default`, whose settled default task 2.1 states, and after `split-persistence-modules`. Re-measure every path below at apply, because `split-persistence-modules` moves `internal/persistence/store` and makes `persistence` its own module. Run Go commands from inside the owning module, as `CONTRIBUTING.md` describes, with `GOTOOLCHAIN=go1.26.9`.

## 1. Stability statement (kartaladev/wrkflw#158, design D1 to D4, Q1, Q2)

- [ ] 1.1 Recover the source with `git show f524e82a:STABILITY.md`, and keep only the parts listed in design D4. Verify: the PR description lists each section of the old document as carried or dropped, with the D4 reason.
- [ ] 1.2 Re-derive the two data-loss instances on the post-split tree before quoting them (D3):
  - The `Incident.Kind` instance: confirm the field comment and that the resolve-incident endpoint still deletes an `IncidentAction`.
  - The compensation-cursor instance: find the cursor fields an older build would drop. If none exist any more, drop that instance and say so in the PR.
  - Confirm the decode is still plain, with no `DisallowUnknownFields` on the snapshot load path, and that `Token.ArrivalFlow` is still persisted.

  Verify: the `git grep` commands used, and their output, are in the PR.
- [ ] 1.3 Write the `# Stability and upgrades` section of root `doc.go` next to `# Trust boundary` (Q2 a). It covers:
  - the v0.x SemVer policy and what "public API" covers;
  - the deprecation taxonomy;
  - **Rolling upgrades are not supported**, with the mechanism stated in exported identifiers (`engine.InstanceState`, `engine.Incident.Kind`, `engine.Token.ArrivalFlow`) and no `internal/` path (D2), plus the two instances (1.2), the asymmetry and the upgrade procedure (D1);
  - the downgrade posture (Q1 a).

  Verify: `go doc github.com/kartaladev/wrkflw` prints the section. `scripts/check-doc-refs.sh` passes, so there is no ADR or change-path citation. `grep -n 'internal/' doc.go` finds no new hit in the section.
- [ ] 1.4 Shorten the `Incident.Kind` field comment's deploy warning to point at the new section, keeping the field-specific sentence. Verify: `scripts/check-doc-refs.sh`, and `go vet ./...` in the root module.

## 2. README (kartaladev/wrkflw#158, design D5, D6)

- [ ] 2.1 Write `README.md` at the repository root with these sections:
  - what wrkflw is, in the vocabulary of `CONTEXT.md`;
  - requirements: the Go version, by pointing at `go.mod`;
  - install, naming the post-split modules a consumer requires per use;
  - a quickstart copied from `examples/readme_quickstart/main.go` (D5);
  - a package map;
  - a link to the root `doc.go` `# Trust boundary` section from the wiring quickstart (the #43 acceptance criterion on #48);
  - a persistence section covering the three dialects, `WithHistoryCap`, the default as `decide-history-cap-default` settled it (unbounded under its recommended option) and its growth shape (with the benchmark named, and no figures), `WarnUnsafeConfig`, and the "recreate a database created by a pre-release build" statement (D6);
  - a "Stability and upgrades" summary that repeats "never run two builds against one instance store" and links the `doc.go` section;
  - the licence.

  Verify:
  - `test -f README.md`;
  - `grep -n 'Trust boundary' README.md`;
  - `grep -n -i 'rolling' README.md`;
  - a diff of each quickstart block against `examples/readme_quickstart/main.go` shows no drift;
  - `go build ./examples/...` in the examples module.
- [ ] 2.2 Check every module path and identifier the README names against the tree: `go list -m all` in the workspace for modules, and `go doc <pkg>.<Ident>` for each identifier. Verify: the command log is in the PR, and every lookup resolves.
- [ ] 2.3 Fix `examples/readme_quickstart/main.go`'s doc comment if it names a README section that now has a different title. Verify: `go vet ./...` in the examples module.

## 3. Integration

- [ ] 3.1 Run the whole gate: the per-module build, test and lint loop from `CONTRIBUTING.md`, plus `scripts/check-doc-refs.sh`, `scripts/check-openspec.sh` and `openspec validate document-stability-policy --strict`. Verify: all exit 0.
- [ ] 3.2 Check pkg.go.dev rendering locally: `go run golang.org/x/pkgsite/cmd/pkgsite@latest` from the repository root, then open the root module page and confirm that both the README and the `# Stability and upgrades` section render. Verify: a screenshot or note in the PR.

## Workflow follow-up

- The PR body names `document-stability-policy`. Its last commit runs `openspec archive document-stability-policy`.
- After merge, comment on kartaladev/wrkflw#158 with the change name, and close it. kartaladev/wrkflw#37 is closed by `decide-history-cap-default`.
