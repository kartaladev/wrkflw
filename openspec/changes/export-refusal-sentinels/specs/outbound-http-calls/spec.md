## MODIFIED Requirements

### Requirement: The default client refuses cross-host redirects
The default client of an action built by NewHTTPCall SHALL follow a redirect only while it stays on the original request's hostname and the chain holds at most 9 redirects, MUST refuse a redirect to a different hostname with a non-retryable ErrCrossHostRedirect, and MUST refuse the 10th redirect of a chain with ErrTooManyRedirects; a change of port on the same hostname SHALL still be followed.

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
    | 302 to itself forever on the same hostname        | ErrTooManyRedirects, in an error naming the 10-redirect limit, not ErrCrossHostRedirect |
```

#### Scenario: The hop limit refuses the 10th redirect
```gherkin
Scenario Outline: a same-host redirect chain of a given length
  Given an httpcall action using the default client
  And its target answers with <hops> same-host 302 redirects and then 200
  When the action runs
  Then the outcome is <outcome>

  Examples:
    | hops | outcome                     |
    | 1    | success with httpStatus 200 |
    | 9    | success with httpStatus 200 |
    | 10   | ErrTooManyRedirects         |
    | 11   | ErrTooManyRedirects         |
```

#### Scenario: The hop-limit refusal keeps its retry classification
```gherkin
Given an httpcall action using the default client
And its target answers with a 302 to itself forever on the same hostname
When the action runs
Then the action fails with ErrTooManyRedirects
And action.IsRetryable reports the error as retryable
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

### Requirement: A failing body validator blocks the request
When WithBodyValidator is set and the validator rejects the request body, the action MUST NOT send the request and SHALL fail with a non-retryable ErrBodyRejected, in an error naming the body validation failure and still matching the validator's own error under `errors.Is`.

#### Scenario: Validator rejects the body
```gherkin
Given an httpcall action with a body validator that rejects every body with a sentinel error
When the action runs
Then the target receives no request
And the action fails with ErrBodyRejected
And the error names the body validation failure
And the error matches the validator's sentinel error under errors.Is
And the error is not retryable
```

#### Scenario: Negative control - a passing validator sends the request
```gherkin
Given an httpcall action with a body validator that accepts the body
When the action runs
Then the target receives the request
And no error is returned
```
