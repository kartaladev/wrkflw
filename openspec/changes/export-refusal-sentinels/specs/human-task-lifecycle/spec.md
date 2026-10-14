## ADDED Requirements

### Requirement: A reassignment must name the current claimant as its source
The task service MUST refuse a reassignment whose `from` is not the task's current claimant (the empty string when the task is unclaimed) with `task.ErrNotCurrentClaimant`, in an error naming both actor IDs, before authorizing the reassigner and without issuing a trigger, so the journal never records a false source.

#### Scenario: Source of a reassignment request
```gherkin
Scenario Outline: The from of a reassignment is checked against the claim
  Given <task>
  When an eligible reassigner requests a reassignment from <from> to "bob"
  Then the result is <result>

  Examples:
    | task                                | from     | result                                                                      |
    | a task currently claimed by "alice" | "carol"  | task.ErrNotCurrentClaimant naming "carol" and "alice", no trigger           |
    | a task currently claimed by "alice" | ""       | task.ErrNotCurrentClaimant naming "alice", no trigger                       |
    | a task currently claimed by "alice" | "Alice"  | task.ErrNotCurrentClaimant naming "Alice" and "alice", no trigger           |
    | a task currently claimed by "alice" | "alice " | task.ErrNotCurrentClaimant, no trigger                                      |
    | an open, unclaimed task             | "alice"  | task.ErrNotCurrentClaimant naming "alice", no trigger                       |
    | a task currently claimed by "alice" | "alice"  | a HumanReassigned trigger from "alice" to "bob"                             |
    | an open, unclaimed task             | ""       | a HumanReassigned trigger from "" to "bob"                                  |
```

#### Scenario: The refusal is not an authorization denial and keeps its HTTP status
```gherkin
Scenario: A mismatched source is neither a denial nor a reclassified error
  Given a task currently claimed by "alice"
  When an eligible reassigner requests a reassignment from "carol" to "bob"
  Then the error matches task.ErrNotCurrentClaimant
  And the error does not match authz.ErrNotAuthorized
  And the authorizer was not consulted
  And the HTTP error classifier renders the error as 500
```
