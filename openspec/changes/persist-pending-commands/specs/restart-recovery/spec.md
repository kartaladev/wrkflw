# Spec Delta

## ADDED Requirements

### Requirement: A step's post-commit commands are made durable with the step
When a step commits, every command the runtime will perform after the commit SHALL be recorded as one pending-command row in that step's transaction, and a step that fails to commit MUST leave no row; timer, terminal and send-message commands, whose effect is already durable in the commit, and best-effort cancel actions MUST leave no row.

#### Scenario: Rows are written per command kind on every store
```gherkin
Scenario Outline: a committed step records its unperformed commands
  Given a ProcessDriver over a <store> instance store
  And the runtime is stopped immediately before it performs the step's commands
  When a step commits that emits <command>
  Then exactly <rows> pending-command row of kind "<kind>" exists for the instance

  Examples:
    | store     | command                          | rows | kind               |
    | Postgres  | InvokeAction                     | 1    | invoke_action      |
    | MySQL     | InvokeAction                     | 1    | invoke_action      |
    | SQLite    | InvokeAction                     | 1    | invoke_action      |
    | in-memory | InvokeAction                     | 1    | invoke_action      |
    | SQLite    | AwaitHuman                       | 1    | await_human        |
    | SQLite    | UpdateTask                       | 1    | update_task        |
    | SQLite    | ThrowSignal                      | 1    | throw_signal       |
    | SQLite    | StartSubInstance                 | 1    | start_sub_instance |
```

#### Scenario: Commands whose effect is already durable write no row
```gherkin
Scenario Outline: excluded command kinds
  Given a ProcessDriver over a SQLite instance store and timer store
  And the runtime is stopped immediately before it performs the step's commands
  When a step commits that emits only <command>
  Then no pending-command row exists for the instance

  Examples:
    | command            |
    | ScheduleTimer      |
    | CancelTimer        |
    | CompleteInstance   |
    | FailInstance       |
    | SendMessage        |
    | InvokeCancelAction |
```

#### Scenario: A failed commit leaves no pending-command row
```gherkin
Scenario Outline: injected failure inside the step's transaction
  Given a ProcessDriver over a <dialect> instance store and timer store
  And an instance parked on a timer whose firing enters a service task and arms a timer
  And the timer store is set to fail when it persists a new timer
  When the timer fires through ApplyTrigger
  Then ApplyTrigger returns the injected error
  And the instance version is unchanged
  And no pending-command row exists for the instance

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: Negative control, a performed command leaves no row behind
```gherkin
Given a ProcessDriver over a SQLite instance store
And a definition start -> serviceTask(A) -> end
When Drive runs to completion
Then action A was invoked exactly once
And the instance is StatusCompleted
And no pending-command row exists for the instance
```

### Requirement: Unperformed commands of a committed step are performed after a restart
When a process stops after a step commits and before its commands are performed, a recovery worker over the same stores SHALL perform each pending command once it claims it, MUST feed any follow-up trigger to the instance, and MUST delete the row once the perform completes.

#### Scenario: A service task parked by a crash completes after restart
```gherkin
Scenario Outline: crash between commit and perform, then recover
  Given a ProcessDriver over a <store> instance store with a spy action "A"
  And a definition start -> serviceTask(A) -> end
  And the runtime is stopped immediately before it performs the step's commands
  When Drive is called for instance "pc-1"
  Then action "A" has been invoked 0 times
  And instance "pc-1" is StatusRunning with a token parked on the service task
  When a fresh driver and recovery worker start over the same stores
  Then action "A" has been invoked exactly 1 time
  And instance "pc-1" reaches StatusCompleted
  And no pending-command row exists for "pc-1"

  Examples:
    | store     |
    | Postgres  |
    | MySQL     |
    | SQLite    |
    | in-memory |
```

#### Scenario: A user task whose task row was never written gets its row
```gherkin
Given a ProcessDriver over a SQLite instance store and task store
And a definition start -> userTask(T) -> end
And the runtime is stopped immediately before it performs the step's commands
When Drive is called for instance "pc-ut-1"
Then no task row exists for "pc-ut-1"
When a fresh driver and recovery worker start over the same stores
Then exactly one open task row exists for "pc-ut-1"
And completing that task through the task service completes "pc-ut-1"
```

#### Scenario: Each recoverable kind reaches its effect after restart
```gherkin
Scenario Outline: recovered effect per command kind
  Given a ProcessDriver over a SQLite instance store
  And the runtime is stopped immediately before it performs a step that emits <command>
  When a fresh driver and recovery worker start over the same stores
  Then <effect>

  Examples:
    | command          | effect                                                                 |
    | UpdateTask       | the task row reflects the update                                       |
    | ThrowSignal      | an instance waiting on the signal receives it                           |
    | StartSubInstance | exactly one child instance exists and its completion resumes the parent |
```

#### Scenario: Commands of a step that ended the instance are still performed
```gherkin
Given a definition whose end event throws signal "done"
And a second instance waiting on signal "done"
And the runtime is stopped immediately before it performs the first instance's final step commands
When a fresh driver and recovery worker start over the same stores
Then the first instance is StatusCompleted
And the second instance receives signal "done"
And no pending-command row exists for the first instance
```

#### Scenario: A running worker picks up rows left by another process's crash
```gherkin
Given a pending-command row older than the worker's claim lease
And a recovery worker that has already been running for longer than one poll interval
When one poll interval elapses
Then the row is claimed and performed
```

### Requirement: Recovery is driven by pending-command rows, not by an instance scan
Pending-command recovery SHALL load, and acquire ownership of, only instances that have a claimable pending-command row; an instance with no row MUST NOT be loaded or acquired by pending-command recovery, whatever instance store or ownership wrapper the driver uses.

#### Scenario: Healthy instances are never acquired by recovery
```gherkin
Scenario Outline: ownership taken by one recovery pass
  Given a caching instance store and a driver built over the same instance-ownership value
  And <healthy> running instances with no pending-command row
  And <marked> instances each holding one claimable pending-command row
  When one recovery pass runs
  Then the ownership port records exactly <marked> acquires
  And no healthy instance was loaded by recovery

  Examples:
    | healthy | marked |
    | 3       | 0      |
    | 3       | 1      |
    | 0       | 2      |
    | 1000    | 1      |
```

#### Scenario: One pass claims no more rows than its batch size
```gherkin
Scenario Outline: batch ceiling
  Given a recovery worker with a batch size of 10
  And <rows> claimable pending-command rows
  When the worker runs one pass
  Then exactly <claimed> rows are claimed in that pass
  And the remaining rows are claimed by later passes

  Examples:
    | rows | claimed |
    | 0    | 0       |
    | 9    | 9       |
    | 10   | 10      |
    | 11   | 10      |
```

#### Scenario: A second replica recovers rows the first replica never touched
```gherkin
Given two drivers on separate replicas over one Postgres database with session-lock ownership
And replica A has run a recovery pass with no claimable rows
And a step committed on a third, crashed replica left a pending-command row for instance "pc-r-1"
When replica B runs a recovery pass after the row's claim lease expires
Then replica B performs the row
And instance "pc-r-1" leaves the parked state
```

### Requirement: A claimed pending command is performed by one worker at a time
A pending-command row SHALL be performed only by the worker or driver holding its unexpired claim; a concurrent worker MUST NOT perform a row whose claim has not expired, and a row MUST become claimable again once its claim lease expires without the row being deleted.

#### Scenario: Two workers racing for one row perform it once
```gherkin
Scenario Outline: concurrent recovery of one row
  Given one pending-command row with an expired claim for an InvokeAction of spy action "A" on a <dialect> store
  When two recovery workers run one pass concurrently
  Then action "A" has been invoked exactly 1 time
  And no pending-command row remains

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A row the live driver is still performing is not taken
```gherkin
Given a driver performing a slow action "A" for a step it just committed
And a recovery worker whose claim lease is longer than the action's duration
When the recovery worker runs a pass while "A" is still executing
Then the worker does not invoke "A"
And when "A" returns, the driver deletes the row
```

#### Scenario: A claim abandoned by a dead worker becomes claimable again
```gherkin
Scenario Outline: lease boundary
  Given a pending-command row claimed <age> ago by a worker that has since died
  And a claim lease of 30 seconds
  When a second worker runs a pass
  Then the row is <outcome>

  Examples:
    | age | outcome             |
    | 0s  | not performed       |
    | 29s | not performed       |
    | 30s | claimed and performed |
    | 31s | claimed and performed |
    | 1h  | claimed and performed |
```

### Requirement: Recovery re-runs only the commands of a step that did not succeed
When a step emits several commands and the runtime stops, or a perform fails, part-way through them, recovery SHALL perform only the commands whose rows remain, and a command whose perform succeeded MUST NOT be performed again.

#### Scenario: A part-failed step re-drives only the remainder
```gherkin
Scenario Outline: progress within one step
  Given a step that emits <n> commands in order, each an InvokeAction of a spy action
  And the perform of command <failing> fails <passes> times before succeeding
  When recovery passes run until no pending-command row remains
  Then every command before <failing> was invoked exactly once
  And command <failing> was invoked <passes> + 1 times
  And every command after <failing> was invoked exactly once

  Examples:
    | n | failing | passes |
    | 1 | 1       | 1      |
    | 3 | 2       | 1      |
    | 3 | 3       | 10     |
```

### Requirement: A re-driven human-task creation never overwrites an existing task row
Recovery of an `AwaitHuman` command SHALL create the task row only when none exists, and MUST leave an existing row's state, claimant and candidates unchanged.

#### Scenario: A claimant survives a duplicate AwaitHuman re-drive
```gherkin
Given a user task whose task row exists and is claimed by actor "alice"
And a pending-command row for the AwaitHuman that created it, left by a crash after the row was written
When the recovery worker performs the row
Then the task is still claimed by "alice"
And its candidates are unchanged
And the pending-command row is deleted
```

#### Scenario: Negative control, a missing task row is created
```gherkin
Given a pending-command row for an AwaitHuman whose task row does not exist
When the recovery worker performs the row
Then exactly one open, unclaimed task row exists for that task
```

### Requirement: A re-driven command is idempotent toward its instance
A re-driven command SHALL be performed with the same input as the original, including the engine's `_idempotencyKey`, and when its follow-up trigger finds its token already resumed, its instance terminal, or its child already created, recovery MUST treat the command as performed and delete the row.

#### Scenario: A follow-up that is no longer applicable is absorbed
```gherkin
Scenario Outline: duplicate re-drive after a perform that already took effect
  Given a pending-command row for <command> whose effect already took place before the crash
  When the recovery worker performs the row
  Then <observed>
  And the pending-command row is deleted
  And no operator incident is raised

  Examples:
    | command          | observed                                                     |
    | InvokeAction     | the action is invoked again and its follow-up finds no token |
    | StartSubInstance | no second child instance is created                          |
```

#### Scenario: The re-driven action receives the original input
```gherkin
Given a service task inside a sub-process whose action "A" is scoped to that sub-process
And a pending-command row for its InvokeAction left by a crash
When the recovery worker performs the row
Then the scoped action "A" is invoked, not a global action of the same name
And its input carries the same "_idempotencyKey" the original invocation carried
```

#### Scenario: An engine-emitted child start is not refused for reserved keys
```gherkin
Given a pending-command row for a StartSubInstance whose input carries the parent's engine-reserved variables
When the recovery worker performs the row
Then the child instance is started
```

### Requirement: Corrupt or unknown pending-command rows are isolated
A pending-command row whose kind is unknown, or whose payload is null or cannot be decoded, SHALL be counted as a failed attempt reported with an error wrapping `engine.ErrUnknownPendingCommand` or `engine.ErrMalformedPendingCommand`, and MUST NOT panic or stop the worker from performing every other row.

#### Scenario: A bad row does not block healthy rows
```gherkin
Scenario Outline: one bad row beside one healthy row
  Given a pending-command row whose <defect>
  And a healthy pending-command row for an InvokeAction of spy action "A"
  When the recovery worker runs one pass
  Then the worker has not panicked
  And action "A" has been invoked exactly 1 time
  And the bad row remains with its attempt count incremented
  And the reported error <error>

  Examples:
    | defect                               | error                                            |
    | kind is "launch_rocket"              | wraps engine.ErrUnknownPendingCommand            |
    | kind is "Invoke_Action"              | wraps engine.ErrUnknownPendingCommand            |
    | kind is " invoke_action"             | wraps engine.ErrUnknownPendingCommand            |
    | kind is empty                        | wraps engine.ErrUnknownPendingCommand            |
    | payload is the JSON literal null     | wraps engine.ErrMalformedPendingCommand         |
    | payload is not JSON                  | wraps engine.ErrMalformedPendingCommand         |
```

#### Scenario: Negative control, a known kind spelled exactly is performed
```gherkin
Given a pending-command row of kind "invoke_action" with a valid payload
When the recovery worker runs one pass
Then the command is performed and its row deleted
```

### Requirement: A failing re-drive is retried with backoff up to a limit
Each failed re-drive of a pending-command row SHALL increment its attempt count, and the row MUST NOT be claimed again before its backoff has elapsed; once the attempt count reaches the configured maximum the row MUST stop being re-driven and be reported as dead-lettered (pending decision Q2).

#### Scenario: Attempts are counted and bounded
```gherkin
Scenario Outline: a command whose perform always fails
  Given a recovery worker configured with a maximum of <max> attempts
  And a pending-command row whose perform fails because <cause>
  When recovery passes run for longer than the sum of every backoff
  Then the perform was attempted exactly <max> times
  And the row is dead-lettered and no longer re-driven
  And a dead-letter is reported naming the instance, the command and the last error

  Examples:
    | max | cause                                        |
    | 1   | the task store refuses the write             |
    | 3   | the task store refuses the write             |
    | 3   | the child's definition is not registered     |
```

#### Scenario: A row is not re-claimed inside its backoff
```gherkin
Given a pending-command row whose last re-drive failed with a backoff of 10 seconds
When a worker runs a pass 9 seconds later
Then the row is not claimed
When a worker runs a pass 10 seconds after the failure
Then the row is claimed
```

#### Scenario: Negative control, a transient failure recovers below the limit
```gherkin
Given a recovery worker configured with a maximum of 3 attempts
And a pending-command row whose perform fails once and then succeeds
When recovery passes run until the row is gone
Then the perform was attempted exactly 2 times
And no dead-letter is reported
```

#### Scenario: Business failure of an action is not a re-drive failure
```gherkin
Given a pending-command row for an InvokeAction whose action returns an error
When the recovery worker performs the row
Then the instance receives ActionFailed and applies its retry policy
And the pending-command row is deleted with no attempt counted
```
