## ADDED Requirements

### Requirement: A variables-limit refusal answers 413 identically on every adapter
Each adapter MUST answer a request refused with `runtime.ErrVariablesTooLarge` or `runtime.ErrVariablesTooDeep` with 413 and the fixed envelope {"error":"variables_too_large","message":"variables exceed the configured limit"}, which MUST NOT name the limit or echo the payload, and the request MUST leave the instance unchanged.

#### Scenario: Variables-limit refusal per route and adapter
```gherkin
Scenario Outline: Variables-limit refusal per route and adapter
  Given the <adapter> adapter mounted with a 1 MiB body cap over a ProcessDriver with a per-payload limit of 64 bytes and a depth limit of 8
  When a caller sends <request> with <payload>
  Then the response status is <status>

  Examples:
    | adapter | request                         | payload                                 | status |
    | stdlib  | POST /instances                 | variables encoding to 65 bytes          | 413    |
    | gin     | POST /instances                 | variables encoding to 65 bytes          | 413    |
    | fiber   | POST /instances                 | variables encoding to 65 bytes          | 413    |
    | stdlib  | POST /instances                 | variables nested 9 deep                 | 413    |
    | gin     | POST /instances                 | variables nested 9 deep                 | 413    |
    | fiber   | POST /instances                 | variables nested 9 deep                 | 413    |
    | stdlib  | POST /instances/{id}/signals    | a payload encoding to 65 bytes          | 413    |
    | gin     | POST /messages                  | a payload encoding to 65 bytes          | 413    |
    | fiber   | POST /tasks/{token}/complete    | an output encoding to 65 bytes          | 413    |
    | stdlib  | POST /instances                 | variables encoding to 64 bytes          | 201    |
    | gin     | POST /instances                 | variables encoding to 64 bytes          | 201    |
    | fiber   | POST /instances                 | variables encoding to 64 bytes          | 201    |
```

#### Scenario: The refusal envelope discloses neither the limit nor the payload
```gherkin
Given each adapter mounted over a ProcessDriver with a per-payload limit of 64 bytes
When a caller sends POST /instances with variables {"secret": "s3cr3t-value-padded-past-the-limit-xxxxxxxxxxxxxxxxxxxxxxx"}
Then each adapter's body equals {"error":"variables_too_large","message":"variables exceed the configured limit"}
And no body contains "64" or "s3cr3t-value"
```

#### Scenario: Negative control - the body cap still answers its own envelope
```gherkin
Given each adapter mounted with a 64-byte body cap over a ProcessDriver with a per-payload limit of 1 MiB
When a caller sends a 256-byte body to POST /instances
Then each adapter answers 413 with {"error":"request_too_large","message":"request body exceeds the configured limit"}
```
