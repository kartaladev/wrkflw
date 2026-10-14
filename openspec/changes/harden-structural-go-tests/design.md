# Design

## Context

See proposal.md, *What Changes*, for the measured premises. The probes were run in a scratch `git archive HEAD` copy with go1.26.9.

| Probe | Result |
|---|---|
| `KindProbeAppended` appended after `KindCompensationThrowEvent` | `TestDocGoClassifiesEveryKind`, `TestAllKindsRegistered` and `TestRecordedNodeTypeMatchesFromWire` all green |
| `parallelGateway` removed from *Matches BPMN* / moved out of the section | `doc_bpmn_test.go:174` fires `UNCLASSIFIED` in both rows |

The appended-kind row is expected for `TestRecordedNodeTypeMatchesFromWire`. It walks by registration, and an unregistered kind ends the walk, which its covered-count floor reports. It is the control that shows the other two walks are bound to a name rather than derived.

## Goals / Non-Goals

**Goals:**
- Every structural test touched here is shown red under a planted mutation of the property it guards, and green on an at-limit neighbor that it must still accept.
- Every limit these tests document is true, and none of them fails open.

**Non-Goals:**
- New structural tests beyond the Q1 fixture or tripwire.
- Other structural tests that no absorbed issue names.

## Decisions

### D1. #189: walk kinds by registration in both named-bound walks

`doc_bpmn_test.go:165` and `definition/kinds/kinds_test.go:19` adopt the walk that `nodetype_guard_test.go` already uses: iterate from `KindStartEvent` until `String()` falls back to `NodeKind(n)`, then assert a covered-count floor so that a disappearing kind is loud. The floor is today's count (17) and is written beside the walk. The probe in *Context* is the red: with a registered appended kind, the named bound skips it by construction.

- Rejected: *update the named bound to the new last constant*. That is the same mistake `kinds_test.go`'s own comment records making once already, with `KindEventBasedGateway`.

### D2. Fail-open limits are fixed; fail-closed limits stay documented, accurately

This applies to the limits block of `runtime/monitor/internal_leak_test.go`. A documented limit is checked by a mutation: it must fail closed (loud) or it gets fixed. The promoted-method limit under-reports, so it is fail-open and is either closed or tripwired under Q1. The sibling changes apply the same rule to the headers they own.

## Risks / Trade-offs

- [The `go/ast` resolution (Q1) misses aliases or generics] → The planted fixture decides; if it shows `go/types` is needed, the alternative (tripwire) is taken and the header records why.

## Migration Plan

Test files only, so there is nothing to deploy. Rollback is a revert.

## Coupling

| Change | Shared surface | Owner / order |
|---|---|---|
| `split-persistence-modules` | `internal_leak_test.go` (scan scope per module) | That change owns new modules. Either order; the second lander rebases. |
| `remove-casbin` / `replace-casbin-with-composed-deciders` | `knownOpenInternalLeaks` casbin entry | They own the entry. Q1 reconciles both self-cleaning maps against whatever entries remain at landing. |

## Open Questions

This is a decision the maintainer has not made, so Readiness is `human`. Tasks follow the recommendation and are marked *pending Q1*. A different answer changes only tasks 2.1–2.2.

**Q1 (#180). Promoted methods through an embedded unexported type.**
- **Recommended:** close it now in `go/ast`. An unexported type can be embedded only inside its own package, so a per-package pass can resolve the embedded type's methods without `go/types`. This is a hypothesis: it is proven or refuted by a planted fixture (an exported struct embedding an unexported type whose exported method names an internal type) going red. If the fixture shows `go/types` is needed (aliases, generics), fall back to the alternative.
- Alternative: keep the documented limit and add a tripwire test that fails when any exported struct embeds an unexported type that has exported methods. Today there are 0 such embeddings.
