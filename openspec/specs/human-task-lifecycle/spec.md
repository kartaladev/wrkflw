# human-task-lifecycle Specification

## Purpose
Defines how a human task moves from unclaimed through claimed to completed or cancelled, what each operation records, and how operations that arrive too late or malformed are refused.

## Requirements

### Requirement: Claiming records the claimant and moves the task to claimed
Applying a claim to an open human task SHALL set its state to claimed and record a claim naming the claiming actor and the trigger's occurrence time.

#### Scenario: Claim on an open task
```gherkin
Scenario: An unclaimed task is claimed
  Given a running instance with an unclaimed human task
  When a HumanClaimed trigger for actor "bob" is applied
  Then the task state is claimed
  And the task's claim names actor "bob" and the trigger's occurrence time
  And the token stays waiting on the task
```

### Requirement: Human-task triggers on a closed task are refused as too late
A claim, reassignment or completion delivered to a task that is already completed or cancelled MUST fail with `engine.ErrTaskNotOpen`, which wraps `engine.ErrInvalidTransition`, and MUST leave the instance state unchanged, even while the instance itself is still running.

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
    | HumanCompleted  |
```

### Requirement: Human-task triggers on a terminal instance are refused
A claim, reassignment or completion delivered to an instance that is already completed, failed or terminated MUST fail with `engine.ErrInstanceTerminal`.

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
    | terminated | HumanClaimed    |
    | completed  | HumanClaimed    |
```

### Requirement: Completion validates the outcome against the declared set
Completing a user task that declares outcomes MUST fail with `engine.ErrOutcomeRequired` when no outcome is given and with `engine.ErrInvalidOutcome` when the outcome is not declared; a task declaring no outcomes SHALL accept any outcome or none. Both sentinels classify as HTTP 400.

#### Scenario: Outcome validation
```gherkin
Scenario Outline: Completing with an outcome
  Given a user task that declares outcomes <declared>
  When it is completed with outcome <outcome>
  Then the result is <result>

  Examples:
    | declared            | outcome    | result                       |
    | ["approve","reject"]| "approve"  | completed                    |
    | ["approve","reject"]| "maybe"    | engine.ErrInvalidOutcome     |
    | ["approve","reject"]| ""         | engine.ErrOutcomeRequired    |
    | []                  | "anything" | completed                    |
    | []                  | ""         | completed                    |
```

#### Scenario: Outcome sentinels map to 400
```gherkin
Scenario Outline: HTTP classification of outcome errors
  Given a completion error wrapping <sentinel>
  When the HTTP error classifier renders it
  Then the status is 400 with error code "bad_request"

  Examples:
    | sentinel                  |
    | engine.ErrInvalidOutcome  |
    | engine.ErrOutcomeRequired |
```

### Requirement: A wait-mode manual task completes only on a bare trigger
Completing a manual user task that waits for a trigger MUST fail with `engine.ErrManualTaskPayload` when the completion carries an output, an outcome or a note, and SHALL succeed when it carries none.

#### Scenario: Manual task payloads
```gherkin
Scenario Outline: Completing a wait-mode manual task
  Given a wait-mode manual user task
  When it is completed carrying <payload>
  Then the result is <result>

  Examples:
    | payload              | result                       |
    | nothing              | completed                    |
    | an outcome           | engine.ErrManualTaskPayload  |
    | a note               | engine.ErrManualTaskPayload  |
    | a non-empty output   | engine.ErrManualTaskPayload  |
```

### Requirement: Completion records who completed the task and with what disposition
Completing an open human task SHALL set its state to completed, record the completing actor, the occurrence time, the outcome and the note on the task, merge the output into the process variables, and advance the waiting token.

#### Scenario: Completion audit
```gherkin
Scenario: A task is completed with outcome and note
  Given a running instance with an open user task that declares no outcomes
  When actor "alice" completes it with outcome "approve", note "looks good" and output {"approved": true}
  Then the task state is completed
  And the task's completion names actor "alice", the occurrence time, outcome "approve" and note "looks good"
  And the process variable "approved" is true
  And the token has advanced past the task
```

### Requirement: Reassignment requires a target
The task service MUST refuse a reassignment whose target is empty with `engine.ErrEmptyReassignTarget` before reading the task, and the engine MUST refuse a HumanReassigned trigger with an empty target with the same sentinel, which classifies as HTTP 400.

#### Scenario: Reassignment inputs
```gherkin
Scenario Outline: Validating a reassignment request
  Given <task>
  When the claimant "alice" requests a reassignment from "alice" to <to>
  Then the result is <result>

  Examples:
    | task                                   | to      | result                                                          |
    | a task currently claimed by "alice"    | ""      | engine.ErrEmptyReassignTarget, no trigger                       |
    | a task id no store holds               | ""      | engine.ErrEmptyReassignTarget, not humantask.ErrTaskNotFound    |
    | a task currently claimed by "alice"    | "bob"   | a HumanReassigned trigger naming "bob"                          |
```

#### Scenario: Empty target refused at the engine too
```gherkin
Scenario: Engine refuses a HumanReassigned with no target
  Given an open human task
  When a HumanReassigned trigger with an empty target is applied
  Then the step fails with engine.ErrEmptyReassignTarget
  And the HTTP error classifier renders it as 400
```

### Requirement: Reassignment replaces the claim rather than appending
Applying a reassignment to a claimed task SHALL leave it claimed with a single claim naming the target actor ID, overwriting the previous claim.

#### Scenario: Claim is overwritten
```gherkin
Scenario: Reassigning a claimed task
  Given a task claimed by "alice"
  When a HumanReassigned trigger from "alice" to "bob" is applied
  Then the task state is claimed
  And the task's only claim names actor "bob"
```

### Requirement: A candidate resolution replaces the list and leaves closed tasks frozen
Applying a candidate resolution to an open task SHALL replace its candidate list wholesale, and re-applying the same resolution SHALL be idempotent; applying one to a completed or cancelled task, or to a terminal instance, MUST leave the state unchanged and return no error.

#### Scenario: Candidate resolution
```gherkin
Scenario Outline: Applying a HumanCandidatesResolved trigger
  Given <target>
  When a HumanCandidatesResolved trigger with candidates <new> is applied
  Then <result>

  Examples:
    | target                                         | new         | result                                              |
    | an open task with candidates [a, b]            | [c]         | the task's candidates are exactly [c]               |
    | an open task whose candidates are already [c]  | [c]         | the task's candidates are exactly [c]               |
    | a closed task with candidates [a, b]           | [c]         | candidates stay [a, b], no command, no error        |
    | an open task on a terminal instance            | [c]         | the state is returned unchanged, no command, no error |
```

### Requirement: Refreshing candidates applies to open tasks only
The task service's RefreshCandidates MUST fail with `ErrNoActorResolver` when no actor resolver is configured, with `humantask.ErrTaskNotFound` for an unknown task, and with `task.ErrTaskNotOpen` (identical to `engine.ErrTaskNotOpen`, classifying 422) for a completed or cancelled task; otherwise it SHALL return a trigger carrying the freshly resolved candidates.

#### Scenario: Refresh outcomes
```gherkin
Scenario Outline: Refreshing a task's candidates
  Given <setup>
  When an eligible actor refreshes the task's candidates
  Then the result is <result>

  Examples:
    | setup                                              | result                                                     |
    | an open task and a configured resolver             | a HumanCandidatesResolved trigger with the resolved actors |
    | an open task and a resolver whose registry shrank  | the trigger omits the departed actor                       |
    | no resolver configured                             | ErrNoActorResolver                                         |
    | an unknown task id                                 | humantask.ErrTaskNotFound                                  |
    | a completed task                                   | task.ErrTaskNotOpen                                        |
```

#### Scenario: A refresh on a closed task classifies as a state conflict
```gherkin
Scenario: HTTP classification of a closed-task refresh
  Given a refresh error wrapping task.ErrTaskNotOpen
  When the HTTP error classifier renders it
  Then the status is 422 with error code "conflict_state"
```

### Requirement: Task stores refuse a task whose claim contradicts its state
Every shipped task store MUST reject on Upsert, with an error wrapping `humantask.ErrInvalidTask` and persisting nothing, a claimed task without a claim, an unclaimed task carrying a claim, or an unknown state; completed and cancelled tasks SHALL be accepted with or without a claim.

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

### Requirement: An empty actor ID lists no assigned tasks
A task store's assigned-to query MUST return an empty result, not an error and not the unclaimed tasks, when asked for the empty actor ID, and SHALL return a non-empty ID's claimed tasks sorted by task ID.

#### Scenario: Empty actor ID is not a wildcard
```gherkin
Scenario Outline: AssignedTo with an empty ID
  Given the <store> task store holding unclaimed tasks
  When AssignedTo is called with actor ID ""
  Then no error is returned
  And the result is empty

  Examples:
    | store           |
    | memory          |
    | sql on postgres |
    | sql on mysql    |
    | sql on sqlite   |
```

#### Scenario: A real claimant still sees its tasks
```gherkin
Scenario Outline: AssignedTo with a claimant ID
  Given the <store> task store holding tasks "tok-a2" and "tok-a1" claimed by "actor-a", one claimed by "actor-b" and one unclaimed
  When AssignedTo is called with actor ID "actor-a"
  Then the result is ["tok-a1", "tok-a2"] in that order

  Examples:
    | store           |
    | memory          |
    | sql on postgres |
    | sql on mysql    |
    | sql on sqlite   |
```
