## MODIFIED Requirements

### Requirement: WithMiddleware runs consumer middleware once per request
On every adapter, middleware passed to WithMiddleware SHALL run exactly once for a request answered by any wrkflw route, whichever route group answers it, and MUST NOT run for a route the consumer registered on the same router or for a path no route matches.

#### Scenario: Exactly one execution per request on every route group
```gherkin
Scenario Outline: counting one consumer middleware for one request
  Given Mount on a fresh <adapter> router with a counting middleware passed through WithMiddleware
  When a caller sends <request>
  Then the middleware ran exactly 1 time

  Examples:
    | adapter | request                      |
    | stdlib  | GET /instances/{id}          |
    | stdlib  | POST /tasks/{token}/claim    |
    | stdlib  | POST /messages               |
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

#### Scenario: Consumer routes and unrouted paths are left alone
```gherkin
Scenario Outline: middleware scope is the library's routes only
  Given Mount on a <adapter> router with a counting middleware passed through WithMiddleware
  And the consumer registers GET /consumer-route on the same router
  When a caller sends <request>
  Then the response status is <status>
  And the middleware ran 0 times

  Examples:
    | adapter | request              | status |
    | stdlib  | GET /consumer-route  | 200    |
    | gin     | GET /consumer-route  | 200    |
    | fiber   | GET /consumer-route  | 200    |
    | stdlib  | GET /no-such-path    | 404    |
    | gin     | GET /no-such-path    | 404    |
    | fiber   | GET /no-such-path    | 404    |
```

#### Scenario: Concurrent requests each run the middleware once
```gherkin
Scenario Outline: no shared registration multiplies the count
  Given Mount on a <adapter> router with a counting middleware passed through WithMiddleware
  When 50 callers send GET /instances/{id} concurrently
  Then the middleware ran exactly 50 times

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

## ADDED Requirements

### Requirement: Every adapter writes the same response bytes
For the same request against the same service state, every adapter SHALL answer the same status and a byte-identical body, and every JSON body SHALL carry `Content-Type: application/json` with no charset parameter; a response with no body MUST be empty on every adapter.

#### Scenario: Byte parity across adapters
```gherkin
Scenario Outline: raw bodies compared, not decoded documents
  Given stdlib, gin and fiber mounted over the same service holding instance "i-1"
  When a caller sends <request>
  Then every adapter answers <status>
  And the raw response bodies are byte-identical
  And the Content-Type is <content_type> on every adapter

  Examples:
    | request                        | status | content_type     |
    | GET /instances/i-1             | 200    | application/json |
    | GET /instances/i-1/snapshot    | 200    | application/json |
    | GET /instances/does-not-exist  | 404    | application/json |
    | POST /instances with body {}   | 400    | application/json |
    | GET /healthz                   | 200    | application/json |
    | POST /messages for a waiter    | 202    | absent           |
```

#### Scenario: Negative control - a JSON body does not end in a newline
```gherkin
Given the stdlib adapter over a service holding instance "i-1"
When a caller sends GET /instances/i-1
Then the last byte of the body is "}"
```

### Requirement: A response that cannot be encoded answers 500
Pending OQ1. When a route's response value fails to encode as JSON, every adapter SHALL answer 500 with the envelope {"error":"internal_error"}, MUST NOT render the encoding error's text, and SHALL log the error at ERROR level; a response that encodes SHALL be unaffected.

#### Scenario: Marshal failure on every adapter
```gherkin
Scenario Outline: an instance mapper returns a value whose MarshalJSON fails with "SECRET-marshal-failure"
  Given the <adapter> adapter mounted with that instance mapper and a capturing logger
  When a caller sends POST /instances for a known definition
  Then the response status is 500
  And the body equals {"error":"internal_error"}
  And the body does not contain "SECRET-marshal-failure"
  And the Content-Type is application/json
  And the logger holds one ERROR record carrying the marshal error

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Negative control - an encodable mapper answers its status
```gherkin
Scenario Outline: a mapper returning a plain map
  Given the <adapter> adapter mounted with an instance mapper returning {"ok":true}
  When a caller sends POST /instances for a known definition
  Then the response status is 201
  And the body equals {"ok":true}

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: A JSON request body is exactly one value
Every adapter SHALL accept a request body only when it holds exactly one JSON value followed by nothing but JSON whitespace, and MUST refuse any other trailing content with 400 and the error code "bad_request"; the body cap SHALL still outrank this refusal.

#### Scenario: Trailing content after the value
```gherkin
Scenario Outline: POST /instances with a valid start body followed by <suffix>
  Given the <adapter> adapter mounted with a 4096-byte cap over a registry holding "greeting"
  When a caller sends POST /instances with {"def_ref":"greeting"} followed by <suffix>
  Then the response status is <status>

  Examples:
    | adapter | suffix                      | status |
    | stdlib  | nothing                     | 201    |
    | gin     | nothing                     | 201    |
    | fiber   | nothing                     | 201    |
    | stdlib  | "\n"                        | 201    |
    | gin     | " \t\r\n"                   | 201    |
    | fiber   | "   "                       | 201    |
    | stdlib  | "   " and 64 "x" bytes      | 400    |
    | gin     | "   " and 64 "x" bytes      | 400    |
    | fiber   | "   " and 64 "x" bytes      | 400    |
    | stdlib  | a second value {}           | 400    |
    | gin     | a second value {}           | 400    |
    | fiber   | a second value {}           | 400    |
    | stdlib  | a NUL byte                  | 400    |
    | gin     | " " (non-JSON space)   | 400    |
    | fiber   | 5000 "x" bytes past the cap | 413    |
```

#### Scenario: The rule holds on every body-reading route group
```gherkin
Scenario Outline: trailing bytes on other routes
  Given the <adapter> adapter over a service where the named instance, task or waiter exists
  And the caller is identified where the route requires it
  When a caller sends <request> with a valid body followed by " x"
  Then the response status is 400
  And the error code is "bad_request"

  Examples:
    | adapter | request                         |
    | stdlib  | POST /instances/{id}/signals    |
    | gin     | POST /messages                  |
    | fiber   | POST /tasks/{token}/complete    |
```

### Requirement: A malformed JSON body is refused with one message on every adapter
A request body that is not valid JSON, or whose JSON does not fit the route's input shape, SHALL be refused with 400 and the error code "bad_request" on every adapter, with a byte-identical envelope across adapters for the same body; the message MUST NOT carry a framework's own prefix.

#### Scenario: Malformed bodies
```gherkin
Scenario Outline: POST /instances with <body>
  Given stdlib, gin and fiber mounted over the same service
  When a caller sends POST /instances with <body>
  Then every adapter answers 400
  And the error code is "bad_request"
  And the raw envelopes are byte-identical across adapters
  And no message contains "bind from body"

  Examples:
    | body                     |
    | not json                 |
    | {"def_ref":              |
    | []                       |
    | "a string"               |
    | {"vars":"not-an-object"} |
    | an empty body            |
```

#### Scenario: Negative control - a well-formed body is admitted
```gherkin
Given stdlib, gin and fiber mounted over a registry holding "greeting"
When a caller sends POST /instances with {"def_ref":"greeting","vars":{"name":"ada"}}
Then every adapter answers 201
```

### Requirement: Paging query parameters are refused rather than defaulted when malformed
Pending OQ2. On every listing route and adapter, a `limit` that is not a base-10 integer of at least 1, or a `total` other than `true`, `false`, `1` or `0`, MUST be refused with 400 and code "bad_request"; an absent parameter SHALL take its default, and a limit above the maximum SHALL be clamped.

#### Scenario: limit and total values
```gherkin
Scenario Outline: GET /admin/instances?<query> with three instances stored
  Given the <adapter> adapter's AdminRoutes over a service holding three instances
  When a caller sends GET /admin/instances?<query>
  Then the response status is <status>
  And <outcome>

  Examples:
    | adapter | query          | status | outcome                               |
    | stdlib  | limit=2        | 200    | the page holds 2 items and has_more   |
    | gin     | limit=1        | 200    | the page holds 1 item and has_more    |
    | fiber   | limit=3        | 200    | the page holds 3 items, no has_more   |
    | stdlib  | limit=100000   | 200    | the page holds 3 items                |
    | gin     | (none)         | 200    | the page holds 3 items                |
    | fiber   | limit=0        | 400    | the error code is "bad_request"       |
    | stdlib  | limit=-1       | 400    | the error code is "bad_request"       |
    | gin     | limit=-5       | 400    | the error code is "bad_request"       |
    | fiber   | limit=abc      | 400    | the error code is "bad_request"       |
    | stdlib  | limit=         | 400    | the error code is "bad_request"       |
    | gin     | limit=%202     | 400    | the error code is "bad_request"       |
    | fiber   | limit=2.0      | 400    | the error code is "bad_request"       |
    | stdlib  | limit=99999999999999999999 | 400 | the error code is "bad_request" |
    | gin     | total=true     | 200    | the body carries total_count 3        |
    | fiber   | total=1        | 200    | the body carries total_count 3        |
    | stdlib  | total=false    | 200    | the request succeeds                  |
    | gin     | total=0        | 200    | the request succeeds                  |
    | fiber   | total=maybe    | 400    | the error code is "bad_request"       |
    | stdlib  | total=TRUE     | 400    | the error code is "bad_request"       |
```

#### Scenario: Every listing route parses limit the same way
```gherkin
Scenario Outline: a malformed limit on each listing route
  Given AdminRoutes mounted on the <adapter> adapter with every optional dependency set
  When a caller sends GET <path>?limit=-1
  Then the response status is 400

  Examples:
    | adapter | path                 |
    | stdlib  | /admin/dead-letters  |
    | gin     | /admin/timers        |
    | fiber   | /admin/instances     |
```

#### Scenario: Negative control - an unknown status was already refused
```gherkin
Scenario Outline: the existing status refusal is unchanged
  Given the <adapter> adapter's AdminRoutes
  When a caller sends GET /admin/instances?status=bogus
  Then the response status is 400

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: A malformed body on an optional-body route is refused
Pending OQ3. On a route whose body is optional, every adapter SHALL treat an empty body as absent, and MUST refuse a non-empty body that is not exactly one JSON value fitting the route's input with 400 and code "bad_request" before the route acts.

#### Scenario: Optional bodies on the incident-resolve route
```gherkin
Scenario Outline: POST /admin/instances/{id}/incidents/{incidentID}/resolve with <body>
  Given the <adapter> adapter's AdminRoutes over a service where the named instance does not exist
  When a caller sends that request with <body>
  Then the response status is <status>

  Examples:
    | adapter | body                       | status |
    | stdlib  | no body                    | 404    |
    | gin     | no body                    | 404    |
    | fiber   | {"add_attempts":1}         | 404    |
    | stdlib  | not json                   | 400    |
    | gin     | {"add_attempts":"three"}   | 400    |
    | fiber   | {} xx                      | 400    |
    | stdlib  | whitespace only            | 404    |
```

#### Scenario: The optional claim body
```gherkin
Scenario Outline: POST /tasks/{token}/claim by an eligible, identified caller with <body>
  Given the <adapter> adapter over a service with an open task the caller may claim
  When the caller sends POST /tasks/{token}/claim with <body>
  Then the response status is <status>

  Examples:
    | adapter | body       | status |
    | stdlib  | no body    | 200    |
    | gin     | {}         | 200    |
    | fiber   | not json   | 400    |
```

### Requirement: Consumer middleware runs before identity resolution and the body read
On every adapter, middleware passed to WithMiddleware SHALL run before the library resolves identity or reads the body, so a context value it sets is visible to the route, and a middleware that answers without calling the next handler SHALL stop the route from running.

#### Scenario: An actor set by middleware is the route's actor
```gherkin
Scenario Outline: middleware places an actor through authz.ContextWithActor
  Given the <adapter> adapter mounted with no RequestActorFunc
  And a WithMiddleware that sets actor "alice" with role "manager" on the request context
  And an open task whose eligibility admits role "manager"
  When a caller sends POST /tasks/{token}/claim with no body
  Then the response status is 200
  And the task is claimed by "alice"

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: A short-circuiting middleware stops the route
```gherkin
Scenario Outline: middleware answers 401 itself
  Given the <adapter> adapter with a WithMiddleware that answers 401 without calling the next handler
  When a caller sends POST /instances for a known definition
  Then the response status is 401
  And no instance was started
  And the response carries no X-Content-Type-Options header

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

#### Scenario: Negative control - without the middleware the same claim is refused
```gherkin
Scenario Outline: no actor anywhere
  Given the <adapter> adapter mounted with no RequestActorFunc and no WithMiddleware
  When a caller sends POST /tasks/{token}/claim for an open task
  Then the response status is 401

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```

### Requirement: fiber accepts WithBodyReadTimeout and reports it inert
The fiber adapter SHALL accept a WithBodyReadTimeout option, MUST log exactly one WARN record at mount naming the option as inert on fasthttp when it is set to a positive duration, and SHALL serve requests exactly as it does without the option.

#### Scenario: The option is accepted and reported
```gherkin
Scenario Outline: fiber Mount with WithBodyReadTimeout(<timeout>)
  Given a fiber app and a capturing logger
  When Mount runs with WithBodyReadTimeout(<timeout>)
  Then the logger holds <warns> WARN records naming WithBodyReadTimeout
  And POST /instances for a known definition answers 201

  Examples:
    | timeout | warns |
    | 1s      | 1     |
    | 1ns     | 1     |
    | 0       | 0     |
    | -1s     | 0     |
```

#### Scenario: Negative control - the option is absent
```gherkin
Given a fiber app and a capturing logger
When Mount runs without WithBodyReadTimeout
Then the logger holds no WARN record naming WithBodyReadTimeout
```
