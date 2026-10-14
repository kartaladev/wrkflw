## ADDED Requirements

### Requirement: The entry action runs before the task exists and the task sees its output
When a token enters a userTask that declares `entry_action`, the engine SHALL dispatch that action and park the token before creating the task. On the action's success it MUST merge the action's output into the instance variables before it creates the task, so that the task's variables snapshot, eligibility predicate and candidate resolution all read the merged values.

#### Scenario: The output reaches the variables snapshot, the predicate and the candidates
```gherkin
Scenario Outline: What the created task sees after the entry action returns region "EU"
  Given a userTask "review" with entry_action "review.prepare" and an eligibility predicate 'vars.region == "EU"'
  And an instance whose variables hold no "region"
  When the token enters "review" and the entry action completes with output {"region": "EU"}
  Then <observation>

  Examples:
    | observation                                                                                |
    | the instance variables hold region "EU"                                                     |
    | the created task's variables snapshot holds region "EU"                                     |
    | an actor eligible only through the predicate may claim the task                             |
    | the actor resolver is asked for candidates after the merge and sees region "EU"             |
    | the task is offered once, with one AwaitHuman, after the ActionCompleted step               |
```

#### Scenario: A userTask without an entry action is created on entry, as today
```gherkin
Given a userTask "review" that declares no entry_action
When the token enters "review"
Then the task is created and offered in the same step
And no InvokeAction is dispatched for "review"
```

#### Scenario: The entry action receives the entry call envelope
```gherkin
Given a userTask "review" with entry_action "review.prepare"
When the token enters "review"
Then exactly one InvokeAction names "review.prepare"
And its call has kind "entry", a zero actor, an empty input and an empty applied set
And its call carries the instance variables, the instance id and node id "review"
And its call's task id is the id the task is later created with
And its idempotency key is that task id followed by ":entry"
```

#### Scenario: An unregistered entry action fails like any unknown action
```gherkin
Given a userTask "review" whose entry_action names an action no catalog registers
When the token enters "review" and the action is dispatched
Then a non-retryable ActionFailed is recorded for the entry command
And the node's on_failure policy for the entry action decides what follows
```

### Requirement: Each visit runs the entry action once under a fresh task id
Each entry of a token into a userTask with an entry action SHALL mint one task id, dispatch the entry action once under that id and the key `<task id>:entry`, and create the task with that same id; a later visit to the same node MUST mint a different id and key.

#### Scenario: A loop runs the entry action once per visit with distinct keys
```gherkin
Scenario Outline: Visit <visit> of a loop through "review"
  Given a definition that loops back into userTask "review", which has entry_action "review.prepare"
  When the token enters "review" for the <visit> time and the entry action completes
  Then exactly one entry InvokeAction was dispatched for this visit
  And the created task's id equals the id that visit's call named
  And the idempotency key differs from the key of every earlier visit

  Examples:
    | visit  |
    | first  |
    | second |
    | third  |
```

#### Scenario: A duplicate entry completion creates no second task
```gherkin
Given the entry action of userTask "review" completed and the task was created
When the same ActionCompleted for the entry command is delivered again
Then the step fails with ErrTokenNotFound
And the instance holds exactly one task for this visit
```

#### Scenario: A crash after dispatch re-drives the entry action under the same key
```gherkin
Given the step that dispatched the entry action of userTask "review" committed
And the process stopped before the action's reply was applied
When the instance is recovered and its pending command is re-driven
Then the re-driven call names the same task id and the same idempotency key
And no task exists until the reply is applied
```

### Requirement: No task exists while the entry action is in flight
Between the dispatch of an entry action and the step that applies its outcome, the instance SHALL hold no task for that visit, no task store SHALL hold a row for the pending id, a human trigger naming the pending id MUST fail with `humantask.ErrTaskNotFound`, and a task route naming it MUST answer 404.

#### Scenario: Human triggers on the pending id are refused
```gherkin
Scenario Outline: <trigger> names the pending task id while the entry action runs
  Given the token of userTask "review" is parked on its entry action under pending task id P
  When a <trigger> trigger naming task P is applied
  Then the step fails with humantask.ErrTaskNotFound
  And the instance state is unchanged

  Examples:
    | trigger         |
    | HumanClaimed    |
    | HumanReassigned |
    | HumanCompleted  |
```

#### Scenario: No task row exists on any dialect
```gherkin
Scenario Outline: The task store on <dialect> while the entry action runs
  Given a ProcessDriver backed by the <dialect> store
  And an instance whose userTask "review" is parked on its entry action under pending task id P
  When the task store is read for task P
  Then it reports humantask.ErrTaskNotFound
  And after the entry action completes the store holds task P

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
    | memory   |
```

#### Scenario: Task routes answer 404 on every adapter
```gherkin
Scenario Outline: GET /tasks/P on <adapter> while the entry action runs
  Given the <adapter> adapter over an instance whose userTask "review" is parked on its entry action under pending task id P
  When an authorized caller requests GET /tasks/P
  Then the response status is 404
  And after the entry action completes the same request answers 200

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: A token leaving the node during the entry action creates no task
When a token parked on an entry action leaves its node without the action's success being applied, by instance cancellation, termination, or an interrupting event that removes the token, the engine SHALL create no task for that visit, and a later reply for the entry command MUST NOT create one.

#### Scenario: Paths that remove the token before the task exists
```gherkin
Scenario Outline: The instance is <path> while the entry action of "review" runs
  Given the token of userTask "review" is parked on its entry action under pending task id P
  When the instance is <path>
  Then the instance holds no task with id P
  And the task store holds no row for P
  And a later ActionCompleted for the entry command does not create task P

  Examples:
    | path                                                  |
    | cancelled                                             |
    | terminated by a terminate end in a parallel branch    |
    | interrupted by an interrupting event sub-process      |
```

#### Scenario: The same entry completion on a token still parked creates the task
```gherkin
Given the token of userTask "review" is parked on its entry action under pending task id P
When the ActionCompleted for the entry command is applied
Then the instance holds task P in state unclaimed
```

### Requirement: The entry action's terminal failure follows its on_failure policy
After the node's retry policy is exhausted or the failure is non-retryable, an entry action whose policy is `stop` SHALL run the existing chain (RecoveryFlow, else error boundary, else incident) and create no task; under `ignore` it SHALL record the failure, log a WARN, raise no incident, take neither RecoveryFlow nor boundary, and create and offer the task with variables unchanged.

#### Scenario: Terminal failure under each policy
```gherkin
Scenario Outline: The entry action fails terminally under on_failure <policy> on a node with <handler>
  Given a userTask "review" with entry_action "review.prepare", on_failure <policy> and <handler>
  When the entry action fails non-retryably
  Then <outcome>
  And the instance holds <tasks>

  Examples:
    | policy        | handler                       | outcome                                                                      | tasks                                        |
    | stop          | no RecoveryFlow or boundary   | an IncidentAction is raised on the token                                     | no task for this visit                       |
    | stop          | a RecoveryFlow                | the token follows the RecoveryFlow                                           | no task for this visit                       |
    | stop          | an error boundary             | the token leaves through the error boundary                                  | no task for this visit                       |
    | (absent)      | no RecoveryFlow or boundary   | an IncidentAction is raised on the token                                     | no task for this visit                       |
    | ignore        | no RecoveryFlow or boundary   | no incident is raised and a WARN naming the node and command is logged       | an unclaimed task under the pending id       |
    | ignore        | a RecoveryFlow                | the RecoveryFlow is not taken and no incident is raised                      | an unclaimed task under the pending id       |
    | ignore        | an error boundary             | the error boundary is not taken and no incident is raised                    | an unclaimed task under the pending id       |
```

#### Scenario: Ignore offers the task on the unchanged variables and journals the failure once
```gherkin
Given a userTask "review" with entry_action "review.prepare" and on_failure ignore
And an instance whose variables are {"order": 7}
When the entry action fails non-retryably
Then the created task's variables snapshot is {"order": 7}
And the journal holds exactly one ActionFailed row for the entry command
And the task is offered with one AwaitHuman
```

#### Scenario: Retries come first under both policies
```gherkin
Scenario Outline: A retryable failure under on_failure <policy> with retry budget left
  Given a userTask "review" with entry_action "review.prepare", on_failure <policy> and a retry policy of 3 attempts
  When the entry action fails retryably on its first attempt
  Then a retry timer is scheduled
  And no task exists yet
  And no incident is raised

  Examples:
    | policy |
    | stop   |
    | ignore |
```

### Requirement: The completion action's terminal failure follows its on_failure policy
After the node's retry policy is exhausted or the failure is non-retryable, a userTask completion action whose policy is `stop` SHALL run the existing chain unchanged; under `ignore` it SHALL record the failure, log a WARN, raise no incident, take neither RecoveryFlow nor boundary, and advance the token with the human's completion output kept and no action output merged.

#### Scenario: Terminal completion-action failure under each policy
```gherkin
Scenario Outline: The completion action fails terminally under on_failure <policy>
  Given a userTask "review" with completion_action "review.finalize", on_failure <policy> and no RecoveryFlow or boundary
  And the task was completed with output {"approved": true}
  When the completion action fails non-retryably
  Then <outcome>
  And the instance variables hold approved true

  Examples:
    | policy   | outcome                                                                                  |
    | stop     | an IncidentAction is raised and the token stays on "review"                              |
    | (absent) | an IncidentAction is raised and the token stays on "review"                              |
    | ignore   | no incident is raised, a WARN is logged and the token advances along its outgoing flow   |
```

#### Scenario: A succeeding completion action is unaffected by the policy
```gherkin
Scenario Outline: The completion action succeeds under on_failure <policy>
  Given a userTask "review" with completion_action "review.finalize" and on_failure <policy>
  When the task is completed and the completion action returns {"filed": true}
  Then the instance variables hold filed true
  And the token advances along its outgoing flow

  Examples:
    | policy |
    | stop   |
    | ignore |
```

### Requirement: A userTask's deadline starts at task creation
A userTask's deadline timer and reminder SHALL be scheduled in the step that creates the task, after any entry action has returned, and the task's due date MUST be computed from that step's time rather than from the token's entry into the node.

#### Scenario: A slow entry action delays the deadline instead of consuming it
```gherkin
Scenario Outline: Entry action returns <elapsed> after the token entered a node with a PT1H deadline
  Given a userTask "review" with entry_action "review.prepare" and a deadline of PT1H
  And the token entered "review" at 10:00
  When the entry action completes at <returned>
  Then the task's due date is <due>
  And no deadline timer existed before <returned>

  Examples:
    | elapsed | returned | due   |
    | PT0S    | 10:00    | 11:00 |
    | PT10M   | 10:10    | 11:10 |
    | PT2H    | 12:00    | 13:00 |
```

#### Scenario: Without an entry action the deadline starts on entry, as today
```gherkin
Given a userTask "review" with no entry_action and a deadline of PT1H
When the token enters "review" at 10:00
Then the task's due date is 11:00
```

### Requirement: An entry action's success records no compensation entry
PENDING Q1 (recommended option). A successful entry action SHALL NOT record a compensation entry for its node, even when the node declares `compensate_action`; the node's compensation entry MUST be recorded only by its completion action's success, as today.

#### Scenario: Only the completion action records compensation
```gherkin
Scenario Outline: Compensation entries after <progress>
  Given a userTask "review" with entry_action "review.prepare", completion_action "review.finalize" and compensate_action "review.undo"
  When <progress>
  Then the scope holds <entries> compensation entries for "review"

  Examples:
    | progress                                                         | entries |
    | the entry action completed and the task is open                  | 0       |
    | the entry action completed and the completion action completed   | 1       |
```

### Requirement: A userTask's boundary events are armed at task creation
PENDING Q3 (recommended option). A userTask's timer, message and signal boundary events SHALL be armed in the step that creates the task, and MUST NOT be armed while the entry action is in flight.

#### Scenario: Boundary arms before and after the entry action
```gherkin
Scenario Outline: An interrupting timer boundary of PT5M on "review" when <moment>
  Given a userTask "review" with entry_action "review.prepare" and an interrupting timer boundary of PT5M
  When <moment>
  Then <arms>

  Examples:
    | moment                                         | arms                                       |
    | the token is parked on the entry action        | no boundary timer is scheduled             |
    | the entry action has completed                 | one boundary timer is scheduled for "review" |
    | the entry action failed under on_failure ignore | one boundary timer is scheduled for "review" |
```
