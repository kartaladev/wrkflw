## MODIFIED Requirements

### Requirement: Task operations are refused for an actor the task's eligibility does not admit
A claim, completion or candidate refresh by an actor the task's eligibility spec does not admit MUST be refused with `authz.ErrNotAuthorized` when its trigger is applied to the instance, however that trigger was produced, and the refusal MUST leave the task and the instance unchanged.

#### Scenario: Ineligible actor is refused on each operation
```gherkin
Scenario Outline: An actor outside the eligibility spec is refused
  Given an open human task whose eligibility spec the actor "bob" does not satisfy
  When "bob" attempts to <operation> the task
  Then the attempt fails with an error wrapping authz.ErrNotAuthorized

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

#### Scenario: A refused operation changes nothing
```gherkin
Scenario Outline: The refusal leaves the task and the instance as they were
  Given an open human task whose eligibility spec the actor "bob" does not satisfy
  When "bob" attempts to <operation> the task and is refused
  Then the task's state, claim and candidates are unchanged
  And the instance's state is unchanged
  And the instance's version is unchanged
  And no journal entry is recorded for the attempt

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

#### Scenario: Eligible actor is not refused
```gherkin
Scenario Outline: An actor the spec admits proceeds
  Given an open human task whose eligibility spec the actor "alice" satisfies under the default composite authorizer
  When "alice" attempts to <operation> the task
  Then the attempt is not refused

  Examples:
    | operation           |
    | claim               |
    | complete            |
    | refresh candidates  |
```

#### Scenario: A hand-built trigger passed straight to ApplyTrigger is refused
```gherkin
Scenario Outline: No back door around the task service
  Given a ProcessDriver built with the default composite authorizer
  And an open human task whose eligibility spec the actor "bob" does not satisfy
  When a <trigger> naming "bob" as its <actor field> is built by hand and passed to ProcessDriver.ApplyTrigger
  Then ApplyTrigger fails with an error wrapping authz.ErrNotAuthorized
  And the instance's version is unchanged

  Examples:
    | trigger                 | actor field |
    | HumanClaimed            | Actor       |
    | HumanCompleted          | Actor       |
    | HumanCandidatesResolved | By          |
```

#### Scenario: Negative control - a hand-built trigger from an eligible actor is applied
```gherkin
Scenario: The back-door guard admits the legal neighbour
  Given a ProcessDriver built with the default composite authorizer
  And an open human task whose eligibility spec the actor "alice" satisfies
  When a HumanCompleted naming "alice" is built by hand and passed to ProcessDriver.ApplyTrigger
  Then ApplyTrigger returns no error
  And the task is completed
```

#### Scenario: The task service produces the trigger and the refusal comes from applying it
```gherkin
Scenario Outline: Producing a trigger is not authorizing it
  Given an open human task whose eligibility spec the actor "bob" does not satisfy
  When TaskService.<method> is called for the task with actor "bob"
  Then it returns a <trigger> and no error
  When that trigger is passed to ProcessDriver.ApplyTrigger
  Then ApplyTrigger fails with an error wrapping authz.ErrNotAuthorized

  Examples:
    | method            | trigger                 |
    | Claim             | HumanClaimed            |
    | Complete          | HumanCompleted          |
    | RefreshCandidates | HumanCandidatesResolved |
```

## ADDED Requirements

### Requirement: The driver authorizes human triggers at the transition
`ProcessDriver.ApplyTrigger` MUST consult its `authz.Authorizer` for every `HumanClaimed`, `HumanCompleted`, `HumanReassigned` and `HumanCandidatesResolved` trigger before the engine applies it. The request MUST name the operation, the trigger's actor, and the task's eligibility, variable snapshot and claim state as held in the loaded instance snapshot. Any error the authorizer returns MUST be returned, and nothing is written.

#### Scenario: The request the authorizer receives
```gherkin
Scenario Outline: One request per human trigger, built from the snapshot
  Given a ProcessDriver whose authorizer records every request and allows it
  And an open human task with eligibility spec E, variable snapshot V, claimed by "carol"
  When a <trigger> whose <actor field> is "alice" is passed to ApplyTrigger
  Then the authorizer receives exactly one request
  And its operation is <operation>
  And its actor is "alice", its spec is E, its vars are V
  And its task view is claimed with claimant ID "carol"

  Examples:
    | trigger                 | actor field | operation          |
    | HumanClaimed            | Actor       | claim              |
    | HumanCompleted          | Actor       | complete           |
    | HumanReassigned         | By          | reassign           |
    | HumanCandidatesResolved | By          | refresh_candidates |
```

#### Scenario: Unclaimed task projects an unclaimed view
```gherkin
Scenario: Claim state boundary
  Given a ProcessDriver whose authorizer records every request and allows it
  And an open, unclaimed human task
  When a HumanClaimed naming "alice" is passed to ApplyTrigger
  Then the request's task view is unclaimed with an empty claimant ID
```

#### Scenario: Any authorizer error stops the transition and writes nothing
```gherkin
Scenario Outline: Refusal and failure both fail closed
  Given a ProcessDriver whose authorizer returns <error>
  And an open human task
  When a <trigger> is passed to ApplyTrigger
  Then ApplyTrigger fails with an error wrapping <error>
  And errors.Is(err, authz.ErrNotAuthorized) is <is denial>
  And the instance's version, journal and task store record are unchanged
  And no event is published and no command is performed

  Examples:
    | error                     | trigger                 | is denial |
    | authz.ErrNotAuthorized    | HumanClaimed            | true      |
    | authz.ErrNotAuthorized    | HumanReassigned         | true      |
    | authz.ErrSpecNotEvaluable | HumanCompleted          | false     |
    | authz.ErrNoDeciders       | HumanCandidatesResolved | false     |
    | context.Canceled          | HumanClaimed            | false     |
```

#### Scenario: Negative control - an allowing authorizer lets the transition commit
```gherkin
Scenario Outline: Allowed human triggers are applied
  Given a ProcessDriver whose authorizer allows every request
  And an open human task claimed by "carol"
  When a valid <trigger> is passed to ApplyTrigger
  Then ApplyTrigger returns no error
  And the instance's version advances by one

  Examples:
    | trigger                 |
    | HumanClaimed            |
    | HumanCompleted          |
    | HumanReassigned         |
    | HumanCandidatesResolved |
```

#### Scenario: Non-human triggers are never put to the authorizer
```gherkin
Scenario Outline: Only human triggers are gated
  Given a ProcessDriver whose authorizer denies every request and records it
  And an instance parked where a <trigger> applies
  When the <trigger> is delivered
  Then the trigger is applied without an authorization error
  And the authorizer received no request

  Examples:
    | trigger           |
    | TimerFired        |
    | SignalReceived    |
    | MessageReceived   |
    | CancelRequested   |
    | ActionCompleted   |
    | ResolveIncident   |
```

#### Scenario: A trigger naming an unknown task reaches the engine's own refusal
```gherkin
Scenario: Missing task is not an authorization question
  Given a ProcessDriver whose authorizer denies every request and records it
  When a HumanClaimed naming a task ID the instance snapshot does not hold is passed to ApplyTrigger
  Then ApplyTrigger fails with an error wrapping engine.ErrTokenNotFound
  And the authorizer received no request
```

#### Scenario: A closed task is authorized before the engine reports it closed
```gherkin
Scenario Outline: Authorization comes first on a completed task
  Given a ProcessDriver built with the default composite authorizer
  And a completed human task whose eligibility admits "alice" and not "bob"
  When a HumanClaimed naming <actor> is passed to ApplyTrigger
  Then ApplyTrigger fails with an error wrapping <error>

  Examples:
    | actor   | error                   |
    | "bob"   | authz.ErrNotAuthorized  |
    | "alice" | engine.ErrTaskNotOpen   |
```

#### Scenario: The instance snapshot decides, not the task store's copy
```gherkin
Scenario Outline: A task store copy that disagrees with the snapshot is not consulted
  Given an open human task whose eligibility in the instance snapshot <snapshot>
  And the task store's copy of the task carries an eligibility that <store copy>
  When a HumanClaimed naming "bob" is passed to ApplyTrigger
  Then the result is <result>

  Examples:
    | snapshot          | store copy        | result                 |
    | does not admit bob | admits bob        | authz.ErrNotAuthorized |
    | admits bob         | does not admit bob | the claim is applied   |
```

#### Scenario: A decision made on a stale snapshot cannot commit
```gherkin
Scenario: Lost update between authorization and commit
  Given an allowing authorizer and an open human task at instance version 3
  And another writer commits version 4 after ApplyTrigger has loaded version 3 and authorized against it
  When ApplyTrigger commits
  Then ApplyTrigger fails with an error wrapping kernel.ErrConcurrentUpdate
  And the instance stays at version 4 with the other writer's state
```

#### Scenario: Each delivery is authorized afresh
```gherkin
Scenario: Duplicate delivery of the same trigger
  Given a ProcessDriver whose authorizer records every request and allows it
  And an open human task
  When the same HumanCompleted is passed to ApplyTrigger twice
  Then the authorizer receives two requests
  And the second ApplyTrigger fails with an error wrapping engine.ErrTaskNotOpen
```

#### Scenario: The task routes answer the driver's refusal identically on every adapter
```gherkin
Scenario Outline: 403 originates in the driver
  Given the <adapter> routes are mounted over a driver built with the default composite authorizer
  And the resolver authenticates <actor>
  When the caller posts to /tasks/{token}/<operation>
  Then the response status is <status>

  Examples:
    | adapter | operation | actor                           | status |
    | stdlib  | claim     | "bob", ineligible for the task  | 403    |
    | gin     | claim     | "bob", ineligible for the task  | 403    |
    | fiber   | claim     | "bob", ineligible for the task  | 403    |
    | stdlib  | complete  | "bob", ineligible for the task  | 403    |
    | gin     | complete  | "bob", ineligible for the task  | 403    |
    | fiber   | complete  | "bob", ineligible for the task  | 403    |
    | stdlib  | claim     | "alice", eligible for the task  | 200    |
    | gin     | claim     | "alice", eligible for the task  | 200    |
    | fiber   | claim     | "alice", eligible for the task  | 200    |
```

### Requirement: A driver with no authorizer refuses human triggers
A `ProcessDriver` built without an authorizer, or with a nil one, MUST refuse every human trigger passed to `ApplyTrigger` with `runtime.ErrNoAuthorizer`, before anything is written, and the error's message MUST name `authz.AllowAll{}` as the explicit opt-out. Such a driver SHALL still be constructed without error and SHALL still apply non-human triggers.

#### Scenario: Authorizer configuration decides admission
```gherkin
Scenario Outline: Nil is refused, an explicit authorizer is honoured
  Given a ProcessDriver built <configuration>
  And an open human task whose eligibility admits "alice" and not "bob"
  When a HumanClaimed naming <actor> is passed to ApplyTrigger
  Then the result is <result>

  Examples:
    | configuration                                           | actor   | result                                    |
    | with runtime.WithHumanTasks(resolver, tasks, nil)       | "alice" | runtime.ErrNoAuthorizer, version unchanged |
    | with runtime.WithHumanTasks(resolver, tasks, nil)       | "bob"   | runtime.ErrNoAuthorizer, version unchanged |
    | with runtime.WithHumanTasks(resolver, tasks, authz.AllowAll{}) | "bob"   | the claim is applied                |
    | with runtime.WithHumanTasks(resolver, tasks, authz.NewComposite()) | "alice" | the claim is applied            |
    | with runtime.WithHumanTasks(resolver, tasks, authz.NewComposite()) | "bob"   | authz.ErrNotAuthorized          |
```

#### Scenario: The refusal names the opt-out and is not a denial
```gherkin
Scenario: ErrNoAuthorizer is a configuration error
  Given a ProcessDriver built with a nil authorizer
  When a HumanCompleted is passed to ApplyTrigger
  Then the error's message contains "authz.AllowAll{}"
  And errors.Is(err, authz.ErrNotAuthorized) is false
```

#### Scenario: Each human trigger kind is refused
```gherkin
Scenario Outline: No human trigger slips past a nil authorizer
  Given a ProcessDriver built with a nil authorizer
  And an open human task
  When a <trigger> is passed to ApplyTrigger
  Then ApplyTrigger fails with an error wrapping runtime.ErrNoAuthorizer
  And the instance's version and journal are unchanged

  Examples:
    | trigger                 |
    | HumanClaimed            |
    | HumanCompleted          |
    | HumanReassigned         |
    | HumanCandidatesResolved |
```

#### Scenario: Negative control - a driver with no authorizer still runs non-human work
```gherkin
Scenario: Construction and non-human triggers are unaffected
  Given a ProcessDriver built with a nil authorizer
  Then its construction returned no error
  When a TimerFired for a parked timer is delivered
  Then the trigger is applied without error
```

#### Scenario: The task routes classify the missing authorizer as an internal error
```gherkin
Scenario Outline: Misconfiguration is a 500 with no message
  Given the <adapter> routes are mounted over a driver built with a nil authorizer
  And the resolver authenticates an actor
  When the caller posts to /tasks/{token}/claim
  Then the response status is 500
  And the error code is "internal_error"
  And the response does not contain "AllowAll"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: A candidate refresh is authorized as the actor who requested it
A candidate-resolution trigger MUST carry the actor who requested the refresh, and `TaskService.RefreshCandidates` SHALL put its `by` actor on the trigger it returns; the driver MUST authorize the refresh as that actor, so a refresh whose actor the eligibility spec does not admit is refused with `authz.ErrNotAuthorized` and the candidate list is unchanged.

#### Scenario: The requesting actor rides on the trigger
```gherkin
Scenario: RefreshCandidates stamps the requester
  Given an open human task and a configured actor resolver
  When TaskService.RefreshCandidates is called with by actor "alice"
  Then the returned HumanCandidatesResolved trigger's By is "alice"
```

#### Scenario: The requester decides the refresh
```gherkin
Scenario Outline: Refresh admitted or refused by its requester
  Given a ProcessDriver built with the default composite authorizer
  And an open human task with candidates [a, b] whose eligibility spec is <spec>
  When a HumanCandidatesResolved with candidates [c] and By <by> is passed to ApplyTrigger
  Then the result is <result>

  Examples:
    | spec                       | by               | result                                           |
    | roles ["manager"]          | a manager        | candidates are exactly [c]                        |
    | roles ["manager"]          | a viewer         | authz.ErrNotAuthorized, candidates stay [a, b]   |
    | roles ["manager"]          | the zero actor   | authz.ErrNotAuthorized, candidates stay [a, b]   |
    | empty (no fields set)      | the zero actor   | candidates are exactly [c]                        |
```

### Requirement: The human-task counter counts only admitted transitions
The `wrkflw_human_tasks_total` counter MUST increase by exactly one, labelled with the operation's event, for each human trigger that passes authorization and is committed, and MUST NOT increase for a trigger refused by authorization or by a missing authorizer, nor for a trigger the task service produces that is never applied.

#### Scenario: Counting by outcome
```gherkin
Scenario Outline: Counter delta per delivery
  Given a ProcessDriver with a meter provider recording wrkflw_human_tasks_total
  And <setup>
  When <action>
  Then wrkflw_human_tasks_total{event=<event>} increased by <delta>

  Examples:
    | setup                                         | action                                                    | event                | delta |
    | an authorizer that allows                     | a HumanClaimed is applied and committed                   | claimed              | 1     |
    | an authorizer that allows                     | a HumanCompleted is applied and committed                 | completed            | 1     |
    | an authorizer that allows                     | a HumanReassigned is applied and committed                | reassigned           | 1     |
    | an authorizer that allows                     | a HumanCandidatesResolved is applied and committed        | candidates_refreshed | 1     |
    | an authorizer that denies                     | a HumanClaimed is passed to ApplyTrigger                  | claimed              | 0     |
    | a nil authorizer                              | a HumanCompleted is passed to ApplyTrigger                | completed            | 0     |
    | an authorizer that allows                     | TaskService.Claim returns a trigger nobody applies        | claimed              | 0     |
```
