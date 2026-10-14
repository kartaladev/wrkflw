## ADDED Requirements

### Requirement: An authored definition document is refused above the byte limit
Decoding a definition from JSON, or parsing one from YAML, MUST refuse a document longer than the byte limit with `model.ErrDefinitionTooLarge` before decoding its content, whichever field carries the bytes; a document exactly at the limit SHALL decode. The default limit MUST be finite, and `ParseYAML` SHALL accept a configured limit.

#### Scenario: Byte-limit boundary in both formats
```gherkin
Scenario Outline: Byte-limit boundary in both formats
  Given a valid <format> definition document of <size> whose bulk is <carrier>
  And <limit>
  When the document is decoded
  Then the outcome is <outcome>

  Examples:
    | format | size                       | carrier                         | limit                                  | outcome                      |
    | JSON   | one byte over the default  | an inline rule document         | the default limit                      | model.ErrDefinitionTooLarge  |
    | JSON   | one byte over the default  | a node name                     | the default limit                      | model.ErrDefinitionTooLarge  |
    | JSON   | one byte over the default  | 50,000 nodes                    | the default limit                      | model.ErrDefinitionTooLarge  |
    | JSON   | exactly the default        | an inline rule document         | the default limit                      | decoded                      |
    | YAML   | 1025 bytes                 | an inline rule mapping          | ParseYAML configured with 1024 bytes   | model.ErrDefinitionTooLarge  |
    | YAML   | 1025 bytes                 | a node name                     | ParseYAML configured with 1024 bytes   | model.ErrDefinitionTooLarge  |
    | YAML   | 1024 bytes                 | an inline rule mapping          | ParseYAML configured with 1024 bytes   | decoded                      |
    | YAML   | 1023 bytes                 | an inline rule mapping          | ParseYAML configured with 1024 bytes   | decoded                      |
    | YAML   | one byte over the default  | an inline rule mapping          | the default limit                      | model.ErrDefinitionTooLarge  |
```

#### Scenario: The limit counts bytes, not characters
```gherkin
Scenario Outline: The limit counts bytes, not characters
  Given ParseYAML configured with a limit of <limit>
  And a YAML definition whose node name holds multi-byte UTF-8 characters, totalling 1024 bytes and fewer characters
  When the document is parsed
  Then the outcome is <outcome>

  Examples:
    | limit      | outcome                     |
    | 1024 bytes | decoded                     |
    | 1023 bytes | model.ErrDefinitionTooLarge |
```

#### Scenario: An oversize document is refused without decoding its content
```gherkin
Given ParseYAML configured with a limit of 1 MiB
When a reader supplying an endless stream of YAML bytes is parsed
Then parsing fails with model.ErrDefinitionTooLarge
And no more than the limit plus one byte is read from the reader
```

#### Scenario: A definition already stored is not re-checked on load
```gherkin
Scenario Outline: A definition already stored is not re-checked on load
  Given a <dialect> definition store holding a definition whose stored document is one byte over the default limit, written before the limit existed
  When the definition is loaded by ID and version
  Then it loads without error
  And instances bound to it can still be driven

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
    | SQLite   |
```

#### Scenario: Negative control - library-encoded definitions under the limit round-trip
```gherkin
Given a definition with a businessRuleTask carrying a 1 KiB inline rule document
When it is encoded as JSON and decoded again
Then decoding succeeds and the inline rule document is unchanged
```
