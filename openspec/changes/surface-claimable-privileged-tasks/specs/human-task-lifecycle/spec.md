## ADDED Requirements

### Requirement: The claimable list honours eligibility privileges on every task store
(Pending Q3.) Listing the tasks claimable by an actor MUST include every unclaimed task whose eligibility privileges contain, by exact string comparison, one of the actor's granted privileges, alongside the existing candidate and role matches, identically on the in-memory, Postgres, MySQL and SQLite task stores.

#### Scenario: Privilege matching in the claimable list
```gherkin
Scenario Outline: Privileges-only task and the inbox
  Given the <store> task store holding an unclaimed task whose eligibility is privileges ["finance-task claim"] and whose candidates are empty
  When the tasks claimable by an actor granted <granted> with roles <roles> are listed
  Then the task is <listed>

  Examples:
    | store    | granted                            | roles                  | listed     |
    | memory   | ["finance-task claim"]             | []                     | listed     |
    | memory   | ["other", "finance-task claim"]    | []                     | listed     |
    | memory   | []                                 | []                     | not listed |
    | memory   | ["finance-task *"]                 | []                     | not listed |
    | memory   | ["Finance-Task claim"]             | []                     | not listed |
    | memory   | ["finance-task claim "]            | []                     | not listed |
    | memory   | ["finance-task claim"] spelled with U+2011 | []              | not listed |
    | memory   | []                                 | ["finance-task claim"] | not listed |
    | postgres | ["finance-task claim"]             | []                     | listed     |
    | postgres | ["finance-task *"]                 | []                     | not listed |
    | mysql    | ["finance-task claim"]             | []                     | listed     |
    | mysql    | ["Finance-Task claim"]             | []                     | not listed |
    | sqlite   | ["finance-task claim"]             | []                     | listed     |
    | sqlite   | []                                 | ["finance-task claim"] | not listed |
```

#### Scenario: Only unclaimed tasks are listed
```gherkin
Scenario Outline: Task state gates the listing
  Given the memory task store holding a <state> task whose eligibility is privileges ["finance-task claim"]
  When the tasks claimable by an actor granted ["finance-task claim"] are listed
  Then the task is <listed>

  Examples:
    | state     | listed     |
    | unclaimed | listed     |
    | claimed   | not listed |
    | completed | not listed |
    | cancelled | not listed |
```

#### Scenario: Negative control, role and candidate matches are unchanged
```gherkin
Scenario Outline: Existing eligibility routes still list
  Given the <store> task store holding an unclaimed task whose eligibility is <spec> and whose candidates are <candidates>
  When the tasks claimable by actor "alice" with role "manager" and no privileges are listed
  Then the task is listed

  Examples:
    | store  | spec            | candidates |
    | memory | role "manager"  | []         |
    | memory | empty           | ["alice"]  |
    | sqlite | role "manager"  | []         |
```

#### Scenario: The listing agrees with the default composite authorizer
```gherkin
Scenario Outline: Discovery and authorization agree on identity fields
  Given an unclaimed task with eligibility <spec>, no predicate and no candidates
  And an actor <actor>
  When the claimable tasks for the actor are listed and the default composite authorizer evaluates a claim
  Then the task is listed exactly when the claim is allowed

  Examples:
    | spec                              | actor                                 |
    | privileges ["finance-task claim"] | granted ["finance-task claim"]        |
    | privileges ["finance-task claim"] | granted ["finance-task *"]            |
    | role "manager"                    | with role "manager"                   |
    | role "manager"                    | with role "viewer"                    |
```

### Requirement: The static resolver returns actors granted a spec privilege
(Pending Q3.) `humantask.StaticActorResolver` SHALL return, as candidates for an eligibility spec, every mapped actor whose granted privileges contain one of the spec's privileges by exact string comparison, deduplicated by actor ID and sorted by ID, alongside the actors of the spec's roles.

#### Scenario: Privilege candidates from the static map
```gherkin
Scenario Outline: Candidates for a privileges-only spec
  Given a static resolver mapping role "fin" to actors "u2" granted ["finance-task claim"] and "u1" granted ["other"]
  When candidates are resolved for a spec with privileges <privileges>
  Then the candidate IDs are <ids>

  Examples:
    | privileges               | ids        |
    | ["finance-task claim"]   | ["u2"]     |
    | ["other"]                | ["u1"]     |
    | ["Finance-Task claim"]   | []         |
    | ["finance-task *"]       | []         |
    | []                       | []         |
```

#### Scenario: Negative control, role candidates are unchanged
```gherkin
Scenario: A roles-only spec still resolves by role
  Given a static resolver mapping role "fin" to actors "u2" and "u1"
  When candidates are resolved for a spec with roles ["fin"]
  Then the candidate IDs are ["u1", "u2"]
```
