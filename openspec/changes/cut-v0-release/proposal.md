# Proposal

## Why

wrkflw has never been released. `git ls-remote --tags` shows only six `audit*` markers and no semver tag, `gh release list` is empty, and `proxy.golang.org`'s version list for `github.com/kartaladev/wrkflw` is empty. A consumer can only depend on a pseudo-version, which after the module split is unsafe (the ambiguous-import hazard below). kartaladev/wrkflw#48 closes the planning map: once every release blocker has landed, cut the first `v0.x.y`, tagged per module in lockstep as kartaladev/wrkflw#30 (decided) ruled, with release notes that state every break the thread collected.

## What Changes

Every premise below was re-measured on `main` (`b8576841`).

**Absorbed issues**
- **kartaladev/wrkflw#48 (folded in).** The release itself: the tag, the release notes, and the checks that a consumer can fetch every module. Its two "unticketed" preconditions (the README and the stability statement) were later ticketed as kartaladev/wrkflw#158 and belong to `document-stability-policy`; this change verifies them, it does not write them.
- Decision built and not reopened: **kartaladev/wrkflw#30 (decided)**, call 2 (lockstep versions, one number per release, path-prefixed tags, root tagged first, a script that bumps inter-module requires and tags) and the post-tag half of call 4 (a `GOWORK=off` per-module CI job), plus its audit item that the release script rewrites the nested modules' pseudo-version requires at the first tag.

**The release**
- **A release script** (`scripts/release.sh`) owns the bump-and-tag sequence. It derives the module list from `scripts/modules.sh` (never a hand list, because the module count is still open in `split-persistence-modules` Q2). It refuses a dirty tree, a version that is not `v0.MINOR.PATCH`, or an existing tag. It rewrites every nested module's `require github.com/kartaladev/wrkflw… <pseudo-version>` to the release version and tidies, tags root `vX.Y.Z` first, then each nested module `<path>/vX.Y.Z`. A dry-run mode prints the plan without tagging.
- **A `GOWORK=off` per-module CI job**, added after the first tag exists (#30 call 4). It builds and tests each module against its own `go.mod`, which the committed `go.work` hides today.
- **Release notes** assembled from the #48 thread and from the lines each blocking change hands over (design, Release-notes inventory). Every BREAKING entry gets a migration line. The notes state the Go floor, the persisted-format breaks, and the default-posture changes explicitly.
- **Consumer verification per module.** From a scratch module outside the workspace (`GOWORK=off`, public proxy), `go get <module>@vX.Y.Z` and a build of one import succeed for every module, and pkg.go.dev resolves every module path.

**Stale premises found on re-measurement**
- #48 comment 1: "`go.mod` went to `go 1.26.8`". It is now **`go 1.26.9`** (`c7b13785`, #243), in both `go.mod` and `examples/go.mod`. The notes state the current floor.
- #48 comment 1: "the only red left is `dependency review` … Dependency graph is disabled". **Stale:** `dependency review` passes on PR #247. On `main` pushes it is skipped by design (`if: github.event_name == 'pull_request'`).
- #48 comment 1: "`golangci-lint` scans nothing" and "govulncheck verified to run". **Fixed and holds:** both jobs pass on `main` (`eebe55ba`), govulncheck is pinned at `v1.1.4` and fed every module by `scripts/modules.sh`.
- #48 body: "`README.md` does not exist". **Holds** at HEAD; owned by `document-stability-policy`.
- #48 blocker audit: "#187 real, five unfiltered paths". The GitHub issue is now **closed**, but its work is the active change `reserve-engine-variable-keys`, which still lists this change under Blocks. Kept as a dependency.
- #48's GitHub `blocked_by` list: 11 of its 27 blockers are still open (#37, #39, #40, #41, #42, #44, #106, #109, #110, #115, #158). Each maps to an active or planned change in Depends on.

**Impact flags**
- **BREAKING (release-noted, not introduced here).** This change adds no break of its own. It publishes the breaks the blocking changes and the #48 thread record.
- **Consumer go.mod (flagged, the core of this change).** The first tags make every module requirable at a semver version. Pseudo-version users must move root and every nested module together to the same `vX.Y.Z`; mixing a pre-split root pseudo-version with a post-split `persistence` module fails with "ambiguous import". The `go` directive sets a floor on every consumer (Q3).
- **Persisted formats (flagged): no change here, but tagging closes the in-place-edit window.** `harden-definition-store-parity` and `encode-persisted-enums-by-name` edit `0001_init.sql` in place on the strength that no tag exists. After this change, any schema change needs a new migration.
- **Default security posture: unchanged here.** The notes restate the posture `define-access-and-disclosure` and the #43 Trust boundary section document.

**Out of scope**
- The README, the stability statement, the rolling-upgrade data-loss warning and the rollback posture (#48 comment on #212): `document-stability-policy` (kartaladev/wrkflw#158); the history-cap default: `decide-history-cap-default` (kartaladev/wrkflw#37).
- The module split and its `go.work` entries: `split-persistence-modules` (kartaladev/wrkflw#39).
- Writing the release-note lines for each break: each blocking change hands its lines over; this change assembles them.
- Making CI guards non-vacuous: `harden-ci-guard-wiring`. This change only requires CI to be green on the tagged commit.
- Post-release work the thread names: the user-task entry action (`add-user-task-entry-action`, kartaladev/wrkflw#121) and the rule-engine adapter (`back-business-rule-task-with-rlng`, kartaladev/wrkflw#118).

**Decisions not yet made** (design, Open Questions; a release is the maintainer's call)
- **Q1:** the exact first version.
- **Q2:** a committed `CHANGELOG.md`, or GitHub Release notes only.
- **Q3:** ship with the `go 1.26.9` floor, or lower the `go` directive.

**Coordination**
- Size: M
- Depends on: add-fiber-v2-adapter, add-gorm-adapter, add-lent-transaction-seam, add-user-task-updates, authorize-task-triggers-in-driver, define-access-and-disclosure, document-stability-policy, encode-persisted-enums-by-name, harden-definition-store-parity, persist-pending-commands, remove-casbin, replace-casbin-with-composed-deciders, reserve-engine-variable-keys, split-persistence-modules, unify-http-routes-as-data, `add-user-task-updates-call-envelope`, `add-user-task-updates-http`, `add-user-task-updates-outbox`, `decide-history-cap-default`
- Blocks: add-user-task-entry-action, back-business-rule-task-with-rlng
- Readiness: human

## Capabilities

### New Capabilities

None. Release engineering changes no library behavior; `skip_specs: true`.

### Modified Capabilities

None.

## Impact

- **New:** `scripts/release.sh` and its self-test; release notes (a GitHub Release per tag, plus `CHANGELOG.md` if Q2 says so).
- **CI:** `.github/workflows/ci.yml` gains the `GOWORK=off` per-module job after the first tag.
- **go.mod:** every nested module's require of root (and of sibling modules) moves from a pseudo-version to `vX.Y.Z`.
- **Git:** one root tag and one path-prefixed tag per nested module, all `vX.Y.Z`. Root is tagged first; nested modules are tagged in dependency order on the commits that bump their requires (design, decision 3).
- **Docs:** `CONTRIBUTING.md` gains a "Cutting a release" section that names the script.
