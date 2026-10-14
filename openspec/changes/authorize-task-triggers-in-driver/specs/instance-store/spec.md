## ADDED Requirements

### Requirement: A candidate resolution's requesting actor round-trips through the journal
`Entries` SHALL return a recorded `HumanCandidatesResolved` trigger carrying the same requesting actor (`By`) and candidates it was committed with, identically on Postgres, MySQL and SQLite, and a journal row recorded before the requesting actor existed MUST decode with the zero actor and its candidates intact rather than fail.

#### Scenario: Requesting actor survives the journal on each dialect
```gherkin
Scenario Outline: By and candidates round-trip
  Given a <dialect> instance store
  And an instance whose committed step recorded a HumanCandidatesResolved with By actor ID "alice", roles ["manager"], attributes {"desk": "7"} and candidates [c]
  When Entries is read for the instance
  Then the HumanCandidatesResolved entry has By actor ID "alice", roles ["manager"] and attributes {"desk": "7"}
  And its candidates are exactly [c]

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: A row written before the requesting actor existed still decodes
```gherkin
Scenario Outline: Legacy candidates row
  Given a <dialect> instance store
  And a journal row of kind HumanCandidatesResolved whose payload carries a task ID and candidates [c] and no "by" key
  When Entries is read for the instance
  Then no error is returned
  And the entry's By is the zero actor
  And its candidates are exactly [c]

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: Negative control - other human trigger rows are unchanged
```gherkin
Scenario Outline: Neighbouring trigger kinds keep their actors
  Given a SQLite instance store
  And a committed step that recorded a <trigger> naming actor "alice"
  When Entries is read for the instance
  Then the entry's <actor field> is "alice"

  Examples:
    | trigger         | actor field |
    | HumanClaimed    | Actor       |
    | HumanCompleted  | Actor       |
    | HumanReassigned | By          |
```
