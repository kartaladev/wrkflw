## ADDED Requirements

### Requirement: Recorded actors keep the caller whole and carry no privileges
A claim or completion SHALL record the acting caller with its ID, role, attributes and on-behalf principal, and MUST record no privileges at either level, identically in the instance snapshot, the journal and every task store; the claimant and completer identity columns SHALL hold the principal's ID.

#### Scenario: What a recorded actor holds
```gherkin
Scenario Outline: Stored claim and completion actors
  Given an open human task in the <store> task store
  When "a1" with role "assistant" and privileges ["pay"] on behalf of "m1" with role "approver" and privileges ["sign"] <operation> the task
  Then the recorded <record> names caller "a1" with role "assistant" and principal "m1" with role "approver"
  And the raw stored row and the snapshot contain neither "pay" nor "sign"
  And the task's <column> is "m1"

  Examples:
    | store              | operation | record     | column       |
    | memory             | claims    | claim      | claimant     |
    | sql on postgres    | claims    | claim      | claimed_by   |
    | sql on mysql       | claims    | claim      | claimed_by   |
    | sql on sqlite      | claims    | claim      | claimed_by   |
    | memory             | completes | completion | completer    |
    | sql on postgres    | completes | completion | completed_by |
    | sql on mysql       | completes | completion | completed_by |
    | sql on sqlite      | completes | completion | completed_by |
```

#### Scenario: An actor acting for itself is recorded without a principal
```gherkin
Scenario: No on-behalf principal
  Given an open human task
  When "alice" with role "approver" and privileges ["pay"] claims it
  Then the recorded claim names "alice" with role "approver" and no principal
  And the recorded claim carries no privileges
  And the task's claimant is "alice"
```

#### Scenario: Request-time privileges still authorize
```gherkin
Scenario: Stripping happens after the decision
  Given an open human task whose eligibility requires privileges ["pay"]
  When "alice" holding privileges ["pay"] claims it
  Then the claim is applied
  And the recorded claim carries no privileges
```

### Requirement: Completing an unclaimed claimant-only task claims it in the same step
Completing an unclaimed user task that declares `claimant_only` SHALL record a claim naming the completing caller at the completion's occurrence time, then the completion, in one step with exactly one journal row; the claimant column SHALL hold the principal's ID, and the claimed-task metric SHALL count it. Without the rule, completing an unclaimed task SHALL record no claim.

#### Scenario: Implicit claim
```gherkin
Scenario Outline: Completion of an unclaimed task
  Given an unclaimed open user task with claimant_only <rule> held in the <store> task store
  When eligible actor "bob" completes it
  Then the task state is completed
  And the task <claim>
  And the journal holds exactly one new row, a HumanCompleted naming "bob"

  Examples:
    | rule  | store           | claim                                                        |
    | true  | memory          | carries a claim naming "bob" at the completion's time         |
    | true  | sql on postgres | carries a claim naming "bob" and claimed_by is "bob"          |
    | true  | sql on mysql    | carries a claim naming "bob" and claimed_by is "bob"          |
    | true  | sql on sqlite   | carries a claim naming "bob" and claimed_by is "bob"          |
    | false | memory          | carries no claim                                              |
    | false | sql on postgres | carries no claim and claimed_by is empty                      |
```

#### Scenario: Replay reproduces the implicit claim
```gherkin
Scenario: Rebuilding from the journal after a restart
  Given an instance whose claimant-only task "bob" completed while unclaimed
  When the process restarts and the instance is rebuilt from its journal
  Then the rebuilt task carries the same claim and completion as before the restart
```

#### Scenario: The metric counts the implicit claim
```gherkin
Scenario: Claimed counter
  Given an unclaimed open user task with claimant_only true
  When eligible actor "bob" completes it
  Then the human-task counter for event "claimed" increased by exactly 1
  And the human-task counter for event "completed" increased by exactly 1
```

### Requirement: Racing completions of one unclaimed task admit exactly one
When two eligible actors complete the same unclaimed claimant-only task concurrently, exactly one completion MUST be applied and the other MUST fail without changing the task; a retry of the losing call SHALL fail with `engine.ErrTaskNotOpen`.

#### Scenario: Concurrent implicit claims
```gherkin
Scenario Outline: Two completers race
  Given an unclaimed open user task with claimant_only true on <backend>
  When "bob" and "carol" complete it concurrently
  Then exactly one completion is applied and the task's claim and completion name the same actor
  And the journal holds exactly one HumanCompleted for the task
  And retrying the losing call fails with engine.ErrTaskNotOpen

  Examples:
    | backend         |
    | memory          |
    | sql on postgres |
    | sql on mysql    |
    | sql on sqlite   |
```

### Requirement: Resolved candidates are principals
Candidates produced by the actor resolver SHALL be stored without privileges, and a resolver-supplied candidate carrying an on-behalf principal MUST fail the resolution with `authz.ErrInvalidActor`, leaving the task and instance unchanged (pending Q3).

#### Scenario: Candidate shapes
```gherkin
Scenario Outline: Storing resolved candidates
  Given an actor resolver returning <candidate>
  When the task's candidates are resolved
  Then the result is <result>

  Examples:
    | candidate                                   | result                                                   |
    | "u1" role "approver" privileges ["pay"]     | the stored candidate is "u1" role "approver", no privileges |
    | "u1" on behalf of "m1"                      | authz.ErrInvalidActor, candidates unchanged (pending Q3) |
    | no actors                                   | the stored candidate list is empty                       |
```

### Requirement: The claimable list matches the principal
Every task store's claimable query SHALL list an unclaimed task for an actor when the principal's ID is among the task's candidates or the principal's role is one of the task's eligible roles by exact string, and SHALL NOT list it on the strength of the caller's own ID or role.

#### Scenario: Claimable by principal
```gherkin
Scenario Outline: ClaimableBy uses the principal
  Given the <store> task store holding an unclaimed task with candidates ["m1"] and eligible roles ["approver"]
  When ClaimableBy is called for <actor>
  Then the task <listed>

  Examples:
    | store           | actor                                              | listed        |
    | memory          | "a1" on behalf of "m1"                             | is listed     |
    | memory          | "m1" acting for itself                             | is listed     |
    | memory          | "m1" role "clerk" on behalf of "x1" role "clerk"   | is not listed |
    | memory          | "a1" role "assistant" on behalf of "x1" role "approver" | is listed |
    | memory          | "u1" role "Approver"                               | is not listed |
    | sql on postgres | "a1" on behalf of "m1"                             | is listed     |
    | sql on mysql    | "a1" on behalf of "m1"                             | is listed     |
    | sql on sqlite   | "a1" on behalf of "m1"                             | is listed     |
    | sql on postgres | "m1" role "clerk" on behalf of "x1" role "clerk"   | is not listed |
    | sql on mysql    | "m1" role "clerk" on behalf of "x1" role "clerk"   | is not listed |
    | sql on sqlite   | "m1" role "clerk" on behalf of "x1" role "clerk"   | is not listed |
```

### Requirement: An actor recorded before the single-role model is refused on read
Reading a snapshot, journal row or task row whose recorded actor carries the retired roles list MUST fail with an error naming `roles` and MUST NOT decode it as an actor with no role (pending Q4).

#### Scenario: Pre-change actor rows
```gherkin
Scenario Outline: Legacy roles list on read
  Given a <record> whose stored claim actor is {"id":"alice","roles":<roles>}
  When it is read
  Then the result is <result>

  Examples:
    | record               | roles          | result                                   |
    | instance snapshot    | ["approver"]   | an error naming "roles" (pending Q4)     |
    | journal row          | ["approver"]   | an error naming "roles" (pending Q4)     |
    | task row on postgres | ["approver"]   | an error naming "roles" (pending Q4)     |
    | task row on mysql    | []             | an error naming "roles" (pending Q4)     |
    | task row on sqlite   | ["approver"]   | an error naming "roles" (pending Q4)     |
```

#### Scenario: Negative control - a current actor row reads back
```gherkin
Scenario: Single-role row decodes
  Given a task row whose stored claim actor is {"id":"alice","role":"approver"}
  When it is read
  Then the claim names "alice" with role "approver"
```
