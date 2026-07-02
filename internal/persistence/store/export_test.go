// Package store exposes internal helpers that are needed only in tests.
// This file is compiled exclusively during test runs (package store, not store_test)
// so that black-box tests in conformance_test.go can call the unexported querier method.
package store

import (
	"context"
	"time"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// QuerierForTest exposes the internal querier(ctx) accessor for use by
// the package-level black-box conformance tests. It MUST NOT be called from
// non-test code.
func (s *Store) QuerierForTest(ctx context.Context) database.Querier {
	return s.querier(ctx)
}

// CapHistory exposes the unexported capHistory helper for black-box tests.
var CapHistory = capHistory

// MapConflictForTest exposes the unexported mapConflict method for black-box
// tests. It MUST NOT be called from non-test code.
func (s *Store) MapConflictForTest(err error) error { return s.mapConflict(err) }

// TimeArgForTest exposes the unexported timeArg helper for black-box tests.
func (s *Store) TimeArgForTest(t time.Time) any { return timeArg(s.dialect, t) }

// TimeArgForDialect exposes timeArg as a free function keyed on a Store's dialect,
// for use by black-box tests that do not hold a *Store (e.g. relay conformance helpers).
func TimeArgForDialect(s *Store, t time.Time) any { return timeArg(s.dialect, t) }

// WithRelayListenReady exposes the test-only withRelayListenReady relay option
// to black-box tests so they can synchronize on the listen loop's actual LISTEN
// establishment instead of sleeping.
func WithRelayListenReady(ch chan struct{}) RelayOption { return withRelayListenReady(ch) }
