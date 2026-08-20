package store_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/dbtest"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// conflictDialect wraps a real [dialect.Dialect] and forces IsRetryableConflict
// to a fixed answer, so a test can make ANY driver error the store surfaces look
// like a transient serialization conflict without having to reproduce a real
// one. Reproducing genuine contention (SQLITE_BUSY, a Postgres 40001, a MySQL
// 1213 deadlock) is inherently racy; forcing the classifier is deterministic and
// exercises exactly the branch under test — whether the error reaches
// mapConflict at all.
//
// notifyStatement, when non-empty, overrides the wrapped dialect's NOTIFY
// statement so the maybeNotify path can be made to fail on demand.
type conflictDialect struct {
	dialect.Dialect
	retryable       bool
	notifyStatement string
}

func (d conflictDialect) IsRetryableConflict(err error) bool {
	return d.retryable && err != nil
}

func (d conflictDialect) NotifyStatement(channel string) string {
	if d.notifyStatement != "" {
		return d.notifyStatement
	}
	return d.Dialect.NotifyStatement(channel)
}

// --- a driver whose Result.RowsAffected always fails -----------------------
//
// Commit's `res.RowsAffected()` error branch cannot be provoked through a real
// SQLite/Postgres/MySQL driver — they all compute the count locally and return a
// nil error. A minimal fake driver is the only way to reach that branch, and the
// branch is a genuine driver call, so it belongs in the mapped class.

var errRowsAffectedFailed = errors.New("driver: rows affected unavailable")

type badRowsDriver struct{}

func (badRowsDriver) Open(string) (driver.Conn, error) { return badRowsConn{}, nil }

type badRowsConn struct{}

func (badRowsConn) Prepare(string) (driver.Stmt, error) { return badRowsStmt{}, nil }
func (badRowsConn) Close() error                        { return nil }
func (badRowsConn) Begin() (driver.Tx, error)           { return badRowsTx{}, nil }

type badRowsTx struct{}

func (badRowsTx) Commit() error   { return nil }
func (badRowsTx) Rollback() error { return nil }

type badRowsStmt struct{}

func (badRowsStmt) Close() error  { return nil }
func (badRowsStmt) NumInput() int { return -1 }
func (badRowsStmt) Exec([]driver.Value) (driver.Result, error) {
	return badRowsResult{}, nil
}
func (badRowsStmt) Query([]driver.Value) (driver.Rows, error) { return nil, io.EOF }

type badRowsResult struct{}

func (badRowsResult) LastInsertId() (int64, error) { return 0, errRowsAffectedFailed }
func (badRowsResult) RowsAffected() (int64, error) { return 0, errRowsAffectedFailed }

func init() { sql.Register("wrkflw_badrows", badRowsDriver{}) }

// TestStoreMapsDriverErrorsToConcurrentUpdate is the regression test for audit
// item 118: the same transient driver failure must reach callers under ONE
// identity, kernel.ErrConcurrentUpdate, from every DB-touching error path in
// Store.Create and Store.Commit.
//
// Before the fix, five such paths returned the raw wrapped driver error, so a
// consumer retrying on the documented sentinel simply missed them.
//
// This is dialect-neutral by construction: the branches under test sit ABOVE the
// dialect seam, and IsRetryableConflict is what recognises a Postgres
// serialization failure (40001) and a MySQL deadlock (1213) as well as
// SQLITE_BUSY. The cases run on SQLite because it needs no container; the
// mapping code they exercise is shared by all three dialects and is not
// re-verified per dialect here.
func TestStoreMapsDriverErrorsToConcurrentUpdate(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name string
		// run performs its own setup, breaks whatever it needs to break, and
		// returns the error the store surfaced.
		run    func(t *testing.T) error
		assert func(t *testing.T, err error)
	}

	// wantConcurrentUpdate is the assertion shared by every mapped-path case.
	wantConcurrentUpdate := func(t *testing.T, err error) {
		require.Error(t, err, "the broken path must return an error")
		assert.ErrorIs(t, err, kernel.ErrConcurrentUpdate,
			"a retryable driver conflict must reach the caller as ErrConcurrentUpdate, got: %v", err)
	}

	cases := []testCase{
		{
			name: "Create: instance INSERT fallthrough",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: true})
				// Drop the target table so the INSERT fails at the driver.
				_, err := db.Exec(`DROP TABLE wrkflw_instances`)
				require.NoError(t, err)
				_, err = s.Create(t.Context(), newTestInstance(t, "cm-insert"))
				return err
			},
			assert: wantConcurrentUpdate,
		},
		{
			name: "Create: begin fallthrough",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: true})
				require.NoError(t, db.Close()) // begin now fails
				_, err := s.Create(t.Context(), newTestInstance(t, "cm-begin"))
				return err
			},
			assert: wantConcurrentUpdate,
		},
		{
			name: "Create: maybeNotify fallthrough",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				// A NOTIFY statement that is not valid SQL: the Exec inside
				// maybeNotify fails after the journal and outbox rows are written.
				d := conflictDialect{
					Dialect:         dialect.NewSQLite(),
					retryable:       true,
					notifyStatement: `THIS IS NOT SQL`,
				}
				s := newConflictStore(t, db, d, store.WithOutboxNotify())
				_, err := s.Create(t.Context(), newTestInstance(t, "cm-notify"))
				return err
			},
			assert: wantConcurrentUpdate,
		},
		{
			name: "Commit: begin fallthrough",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: true})
				require.NoError(t, db.Close())
				_, err := s.Commit(t.Context(), 1, newTestInstance(t, "cm-commit-begin"))
				return err
			},
			assert: wantConcurrentUpdate,
		},
		{
			name: "Commit: RowsAffected fallthrough",
			run: func(t *testing.T) error {
				db, err := sql.Open("wrkflw_badrows", "ignored")
				require.NoError(t, err)
				t.Cleanup(func() { _ = db.Close() })
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: true})
				_, err = s.Commit(t.Context(), 1, newTestInstance(t, "cm-rows"))
				return err
			},
			assert: wantConcurrentUpdate,
		},
		{
			// Control: without this case the fix could "pass" by mapping
			// everything to ErrConcurrentUpdate unconditionally.
			name: "control: a non-retryable error passes through unchanged",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: false})
				_, err := db.Exec(`DROP TABLE wrkflw_instances`)
				require.NoError(t, err)
				_, err = s.Create(t.Context(), newTestInstance(t, "cm-passthrough"))
				return err
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.NotErrorIs(t, err, kernel.ErrConcurrentUpdate,
					"a non-retryable error must NOT be reclassified as contention")
				assert.Contains(t, err.Error(), "create: insert instance",
					"the original context must survive: %v", err)
			},
		},
		{
			// Control: IsUniqueViolation must stay checked BEFORE mapConflict, or
			// a genuine duplicate instance degrades into "retry forever".
			name: "control: a duplicate instance still maps to ErrInstanceExists",
			run: func(t *testing.T) error {
				db := dbtest.RunTestSQLite(t)
				// retryable:true is the hostile setting — if the fix mapped the
				// unique violation, this case would return ErrConcurrentUpdate.
				s := newConflictStore(t, db, conflictDialect{Dialect: dialect.NewSQLite(), retryable: true})
				_, err := s.Create(t.Context(), newTestInstance(t, "cm-dup"))
				require.NoError(t, err, "first Create must succeed")
				_, err = s.Create(t.Context(), newTestInstance(t, "cm-dup"))
				return err
			},
			assert: func(t *testing.T, err error) {
				require.Error(t, err)
				assert.ErrorIs(t, err, kernel.ErrInstanceExists,
					"a duplicate instance must stay ErrInstanceExists, got: %v", err)
				assert.NotErrorIs(t, err, kernel.ErrConcurrentUpdate,
					"a duplicate instance must NOT be reclassified as retryable contention")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.assert(t, tc.run(t))
		})
	}
}

// newConflictStore builds a Store over conn with the supplied dialect, failing
// the test if construction errors.
func newConflictStore(t *testing.T, conn any, d dialect.Dialect, opts ...store.Option) *store.Store {
	t.Helper()
	s, err := store.New(conn, d, opts...)
	require.NoError(t, err, "store.New")
	return s
}
