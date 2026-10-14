## MODIFIED Requirements

### Requirement: A boundary whose target names no node fails the step
When a fired message boundary's outgoing flow targets a node the definition does not declare, the step SHALL fail with `engine.ErrBoundaryTargetNotFound`, in an error naming the boundary; a declared node whose ID is blank MUST remain a valid target.

#### Scenario: Unresolvable and blank-but-declared boundary targets
```gherkin
Scenario Outline: An armed message boundary fires toward <target>
  Given a definition reaching the engine whose boundary "bnd" has its outgoing flow toward <target>
  When the boundary's message is received
  Then <outcome>

  Examples:
    | target                          | outcome                                                                                   |
    | an undeclared blank node ID     | the step is refused with engine.ErrBoundaryTargetNotFound, in an error naming boundary "bnd" |
    | an undeclared node "ghost"      | the step is refused with engine.ErrBoundaryTargetNotFound, in an error naming boundary "bnd" |
    | a declared node whose ID is ""  | the step succeeds and the target node's action is invoked                                 |
```
