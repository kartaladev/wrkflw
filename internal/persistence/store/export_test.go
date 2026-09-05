// Package store exposes internal helpers that are needed only in tests.
// This file is compiled exclusively during test runs (package store, not store_test)
// so that black-box tests in conformance_test.go can call the unexported querier method.
package store

import (
	"context"
	"time"

	"github.com/kartaladev/wrkflw/internal/database"
	"github.com/kartaladev/wrkflw/internal/persistence/dialect"
)

// QuerierForTest exposes the internal querier(ctx) accessor for use by
// the package-level black-box conformance tests. It MUST NOT be called from
// non-test code.
func (s *Store) QuerierForTest(ctx context.Context) database.Querier {
	return s.querier(ctx)
}

// CapHistory exposes the unexported capHistory helper for black-box tests.
var CapHistory = capHistory

// MarshalSnapshotForTest exposes the unexported marshalSnapshot helper — the
// exact encoder Create and Commit persist the snapshot column with — so the
// benchmarks measure the real per-step cost rather than a hand-copied
// json.Marshal(capHistory(...)) that would drift away from it.
// It MUST NOT be called from non-test code.
var MarshalSnapshotForTest = marshalSnapshot

// MapConflictForTest exposes the unexported mapConflict method for black-box
// tests. It MUST NOT be called from non-test code.
func (s *Store) MapConflictForTest(err error) error { return s.mapConflict(err) }

// TimeArgForTest exposes the unexported timeArg helper for black-box tests.
func (s *Store) TimeArgForTest(t time.Time) any { return timeArg(s.dialect, t) }

// TimeArgForDialect exposes timeArg as a free function keyed on a Store's dialect,
// for use by black-box tests that do not hold a *Store (e.g. relay conformance helpers).
func TimeArgForDialect(s *Store, t time.Time) any { return timeArg(s.dialect, t) }

// TimeArgForDialectValue exposes timeArg keyed directly on a dialect, so tests can
// assert the encoding contract without opening a database connection.
func TimeArgForDialectValue(d dialect.Dialect, t time.Time) any { return timeArg(d, t) }

// MySQLHashKeyForTest exposes the unexported mysqlHashKey helper so
// ownership_conformance_test.go can verify the 64-char SHA-256 key contract.
var MySQLHashKeyForTest = mysqlHashKey

// WithRelayListenReady exposes the test-only withRelayListenReady relay option
// to black-box tests so they can synchronize on the listen loop's actual LISTEN
// establishment instead of sleeping.
func WithRelayListenReady(ch chan struct{}) RelayOption { return withRelayListenReady(ch) }

// NotifyForTest returns the Store's internal notify field (a [dialect.Notifier])
// so black-box tests can assert that WithNotifier wires the value correctly.
// It MUST NOT be called from non-test code.
func (s *Store) NotifyForTest() dialect.Notifier { return s.notify }

// PgxNotifierReconnectBackoffForTest exposes the internal
// pgxNotifierReconnectBackoff constant so tests can calculate the minimum wait
// needed before asserting that a reconnect attempt has occurred.
const PgxNotifierReconnectBackoffForTest = pgxNotifierReconnectBackoff

// WaitBackoffForTest exposes the unexported pgxNotifier.waitBackoff method to
// black-box tests, given the [dialect.Notifier] returned by [NewPgxNotifier]
// (which is always backed by a *pgxNotifier). It MUST NOT be called from
// non-test code.
func WaitBackoffForTest(n dialect.Notifier, ctx context.Context) error {
	return n.(*pgxNotifier).waitBackoff(ctx)
}

// ListArmedPageSQLForTest exposes the statement and bind arguments
// [TimerStore.ListArmedPage] actually issues, so plan-shape tests can EXPLAIN
// the real query instead of a hand-copied duplicate that drifts from it.
// It MUST NOT be called from non-test code.
func (s *TimerStore) ListArmedPageSQLForTest(cursor string, fetch int) (string, []any, error) {
	sqlText, args, err := s.listArmedPageSQL(cursor, fetch)
	if err != nil {
		return "", nil, err
	}
	return s.dialect.Rebind(sqlText), args, nil
}
