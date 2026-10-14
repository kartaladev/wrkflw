## ADDED Requirements

### Requirement: A cyclic subprocess graph is refused
`model.Validate` SHALL refuse with `ErrCyclicSubprocess`, naming the subProcess node that closes the cycle, a definition whose subprocess graph contains a definition enclosing itself at any depth, so registration and publishing refuse it with `ErrInvalidDefinition` before any encoding. Pending: design OQ1 decides the sentinel and this rule.

#### Scenario: Validation refuses the cycle
```gherkin
Scenario Outline: A cycle fails validation by name
  Given a definition "d" built in Go with a start event, a subProcess node "sub" and an end event, connected in that order
  And <cycle>
  When "d" is validated
  Then validation fails with ErrCyclicSubprocess
  And the error names "sub"

  Examples:
    | cycle                                                                             |
    | the definition of "sub" is "d" itself                                             |
    | the definition of "sub" contains a valid subProcess whose definition is "d"       |
```

#### Scenario: Registration refuses the cycle and claims no key
```gherkin
Given an empty in-memory definition registry
And a definition "loop" whose subProcess node's definition is "loop" itself
When the definition is registered
Then registration fails with ErrInvalidDefinition
And the error also matches ErrCyclicSubprocess
And looking up Latest("loop") returns ErrDefinitionNotFound
And the process keeps running
```

#### Scenario: Negative control - a shared acyclic subprocess definition validates
```gherkin
Given a valid definition with two subProcess nodes that share one valid subprocess definition
When the definition is validated
Then validation succeeds
```
