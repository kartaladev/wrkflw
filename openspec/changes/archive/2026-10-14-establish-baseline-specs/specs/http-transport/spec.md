## Purpose

The observable contract of the HTTP adapters (stdlib, gin, fiber) that mount wrkflw's route groups onto a consumer's router: the documented unauthenticated default posture, error bodies, response headers, body limits, middleware behavior and health probes, including the divergences between adapters that are deliberate and documented.

## ADDED Requirements

### Requirement: Unauthenticated write is the documented default posture
The instance, signal, message, admin and health routes MUST NOT authenticate callers: mounted with no identity configured, each SHALL serve the request rather than refuse it, on every adapter. Securing them is the consumer's job, by mounting them onto a router group their own middleware protects.

#### Scenario: Instance, signal, message and health routes answer an unidentified caller
```gherkin
Scenario Outline: a route served with no identity configured
  Given the <adapter> adapter is mounted with no RequestActorFunc and no actor on the request context
  And the instance, definition or waiting subscription the request names exists
  When an unidentified caller sends <request>
  Then the response status is <status>

  Examples:
    | adapter | request                         | status |
    | stdlib  | POST /instances                 | 201    |
    | gin     | POST /instances                 | 201    |
    | fiber   | POST /instances                 | 201    |
    | stdlib  | POST /instances/{id}/signals    | 200    |
    | gin     | POST /instances/{id}/signals    | 200    |
    | fiber   | POST /instances/{id}/signals    | 200    |
    | stdlib  | POST /messages                  | 202    |
    | gin     | POST /messages                  | 202    |
    | fiber   | POST /messages                  | 202    |
    | stdlib  | GET /readyz                     | 200    |
    | gin     | GET /readyz                     | 200    |
    | fiber   | GET /readyz                     | 200    |
```

#### Scenario: Admin routes registered without authentication
```gherkin
Scenario: an unidentified caller lists instances
  Given AdminRoutes is mounted on a router with no identity configured
  And one instance exists
  When an unidentified caller sends GET /admin/instances
  Then the response status is 200
  And the body lists the instance
```

#### Scenario: Negative control - the human-task routes do refuse the same caller
```gherkin
Given the same mount with no identity configured
When an unidentified caller sends POST /tasks/{token}/claim for an open task
Then the response status is 401
```

### Requirement: Admin routes are opt-in and optional admin routes need their dependency
Mount SHALL NOT register any admin route; admin routes MUST be registered only by mounting AdminRoutes explicitly, and an admin route backed by an optional dependency SHALL stay unregistered while that dependency is nil.

#### Scenario: Mount alone registers no admin route
```gherkin
Scenario Outline: admin is absent until the consumer opts in
  Given the <adapter> adapter mounted through Mount only
  When a caller sends GET /admin/instances
  Then the response status is 404

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: An optional admin route is absent while its dependency is nil
```gherkin
Scenario Outline: dead-letter admin needs its dependency
  Given AdminRoutes is mounted on the <adapter> adapter with DeadLetters set to nil
  When a caller sends GET /admin/dead-letters
  Then the response status is 404

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Negative control - an explicitly mounted always-on admin route answers
```gherkin
Given AdminRoutes is mounted with only the service set
When a caller sends GET /admin/instances
Then the response status is 200
```

### Requirement: Error responses use one client-safe envelope
Every error response SHALL be a JSON envelope with an "error" code and an optional "message"; a 5xx response MUST carry no message and never the underlying error text, the 401 and 413 messages MUST be fixed strings, and only the 400, 403, 404, 409 and 422 responses SHALL render the error's text.

#### Scenario: Status, code and message rendering per error class
```gherkin
Scenario Outline: ClassifyError maps an error carrying an internal marker
  Given an error matching <sentinel> whose text also contains an internal marker
  When it is classified for an HTTP response
  Then the status is <status>
  And the error code is <code>
  And the marker <rendered> in the message

  Examples:
    | sentinel                         | status | code                 | rendered      |
    | ErrUnauthenticated               | 401    | unauthenticated      | does not appear |
    | ErrIdentityUnavailable           | 503    | identity_unavailable | does not appear |
    | an instance-not-found error      | 404    | not_found            | appears       |
    | authz.ErrNotAuthorized           | 403    | forbidden            | appears       |
    | a concurrent-update error        | 409    | conflict             | appears       |
    | ErrRequestBodyTooLarge           | 413    | request_too_large    | does not appear |
    | ErrBadInput                      | 400    | bad_request          | appears       |
    | service.ErrConflict              | 422    | conflict_state       | appears       |
    | no library sentinel              | 500    | internal_error       | does not appear |
```

#### Scenario: An unclassified failure never reaches the client
```gherkin
Scenario Outline: 5xx bodies hide the raw error
  Given the <adapter> adapter over a service that fails with an unclassified internal error
  When a caller sends POST /instances
  Then the response status is 500
  And the body does not contain the internal error's text
  And the message is empty or absent

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Error envelopes are identical across adapters
```gherkin
Scenario Outline: envelope parity
  Given each adapter mounted over the same service
  When a caller sends <request>
  Then every adapter answers <status>
  And the decoded error envelopes are identical

  Examples:
    | request                          | status |
    | GET /instances/does-not-exist    | 404    |
    | POST /instances with body {}     | 400    |
```

### Requirement: Identity and body-size errors outrank other classifications
When an error matches several classes, an identity refusal MUST classify as 401 or 503 ahead of every other class, a 401 cause SHALL win over a 503 wrapper, and an oversize-body error MUST classify as 413 even though it also matches bad input.

#### Scenario: Precedence of co-matching errors
```gherkin
Scenario Outline: an error matching two classes
  Given an error that wraps <outer> around <inner>
  When it is classified for an HTTP response
  Then the status is <status>

  Examples:
    | outer                    | inner                       | status |
    | ErrIdentityUnavailable   | authz.ErrNotAuthorized      | 503    |
    | ErrIdentityUnavailable   | an instance-not-found error | 503    |
    | ErrIdentityUnavailable   | ErrBadInput                 | 503    |
    | ErrIdentityUnavailable   | ErrUnauthenticated          | 401    |
    | ErrBadInput              | ErrRequestBodyTooLarge      | 413    |
```

#### Scenario: Negative control - plain bad input stays 400
```gherkin
Given an error wrapping ErrBadInput alone
When it is classified for an HTTP response
Then the status is 400
```

### Requirement: Inbound request bodies are capped
Each adapter SHALL refuse a request body larger than the configured WithMaxBodyBytes limit (default 1 MiB) with 413 and the fixed envelope {"error":"request_too_large","message":"request body exceeds the configured limit"}, which MUST NOT name the limit; a body of exactly the limit SHALL be admitted, and a limit of zero or less SHALL disable the cap.

#### Scenario: Cap boundary on a required-body route
```gherkin
Scenario Outline: POST /instances against the cap on every adapter
  Given stdlib, gin and fiber each mounted with cap <cap>
  When a caller sends POST /instances with a <size> body
  Then every adapter answers <status>

  Examples:
    | cap              | size            | status |
    | unset (1 MiB)    | 2 MiB           | 413    |
    | 64 bytes         | 256-byte        | 413    |
    | 1024 bytes       | 1025-byte       | 413    |
    | 1024 bytes       | 1024-byte       | 201    |
    | 0                | 2 MiB           | 201    |
    | -1               | 2 MiB           | 201    |
```

#### Scenario: The 413 body is the fixed envelope on every adapter
```gherkin
Given each adapter mounted with a 64-byte cap
When a caller sends a 256-byte body to POST /instances
Then each adapter's body equals {"error":"request_too_large","message":"request body exceeds the configured limit"}
```

#### Scenario: The cap also bounds a route whose body is optional
```gherkin
Scenario Outline: the optional-body incident-resolve admin route
  Given each adapter's AdminRoutes mounted with cap <cap>
  And the named instance does not exist
  When a caller posts <body> to /admin/instances/{id}/incidents/{incidentID}/resolve
  Then every adapter answers <status>

  Examples:
    | cap        | body                 | status |
    | 64 bytes   | a 256-byte body      | 413    |
    | 4096 bytes | {"add_attempts":1}   | 404    |
    | 4096 bytes | no body              | 404    |
    | 0          | a 2 MiB body         | 404    |
```

### Requirement: Fiber refuses bodies above its own BodyLimit without an envelope
On the fiber adapter a request body above the fiber app's own BodyLimit (4 MiB by default) SHALL be refused by the framework before any wrkflw route runs, producing no wrkflw error envelope; this is a documented divergence from stdlib and gin, which MUST answer the ordinary 413 envelope.

#### Scenario: A body above fiber's BodyLimit
```gherkin
Given each adapter mounted with a 1 MiB cap and fiber using its default 4 MiB BodyLimit
When a caller sends a 5 MiB body to POST /instances
Then stdlib answers 413
And gin answers 413
But fiber produces no wrkflw response and no error envelope
```

#### Scenario: Negative control - below fiber's BodyLimit fiber matches the others
```gherkin
Given each adapter mounted with a 64-byte cap
When a caller sends a 256-byte body to POST /instances
Then fiber answers 413 with the same envelope as stdlib and gin
```

### Requirement: Responses the library writes carry nosniff
Every response written by a wrkflw route handler, including error envelopes and health probes, MUST carry the header X-Content-Type-Options: nosniff on every adapter.

#### Scenario: nosniff on each write path
```gherkin
Scenario Outline: library-written responses
  Given stdlib, gin and fiber mounted over the same service, with health routes where needed
  When a caller sends <request>
  Then every adapter answers <status>
  And every response carries X-Content-Type-Options: nosniff

  Examples:
    | request                                 | status |
    | POST /instances for a known definition  | 201    |
    | GET /instances/missing-id               | 404    |
    | GET /readyz                             | 200    |
```

### Requirement: The library does not inject into the consumer's handler chain
wrkflw MUST NOT insert its own handler into a router or middleware chain the consumer owns, so a response written by a consumer middleware that short-circuits SHALL carry no nosniff header unless the consumer places the adapter's NosniffMiddleware ahead of it, which every adapter SHALL offer.

#### Scenario: Short-circuiting consumer middleware with and without NosniffMiddleware
```gherkin
Scenario Outline: a consumer middleware answers 401 itself
  Given the <adapter> adapter whose consumer middleware answers 401 without calling the next handler
  And NosniffMiddleware is <installed>
  When a caller sends GET /instances/{id}
  Then the response status is 401
  And X-Content-Type-Options is <header>

  Examples:
    | adapter | installed                     | header  |
    | stdlib  | not installed                 | absent  |
    | gin     | not installed                 | absent  |
    | fiber   | not installed                 | absent  |
    | stdlib  | wrapped around the chain      | nosniff |
    | gin     | placed before the middleware  | nosniff |
    | fiber   | placed before the middleware  | nosniff |
```

### Requirement: WithMiddleware runs consumer middleware once per request
On gin and fiber, middleware passed to WithMiddleware SHALL run exactly once for a request answered by any wrkflw route, whichever route group answers it; on gin it MUST NOT run for routes the consumer registered on the same router.

#### Scenario: Exactly one execution per request on every route group
```gherkin
Scenario Outline: counting one consumer middleware for one request
  Given Mount on a fresh <adapter> router with a counting middleware passed through WithMiddleware
  When a caller sends <request>
  Then the middleware ran exactly 1 time

  Examples:
    | adapter | request                      |
    | gin     | GET /instances/{id}          |
    | gin     | POST /tasks/{token}/claim    |
    | gin     | POST /messages               |
    | fiber   | GET /instances/{id}          |
    | fiber   | POST /tasks/{token}/claim    |
    | fiber   | POST /messages               |
```

#### Scenario: gin leaves the consumer's own routes alone
```gherkin
Given Mount on a gin router with a counting middleware passed through WithMiddleware
And the consumer registers GET /consumer-route on the same router
When a caller sends GET /consumer-route
Then the response status is 200
And the middleware ran 0 times
```

### Requirement: Health probes report readiness without leaking errors
GET /healthz SHALL answer 200 with status "ok", and GET /readyz SHALL answer 200 with status "ok" when every registered check passes and 503 with status "unavailable" when any fails, naming every check as "ok" or "unavailable" and MUST NOT include a check's error text.

#### Scenario: Readiness outcomes
```gherkin
Scenario Outline: GET /readyz
  Given health routes mounted with <checks>
  When a caller sends GET /readyz
  Then the response status is <status>
  And the body status is <body_status>
  And each check is reported only as "ok" or "unavailable"

  Examples:
    | checks                               | status | body_status |
    | no checks                            | 200    | ok          |
    | every check passing                  | 200    | ok          |
    | one check failing                    | 503    | unavailable |
```

#### Scenario: A failing check yields 503 on every adapter
```gherkin
Scenario Outline: readiness failure parity
  Given the <adapter> adapter's health routes with a check "db" that fails
  When a caller sends GET /readyz
  Then the response status is 503

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Liveness answers 200 on every adapter
```gherkin
Scenario Outline: GET /healthz
  Given the <adapter> adapter's health routes are mounted
  When a caller sends GET /healthz
  Then the response status is 200

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```
