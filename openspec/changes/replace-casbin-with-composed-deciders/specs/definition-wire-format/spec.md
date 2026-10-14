## ADDED Requirements

### Requirement: A user task carries claimant_only and reassign_eligibility on the wire
Both decoders SHALL accept on a userTask node a boolean `claimant_only` and an object `reassign_eligibility` with keys `roles`, `privileges` and `expr` (shape pending Q2), and encoding SHALL round-trip them; on any other node kind either key with a non-zero value MUST be refused with `ErrKeyNotOnKind`, and an unknown key inside `reassign_eligibility` MUST be refused as an unknown key.

#### Scenario: Round trip on a user task
```gherkin
Scenario Outline: Ownership keys survive encode and decode
  Given a <format> definition with a userTask node "approve" carrying <keys>
  When the definition is decoded and re-encoded
  Then the re-encoded node carries <keys> unchanged

  Examples:
    | format | keys                                                                       |
    | JSON   | claimant_only true                                                         |
    | YAML   | claimant_only true                                                         |
    | JSON   | claimant_only true and reassign_eligibility {roles: ["supervisor"]}       |
    | YAML   | claimant_only true and reassign_eligibility {privileges: ["task reassign"]} |
    | JSON   | claimant_only true and reassign_eligibility {expr: "actor.ID != \"\""}     |
```

#### Scenario: The default leaves the claim advisory
```gherkin
Scenario Outline: Absent or false claimant_only
  Given a <format> definition with a userTask node "approve" <declaration>
  When the definition is decoded
  Then the node's claimant-only rule is off
  And re-encoding writes no claimant_only key

  Examples:
    | format | declaration                     |
    | JSON   | without claimant_only           |
    | JSON   | with claimant_only false        |
    | YAML   | without claimant_only           |
    | YAML   | with claimant_only false        |
```

#### Scenario: Ownership keys on another kind are refused
```gherkin
Scenario Outline: A kind that never reads the keys
  Given a <format> definition with a "<kind>" node "n1" carrying <key>
  When the definition is decoded
  Then decoding fails with ErrKeyNotOnKind
  And the error names "<key name>", "<kind>" and "n1"

  Examples:
    | format | kind        | key                                              | key name             |
    | JSON   | serviceTask | claimant_only true                               | claimant_only        |
    | YAML   | serviceTask | claimant_only true                               | claimant_only        |
    | JSON   | manualTask  | reassign_eligibility {roles: ["supervisor"]}     | reassign_eligibility |
    | YAML   | endEvent    | reassign_eligibility {roles: ["supervisor"]}     | reassign_eligibility |
```

#### Scenario: Negative control - false on another kind is a scalar zero
```gherkin
Scenario: claimant_only false on a serviceTask
  Given a JSON definition with a "serviceTask" node carrying claimant_only false
  When the definition is decoded
  Then decoding succeeds
```

#### Scenario: Malformed ownership values are refused
```gherkin
Scenario Outline: Wrong types and unknown inner keys
  Given a <format> definition with a userTask node carrying <value>
  When the definition is decoded
  Then decoding fails with <error>

  Examples:
    | format | value                                              | error                                  |
    | JSON   | claimant_only "true"                               | a type error naming claimant_only      |
    | YAML   | claimant_only "yes"                                | a type error naming claimant_only      |
    | JSON   | reassign_eligibility {rolez: ["supervisor"]}       | the unknown-key refusal naming rolez   |
    | YAML   | reassign_eligibility {Roles: ["supervisor"]}       | the unknown-key refusal naming Roles   |
    | JSON   | reassign_eligibility ["supervisor"]                | a type error naming reassign_eligibility |
```
