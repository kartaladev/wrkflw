# outbound-http-calls Specification

## Purpose
The HTTP client behavior of the httpcall service action, which calls a REST endpoint from a process: how its default client follows or refuses redirects, how failures are classified for retry, how response and request bodies are bounded, and how connections are pooled.

## Requirements

### Requirement: The default client refuses cross-host redirects
The default client of an action built by NewHTTPCall SHALL follow a redirect only while it stays on the original request's hostname, up to 10 hops, and MUST refuse a redirect to a different hostname with a non-retryable ErrCrossHostRedirect; a change of port on the same hostname SHALL still be followed.

#### Scenario: Redirect destinations under the default client
```gherkin
Scenario Outline: following a redirect with the default client
  Given an httpcall action using the default client
  And its target answers with <redirect>
  When the action runs
  Then the outcome is <outcome>

  Examples:
    | redirect                                          | outcome                                                  |
    | no redirect                                       | success with httpStatus 200                              |
    | 302 to the same hostname on another port          | success with httpStatus 200                              |
    | 302 to a different hostname                       | ErrCrossHostRedirect, not retryable                      |
    | 302 to itself forever on the same hostname        | refused with an error naming the 10-redirect limit, not ErrCrossHostRedirect |
```

#### Scenario: The boundary is hostname equality, not Go's subdomain rule
```gherkin
Scenario Outline: hostnames that differ are refused, subdomains included
  Given the default redirect policy
  When a request to <origin> is redirected to <next>
  Then the hop is refused with ErrCrossHostRedirect

  Examples:
    | origin                     | next                            |
    | https://api.internal/thing | https://sub.api.internal/thing  |
    | https://api.internal/thing | https://elsewhere.example/thing |
```

### Requirement: The default client refuses https-to-plaintext redirects
The default client MUST refuse a redirect that leaves https for a plaintext scheme, judged against the original request's scheme, with a non-retryable ErrDowngradeRedirect; upgrades and chains that began on http SHALL still be followed.

#### Scenario: Scheme changes along a redirect chain
```gherkin
Scenario Outline: the default policy judges a hop by the origin's scheme
  Given a redirect chain that began at <origin>
  And it has already passed through <chain>
  When the next hop is <next>
  Then the hop is <outcome>

  Examples:
    | origin                           | chain                       | next                             | outcome                              |
    | https://api.internal/thing       | nothing else                | http://api.internal/thing        | refused with ErrDowngradeRedirect    |
    | https://api.internal:8443/thing  | nothing else                | http://api.internal:8080/thing   | refused with ErrDowngradeRedirect    |
    | http://api.internal/thing        | nothing else                | https://api.internal/thing       | allowed                              |
    | https://api.internal/a           | nothing else                | https://api.internal/b           | allowed                              |
    | http://api.internal/a            | nothing else                | http://api.internal/b            | allowed                              |
    | http://api.internal/a            | https://api.internal/b      | http://api.internal/c            | allowed                              |
```

#### Scenario: A refused downgrade is not retried
```gherkin
Given an https origin that redirects to a plaintext URL on a different port
And a client carrying the package's redirect policy
When the action runs
Then the error matches ErrDowngradeRedirect
And it is not retryable
```

### Requirement: A supplied client is used exactly as given
An action configured with WithHTTPClient MUST use that client exactly as given: neither redirect refusal SHALL apply to it, and its own transport SHALL be used instead of the action's pool settings.

#### Scenario: A supplied client keeps Go's redirect behavior
```gherkin
Given an httpcall action configured with WithHTTPClient and a bare http.Client
And its target answers 302 to a different hostname
When the action runs
Then the redirect is followed
And the result has httpStatus 200
```

#### Scenario: A supplied client keeps its own transport
```gherkin
Given an httpcall action configured with WithHTTPClient whose transport caps idle connections per host at 2
And WithMaxIdleConnsPerHost is also set to the round's concurrency
When rounds of 8 concurrent requests are made to one host
Then connections are reopened as the supplied transport's cap of 2 dictates
```

### Requirement: Response status classifies retryability
An httpcall action MUST report a 4xx response other than 408 and 429 as a non-retryable error, and SHALL report 5xx, 408 and 429 responses and transport failures as retryable errors.

#### Scenario: Retryability by response
```gherkin
Scenario Outline: classifying a failed call
  Given an httpcall action whose target <response>
  When the action runs
  Then it returns an error that <retryable>

  Examples:
    | response                     | retryable           |
    | answers 400                  | is not retryable    |
    | answers 429                  | is retryable        |
    | answers 503                  | is retryable        |
    | answers 408                  | is retryable        |
    | cannot be reached            | is retryable        |
```

#### Scenario: Negative control - a 2xx is a result, not an error
```gherkin
Given an httpcall action whose target answers 200
When the action runs
Then no error is returned
And the output httpStatus is 200
```

### Requirement: Bodies read into memory are capped
An httpcall action SHALL cap the response body, and any request body it buffers, at WithMaxResponseSize bytes (default 10 MiB), failing with a non-retryable ErrBodyTooLarge when exceeded, before sending when it is the request body; a non-positive size SHALL disable the cap.

#### Scenario: Size cap outcomes
```gherkin
Scenario Outline: bounding bodies read into memory
  Given an httpcall action with <cap>
  When <body>
  Then the outcome is <outcome>

  Examples:
    | cap                     | body                                         | outcome                                         |
    | WithMaxResponseSize(16) | the response body is under 16 bytes          | success                                         |
    | WithMaxResponseSize(16) | the response body exceeds 16 bytes           | ErrBodyTooLarge, not retryable                  |
    | WithMaxResponseSize(0)  | the response body exceeds 16 bytes           | success                                         |
    | no option (10 MiB)      | the response body exceeds 10 MiB             | ErrBodyTooLarge, not retryable                  |
    | WithMaxResponseSize(16) | a buffered request body exceeds 16 bytes     | ErrBodyTooLarge, not retryable, body validator never runs |
```

#### Scenario: An over-cap request body is never sent
```gherkin
Scenario: The target sees no request when the buffered body is over the cap
  Given an httpcall action with WithMaxResponseSize(16) and a target that counts requests
  When a buffered request body of 64 bytes is built
  Then the action fails with ErrBodyTooLarge
  And the target has received no request
```

### Requirement: A failing body validator blocks the request
When WithBodyValidator is set and the validator rejects the request body, the action MUST NOT send the request and SHALL fail with a non-retryable error naming the body validation failure.

#### Scenario: Validator rejects the body
```gherkin
Given an httpcall action with a body validator that rejects every body
When the action runs
Then the target receives no request
And the action fails with an error naming the body validation failure
And the error is not retryable
```

#### Scenario: Negative control - a passing validator sends the request
```gherkin
Given an httpcall action with a body validator that accepts the body
When the action runs
Then the target receives the request
And no error is returned
```

### Requirement: The default client owns a sized connection pool
Each action built by NewHTTPCall SHALL use its own transport, not the process-wide default transport, that keeps up to 100 idle connections per host unless WithMaxIdleConnsPerHost overrides it (a non-positive value restoring Go's default of 2), with no total per-host limit unless WithMaxConnsPerHost sets one.

#### Scenario: Connections opened for rounds of 8 concurrent requests
```gherkin
Scenario Outline: pool sizing across rounds of concurrent requests to one host
  Given an httpcall action configured with <option>
  When several rounds of 8 truly concurrent requests are made to one host
  Then the server accepts <connections>

  Examples:
    | option                         | connections                                    |
    | no pool option                 | exactly 8 connections in total                 |
    | WithMaxIdleConnsPerHost(2)     | the count an idle cap of 2 predicts            |
    | WithMaxIdleConnsPerHost(0)     | the count an idle cap of 2 predicts            |
    | WithMaxIdleConnsPerHost(-1)    | the count an idle cap of 2 predicts            |
    | WithMaxConnsPerHost(1)         | exactly 1 connection                           |
```

#### Scenario: Construction does not depend on the process-wide default transport
```gherkin
Given http.DefaultTransport has been replaced by a wrapping round tripper
When an httpcall action is built with NewHTTPCall and run against a server
Then construction does not panic
And the call succeeds with httpStatus 200
```
