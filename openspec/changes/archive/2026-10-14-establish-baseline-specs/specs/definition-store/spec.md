## Purpose

Defines how process definitions are published, versioned and looked up, in the durable store on every supported backend and in the in-memory registries: published versions are immutable, republishing is safe, and "latest" means the highest version everywhere.

## ADDED Requirements

### Requirement: A published version is immutable and republishing it is idempotent
`PublishDefinition` SHALL store a new `(ID, Version)`, SHALL return nil and leave one row when that version is already stored with content that normalises to the same definition, and SHALL refuse different content with `ErrDefinitionExists`, keeping the first content.

#### Scenario: Republishing identical content is a no-op on every backend
```gherkin
Scenario Outline: Identical republish succeeds and leaves one row
  Given a definition store on <backend>
  And <definition> already published
  When the same content is published again
  Then the publish returns nil
  And exactly one row exists for that ID
  And reading it back returns the published definition unchanged, apart from its scoped-action catalog, which is never serialized

  Examples:
    | backend  | definition                                         |
    | postgres | a minimal definition                               |
    | mysql    | a minimal definition                               |
    | sqlite   | a minimal definition                               |
    | postgres | the rich conformance definition                    |
    | mysql    | the rich conformance definition                    |
    | sqlite   | the rich conformance definition                    |
    | postgres | a built definition carrying scoped actions         |
    | mysql    | a built definition carrying scoped actions         |
    | sqlite   | a built definition carrying scoped actions         |
```

#### Scenario: Different content under a published version is refused
```gherkin
Scenario Outline: Conflicting republish fails and the first content survives
  Given a definition store on <backend>
  And definition "pub-conflict" version 1 published with cancel action "action-first"
  When "pub-conflict" version 1 is published with cancel action "action-second"
  Then the publish fails with ErrDefinitionExists
  And the error names "pub-conflict:1"
  And reading version 1 back returns cancel action "action-first"

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: A stored encoding that differs only in serialisation is not a conflict
```gherkin
Scenario Outline: Equality is judged on normalised content, not stored bytes
  Given a definition store on <backend>
  And a row for "pub-normalise" version 1 seeded directly with an encoding that differs from the canonical one only in serialisation
  When the same definition is published through PublishDefinition
  Then the publish returns nil
  And exactly one row exists for "pub-normalise"

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: The MySQL outcome does not depend on the consumer's DSN flags
```gherkin
Scenario Outline: A conflicting MySQL publish is refused whatever clientFoundRows is set to
  Given a MySQL definition store whose DSN sets clientFoundRows=<flag>
  And a version already published
  When different content is published under that version
  Then the publish fails with ErrDefinitionExists
  And the first content is still the one stored

  Examples:
    | flag  |
    | false |
    | true  |
```

#### Scenario: A publish contending with an uncommitted insert resolves on commit
```gherkin
# Postgres only. The contention outcome was measured on Postgres, where the
# contending insert blocks until the holder resolves; no contention test exists
# for MySQL or SQLite, so this scenario states no outcome for them.
Scenario Outline: The holder's commit decides the contender's outcome
  Given a definition store on <backend>
  And another transaction has inserted "contended" version 1 with content "holder-content" and not yet committed
  When a publish of "contended" version 1 with content "contender-content" is started
  And the other transaction commits
  Then the publish fails with ErrDefinitionExists
  And reading version 1 back returns "holder-content"

  Examples:
    | backend  |
    | postgres |
```

### Requirement: An invalid definition is refused before anything is written
`PublishDefinition` SHALL run the same authoring gate as in-memory registration and SHALL refuse a definition that fails it with `ErrInvalidDefinition`, wrapped with the broken rule, without writing a row.

#### Scenario: An invalid definition writes no row on any backend
```gherkin
Scenario Outline: A refused publish leaves the store empty for that ID
  Given a definition store on <backend>
  When <definition> is published
  Then the publish fails with ErrInvalidDefinition
  And the error also matches <rule>
  And no row exists for that ID

  Examples:
    | backend  | definition                             | rule              |
    | postgres | definition "pub-invalid" with no nodes | ErrNoStartEvent   |
    | mysql    | definition "pub-invalid" with no nodes | ErrNoStartEvent   |
    | sqlite   | definition "pub-invalid" with no nodes | ErrNoStartEvent   |
    | postgres | definition "pub-zero" at version 0     | ErrInvalidVersion |
    | mysql    | definition "pub-zero" at version 0     | ErrInvalidVersion |
    | sqlite   | definition "pub-zero" at version 0     | ErrInvalidVersion |
```

#### Scenario: A valid definition publishes and reads back
```gherkin
Scenario Outline: A valid definition round-trips through the store
  Given a definition store on <backend>
  When valid definition "d-rr" version 1 is published
  Then the publish succeeds
  And GetDefinition("d-rr", 1) returns ID "d-rr" and version 1

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

### Requirement: Latest resolves to the highest version on every registry
On the durable store and the in-memory and map registries, `Latest(id)` SHALL resolve to the highest version of `id` whatever the publish or registration order, `Version(id, v)` SHALL resolve that exact version, and a lookup of a version or ID that differs from every stored one by more than letter case, accent, width or Unicode normalisation SHALL return `ErrDefinitionNotFound`.

#### Scenario: The durable store resolves latest and pinned lookups
```gherkin
Scenario Outline: Lookup by qualifier on each backend
  Given a definition store on <backend>
  And "d-lq" versions 1 and 2 published
  When each qualifier is looked up
  Then Version("d-lq", 1) returns version 1
  And Version("d-lq", 2) returns version 2
  And Latest("d-lq") returns version 2
  And Version("d-lq", 99) fails with ErrDefinitionNotFound
  And Latest("no-such-id") fails with ErrDefinitionNotFound

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: Registering an older version after a newer one does not demote latest
```gherkin
Given an empty in-memory definition registry
When "order" version 2 is registered and then "order" version 1 is registered
Then Latest("order") resolves to version 2
And Version("order", 1) resolves to version 1
And Version("order", 2) resolves to version 2
```

#### Scenario: The map registry also resolves latest by highest version
```gherkin
Given a map definition registry constructed with "proc" versions 1 and 2
When Latest("proc") is looked up
Then it resolves to version 2
And Version("proc", 1) resolves to version 1
```

#### Scenario: An in-memory miss is reported
```gherkin
Scenario Outline: A lookup of an unregistered ID or version fails
  Given <registry>
  When <qualifier> is looked up
  Then the lookup fails with ErrDefinitionNotFound

  Examples:
    | registry                                       | qualifier               |
    | an empty in-memory definition registry         | Latest("nonexistent")   |
    | an in-memory registry holding "order" v1, v2   | Version("order", 9)     |
    | an empty map definition registry               | a Latest qualifier      |
```

### Requirement: In-memory registration keeps the first registration of a version
`MemDefinitionRegistry.Register` and `RegisterDefinition` SHALL refuse a registration of an already-registered `(ID, Version)` carrying different content with `ErrDefinitionExists`, SHALL keep the first registration, and SHALL accept another version of the same ID.

#### Scenario: A duplicate version with different content is refused
```gherkin
Scenario Outline: Different content under a registered version is refused and the first kept
  Given <registry> holding "sub" version 2 with cancel action "first"
  When "sub" version 2 is registered again with cancel action "second"
  Then registration fails with ErrDefinitionExists
  And Version("sub", 2) still resolves to the definition with cancel action "first"

  Examples:
    | registry                                  |
    | an in-memory registry                     |
    | the process-global registry, via RegisterDefinition |
```

#### Scenario: A different version of the same ID is accepted
```gherkin
Given an in-memory registry holding "order" version 2
When "order" version 1 is registered
Then registration succeeds
```

### Requirement: Message-start names are unique across globally registered definitions
`RegisterDefinition` SHALL refuse with `ErrDuplicateMessageStart` a definition whose message-start name is already the message-start name of another registered definition ID, or is repeated on two of its own start nodes, and SHALL accept distinct names. Whether a name held only by a superseded version collides is not specified.

#### Scenario: A colliding message-start name is refused
```gherkin
Scenario Outline: Duplicate message-start names fail registration
  Given the process-global definition registry
  When <registrations>
  Then <outcome>

  Examples:
    | registrations                                                                 | outcome                                                               |
    | "cross-a" and then "cross-b" are registered, both starting on message M       | "cross-a" registers and "cross-b" fails with ErrDuplicateMessageStart |
    | one definition declaring message M on two different start nodes is registered | registration fails with ErrDuplicateMessageStart                      |
```

#### Scenario: Distinct message-start names register
```gherkin
Given the process-global definition registry
When two definitions starting on distinct messages M1 and M2 are registered
Then both register
```
