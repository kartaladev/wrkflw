# Proposal

## Why

Two questions about removed dependencies are still unanswered. Nothing in CI stops a removed module coming back: goavro has already been reported removed while it was still in the graph (kartaladev/wrkflw#113). And no rule says what happens to a deny-list entry once the dependency it names has left `go.mod`. watermill has already left, and casbin leaves with `remove-casbin`. So `watermill` is now written in two lists, and nothing tells a reader which of them is the real gate. Each further removal adds another such entry. The answers should be decided once, before casbin's entries set a precedent by default.

## What Changes

Every premise below was re-measured on `main` (`4763d36e`, go1.26.9).

| Premise (source) | Re-measurement |
|---|---|
| `deniedEngineImports` at `engine/purity_test.go:161-173`, with `watermill` at `:168` and `casbin` at `:171` (#125) | **Holds.** The var starts at `:161`, `"watermill"` is at `:168` and `"casbin"` at `:171`. The doc comment at `:159` still names watermill. |
| `CONTRIBUTING.md:47-49` reads "Never import watermill, casbin, gocron, or clockwork" (#125 body) | **Stale.** The sentence is now at `CONTRIBUTING.md:122-123`, and #131 already struck watermill from it: "Never import casbin, gocron, or clockwork". |
| After #131, `git grep -in watermill` returns 6 lines (#125 comment 2) | **Stale count, conclusion holds.** It returns 8 lines. Two are new prose comments (`eventing/inprocess.go:476`, `eventing/inprocess_test.go:637`). There are still zero imports, nothing in `go.mod`, and nothing in `go list -m all`. |
| The eventing module-graph ban makes the engine entry redundant (#125, reading 3) | **Holds for watermill's module path.** `go list -deps ./eventing` includes `github.com/kartaladev/wrkflw/engine`, so a watermill import in `engine` would also fail `TestEventingDependencyGraphIsMessagingVendorFree`. The engine entry is a substring match (`"watermill"`) and is broader than the eventing prefix match, but the only difference is a fork published under another owner. |
| casbin is still a live dependency (#125 comment 2) | **Holds.** `github.com/casbin/casbin/v2 v2.135.0` is a direct require. `go mod why -m` reaches `casbin/govaluate` and `bmatcuk/doublestar/v4` only through `casbinauthz` and casbin. |
| goavro is gone and nothing guards against its return (#124) | **Holds.** `goavro` and `snappy` appear in no `go.mod`, `go.sum`, Go file or script, and in no `go list -m all` output (242 modules). The only module-ban guards are `eventing/vendorfree_test.go` and `service/vendorfree_test.go`, and each covers one package's build graph. |
| goavro was in the *module* graph but never in a consumer's *build* graph (#20 register, #113 entry) | **Holds, and it matters.** No package imported `definition/model/validate/avro`. A guard over a consumer-shaped build graph would therefore not have caught the regrowth #113 reported. Only a guard over `./...` or over the module graph would have. |

What the change does, **once the Open Questions in design.md are answered**:

- **Write down a single policy for removed dependencies.** The policy lives in a comment on `deniedEngineImports` and in the *Engine purity* bullet of `CONTRIBUTING.md`. It is applied to watermill now and to casbin, govaluate and doublestar now that `remove-casbin` has landed.
- **Add a guard against regrowth.** kartaladev/wrkflw#124 asks whether goavro should get a guard, and whether that guard should be goavro-specific or a general list with a stated reason for each entry. The recommended option is a single Go test over the workspace module graph. It adds no CI job.
- **Settle the duplication #125 found.** Each list says what it covers, so a reader can tell which one blocks which regression.

Absorbed issues:
- kartaladev/wrkflw#125, **folded in** as Open Questions 1, 3 and 4. Following the #131 review's recommendation, which the maintainer endorsed in the thread, the watermill and casbin entries are decided as separate cases under one policy. The casbin case is resolved here, after `remove-casbin`, not in `remove-casbin` itself. The `CONTRIBUTING.md` line reference is **stale** (see table).
- kartaladev/wrkflw#124, **folded in** as Open Question 2.
- No issue is superseded.

**Decisions not yet made** (design.md, Open Questions). The threads lay out options but record no outcome, so Readiness is human:
- **Q1:** what happens to an engine-import deny-list entry once its module leaves `go.mod`.
- **Q2:** whether goavro gets a guard, and in what form (none, a goavro-specific script, or a general list), checked against which graph.
- **Q3:** how a general list relates to the spec-backed ban in `eventing/vendorfree_test.go`.
- **Q4:** which of casbin, govaluate and doublestar the list bans, and whether snappy, which left with goavro, belongs on it.
- **Q5:** whether the ban becomes a spec requirement (in `module-dependencies`) or stays a test-only guard.

**BREAKING:** none. **Persisted formats:** not affected. **Default security posture:** not affected. **Consumer go.mod:** not affected. The guard only keeps out modules that are already absent.

**Out of scope**
- Removing casbin, govaluate and doublestar, and the interim decision to keep the `"casbin"` entry. `remove-casbin` owns both (its tasks 1.5 and 1.7). This change revisits that entry only after `remove-casbin` lands.
- The `eventing` vendor-free requirement in `openspec/specs/event-delivery/spec.md`. This change does not modify it. Q3 decides only how the new list refers to it.
- The `module-dependencies` capability and `service/vendorfree_test.go`. `split-persistence-modules` creates the capability and moves that test. If Q5 promotes the ban to a requirement, a later change adds it once that capability is archived.
- Wiring a new CI job, and the guard-wiring meta-checks such as kartaladev/wrkflw#233. `harden-ci-guard-wiring` owns these, and they only matter if Q2 picks a script.
- The doc comment of `ErrUnserializableValidation`, which names packages that no longer exist (kartaladev/wrkflw#123, a goavro follow-up). `fix-doc-rot` owns it.
- The release-note lines for removed dependencies. `cut-v0-release` owns them.

**Coordination**
- Size: S
- Depends on: remove-casbin
- Blocks: nothing
- Readiness: human

## Capabilities

### New Capabilities

None.

### Modified Capabilities

None. `skip_specs: true` is set. The change adds a test-only guard and edits contributor documentation, and the library's observable behavior does not move: every module the guard bans is already absent, so a consumer's build and `go.mod` stay the same. A behavior-level home for "a consumer's build never contains module X" does exist. It is the `module-dependencies` capability, but `split-persistence-modules` has not yet archived it, and adding a delta against an unarchived capability would couple this S change to an XL change with human readiness. Q5 records the option of promoting the ban later.

## Impact

- **Tests:** `engine/purity_test.go` (the `deniedEngineImports` entries and their doc comment), plus a new root-module test holding the banned-module list if Q2 picks the list form. Under some Q3 options, `eventing/vendorfree_test.go` gets a comment only.
- **Docs:** `CONTRIBUTING.md:122-123` (the *Engine purity* bullet), and one sentence stating the removed-dependency policy.
- **Not affected:** exported identifiers, production code, `go.mod` and `go.sum`, persisted formats, the default security posture, and CI workflow files (unless Q2 picks a script).
