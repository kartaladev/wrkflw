# Design

## Context

`scripts/check-openspec.sh` currently checks two things. `check_config` runs on a directory that holds `openspec/`. `self_test` copies `openspec/` into a scratch directory, plants each failure with `expect_fail <name> <shell command>`, and requires `check_config` to fail on it. It also requires the unmutated tree to pass. Neither looks at spec files; spec content is left to `openspec validate --all --strict`. The proposal (Why) shows that strict validation does not catch these malformed headers. CI runs the script on ubuntu-latest, and contributors run it on macOS, so it has to work with both mawk/gawk and BSD awk.

## Goals / Non-Goals

**Goals:**
- Fail on every malformed scenario header and every malformed scenario fence under `openspec/specs/` and `openspec/changes/`.
- Prove each failure mode on every run with a planted mutation, so a green run also shows the check can still fail.

**Non-Goals:**
- Parsing Markdown in general. The check knows fences, headers and nothing else.
- Replacing `openspec validate`. The check runs alongside it.

## Decisions

**D1. A separate `check_scenarios <dir>` function, called beside `check_config`.**
`check_scenarios` prints each problem as `check-openspec: <file>:<line>: <problem>` and returns 1 if there are any. The main path runs it after `check_config`. `expect_fail` gains a first argument naming the check to run (`check_config` or `check_scenarios`), so each mutation is checked by the function it targets.
- *Rejected:* folding the check into `check_config`. That function builds a probe change and calls `openspec instructions` five times, so every scenario mutation would also pay the probe's cost. A failure would also be attributed to the config.
- *Rejected:* a separate script. The self-test harness, the CI step and the CONTRIBUTING line already exist for this one.

**D2. One POSIX awk pass per file, fence-aware.**
- A line starting with ```` ``` ```` toggles the fence state.
- Lines inside a fence are skipped. This keeps the indented `Scenario Outline:` lines inside gherkin blocks legal; the real tree has many.
- Outside a fence, a header matching `^#+[ \t]*[Ss][Cc][Ee][Nn][Aa][Rr][Ii][Oo]` that is not `^#### Scenario: [^ ]` is a header problem. awk has no portable case-insensitive flag, so the pattern spells out both cases.
- A `#### Scenario:` header opens a scenario. Any other header closes the open one, as does end of file.
- A closed scenario must have counted exactly one opening fence, and that fence line must be exactly ```` ```gherkin ```` (trailing blanks allowed).
- A file that ends inside a fence fails.

*Evidence:* the archived adopt-openspec-workflow design measured that a `#### Scenario:` line inside a fence is not split off by OpenSpec, so ignoring fenced lines matches the parser. A prototype of this pass over today's tree reports 700 scenarios and no problems.
- *Rejected:* `openspec show --json` and comparing scenario counts with header counts. A count comparison misses `#### Scenario Outline: X`, which OpenSpec parses as a scenario. It is also slower, and it needs the JSON shape to stay stable across CLI versions.
- *Rejected:* grep alone. It cannot tell a header from a fenced line, or count fences per scenario.

**D3. Files in scope: every `spec.md` under any `specs/` directory, at any depth, below `openspec/specs/` and `openspec/changes/`, archive included.**
The files are found with `find`. OpenSpec only reads scenarios from these files, and proposal, design and tasks files quote headers in prose.
- *Rejected:* skipping `archive/`. Measured: all 21 archived delta specs pass today, so there is nothing to exempt. Checking the archive also covers the moment `openspec archive` moves a change there.

**D4. Header near-miss family.**
Any header depth, any case, and an optional space after the hashes. The same pattern catches `Scenario Outline:`, `Scenarios:` and `scenario:`, because each starts with `scenario` after the hashes.
- *Rejected:* listing exact bad spellings. The next typo would not be on the list.
- *Rejected:* allowing spellings that OpenSpec parses, such as `#### scenario:`. The project rule names a single form, and the risk is in that form drifting.

**D5. Self-test mutations target real files and are located by pattern, not by fixed name.**
- The main-spec mutations rewrite the second `#### Scenario:` header of the first `openspec/specs/*/spec.md`, in sorted order.
- The archive-scope mutation does the same on the first archived delta spec.
- The fence mutations edit the first scenario's fence.

If no such file exists, the self-test fails with a message naming what is missing, so a mutation can never pass vacuously against an empty tree. The planted mutations are:

1. `#### Scenario Outline:` header
2. `### Scenario:` header
3. `##### Scenario:` header
4. `####Scenario:` header (no space)
5. `#### scenario:` header (lowercase)
6. a scenario with its fence removed (no fence)
7. a scenario with a second gherkin fence
8. a scenario whose fence is ```` ```text ````
9. an unclosed fence at end of file
10. mutation 1 applied to an archived delta spec (scope)

The unmutated tree must pass `check_scenarios`. That run is the negative control: it shows fenced `Scenario Outline:` lines and well-formed headers are accepted.

## Risks / Trade-offs

- [The awk fence toggle treats any line starting with ```` ``` ```` as a fence, including a four-backtick fence.] → No spec uses one today (measured). If one appears, the check fails loudly; it does not pass silently.
- [`~~~` fences are not recognized.] → None exist today. If one wraps a header-like line, the check fails rather than passing it. This is listed as out of scope in the proposal.
- [Self-test time grows by ten scratch copies.] → The copies are of `openspec/` only and need no `openspec` CLI calls, so the added time is small next to the config probe.

## Migration Plan

None. The real tree passes as it stands. Rollback is a revert of the script.

## Cross-change coupling

The change touches only `scripts/check-openspec.sh` and `CONTRIBUTING.md`. Any other change editing the script's self-test or `expect_fail` must rebase onto D1's new `expect_fail` signature. This change owns that signature.
