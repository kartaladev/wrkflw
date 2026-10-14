## MODIFIED Requirements

### Requirement: Duplicate creation is refused with ErrInstanceExists
Creating an instance whose ID already exists in the store SHALL fail with `kernel.ErrInstanceExists` (re-exported as `persistence.ErrInstanceExists`), whichever driver path reaches the database; on Postgres the conflict MUST be recognised from its SQLSTATE, so pgx and a `database/sql` driver agree.

#### Scenario: Duplicate and fresh IDs
```gherkin
Scenario Outline: Create on each SQL dialect
  Given a <dialect> instance store holding instance "i1"
  When an instance is created with ID "<id>"
  Then the result is <result>

  Examples:
    | dialect                 | id | result                              |
    | Postgres                | i1 | an error matching ErrInstanceExists |
    | Postgres (database/sql) | i1 | an error matching ErrInstanceExists |
    | MySQL                   | i1 | an error matching ErrInstanceExists |
    | SQLite                  | i1 | an error matching ErrInstanceExists |
    | Postgres                | i9 | success with version 1              |
    | Postgres (database/sql) | i9 | success with version 1              |
    | MySQL                   | i9 | success with version 1              |
    | SQLite                  | i9 | success with version 1              |
```

## ADDED Requirements

### Requirement: The stored journal encoding of a trigger does not change
A SQL-backed instance store SHALL decode a journal row written by an earlier wrkflw build to the trigger it recorded, and SHALL write each trigger kind with the same kind discriminator and payload bytes as that earlier build; a row whose kind is outside the vocabulary MUST be refused, never skipped.

#### Scenario: Golden journal rows decode and re-encode unchanged
```gherkin
Scenario Outline: kind <kind>
  Given a journal row of kind "<kind>" captured from the store before the module split
  When the row is decoded and the resulting trigger is encoded again
  Then the decoded trigger equals the one recorded when the row was captured
  And the re-encoded kind is "<kind>" and its payload bytes equal the captured payload

  Examples:
    | kind                       |
    | start_instance             |
    | action_completed           |
    | action_failed              |
    | human_completed            |
    | human_claimed              |
    | human_candidates_resolved  |
    | human_reassigned           |
    | timer_fired                |
    | signal_received            |
    | message_received           |
    | sub_instance_completed     |
    | sub_instance_failed        |
    | compensate_requested       |
    | cancel_requested           |
    | resolve_incident           |
    | resolve_compensation_stall |
```

#### Scenario: Every trigger kind has a golden row
```gherkin
Given the set of trigger kinds the store can write
Then each one has a captured golden journal row
```

#### Scenario: An unknown kind is refused
```gherkin
Scenario Outline: kind "<kind>"
  Given a journal row of kind "<kind>"
  When the row is decoded
  Then it is refused with an error naming the kind "<kind>"

  Examples:
    | kind             |
    |                  |
    | timer_fired_v2   |
    | TIMER_FIRED      |
    |  timer_fired     |
```
