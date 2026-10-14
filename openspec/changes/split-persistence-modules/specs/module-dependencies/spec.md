## Purpose

Defines what each wrkflw module brings into a consumer's build, so a consumer imports only what it uses: the core module carries no database driver, web framework or cache client, and each adapter module carries only its own.

## ADDED Requirements

### Requirement: Importing the core module builds no driver, framework or cache client
For every package of the root module that a consumer can import, its non-test transitive build MUST NOT contain any module of a database driver, a migration tool, an HTTP framework, or a remote cache client; those enter a consumer's build only through the adapter module the consumer imports.

#### Scenario: A denied module is absent from every importable root package's build
```gherkin
Scenario Outline: <module> stays out of the core
  Given every package of the root module whose import path has no "internal" element
  When the non-test transitive dependencies of each are listed
  Then no dependency equals or lies under "<module>"

  Examples:
    | module                            |
    | github.com/jackc/pgx              |
    | github.com/go-sql-driver/mysql    |
    | modernc.org/sqlite                |
    | github.com/pressly/goose          |
    | github.com/gin-gonic/gin          |
    | github.com/gofiber/fiber          |
    | github.com/valyala/fasthttp       |
    | github.com/quic-go/quic-go        |
    | go.mongodb.org/mongo-driver       |
    | github.com/redis/go-redis         |
    | github.com/bradfitz/gomemcache    |
    | github.com/testcontainers         |
```

#### Scenario: The package set is enumerated, not listed by hand
```gherkin
Given a package added to the root module with no "internal" element in its path
When the dependency check runs
Then that package is among the packages checked
```

#### Scenario: Negative control, the adapter module does carry its driver
```gherkin
Scenario Outline: <adapter> brings <module> into the build
  Given the package <adapter>
  When its non-test transitive dependencies are listed
  Then a dependency lies under "<module>"

  Examples:
    | adapter                                              | module                         |
    | github.com/kartaladev/wrkflw/persistence/pgx         | github.com/jackc/pgx           |
    | github.com/kartaladev/wrkflw/transport/http/gin      | github.com/gin-gonic/gin       |
    | github.com/kartaladev/wrkflw/transport/http/fiber    | github.com/gofiber/fiber       |
```

### Requirement: The database/sql path carries no pgx
The `persistence` and `persistence/sql` packages MUST NOT contain any `github.com/jackc/pgx` module in their non-test transitive build, so a consumer on `database/sql`, including one on Postgres through another driver, never compiles pgx.

#### Scenario: pgx is absent from the database/sql path
```gherkin
Scenario Outline: <package> without pgx
  Given the package <package>
  When its non-test transitive dependencies are listed
  Then no dependency lies under "github.com/jackc/pgx"

  Examples:
    | package                                       |
    | github.com/kartaladev/wrkflw/persistence      |
    | github.com/kartaladev/wrkflw/persistence/sql  |
```

#### Scenario: Negative control, the pgx adapter carries pgx
```gherkin
Given the package github.com/kartaladev/wrkflw/persistence/pgx
When its non-test transitive dependencies are listed
Then a dependency lies under "github.com/jackc/pgx"
```

### Requirement: The persistence module carries no remote cache client
The `persistence` package's non-test transitive build MUST NOT contain the Redis or memcache client modules; a consumer gets one only by importing that cache provider.

#### Scenario: Remote cache clients stay out of persistence
```gherkin
Scenario Outline: <module> absent from persistence
  Given the package github.com/kartaladev/wrkflw/persistence
  When its non-test transitive dependencies are listed
  Then no dependency lies under "<module>"

  Examples:
    | module                         |
    | github.com/redis/go-redis      |
    | github.com/bradfitz/gomemcache |
    | github.com/testcontainers      |
```

#### Scenario: Negative control, a provider carries its client
```gherkin
Scenario Outline: <provider> brings <module>
  Given <provider> package
  When its non-test transitive dependencies are listed
  Then a dependency lies under "<module>"

  Examples:
    | provider                  | module                         |
    | the Redis cache provider  | github.com/redis/go-redis      |
    | the memcache provider     | github.com/bradfitz/gomemcache |
```

### Requirement: Each HTTP binding carries only its own framework
Each HTTP framework binding MUST be importable without bringing another binding's framework into the build, and the standard-library binding MUST carry no third-party HTTP framework.

#### Scenario: A binding carries no other framework
```gherkin
Scenario Outline: <binding> without <foreign>
  Given the package <binding>
  When its non-test transitive dependencies are listed
  Then no dependency lies under "<foreign>"

  Examples:
    | binding                                            | foreign                     |
    | github.com/kartaladev/wrkflw/transport/http/stdlib | github.com/gin-gonic/gin    |
    | github.com/kartaladev/wrkflw/transport/http/stdlib | github.com/gofiber/fiber    |
    | github.com/kartaladev/wrkflw/transport/http/gin    | github.com/gofiber/fiber    |
    | github.com/kartaladev/wrkflw/transport/http/gin    | github.com/valyala/fasthttp |
    | github.com/kartaladev/wrkflw/transport/http/fiber  | github.com/gin-gonic/gin    |
    | github.com/kartaladev/wrkflw/transport/http/fiber  | github.com/quic-go/quic-go  |
```
