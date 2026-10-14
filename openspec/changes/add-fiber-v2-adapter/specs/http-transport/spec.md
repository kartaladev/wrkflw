## ADDED Requirements

### Requirement: The Fiber v2 adapter serves the route table like the others
The `fiberv2` adapter SHALL register the same routes as the stdlib adapter and SHALL answer every request with the same status and byte-identical body; like the fiber adapter, it MUST leave a body above the app's own BodyLimit to the framework, with no wrkflw envelope.

#### Scenario: Parity with stdlib
```gherkin
Scenario Outline: fiberv2 against stdlib
  Given stdlib and fiberv2 mounted over the same service holding instance "i-1" and an open task
  When a caller sends <request>
  Then both adapters answer <status>
  And the raw bodies are byte-identical

  Examples:
    | request                                   | status |
    | GET /instances/i-1                        | 200    |
    | GET /instances/does-not-exist             | 404    |
    | POST /instances with body {}              | 400    |
    | POST /instances with trailing bytes       | 400    |
    | POST /tasks/{token}/claim unidentified    | 401    |
    | POST /instances with a body over the cap  | 413    |
    | GET /admin/instances?limit=-1             | 400    |
    | GET /readyz with a failing check          | 503    |
```

#### Scenario: The fasthttp BodyLimit divergence applies to fiberv2
```gherkin
Given fiberv2 mounted with a 1 MiB cap on an app using its default 4 MiB BodyLimit
When a caller sends a 5 MiB body to POST /instances
Then fiberv2 produces no wrkflw response and no error envelope
```

#### Scenario: Negative control - below the BodyLimit the cap answers 413
```gherkin
Given fiberv2 mounted with a 64-byte cap
When a caller sends a 256-byte body to POST /instances
Then the response status is 413
And the body equals {"error":"request_too_large","message":"request body exceeds the configured limit"}
```

### Requirement: Fiber v2 routes see the context consumer middleware sets
On the `fiberv2` adapter, a value consumer middleware places on the request's user context SHALL be the context every library route and identity resolver receives, so an actor set through `authz.ContextWithActor` MUST be honored exactly as on the other adapters.

#### Scenario: Actor from middleware reaches the task route
```gherkin
Scenario Outline: fiberv2 middleware sets the actor through <seam>
  Given fiberv2 mounted with no RequestActorFunc
  And consumer middleware that sets actor "alice" with role "manager" through <seam>
  And an open task whose eligibility admits role "manager"
  When a caller sends POST /tasks/{token}/claim with no body
  Then the response status is <status>

  Examples:
    | seam                                         | status |
    | WithMiddleware and SetUserContext            | 200    |
    | app-level Use and SetUserContext             | 200    |
```

#### Scenario: Negative control - no middleware, no actor
```gherkin
Given fiberv2 mounted with no RequestActorFunc and no middleware
When a caller sends POST /tasks/{token}/claim for an open task
Then the response status is 401
```
