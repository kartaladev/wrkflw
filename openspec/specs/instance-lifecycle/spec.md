# instance-lifecycle Specification

## Purpose
Defines how a process instance is started, cancelled and refused once it is terminal, and how a process driver shuts down: which starts and cancels are refused or reported, which entry points refuse new work while draining, and which sentinel each answer carries.

## Requirements

### Requirement: Shutdown drains admitted work before returning
`ProcessDriver.Shutdown` SHALL wait for externally-initiated work it has already admitted to finish before returning nil, and when its context expires first it MUST return an error matching `runtime.ErrDrainTimeout`.

#### Scenario: Shutdown waits for an in-flight Drive
```gherkin
Scenario: A Drive call is parked inside a blocking action
  Given a driver whose Drive call is blocked inside the action of its only service task
  When Shutdown is called with a context that has no deadline
  Then Shutdown has not returned while the action is still blocked
  And once the action is released and Drive returns, Shutdown returns nil
```

#### Scenario: The drain deadline expires
```gherkin
Scenario: Shutdown's context expires while a Drive is still in flight
  Given a driver whose Drive call is blocked inside the action of its only service task
  When Shutdown is called with a context that expires after 50ms
  Then Shutdown returns an error matching runtime.ErrDrainTimeout
```

### Requirement: A draining driver refuses new external work
Once `Shutdown` has begun, every externally-initiated entry point of `ProcessDriver` and of `service.ProcessEngine` SHALL refuse new work with an error matching `runtime.ErrDriverShuttingDown`, and a timer-start fire MUST create no instance.

#### Scenario: Driver entry points after Shutdown
```gherkin
Scenario Outline: <entry point> is called on a driver that has been shut down
  Given a ProcessDriver on which Shutdown has returned
  When <entry point> is called
  Then it fails with runtime.ErrDriverShuttingDown

  Examples:
    | entry point      |
    | Drive            |
    | ApplyTrigger     |
    | DeliverMessage   |
    | BroadcastSignal  |
    | CancelInstance   |
    | ResolveIncident  |
    | ReverseInstance  |
```

#### Scenario: Service entry points after Shutdown
```gherkin
Scenario Outline: <operation> is called on a process engine that has been shut down
  Given a service.ProcessEngine on which Shutdown has returned
  When <operation> is called
  Then it fails with runtime.ErrDriverShuttingDown

  Examples:
    | operation             |
    | StartInstance         |
    | ClaimTask             |
    | CompleteTask          |
    | ReassignTask          |
    | RefreshTaskCandidates |
```

#### Scenario: A timer-start fire while draining
```gherkin
Scenario Outline: A timer-start event fires on a driver that is <state>
  Given a definition whose start event is a timer start
  When the timer-start fire runs on a driver that is <state>
  Then the instance store holds <count> instances

  Examples:
    | state                    | count |
    | admitting work           | 1     |
    | draining after Shutdown  | 0     |
```

### Requirement: A second start of an instance is refused
A `StartInstance` trigger delivered to an instance that has already been started SHALL fail with `ErrInstanceAlreadyStarted`, which MUST also match `ErrInvalidTransition`, whatever the first start's occurrence time; a never-started instance MUST still start.

#### Scenario: Starting an instance twice
```gherkin
Scenario Outline: StartInstance is applied to an instance parked at a user task after <first start>
  Given an instance parked at a user task after <first start>
  When StartInstance is applied again
  Then the step fails with ErrInstanceAlreadyStarted
  And the error matches ErrInvalidTransition

  Examples:
    | first start               |
    | a start at a real time    |
    | a start with a zero time  |
```

#### Scenario: A fresh instance starts
```gherkin
Scenario: Negative control - a never-started instance
  Given an instance state that has never been started
  When StartInstance is applied
  Then the step succeeds with one token placed and status StatusRunning
```

### Requirement: A cancel that will not terminate the instance is reported
A `CancelRequested` delivered while an admin partial-rollback compensation walk is in flight SHALL fail with `ErrCancelNotApplicable`, which MUST also match `ErrInvalidTransition`; a cancel delivered while a walk that resumes the instance is in flight SHALL succeed and record the cancel to apply when the walk finishes.

#### Scenario: Cancel during a partial rollback and during a resuming walk
```gherkin
Scenario Outline: CancelRequested arrives during <walk>
  Given an instance whose <walk> is in flight
  When CancelRequested is applied
  Then <outcome>

  Examples:
    | walk                                  | outcome                                                                                 |
    | admin partial-rollback walk           | the step fails with ErrCancelNotApplicable, also matching ErrInvalidTransition          |
    | compensation walk that resumes        | the step succeeds and the instance records a pending cancel                              |
```

#### Scenario: The driver keeps the sentinel and still cancels children
```gherkin
Scenario Outline: ProcessDriver.CancelInstance over a dropped cancel
  Given <setup>
  When CancelInstance is called on the root instance
  Then <outcome>

  Examples:
    | setup                                                                                  | outcome                                                                                              |
    | a parent whose partial-rollback walk is in flight, with a running called child          | it fails with ErrCancelNotApplicable and the child is StatusTerminated                                |
    | a running parent, a child whose partial-rollback walk is in flight, and a grandchild    | it succeeds, the parent is StatusTerminated, the child stays StatusCompensating, the grandchild is StatusTerminated |
    | a single instance whose partial-rollback walk is in flight                              | it fails matching both ErrCancelNotApplicable and ErrInvalidTransition, and the returned status is StatusCompensating |
```

### Requirement: An ambiguous message start is refused
`ProcessDriver.DeliverMessage` SHALL fail with `runtime.ErrAmbiguousMessageStart` and create no instance when no running instance awaits the message and its name matches a message-start event on more than one registered definition.

#### Scenario: Two definitions start on the same message
```gherkin
Scenario: Definitions "def-a" and "def-b" both start on message "order.created"
  When "order.created" is delivered with correlation key "9"
  Then DeliverMessage fails with runtime.ErrAmbiguousMessageStart
  And the instance store holds no instance
```

#### Scenario: A unique message start creates an instance
```gherkin
Scenario: Negative control - one definition starts on "order.created"
  When "order.created" is delivered with correlation key "42"
  Then DeliverMessage succeeds
  And exactly one instance exists and it reaches StatusCompleted
```

### Requirement: A keyed message start creates one instance per correlation key
When a message starts an instance and carries a non-empty correlation key, `ProcessDriver.DeliverMessage` SHALL create at most one instance for each message name and correlation key, including under concurrent and repeated delivery, and every duplicate delivery MUST succeed without creating another.

#### Scenario: Repeated and distinct keys
```gherkin
Scenario: Deliveries of "order.created" with keys k1, k1 and k2
  Given one definition that starts on "order.created"
  When the three messages are delivered in turn
  Then every delivery succeeds
  And exactly two instances exist
```

#### Scenario: Concurrent delivery of one key
```gherkin
Scenario: 16 goroutines deliver "order.created" with key "42" at once
  Given one definition that starts on "order.created"
  When all 16 deliveries return
  Then every delivery succeeded
  And exactly one instance exists
```

### Requirement: A dead instance and a missing token are told apart
A trigger naming a command that no token awaits on a live instance SHALL fail with `ErrTokenNotFound`, and a `HumanCompleted` delivered to a terminal instance SHALL fail with `ErrInstanceTerminal`, which MUST NOT match `ErrTokenNotFound`; both MUST match `ErrInvalidTransition`.

#### Scenario: Unknown command and late completion
```gherkin
Scenario Outline: <trigger> is applied to <instance>
  When <trigger> is applied to <instance>
  Then the step fails with <error>
  And the error matches ErrInvalidTransition
  And <negative>

  Examples:
    | trigger                                         | instance                                       | error                | negative                                  |
    | ActionCompleted for command "no-such-command"    | a running instance parked on a service task    | ErrTokenNotFound     | nothing further                           |
    | HumanCompleted for its user task                 | an instance parked on a user task, then cancelled | ErrInstanceTerminal  | the error does not match ErrTokenNotFound |
    | HumanCompleted for its user task                 | an instance that completed through an event sub-process | ErrInstanceTerminal  | the error does not match ErrTokenNotFound |
```
