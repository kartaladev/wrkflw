## ADDED Requirements

### Requirement: An update is submitted with POST /tasks/{token}/updates and answered 202 with its seq
`POST /tasks/{token}/updates` with body `{"update": <name>, "input": <object>}` SHALL submit that update as the resolved actor and answer 202 with `{"seq": <n>}`, the seq of the row the submission wrote, identically on stdlib, gin and fiber.

#### Scenario: Accepted submission per adapter
```gherkin
Scenario Outline: 202 with the seq
  Given the <adapter> routes mounted over an instance with open task "t1" declaring update "assessment"
  And the resolver authenticates eligible actor "alice"
  When "alice" posts {"update": "assessment", "input": {"score": 4}} to /tasks/t1/updates
  Then the response status is 202 and the body is {"seq": 1}
  And the history of "t1" holds seq 1 with actor "alice"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: The actor is never taken from the body
```gherkin
Scenario: A body-supplied actor is ignored
  Given the resolver authenticates "bob", who is not eligible for task "t1"
  When "bob" posts {"update": "u", "input": {}, "actor": {"id": "alice"}} to /tasks/t1/updates
  Then the response status is 403 with error code "forbidden"
  And no row is written
```

#### Scenario: Request-shape refusals
```gherkin
Scenario Outline: Malformed submissions
  Given an authenticated eligible caller and open task "t1" declaring update "u"
  When the caller posts <body> to /tasks/t1/updates
  Then the response status is <status> with error code "<code>"
  And no row is written

  Examples:
    | body                                   | status | code        |
    | {"update": "nope", "input": {}}        | 400    | bad_request |
    | {"update": "U", "input": {}}           | 400    | bad_request |
    | {"input": {}}                          | 400    | bad_request |
    | {"update": "u", "input": [1, 2]}       | 400    | bad_request |
    | {"update": "u", "input": "x"}          | 400    | bad_request |
    | not JSON                               | 400    | bad_request |
```

#### Scenario: Negative control - an absent input is an empty object
```gherkin
Scenario: input may be omitted for an update with no descriptor
  Given an authenticated eligible caller and open task "t1" declaring update "u" with no validation
  When the caller posts {"update": "u"} to /tasks/t1/updates
  Then the response status is 202
  And the row's input is {}
```

### Requirement: Update refusals map to stable HTTP statuses
The update route MUST answer `validation.ErrInvalidInput` and `engine.ErrUnknownUpdate` with 400 `bad_request`, `engine.ErrUpdateInFlight`, `engine.ErrUpdateDependencyMissing` and `engine.ErrTaskNotOpen` with 422 `conflict_state`, an unknown task with 404 `not_found`, and `authz.ErrNotAuthorized` with 403, identically on every adapter.

#### Scenario: Classification per refusal
```gherkin
Scenario Outline: Engine refusal to HTTP
  Given an authenticated caller and task "t1" where <setup>
  When the caller posts a submission of <update> to /tasks/<token>/updates
  Then the response status is <status> with error code "<code>"

  Examples:
    | setup                                                | update | token | status | code           |
    | "u" requires integer "score" and input lacks it      | "u"    | t1    | 400    | bad_request    |
    | an update action is pending                          | "u"    | t1    | 422    | conflict_state |
    | "b" depends on "a", which is not applied             | "b"    | t1    | 422    | conflict_state |
    | the task is cancelled                                | "u"    | t1    | 422    | conflict_state |
    | no task has token "zz"                               | "u"    | zz    | 404    | not_found      |
    | the caller is not eligible                           | "u"    | t1    | 403    | forbidden      |
```

#### Scenario: The dependency refusal names what is missing
```gherkin
Scenario: The 422 message is actionable
  Given task "t1" where "c" depends on "a" and "b", neither applied
  When an eligible caller submits "c"
  Then the response status is 422
  And the error message names "a" and "b"
```

#### Scenario: Completion refusal for missing updates
```gherkin
Scenario Outline: POST complete on an incomplete task
  Given the <adapter> routes and task "t1" whose required update "a" is not applied
  When an eligible caller posts to /tasks/t1/complete
  Then the response status is 422 with error code "conflict_state"
  And the error message names "a"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: A policy-refused resubmission (pending add-user-task-updates Q1)
```gherkin
Scenario: refuse policy surfaces as a conflict carrying the seq
  Given task "t1" whose update "u" declares resubmit refuse and is applied
  When an eligible caller submits "u" again
  Then the response status is 422 with error code "conflict_state"
  And the history of "t1" ends with a rejected row for the attempt
```

### Requirement: GET /tasks/{token} reports the task, its completability and each declared update
`GET /tasks/{token}` SHALL answer 200 with the task read from its instance snapshot, `completable`, `missing_updates` in declaration order, and one `updates` entry per declared update in declaration order carrying `name`, `required`, `depends_on`, `submittable`, `pending`, `applied` and `validation`, and 404 `not_found` for an unknown task.

#### Scenario: A task mid-way through its updates
```gherkin
Scenario: The read surface for a form
  Given task "t1" declaring "a" (required), "b" (required, depends_on ["a"]) and "c" (required: false)
  And "a" is applied at seq 1 and nothing is pending
  When an identified caller gets /tasks/t1
  Then the response status is 200
  And "completable" is false and "missing_updates" is ["b"]
  And "updates" lists "a", "b", "c" in that order
  And "a" has "applied" {"seq": 1, "at": <time>} and "pending" null
  And "b" has "submittable" true and "applied" null
  And "c" has "required" false and "submittable" true
```

#### Scenario Outline-like rows for submittable
```gherkin
Scenario Outline: When an update is submittable
  Given task "t1" in which <situation>
  When an identified caller gets /tasks/t1
  Then update "<name>" has "submittable" <value>

  Examples:
    | situation                                                    | name | value |
    | "b" depends on "a" and "a" is not applied                    | b    | false |
    | "b" depends on "a" and "a" is applied                        | b    | true  |
    | "a" declares resubmit refuse and is applied                  | a    | false |
    | "a" declares resubmit replace and is applied                 | a    | true  |
    | an update action is pending on the task                      | a    | false |
    | the task is completed                                        | a    | false |
```

#### Scenario: A task with no declared updates
```gherkin
Scenario: Completable with an empty list
  Given task "t1" whose node declares no updates
  When an identified caller gets /tasks/t1
  Then "completable" is true, "missing_updates" is [] and "updates" is []
```

#### Scenario: Unknown task and parity
```gherkin
Scenario Outline: 404 and identical bodies
  Given the <adapter> routes mounted
  When a caller gets /tasks/<token>
  Then the response is <result>

  Examples:
    | adapter | token | result                                               |
    | stdlib  | zz    | 404 not_found                                        |
    | gin     | zz    | 404 not_found                                        |
    | fiber   | zz    | 404 not_found                                        |
    | stdlib  | t1    | 200, byte-identical to the gin and fiber responses   |
```

### Requirement: GET /tasks/{token}/updates returns the task's history in seq order
`GET /tasks/{token}/updates` SHALL answer 200 with the task's update rows in ascending seq, each carrying `seq`, `name`, `status`, `submitted_seq`, `input`, `value`, `reason`, `actor` and `occurred_at`, and 404 `not_found` for an unknown task.

#### Scenario: Paired rows of an action-backed update
```gherkin
Scenario: submitted then applied
  Given task "t1" whose update "a" was submitted at seq 1 and applied at seq 2
  When an identified caller gets /tasks/t1/updates
  Then the rows are seq 1 "submitted" and seq 2 "applied" with submitted_seq 1, in that order
```

#### Scenario: A task with no rows
```gherkin
Scenario: Empty history is not an error
  Given an open task "t1" with no update rows
  When an identified caller gets /tasks/t1/updates
  Then the response status is 200 and the list is empty
```

#### Scenario: Paging a long history (pending Q2)
```gherkin
Scenario Outline: limit and cursor
  Given task "t1" with 120 update rows
  When a caller gets /tasks/t1/updates with <query>
  Then the response is <result>

  Examples:
    | query                 | result                                                  |
    | no parameters         | the first page at the default limit, and a next cursor  |
    | limit=1               | seq 1 only, and a next cursor                           |
    | limit=0               | 400 bad_request                                         |
    | limit=abc             | 400 bad_request                                         |
    | the returned cursor   | the page after the previous one, with no seq repeated   |
    | limit above maximum   | the maximum number of rows                              |
```
