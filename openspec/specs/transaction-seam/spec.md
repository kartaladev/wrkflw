# transaction-seam Specification

## Purpose
Defines the transactional unit the engine owns on a durable store: one applied step commits all of its writes together, and the optional `kernel.TxRunner` capability lets a caller group store writes into one unit that commits or rolls back as a whole.

## Requirements

### Requirement: A step's state and timer writes commit together or not at all
On a SQL-backed instance store with a durable timer store wired through `runtime.WithTimerStore`, the instance state and the timer rows a step arms or cancels SHALL commit in one transaction; when any write of the step fails, the instance MUST stay at its previous version and its timer rows MUST be exactly those that existed before the step.

#### Scenario: A failed step that arms a timer leaves no trace
```gherkin
Scenario Outline: injected failure inside the step's transaction
  Given a ProcessDriver over a <dialect> instance store and timer store
  And an instance parked on a timer "wait1" whose firing arms a second timer "wait2"
  And the timer store is set to fail when it persists a new timer
  When wait1 fires through ApplyTrigger
  Then ApplyTrigger returns the injected error
  And the instance version is unchanged and its status is still StatusRunning
  And the only armed timer row is still wait1
  And no timer other than wait1 is scheduled

  Examples:
    | dialect  |
    | SQLite   |
    | Postgres |
```

#### Scenario: A failed first step leaves no instance and nothing that can fire
```gherkin
Given a ProcessDriver over a SQLite instance store and timer store
And a definition whose first step arms an immediate one-shot timer
And the timer store is set to fail when it persists a new timer
When Drive is called for instance "txf-create-1"
Then Drive returns the injected error
And loading "txf-create-1" returns kernel.ErrInstanceNotFound
And no timer row is armed
And advancing the clock and ticking the scheduler still leaves "txf-create-1" not found
```

#### Scenario: A failed step that cancels a timer keeps the timer live
```gherkin
Given a ProcessDriver over a SQLite instance store and timer store
And an instance with a recurring reminder timer armed on a human task
And the timer store is set to fail after deleting a timer row
When the human task is completed, which cancels the reminder
Then the step returns the injected error
And the reminder's timer row is still armed
And the reminder still fires on the next occurrence
```

#### Scenario: Negative control, a successful step persists its timer
```gherkin
Given a ProcessDriver over a SQLite instance store and timer store
And a definition whose first step arms a past-due one-shot timer
When Drive is called
Then the timer is persisted exactly once
And the timer's fire observes the committed instance and completes it
```

### Requirement: RunInTx commits or rolls back the caller's unit as a whole
A SQL-backed instance store's `RunInTx` SHALL commit every store write made with the context it hands to the callback when the callback returns nil, and SHALL roll all of them back and return the callback's error unchanged (matching under `errors.Is`) when the callback returns an error.

#### Scenario: Callback outcome decides the unit
```gherkin
Scenario Outline: RunInTx on each SQL dialect
  Given a <dialect> instance store
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

### Requirement: The caching instance store never serves a rolled-back write
The caching instance store's `RunInTx` SHALL forward to a backing store that implements `kernel.TxRunner`, and when the unit fails by error or by panic it MUST evict every instance it cached during the unit, so a later read returns the last committed state; a panic MUST propagate unchanged.

#### Scenario: Failed unit evicts the optimistic cache entry
```gherkin
Scenario Outline: commit inside RunInTx followed by failure
  Given a CachingInstanceStore over a SQLite instance store
  And instance "<id>" created and loaded through the wrapper at status StatusRunning
  When RunInTx commits status StatusCompleted for "<id>" through the wrapper and then <failure>
  Then <propagation>
  And loading "<id>" through the wrapper returns status StatusRunning and the pre-unit version
  And loading "<id>" from the backing store returns status StatusRunning and the pre-unit version

  Examples:
    | id               | failure                               | propagation                                       |
    | wrap-tx-rollback | the callback returns an error         | RunInTx returns that error (errors.Is)            |
    | wrap-tx-panic    | the callback panics "wrapper run in tx: kaboom" | the panic value "wrapper run in tx: kaboom" propagates out of RunInTx |
```

#### Scenario: Negative control, a successful unit stays readable through the wrapper
```gherkin
Given a CachingInstanceStore over a SQLite instance store
When RunInTx runs a callback that creates "wrap-tx-commit" through the wrapper and returns nil
Then RunInTx returns nil
And loading "wrap-tx-commit" through the wrapper succeeds
```

#### Scenario: Backing store without the capability still runs the callback
```gherkin
Given a CachingInstanceStore over a backing store that does not implement kernel.TxRunner
When RunInTx runs a callback that returns nil
Then the callback has run
And RunInTx returns nil
```
