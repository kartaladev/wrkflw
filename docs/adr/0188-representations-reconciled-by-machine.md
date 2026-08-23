# 188. Machine-checked reconciliation of a node's parallel representations — REJECTED

- Status: **Rejected** (owner decision, 2026-08-25 — proposed, audited, and declined)
- Date: 2026-08-25
- Spec (retained for its evidence): `docs/specs/2026-08-24-eligibility-representation.md`
- Audit: `docs/plans/sweep-evidence/audit-0188-adjudication.md` and the four lens reports
- Superseded plan: `docs/plans/2026-08-25-representations-reconciled.md` — **do not execute**
- Produced: backlog **141**, **143**, **144**, and the `humantask.Clone()` finding, all of which are
  fixed **directly** instead

## Context

A node's fields are declared in five Go types — `authz.AuthzSpec`, `activity.UserTask`,
`model.NodeWire` (44 exported fields, the persisted contract), `model.nodeYAML` (39, the YAML
authoring form), and `humantask.HumanTask.Eligibility` — and copied between them by hand-written
assignments at `yaml.go:112-114`, `activity.go:240`, `activity.go:251` and `engine/step_nodes.go:724`
(plus, found during the audit, `humantask.Clone()`). Each is a struct literal that stays valid when a
field is added on one side and forgotten on the other.

The risk is real and has a record: ADR-0185-core's D3 needed to add one boolean and **missed
`nodeYAML` entirely**, which under ADR-0167's strict decoding would have shipped a user task
unauthorable in YAML.

This ADR proposed closing that class with three reflective guards: a value round-trip through the
user-task conversion pair, a self-cleaning `nodeYAML`-vs-`NodeWire` coverage list, and a two-way
`UserTask`↔`AuthzSpec` correspondence.

## Decision

**Rejected. Do not build it.** Three grounds, in order of weight.

### 1. It has zero user-facing value, and it is scaffolding for parked work

The proposal changes test files and one line of a documentation generator. No consumer of this
library could observe whether it shipped. Its only beneficiary is a future contributor adding a field
to the eligibility concept — and the delivery it was built to enable, **ADR-0185 D3, is itself parked
after three failed audits**. Building infrastructure for deferred work, ahead of the user-facing
security items still open (backlog 51/52/53, 103, 124), is the wrong order.

### 2. As designed it did not close the class — proven by execution

The guards compare field **names**; the defect is field **copies**. Two audit lenses independently
implemented ADR-0185 D3 in full, omitting only the mint site at `engine/step_nodes.go:724`, and
**all three guards stayed green** — the new authorization field shipping at its zero value, in the
**fail-open** direction. A fourth lens established that one of the three guards **cannot compile**
where prescribed (`model.specFor` is unexported; the internal-package workaround is an import cycle).

This is fixable — value-guard the two data-moving sites off the correspondence table — but that is a
further design-and-audit cycle for something ground 1 says should not be first in the queue.

### 3. The repo already had most of it, and the argument for the chosen shape did not hold

- `definition/model/strict_decoding_test.go:519`
  `TestAllDeclaredYAMLTagsParseUnderStrictDecoding` already **parses `yaml.go`'s source** to derive
  the declared tag set across four files and requires each to be exercised — a derived, self-cleaning
  guard in exactly the style this ADR claimed to introduce. The right move was to **extend** it.
- The ADR's rejection of the alternative (embedding a shared struct into `NodeWire`) rested on
  `node_wire.go:119`'s legacy `ReadTrigger` path proving the wire/domain seam is load-bearing. The
  path is real, but **eligibility has no legacy form**, so the inference does not carry: embedding on
  the real 44-field `NodeWire` builds clean and emits byte-identical JSON. Only the consistency
  argument survived, which is weaker than what was presented.

## Consequences

### Positive

- The four defects this work *discovered* are fixed directly and cheaply, without a framework:
  **143** (`event.WithBoundaryAction`/`WithBoundaryErrorExpr` unreachable from YAML — the one
  genuinely user-facing item), **144** (YAML cannot author the nested trigger forms), **141**
  (`SECURITY.md` understates policy-at-rest locations, plus the hardcoded pin at
  `internal/atrest/render_test.go:227`), and `humantask.Clone()`'s false safety comment.
- Effort returns to work users can observe.

### Negative

- ⚠ **The representation trap remains.** Whoever next adds a field to the eligibility concept must
  edit every site by hand, and a miss is still silent at `yaml.go`'s mapping and at
  `engine/step_nodes.go`'s mint site. **ADR-0185 D3's plan must carry that warning explicitly**, with
  the site list, rather than relying on this ADR having shipped.
- The audit's two working guards (`nodeYAML` coverage and the eligibility correspondence) are
  discarded along with the two that did not work. Reviving just those two remains available and cheap
  if the trap bites again.

### Neutral

- The spec and audit evidence are retained: they carry executed field-set derivations (44/39 and the
  five-field difference, derived twice), the `ReadTrigger` legacy-path finding, and the four defects
  above. ⚠ **Read the adjudication before citing the spec** — several of its claims were refuted,
  including *"that is the whole protection"* (§1.2) and the embedding-rejection argument (§2).
