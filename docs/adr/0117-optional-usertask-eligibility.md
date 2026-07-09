# 117. Optional, co-equal UserTask eligibility dimensions

- Status: Accepted
- Date: 2026-07-10

## Context

`wrkflw`'s authorization model is required to be role-based,
resource-privilege-based, **and** attribute-based (over process/data
variables), with the three treated as co-equal dimensions (see the
project's authorization requirement and ADR-0063 for the scoped catalog).

The `UserTask` model already carried all three dimensions —
`CandidateRoles`, `EligibilityPrivileges`, `EligibilityExpr` — and the
runtime already treated an **empty** eligibility spec as an open engine
gate, deferring authorization to the consumer's transport layer (e.g. HTTP
security middleware). The three dimensions were therefore semantically
co-equal and each independently optional at execution time.

The **authoring API** did not reflect this. The constructor was:

```go
func NewUserTask(id string, roles []string, opts ...UserTaskOption) model.Node
```

Roles were a **mandatory positional** argument, while privileges and the
attribute expression were optional functional options
(`WithEligibilityPrivileges`, `WithEligibilityExpr`). This made RBAC look
privileged over the other two dimensions and forced every call site to pass
a `roles` slice (usually `nil`) even when eligibility was expressed by
privilege or attribute — or not at all. It also made a *no-eligibility*
UserTask (needed for the manual task of ADR-0118, and for pure
transport-level authorization) awkward to express: `NewUserTask("x", nil)`.

A secondary asymmetry: the option/field/wire vocabulary mixed "candidate"
(BPMN's term for eligible users/groups) with "eligibility", so the three
dimensions did not read as one family.

## Decision

1. **Drop the positional `roles` slice.** The constructor becomes:

   ```go
   func NewUserTask(id string, opts ...UserTaskOption) model.Node
   ```

   Eligibility is set entirely through functional options; with none set,
   the engine gate is open and authorization defers to the transport layer.
   This is a breaking change. The library is unreleased, so no positional
   shim or migrator is provided — all call sites are migrated in the same
   change.

2. **Add a `WithEligibleRoles` option** as the roles counterpart to the
   existing privilege/attribute options, and **rename the whole family** so
   the three dimensions read uniformly:

   | dimension | option | `UserTask` field | wire key (JSON/YAML) |
   |---|---|---|---|
   | roles | `WithEligibleRoles(...)` | `EligibleRoles` | `eligibleRoles` |
   | privileges | `WithEligiblePrivileges(...)` | `EligiblePrivileges` | `eligiblePrivileges` |
   | attribute expr | `WithEligibleExpr(...)` | `EligibleExpr` | `eligibleExpr` |

   The former names (`WithCandidateRoles`/`CandidateRoles`/`candidateRoles`,
   `WithEligibilityPrivileges`/`EligibilityPrivileges`/`eligibilityPrivileges`,
   `WithEligibilityExpr`/`EligibilityExpr`/`eligibilityExpr`) are removed.

3. **All three dimensions are co-equal and independently optional.** Any
   combination (including none) is valid; multiple `WithEligibleRoles` /
   `WithEligiblePrivileges` calls are additive.

4. **Scope boundary.** The runtime "eligibility" concept
   (`authz.AuthzSpec` carried on `AwaitHuman`/`HumanTask` as the
   `Eligibility` field) is a **distinct** concern and is deliberately left
   untouched by the rename. "A privilege held by a role or a specific user"
   remains a casbin-adapter capability behind the `Authorizer` abstraction,
   out of scope here.

## Consequences

- RBAC is no longer privileged in the authoring API; the three eligibility
  dimensions are symmetric in name, optionality, and wire representation.
- A **no-eligibility** UserTask is now the natural default
  (`NewUserTask("x")`), which makes the manual task of
  [ADR-0118](0118-manual-user-task.md) directly expressible.
- Breaking: every `NewUserTask` call site was migrated (positional roles →
  `WithEligibleRoles`, dropped `nil`), and the JSON/YAML wire keys changed
  (`candidateRoles`/`eligibilityPrivileges`/`eligibilityExpr` →
  `eligibleRoles`/`eligiblePrivileges`/`eligibleExpr`). The golden wire
  fixture and README were updated. Unreleased ⇒ acceptable, no aliases.
- The fluent builder method `build.Builder.AddUserTask(id, roles []string,
  opts...)` still takes roles positionally. It is a separate surface from
  `NewUserTask` and was not part of this change's scope; relaxing it for
  consistency is a possible follow-up.
- The runtime `authz.AuthzSpec.Eligibility` field keeps its name; readers
  must not conflate the definition-layer `EligibleRoles`/`EligiblePrivileges`
  fields with that runtime spec.
