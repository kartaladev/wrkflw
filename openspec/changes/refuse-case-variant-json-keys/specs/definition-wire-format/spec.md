## MODIFIED Requirements

### Requirement: Unknown keys are refused at decode
Decoding a definition SHALL refuse, with an error naming the key, any key that is not byte-for-byte a key the wire format defines at that position, in YAML and JSON alike. A JSON key that matches a defined key only under Unicode case folding SHALL be refused with `ErrCaseVariantKey`, naming the authored and the defined key, at every nesting level; an inline rule document's keys are not wire keys.

#### Scenario: An unknown key is refused in either format
```gherkin
Scenario Outline: An undefined key fails the decode and is named
  Given a valid definition in <format>
  And an extra key "<key>" added <where>
  When the definition is decoded
  Then decoding fails
  And the error names "<key>"

  Examples:
    | format | where                       | key             |
    | JSON   | at the top level            | bogus_top       |
    | JSON   | on a node                   | bogus_node_key  |
    | JSON   | on a node, as a misspelling | eligable_roles  |
    | YAML   | at the top level            | bogus_top_level |
    | YAML   | on a node, as a misspelling | eligable_roles  |
    | YAML   | on a flow                   | bogus_flow_key  |
```

#### Scenario: The unmodified definition still decodes
```gherkin
Scenario Outline: A definition with only defined keys decodes
  Given a valid definition in <format> carrying only defined keys
  When the definition is decoded
  Then decoding succeeds

  Examples:
    | format |
    | JSON   |
    | YAML   |
```

#### Scenario: A JSON key that case-folds onto a defined key is refused at every level
```gherkin
Scenario Outline: A fold-equal JSON key fails with ErrCaseVariantKey and names both spellings
  Given a valid JSON definition
  And the defined key "<defined>" <where> spelled as "<authored>"
  When the definition is decoded with json.Unmarshal
  Then decoding fails with ErrCaseVariantKey
  And the error names "<authored>" and "<defined>"

  Examples:
    | where                                         | defined        | authored       |
    | at the top level                              | id             | ID             |
    | at the top level                              | version        | Version        |
    | at the top level                              | nodes          | NODES          |
    | at the top level                              | cancel_actions | Cancel_Actions |
    | on a node                                     | id             | Id             |
    | on a node                                     | kind           | KIND           |
    | on a node                                     | name           | Name           |
    | on a node                                     | action         | Action         |
    | on a node                                     | eligible_roles | eLiGiBlE_rOlEs |
    | on a node, by a non-ASCII fold (U+212A)       | kind           | Kind      |
    | on a node, by a non-ASCII fold (U+017F)       | signal_name    | ſignal_name |
    | on a flow                                     | target         | Target         |
    | on a flow                                     | is_default     | IS_DEFAULT     |
    | inside a node's retry_policy                  | max_attempts   | Max_Attempts   |
    | inside a node's timer_trigger                 | kind           | Kind           |
    | inside an at_times entry of a trigger         | hour           | Hour           |
    | inside a node's validation                    | kind           | Kind           |
    | at the top level of a nested subprocess       | id             | ID             |
    | on a node of a nested subprocess              | kind           | Kind           |
    | on a flow of a nested subprocess              | source         | Source         |
    | on a node two subprocess levels down          | name           | NAME           |
```

#### Scenario: A case variant cannot shadow the defined key
```gherkin
Scenario Outline: Carrying both spellings is refused whichever comes first
  Given a JSON definition whose top level carries <pair>
  When the definition is decoded with json.Unmarshal
  Then decoding fails with ErrCaseVariantKey
  And the error names "ID"

  Examples:
    | pair                          |
    | "id":"d" followed by "ID":"z" |
    | "ID":"z" followed by "id":"d" |
```

#### Scenario: A key that does not fold onto a defined key stays an unknown key
```gherkin
Scenario Outline: A near-miss that is not a fold variant is refused as unknown, not as a case variant
  Given a valid JSON definition
  And a node carrying the extra key "<key>"
  When the definition is decoded with json.Unmarshal
  Then decoding fails with an error naming "<key>"
  And the error is not ErrCaseVariantKey

  Examples:
    | key      |
    | " name"  |
    | "name "  |
    | "nàme"   |
    | "names"  |
    | ""       |
```

#### Scenario: Exact keys and case-insensitive values are still accepted
```gherkin
Scenario Outline: Only keys are compared exactly, and only wire-format keys
  Given a JSON definition carrying <content>
  When the definition is decoded with json.Unmarshal
  Then decoding succeeds
  And <observation>

  Examples:
    | content                                                                                      | observation                                        |
    | every defined key spelled exactly, at every level including a nested subprocess, retry_policy, timer_trigger with at_times, and validation | re-encoding it yields the same bytes          |
    | a node whose name value is "ID" and a flow whose id value is "Target"                        | the node's Name is "ID"                            |
    | a businessRuleTask whose rule is the inline document {"Foo":1,"KIND":"x"}                    | the rule's inline document keeps the keys "Foo" and "KIND" |
```

#### Scenario: YAML refuses the same case variants
```gherkin
Scenario Outline: A case-variant key in YAML is refused as an unknown key
  Given a valid YAML definition
  And the defined key "<defined>" <where> spelled as "<authored>"
  When it is parsed with ParseYAML
  Then parsing fails with an error naming "<authored>"

  Examples:
    | where                        | defined      | authored     |
    | at the top level             | id           | ID           |
    | on a node                    | name         | Name         |
    | on a flow                    | target       | Target       |
    | inside a node's retry_policy | max_attempts | Max_Attempts |
    | inside a node's validation   | kind         | Kind         |
```

#### Scenario: Library-encoded definitions are never refused as case variants
```gherkin
Given the golden definition file covering every option-bearing node kind
And a definition parsed from the all-fields YAML fixture
When each is encoded with MarshalJSON and decoded with json.Unmarshal
Then neither is refused with ErrCaseVariantKey
```

#### Scenario: A stored row carrying a case variant fails to load on every dialect
```gherkin
Scenario Outline: The definition store surfaces the refusal on load
  Given a <dialect> definition store
  And a row for "d" version 1 written outside the library whose JSON carries "ID" in place of "id"
  When the definition is loaded with <call>
  Then the load fails with an error wrapping ErrCaseVariantKey

  Examples:
    | dialect  | call                         |
    | Postgres | GetDefinition("d", 1)        |
    | Postgres | Lookup of "d" at latest      |
    | MySQL    | GetDefinition("d", 1)        |
    | MySQL    | Lookup of "d" at latest      |
    | SQLite   | GetDefinition("d", 1)        |
    | SQLite   | Lookup of "d" at latest      |
```

#### Scenario: Republishing over such a row does not disclose the stored key
```gherkin
Given a definition store holding an out-of-band row for "d" version 1 whose JSON carries "ID"
When a valid definition "d" version 1 is published
Then publishing fails with an error saying the stored definition cannot be decoded
And the error does not wrap ErrCaseVariantKey
And the error does not contain "ID"
```

#### Scenario: Many case variants yield a bounded error
```gherkin
Given a JSON definition whose nodes carry 500 case-variant keys
When the definition is decoded with json.Unmarshal
Then decoding fails with ErrCaseVariantKey
And the error names the first case-variant key in document order
And the error text is shorter than the input
```
