## ADDED Requirements

### Requirement: The default generator is idgen.XID
When no generator is configured, or a nil one is passed to `service.WithIDGenerator` or `runtime.WithIDGenerator`, both the ProcessEngine and the ProcessDriver SHALL mint new instance IDs with `idgen.XID`: 20 lowercase base32hex characters, no hyphen, ordered by creation time.

#### Scenario: Unconfigured entry points mint xid-shaped IDs
```gherkin
Scenario Outline: Default generator on each entry point
  Given a <entry> built <wiring>
  When two instances are started without an explicit ID, one after the other
  Then each ID is 20 characters from [0-9a-v] with no "-"
  And the second ID sorts after the first

  Examples:
    | entry         | wiring                                 |
    | ProcessEngine | with no ID generator option            |
    | ProcessEngine | with service.WithIDGenerator(nil)      |
    | ProcessDriver | with no ID generator option            |
    | ProcessDriver | with runtime.WithIDGenerator(nil)      |
```

#### Scenario: Negative control, a configured generator still wins
```gherkin
Scenario: The default yields to configuration
  Given a ProcessEngine built with service.WithIDGenerator(idgen.UUIDv7())
  When an instance is started without an explicit ID
  Then the ID parses as a UUID whose version is 7
```

### Requirement: UUIDv7 identifiers do not reveal their neighbours
(Pending Q5.) `idgen.UUIDv7` SHALL fill every non-timestamp, non-version, non-variant bit of each identifier from a cryptographically secure source, so identifiers minted in the same millisecond MUST NOT be derivable from one another.

#### Scenario: Same-millisecond IDs differ in their random bits
```gherkin
Scenario: A burst of UUIDv7 IDs
  Given the idgen.UUIDv7 generator
  When 10000 IDs are minted in a tight loop
  Then no two IDs share their final 62 bits
  And no ID's final 62 bits equal another's plus or minus one
```

#### Scenario: Negative control, xid neighbours are derivable by design
```gherkin
Scenario: The default makes no opacity promise
  Given the idgen.XID generator
  When two IDs are minted in the same second by the same process
  Then their 3-byte counters differ by exactly one
```
