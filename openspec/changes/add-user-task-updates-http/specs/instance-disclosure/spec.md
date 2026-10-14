## ADDED Requirements

### Requirement: The task routes widen update fields only by their own disclosure category
For a caller the resolver does not identify, the task and history routes SHALL show structural update fields under every set and MUST withhold update `validation` descriptors unless policy is disclosed, `input` and `value` unless variables are disclosed, and `actor` unless actors are disclosed.

#### Scenario: Category per field
```gherkin
Scenario Outline: One category, one field
  Given a resolver that returns httpcore.ErrUnauthenticated and the disclosure set <set>
  And task "t1" whose update "a" declares a json-schema validation and was applied at seq 1 by "alice" with input {"ssn": "111-22-3333"} and value {"ssn": "111-22-3333"}
  When the caller gets <route>
  Then <shown>
  And <withheld>

  Examples:
    | set         | route               | shown                                                   | withheld                                                     |
    | {}          | /tasks/t1           | "a" with required, depends_on, submittable, applied seq | the validation descriptor is absent                          |
    | {policy}    | /tasks/t1           | the validation descriptor of "a"                        | no occurrence of "111-22-3333"                               |
    | {}          | /tasks/t1/updates   | seq 1, name "a", status applied, occurred_at            | input, value and actor are absent                            |
    | {variables} | /tasks/t1/updates   | input and value of seq 1                                | the actor is absent                                          |
    | {actors}    | /tasks/t1/updates   | the actor "alice" of seq 1                              | input and value are absent                                   |
    | {all}       | /tasks/t1/updates   | input, value and actor of seq 1                         | nothing named is withheld                                    |
```

#### Scenario: An identified caller sees every field
```gherkin
Scenario Outline: Disclosure narrows only unidentified callers
  Given a resolver that authenticates "alice" and the disclosure set {}
  When "alice" gets <route>
  Then every update field, including input, value, actor and validation, is present

  Examples:
    | route             |
    | /tasks/t1         |
    | /tasks/t1/updates |
```

#### Scenario: The task projection follows the instance projection
```gherkin
Scenario: Same rules as the instance routes for the task's own fields
  Given a resolver that returns httpcore.ErrUnauthenticated and the disclosure set {}
  When the caller gets /tasks/t1
  Then the task's claim actor, candidates, eligibility and variable snapshot are withheld exactly as GET /instances/{id} withholds them for the same task
```

### Requirement: A rejection reason is withheld like incident error text
For a caller the resolver does not identify, the history route MUST withhold a rejected row's `reason` exactly as an incident's error text is withheld, restoring it only when both operations and variables are disclosed or projection is disabled, and SHALL always show that the row is rejected.

#### Scenario: Reason disclosure (pending Q1)
```gherkin
Scenario Outline: Error text and the unidentified caller
  Given a resolver that returns httpcore.ErrUnauthenticated and the disclosure set <set>
  And task "t1" whose update "a" was rejected at seq 2 with reason "crm: account 4471 locked"
  When the caller gets /tasks/t1/updates
  Then seq 2 shows status rejected
  And the reason is <reason>

  Examples:
    | set                     | reason                      |
    | {}                      | absent                      |
    | {variables}             | absent                      |
    | {operations}            | absent                      |
    | {operations, variables} | "crm: account 4471 locked"  |
    | {all}                   | "crm: account 4471 locked"  |
```

#### Scenario: Negative control - an identified caller reads the reason
```gherkin
Scenario: The actor who can retry sees why
  Given a resolver that authenticates "alice"
  When "alice" gets /tasks/t1/updates for a task with a rejected row
  Then the rejected row carries its reason
```
