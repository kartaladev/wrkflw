## Purpose

When an instance that cannot make progress, or that has lost optional behavior, raises an operator-visible incident or a warning instead of parking silently, and which incidents an operator may resolve.

## ADDED Requirements

### Requirement: A token stranded by a definition defect raises a definition-defect incident
When a token reaches a node its definition specifies so incompletely that no trigger can ever resume it, the engine SHALL move the token to `TokenIncident`, record one `IncidentDefinitionDefect` naming the token and node with a reason stating what is missing, schedule no work for it, and leave the instance `StatusRunning`.

#### Scenario: Each stuck shape raises a definition-defect incident
```gherkin
Scenario Outline: A definition that skipped model.Validate strands a token on "<node>"
  Given a definition in which <shape>
  When an instance starts and is driven <drive>
  Then exactly one token remains, on "<node>", in state TokenIncident
  And exactly one incident exists, of kind IncidentDefinitionDefect, naming that token and "<node>"
  And the incident's error mentions "<reason>"
  And the instance status is StatusRunning

  Examples:
    | shape                                                       | node           | drive                                | reason                              |
    | an intermediate catch declares no timer, signal or message   | catch-nothing  | from start                           | no timer, signal or message trigger |
    | an intermediate throw declares no signal                     | throw-nothing  | from start                           | signal                              |
    | a sub-process carries no nested definition                   | sp             | from start                           | nested definition                   |
    | a service task has no outgoing flow                          | dead           | until its action completes           | outgoing flow                       |
    | a user task has no outgoing flow                             | dead           | until its human task completes       | outgoing flow                       |
    | a signal throw has no outgoing flow                          | dead           | from start                           | outgoing flow                       |
    | a send task has no outgoing flow                             | dead           | from start                           | outgoing flow                       |
```

#### Scenario: A stranded catch, throw or sub-process schedules nothing
```gherkin
Scenario Outline: No work is emitted for a node that can never advance
  Given a definition in which <shape>
  When an instance starts
  Then the step emits no command

  Examples:
    | shape                                                     |
    | an intermediate catch declares no timer, signal or message |
    | an intermediate throw declares no signal                   |
    | a sub-process carries no nested definition                 |
```

#### Scenario: Well-formed neighbors are not incidents
```gherkin
Scenario Outline: Negative control - the same node kinds with what they need
  Given a definition in which <shape>
  When an instance runs
  Then <outcome>

  Examples:
    | shape                                         | outcome                                              |
    | an intermediate catch waits on a signal        | the token waits and resumes when the signal arrives   |
    | an intermediate throw declares a signal        | a ThrowSignal command is emitted                      |
    | every branch ends in an end event              | the instance reaches StatusCompleted with no tokens   |
```

### Requirement: An instance holding a definition-defect incident can still be cancelled
An instance carrying an `IncidentDefinitionDefect` SHALL remain cancellable: a `CancelRequested` trigger MUST end it at `StatusTerminated`, so the defect is a stop and not a wedge.

#### Scenario: Cancelling an instance with a defect incident
```gherkin
Scenario Outline: An operator retires a stranded instance
  Given an instance holding an IncidentDefinitionDefect raised by <shape>
  When a CancelRequested trigger is applied
  Then the step succeeds
  And the instance status is StatusTerminated

  Examples:
    | shape                                                    |
    | an intermediate catch declaring no trigger                |
    | an intermediate throw declaring no signal                 |
    | a signal throw with no outgoing flow                      |
```

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
```

#### Scenario: A terminal instance answers with the terminal refusal
```gherkin
Scenario: ResolveIncident on an instance whose compensation walk was abandoned
  Given an instance that raised a compensation-stall incident and was then terminated
  When ResolveIncident names that incident
  Then the step fails with ErrInstanceTerminal
  And the error is not ErrIncidentNotResolvable
```

### Requirement: A boundary that can never be armed is warned about, not raised as an incident
When a timer boundary is attached to a host that cannot arm it (send task, sub-process or call activity) on a definition that skipped validation, the engine SHALL NOT arm it, SHALL log a warning naming the host and the boundary, and MUST still run the host normally; an error boundary on such a host MUST NOT produce that warning.

#### Scenario: Unarmable boundary hosts warn and still run
```gherkin
Scenario Outline: A timer boundary attached to a <host>
  Given a definition, refused by model.Validate with ErrBoundaryTriggerHost, that reaches the engine
  When an instance starts and reaches the host
  Then a WARN log names host "host" and boundary "bnd"
  And no boundary is armed
  And <host effect>

  Examples:
    | host          | host effect                             |
    | send task     | a SendMessage command is emitted        |
    | sub-process   | the sub-process opens its child scope   |
    | call activity | a StartSubInstance command is emitted   |
```

#### Scenario: An error boundary on a call activity does not warn
```gherkin
Scenario: Negative control - an error boundary is a legitimate attachment
  Given an error boundary with code "E1" attached to a call activity
  When an instance starts and reaches the call activity
  Then no "boundary event cannot be armed" warning is logged
```

### Requirement: A compensation action that stops reporting back raises a stall incident
When `StepOptions.CompensationStallAfter` is non-zero and a dispatched compensation action has not reported back within it, the engine SHALL raise one `IncidentCompensationStall` naming that command and node, with no token, and emit no command; when the window is zero no stall timer SHALL be scheduled.

#### Scenario: The stall window elapses
```gherkin
Scenario: A cancel-started compensation walk whose action never replies
  Given CompensationStallAfter is set and a compensation action has been dispatched
  When the stall timer fires
  Then exactly one IncidentCompensationStall exists, naming the dispatched command and its node, with an empty token ID
  And the step emits no command
```

#### Scenario: A dying walk still reports its stall
```gherkin
Scenario: The stall is detected on a cancel-started, terminating walk
  Given a cancel-started compensation walk that will terminate the instance
  When the stall timer fires
  Then exactly one IncidentCompensationStall is raised
```

#### Scenario: A zero window disables detection
```gherkin
Scenario: Negative control - CompensationStallAfter is zero
  When a compensation walk dispatches its action
  Then no stall timer is scheduled
```

### Requirement: A compensation action that fails raises a compensation-failed incident
When a dispatched compensation action replies that it failed, the engine SHALL log a warning and raise one `IncidentCompensationFailed` naming the failed command, its node and the error, with no token; with no compensation retry policy the walk SHALL skip that record and dispatch the next one.

#### Scenario: A failed compensation is visible and the walk advances
```gherkin
Scenario: The first of two compensation records fails
  Given a cancel-started compensation walk that dispatched "undo-beta"
  When "undo-beta" replies ActionFailed with "undo-beta blew up"
  Then a WARN log names the instance, command, node "beta" and the error
  And exactly one IncidentCompensationFailed names that command and node "beta" with an empty token ID and error "undo-beta blew up"
  And the next compensation action "undo-alpha" is invoked
```

### Requirement: Compensation-walk escape verbs act only on the walk in flight
A `ResolveCompensationStall` trigger (retry, skip or abandon) SHALL fail with `ErrNoCompensationWalk` when no compensation walk is in flight, with `ErrCompensationCommandMismatch` when its command ID is not the walk's dispatched command, and with `ErrStallIncidentNotFound` when it names an incident ID that is not an open compensation-stall incident.

#### Scenario: Escape verbs refuse a missing or mismatched walk
```gherkin
Scenario Outline: <verb> is applied <situation>
  Given an instance that completed three compensable activities
  When <verb> is applied <situation>
  Then the step fails with <error>

  Examples:
    | verb    | situation                                       | error                          |
    | retry   | to a running instance with no walk in flight    | ErrNoCompensationWalk          |
    | skip    | to a running instance with no walk in flight    | ErrNoCompensationWalk          |
    | abandon | to a running instance with no walk in flight    | ErrNoCompensationWalk          |
    | retry   | naming a command the walk has moved past        | ErrCompensationCommandMismatch |
    | skip    | naming a command the walk has moved past        | ErrCompensationCommandMismatch |
    | abandon | naming a command the walk has moved past        | ErrCompensationCommandMismatch |
    | skip    | naming an incident ID that does not exist       | ErrStallIncidentNotFound       |
```

#### Scenario: Each verb is accepted on the walk it names
```gherkin
Scenario Outline: Negative control - <verb> names the dispatched command of a stalled admin walk
  Given a cancel-started walk that dispatched "c3" and stalled
  When <verb> is applied naming that command
  Then <outcome>

  Examples:
    | verb    | outcome                                                                                   |
    | retry   | "c3" is dispatched again under a fresh command ID and the stall incident is retired        |
    | skip    | "c3" is given up on, "c2" is dispatched and the instance stays StatusCompensating          |
    | abandon | the instance ends StatusTerminated keeping only the records the walk never dispatched      |
```

### Requirement: Abandon is refused on a compensation walk that resumes
An abandon `ResolveCompensationStall` SHALL fail with `ErrCompensationWalkResumes` when the walk in flight finishes by resuming the instance (a compensation throw, a partial rollback or a full reverse), and skip MUST remain accepted on that walk.

#### Scenario: Abandon on a scope-wide compensation throw
```gherkin
Scenario: A scope-wide compensation throw has dispatched "undoB"
  When abandon is applied naming "undoB"
  Then the step fails with ErrCompensationWalkResumes
```

#### Scenario: Skip still drains the resuming walk
```gherkin
Scenario: Negative control - skip on the same walk
  Given a scope-wide compensation throw has dispatched "undoB"
  When skip is applied naming "undoB"
  Then the step succeeds and "undoA" is dispatched
```
