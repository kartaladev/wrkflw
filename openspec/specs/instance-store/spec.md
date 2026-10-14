# instance-store Specification

## Purpose
Defines what a durable instance store keeps for each process instance (a versioned state snapshot and its trigger journal), how concurrent writers are kept from overwriting each other, and how the schema is brought up to date.

## Requirements

### Requirement: Instance state round-trips through a versioned store
A SQL-backed instance store SHALL return, on `Load`, the state written by the latest successful `Create` or `Commit` together with its current version token, and each successful `Commit` against the current token SHALL return a new token.

#### Scenario: Create, load and commit on each SQL dialect
```gherkin
Scenario Outline: version tokens advance
  Given a <dialect> instance store opened after migration
  When instance "i1" is created
  Then Create returns version 1
  And Load of "i1" returns InstanceID "i1" and version 1
  When a step is committed against version 1
  Then Commit returns version 2

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Duplicate creation is refused with ErrInstanceExists
Creating an instance whose ID already exists in the store SHALL fail with `kernel.ErrInstanceExists` (re-exported as `persistence.ErrInstanceExists`).

#### Scenario: Duplicate and fresh IDs
```gherkin
Scenario Outline: Create on each SQL dialect
  Given a <dialect> instance store holding instance "i1"
  When an instance is created with ID "<id>"
  Then the result is <result>

  Examples:
    | dialect  | id | result                           |
    | Postgres | i1 | an error matching ErrInstanceExists |
    | MySQL    | i1 | an error matching ErrInstanceExists |
    | SQLite   | i1 | an error matching ErrInstanceExists |
    | Postgres | i9 | success with version 1           |
    | MySQL    | i9 | success with version 1           |
    | SQLite   | i9 | success with version 1           |
```

### Requirement: Loading an unknown instance fails with ErrInstanceNotFound
Loading an instance ID that the store does not hold SHALL fail with `kernel.ErrInstanceNotFound` (re-exported as `persistence.ErrInstanceNotFound`).

#### Scenario: Unknown versus known ID
```gherkin
Scenario Outline: Load on each SQL dialect
  Given a <dialect> instance store holding only instance "i1"
  When "<id>" is loaded
  Then the result is <result>

  Examples:
    | dialect  | id   | result                                |
    | Postgres | nope | an error matching ErrInstanceNotFound |
    | MySQL    | nope | an error matching ErrInstanceNotFound |
    | SQLite   | nope | an error matching ErrInstanceNotFound |
    | Postgres | i1   | the stored state                      |
    | MySQL    | i1   | the stored state                      |
    | SQLite   | i1   | the stored state                      |
```

### Requirement: A commit against a stale version is refused with ErrConcurrentUpdate
`Commit` SHALL fail with `kernel.ErrConcurrentUpdate` when the expected version token is not the instance's current one, so a writer that lost a race cannot overwrite the winner's state.

#### Scenario: Stale token after a successful commit
```gherkin
Scenario Outline: Commit on each SQL dialect
  Given a <dialect> instance store holding instance "i1" at version 1
  When a step is committed against version 1
  Then Commit returns version 2
  When another step is committed against version 1
  Then the error matches kernel.ErrConcurrentUpdate

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Every applied step is recorded in the instance's journal
Each successful `Create` and `Commit` SHALL record the step's trigger in the instance's journal, and `Entries` SHALL return the recorded triggers for that instance.

#### Scenario: Journal after create and one commit
```gherkin
Scenario Outline: Entries on each SQL dialect
  Given a <dialect> instance store
  When instance "i1" is created and one step is committed, both with a StartInstance trigger
  Then Entries for "i1" returns 2 triggers
  And each returned trigger is a StartInstance trigger

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: A history cap bounds the inline history without dropping open visits
On a SQL instance store configured with a history cap of n greater than zero, the stored instance snapshot SHALL keep every open node visit plus at most the n most recent closed visits, in their original order.

#### Scenario: Capping closed visits
```gherkin
Scenario Outline: history cap of <n>
  Given a SQL instance store configured with a history cap of <n>
  And an instance whose history is <history> in order
  When a step for that instance is committed and the instance is loaded
  Then the loaded history is <kept> in order

  Examples:
    | n | history                                 | kept                |
    | 1 | open human, closed c1, closed c2, closed c3 | human, c3           |
    | 2 | closed c1, closed c2, open o1, closed c3    | c2, o1, c3          |
```

### Requirement: Schema migration is idempotent
The schema migration the `persistence` package offers for each SQL dialect SHALL succeed when run again on a database it has already migrated.

#### Scenario: Second migration is a no-op
```gherkin
Scenario Outline: re-running the migration
  Given a <dialect> database already migrated by the persistence package's <dialect> migration
  When the <dialect> migration is run again
  Then it returns nil

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```
