# Spec Delta

## ADDED Requirements

### Requirement: Instance enums encode as their stable names
The enums an instance store persists in the snapshot and the journal, `engine.Status`, `engine.TokenState`, `engine.IncidentKind`, `engine.TimerKind`, `engine.CompensationDisposition` and `humantask.TaskState` SHALL encode to JSON as the JSON string of their stable name, and SHALL decode that string back to the same value.

#### Scenario: The full name table
```gherkin
Scenario Outline: <enum> value <constant> round-trips by name
  When <constant> is encoded with json.Marshal
  Then the result is the JSON string "<name>"
  And decoding "<name>" into a <enum> yields <constant>

  Examples:
    | enum                    | constant                   | name                       |
    | Status                  | StatusRunning              | running                    |
    | Status                  | StatusCompleted            | completed                  |
    | Status                  | StatusFailed               | failed                     |
    | Status                  | StatusCompensating         | compensating               |
    | Status                  | StatusTerminated           | terminated                 |
    | TokenState              | TokenActive                | active                     |
    | TokenState              | TokenWaiting               | waiting                    |
    | TokenState              | TokenJoining               | joining                    |
    | TokenState              | TokenIncident              | incident                   |
    | IncidentKind            | IncidentAction             | IncidentAction             |
    | IncidentKind            | IncidentCompensationStall  | IncidentCompensationStall  |
    | IncidentKind            | IncidentCompensationFailed | IncidentCompensationFailed |
    | IncidentKind            | IncidentDefinitionDefect   | IncidentDefinitionDefect   |
    | TimerKind               | TimerIntermediate          | TimerIntermediate          |
    | TimerKind               | TimerDeadline              | TimerDeadline              |
    | TimerKind               | TimerInWait                | TimerInWait                |
    | TimerKind               | TimerRetry                 | TimerRetry                 |
    | TimerKind               | TimerCompensationStall     | TimerCompensationStall     |
    | TimerKind               | TimerCompensationRetry     | TimerCompensationRetry     |
    | CompensationDisposition | CompensationRetry          | retry                      |
    | CompensationDisposition | CompensationSkip           | skip                       |
    | CompensationDisposition | CompensationAbandon        | abandon                    |
    | TaskState               | Unclaimed                  | unclaimed                  |
    | TaskState               | Claimed                    | claimed                    |
    | TaskState               | Completed                  | completed                  |
    | TaskState               | Cancelled                  | cancelled                  |
```

#### Scenario: The name does not depend on the constant's position
```gherkin
Scenario: The zero value encodes by name, not as 0
  When the zero value of each of the six enums is encoded
  Then each result is a JSON string from the name table
  And no result is a JSON number
```

### Requirement: Encoding an enum value that has no name fails
Encoding a value of an enum the instance store persists that has no stable name MUST fail with that enum's unknown-value sentinel, and MUST NOT produce a name, a number or a placeholder string.

#### Scenario: One past the last constant, and negative values
```gherkin
Scenario Outline: encoding <enum>(<value>)
  When <enum>(<value>) is encoded with json.Marshal
  Then the result is <result>

  Examples:
    | enum                    | value | result                                                          |
    | Status                  | 5     | an error matching engine.ErrUnknownStatus (pending Q1)          |
    | Status                  | -1    | an error matching engine.ErrUnknownStatus (pending Q1)          |
    | Status                  | 4     | the JSON string "terminated"                                    |
    | TokenState              | 4     | an error matching engine.ErrUnknownTokenState (pending Q1)      |
    | TokenState              | 3     | the JSON string "incident"                                      |
    | IncidentKind            | 99    | an error matching engine.ErrUnknownIncidentKind (pending Q1)    |
    | IncidentKind            | 3     | the JSON string "IncidentDefinitionDefect"                      |
    | TimerKind               | 6     | an error matching engine.ErrUnknownTimerKind (pending Q1)       |
    | TimerKind               | 5     | the JSON string "TimerCompensationRetry"                        |
    | CompensationDisposition | 3     | an error matching engine.ErrUnknownCompensationDisposition (pending Q1) |
    | CompensationDisposition | 2     | the JSON string "abandon"                                       |
    | TaskState               | 4     | an error matching humantask.ErrUnknownTaskState (pending Q1)    |
    | TaskState               | 3     | the JSON string "cancelled"                                     |
```

#### Scenario: A state carrying a nameless value is not persisted
```gherkin
Scenario Outline: commit on <dialect>
  Given a <dialect> instance store holding instance "i1" at version 1
  When a step whose state has a token in TokenState(9) is committed against version 1
  Then the error matches engine.ErrUnknownTokenState (pending Q1)
  And Load of "i1" returns version 1 and the previously stored state
  When a step whose tokens are all TokenWaiting is committed against version 1
  Then Commit returns version 2

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: Decoding an instance enum accepts only an exact name
Decoding any of the enums an instance store persists MUST accept only a JSON string that is byte-for-byte one of its stable names, and MUST refuse with that enum's unknown-value sentinel an unknown name, a case, whitespace or Unicode variant, the empty string, a JSON number and `null`.

#### Scenario: Strict decode, no integer arm
```gherkin
Scenario Outline: decoding <input> into a <enum>
  When the JSON <input> is decoded into a <enum>
  Then the result is <result>

  Examples:
    | enum                    | input              | result                                                                  |
    | Status                  | "running"          | StatusRunning                                                           |
    | Status                  | "Running"          | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | "running "         | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | " running"         | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | "runnіng" (Cyrillic і) | an error matching engine.ErrUnknownStatus (pending Q1)              |
    | Status                  | "paused"           | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | ""                 | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | "unknown"          | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | 0                  | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | 2                  | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | Status                  | null               | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | TokenState              | "joining"          | TokenJoining                                                            |
    | TokenState              | 2                  | an error matching engine.ErrUnknownTokenState (pending Q1)              |
    | IncidentKind            | "IncidentAction"   | IncidentAction                                                          |
    | IncidentKind            | "incidentAction"   | an error matching engine.ErrUnknownIncidentKind (pending Q1)            |
    | IncidentKind            | "IncidentKind(unknown)" | an error matching engine.ErrUnknownIncidentKind (pending Q1)       |
    | TimerKind               | "TimerRetry"       | TimerRetry                                                              |
    | TimerKind               | "retry"            | an error matching engine.ErrUnknownTimerKind (pending Q1)               |
    | CompensationDisposition | "skip"             | CompensationSkip                                                        |
    | CompensationDisposition | "SKIP"             | an error matching engine.ErrUnknownCompensationDisposition (pending Q1) |
    | CompensationDisposition | 1                  | an error matching engine.ErrUnknownCompensationDisposition (pending Q1) |
    | TaskState               | "claimed"          | Claimed                                                                 |
    | TaskState               | "unknown"          | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
```

### Requirement: No enum the instance store persists is written as an ordinal
A SQL instance store SHALL write the instance status column as the status name, and every enum in the stored snapshot as a JSON string name, identically on Postgres, MySQL and SQLite.

#### Scenario: Raw column and snapshot contents after create and commit
```gherkin
Scenario Outline: instance rows on <dialect>
  Given a <dialect> instance store opened after migration
  When instance "i1" is created in StatusRunning with one TokenWaiting token
  Then the raw status column of "i1" holds the text "running"
  And the raw snapshot of "i1" holds "Status":"running" and "State":"waiting"
  When a step that completes "i1" is committed
  Then the raw status column of "i1" holds the text "completed"
  And the raw snapshot of "i1" holds no enum field whose value is a JSON number

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

### Requirement: The journal records a compensation disposition by name
The journal SHALL store the disposition of a recorded `ResolveCompensationStall` trigger as its stable name, and `Entries` SHALL return the trigger with that disposition on every SQL dialect.

#### Scenario: Disposition round-trip
```gherkin
Scenario Outline: <disposition> on <dialect>
  Given a <dialect> instance store holding instance "i1" whose compensation walk has stalled
  When a step applying a ResolveCompensationStall trigger with disposition <disposition> is committed
  Then the raw journal payload of that step holds "disposition":"<name>"
  And Entries for "i1" returns that trigger with disposition <disposition>

  Examples:
    | dialect  | disposition         | name    |
    | Postgres | CompensationRetry   | retry   |
    | MySQL    | CompensationSkip    | skip    |
    | SQLite   | CompensationAbandon | abandon |
    | Postgres | CompensationAbandon | abandon |
    | MySQL    | CompensationRetry   | retry   |
    | SQLite   | CompensationSkip    | skip    |
```

### Requirement: A stored instance enum outside the vocabulary is refused on read
Every SQL instance-store read that decodes a persisted enum (Load, Entries, and listing instances) MUST fail with that enum's unknown-value sentinel when the stored value is not one of its stable names, including an integer written by an earlier build, and MUST NOT substitute a zero value or a default.

#### Scenario: Out-of-band instance rows on each read path
```gherkin
Scenario Outline: <read> over a row whose <field> holds <stored> on <dialect>
  Given a <dialect> store holding a row written by the library
  And the row's <field> is overwritten out of band with <stored>
  When <read> is called for that row
  Then the result is <result>

  Examples:
    | dialect  | read                     | field                          | stored          | result                                                                  |
    | Postgres | Load                     | snapshot Status                | "paused"        | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | MySQL    | Load                     | snapshot Status                | 2               | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | SQLite   | Load                     | snapshot token State           | 1               | an error matching engine.ErrUnknownTokenState (pending Q1)              |
    | Postgres | Load                     | snapshot incident Kind         | "IncidentBogus" | an error matching engine.ErrUnknownIncidentKind (pending Q1)            |
    | MySQL    | Load                     | snapshot task State            | "Claimed"       | an error matching humantask.ErrUnknownTaskState (pending Q1)            |
    | SQLite   | Load                     | snapshot timer Kind            | "TimerBogus"    | an error matching engine.ErrUnknownTimerKind (pending Q1)               |
    | Postgres | Load                     | snapshot Status                | "running"       | the stored state, StatusRunning                                         |
    | Postgres | InstanceLister.List      | instance status column         | "paused"        | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | SQLite   | InstanceLister.List      | instance status column         | "failed"        | a page holding the instance with StatusFailed                           |
    | MySQL    | InstanceLister.List      | instance status column         | "2"             | an error matching engine.ErrUnknownStatus (pending Q1)                  |
    | MySQL    | Entries                  | journal disposition            | "Retry"         | an error matching engine.ErrUnknownCompensationDisposition (pending Q1) |
    | SQLite   | Entries                  | journal disposition            | 0               | an error matching engine.ErrUnknownCompensationDisposition (pending Q1) |
    | Postgres | Entries                  | journal disposition            | "abandon"       | the trigger, with disposition CompensationAbandon                       |
```

### Requirement: Instance listing filters by status name
`InstanceLister.List` SHALL select instances by their status name on every SQL dialect and on the in-memory store, and MUST fail with the status sentinel, returning no page, when the filter's status has no name.

#### Scenario: Each status and an out-of-range filter
```gherkin
Scenario Outline: filter <filter> on <store>
  Given the <store> holding one instance in each of the five statuses
  When List is called with status filter <filter> and IncludeTotal
  Then the result is <result>

  Examples:
    | store            | filter             | result                                                        |
    | Postgres lister  | StatusRunning      | exactly the running instance, TotalCount 1                    |
    | MySQL lister     | StatusCompensating | exactly the compensating instance, TotalCount 1               |
    | SQLite lister    | StatusTerminated   | exactly the terminated instance, TotalCount 1                 |
    | in-memory store  | StatusFailed       | exactly the failed instance, TotalCount 1                     |
    | Postgres lister  | no filter          | all five instances, TotalCount 5                              |
    | Postgres lister  | Status(9)          | an error matching engine.ErrUnknownStatus (pending Q1)        |
    | MySQL lister     | Status(5)          | an error matching engine.ErrUnknownStatus (pending Q1)        |
    | SQLite lister    | Status(-1)         | an error matching engine.ErrUnknownStatus (pending Q1)        |
    | in-memory store  | Status(9)          | an error matching engine.ErrUnknownStatus (pending Q1)        |
```

#### Scenario: A filter value is bound, never interpolated
```gherkin
Scenario Outline: filter on <dialect> beside an instance whose status column was overwritten
  Given a <dialect> lister whose store holds a running instance
  And a second row whose status column holds "running' OR '1'='1"
  When List is called with status filter StatusRunning
  Then the page holds exactly the running instance

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```
