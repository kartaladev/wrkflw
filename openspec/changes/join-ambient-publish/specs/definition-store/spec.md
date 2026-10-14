## ADDED Requirements

### Requirement: A publish joins the transaction its context carries
When its context carries a unit opened by an instance store's `RunInTx` or a transaction lent through `persistence.WithTx`, `PublishDefinition` SHALL write inside that transaction, so the published Version exists after the unit commits and does not exist after it rolls back; with no transaction in the context it MUST commit on its own as before.

#### Scenario: The unit's outcome decides whether the Version exists
```gherkin
Scenario Outline: publish inside a unit, then commit or roll back
  Given a <backend> definition store and a <backend> instance store over the same database
  And a <unit> is open
  When "amb-<ending>" version 1 is published with the unit's context
  Then the publish returns nil
  When the unit <ending>
  Then GetDefinition("amb-<ending>", 1) with a context carrying no transaction <outcome>
  And a new definition store over the same database <outcome>

  Examples:
    | backend  | unit                                         | ending       | outcome                              |
    | postgres | unit opened by RunInTx                       | commits      | returns version 1                    |
    | mysql    | unit opened by RunInTx                       | commits      | returns version 1                    |
    | sqlite   | unit opened by RunInTx                       | commits      | returns version 1                    |
    | postgres | unit opened by RunInTx                       | rolls back   | fails with kernel.ErrDefinitionNotFound |
    | mysql    | unit opened by RunInTx                       | rolls back   | fails with kernel.ErrDefinitionNotFound |
    | sqlite   | unit opened by RunInTx                       | rolls back   | fails with kernel.ErrDefinitionNotFound |
    | postgres | consumer transaction lent through WithTx     | commits      | returns version 1                    |
    | mysql    | consumer transaction lent through WithTx     | commits      | returns version 1                    |
    | sqlite   | consumer transaction lent through WithTx     | commits      | returns version 1                    |
    | postgres | consumer transaction lent through WithTx     | rolls back   | fails with kernel.ErrDefinitionNotFound |
    | mysql    | consumer transaction lent through WithTx     | rolls back   | fails with kernel.ErrDefinitionNotFound |
    | sqlite   | consumer transaction lent through WithTx     | rolls back   | fails with kernel.ErrDefinitionNotFound |
```

#### Scenario: Nothing outside the unit sees the publish before it commits
```gherkin
Scenario Outline: read from outside the open unit
  Given a <backend> definition store
  And a consumer transaction lent through persistence.WithTx in which "iso-def" version 1 was published
  When GetDefinition("iso-def", 1) is called with a context carrying no transaction, before the consumer commits
  Then it fails with kernel.ErrDefinitionNotFound

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: Negative control, a publish with no transaction commits on its own
```gherkin
Scenario Outline: no transaction in the context
  Given a <backend> definition store
  When "solo" version 1 is published with a context carrying no transaction
  Then the publish returns nil
  And GetDefinition("solo", 1) from another connection returns version 1 without any commit by the caller

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

### Requirement: Definition reads inside a unit see the unit's own publish
`GetDefinition` and `Lookup` called with a transaction-carrying context SHALL read through that transaction, so a Version published earlier in the same unit is returned and is counted by Latest; such a read, and a republish in the same unit, MUST complete without waiting on a connection the unit itself holds.

#### Scenario: Publish then read in one unit
```gherkin
Scenario Outline: read-your-own-publish inside one unit
  Given a <backend> definition store in which "ryw" version 1 is committed
  And a consumer transaction lent through persistence.WithTx in which "ryw" version 2 was published
  When <read> is called with the lent context and a 5-second deadline
  Then it returns <result> before the deadline

  Examples:
    | backend  | read                                        | result                 |
    | postgres | GetDefinition("ryw", 2)                     | version 2              |
    | mysql    | GetDefinition("ryw", 2)                     | version 2              |
    | sqlite   | GetDefinition("ryw", 2)                     | version 2              |
    | postgres | Lookup(Latest("ryw"))                       | version 2              |
    | mysql    | Lookup(Latest("ryw"))                       | version 2              |
    | sqlite   | Lookup(Latest("ryw"))                       | version 2              |
    | sqlite   | Lookup(Version("ryw", 1))                   | version 1              |
    | postgres | an identical republish of "ryw" version 2   | nil                    |
    | mysql    | an identical republish of "ryw" version 2   | nil                    |
    | sqlite   | an identical republish of "ryw" version 2   | nil                    |
    | postgres | a different-content publish of "ryw" version 2 | ErrDefinitionExists |
    | mysql    | a different-content publish of "ryw" version 2 | ErrDefinitionExists |
    | sqlite   | a different-content publish of "ryw" version 2 | ErrDefinitionExists |
```

#### Scenario: A single-connection SQLite pool does not deadlock a publish inside a unit
```gherkin
Given a SQLite database whose pool allows one open connection
And a definition store and an instance store over it
And a unit opened by RunInTx
When "one-conn" version 1 is published with the unit's context and a 5-second deadline
Then the publish returns nil before the deadline
And after the unit commits, GetDefinition("one-conn", 1) returns version 1
```

### Requirement: A definition and an Instance of it can be created in one lent transaction
Inside one transaction lent through `persistence.WithTx`, a `StartInstance` whose definition reference names a Version published earlier in that transaction SHALL resolve it, and the definition and the Instance MUST both exist after the consumer commits and calls `Resume`, and neither after it rolls back.

#### Scenario: Publish and start together
```gherkin
Scenario Outline: publish a Version and start an Instance of it atomically
  Given a ProcessEngine over a <backend> instance store whose definition registry is the <backend> definition store
  And a consumer transaction lent through persistence.WithTx
  When "pub-start" version 1 is published with the lent context
  And StartInstance creates instance "ps-1" of Version("pub-start", 1) with the lent context
  Then both calls return nil
  When the consumer <ending>
  Then GetDefinition("pub-start", 1) <def_outcome>
  And GetInstance for "ps-1" <inst_outcome>

  Examples:
    | backend  | ending                        | def_outcome                             | inst_outcome                        |
    | postgres | commits and calls Resume      | returns version 1                       | succeeds                            |
    | mysql    | commits and calls Resume      | returns version 1                       | succeeds                            |
    | sqlite   | commits and calls Resume      | returns version 1                       | succeeds                            |
    | postgres | rolls back and calls Discard  | fails with kernel.ErrDefinitionNotFound | returns kernel.ErrInstanceNotFound  |
    | mysql    | rolls back and calls Discard  | fails with kernel.ErrDefinitionNotFound | returns kernel.ErrInstanceNotFound  |
    | sqlite   | rolls back and calls Discard  | fails with kernel.ErrDefinitionNotFound | returns kernel.ErrInstanceNotFound  |
```

### Requirement: A refused publish leaves the unit committable
A publish inside a unit that is refused with `ErrDefinitionExists` or `ErrInvalidDefinition` SHALL write nothing and MUST NOT mark the unit for rollback, so the caller can publish a new Version in the same unit and commit it.

#### Scenario: Publish a new Version after a refusal, in the same unit
```gherkin
Scenario Outline: the documented remedy works inside the unit
  Given a <backend> definition store in which "rf" version 1 is committed with cancel action "first"
  And a <unit> is open
  When <refused> is published with the unit's context
  Then the publish fails with <error>
  When "rf" version 2 is published with the unit's context and the unit commits
  Then the commit succeeds
  And GetDefinition("rf", 2) returns version 2
  And GetDefinition("rf", 1) returns cancel action "first"

  Examples:
    | backend  | unit                                     | refused                                      | error                |
    | postgres | unit opened by RunInTx                   | "rf" version 1 with cancel action "second"   | ErrDefinitionExists  |
    | mysql    | unit opened by RunInTx                   | "rf" version 1 with cancel action "second"   | ErrDefinitionExists  |
    | sqlite   | unit opened by RunInTx                   | "rf" version 1 with cancel action "second"   | ErrDefinitionExists  |
    | sqlite   | unit opened by RunInTx                   | "rf" at version 0                            | ErrInvalidDefinition |
    | postgres | consumer transaction lent through WithTx | "rf" version 1 with cancel action "second"   | ErrDefinitionExists  |
    | sqlite   | consumer transaction lent through WithTx | "rf" at version 0                            | ErrInvalidDefinition |
```

#### Scenario: A failed publish statement leaves a lent transaction as it was
```gherkin
Scenario Outline: the insert fails inside a lent transaction
  Given a <backend> definition store whose publish insert is set to fail with an injected database error
  And a consumer transaction lent through persistence.WithTx that has inserted its own row "c-pub"
  When "fail-pub" version 1 is published with the lent context
  Then the publish returns the injected error
  When the consumer ignores the error and commits
  Then the consumer row "c-pub" exists
  And GetDefinition("fail-pub", 1) fails with kernel.ErrDefinitionNotFound

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

### Requirement: The caching definition registry never serves or shares an uncommitted definition
A `CachingDefinitionRegistry` lookup made with a transaction-carrying context SHALL NOT populate the cache and MUST NOT share an in-flight result with a lookup outside that transaction, in either direction; so after the unit rolls back, every lookup with a context carrying no transaction returns `kernel.ErrDefinitionNotFound` for the whole TTL.

#### Scenario: A rolled-back publish is never served
```gherkin
Scenario Outline: lookup through the wrapper after the unit rolls back
  Given a CachingDefinitionRegistry with TTL 5 minutes over a <backend> definition store
  And a <unit> in which "cache-rb" version 1 was published
  When Lookup(Version("cache-rb", 1)) is called through the wrapper with the unit's context
  Then it returns version 1
  When the unit rolls back
  Then Lookup(<qualifier>) through the wrapper with a context carrying no transaction fails with kernel.ErrDefinitionNotFound
  And after the clock advances 4 minutes 59 seconds it still fails with kernel.ErrDefinitionNotFound

  Examples:
    | backend  | unit                                     | qualifier                  |
    | postgres | unit opened by RunInTx                   | Version("cache-rb", 1)     |
    | mysql    | unit opened by RunInTx                   | Version("cache-rb", 1)     |
    | sqlite   | unit opened by RunInTx                   | Version("cache-rb", 1)     |
    | postgres | consumer transaction lent through WithTx | Version("cache-rb", 1)     |
    | mysql    | consumer transaction lent through WithTx | Version("cache-rb", 1)     |
    | sqlite   | consumer transaction lent through WithTx | Version("cache-rb", 1)     |
    | sqlite   | consumer transaction lent through WithTx | Latest("cache-rb")         |
```

#### Scenario: Concurrent lookups across the transaction boundary do not share a result
```gherkin
Scenario Outline: one lookup inside the unit, one outside, same key, overlapping
  Given a CachingDefinitionRegistry over a SQLite definition store whose backing lookups are held until released
  And a consumer transaction lent through persistence.WithTx in which "sf" version 1 was published
  When <first> is started and held in the backing store
  And <second> is started while the first is held
  And both are released
  Then the lookup with the lent context returns version 1
  And the lookup with a context carrying no transaction fails with kernel.ErrDefinitionNotFound
  And after the consumer rolls back, a lookup with no transaction fails with kernel.ErrDefinitionNotFound

  Examples:
    | first                                                    | second                                                   |
    | Lookup(Version("sf", 1)) with the lent context           | Lookup(Version("sf", 1)) with no transaction             |
    | Lookup(Version("sf", 1)) with no transaction             | Lookup(Version("sf", 1)) with the lent context           |
```

#### Scenario: Negative control, a committed publish is cached as before
```gherkin
Scenario Outline: commit, then look up twice with no transaction
  Given a CachingDefinitionRegistry with TTL 5 minutes over a <backend> definition store that counts backing lookups
  And a <unit> in which "cache-ok" version 1 was published and which then committed
  When Lookup(Version("cache-ok", 1)) is called twice through the wrapper with a context carrying no transaction
  Then both return version 1
  And the backing store was consulted exactly once

  Examples:
    | backend  | unit                                     |
    | postgres | unit opened by RunInTx                   |
    | sqlite   | consumer transaction lent through WithTx |
```
