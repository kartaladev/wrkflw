# Design

## Context

See proposal.md (Why) for motivation. The state on `main` (`b8576841`) that shapes the approach:

- **Workspace.** `go.work` is committed and uses `.` and `./examples`. `scripts/modules.sh` prints one package pattern per module and fails loudly when `go.work` and the `go.mod` files on disk disagree. After `split-persistence-modules` lands, the module count is 10 or 11, depending on that change's Q2 (cache module layout).
- **Nested requires.** #30's audit found that `go mod tidy` ignores `go.work`. Nested modules therefore require root (and each other) at a pseudo-version of a pushed commit, never with a `replace`. The first tag must rewrite those pseudo-versions.
- **No release has ever happened.** There are no semver tags, no GitHub Releases, and the proxy's version list is empty. There is no `CHANGELOG.md`. kartaladev/wrkflw#45's requirement 7 asked for a changelog line and resolved to a comment on #48, because no file existed.
- **CI.** Every job iterates `scripts/modules.sh` under the workspace. Nothing builds a module against its own `go.mod` alone, so a nested `go.mod` that only resolves through `go.work` passes CI today.
- **Go floor.** `go.mod` and `examples/go.mod` declare `go 1.26.9`. #48 comment 1 records the reason the floor left 1.25: `golang.org/x/crypto` v0.56.0, the first fix for GO-2026-6354/6355, declares `go 1.26.0`. The repository now requires x/crypto `v0.57.0`.

## Goals / Non-Goals

**Goals:**
- One command produces the lockstep tags, and it can be rehearsed with a dry run.
- Every module a consumer can name resolves through the public proxy at the release version, verified from outside the workspace.
- The release notes state every break the thread and the blocking changes recorded, each with its migration line.

**Non-Goals:**
- Automated publishing on tag push (GoReleaser, release-please). wrkflw ships source only; there is no binary to build.
- A stability or compatibility promise. That is `document-stability-policy`'s.
- Signing tags. Nothing in the thread asks for it. It can be added without changing the scheme.

## Decisions

1. **Lockstep, path-prefixed tags** (decided, kartaladev/wrkflw#30 call 2). Root is tagged `vX.Y.Z`; each nested module is tagged `<dir>/vX.Y.Z`, for example `persistence/pgx/v0.1.0`. Rejected in #30: independent versions per module, which makes a consumer solve a compatibility matrix across ten modules.

2. **The script derives the module set from `scripts/modules.sh`.** It never holds a hand list. Rejected: a literal list in the script. The module count is still open (`split-persistence-modules` Q2), and `modules.sh`'s comments record that a hand list diffed against itself was already found vacuous once in this repository.

3. **Tag in dependency order, one round per layer.** A nested module's `go.sum` needs the hash of every module it requires at `vX.Y.Z`, and that hash only exists once the required module's tag is pushed. So the script:
   - tags root at the release commit and pushes the tag;
   - for each layer of the intra-repo require graph (computed from the `go.mod` files, not hard-coded), sets the requires with `go mod edit -require`, runs `GOWORK=off GOPROXY=direct go mod tidy`, commits, tags that layer's modules at that commit, and pushes.
   It refuses a cycle in the require graph. Rejected: all tags on one commit. That commit's nested `go.sum` would have to name hashes of tags that do not exist yet. #30 reads "root tagged first and nested modules after their requires are bumped to it", which is this order.

4. **`GOPROXY=direct` while bumping.** Asking the proxy for a version before its tag is pushed can cache a not-found answer. Fetching straight from the repository avoids that. Consumer verification (decision 6) then uses the default proxy on purpose.

5. **The `GOWORK=off` job is added only after the first tag** (decided, #30 call 4). Before the tag, a nested module resolves root only through a pushed pseudo-version, which makes the job slow and fragile. After the tag, it checks that every module builds and tests against its own `go.mod`. That is exactly what a consumer gets and what the workspace hides.

6. **Verify as a consumer, per module.** From a scratch directory outside the repository, with `GOWORK=off` and the default `GOPROXY`, the check runs `go mod init`, then `go get <module>@vX.Y.Z`, then builds a file that imports one package from that module. It also requests `https://pkg.go.dev/<module>@vX.Y.Z` so each module is indexed, and confirms that the page resolves. Rejected: checking only `go list -m -versions`. That proves the tag is visible but not that the module builds without the workspace.

7. **Release notes come from one inventory** (below), not from memory. Each blocking change's tasks hand over its lines. Each line names its source, either the issue or change, and the commit. Entries are grouped as Breaking, Persisted format, Security posture, Behavior fixes, and Dependencies and toolchain.

### Release-notes inventory (input, to be completed as blockers land)

From the #48 thread (already merged on `main`):

| Source | Entry | Kind |
|---|---|---|
| #51 (`6f7793fb`), #243 (`c7b13785`) | Minimum Go is 1.26.9 (inherited from x/crypto's 1.26.0 floor, then patch bumps) | Toolchain |
| #69 (`ae1dae90`) | An unevaluable attribute predicate is a 500, not `ErrNotAuthorized`/403; still fails closed | Breaking |
| #54 (`3cd38000`) | Trigger-less intermediate throw events are refused (`ErrThrowEventMissingTrigger`) | Breaking |
| #53 (`e6c2c020`) | `RegisterDefinition` refuses a definition failing `model.Validate` | Fix, noted |
| #67 (`e413612f`) | Default outbound client refuses cross-host redirects (`ErrCrossHostRedirect`) | Breaking |
| #84 (`457d2f97`) | Default outbound client refuses https→http redirects (`ErrDowngradeRedirect`) | Breaking |
| #78 (`d89bac6d`) | Token on a node with no outgoing flow raises `IncidentDefinitionDefect` | Behavior |
| #26 (PR #136, `13d28545`) | `RegisterKind` no longer exported; `ErrForeignNodeType` | Breaking |
| #27 (#47) | `label` retired, `name` is the display string; `businessRuleTask.rule` reserved (`ErrRuleNotSupported`) | Breaking |
| #45 (`2b4ad044`) | `eventing` API reshaped; watermill leaves the module graph | Breaking, go.mod |
| #113 (`1c9f2710`) | Avro validation and goavro removed | go.mod |
| #43 (`87c7fb91`) | Trust boundary: the disclosure seam guards the unidentified, not the unauthorized | Posture |
| #107 (`0b604f44`) | `RoleAuthorizer` evaluates `Privileges`; `ErrRolesAndPrivileges` | Breaking |
| #132 (`e9ad4b36`) | `InProcess.Publish` returns `ErrNoSubscription` after all subscriptions end | Breaking |
| #183 (`c84bfacb`) | Node wire format refuses a key its kind does not read | Breaking (hand-authored only) |
| #214 (`7b8b7bbb`) | Out-of-vocabulary `termination_outcome` refused at decode | Fix |
| #120 (`b7ebc191`) | Converging gateways corrected; `Token` gains persisted `ArrivalFlowID` | Behavior, persisted |
| #212 (PR #223) | `boundaryArm.Flow` removed; old binaries error on new-shape arms | Persisted |
| #29, #117 (#41) | User-task actions receive `humantask.Call[In]` | Breaking (lands with `add-user-task-updates`) |
| #119 | `Actor.Role`, `Actor.OnBehalf`, `ErrInvalidActor` | Breaking (lands with `replace-casbin-with-composed-deciders`) |

Handed over by active changes (each one's tasks name its lines): `remove-casbin`, `replace-casbin-with-composed-deciders`, `authorize-task-triggers-in-driver`, `encode-persisted-enums-by-name` (databases from earlier builds must be recreated), `harden-definition-store-parity`, `split-persistence-modules` (consumer go.mod and the ambiguous-import note), `add-gorm-adapter` (new module), `add-fiber-v2-adapter` (new module), `define-access-and-disclosure`, `reserve-engine-variable-keys`, `persist-pending-commands`, `add-lent-transaction-seam`, `unify-http-routes-as-data`, `add-user-task-updates`, `document-stability-policy` (links to the stability statement and rolling-upgrade warning).

A thread entry whose change was later reverted or superseded is re-checked against the tagged commit before it is published. The thread itself warns that a line drafted from the issue title rather than the merged commit was wrong once (#120).

### Cross-change coupling

| Change | Coupling | Owner |
|---|---|---|
| `split-persistence-modules` | It creates the nested modules and their pseudo-version requires. This change rewrites the requires and tags them. Tagging before the split hits the ambiguous-import hazard. | It lands first. |
| `add-gorm-adapter`, `add-fiber-v2-adapter` | Each adds a module that must be tagged. Decision 2 picks them up from `go.work` with no edit here. | They land first. |
| `harden-definition-store-parity`, `encode-persisted-enums-by-name` | Both edit `0001_init.sql` in place. That is legal only while no tag exists. Tagging closes the window. | They land first. This change owns the window's closing. |
| `document-stability-policy` | Writes the README (with the link to the root `doc.go` `# Trust boundary` section, a #48 acceptance criterion carried from #43), the stability statement, the rolling-upgrade warning and the rollback posture. The notes link to them. | It lands first. |
| Every other change in Depends on | It is a #48 blocker, or its proposal lists this change under Blocks, and it hands release-note lines over. | It lands first. |

## Risks / Trade-offs

- [A tag is pushed and then found wrong] → Go tags are effectively immutable once the proxy has fetched them, and `sum.golang.org` remembers the hash. Rehearse with `--dry-run`, then with real tags on a fork's throwaway module path. Fix a bad tag with a new patch version plus a `retract` directive, never by moving the tag.
- [The proxy caches a not-found answer for a version requested before its tag existed] → Bump with `GOPROXY=direct` (decision 4). Consumer verification runs last.
- [A nested module builds only through `go.work`] → `GOWORK=off go build` per module during the bump. The post-tag CI job (decision 5) keeps it true afterwards.
- [The Go 1.26.9 floor shuts out consumers on older toolchains] → Stated in the notes either way. Q3 offers the alternative.
- [The notes miss a break] → The inventory is cross-checked against `git log --format=%s <first-commit>..vX.Y.Z | grep '!:'` and against every active change's hand-over task before publishing.
- [A blocker lands half-done] → Readiness is human. The maintainer confirms that every Depends on change is archived before running the script.

## Migration Plan

1. Confirm that every Depends on change is archived and that CI is green on the release commit, including `dependency review` on the release PR.
2. Settle Q1–Q3. Merge the release PR, which holds the notes (and `CHANGELOG.md` if Q2 says so), the script, the `CONTRIBUTING.md` section, and this change's `openspec archive`.
3. Run `scripts/release.sh --dry-run vX.Y.Z`, then `scripts/release.sh vX.Y.Z`.
4. Run the consumer verification per module, then publish the GitHub Release with the notes.
5. Add the `GOWORK=off` job in a follow-up commit, because it needs the tags to exist.

Rollback: a published version cannot be withdrawn, only retracted. Retract it in the next patch release's root `go.mod` (and in each nested module) and publish a fixed version.

## Open Questions

These are the maintainer's calls. None of them changes the script's shape or the task breakdown.

- **Q1. The exact first version.**
  - (a) `v0.1.0`.
  - (b) `v0.0.1`.
  - (c) another minor.
  - Recommendation: (a). There is no earlier release to continue from, and `v0.0.x` conventionally signals "not meant for use". Every module gets the same number either way.
- **Q2. A committed `CHANGELOG.md`, or GitHub Release notes only.** #45 requirement 7 resolved to a #48 comment only because no file existed; nobody decided it.
  - (a) A root `CHANGELOG.md` (Keep a Changelog form) that the release PR reviews, with the GitHub Release body linking to its section.
  - (b) GitHub Release notes only.
  - Recommendation: (a). The notes are then reviewed in the same PR as the code, they live in the module zip a consumer downloads, and they survive outside GitHub. Task 3.3 is conditional on this answer.
- **Q3. The Go floor.**
  - (a) Ship with `go 1.26.9` as it stands.
  - (b) Lower the `go` directive to `1.26.0`, the floor x/crypto actually imposes, and keep CI on 1.26.9.
  - Recommendation: (a). The maintainer accepted the 1.26 floor on #51. Under (b), a consumer could build with a 1.26 patch that carries the standard-library vulnerabilities govulncheck cleared. Either way the notes state it.
