# Tasks

Apply waits for every change in the proposal's Depends on to be archived and for the maintainer's rulings on design Q1–Q3. Readiness is human: running the tag step is the maintainer's call. Re-measure the design *Context* figures (module count, `go` directive, nested pseudo-versions) on the base you start from. Run commands per module through `scripts/modules.sh` as `CONTRIBUTING.md` describes. A bare `./...` covers only the root module.

## 1. Preconditions (verify, do not build)

- [ ] 1.1 Confirm that every Depends on change is archived. Verify: `openspec list --json` shows none of them active, and `gh api repos/kartaladev/wrkflw/issues/48/dependencies/blocked_by --jq '[.[]|select(.state=="open")]|length'` prints 0, or each remaining open blocker is named in an archived change's proposal.
- [ ] 1.2 Confirm the README exists, links its wiring quickstart to the root `doc.go` `# Trust boundary` section (the #48 acceptance criterion carried from #43), and links the stability statement and rolling-upgrade warning (`document-stability-policy`). Verify: `test -f README.md` and `grep -n 'Trust boundary' README.md`.
- [ ] 1.3 Confirm CI is green on the release commit for every job, including `govulncheck` reporting `Your code is affected by 0 vulnerabilities` over every module, and `dependency review` passing on the release PR. Verify: `gh pr checks <release PR>` shows all pass.

## 2. Release script (test-first)

- [ ] 2.1 Write `scripts/release_test.sh` against a toy three-module workspace (root, a child requiring root, a grandchild requiring both) with a local bare remote, all in a temp dir. Cover these refusals: a dirty tree, `v1.0.0`, `0.1.0` with no `v`, `v0.1`, an existing tag, a module on disk missing from `go.work`, and a require cycle. Also cover a negative control (a clean tree and `v0.1.0` succeed). Watch it go red with no `scripts/release.sh`. Verify: `bash scripts/release_test.sh` fails.
- [ ] 2.2 Implement `scripts/release.sh [--dry-run] vX.Y.Z` per design decisions 2–4. It takes the module set from `scripts/modules.sh`, builds the require graph from each `go.mod`, tags root first, then bumps, tidies (`GOWORK=off GOPROXY=direct`), commits and tags one layer at a time. `--dry-run` prints every tag and commit and changes nothing. Verify: `bash scripts/release_test.sh` passes, including the toy's assertions that root's tag precedes the child's, that the grandchild's `go.mod` names `v0.1.0` for both, that no pseudo-version remains, and that `GOWORK=off go build ./...` passes in every toy module at its tag.
- [ ] 2.3 Wire `release_test.sh` into CI next to the existing script self-tests, and document "Cutting a release" in `CONTRIBUTING.md` (the script, the dry run, retract-not-move). Verify: the CI job runs it and is green, and the documented dry-run command runs as written.
- [ ] 2.4 Run `scripts/release.sh --dry-run vX.Y.Z` (Q1's version) on the release commit of the real repository. Verify: the plan lists exactly the modules `scripts/modules.sh` prints, root first, in dependency order, with every nested pseudo-version require rewritten.

## 3. Release notes

- [ ] 3.1 Assemble the notes from design's *Release-notes inventory* and from each Depends on change's hand-over lines, grouped Breaking / Persisted format / Security posture / Behavior fixes / Dependencies and toolchain. Every Breaking entry gets a migration line. Verify: every change in Depends on and every inventory row appears, or is listed as dropped with a reason.
- [ ] 3.2 Cross-check the notes against the tagged range. Re-check each thread entry against the merged commit, not the issue title. Verify: `git log --format='%h %s' <root>..HEAD | grep -E '^[0-9a-f]+ [a-z]+(\([^)]*\))?!:'` yields no commit absent from the notes. The notes also state the Go floor (Q3), the ambiguous-import instruction for pseudo-version users, and that databases from pre-release builds must be recreated (`encode-persisted-enums-by-name`).
- [ ] 3.3 If Q2 is (a): commit `CHANGELOG.md` with the release section in the release PR. If Q2 is (b): record the decision in the release PR body. Verify: the release PR diff shows `CHANGELOG.md`, or the PR body states Q2 (b).

## 4. Tag and verify as a consumer

- [ ] 4.1 After the release PR (which runs `openspec archive cut-v0-release`) merges, run `scripts/release.sh vX.Y.Z` on its merge commit. Verify: `git ls-remote --tags origin` lists `vX.Y.Z` and `<dir>/vX.Y.Z` for every module `scripts/modules.sh` printed.
- [ ] 4.2 For each module, in a fresh temp dir outside the repository, run with `GOWORK=off` and the default `GOPROXY`: `go mod init probe`, `go get <module>@vX.Y.Z`, and a build of a file importing one of its packages. Verify: every module succeeds, and `go list -m -versions <module>` lists `vX.Y.Z`.
- [ ] 4.3 Request `https://pkg.go.dev/<module>@vX.Y.Z` for each module. Verify: each page resolves and renders the module's package list, and root's page renders the README.
- [ ] 4.4 Publish the GitHub Release for the root tag with the notes from group 3. Verify: `gh release view vX.Y.Z --repo kartaladev/wrkflw` shows the notes.

## 5. Post-tag CI

- [ ] 5.1 Add the `GOWORK=off` per-module job (#30 call 4). For each module from `scripts/modules.sh`, it runs `GOWORK=off go build` and `go test` from inside the module directory. Prove it catches the target defect: on a scratch branch, give one nested module a require that resolves only through `go.work`, and watch the job go red. Then revert. Verify: green on `main` and red on the scratch branch.

## Workflow follow-up

- The release PR's last commit runs `openspec archive cut-v0-release`. Group 4 and 5 run after it merges, as the maintainer's release steps.
- After tagging, comment on and close kartaladev/wrkflw#48 with the tag and the release link.
