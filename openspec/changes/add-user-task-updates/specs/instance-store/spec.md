## ADDED Requirements

### Requirement: Task update rows commit atomically with their step
Each `Create` and `Commit` SHALL persist the step's task update rows in the same transaction as its snapshot, journal entry and outbox rows, so that either all of them are stored or none is, on every shipped instance store.

#### Scenario: Rows and step are one unit
```gherkin
Scenario Outline: Commit stores the rows with the step
  Given a <store> instance store and an instance whose next step records an applied update row for task "t1"
  When the step is committed
  Then History for "t1" returns that row
  And the snapshot at the new version references the row's seq

  Examples:
    | store              |
    | Postgres           |
    | MySQL              |
    | SQLite             |
    | in-memory          |
    | caching over SQLite |
```

#### Scenario: A refused commit stores no row
```gherkin
Scenario Outline: A stale-version commit leaves no orphan row
  Given a <store> instance store
  When a step carrying a task update row is committed against a stale version
  Then the commit fails with kernel.ErrConcurrentUpdate
  And History for the task is unchanged

  Examples:
    | store     |
    | Postgres  |
    | MySQL     |
    | SQLite    |
    | in-memory |
```

#### Scenario: A failure inside the transaction rolls the step back
```gherkin
Scenario: Fault injection on the row insert
  Given a SQLite instance store whose task update insert is made to fail
  When a step carrying a task update row is committed
  Then the commit fails
  And the instance version, the journal and the outbox are unchanged
```

#### Scenario: Negative control - a step with no update rows commits as before
```gherkin
Scenario: No rows, no table writes
  Given an instance store and a step carrying no task update rows
  When it is committed
  Then the commit succeeds
  And the task update table gains no row
```

### Requirement: Task update rows are append-only and read back in order
The task update reader SHALL return a task's rows ordered by seq with every column as written, and its latest-applied query SHALL return, per update name, the value of the highest-seq applied row; no store operation SHALL update or delete a stored row.

#### Scenario: History and latest applied per store
```gherkin
Scenario Outline: Reads after a mixed sequence
  Given a <store> instance store
  And task "t1" has rows: 1 applied "a" {"v":1}; 2 submitted "b"; 3 rejected "b" reason "locked"; 4 applied "a" {"v":2}; 5 submitted "b"; 6 applied "b" {"w":1}
  When History and LatestApplied are read for "t1"
  Then History returns seqs 1 to 6 in order with status, name, input, value, reason, actor and time as written
  And LatestApplied returns {"a": {"v": 2}, "b": {"w": 1}}

  Examples:
    | store              |
    | Postgres           |
    | MySQL              |
    | SQLite             |
    | in-memory          |
    | caching over SQLite |
```

#### Scenario: A duplicate seq is refused, never overwritten
```gherkin
Scenario Outline: Primary key on task and seq
  Given a <store> instance store holding row seq 1 for task "t1"
  When a step attempts to store another row with seq 1 for "t1"
  Then the commit fails and the stored row is unchanged

  Examples:
    | store     |
    | Postgres  |
    | MySQL     |
    | SQLite    |
    | in-memory |
```

#### Scenario: Reads for an unknown task are empty
```gherkin
Scenario: No rows is not an error
  Given an instance store with no rows for task "nope"
  When History and LatestApplied are read for "nope"
  Then both return empty results and no error
```

#### Scenario: JSON values round-trip identically on every dialect
```gherkin
Scenario Outline: Value fidelity
  Given an applied row whose value is <value>
  When it is stored and read back on Postgres, MySQL and SQLite
  Then the three reads are equal to <value>

  Examples:
    | value                                       |
    | {}                                          |
    | {"n": 9007199254740993}                     |
    | {"s": "naïve ✓", "nested": {"a": [1, null]}} |
```

### Requirement: Journal rows written before update support still decode
A journal or snapshot row written by a build without update support MUST decode with no updates, no pending update and no applied values, and a `HumanProgressed` journal row MUST round-trip through the journal on every dialect.

#### Scenario: Old rows and the new trigger
```gherkin
Scenario Outline: Codec compatibility
  Given a <dialect> instance store
  When <row> is read back
  Then <result>

  Examples:
    | dialect  | row                                                         | result                                                   |
    | Postgres | a snapshot with a claimed task written by the previous build | the task has no updates, no pending update and seq 0    |
    | SQLite   | a HumanCompleted journal row written by the previous build  | it decodes with no applied values                        |
    | Postgres | a committed HumanProgressed step                            | Entries returns a HumanProgressed with task, update, input and actor |
    | MySQL    | a committed HumanProgressed step                            | Entries returns a HumanProgressed with task, update, input and actor |
    | SQLite   | a committed HumanProgressed step                            | Entries returns a HumanProgressed with task, update, input and actor |
```
