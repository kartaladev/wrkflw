## Purpose

Defines declared updates on a user task: ordered, named and individually validated submissions, optionally backed by a consumer action, which an actor makes before completion. It covers how they are gated, applied, rejected and audited, and how completion assembles them.

## ADDED Requirements

### Requirement: An action-less update is applied in the step that submits it
Submitting a declared update that names no action to an open task MUST, in one committed step, record an `applied` row whose value is the submitted input, reference it from the task's updates by name, move the task to `progressed`, and return the row's seq.

#### Scenario: First action-less update
```gherkin
Scenario: The submission becomes the update's value
  Given an open, unclaimed user task "t1" declaring update "assessment" with no action
  When actor "alice" submits update "assessment" with input {"score": 4}
  Then the submission returns seq 1
  And task "t1" is in state progressed and claimed by "alice"
  And the task's update "assessment" references seq 1
  And the update history of "t1" is exactly one row: seq 1, "assessment", applied, value {"score": 4}, actor "alice"
  And the instance variables do not contain "assessment" or "score"
```

#### Scenario: The snapshot holds references, never values
```gherkin
Scenario: Values live in the audit table only
  Given task "t1" with update "assessment" applied with input {"secret": "111-22-3333"}
  When the instance snapshot is loaded from the store
  Then the snapshot's task "t1" names update "assessment" with its seq and time
  And the snapshot contains no occurrence of "111-22-3333"
```

### Requirement: An update's input is validated against that update's descriptor before any state change
A submission whose input fails the named update's validation descriptor MUST be refused with an error wrapping `validation.ErrInvalidInput`, with no row written and the instance unchanged; an update declaring no descriptor SHALL accept any JSON object.

#### Scenario: Per-update validation
```gherkin
Scenario Outline: The update's own descriptor decides
  Given user task "t1" declaring update "assessment" with <descriptor>
  When update "assessment" is submitted with input <input>
  Then the result is <result>

  Examples:
    | descriptor                                        | input          | result                                         |
    | json-schema requiring integer "score"             | {"score": 4}   | accepted, seq returned                         |
    | json-schema requiring integer "score"             | {"score": "4"} | validation.ErrInvalidInput, no row, no step    |
    | json-schema requiring integer "score"             | {}             | validation.ErrInvalidInput, no row, no step    |
    | no descriptor                                     | {}             | accepted, seq returned                         |
    | no descriptor                                     | {"any": [1]}   | accepted, seq returned                         |
```

#### Scenario: The task's completion descriptor is not applied to an update
```gherkin
Scenario: Two slots, two descriptors
  Given user task "t1" whose completion validation requires "approved"
  And whose update "assessment" declares no descriptor
  When update "assessment" is submitted with input {"score": 4}
  Then the submission is accepted
```

### Requirement: Submitting an update the task does not declare is refused
A submission naming an update the task's node does not declare, compared exactly, MUST be refused with `engine.ErrUnknownUpdate` before any state change.

#### Scenario: Update names are compared exactly
```gherkin
Scenario Outline: Only a declared name is accepted
  Given user task "t1" declaring exactly the updates "assessment" and "attachments"
  When update <name> is submitted with input {}
  Then the result is <result>

  Examples:
    | name           | result                     |
    | "assessment"   | accepted                   |
    | "Assessment"   | engine.ErrUnknownUpdate    |
    | "assessment "  | engine.ErrUnknownUpdate    |
    | "assessmént"   | engine.ErrUnknownUpdate    |
    | ""             | engine.ErrUnknownUpdate    |
    | "review"       | engine.ErrUnknownUpdate    |
```

#### Scenario: A task with no declared updates refuses every update
```gherkin
Scenario: Updates are opt-in per node
  Given user task "t1" whose node declares no updates
  When update "assessment" is submitted with input {}
  Then the step fails with engine.ErrUnknownUpdate
  And the instance version is unchanged
```

### Requirement: An update is submittable only once every update it depends on is applied
A submission whose update names dependencies not all applied MUST be refused with an error matching `engine.ErrUpdateDependencyMissing` that names every missing dependency, before any state change; resubmitting a dependency MUST NOT clear or invalidate its dependents.

#### Scenario: Dependency gate
```gherkin
Scenario Outline: depends_on gates submission order
  Given user task "t1" declaring "a", "b" depending on ["a"], and "c" depending on ["a", "b"]
  And the applied updates are <applied>
  When update <name> is submitted
  Then the result is <result>

  Examples:
    | applied     | name | result                                                        |
    | none        | "a"  | accepted                                                      |
    | none        | "b"  | engine.ErrUpdateDependencyMissing naming ["a"]                |
    | none        | "c"  | engine.ErrUpdateDependencyMissing naming ["a", "b"]           |
    | ["a"]       | "c"  | engine.ErrUpdateDependencyMissing naming ["b"]                |
    | ["a"]       | "b"  | accepted                                                      |
    | ["a", "b"]  | "c"  | accepted                                                      |
```

#### Scenario: No cascade on resubmitting a dependency
```gherkin
Scenario: Dependents keep their values
  Given user task "t1" with "a" and "b" depending on ["a"], both applied
  When update "a" is resubmitted under resubmit replace
  Then update "b" still references its earlier applied seq
  And update "b" is not reported missing at completion
```

#### Scenario: A rejected dependency does not satisfy the gate
```gherkin
Scenario: Only applied counts
  Given user task "t1" with "b" depending on ["a"]
  And the only row for "a" is rejected
  When update "b" is submitted
  Then the step fails with engine.ErrUpdateDependencyMissing naming ["a"]
```

### Requirement: Resubmission follows the update's declared policy
Resubmitting an applied update under `replace` (the default) MUST apply the new value and keep the earlier rows in history; under `refuse` it MUST leave the applied value unchanged and record the attempt as a `rejected` row.

#### Scenario: Replace and refuse
```gherkin
Scenario Outline: Second submission of an applied update
  Given user task "t1" whose update "u" declares resubmit <policy> and is applied at seq 1 with value {"v": 1}
  When update "u" is submitted again with input {"v": 2}
  Then update "u" references seq <ref> and its latest applied value is <value>
  And the history of "t1" is <history>

  Examples:
    | policy  | ref | value     | history                                  |
    | replace | 2   | {"v": 2}  | seq 1 applied, seq 2 applied             |
    | refuse  | 1   | {"v": 1}  | seq 1 applied, seq 2 rejected            |
```

#### Scenario: How the refusal reaches the submitter (pending Q1)
```gherkin
Scenario: A refused resubmission is reported as a refusal
  Given user task "t1" whose update "u" declares resubmit refuse and is applied
  When update "u" is submitted again
  Then the submission fails with engine.ErrUpdateRefused carrying the rejected row's seq
  And the rejected row is committed
```

#### Scenario: Negative control - a first submission under refuse is accepted
```gherkin
Scenario: refuse limits resubmission only
  Given user task "t1" whose update "u" declares resubmit refuse and has no rows
  When update "u" is submitted
  Then it is applied
```

### Requirement: An action-backed update runs its action durably and closes on the action's outcome
Submitting an update that names an action MUST commit a `submitted` row and invoke the action with a `humantask.Call` of kind `update`; the action's success MUST close it `applied` with the action's output as value, entering `progressed`, and nothing is merged into the instance variables.

#### Scenario: Envelope of an update action
```gherkin
Scenario: The action sees the submission, the previous value and every applied value
  Given instance "i1", user task "t1" at node "review" with update "assessment" backed by action "review.assess"
  And update "notes" applied with value {"n": 1}
  And update "assessment" applied earlier with value {"score": 3}
  When actor "alice" submits update "assessment" with input {"score": 4} and receives seq 5
  Then "review.assess" receives an envelope with "kind" "update", "update" "assessment", "task_id" "t1", "instance_id" "i1", "node_id" "review"
  And "actor" names "alice", "input" is {"score": 4}, "previous" is {"score": 3}
  And "applied" is {"notes": {"n": 1}, "assessment": {"score": 3}}
  And "idempotency_key" is "t1:5"
```

#### Scenario: Submitted then applied
```gherkin
Scenario: Two rows for an action-backed update
  Given an open, unclaimed user task "t1" with update "assessment" backed by action "review.assess"
  When actor "alice" submits it with input {"score": 4}
  Then a submitted row with seq 1, input {"score": 4} and actor "alice" is committed
  And task "t1" is claimed by "alice" and not yet progressed
  When "review.assess" completes with output {"score": 4, "grade": "B"}
  Then an applied row with seq 2, submitted_seq 1 and value {"score": 4, "grade": "B"} is committed
  And task "t1" is progressed and its update "assessment" references seq 2
  And the instance variables are unchanged
```

#### Scenario: First submission carries no previous value
```gherkin
Scenario: previous is absent until a value exists
  Given update "assessment" backed by an action with no applied row
  When it is submitted
  Then the action's envelope has no "previous" key
```

### Requirement: An update action's terminal failure rejects the update to its actor, never to the operator
After the node's retry policy is exhausted, an update action's failure MUST close the update `rejected` with the failure's text as reason and restore the task to its state before the submission, and MUST NOT take the recovery flow, an error boundary or raise an incident.

#### Scenario: Rejection restores the task
```gherkin
Scenario Outline: A failed update action
  Given a running instance with task "t1" in state <before>, update "u" backed by action "u.act" that fails with <error>
  And the node declares a retry policy, a recovery flow and an error boundary
  When "u" is submitted and the action's failure becomes terminal
  Then a rejected row closes the submission with reason containing <text>
  And task "t1" is in state <before> and update "u" still references <ref>
  And the instance is running with no incident, and the token is still on the task
  And the recovery flow and the error boundary were not taken

  Examples:
    | before    | error                                  | text           | ref                 |
    | claimed   | a non-retryable error "locked"         | "locked"       | nothing             |
    | progressed| a non-retryable error "locked"         | "locked"       | its earlier seq     |
    | claimed   | a retryable error until retries run out| the last error | nothing             |
```

#### Scenario: A retry re-invokes the update action with the same envelope
```gherkin
Scenario: Retries are transparent to the actor
  Given update "u" backed by action "u.act" that fails retryably once and then succeeds
  And the node's retry policy allows a second attempt
  When "u" is submitted
  Then "u.act" is invoked twice with byte-identical envelopes
  And no other action is invoked for the retry
  And the update closes applied
```

#### Scenario: An unregistered action name rejects the update
```gherkin
Scenario: Unknown action is the actor's rejection, not an incident
  Given update "u" naming action "no.such.action", which no catalog registers
  When "u" is submitted
  Then a rejected row closes it with a reason naming "no.such.action"
  And no incident is raised
```

### Requirement: Only one update may be in flight on a task
While an update action is pending on a task, a second update, a claim, a reassignment or a completion of that task MUST be refused with `engine.ErrUpdateInFlight` and leave the instance unchanged; once the action returns, each SHALL be accepted again.

#### Scenario: Guard per human operation
```gherkin
Scenario Outline: The pending action blocks every task operation
  Given task "t1" with update "u" submitted and its action not yet returned
  When <operation> is applied to "t1"
  Then the step fails with engine.ErrUpdateInFlight
  And errors.Is(err, engine.ErrInvalidTransition) is true
  And the instance version is unchanged

  Examples:
    | operation                          |
    | a submission of update "u"         |
    | a submission of another update "w" |
    | a claim by "bob"                   |
    | a reassignment to "bob"            |
    | a completion                       |
```

#### Scenario: Negative control - the guard lifts when the action returns
```gherkin
Scenario Outline: After applied or rejected, operations proceed
  Given task "t1" whose pending update action has just <closed>
  When <operation> is applied to "t1" by an eligible actor
  Then it is not refused with engine.ErrUpdateInFlight

  Examples:
    | closed    | operation             |
    | succeeded | a submission of "w"   |
    | failed    | a claim               |
    | succeeded | a reassignment        |
    | succeeded | a completion          |
```

#### Scenario: Two concurrent submissions on one task
```gherkin
Scenario: The instance version admits one
  Given an open task "t1" with updates "u" and "w", both action-backed
  When submissions of "u" and "w" race through one ProcessDriver on the same loaded version
  Then exactly one commits a submitted row
  And the other, re-applied by the driver's concurrency retry, fails with engine.ErrUpdateInFlight
  And no seq is used twice for "t1"
```

### Requirement: Closing a task while its update action is in flight abandons the update
When a task with a pending update is closed by a deadline, an interrupting boundary, instance cancellation or the stale-command sweep, the closing step MUST record an `abandoned` row for the pending submission, and the action's later result MUST NOT record an `applied` or `rejected` row.

#### Scenario: Each closing path
```gherkin
Scenario Outline: The pending update is closed with the task
  Given task "t1" with update "u" submitted at seq 3 and its action not yet returned
  When <closer> closes the task
  Then an abandoned row with submitted_seq 3 is committed in the same step
  And task "t1" is cancelled with no pending update

  Examples:
    | closer                                   |
    | the task's deadline firing               |
    | an interrupting boundary on the task     |
    | cancellation of the instance             |
    | the stale-command sweep                  |
```

#### Scenario: A late action result writes nothing
```gherkin
Scenario: The abandoned row is the last row
  Given task "t1" whose pending update was abandoned by a deadline
  When the update's action completes afterwards
  Then the history of "t1" ends with the abandoned row
  And no applied row is written for the abandoned submission
```

#### Scenario: Negative control - closing a task with nothing pending writes no row
```gherkin
Scenario: No pending update, no abandoned row
  Given task "t1" with update "u" applied and nothing pending
  When the task's deadline fires
  Then no row is added to the history of "t1"
```

### Requirement: A progressed task always carries a claim and an applied update, and stays open
A task in state `progressed` MUST carry a claim and at least one applied update, a task with a pending update MUST be claimed or progressed, and a progressed task SHALL be open to every human operation an open task accepts.

#### Scenario: State entry
```gherkin
Scenario Outline: Which events enter progressed
  Given task "t1" in state <before>
  When <event>
  Then task "t1" is in state <after>

  Examples:
    | before     | event                                              | after      |
    | unclaimed  | an action-less update is applied                   | progressed |
    | claimed    | an action-less update is applied                   | progressed |
    | unclaimed  | an action-backed update is submitted               | claimed    |
    | claimed    | that update's action succeeds                      | progressed |
    | claimed    | that update's action fails terminally              | claimed    |
    | progressed | another update is applied                          | progressed |
    | progressed | the task is completed                              | completed  |
```

#### Scenario: The snapshot invariants are enforced
```gherkin
Scenario Outline: humantask validation of a task record
  Given a task record <shape>
  When it is validated
  Then the result is <result>

  Examples:
    | shape                                                       | result                    |
    | in state progressed with a claim and one applied update     | valid                     |
    | in state progressed with a claim and no applied update      | humantask.ErrInvalidTask  |
    | in state progressed with no claim and one applied update    | humantask.ErrInvalidTask  |
    | in state unclaimed with a pending update                    | humantask.ErrInvalidTask  |
    | in state claimed with a claim and a pending update          | valid                     |
    | in state progressed with a claim, an update and a pending one | valid                   |
```

#### Scenario: Open-task operations on a progressed task
```gherkin
Scenario Outline: progressed is open
  Given task "t1" in state progressed with nothing pending
  When a valid <trigger> is applied
  Then the step succeeds

  Examples:
    | trigger                 |
    | HumanClaimed            |
    | HumanReassigned         |
    | HumanProgressed         |
    | HumanCompleted          |
    | HumanCandidatesResolved |
```

### Requirement: Completion requires every required update to be applied
Completing a task whose node declares updates MUST be refused with an error matching `engine.ErrRequiredUpdatesMissing`, naming every required update without an applied row in declaration order, before any state change; an update declared `required: false` SHALL never block completion.

#### Scenario: Completability
```gherkin
Scenario Outline: Required, optional and missing
  Given task "t1" declaring "a" (required), "b" (required: false) and "c" (required)
  And the applied updates are <applied>
  When "t1" is completed
  Then the result is <result>
  And humantask.Completable reports <report>

  Examples:
    | applied     | result                                               | report                     |
    | none        | engine.ErrRequiredUpdatesMissing naming ["a", "c"]   | not completable, ["a","c"] |
    | ["a", "b"]  | engine.ErrRequiredUpdatesMissing naming ["c"]        | not completable, ["c"]     |
    | ["a", "c"]  | completed                                            | completable, []            |
    | ["c", "a"]  | completed                                            | completable, []            |
```

#### Scenario: A rejected or abandoned row does not count
```gherkin
Scenario Outline: Only applied satisfies required
  Given task "t1" declaring required update "a" whose only row is <status>
  When "t1" is completed
  Then the step fails with engine.ErrRequiredUpdatesMissing naming ["a"]

  Examples:
    | status    |
    | rejected  |
    | submitted |
```

#### Scenario: Negative control - a task with no declared updates is always completable
```gherkin
Scenario: No declaration, no gate
  Given task "t1" whose node declares no updates
  When "t1" is completed with output {"approved": true}
  Then the task is completed
  And humantask.Completable reports completable with no missing updates
```

### Requirement: Completion merges applied update values namespaced by update name
Completing a task that declares updates MUST validate the node's completion descriptor against the assembled latest applied values, then merge each applied value into the instance variables under its update's name; a task declaring no updates MUST keep today's flat merge of its output with a byte-identical snapshot and journal.

#### Scenario: Namespaced merge
```gherkin
Scenario: Two updates with overlapping keys do not overwrite each other
  Given task "t1" with updates "a" applied {"score": 1} and "b" applied {"score": 2}
  When "t1" is completed
  Then the instance variable "a" is {"score": 1} and "b" is {"score": 2}
  And no top-level variable "score" is written by the completion
```

#### Scenario: Whole-output descriptor over the assembled values
```gherkin
Scenario Outline: The completion slot sees the assembled map
  Given task "t1" whose completion validation is the expression "a.score >= 0"
  And update "a" applied with value <value>
  When "t1" is completed
  Then the result is <result>

  Examples:
    | value          | result                                            |
    | {"score": 3}   | completed                                         |
    | {"score": -1}  | validation.ErrInvalidInput, task still progressed |
```

#### Scenario: Latest applied value wins
```gherkin
Scenario: Replace then complete
  Given task "t1" with update "a" applied {"v": 1} at seq 1 and {"v": 2} at seq 2
  When "t1" is completed
  Then the instance variable "a" is {"v": 2}
```

#### Scenario: Negative control - the no-updates path is byte-identical
```gherkin
Scenario: Existing definitions are unaffected
  Given a definition whose user task declares no updates, run by this build and by the build before it
  When the same task is completed with output {"approved": true} on both
  Then the committed snapshots are byte-identical
  And the journal rows are byte-identical
  And the instance variable "approved" is true
```

### Requirement: Update rows are numbered per task without gaps or reuse
Every row recorded for a task MUST take the next sequence number of that task, starting at 1, whatever its status, and a closing row MUST name the seq of the submission it closes.

#### Scenario: Numbering across statuses
```gherkin
Scenario: Seq is per task and strictly increasing
  Given tasks "t1" and "t2" on one instance
  When "t1" applies an action-less update, then submits an action-backed update that is rejected
  And "t2" applies an action-less update
  Then the seqs of "t1" are 1 (applied), 2 (submitted), 3 (rejected, submitted_seq 2)
  And the seq of "t2" is 1
```

### Requirement: The task service submits updates for library consumers
`TaskService.Progress` SHALL produce a `HumanProgressed` trigger for an existing task naming the actor, the update and the input, and MUST fail with `humantask.ErrTaskNotFound` for an unknown task and with `task.ErrTaskNotOpen` for a completed or cancelled one.

#### Scenario: Progress outcomes
```gherkin
Scenario Outline: Producing a progress trigger
  Given <setup>
  When "alice" calls TaskService.Progress for update "u" with input {"x": 1}
  Then the result is <result>

  Examples:
    | setup                 | result                                                   |
    | an open task "t1"     | a HumanProgressed trigger for "t1", "u", {"x": 1}, "alice" |
    | an unknown task id    | humantask.ErrTaskNotFound                                |
    | a completed task      | task.ErrTaskNotOpen                                      |
```

### Requirement: An update survives a restart between its commit and its action
A committed `submitted` row whose action was not performed before a crash MUST be completed by re-driving the same action invocation with a byte-identical envelope, so the update closes `applied` or `rejected` exactly once.

#### Scenario: Crash after commit, before perform
```gherkin
Scenario: The re-drive is the same invocation
  Given update "u" on task "t1" submitted at seq 4, committed, and the process stops before the action is performed
  When the process restarts and pending commands are recovered
  Then "u"'s action receives an envelope with idempotency key "t1:4", identical to the one that would have been sent
  And exactly one closing row with submitted_seq 4 is written
```
