## ADDED Requirements

### Requirement: A refused def_ref names the field on every adapter
On every adapter, POST /instances with a `def_ref` that is not a string, or is a string that is not a valid qualifier, SHALL answer 400 `bad_request` with a message that names `def_ref`, is identical on stdlib, gin and fiber and on every supported Go toolchain, and carries no Go type name; the error MUST match `model.ErrInvalidQualifier`. Pending: design OQ3 decides this rule.

#### Scenario: A malformed def_ref is refused naming the field
```gherkin
Scenario Outline: The 400 names def_ref
  Given the <adapter> adapter
  When a caller sends POST /instances with body {"def_ref":<value>}
  Then the response status is 400
  And the error code is "bad_request"
  And the message contains "def_ref"
  And the message does not contain "StartInput"
  And the message is identical across the stdlib, gin and fiber adapters for the same body

  Examples:
    | adapter | value   |
    | stdlib  | 123     |
    | gin     | 123     |
    | fiber   | 123     |
    | stdlib  | true    |
    | gin     | {}      |
    | fiber   | []      |
    | stdlib  | ""      |
    | gin     | ""      |
    | fiber   | ""      |
```

#### Scenario: Negative control - a valid def_ref starts an instance
```gherkin
Scenario Outline: A string qualifier is accepted
  Given the <adapter> adapter over a registry holding definition "greeting"
  When a caller sends POST /instances with body {"def_ref":"greeting"}
  Then the response status is 201

  Examples:
    | adapter |
    | stdlib  |
    | gin     |
    | fiber   |
```
