## MODIFIED Requirements

### Requirement: Identity is refused before the request body is read
On the claim, complete, reassign and updates task routes an unauthenticated request MUST be refused with 401 before its body is read, so the body's size or validity SHALL NOT change the answer and an unauthenticated caller learns nothing about whether its body parsed.

#### Scenario: Body problems do not pre-empt the 401
```gherkin
Scenario Outline: unauthenticated request with a bad body
  Given the <adapter> adapter mounted with no identity configured
  When an unauthenticated caller sends POST /tasks/{token}/<operation> with <body>
  Then the response status is 401

  Examples:
    | adapter | operation | body                                  |
    | stdlib  | claim     | a body that fails on the first read   |
    | stdlib  | complete  | a body that fails on the first read   |
    | stdlib  | reassign  | a body that fails on the first read   |
    | gin     | claim     | a JSON string where an object belongs |
    | gin     | complete  | a JSON string where an object belongs |
    | gin     | reassign  | a JSON string where an object belongs |
    | fiber   | claim     | a body larger than the configured cap |
    | fiber   | complete  | a body larger than the configured cap |
    | fiber   | reassign  | a body larger than the configured cap |
    | stdlib  | updates   | a body that fails on the first read   |
    | gin     | updates   | a JSON string where an object belongs |
    | fiber   | updates   | a body larger than the configured cap |
```

#### Scenario: The pre-empting 401 carries the unauthenticated code
```gherkin
Scenario Outline: 401 envelope code with a bad body
  Given the <adapter> adapter mounted with no identity configured
  When an unauthenticated caller sends POST /tasks/{token}/claim with <body>
  Then the error code is "unauthenticated"

  Examples:
    | adapter | body                                  |
    | stdlib  | a body that fails on the first read   |
    | gin     | a JSON string where an object belongs |
    | fiber   | a body larger than the configured cap |
```

#### Scenario: The update route's 401 carries the unauthenticated code
```gherkin
Scenario Outline: 401 envelope code on the update route
  Given the <adapter> adapter mounted with no identity configured
  When an unauthenticated caller sends POST /tasks/{token}/updates with a body larger than the configured cap
  Then the response status is 401
  And the error code is "unauthenticated"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Negative control - an authenticated oversize body is still 413
```gherkin
Scenario: Identity resolved, body over the cap
  Given the stdlib adapter with a body cap and an authenticated caller
  When the caller claims a task with a body larger than the cap
  Then the response status is 413
```
