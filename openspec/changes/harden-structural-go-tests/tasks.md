# Tasks

Every fix is test-first. Plant the mutation, watch the test pass it (red: the guard fails open), fix, and watch the same mutation go red while an at-limit neighbor stays green. Go tasks start with the `cc-skills-golang:golang-how-to` skill and run with `GOTOOLCHAIN=go1.26.9`. No task needs Docker. This change sets `skip_specs`, so tasks name the absorbed issue and the design decision instead of scenarios.

## 1. Kind walks (#189)

- [ ] 1.1 Red: in `doc_bpmn_test.go` and `definition/kinds/kinds_test.go`, add a white-box assertion that the walk reaches the last registered kind. Verify with the probe from design *Context*: append a registered kind after `KindCompensationThrowEvent`, and the walk must report it. Today it skips it.
- [ ] 1.2 Fix: walk by registration with a covered-count floor (D1). Verify `go test -run 'TestDocGoClassifiesEveryKind' .` and `go test ./definition/kinds/`. Verify that the appended-kind probe turns `TestDocGoClassifiesEveryKind` red with an `UNCLASSIFIED` message naming the new kind, and that removing a kind's name trips the floor.
- [ ] 1.3 Items 1 and 3: record the two measured mutations (a row dropped from *Matches BPMN*, a row moved out of the section) as table rows in `doc_bpmn_test.go`'s own tests over in-memory doc text, so the guard's ability to fail is pinned rather than recalled. Verify that both rows are red, and that the unmodified text is green.

## 2. Promoted methods in the internal-leak guard (#180)

- [ ] 2.1 Red (pending Q1): add a fixture package where an exported struct embeds an unexported type whose exported method names an internal type. Verify that `TestNoExportedSignatureNamesAnInternalType`'s scanner, pointed at the fixture, reports nothing today.
- [ ] 2.2 Fix (pending Q1): resolve embedded unexported types per package and walk their exported methods. Verify that the 2.1 fixture is reported, that an embedded unexported type with only unexported methods is not (negative control), that the real tree still has 0 offenders, and that both self-cleaning maps (`knownOpenInternalLeaks`, `intentionalCapabilitySeals`) still match. If `go/types` proves necessary, record why in the header and take Q1's alternative instead.
- [ ] 2.3 Update the limits block at `runtime/monitor/internal_leak_test.go:528-537` so that it describes what remains (D2). Verify with `go test ./runtime/monitor/`.

## 3. Integration

- [ ] 3.1 Verify that `GOTOOLCHAIN=go1.26.9 go test . ./definition/kinds/ ./runtime/monitor/` passes.

## Workflow follow-up

- The PR's last commit runs `openspec archive harden-structural-go-tests`.
- After merge, comment on and close kartaladev/wrkflw#189 and #180.
