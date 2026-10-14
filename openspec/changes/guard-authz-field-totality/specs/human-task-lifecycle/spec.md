## ADDED Requirements

### Requirement: Task stores return stored actors whole and without granted privileges
Every shipped task store MUST return each stored claimant, completer and candidate equal to the actor written, except that its granted privileges SHALL be absent. Every other actor field MUST read back unchanged, identically on the memory store and the SQL store on Postgres, MySQL and SQLite. Writing MUST NOT alter the caller's actor.

#### Scenario: A privileged actor is stored without its privileges
```gherkin
Scenario Outline: Actor round trip through a task store
  Given the <store> task store
  And actor "alice" with roles ["manager"], privileges ["finance-task complete"] and attributes {"dept": "fin", "level": 3, "名前": "アリス"}
  When a task whose <slot> is that actor is upserted and read back by Get
  Then the read <slot> has ID "alice", roles ["manager"] and attributes {"dept": "fin", "level": 3, "名前": "アリス"}
  And the read <slot> has no privileges

  Examples:
    | store           | slot                  |
    | memory          | claim actor           |
    | memory          | completion actor      |
    | memory          | only candidate        |
    | sql on postgres | claim actor           |
    | sql on postgres | completion actor      |
    | sql on postgres | only candidate        |
    | sql on mysql    | claim actor           |
    | sql on mysql    | completion actor      |
    | sql on mysql    | only candidate        |
    | sql on sqlite   | claim actor           |
    | sql on sqlite   | completion actor      |
    | sql on sqlite   | only candidate        |
```

#### Scenario: Negative control, actors without privileges round-trip whole
```gherkin
Scenario Outline: Nothing but privileges is dropped
  Given the <store> task store
  And a claimed task whose claim actor is <actor>
  When the task is upserted and read back by Get
  Then the read claim actor is <read>

  Examples:
    | store           | actor                                                   | read                                                    |
    | memory          | ID "bob" only                                           | ID "bob" only                                           |
    | sql on postgres | ID "bob" only                                           | ID "bob" only                                           |
    | sql on mysql    | ID "bob" only                                           | ID "bob" only                                           |
    | sql on sqlite   | ID "bob" only                                           | ID "bob" only                                           |
    | memory          | ID "bob", attributes {"privileges": ["x"]}              | ID "bob", attributes {"privileges": ["x"]}              |
    | sql on postgres | ID "bob", attributes {"privileges": ["x"]}              | ID "bob", attributes {"privileges": ["x"]}              |
    | sql on mysql    | ID "bob", attributes {"privileges": ["x"]}              | ID "bob", attributes {"privileges": ["x"]}              |
    | sql on sqlite   | ID "bob", attributes {"privileges": ["x"]}              | ID "bob", attributes {"privileges": ["x"]}              |
    | memory          | ID "bob", empty roles list and empty attributes map     | ID "bob", no roles and no attributes                    |
    | sql on postgres | ID "bob", empty roles list and empty attributes map     | ID "bob", no roles and no attributes                    |
    | sql on mysql    | ID "bob", empty roles list and empty attributes map     | ID "bob", no roles and no attributes                    |
    | sql on sqlite   | ID "bob", empty roles list and empty attributes map     | ID "bob", no roles and no attributes                    |
```

#### Scenario: Every listing applies the same rule as Get
```gherkin
Scenario Outline: Listings return stripped actors
  Given the <store> task store
  And a task claimed by actor "alice" with privileges ["finance-task complete"]
  And an unclaimed task whose only candidate is actor "carol" with privileges ["finance-task claim"]
  When the tasks are listed by <query>
  Then the listed <slot> has no privileges

  Examples:
    | store           | query                          | slot             |
    | memory          | AssignedTo("alice")            | claim actor      |
    | memory          | ClaimableBy(actor "carol")     | only candidate   |
    | sql on postgres | AssignedTo("alice")            | claim actor      |
    | sql on postgres | ClaimableBy(actor "carol")     | only candidate   |
    | sql on mysql    | AssignedTo("alice")            | claim actor      |
    | sql on mysql    | ClaimableBy(actor "carol")     | only candidate   |
    | sql on sqlite   | AssignedTo("alice")            | claim actor      |
    | sql on sqlite   | ClaimableBy(actor "carol")     | only candidate   |
```

#### Scenario: The caller's actor is not mutated by the write
```gherkin
Scenario Outline: Stripping works on a copy
  Given the <store> task store
  And a task value whose claim actor carries privileges ["finance-task complete"]
  When the task value is upserted
  Then the caller's task value still carries privileges ["finance-task complete"] on its claim actor

  Examples:
    | store           |
    | memory          |
    | sql on postgres |
    | sql on mysql    |
    | sql on sqlite   |
```

#### Scenario: The memory store and the SQL stores agree
```gherkin
Scenario Outline: Parity of the actor round trip
  Given the memory task store and the <dialect> SQL task store
  And the same task, whose claim, completion and candidates carry roles, privileges and attributes, upserted into both
  When the task is read back by Get from both
  Then the two read tasks have equal claim, completion and candidate actors

  Examples:
    | dialect  |
    | postgres |
    | mysql    |
    | sqlite   |
```

### Requirement: Task rows written before the privilege strip read back without privileges
A SQL task store MUST return no granted privileges for a stored claimant, completer or candidate even when the stored row, written by an earlier version or by hand, carries them, and SHALL decode a row written by an earlier version without privileges to the same actor it decoded before.

#### Scenario: A legacy row carrying privileges
```gherkin
Scenario Outline: Reading an older row
  Given the SQL task store on <dialect>
  And a task row whose <column> was written holding an actor with privileges ["finance-task complete"] and roles ["manager"]
  When the task is read back by Get
  Then the actor read from <column> has roles ["manager"] and no privileges

  Examples:
    | dialect  | column            |
    | postgres | candidates        |
    | postgres | claim actor       |
    | postgres | completion actor  |
    | mysql    | candidates        |
    | mysql    | claim actor       |
    | mysql    | completion actor  |
    | sqlite   | candidates        |
    | sqlite   | claim actor       |
    | sqlite   | completion actor  |
```

#### Scenario: Negative control, a legacy row without privileges is unchanged
```gherkin
Scenario Outline: Rows written by the previous version still decode
  Given the SQL task store on <dialect>
  And a task row whose claim actor column holds <stored>, as the previous version wrote it
  When the task is read back by Get
  Then the claim actor read is <read>

  Examples:
    | dialect  | stored                                              | read                                                 |
    | postgres | {"roles":["manager"],"attributes":{"dept":"fin"}}   | ID from the claimant column, roles ["manager"], attributes {"dept": "fin"} |
    | mysql    | {"roles":["manager"],"attributes":{"dept":"fin"}}   | ID from the claimant column, roles ["manager"], attributes {"dept": "fin"} |
    | sqlite   | {"roles":["manager"],"attributes":{"dept":"fin"}}   | ID from the claimant column, roles ["manager"], attributes {"dept": "fin"} |
    | postgres | {}                                                  | ID from the claimant column only                     |
    | mysql    | {}                                                  | ID from the claimant column only                     |
    | sqlite   | {}                                                  | ID from the claimant column only                     |
```
