## Purpose

Defines how a consumer hands its database to wrkflw: a pgx pool or a `database/sql` handle becomes one opaque connection that every durable capability is built from, with the dialect stated explicitly on the `database/sql` path and driver errors classified the same way on every path.

## ADDED Requirements

### Requirement: A pgx pool and a database/sql handle each become an engine connection
`pgx.FromPool` SHALL turn a `*pgxpool.Pool`, and `sql.FromDB` a `*sql.DB` with a `persistence.Dialect`, into a `persistence.Conn` that every durable `persistence` constructor accepts; a nil pool or nil database handle MUST be refused before any connection is returned.

#### Scenario: Each driver path yields a working connection
```gherkin
Scenario Outline: <path> on <dialect>
  Given a migrated <dialect> database reached through <path>
  When an instance store is built from the resulting connection
  And instance "conn-1" is created and loaded
  Then Load returns InstanceID "conn-1" and version 1

  Examples:
    | path                    | dialect  |
    | pgx.FromPool            | Postgres |
    | sql.FromDB              | Postgres |
    | sql.FromDB              | MySQL    |
    | sql.FromDB              | SQLite   |
```

#### Scenario: A nil handle is refused
```gherkin
Scenario Outline: <call> with a nil handle
  When <call> is called with a nil handle
  Then it is refused with an error naming the nil handle
  And no connection is returned

  Examples:
    | call                                      |
    | pgx.FromPool                              |
    | sql.FromDB with persistence.SQLite        |
```

### Requirement: The database/sql dialect is stated, closed and never inferred
`sql.FromDB` SHALL accept exactly `persistence.Postgres`, `persistence.MySQL` and `persistence.SQLite`, MUST refuse any other `persistence.Dialect` value, including the zero value, and MUST NOT derive the dialect from the handle's driver.

#### Scenario: Dialect vocabulary
```gherkin
Scenario Outline: FromDB with dialect <dialect>
  Given an open *sql.DB
  When sql.FromDB is called with <dialect>
  Then the result is <result>

  Examples:
    | dialect                         | result                                          |
    | persistence.Postgres            | a connection                                    |
    | persistence.MySQL               | a connection                                    |
    | persistence.SQLite              | a connection                                    |
    | the zero persistence.Dialect    | refused with an error naming the dialect        |
    | a persistence.Dialect outside the three | refused with an error naming the dialect |
```

#### Scenario: A wrapped driver works when the dialect is given
```gherkin
Given a *sql.DB opened through a wrapping driver registered under a name wrkflw does not recognise, over SQLite
When sql.FromDB is called with persistence.SQLite
And an instance store built from the connection creates and loads instance "wrapped-1"
Then Load returns InstanceID "wrapped-1" and version 1
```

### Requirement: An error classifier decides errors the built-in classifiers do not recognise
`sql.FromDB` SHALL accept a `WithErrorClassifier` option whose classifier decides the class of a driver error that the built-in Postgres, MySQL and SQLite classifiers do not recognise; without one, such an error MUST reach the caller unchanged rather than be mapped to a store sentinel.

#### Scenario: Unrecognised unique-violation error
```gherkin
Scenario Outline: duplicate Create through a driver wrkflw does not know, <classifier>
  Given a SQLite database reached through a driver whose unique-violation error wrkflw does not recognise
  And a connection from sql.FromDB with persistence.SQLite <classifier>
  And an instance store from that connection holding instance "dup-1"
  When instance "dup-1" is created again
  Then <outcome>

  Examples:
    | classifier                                                  | outcome                                                          |
    | and a classifier that reports that error as a unique violation | Create fails with an error matching ErrInstanceExists            |
    | and no classifier                                           | Create fails with an error matching the driver's error under errors.Is, and not ErrInstanceExists |
```

#### Scenario: Negative control, a known driver needs no classifier
```gherkin
Given a SQLite database opened with the modernc driver
And a connection from sql.FromDB with persistence.SQLite and no classifier
And an instance store from that connection holding instance "dup-2"
When instance "dup-2" is created again
Then Create fails with an error matching ErrInstanceExists
```

### Requirement: Postgres over database/sql offers advisory-lock instance ownership
Advisory-lock instance ownership SHALL be available on a Postgres connection from `sql.FromDB` and behave as it does on `pgx.FromPool`: an instance owned by one ownership is reported as not owned by another until the first releases it or is closed.

#### Scenario: Ownership on each Postgres path
```gherkin
Scenario Outline: two ownerships over <path>
  Given two advisory-lock ownerships A and B on one Postgres database through <path>
  When A acquires "own-1"
  Then A reports "own-1" as owned
  And B reports "own-1" as not owned
  When A releases "own-1"
  Then B reports "own-1" as owned

  Examples:
    | path          |
    | pgx.FromPool  |
    | sql.FromDB    |
```

#### Scenario: Closing an ownership frees its instances
```gherkin
Given two advisory-lock ownerships A and B on one Postgres database through sql.FromDB
And A owns "own-2"
When A is closed
Then B reports "own-2" as owned
```
