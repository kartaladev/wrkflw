## ADDED Requirements

### Requirement: Reassign eligibility is validated like eligibility and only beside claimant_only
Definition validation MUST refuse a user task whose reassign eligibility sets both roles and privileges with `model.ErrRolesAndPrivileges`, and one whose reassign eligibility is present but empty, or present without `claimant_only`, with `model.ErrInvalidReassignEligibility`; each error SHALL name the node (pending Q2).

#### Scenario: Validating reassign eligibility
```gherkin
Scenario Outline: Reassign eligibility declarations
  Given a user task "approve" declaring <declaration>
  When the definition is validated
  Then the result is <result>

  Examples:
    | declaration                                                                  | result                                                                  |
    | claimant_only and reassign eligibility with roles and privileges             | model.ErrRolesAndPrivileges, message names "approve"                    |
    | claimant_only and an empty reassign eligibility                              | model.ErrInvalidReassignEligibility, message names "approve" (pending Q2) |
    | reassign eligibility with roles, without claimant_only                       | model.ErrInvalidReassignEligibility, message names "approve" (pending Q2) |
    | claimant_only and reassign eligibility with roles only                       | valid                                                                   |
    | claimant_only and reassign eligibility with privileges and a predicate       | valid                                                                   |
    | claimant_only and reassign eligibility with a predicate only                 | valid                                                                   |
    | claimant_only alone                                                          | valid                                                                   |
    | neither                                                                      | valid                                                                   |
```

#### Scenario: Registration refuses the same defects
```gherkin
Scenario: The authoring gate covers reassign eligibility
  Given an empty in-memory definition registry
  And definition "gated" whose user task declares reassign eligibility without claimant_only
  When the definition is registered
  Then registration fails with ErrInvalidDefinition
  And the error also matches model.ErrInvalidReassignEligibility
```

### Requirement: Registering a claimant-only task without reassign eligibility warns
Registering a definition through `RegisterDefinition` or `MustRegisterDefinition` SHALL log one WARN per user task, at any subprocess depth, that declares `claimant_only` without reassign eligibility, naming the definition and the node, and SHALL still register the definition.

#### Scenario: Stranded-task warning
```gherkin
Scenario Outline: Warning on registration
  Given definition "d1" with <tasks>
  When it is registered through RegisterDefinition
  Then registration succeeds
  And the log holds <warnings>

  Examples:
    | tasks                                                                          | warnings                                    |
    | user task "a" with claimant_only and no reassign eligibility                   | exactly one WARN naming "d1" and "a"        |
    | user task "a" with claimant_only and reassign eligibility                      | no WARN about reassign eligibility          |
    | user task "a" without claimant_only                                            | no WARN about reassign eligibility          |
    | user tasks "a" and "b" with claimant_only and no reassign eligibility          | two WARNs, one naming "a", one naming "b"   |
    | a subprocess holding user task "s" with claimant_only and no reassign eligibility | exactly one WARN naming "d1" and "s"     |
```
