## Purpose

Defines who may perform an operation on a process instance: an additive operation authorizer the ProcessEngine consults before starting, reading, listing, signalling, messaging, cancelling or repairing an instance, and how its refusal reaches a caller.

## ADDED Requirements

### Requirement: A configured operation authorizer is consulted once before each instance operation
(Pending Q6.) When the ProcessEngine is built with `service.WithOperationAuthorizer`, each instance operation MUST consult that authorizer exactly once, before any effect, passing the operation's name, the actor carried in the call's context, and the operation's target.

#### Scenario: Each operation names itself and its target
```gherkin
Scenario Outline: The authorizer sees the operation, the actor and the target
  Given a ProcessEngine built with an operation authorizer that records its calls and allows them
  And the call's context carries actor "u1"
  When <call> is invoked
  Then the authorizer was called exactly once
  And it received operation "<operation>", actor "u1" and <target>

  Examples:
    | call                                         | operation                   | target                                |
    | StartInstance for definition "expense" v2    | instance.start              | definition reference "expense" v2      |
    | GetInstance for "i-1"                        | instance.read               | instance ID "i-1"                     |
    | ListInstances                                | instance.list               | no instance ID and no definition       |
    | DeliverSignal to "i-1"                       | instance.signal             | instance ID "i-1"                     |
    | DeliverMessage correlated to "i-1"           | message.deliver             | no instance ID and no definition       |
    | CancelInstance "i-1"                         | instance.cancel             | instance ID "i-1"                     |
    | ResolveIncident on "i-1"                     | incident.resolve            | instance ID "i-1"                     |
    | ResolveCompensationStall on "i-1"            | compensation.resolve-stall  | instance ID "i-1"                     |
```

#### Scenario: Engine-originated starts are not caller operations
```gherkin
Scenario: A call activity's child start is not authorized
  Given a ProcessEngine built with an operation authorizer that records its calls and allows them
  And a definition whose call activity starts a child instance
  When StartInstance is invoked once for that definition and the child starts
  Then the authorizer was called exactly once, for operation "instance.start"
```

#### Scenario: Human-task operations keep their own authorizer
```gherkin
Scenario: ClaimTask does not consult the operation authorizer
  Given a ProcessEngine built with an operation authorizer that refuses every operation
  And an open human task whose eligibility admits actor "alice"
  When "alice" claims the task through ClaimTask
  Then the claim succeeds
  And the operation authorizer was not called
```

### Requirement: A refused instance operation returns the refusal and changes nothing
(Pending Q6.) When the operation authorizer returns an error, the ProcessEngine MUST return an error matching it under `errors.Is` and MUST NOT create, load into a response, advance, cancel or repair any instance.

#### Scenario: Refusals leave every instance as it was
```gherkin
Scenario Outline: A refusing authorizer blocks the effect
  Given a ProcessEngine built with an operation authorizer that returns authz.ErrNotAuthorized for <operation>
  And a running instance "i-1" parked at a signal wait and holding an open incident
  When <call> is invoked
  Then the returned error matches authz.ErrNotAuthorized under errors.Is
  And <unchanged>

  Examples:
    | operation          | call                               | unchanged                                    |
    | instance.start     | StartInstance                      | the instance count is unchanged               |
    | instance.signal    | DeliverSignal to "i-1"             | "i-1" is still parked at the signal wait      |
    | message.deliver    | DeliverMessage correlated to "i-1" | "i-1" is still parked at the signal wait      |
    | instance.cancel    | CancelInstance "i-1"               | "i-1" is still running                        |
    | incident.resolve   | ResolveIncident on "i-1"           | the incident on "i-1" is still open           |
    | instance.read      | GetInstance for "i-1"              | no ProcessInstance is returned                |
```

#### Scenario: Negative control, an allowing authorizer changes nothing about the outcome
```gherkin
Scenario Outline: Allowed operations behave as without the hook
  Given a ProcessEngine built with an operation authorizer that allows everything
  When <call> is invoked
  Then no error is returned
  And the result equals the result of the same call on an engine with no operation authorizer

  Examples:
    | call                   |
    | StartInstance          |
    | GetInstance for "i-1"  |
    | DeliverSignal to "i-1" |
    | CancelInstance "i-1"   |
```

#### Scenario: An authorizer failure that is not a denial still blocks
```gherkin
Scenario: The authorizer cannot decide
  Given a ProcessEngine built with an operation authorizer that returns error "policy store down" for instance.cancel
  When CancelInstance "i-1" is invoked
  Then the returned error matches "policy store down" under errors.Is
  And errors.Is(err, authz.ErrNotAuthorized) is false
  And "i-1" is still running
```

#### Scenario: Concurrent refused and allowed calls do not interfere
```gherkin
Scenario: Per-call decisions under concurrency
  Given an operation authorizer that allows actor "u1" and refuses actor "u2" for instance.signal
  When "u1" and "u2" deliver the same signal to "i-1" concurrently
  Then "u2" receives an error matching authz.ErrNotAuthorized
  And "i-1" has consumed exactly one signal
```

### Requirement: HTTP routes answer an operation refusal without rendering state
(Pending Q6, Q7.) On the stdlib, gin and fiber adapters, an operation the authorizer refuses with `authz.ErrNotAuthorized` MUST be answered 403 with code "forbidden", and a refusal of any other kind 500 with no message, and neither response body SHALL contain instance state.

#### Scenario: Refused routes on every adapter
```gherkin
Scenario Outline: Route refusals
  Given the <adapter> routes are mounted over an engine whose operation authorizer returns <error> for every operation
  And a resolver that returns an actor with ID "u1"
  And an instance "i-1" whose variables hold "111-22-3333"
  When the client calls <route>
  Then the response status is <status>
  And the response body contains neither "111-22-3333" nor "i-1"

  Examples:
    | adapter | route                         | error                  | status |
    | stdlib  | GET /instances/{id}           | authz.ErrNotAuthorized | 403    |
    | stdlib  | GET /instances/{id}/snapshot  | authz.ErrNotAuthorized | 403    |
    | stdlib  | POST /instances               | authz.ErrNotAuthorized | 403    |
    | stdlib  | POST /instances/{id}/signals  | authz.ErrNotAuthorized | 403    |
    | stdlib  | POST /admin/instances/{id}/cancel | authz.ErrNotAuthorized | 403 |
    | stdlib  | GET /instances/{id}           | errors.New("down")     | 500    |
    | gin     | GET /instances/{id}           | authz.ErrNotAuthorized | 403    |
    | gin     | POST /instances               | authz.ErrNotAuthorized | 403    |
    | fiber   | GET /instances/{id}           | authz.ErrNotAuthorized | 403    |
    | fiber   | POST /instances               | authz.ErrNotAuthorized | 403    |
```

#### Scenario: The route hands the resolved actor to the authorizer
```gherkin
Scenario Outline: Actor from the configured resolver reaches the hook
  Given the <adapter> routes are mounted with a custom resolver returning actor "u1" and no middleware calling authz.ContextWithActor
  And an operation authorizer that records the actor it receives
  When the client calls GET /instances/{id}
  Then the authorizer received actor "u1"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Negative control, an allowing authorizer keeps 200
```gherkin
Scenario Outline: Allowed reads render
  Given the <adapter> routes are mounted over an engine whose operation authorizer allows everything
  When an identified client calls GET /instances/{id}
  Then the response status is 200
  And the response body contains the instance ID

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: Without the option no instance operation is authorized
(Pending Q6.) A ProcessEngine built without `service.WithOperationAuthorizer` MUST perform every instance operation without an authorization step, and passing a nil authorizer to that option MUST fail construction with `service.ErrNilDependency`.

#### Scenario: The default keeps unauthenticated write
```gherkin
Scenario Outline: No hook configured
  Given the <adapter> routes are mounted over an engine built without an operation authorizer and with no request-actor resolver
  When an unauthenticated client calls POST /instances for a registered definition
  Then the response status is 201

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: A nil authorizer is a construction error
```gherkin
Scenario Outline: Explicit nil versus absent
  Given NewProcessEngine is called <with>
  Then the result is <result>

  Examples:
    | with                                     | result                                   |
    | service.WithOperationAuthorizer(nil)     | an error matching service.ErrNilDependency |
    | no operation authorizer option           | an engine and no error                     |
```

### Requirement: The unidentified caller reaches the authorizer as the zero actor
(Pending Q6.) The ProcessEngine MUST NOT refuse an instance operation itself because the context carries no actor; it SHALL pass the zero actor to the configured operation authorizer and let it decide.

#### Scenario: The hook decides for the anonymous caller
```gherkin
Scenario Outline: Zero actor passed through
  Given a ProcessEngine built with an operation authorizer that <decision> the zero actor for instance.start
  And the call's context carries no actor
  When StartInstance is invoked
  Then the authorizer received the zero actor
  And the result is <result>

  Examples:
    | decision | result                                           |
    | allows   | a started instance                               |
    | refuses  | an error matching authz.ErrNotAuthorized         |
```
