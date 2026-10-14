# Spec Delta

## Purpose

Lets a consumer whose data layer is GORM hand its `*gorm.DB` to wrkflw as an engine connection or a lent transaction on the `database/sql` path, so engine writes and the consumer's rows commit or roll back together without a separate GORM store.

## ADDED Requirements

### Requirement: Transaction commits the consumer's rows and the engine step together
`gormpersistence.Transaction` SHALL run the callback inside one GORM transaction whose context lends that transaction to the engine. When the callback returns nil and GORM commits, both the consumer's rows and the engine's step SHALL persist. When the callback returns an error, neither MUST persist and `Transaction` SHALL return that error unchanged (matching under `errors.Is`).

#### Scenario: Callback outcome decides both rows
```gherkin
Scenario Outline: consumer row and engine instance in one Transaction
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  And a definition start -> userTask(T) -> end
  When gormpersistence.Transaction runs a callback that inserts consumer row "<row>" through tx, starts instance "<id>" with the callback's context, and returns <result>
  Then Transaction returns <returned>
  And consumer row "<row>" <row_outcome>
  And GetInstance for "<id>" <instance_outcome>

  Examples:
    | dialect  | row   | id    | result   | returned               | row_outcome  | instance_outcome                      |
    | SQLite   | ok-s  | gi-1  | nil      | nil                    | exists       | returns status StatusRunning          |
    | Postgres | ok-p  | gi-2  | nil      | nil                    | exists       | returns status StatusRunning          |
    | MySQL    | ok-m  | gi-3  | nil      | nil                    | exists       | returns status StatusRunning          |
    | SQLite   | rb-s  | gi-4  | an error | that error (errors.Is) | is absent    | returns kernel.ErrInstanceNotFound    |
    | Postgres | rb-p  | gi-5  | an error | that error (errors.Is) | is absent    | returns kernel.ErrInstanceNotFound    |
    | MySQL    | rb-m  | gi-6  | an error | that error (errors.Is) | is absent    | returns kernel.ErrInstanceNotFound    |
```

#### Scenario: The engine's own failure inside the callback rolls back the consumer's row
```gherkin
Given a *gorm.DB over SQLite with the wrkflw schema migrated and instance "dup-1" already started
And a ProcessEngine over gormpersistence.From of that *gorm.DB
When gormpersistence.Transaction runs a callback that inserts consumer row "c-dup", starts instance "dup-1" again with the callback's context, and returns the error StartInstance returned
Then Transaction returns an error matching persistence.ErrInstanceExists
And consumer row "c-dup" is absent
```

#### Scenario: A panic in the callback rolls back both and re-panics
```gherkin
Scenario Outline: callback panics after the engine call
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB with a spy action catalog
  And a definition start -> serviceTask(A) -> end
  When gormpersistence.Transaction runs a callback that inserts consumer row "c-pan", starts instance "pan-1" with the callback's context, and then panics with value "boom"
  Then Transaction panics with value "boom"
  And consumer row "c-pan" is absent
  And GetInstance for "pan-1" returns kernel.ErrInstanceNotFound
  And action A has been called 0 times

  Examples:
    | dialect  |
    | SQLite   |
    | Postgres |
    | MySQL    |
```

### Requirement: Transaction runs the engine's deferred work only after GORM commits
`gormpersistence.Transaction` SHALL NOT let the engine perform an action, run a timer or apply a follow-up trigger before GORM has committed. After a successful commit it SHALL run the lent transaction's deferred work exactly once before returning. After a rollback it SHALL discard that work, so nothing deferred ever runs.

#### Scenario: Deferred work follows the commit and only the commit
```gherkin
Scenario Outline: service task after start under Transaction
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB with a spy action catalog
  And a definition start -> serviceTask(A) -> end
  When gormpersistence.Transaction runs a callback that starts instance "<id>" with the callback's context and returns <result>
  Then inside the callback, after StartInstance returned, action A had been called 0 times
  And after Transaction returns, action A has been called <calls> times
  And GetInstance for "<id>" <instance_outcome>

  Examples:
    | dialect  | id    | result   | calls | instance_outcome                    |
    | SQLite   | df-1  | nil      | 1     | returns status StatusCompleted      |
    | Postgres | df-2  | nil      | 1     | returns status StatusCompleted      |
    | MySQL    | df-3  | nil      | 1     | returns status StatusCompleted      |
    | SQLite   | df-4  | an error | 0     | returns kernel.ErrInstanceNotFound  |
    | Postgres | df-5  | an error | 0     | returns kernel.ErrInstanceNotFound  |
    | MySQL    | df-6  | an error | 0     | returns kernel.ErrInstanceNotFound  |
```

#### Scenario: Several engine calls in one Transaction resume in call order
```gherkin
Given a *gorm.DB over SQLite with the wrkflw schema migrated
And a ProcessEngine over gormpersistence.From of that *gorm.DB
And a definition start -> userTask(T) -> end
When gormpersistence.Transaction runs a callback that starts instance "two-g" and then completes T on "two-g", both with the callback's context, and returns nil
Then Transaction returns nil
And "two-g" has status StatusCompleted
And its journal holds the start step before the completion step
```

#### Scenario: Concurrent Transactions resume only their own work
```gherkin
Scenario Outline: two goroutines each run one Transaction on the same *gorm.DB
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB with a spy action catalog that records the instance of each call
  And a definition start -> serviceTask(A) -> end
  When one goroutine's Transaction starts "cc-a" and returns nil while another goroutine's Transaction starts "cc-b" and returns an error
  Then action A has been called exactly once, for "cc-a"
  And "cc-a" has status StatusCompleted
  And GetInstance for "cc-b" returns kernel.ErrInstanceNotFound

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
```

#### Scenario: A process that stops between GORM's commit and the deferred work leaves the step committed and unperformed
```gherkin
Given a *gorm.DB over SQLite with the wrkflw schema migrated
And a ProcessEngine over gormpersistence.From of that *gorm.DB with a spy action catalog
And a definition start -> serviceTask(A) -> end
When gormpersistence.Transaction runs a callback that starts instance "cr-1" and returns nil
And the process exits after GORM's commit and before Transaction runs the deferred work
And a new ProcessEngine is opened over the same database
Then GetInstance for "cr-1" returns status StatusRunning with one token on A
And action A has been called 0 times
```

#### Scenario: Transaction returns every connection it borrowed
```gherkin
Scenario Outline: pool after Transaction ends
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated and an idle connection pool
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  When gormpersistence.Transaction runs a callback that starts an instance and then <ending>
  Then the underlying *sql.DB reports 0 connections in use

  Examples:
    | dialect  | ending           |
    | SQLite   | returns nil      |
    | SQLite   | returns an error |
    | SQLite   | panics           |
    | Postgres | returns nil      |
    | MySQL    | returns an error |
```

### Requirement: Engine savepoints coexist with GORM's nested transactions
An engine call made inside a GORM nested transaction (a savepoint GORM opens within the lent transaction) SHALL leave GORM's savepoint usable: when the nested transaction commits, the engine's step SHALL persist with the outer commit, and when GORM rolls the nested transaction back, the engine's step MUST NOT persist while the consumer's rows written outside it still do.

#### Scenario: Engine call inside a GORM nested transaction
```gherkin
Scenario Outline: tx.Transaction nested inside gormpersistence.Transaction
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  When gormpersistence.Transaction runs a callback that inserts consumer row "outer-<id>", then runs tx.Transaction with an inner callback that starts instance "<id>" with the outer callback's context and returns <inner_result>, and the outer callback returns nil
  Then Transaction returns nil
  And consumer row "outer-<id>" exists
  And the database holds <instance_rows> instance row for "<id>"

  Examples:
    | dialect  | id     | inner_result | instance_rows |
    | SQLite   | nest-1 | nil          | one           |
    | Postgres | nest-2 | nil          | one           |
    | MySQL    | nest-3 | nil          | one           |
    | SQLite   | nest-4 | an error     | no            |
    | Postgres | nest-5 | an error     | no            |
    | MySQL    | nest-6 | an error     | no            |
```

#### Scenario: FromTx accepts the transaction GORM hands a nested callback
```gherkin
Scenario Outline: lending a nested GORM transaction by hand
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a transaction begun with db.Begin()
  When gormpersistence.FromTx is called on the tx a nested tx.Transaction callback receives
  Then FromTx returns a persistence.Tx and a nil error

  Examples:
    | dialect  |
    | SQLite   |
    | Postgres |
    | MySQL    |
```

### Requirement: From resolves the dialect from the GORM dialector and refuses any other
`gormpersistence.From` SHALL return a `persistence.Conn` over the `*gorm.DB`'s underlying `*sql.DB` with the dialect given by its dialector's name, exactly `postgres`, `mysql` or `sqlite`. Any other name SHALL be refused with an error naming that dialector name, and a nil `*gorm.DB` SHALL be refused with `persistence.ErrNilDependency`.

#### Scenario: Dialector names
```gherkin
Scenario Outline: From on a *gorm.DB whose dialector reports a name
  Given a *gorm.DB whose dialector's Name() returns "<name>"
  When gormpersistence.From is called on it
  Then From returns <outcome>

  Examples:
    | name        | outcome                                                     |
    | postgres    | a persistence.Conn and a nil error                          |
    | mysql       | a persistence.Conn and a nil error                          |
    | sqlite      | a persistence.Conn and a nil error                          |
    | sqlserver   | a nil Conn and an error naming "sqlserver"                  |
    |             | a nil Conn and an error naming the empty dialector name     |
    | Postgres    | a nil Conn and an error naming "Postgres"                   |
    | MYSQL       | a nil Conn and an error naming "MYSQL"                      |
    |  sqlite     | a nil Conn and an error naming " sqlite"                    |
    | sqlite3     | a nil Conn and an error naming "sqlite3"                    |
    | pоstgres    | a nil Conn and an error naming "pоstgres" (Cyrillic о)      |
```

#### Scenario: A nil *gorm.DB is refused
```gherkin
When gormpersistence.From is called with a nil *gorm.DB
Then it returns a nil Conn and an error matching persistence.ErrNilDependency
```

#### Scenario: An engine over From round-trips an instance on each dialect
```gherkin
Scenario Outline: ProcessEngine over a GORM-backed Conn
  Given a *gorm.DB over <dialect> opened with GORM's own driver for that dialect, with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  When instance "rt-<dialect>" is started on a definition start -> userTask(T) -> end and T is completed
  Then GetInstance for "rt-<dialect>" returns status StatusCompleted
  And its journal holds the start step before the completion step

  Examples:
    | dialect  |
    | SQLite   |
    | Postgres |
    | MySQL    |
```

### Requirement: From refuses a *gorm.DB that is inside a transaction
`gormpersistence.From` SHALL refuse a `*gorm.DB` that is bound to an open transaction with an error naming `FromTx`, and MUST NOT return a connection to the transaction's parent pool, so an engine built from it never writes outside the consumer's transaction without the consumer knowing.

#### Scenario: Transaction-bound handles are refused and plain ones accepted
```gherkin
Scenario Outline: From on handles in and out of a transaction
  Given a *gorm.DB "db" over <dialect>
  When gormpersistence.From is called on <handle>
  Then From returns <outcome>

  Examples:
    | dialect  | handle                                             | outcome                                |
    | SQLite   | db.Begin()                                         | a nil Conn and an error naming FromTx  |
    | Postgres | db.Begin()                                         | a nil Conn and an error naming FromTx  |
    | MySQL    | db.Begin()                                         | a nil Conn and an error naming FromTx  |
    | SQLite   | the tx a db.Transaction callback receives          | a nil Conn and an error naming FromTx  |
    | SQLite   | db                                                 | a persistence.Conn and a nil error     |
    | SQLite   | db.WithContext(ctx)                                | a persistence.Conn and a nil error     |
    | SQLite   | db after a db.Begin() on it was committed           | a persistence.Conn and a nil error     |
```

### Requirement: FromTx accepts only a *gorm.DB bound to a database/sql transaction
`gormpersistence.FromTx` SHALL return a `persistence.Tx` for a `*gorm.DB` bound to an open `database/sql` transaction, with the dialect resolved as `From` resolves it. A `*gorm.DB` not bound to a transaction SHALL be refused with an error naming the missing transaction, and a nil one with `persistence.ErrNilDependency`.

#### Scenario: FromTx on handles in and out of a transaction
```gherkin
Scenario Outline: FromTx inputs
  Given a *gorm.DB "db" over SQLite opened without PrepareStmt
  When gormpersistence.FromTx is called on <handle>
  Then FromTx returns <outcome>

  Examples:
    | handle                                    | outcome                                                    |
    | db.Begin()                                | a persistence.Tx and a nil error                           |
    | the tx a db.Transaction callback receives | a persistence.Tx and a nil error                           |
    | db                                        | a nil Tx and an error naming the missing transaction       |
    | db.WithContext(ctx)                       | a nil Tx and an error naming the missing transaction       |
    | a nil *gorm.DB                            | a nil Tx and an error matching persistence.ErrNilDependency |
```

#### Scenario: FromTx refuses an unsupported dialector as From does
```gherkin
Given a transaction begun with db.Begin() on a *gorm.DB whose dialector's Name() returns "sqlserver"
When gormpersistence.FromTx is called on it
Then it returns a nil Tx and an error naming "sqlserver"
```

#### Scenario: A hand-managed lend through FromTx follows the consumer's outcome
```gherkin
Scenario Outline: db.Begin, FromTx, WithTx, then the consumer ends the transaction
  Given a *gorm.DB over <dialect> with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  And a transaction "tx" begun with db.Begin() and lent through persistence.WithTx of gormpersistence.FromTx(tx)
  When instance "<id>" is started with the lent context and the consumer <ending>
  Then GetInstance for "<id>" <instance_outcome>

  Examples:
    | dialect  | id    | ending                                 | instance_outcome                    |
    | SQLite   | hm-1  | commits tx and calls Resume            | succeeds                            |
    | Postgres | hm-2  | commits tx and calls Resume            | succeeds                            |
    | MySQL    | hm-3  | rolls back tx and calls Discard        | returns kernel.ErrInstanceNotFound  |
```

### Requirement: Migrations through a GORM connection use the goose set
The engine schema for a GORM-backed connection SHALL be created and upgraded only by `persistence.NewMigrator` over the `persistence.Conn` that `gormpersistence.From` returns, and SHALL be the same migration set at the same versions as for a plain `database/sql` connection. No `gormpersistence` function SHALL create, alter or drop an engine table.

#### Scenario: Migrator over From matches the plain migrator
```gherkin
Scenario Outline: schema through a GORM connection
  Given an empty <dialect> database opened through GORM
  When persistence.NewMigrator over gormpersistence.From of it runs Up twice
  Then both runs return nil
  And Version returns the same version a migrator over a plain database/sql connection reaches on an empty database
  And Status lists the same migrations, all applied

  Examples:
    | dialect  |
    | SQLite   |
    | Postgres |
    | MySQL    |
```

#### Scenario: The adapter creates no table by itself
```gherkin
Given an empty SQLite database opened through GORM
When gormpersistence.From, gormpersistence.FromTx on a db.Begin() transaction, and gormpersistence.Transaction with a callback that returns nil are each called on it
Then the database holds no table whose name starts with "wrkflw_" and no goose version table
```

### Requirement: SQLite errors from GORM's driver are classified by SQLite's message text
For the `sqlite` dialect, a connection or transaction from `gormpersistence.From` or `FromTx` SHALL classify a driver error whose text contains SQLite's `UNIQUE constraint failed` as a unique violation and one containing `database is locked` as a retryable conflict, without importing a SQLite driver, so the engine's sentinels are the same as on the built-in SQLite store.

#### Scenario: Engine sentinels under GORM's SQLite driver
```gherkin
Given a *gorm.DB over SQLite opened with gorm.io/driver/sqlite, with the wrkflw schema migrated
And a ProcessEngine over gormpersistence.From of that *gorm.DB
When instance "cls-1" is started twice
Then the second StartInstance returns an error matching persistence.ErrInstanceExists
```

#### Scenario: A real busy database maps to the retryable conflict
```gherkin
Given a SQLite database file opened through gorm.io/driver/sqlite with busy timeout 0
And a second connection holding a write lock on that file
And an instance store over gormpersistence.From of the first *gorm.DB
When the store commits a step for an existing instance
Then the store returns an error matching kernel.ErrConcurrentUpdate
When the second connection releases its lock and the step is committed again
Then the commit returns nil
```

#### Scenario: The classifier's vocabulary
```gherkin
Scenario Outline: classifying one error
  Given the SQLite classifier exported by gormpersistence
  When it classifies an error whose text is "<text>" <wrapping>
  Then it reports unique violation <unique> and retryable conflict <retryable>

  Examples:
    | text                                                    | wrapping                    | unique | retryable |
    | UNIQUE constraint failed: wrkflw_instances.instance_id  | as is                       | true   | false     |
    | UNIQUE constraint failed: wrkflw_instances.instance_id  | wrapped with fmt.Errorf %w  | true   | false     |
    | database is locked                                      | as is                       | false  | true      |
    | database is locked                                      | wrapped with fmt.Errorf %w  | false  | true      |
    | NOT NULL constraint failed: wrkflw_instances.status     | as is                       | false  | false     |
    | FOREIGN KEY constraint failed                           | as is                       | false  | false     |
    | CHECK constraint failed: status                         | as is                       | false  | false     |
    | no such table: wrkflw_instances                         | as is                       | false  | false     |
    | unique constraint failed: wrkflw_instances.instance_id  | as is                       | false  | false     |
    | Database Is Locked                                      | as is                       | false  | false     |
    |                                                         | as is                       | false  | false     |
```

#### Scenario: A nil error is classified as neither
```gherkin
Given the SQLite classifier exported by gormpersistence
When it classifies a nil error
Then it reports unique violation false and retryable conflict false
```

#### Scenario: Postgres and MySQL under GORM keep their own classification
```gherkin
Scenario Outline: duplicate start on a GORM-backed Postgres or MySQL engine
  Given a *gorm.DB over <dialect> opened with GORM's own driver, with the wrkflw schema migrated
  And a ProcessEngine over gormpersistence.From of that *gorm.DB
  When instance "dupc-<dialect>" is started twice
  Then the second StartInstance returns an error matching persistence.ErrInstanceExists

  Examples:
    | dialect  |
    | Postgres |
    | MySQL    |
```

### Requirement: The GORM adapter module pulls in no database driver
The `persistence/gorm` module's non-test import graph SHALL contain `gorm.io/gorm` and MUST NOT contain any `gorm.io/driver/*` package, any SQL driver package, or any package that requires cgo, and no other wrkflw module MUST import `persistence/gorm`.

#### Scenario: Forbidden packages in the adapter's build graph
```gherkin
Scenario Outline: non-test dependency closure of persistence/gorm
  Given the non-test package dependency closure of the persistence/gorm module
  Then it contains no package whose import path starts with "<prefix>"

  Examples:
    | prefix                         |
    | gorm.io/driver/                |
    | github.com/mattn/go-sqlite3    |
    | modernc.org/sqlite             |
    | github.com/jackc/pgx           |
    | github.com/go-sql-driver/mysql |
    | github.com/lib/pq              |
```

#### Scenario: Negative control, the guard sees GORM itself
```gherkin
Given the non-test package dependency closure of the persistence/gorm module
Then it contains gorm.io/gorm
And building it with CGO_ENABLED=0 succeeds
```

#### Scenario: A consumer that does not import the adapter carries no GORM
```gherkin
Scenario Outline: non-test dependency closure of other wrkflw modules
  Given the non-test package dependency closure of the <module> module
  Then it contains no package whose import path starts with "gorm.io/"

  Examples:
    | module          |
    | root            |
    | persistence     |
    | persistence/sql |
    | persistence/pgx |
```
