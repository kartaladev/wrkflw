## MODIFIED Requirements

### Requirement: RunInTx commits or rolls back the caller's unit as a whole
A SQL-backed instance store's `RunInTx` SHALL commit every store write made with the context it hands to the callback when the callback returns nil, and SHALL roll all of them back and return the callback's error unchanged (matching under `errors.Is`) when the callback returns an error. Under a transaction lent through `persistence.WithTx` it SHALL join that transaction instead, undoing only its own writes on error and never committing.

#### Scenario: Callback outcome decides the unit
```gherkin
Scenario Outline: RunInTx on each SQL dialect
  Given a <dialect> instance store
  And a context that carries no transaction lent through persistence.WithTx
  When RunInTx runs a callback that creates instance "<id>" and then returns <result>
  Then RunInTx returns <returned>
  And loading "<id>" <load_outcome>

  Examples:
    | dialect  | id          | result   | returned                     | load_outcome                         |
    | Postgres | tx-commit   | nil      | nil                          | succeeds                             |
    | MySQL    | tx-commit   | nil      | nil                          | succeeds                             |
    | SQLite   | tx-commit   | nil      | nil                          | succeeds                             |
    | Postgres | tx-rollback | an error | that error (errors.Is)       | returns kernel.ErrInstanceNotFound   |
    | MySQL    | tx-rollback | an error | that error (errors.Is)       | returns kernel.ErrInstanceNotFound   |
    | SQLite   | tx-rollback | an error | that error (errors.Is)       | returns kernel.ErrInstanceNotFound   |
```

#### Scenario: Under a lent transaction RunInTx joins and neither commits nor rolls back
```gherkin
Scenario Outline: RunInTx inside a consumer's lent transaction
  Given a <dialect> instance store
  And a consumer transaction that has inserted its own row "c-1" and is lent through persistence.WithTx
  When RunInTx runs, with the lent context, a callback that creates instance "<id>" and then returns <result>
  Then RunInTx returns <returned>
  And the consumer transaction is still open and accepts a further statement
  When the consumer commits its transaction
  Then the consumer row "c-1" exists
  And loading "<id>" <load_outcome>

  Examples:
    | dialect  | id         | result   | returned               | load_outcome                       |
    | Postgres | lent-ok    | nil      | nil                    | succeeds                           |
    | MySQL    | lent-ok    | nil      | nil                    | succeeds                           |
    | SQLite   | lent-ok    | nil      | nil                    | succeeds                           |
    | Postgres | lent-fail  | an error | that error (errors.Is) | returns kernel.ErrInstanceNotFound |
    | MySQL    | lent-fail  | an error | that error (errors.Is) | returns kernel.ErrInstanceNotFound |
    | SQLite   | lent-fail  | an error | that error (errors.Is) | returns kernel.ErrInstanceNotFound |
```

## ADDED Requirements

### Requirement: The engine never ends a transaction it did not open
An engine call made with a context returned by `persistence.WithTx` SHALL NOT commit or roll back the lent transaction, whether the call succeeds or fails; the consumer's transaction MUST remain open and usable, and its outcome MUST be decided only by the consumer's own commit or rollback.

#### Scenario: The lent transaction survives every engine outcome
```gherkin
Scenario Outline: consumer keeps control after the engine call
  Given a ProcessEngine over a <dialect> store
  And a consumer transaction lent through persistence.WithTx
  When StartInstance is called with the lent context and <outcome>
  Then the consumer can still insert its own row "c-2" in that transaction
  And committing the consumer transaction persists "c-2"

  Examples:
    | dialect  | outcome                                   |
    | Postgres | succeeds                                  |
    | MySQL    | succeeds                                  |
    | SQLite   | succeeds                                  |
    | Postgres | returns an injected store write error     |
    | MySQL    | returns an injected store write error     |
    | SQLite   | returns an injected store write error     |
```

### Requirement: A rolled-back lent transaction leaves no engine state
When the consumer rolls back a lent transaction after an engine call, the engine SHALL leave no instance row, journal entry, outbox row or timer row for that call, a later read of the instance MUST return `kernel.ErrInstanceNotFound`, and no cache MUST serve it.

#### Scenario: StartInstance then consumer rollback
```gherkin
Scenario Outline: consumer rolls back after StartInstance
  Given a ProcessEngine over a <dialect> instance store wrapped by CachingInstanceStore
  And a definition whose first step arms a timer and emits an outbox event
  And a consumer transaction lent through persistence.WithTx
  When StartInstance creates instance "rb-1" with the lent context and succeeds
  And the consumer rolls back its transaction and calls Discard on the Continuation
  Then the database holds no instance row, journal entry, outbox row or timer row for "rb-1"
  And GetInstance for "rb-1" returns kernel.ErrInstanceNotFound
  And no timer for "rb-1" is scheduled

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Nothing external runs before the consumer resumes
Inside a lent transaction an engine call SHALL apply exactly one step and MUST NOT perform an action, activate or deactivate a scheduled timer, reconcile signal or message waiters, or apply a follow-up trigger; all of that SHALL run only from `Continuation.Resume`.

#### Scenario: A service task is not called inside the consumer's window
```gherkin
Scenario Outline: first node after start is a service task
  Given a ProcessEngine over a <dialect> store with a spy action catalog
  And a definition start -> serviceTask(A) -> end
  And a consumer transaction lent through persistence.WithTx
  When StartInstance creates instance "ext-1" with the lent context
  Then action A has been called 0 times
  When the consumer then <ending>
  Then action A has been called <calls> times

  Examples:
    | dialect  | ending                                  | calls |
    | SQLite   | rolls back and calls Discard            | 0     |
    | SQLite   | commits and does not call Resume        | 0     |
    | SQLite   | commits and calls Resume                | 1     |
    | Postgres | rolls back and calls Discard            | 0     |
    | Postgres | commits and calls Resume                | 1     |
    | MySQL    | rolls back and calls Discard            | 0     |
    | MySQL    | commits and calls Resume                | 1     |
```

#### Scenario: The coupled step's state is durable at the consumer's commit
```gherkin
Given a ProcessEngine over a SQLite store
And a definition start -> serviceTask(A) -> end
And a consumer transaction lent through persistence.WithTx
When StartInstance creates instance "dur-1" with the lent context
And the consumer commits
Then GetInstance for "dur-1" returns status StatusRunning with one token on A
And the instance's journal holds exactly the entries of the start step
```

#### Scenario: A timer armed by the coupled step is rehydrated after a restart before Resume
```gherkin
Given a ProcessDriver over a SQLite instance store and durable timer store
And a definition whose first step arms a timer "wait1"
And a consumer transaction lent through persistence.WithTx
When the instance is started with the lent context and the consumer commits
And the process stops before Resume is called
And a new ProcessDriver over the same database runs RehydrateTimers
Then timer "wait1" is scheduled
```

### Requirement: A resumed Continuation reaches the state of an unlent call
After the consumer commits and calls `Continuation.Resume`, the instance SHALL reach the same status, journal and scheduled timers as the same engine call made without a lent transaction, and every step after the coupled one MUST commit in its own engine-owned transaction, including incident records.

#### Scenario: Lent then resumed equals unlent
```gherkin
Scenario Outline: compare a lent-and-resumed start with a plain start
  Given a ProcessEngine over a <dialect> store
  And a definition start -> serviceTask(A) -> timerCatch(wait1) -> end
  When instance "plain-1" is started without a lent transaction
  And instance "lent-1" is started with a lent context, the consumer commits, and Resume returns nil
  Then "lent-1" and "plain-1" have the same status and the same journal trigger sequence
  And timer "wait1" is scheduled for both

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A failure after the coupled step is recorded as it is outside a lent transaction
```gherkin
Given a ProcessEngine over a SQLite store
And a definition start -> serviceTask(A) -> end whose action A fails with a non-retryable error
When instance "inc-1" is started with a lent context, the consumer commits, and Resume is called
And instance "inc-2" is started without a lent transaction
Then "inc-1" and "inc-2" each carry exactly one IncidentAction on A
And the IncidentAction on "inc-1" is still present when GetInstance is called from a new ProcessEngine over the same database
```

### Requirement: Engine calls in one lent transaction see each other and share one Continuation
Several engine calls made with the same lent context SHALL see each other's uncommitted writes, and their deferred work SHALL accumulate into the one `Continuation` that `persistence.WithTx` returned, run by `Resume` in call order.

#### Scenario: Create then trigger the same instance in one lent transaction
```gherkin
Scenario Outline: two calls on one instance inside one lent transaction
  Given a ProcessEngine over a <dialect> store
  And a definition start -> userTask(T) -> end
  And a consumer transaction lent through persistence.WithTx
  When StartInstance creates instance "two-1" with the lent context
  And CompleteTask completes T on "two-1" with the same lent context
  Then both calls succeed
  When the consumer commits and calls Resume
  Then "two-1" has status StatusCompleted
  And its journal holds the start step before the completion step

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: Negative control, a read outside the lent transaction does not see its uncommitted writes
```gherkin
Scenario Outline: read on another connection before the consumer commits
  Given a ProcessEngine over a <dialect> store
  And a consumer transaction lent through persistence.WithTx in which StartInstance created "iso-1"
  When GetInstance for "iso-1" is called with a context that carries no lent transaction, before the consumer commits
  Then it returns kernel.ErrInstanceNotFound

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Resume never loses an update or performs a command twice
`Continuation.Resume` SHALL re-load each instance and apply its deferred follow-up triggers through the optimistic-concurrency retry path, so that an instance moved by another actor between the consumer's commit and `Resume` loses no update and no command of the coupled step is performed more than once.

#### Scenario: Another actor moves the instance before Resume
```gherkin
Scenario Outline: concurrent move between commit and Resume
  Given a ProcessEngine over a <dialect> store with a spy action catalog
  And a definition start -> parallelSplit -> (serviceTask(A), userTask(T)) -> join -> end
  And instance "race-1" started with a lent context and committed by the consumer
  When another caller completes T on "race-1" without a lent transaction
  And the consumer then calls Resume
  Then Resume returns nil
  And action A has been called exactly 1 time
  And the journal of "race-1" holds the completion of T and the completion of A

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: A failed engine write leaves the lent transaction as it was
When an engine write inside a lent transaction fails, the engine SHALL undo only its own writes for that call, leaving the consumer's earlier writes and any savepoint the consumer opened intact, so that a consumer who ignores the error and commits persists its own rows and no partial engine state.

#### Scenario: Consumer ignores the engine error and commits
```gherkin
Scenario Outline: engine write fails part-way inside a lent transaction
  Given a ProcessEngine over a <dialect> store
  And a definition whose first step arms a timer
  And the timer store is set to fail when it persists a new timer
  And a consumer transaction lent through persistence.WithTx that has inserted its own row "c-3"
  When StartInstance creates instance "sp-1" with the lent context
  Then StartInstance returns the injected error
  When the consumer ignores the error and commits
  Then the consumer row "c-3" exists
  And the database holds no instance row, journal entry, outbox row or timer row for "sp-1"

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A consumer's own open savepoint survives the engine's
```gherkin
Scenario Outline: consumer savepoint around the engine call
  Given a ProcessEngine over a <dialect> store
  And a consumer transaction lent through persistence.WithTx with its own savepoint "consumer_sp" open
  When StartInstance creates instance "nest-1" with the lent context and <outcome>
  And the consumer rolls back to savepoint "consumer_sp" and commits
  Then the database holds no instance row for "nest-1"
  And the consumer's rows written before "consumer_sp" exist

  Examples:
    | dialect  | outcome                               |
    | Postgres | succeeds                              |
    | MySQL    | succeeds                              |
    | SQLite   | succeeds                              |
    | SQLite   | returns an injected store write error |
```

### Requirement: The caching instance store never serves a lent write before it commits
Under a lent transaction the caching instance store SHALL NOT cache any state written by the engine call; a later read through the wrapper MUST return what is committed in the database, so a consumer rollback is never served and a consumer commit is visible.

#### Scenario: Wrapper reads follow the consumer's outcome
```gherkin
Scenario Outline: CachingInstanceStore under a lent transaction
  Given a ProcessEngine over a CachingInstanceStore over a SQLite instance store
  And instance "<id>" created and loaded through the wrapper at status StatusRunning
  And a consumer transaction lent through persistence.WithTx
  When an engine call with the lent context moves "<id>" to status StatusCompleted
  And the consumer <ending>
  Then loading "<id>" through the wrapper returns status <status>

  Examples:
    | id          | ending                                | status          |
    | cache-rb    | rolls back and calls Discard          | StatusRunning   |
    | cache-ok    | commits and calls Resume              | StatusCompleted |
```
