## ADDED Requirements

### Requirement: Parked message waits resume after a restart
After a fresh `ProcessDriver` over the same instance store has recovered waiters, `ProcessDriver.DeliverMessage` with the name and correlation key a non-terminal instance was parked on before the restart SHALL resume that instance, and MUST NOT start a new instance from a message-start event for that delivery.

#### Scenario: Every message-waiting construct resumes after a restart
```gherkin
Scenario Outline: a message wait survives a driver restart
  Given a ProcessDriver over an in-memory instance store
  And instance "mw-1" parked on <construct> for message "order.paid" with correlation key "ord-1"
  And the driver is discarded
  When a fresh driver over the same instance store recovers waiters
  And "order.paid" is delivered with correlation key "ord-1"
  Then DeliverMessage returns nil
  And instance "mw-1" leaves the wait along <path>

  Examples:
    | construct                                   | path                     |
    | an intermediate message catch event         | the catch's outgoing flow |
    | a message boundary on a user task           | the boundary's flow      |
    | an event-based gateway message arm          | the message arm's flow   |
    | an interrupting message event sub-process   | the event sub-process    |
```

#### Scenario: A message wait survives a restart on every SQL dialect
```gherkin
Scenario Outline: restart over a durable store
  Given a ProcessDriver over a <dialect> instance store
  And instance "mw-2" parked on an intermediate message catch for "order.paid" with key "ord-2"
  And the driver is discarded
  When a fresh driver over the same store recovers waiters
  And "order.paid" is delivered with correlation key "ord-2"
  Then instance "mw-2" reaches StatusCompleted

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A keyed wait is not shadowed by a message start after a restart
```gherkin
Given a definition that starts on message "order.paid" and also waits for "order.paid" downstream
And instance "mw-3" parked on that downstream wait with correlation key "ord-3"
And the driver is discarded
When a fresh driver over the same store recovers waiters
And "order.paid" is delivered with correlation key "ord-3"
Then instance "mw-3" leaves the wait
And the instance store holds exactly one instance
```

#### Scenario: A keyless wait resumes after a restart
```gherkin
Given instance "mw-4" parked on an intermediate message catch for "ping" with an empty correlation key
And the driver is discarded
When a fresh driver over the same store recovers waiters
And "ping" is delivered with an empty correlation key
Then instance "mw-4" leaves the wait
```

#### Scenario: Negative control, a different key does not resume the recovered wait
```gherkin
Scenario Outline: only the exact key matches after recovery
  Given instance "mw-5" parked on "order.paid" with correlation key "ord-5"
  And no definition starts on "order.paid"
  And a fresh driver over the same store has recovered waiters
  When "order.paid" is delivered with correlation key <key>
  Then DeliverMessage returns nil
  And instance "mw-5" is still parked on the wait

  Examples:
    | key        |
    | ""         |
    | "ORD-5"    |
    | " ord-5"   |
    | "ord-5 "   |
    | "ord-6"    |
    | "ord-５"    |
```

#### Scenario: Two parked instances on one key keep the live ambiguity outcome
```gherkin
Given instances "mw-6" and "mw-7" both parked on "order.paid" with correlation key "dup"
And the driver is discarded
When a fresh driver over the same store recovers waiters
Then a WARN naming message "order.paid", key "dup" and both instance ids is logged
And a single delivery of "order.paid" with key "dup" resumes exactly one of the two instances
```

### Requirement: Parked signal waits resume after a restart
After a fresh `ProcessDriver` wired with a `signal.SignalBus` over the same instance store has recovered waiters, `ProcessDriver.BroadcastSignal` SHALL resume every non-terminal instance that was parked on that signal name before the restart.

#### Scenario: Every signal-waiting construct resumes after a restart
```gherkin
Scenario Outline: a signal wait survives a driver restart
  Given a ProcessDriver with a SignalBus over an in-memory instance store
  And instance "sw-1" parked on <construct> for signal "halt"
  And the driver and its SignalBus are discarded
  When a fresh driver with a fresh SignalBus over the same store recovers waiters
  And "halt" is broadcast
  Then BroadcastSignal returns nil
  And instance "sw-1" leaves the wait

  Examples:
    | construct                                  |
    | an intermediate signal catch event         |
    | a signal boundary on a user task           |
    | an event-based gateway signal arm          |
    | an interrupting signal event sub-process   |
```

#### Scenario: A broadcast after a restart reaches every parked instance
```gherkin
Scenario Outline: fan-out count after recovery on every dialect
  Given a ProcessDriver with a SignalBus over a <dialect> instance store
  And <parked> instances parked on signal "halt"
  And the driver and its SignalBus are discarded
  When a fresh driver with a fresh SignalBus over the same store recovers waiters
  And "halt" is broadcast
  Then <parked> instances leave the wait

  Examples:
    | dialect   | parked |
    | in-memory | 0      |
    | in-memory | 1      |
    | in-memory | 3      |
    | Postgres  | 3      |
    | MySQL     | 3      |
    | SQLite    | 3      |
```

#### Scenario: Negative control, another signal name does not resume the recovered wait
```gherkin
Scenario Outline: only the exact name matches after recovery
  Given instance "sw-2" parked on signal "halt"
  And a fresh driver with a SignalBus over the same store has recovered waiters
  When <name> is broadcast
  Then instance "sw-2" is still parked on the wait

  Examples:
    | name     |
    | "HALT"   |
    | " halt"  |
    | "halt2"  |
```

### Requirement: Waiter recovery registers nothing for a terminal instance
Waiter recovery SHALL register no signal or message wait for an instance whose status is terminal, including a terminal instance whose snapshot still holds a repeatable non-interrupting event sub-process arm.

#### Scenario: A terminal instance is not woken after a restart
```gherkin
Scenario Outline: terminal status after recovery
  Given instance "tw-1" in <status> whose snapshot still holds a repeatable non-interrupting message event sub-process arm for "audit" with key "a-1"
  And a definition that starts on message "audit"
  And a fresh driver over the same store has recovered waiters
  When "audit" is delivered with correlation key "a-1"
  Then instance "tw-1" is unchanged
  And the message is handled as when no instance awaits it

  Examples:
    | status           |
    | StatusCompleted  |
    | StatusFailed     |
    | StatusTerminated |
```

#### Scenario: Negative control, a running instance with the same arm is woken
```gherkin
Given instance "tw-2" in StatusRunning holding a repeatable non-interrupting message event sub-process arm for "audit" with key "a-2"
And a fresh driver over the same store has recovered waiters
When "audit" is delivered with correlation key "a-2"
Then instance "tw-2" runs the event sub-process
```

### Requirement: Waiter recovery acquires no instance ownership
Waiter recovery SHALL acquire instance ownership for no instance, including when the driver's instance store is a `persistence.CachingInstanceStore` built over an `InstanceOwnership` that records every `Acquire`.

#### Scenario: Recovery over a caching store leaves ownership untouched
```gherkin
Scenario Outline: acquires counted with a real caching store
  Given a persistence.CachingInstanceStore over a SQLite store and an InstanceOwnership that counts Acquire calls
  And the caching store is the driver's instance store
  And <awaiting> instances parked on message or signal waits
  And <idle> running instances parked on user tasks only
  And <done> completed instances
  When a fresh driver over that caching store recovers waiters
  Then Acquire has been called 0 times
  And each of the <awaiting> waits resumes on its delivery

  Examples:
    | awaiting | idle | done |
    | 0        | 0    | 0    |
    | 0        | 3    | 3    |
    | 3        | 3    | 3    |
```

#### Scenario: Recovery holds no advisory lock on Postgres
```gherkin
Given a persistence.CachingInstanceStore over a Postgres store and persistence.NewAdvisoryLockOwnership
And 3 instances parked on message waits and 3 parked on user tasks only
When a fresh driver over that caching store recovers waiters
Then the recovering connection holds no wrkflw advisory lock
And a second replica's ownership can acquire each of the 6 instances
```

### Requirement: Waiter recovery is idempotent and safe beside live traffic
Running waiter recovery more than once, or while instances advance, SHALL leave each instance registered for exactly the waits its latest committed state holds, so a delivery MUST NOT reach a wait the instance has already left.

#### Scenario: Recovering twice resumes a wait once
```gherkin
Given instance "iw-1" parked on "order.paid" with correlation key "ord-1"
And a fresh driver over the same store recovers waiters twice
When "order.paid" is delivered with correlation key "ord-1" twice
Then instance "iw-1" leaves the wait exactly once
And the second delivery is handled as when no instance awaits it
```

#### Scenario: A wait left during recovery is not re-registered
```gherkin
Given 50 instances parked on "order.paid", each with its own correlation key
When waiter recovery runs while each instance is delivered its message concurrently
And recovery returns
Then delivering "order.paid" again for any of the 50 keys resumes no instance
```

#### Scenario: A wait entered during recovery is reachable afterwards
```gherkin
Given a fresh driver over a store holding instance "iw-2" parked on a user task
When waiter recovery runs while "iw-2" completes the task and parks on "order.shipped" with key "s-2"
And recovery returns
Then delivering "order.shipped" with key "s-2" resumes instance "iw-2"
```
