## ADDED Requirements

### Requirement: Update references are structural and the pending submitter is actor data
The instance projection SHALL show an unidentified caller each task's applied update names, seqs and times and whether an update is pending, and MUST withhold the pending update's submitting actor unless the disclosure set includes actors.

#### Scenario: Projection of update fields (pending Q4)
```gherkin
Scenario Outline: What an unidentified caller sees of a task's updates
  Given an instance whose task "t1" has update "a" applied at seq 1 and update "b" pending at seq 2, submitted by "alice"
  When the instance is projected for an unidentified caller under <set>
  Then the projected task shows update "a" at seq 1 with its time, and a pending update "b" at seq 2
  And the pending submitter is <actor>

  Examples:
    | set       | actor            |
    | {}        | absent           |
    | {policy}  | absent           |
    | {actors}  | "alice"          |
```

#### Scenario: No update value reaches the projection under any set
```gherkin
Scenario Outline: Values are not in the snapshot to project
  Given an instance whose task "t1" has update "a" applied with value {"secret": "111-22-3333"}
  When the instance is projected under <set>
  Then the projection contains no occurrence of "111-22-3333"

  Examples:
    | set          |
    | {}           |
    | {actors}     |
    | {all}        |
```
