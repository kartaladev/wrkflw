## MODIFIED Requirements

### Requirement: Claiming records the claimant and moves the task to claimed
Applying a claim to an open human task SHALL record a claim naming the claiming actor and the trigger's occurrence time, and SHALL set an unclaimed or claimed task's state to claimed; a progressed task SHALL stay progressed with its applied updates kept.

#### Scenario: Claim on an open task
```gherkin
Scenario: An unclaimed task is claimed
  Given a running instance with an unclaimed human task
  When a HumanClaimed trigger for actor "bob" is applied
  Then the task state is claimed
  And the task's claim names actor "bob" and the trigger's occurrence time
  And the token stays waiting on the task
```

#### Scenario: A claim does not regress a progressed task
```gherkin
Scenario: Progress survives a new claimant
  Given a running instance with a human task claimed by "alice" in state progressed with update "a" applied
  When a HumanClaimed trigger for actor "bob" is applied
  Then the task state is progressed
  And the task's claim names actor "bob"
  And the task's update "a" still references its applied seq
```

### Requirement: Human-task triggers on a closed task are refused as too late
A claim, reassignment, update submission or completion delivered to a task that is already completed or cancelled MUST fail with `engine.ErrTaskNotOpen`, which wraps `engine.ErrInvalidTransition`, and MUST leave the instance state unchanged, even while the instance itself is still running.

#### Scenario: Closed task on a running instance
```gherkin
Scenario Outline: Too-late trigger is refused on the task-lifetime key
  Given a running instance whose human task was cancelled by an interrupting boundary
  When a <trigger> trigger for that task is applied
  Then the step fails with engine.ErrTaskNotOpen
  And errors.Is(err, engine.ErrInvalidTransition) is true
  And errors.Is(err, engine.ErrInstanceTerminal) is false
  And the caller's instance state is not mutated

  Examples:
    | trigger         |
    | HumanClaimed    |
    | HumanReassigned |
    | HumanProgressed |
    | HumanCompleted  |
```

#### Scenario: The same triggers on an open task proceed
```gherkin
Scenario Outline: Open task accepts the trigger
  Given a running instance with an open human task
  When a valid <trigger> trigger for that task is applied
  Then the step succeeds

  Examples:
    | trigger         |
    | HumanClaimed    |
    | HumanReassigned |
    | HumanProgressed |
    | HumanCompleted  |
```

### Requirement: Human-task triggers on a terminal instance are refused
A claim, reassignment, update submission or completion delivered to an instance that is already completed, failed or terminated MUST fail with `engine.ErrInstanceTerminal`.

#### Scenario: Terminal instance
```gherkin
Scenario Outline: Terminal instance refuses human triggers
  Given an instance whose status is <status> and which still records the human task
  When a <trigger> trigger for that task is applied
  Then the step fails with engine.ErrInstanceTerminal

  Examples:
    | status     | trigger         |
    | failed     | HumanClaimed    |
    | failed     | HumanReassigned |
    | failed     | HumanCompleted  |
    | failed     | HumanProgressed |
    | terminated | HumanClaimed    |
    | terminated | HumanProgressed |
    | completed  | HumanClaimed    |
    | completed  | HumanProgressed |
```

### Requirement: Reassignment replaces the claim rather than appending
Applying a reassignment to a claimed or progressed task SHALL leave it with a single claim naming the target actor ID, overwriting the previous claim; a claimed task SHALL stay claimed, and a progressed task SHALL stay progressed with its applied updates kept.

#### Scenario: Claim is overwritten
```gherkin
Scenario: Reassigning a claimed task
  Given a task claimed by "alice"
  When a HumanReassigned trigger from "alice" to "bob" is applied
  Then the task state is claimed
  And the task's only claim names actor "bob"
```

#### Scenario: Reassigning a progressed task keeps its progress
```gherkin
Scenario: Updates are retained on reassignment
  Given a task claimed by "alice" in state progressed with updates "a" and "b" applied
  When a HumanReassigned trigger from "alice" to "bob" is applied
  Then the task state is progressed
  And the task's only claim names actor "bob"
  And the task's updates "a" and "b" reference the same seqs as before
```

### Requirement: Task stores refuse a task whose claim contradicts its state
Every shipped task store MUST reject on Upsert, with an error wrapping `humantask.ErrInvalidTask` and persisting nothing, a claimed or progressed task without a claim, an unclaimed task carrying a claim, or an unknown state; completed and cancelled tasks SHALL be accepted with or without a claim.

#### Scenario: Claim invariant on write
```gherkin
Scenario Outline: Upserting a task
  Given the <store> task store
  When a task in state <state> <claim> is upserted
  Then the result is <result>

  Examples:
    | store              | state       | claim              | result                                         |
    | memory             | claimed     | without a claim    | humantask.ErrInvalidTask, nothing persisted    |
    | memory             | unclaimed   | with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | memory             | out of range| without a claim    | humantask.ErrInvalidTask, nothing persisted    |
    | memory             | unclaimed   | without a claim    | stored                                         |
    | memory             | claimed     | with a claim       | stored                                         |
    | memory             | completed   | without a claim    | stored                                         |
    | memory             | cancelled   | with a claim       | stored                                         |
    | sql on postgres    | claimed     | without a claim    | humantask.ErrInvalidTask, nothing persisted    |
    | sql on mysql       | claimed     | without a claim    | humantask.ErrInvalidTask, nothing persisted    |
    | sql on sqlite      | claimed     | without a claim    | humantask.ErrInvalidTask, nothing persisted    |
    | sql on postgres    | unclaimed   | with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on mysql       | unclaimed   | with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on sqlite      | unclaimed   | with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on postgres    | out of range| with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on mysql       | out of range| with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on sqlite      | out of range| with a claim       | humantask.ErrInvalidTask, nothing persisted    |
    | sql on postgres    | claimed     | with a claim       | stored                                         |
    | sql on mysql       | claimed     | with a claim       | stored                                         |
    | sql on sqlite      | claimed     | with a claim       | stored                                         |
```

#### Scenario: The progressed state on write
```gherkin
Scenario Outline: Upserting a progressed task
  Given the <store> task store
  When a task <shape> is upserted
  Then the result is <result>

  Examples:
    | store           | shape                                                   | result                                      |
    | memory          | in state progressed with a claim and one applied update | stored, and read back as progressed         |
    | memory          | in state progressed without a claim                     | humantask.ErrInvalidTask, nothing persisted |
    | sql on postgres | in state progressed with a claim and one applied update | stored, and read back as progressed         |
    | sql on mysql    | in state progressed with a claim and one applied update | stored, and read back as progressed         |
    | sql on sqlite   | in state progressed with a claim and one applied update | stored, and read back as progressed         |
    | sql on postgres | in state progressed without a claim                     | humantask.ErrInvalidTask, nothing persisted |
    | sql on mysql    | in state progressed without a claim                     | humantask.ErrInvalidTask, nothing persisted |
    | sql on sqlite   | in state progressed without a claim                     | humantask.ErrInvalidTask, nothing persisted |
```

#### Scenario: A progressed task still appears in its claimant's assigned list
```gherkin
Scenario Outline: AssignedTo includes progressed tasks
  Given the <store> task store holding task "t1" claimed by "alice" in state progressed
  When AssignedTo is called with actor ID "alice"
  Then the result contains "t1"

  Examples:
    | store           |
    | memory          |
    | sql on postgres |
    | sql on mysql    |
    | sql on sqlite   |
```
