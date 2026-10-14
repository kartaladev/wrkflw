## MODIFIED Requirements

### Requirement: In-memory registration keeps the first registration of a version
`MemDefinitionRegistry.Register`, `RegisterDefinition` and their panicking variants SHALL return nil and change nothing when a registered `(ID, Version)` is registered again with content identical under the comparison `PublishDefinition` uses, SHALL refuse different content with `ErrDefinitionExists` (the panicking variants panic), SHALL keep the first registration, and SHALL accept another version of the same ID.

#### Scenario: An identical re-registration is a no-op
```gherkin
Scenario Outline: Registering identical content again succeeds and changes nothing
  Given <registry> holding "sub" version 2 with cancel action "first"
  When "sub" version 2 is registered again through <call> as <second>
  Then <outcome>
  And Version("sub", 2) still resolves to the object registered first
  And ListDefinitions returns "sub" version 2 exactly once

  Examples:
    | registry                     | call                   | second                                                         | outcome                  |
    | an in-memory registry        | Register               | the same object                                                | registration returns nil |
    | an in-memory registry        | Register               | a separately built definition with identical content          | registration returns nil |
    | an in-memory registry        | Register               | the same definition loaded from YAML instead of the builder   | registration returns nil |
    | an in-memory registry        | MustRegister           | a separately built definition with identical content          | the call does not panic  |
    | the process-global registry  | RegisterDefinition     | the same object                                                | registration returns nil |
    | the process-global registry  | RegisterDefinition     | the definition decoded from its own JSON encoding              | registration returns nil |
    | the process-global registry  | MustRegisterDefinition | a separately built definition with identical content          | the call does not panic  |
```

#### Scenario: Equality is judged on normalised content
```gherkin
Scenario Outline: Representations that encode the same definition are identical
  Given an in-memory registry holding "norm" version 1 with <first>
  When "norm" version 1 is registered again with <second>
  Then registration returns nil
  And Version("norm", 1) still resolves to the object registered first

  Examples:
    | first                     | second                          |
    | no cancel actions (nil)   | an empty cancel-action list     |
    | cancel actions "a", "b"   | a fresh slice holding "a", "b"  |
```

#### Scenario: A duplicate version with different content is refused
```gherkin
Scenario Outline: Different content under a registered version is refused and the first kept
  Given <registry> holding "sub" version 2 with cancel actions <first>
  When "sub" version 2 is registered again through <call> with cancel actions <second>
  Then <outcome>
  And Version("sub", 2) still resolves to the definition with cancel actions <first>

  Examples:
    | registry                    | call                   | first          | second         | outcome                                  |
    | an in-memory registry       | Register               | "first"        | "second"       | registration fails with ErrDefinitionExists |
    | an in-memory registry       | MustRegister           | "first"        | "second"       | the call panics                          |
    | the process-global registry | RegisterDefinition     | "first"        | "second"       | registration fails with ErrDefinitionExists |
    | the process-global registry | MustRegisterDefinition | "first"        | "second"       | the call panics                          |
    | an in-memory registry       | Register               | "a", "b"       | "b", "a"       | registration fails with ErrDefinitionExists |
    | an in-memory registry       | Register               | "first"        | "First"        | registration fails with ErrDefinitionExists |
    | an in-memory registry       | Register               | none           | "first"        | registration fails with ErrDefinitionExists |
```

#### Scenario: Definitions differing only in scoped actions are identical and the first catalog is kept
```gherkin
Scenario Outline: The scoped-action catalog takes no part in the comparison
  Given an in-memory registry holding "sc" version 1 whose scoped action "notify" returns "first"
  When "sc" version 1 is registered again, built identically except that <difference>
  Then registration returns nil
  And the scoped catalog of Version("sc", 1) resolves "notify" to the action returning "first"

  Examples:
    | difference                                          |
    | its scoped action "notify" returns "second"         |
    | its only scoped action is named "other"             |
    | it registers no scoped actions                      |
```

#### Scenario: A definition that cannot be encoded is identical only to itself
```gherkin
Scenario Outline: Identity that cannot be shown is refused, never assumed
  Given an in-memory registry holding "cb" version 1 whose user task carries a callback completion validation
  When "cb" version 1 is registered again as <second>
  Then <outcome>
  And Version("cb", 1) still resolves to the object registered first

  Examples:
    | second                                    | outcome                                                                                   |
    | the same object                           | registration returns nil                                                                  |
    | a separately built copy of the definition | registration fails with ErrDefinitionExists, and the error also matches model.ErrUnserializableValidation |
```

#### Scenario: An identical re-registration of an older version does not move latest
```gherkin
Given an in-memory registry holding "order" version 1 and then "order" version 2
When "order" version 1 is registered again with identical content
Then registration returns nil
And Latest("order") still resolves to the object registered as version 2
And Version("order", 1) still resolves to the object registered first as version 1
```

#### Scenario: Concurrent registrations of one version resolve to a single registration
```gherkin
Scenario Outline: Racing registrations of the same version
  Given <registry> with nothing registered under "race" version 1
  When 8 goroutines concurrently register "race" version 1, <content>
  Then <successes> of the registrations return nil
  And every other registration fails with ErrDefinitionExists
  And Version("race", 1) resolves to the content of a registration that returned nil
  And ListDefinitions returns "race" version 1 exactly once

  Examples:
    | registry                    | content                                         | successes |
    | an in-memory registry       | each with identical content                     | all 8     |
    | an in-memory registry       | each with a distinct cancel action              | exactly 1 |
    | the process-global registry | each with identical content, via RegisterDefinition | all 8 |
    | the process-global registry | each with a distinct cancel action, via RegisterDefinition | exactly 1 |
```

#### Scenario: A different version of the same ID is accepted
```gherkin
Scenario Outline: Another version, or another ID, is not a duplicate
  Given an in-memory registry holding "order" version 2
  When <registration> is registered
  Then registration returns nil

  Examples:
    | registration                                   |
    | "order" version 1                              |
    | "order" version 3                              |
    | "Order" version 2, an ID differing only in case |
```

### Requirement: Message-start names are unique across globally registered definitions
`RegisterDefinition` SHALL refuse with `ErrDuplicateMessageStart` a definition whose message-start name is already the message-start name of another registered definition ID, or is repeated on two of its own start nodes, and SHALL accept distinct names. Whether a name held only by a superseded version collides is not specified. An identical re-registration of a registered `(ID, Version)` SHALL NOT be refused as a collision.

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

#### Scenario: An identical re-registration is not a message-start collision
```gherkin
Scenario Outline: Re-registering after another definition took a superseded version's name
  Given the process-global definition registry
  And "a" version 1 registered starting on message M
  And "a" version 2 registered starting on message N
  And "b" version 1 registered starting on message M
  When <registration>
  Then <outcome>

  Examples:
    | registration                                                           | outcome                                          |
    | "a" version 1 is registered again with identical content               | registration returns nil                         |
    | "a" version 1 is registered again through MustRegisterDefinition with identical content | the call does not panic         |
    | "a" version 2 is registered again with identical content               | registration returns nil                         |
    | "b" version 1 is registered again with identical content               | registration returns nil                         |
    | "a" version 1 is registered with different content, starting on unclaimed message M3 | registration fails with ErrDefinitionExists |
    | a new ID "c" version 1 starting on message M is registered             | registration fails with ErrDuplicateMessageStart |
    | a new ID "c" version 1 starting on unclaimed message M4 is registered  | registration returns nil                         |
```
