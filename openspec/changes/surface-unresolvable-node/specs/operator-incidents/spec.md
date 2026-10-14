## ADDED Requirements

### Requirement: A token on a node its definition does not declare raises an unresolvable-node incident
When a token is driven while sitting on a node that the definition for its scope does not declare, and no flow of that definition targets the node, the engine SHALL move the token to `TokenIncident`, record one `IncidentUnresolvableNode` naming the token, its scope and the node, schedule no work for it, and leave the instance `StatusRunning`; it MUST NOT record an `IncidentDefinitionDefect` for it.

#### Scenario: A token whose node the definition no longer declares
```gherkin
Scenario Outline: A token on "<node>" in <scope> meets a definition that does not declare it
  Given an instance state holding one active token on "<node>" in <scope>
  And the definition handed to the step has DefID "p" and Version <def version>, and the state records Version <state version>
  And no node "<node>" and no flow targeting "<node>" exists in the definition for that scope
  When a trigger that reaches the drive loop is applied
  Then the step succeeds and emits no command
  And that token is in state TokenIncident
  And exactly one incident exists, of kind IncidentUnresolvableNode, naming that token, "<node>" and the token's scope ID
  And the incident's error <version clause>
  And the instance status is StatusRunning
  And a WARN log names the instance, the token and "<node>"

  Examples:
    | node   | scope                                | def version | state version | version clause                       |
    | legacy | the root scope                       | 2           | 1             | names Version 1 and Version 2         |
    | legacy | the root scope                       | 1           | 1             | does not mention a version mismatch   |
    | inner  | the child scope of sub-process "sp"  | 2           | 1             | names Version 1 and Version 2         |
    | Legacy | the root scope, beside a node "legacy" | 1         | 1             | does not mention a version mismatch   |
    | légacy | the root scope, beside a node "legacy" | 1         | 1             | does not mention a version mismatch   |
```

#### Scenario: Other branches keep running
```gherkin
Scenario: A parallel sibling is not held by the incident
  Given an instance with one token on undeclared node "legacy" and one active token entering service task "work"
  When a trigger that reaches the drive loop is applied
  Then one IncidentUnresolvableNode names the token on "legacy"
  And an InvokeAction command is emitted for "work"
  When the action "work" completes
  Then the step succeeds and the incident is still present
```

#### Scenario: A later step does not raise the incident again
```gherkin
Scenario: Duplicate delivery - another trigger is applied after the incident
  Given an instance that already holds one IncidentUnresolvableNode for the token on "legacy"
  When a second trigger that reaches the drive loop is applied
  Then exactly one IncidentUnresolvableNode exists
  And the token is still in state TokenIncident
```

#### Scenario: A declared node is driven normally
```gherkin
Scenario Outline: Negative control - the token's node is declared
  Given an instance state holding one active token on "<node>", which the definition declares as <kind>
  When a trigger that reaches the drive loop is applied
  Then no incident is recorded
  And <outcome>

  Examples:
    | node   | kind            | outcome                                      |
    | legacy | an end event     | the instance reaches StatusCompleted         |
    | legacy | a service task   | an InvokeAction command is emitted for it    |
```

#### Scenario: Pending Q5 - a token parked before this behavior existed is left as it is
```gherkin
Scenario: Replay of an old snapshot whose token already waits on an undeclared node
  Given a snapshot in which a token is in state TokenWaiting on undeclared node "legacy" and no incident exists
  When a trigger that reaches the drive loop is applied
  Then no incident is recorded
  And that token is still in state TokenWaiting
```

### Requirement: A definition that routes to a node it does not declare raises a definition-defect incident
When a token sits on a node the definition for its scope does not declare, but a flow of that same definition targets that node, the engine SHALL raise one `IncidentDefinitionDefect` naming the token and the node, with a reason stating that a flow targets an undeclared node, and MUST NOT raise an `IncidentUnresolvableNode` for it.

#### Scenario: Pending Q2 - an unvalidated definition's flow names an undeclared target
```gherkin
Scenario: A definition that skipped model.Validate routes "a" to undeclared node "ghost"
  Given a definition start -> service task "a" -> "ghost", where no node "ghost" is declared
  When an instance starts and the action "a" completes
  Then exactly one incident exists, of kind IncidentDefinitionDefect, naming the token and "ghost"
  And the incident's error mentions "not declared"
  And no IncidentUnresolvableNode exists
```

#### Scenario: Pending Q2 - removing the flow too makes it an unresolvable node
```gherkin
Scenario: Negative control - the same token where no flow targets "ghost"
  Given an instance state holding one active token on "ghost" and a definition with no node "ghost" and no flow targeting it
  When a trigger that reaches the drive loop is applied
  Then exactly one incident exists, of kind IncidentUnresolvableNode
```

### Requirement: An instance holding an unresolvable-node incident can still be cancelled
An instance carrying an `IncidentUnresolvableNode` SHALL remain cancellable: a `CancelRequested` trigger MUST end it at `StatusTerminated`, and the incident MUST NOT be reported as the instance's terminal cause.

#### Scenario: Cancelling an instance stuck on an undeclared node
```gherkin
Scenario Outline: An operator retires the instance, with the token in <scope>
  Given an instance holding an IncidentUnresolvableNode for a token in <scope>
  When a CancelRequested trigger is applied
  Then the step succeeds
  And the instance status is StatusTerminated
  And no incident of kind IncidentAction is reported as its terminal cause

  Examples:
    | scope                               |
    | the root scope                      |
    | the child scope of sub-process "sp" |
```

### Requirement: Adding an incident kind keeps every stored kind's persisted value
Each incident kind SHALL keep the integer it is persisted as when kinds are added: `IncidentAction` 0, `IncidentCompensationStall` 1, `IncidentCompensationFailed` 2, `IncidentDefinitionDefect` 3, and `IncidentUnresolvableNode` the next value, 4. Each kind MUST render its own constant name as its string form.

#### Scenario: Persisted values and names of every kind
```gherkin
Scenario Outline: Kind <kind>
  Then <kind> is persisted as <value>
  And its string form is "<kind>"

  Examples:
    | kind                       | value |
    | IncidentAction             | 0     |
    | IncidentCompensationStall  | 1     |
    | IncidentCompensationFailed | 2     |
    | IncidentDefinitionDefect   | 3     |
    | IncidentUnresolvableNode   | 4     |
```

#### Scenario: A value no kind owns is not given a kind's name
```gherkin
Scenario: Negative control - the value after the last kind
  Then the integer 5 converted to an incident kind renders as "IncidentKind(unknown)"
```

#### Scenario: The incident survives a store round trip on every dialect
```gherkin
Scenario Outline: An instance holding an IncidentUnresolvableNode is saved and reloaded on <dialect>
  Given an instance holding one IncidentUnresolvableNode for a token on "legacy"
  When it is saved to the <dialect> instance store and loaded back
  Then the loaded instance holds that incident, of kind IncidentUnresolvableNode, naming the same token and node
  And the token is in state TokenIncident

  Examples:
    | dialect    |
    | Postgres   |
    | MySQL      |
    | SQLite     |
```

## MODIFIED Requirements

### Requirement: Only action incidents are resolvable through ResolveIncident
`ResolveIncident` SHALL act only on an `IncidentAction`, clearing it and re-invoking its token's action; for any other incident kind it MUST fail with `ErrIncidentNotResolvable` and leave the incident in place, and on a terminal instance it MUST fail with `ErrInstanceTerminal` instead.

#### Scenario: Resolving an action incident re-invokes the action
```gherkin
Scenario: Negative control - an action that exhausted its retries
  Given an instance whose service action failed terminally and raised one IncidentAction
  When ResolveIncident names that incident with extra attempts
  Then no incident remains
  And the action of the parked node is invoked again
```

#### Scenario: Non-action incidents are refused and kept
```gherkin
Scenario Outline: ResolveIncident names an incident of kind <kind> on a running or compensating instance
  When ResolveIncident is applied
  Then the step fails with ErrIncidentNotResolvable
  And the incident can still be named afterwards

  Examples:
    | kind                       |
    | IncidentCompensationStall  |
    | IncidentCompensationFailed |
    | IncidentDefinitionDefect   |
    | IncidentUnresolvableNode   |
```

#### Scenario: A terminal instance answers with the terminal refusal
```gherkin
Scenario: ResolveIncident on an instance whose compensation walk was abandoned
  Given an instance that raised a compensation-stall incident and was then terminated
  When ResolveIncident names that incident
  Then the step fails with ErrInstanceTerminal
  And the error is not ErrIncidentNotResolvable
```
