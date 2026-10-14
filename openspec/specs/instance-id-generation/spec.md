# instance-id-generation Specification

## Purpose
Defines how a new process instance gets its identifier: a pluggable generator the consumer injects, how an explicit identifier takes precedence, and how a generator failure is reported.

## Requirements

### Requirement: Configured generator mints new instance IDs
When an instance is started without an explicit instance ID, the engine SHALL obtain the ID from the generator configured through `service.WithIDGenerator` or `runtime.WithIDGenerator`, and the started instance SHALL carry exactly that ID.

#### Scenario: Service start uses the configured generator
```gherkin
Given a ProcessEngine built with service.WithIDGenerator returning "svc-gen-1"
And a registered start-to-end definition "d"
When StartInstance is called for the latest version of "d"
Then the returned instance has InstanceID "svc-gen-1"
```

#### Scenario: Driver start with an empty instance ID uses the configured generator
```gherkin
Given a ProcessDriver built with runtime.WithIDGenerator returning "gen-123"
When Drive is called with an empty instance ID
Then the resulting state has InstanceID "gen-123"
```

### Requirement: An explicit instance ID takes precedence over the generator
When the caller supplies a non-empty instance ID to `ProcessDriver.Drive`, the driver SHALL use that ID and MUST NOT use a value from the configured generator.

#### Scenario: Empty versus explicit instance ID
```gherkin
Scenario Outline: the generated value is used only for an empty ID
  Given a ProcessDriver whose generator returns "<generated>"
  When Drive is called with instance ID "<supplied>"
  Then the resulting state has InstanceID "<expected>"

  Examples:
    | supplied   | generated          | expected   |
    |            | gen-123            | gen-123    |
    | explicit-1 | SHOULD-NOT-BE-USED | explicit-1 |
```

### Requirement: A generator failure fails the start with the generator's error
When the configured generator returns an error while an instance ID is being minted, the start call SHALL return an error that matches the generator's error under `errors.Is`, rather than panicking or substituting another ID.

#### Scenario: Generator error propagates from both entry points
```gherkin
Scenario Outline: the generator's own error reaches the caller
  Given a <entry> configured with a generator that returns error "no entropy"
  When a new instance is started without an explicit ID through <call>
  Then the returned error matches "no entropy" under errors.Is

  Examples:
    | entry         | call                  |
    | ProcessEngine | StartInstance         |
    | ProcessDriver | Drive with an empty ID |
```

#### Scenario: Negative control, a succeeding generator starts the instance
```gherkin
Given a ProcessEngine configured with a generator that returns "svc-gen-1" and no error
When StartInstance is called
Then no error is returned
And the instance has InstanceID "svc-gen-1"
```

### Requirement: Shipped generators produce well-formed identifiers
`idgen.UUIDv7` SHALL mint RFC 9562 version-7 UUID strings, `idgen.XID` SHALL mint non-empty, distinct identifiers containing no hyphen and never return an error, and `idgen.Func` SHALL return exactly the value and error of the function it adapts.

#### Scenario: UUIDv7 yields a parseable version-7 UUID
```gherkin
Given the idgen.UUIDv7 generator
When NewID is called
Then no error is returned
And the result parses as a UUID whose version is 7
```

#### Scenario: XID yields distinct hyphen-free identifiers
```gherkin
Given the idgen.XID generator
When NewID is called twice
Then both calls return a nil error
And both results are non-empty
And the two results differ
And neither result contains "-"
```

#### Scenario: Func adapts a plain function
```gherkin
Scenario Outline: Func passes through the adapted function's result
  Given idgen.Func wrapping a function that returns ("<value>", <err>)
  When NewID is called
  Then the result is "<value>"
  And the returned error is <expected_err>

  Examples:
    | value   | err             | expected_err                      |
    | fixed-1 | nil             | nil                               |
    |         | errors.New boom | an error matching boom (errors.Is) |
```
