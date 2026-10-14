# Spec Delta

## ADDED Requirements

### Requirement: SQL task stores persist the task state by name
A SQL task store SHALL write a task's state as its stable name (`unclaimed`, `claimed`, `completed`, `cancelled`), never as an ordinal, identically on Postgres, MySQL and SQLite.

#### Scenario: Raw state column after each transition
```gherkin
Scenario Outline: a task in <state> on <dialect>
  Given a <dialect> task store opened after migration
  When a task in state <state> is upserted
  Then the raw state column of that task holds the text "<name>"
  And Get returns the task in state <state>

  Examples:
    | dialect  | state     | name      |
    | Postgres | Unclaimed | unclaimed |
    | MySQL    | Claimed   | claimed   |
    | SQLite   | Completed | completed |
    | Postgres | Cancelled | cancelled |
    | MySQL    | Unclaimed | unclaimed |
    | SQLite   | Claimed   | claimed   |
```

### Requirement: SQL task stores refuse a stored task whose state is outside the vocabulary
A SQL task store read MUST fail with the task-state unknown-value sentinel when a stored task's state is not one of the task-state names, and MUST NOT read it back as unclaimed or any other state.

#### Scenario: Out-of-band task rows
```gherkin
Scenario Outline: Get over a row whose <field> holds <stored> on <dialect>
  Given a <dialect> store holding a row written by the library
  And the row's <field> is overwritten out of band with <stored>
  When <read> is called for that row
  Then the result is <result>

  Examples:
    | dialect  | read                     | field                          | stored          | result                                                                  |
    | Postgres | the task store's Get     | task state column              | "bogus"         | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | MySQL    | the task store's Get     | task state column              | "bogus"         | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | SQLite   | the task store's Get     | task state column              | "bogus"         | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | SQLite   | the task store's Get     | task state column              | ""              | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | SQLite   | the task store's Get     | task state column              | "cancelled"     | the task, in state Cancelled                                            |
    | MySQL    | the task store's Get     | task state column              | "Claimed"       | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | Postgres | the task store's Get     | task state column              | "1"             | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | Postgres | the task store's Get     | task state column              | "claimed"       | the task, in state Claimed                                              |
    | MySQL    | the task store's Get     | task state column              | "completed"     | the task, in state Completed                                            |
```
