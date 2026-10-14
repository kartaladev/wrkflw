## MODIFIED Requirements

### Requirement: Latest resolves to the highest version on every registry
On the durable store and the in-memory and map registries, `Latest(id)` SHALL resolve to the highest version of `id` whatever the publish or registration order, `Version(id, v)` SHALL resolve that exact version, and a lookup of a version or ID that is not stored exactly, code point for code point, SHALL return `ErrDefinitionNotFound`.

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

#### Scenario: A variant spelling of a stored ID is a miss on every registry
```gherkin
Scenario Outline: Lookups never fold an ID onto a differently spelled one
  Given <registry> holding <stored> version 1 and no other definition
  When Version(<variant>, 1) and Latest(<variant>) are looked up, and GetDefinition(<variant>, 1) where the registry offers it
  Then each lookup fails with ErrDefinitionNotFound
  And no lookup returns the definition stored under <stored>

  Examples:
    | registry                     | stored            | variant                    |
    | a postgres definition store  | "payroll-approve" | "Payroll-Approve"          |
    | a mysql definition store     | "payroll-approve" | "Payroll-Approve"          |
    | a sqlite definition store    | "payroll-approve" | "Payroll-Approve"          |
    | a postgres definition store  | "resume"          | "résumé"                   |
    | a mysql definition store     | "resume"          | "résumé"                   |
    | a sqlite definition store    | "resume"          | "résumé"                   |
    | a postgres definition store  | "A"               | "Ａ" (fullwidth U+FF21)    |
    | a mysql definition store     | "A"               | "Ａ" (fullwidth U+FF21)    |
    | a sqlite definition store    | "A"               | "Ａ" (fullwidth U+FF21)    |
    | a postgres definition store  | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)       |
    | a mysql definition store     | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)       |
    | a sqlite definition store    | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)       |
    | a postgres definition store  | "tw"              | "tw " (trailing space)     |
    | a mysql definition store     | "tw"              | "tw " (trailing space)     |
    | a sqlite definition store    | "tw"              | "tw " (trailing space)     |
    | an in-memory registry        | "payroll-approve" | "Payroll-Approve"          |
    | an in-memory registry        | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)       |
```

## ADDED Requirements

### Requirement: Definition IDs are compared exactly on every backend
On every backend, `PublishDefinition` SHALL treat two definition IDs as the same key only when they are equal code point for code point, so IDs that differ only in letter case, accent, character width, Unicode normalisation form or surrounding whitespace are published as distinct definitions.

#### Scenario: A variant spelling publishes as its own definition
```gherkin
Scenario Outline: Publishing under a variant spelling stores a second definition
  Given a definition store on <backend>
  And <stored> version 1 published with cancel action "stored-first"
  When <variant> version 1 is published with cancel action <content>
  Then the publish returns nil
  And two rows exist, one under <stored> and one under <variant>
  And GetDefinition(<variant>, 1) returns cancel action <content>
  And GetDefinition(<stored>, 1) returns cancel action "stored-first"

  Examples:
    | backend  | stored            | variant                 | content         |
    | postgres | "payroll-approve" | "Payroll-Approve"       | "variant"       |
    | mysql    | "payroll-approve" | "Payroll-Approve"       | "variant"       |
    | sqlite   | "payroll-approve" | "Payroll-Approve"       | "variant"       |
    | postgres | "payroll-approve" | "Payroll-Approve"       | "stored-first"  |
    | mysql    | "payroll-approve" | "Payroll-Approve"       | "stored-first"  |
    | sqlite   | "payroll-approve" | "Payroll-Approve"       | "stored-first"  |
    | postgres | "resume"          | "résumé"                | "variant"       |
    | mysql    | "resume"          | "résumé"                | "variant"       |
    | sqlite   | "resume"          | "résumé"                | "variant"       |
    | postgres | "A"               | "Ａ" (fullwidth U+FF21) | "variant"       |
    | mysql    | "A"               | "Ａ" (fullwidth U+FF21) | "variant"       |
    | sqlite   | "A"               | "Ａ" (fullwidth U+FF21) | "variant"       |
    | postgres | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)    | "variant"       |
    | mysql    | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)    | "variant"       |
    | sqlite   | "é" (NFC U+00E9)  | "é" (NFD e + U+0301)    | "variant"       |
    | postgres | "tw"              | "tw " (trailing space)  | "variant"       |
    | mysql    | "tw"              | "tw " (trailing space)  | "variant"       |
    | sqlite   | "tw"              | "tw " (trailing space)  | "variant"       |
    | postgres | "tw"              | " tw" (leading space)   | "variant"       |
    | mysql    | "tw"              | " tw" (leading space)   | "variant"       |
    | sqlite   | "tw"              | " tw" (leading space)   | "variant"       |
```

#### Scenario: IDs at the length limit that differ only in their last rune are distinct
```gherkin
Scenario Outline: Exact comparison holds at the 255-rune bound
  Given a definition store on <backend>
  And an ID of 254 "a" runes followed by "b", published at version 1 with cancel action "lower"
  When an ID of 254 "a" runes followed by "B" is published at version 1 with cancel action "upper"
  Then the publish returns nil
  And each ID reads back its own cancel action

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: The exact spelling is still the same key
```gherkin
Scenario Outline: Negative control, an exactly equal ID is one definition
  Given a definition store on <backend>
  And "payroll-approve" version 1 published with cancel action "first"
  When "payroll-approve" version 1 is published again with <content>
  Then the publish <outcome>
  And exactly one row exists for "payroll-approve"
  And GetDefinition("payroll-approve", 1) and Latest("payroll-approve") return cancel action "first"

  Examples:
    | backend  | content                   | outcome                         |
    | postgres | identical content         | returns nil                     |
    | mysql    | identical content         | returns nil                     |
    | sqlite   | identical content         | returns nil                     |
    | postgres | cancel action "second"    | fails with ErrDefinitionExists  |
    | mysql    | cancel action "second"    | fails with ErrDefinitionExists  |
    | sqlite   | cancel action "second"    | fails with ErrDefinitionExists  |
```

#### Scenario: Concurrent publishes of variant spellings do not contend for one key
```gherkin
Scenario Outline: Two variant spellings published at once both succeed
  Given a definition store on <backend>
  When "payroll-approve" version 1 and "Payroll-Approve" version 1 are published concurrently with different content
  Then both publishes return nil
  And each ID reads back its own content

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

#### Scenario: Exact comparison survives reopening the store
```gherkin
Scenario Outline: A reopened store keeps variant spellings apart
  Given a definition store on <backend> holding "payroll-approve" and "Payroll-Approve" at version 1 with different content
  When the store is closed and a new definition store is opened over the same database after migrations are applied again
  Then GetDefinition("Payroll-Approve", 1) returns the content published under "Payroll-Approve"
  And GetDefinition("payroll-approve", 1) returns the content published under "payroll-approve"

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```

### Requirement: A published definition reads back unchanged on every backend
`GetDefinition` and `Lookup` SHALL return a definition equal to the one published, on every backend, for every node kind, every timer marker and a retry policy, apart from its scoped-action catalog, which is never serialised.

#### Scenario: Every node kind and marker round-trips through the store
```gherkin
Scenario Outline: A definition carrying the feature reads back equal on each backend
  Given a definition store on each of postgres, mysql and sqlite in turn
  And a valid definition that carries <feature> with each option the authoring gate allows on it set to a non-default value
  When it is published and read back with GetDefinition and with Version and Latest lookups
  Then each read returns a definition equal to the published one

  Examples:
    | feature                                          |
    | a startEvent                                     |
    | a startEvent with a timer                        |
    | an endEvent                                      |
    | an intermediateCatchEvent                        |
    | an intermediateCatchEvent with a timer           |
    | an intermediateThrowEvent                        |
    | a compensationThrowEvent                         |
    | a boundaryEvent                                  |
    | a boundaryEvent with a timer                     |
    | a serviceTask                                    |
    | a serviceTask with a retry policy                |
    | a userTask                                       |
    | a receiveTask                                    |
    | a sendTask                                       |
    | a businessRuleTask                               |
    | a subProcess                                     |
    | a callActivity                                   |
    | an exclusiveGateway                              |
    | a parallelGateway                                |
    | an inclusiveGateway                              |
    | an eventBasedGateway                             |
```

#### Scenario: A node kind with no round-trip coverage is detected
```gherkin
Given the set of node kinds the shipped kind packages register
When it is compared with the node kinds the round-trip definitions carry
Then every registered node kind is carried by at least one round-trip definition
And adding a node kind without round-trip coverage fails this check
```

#### Scenario: The scoped-action catalog is the only part not read back
```gherkin
Scenario Outline: Negative control, scoped actions are the one documented loss
  Given a definition store on <backend>
  And a built definition carrying scoped actions
  When it is published and read back
  Then the read-back definition equals the published one in every field except its scoped-action catalog
  And the read-back definition carries no scoped actions

  Examples:
    | backend  |
    | postgres |
    | mysql    |
    | sqlite   |
```
