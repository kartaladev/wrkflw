// Package store exposes internal helpers that are needed only in tests.
// This file is compiled exclusively during test runs (package store, not store_test)
// so that black-box tests in conformance_test.go can call the unexported querier method.
package store

import (
	"context"

	"github.com/zakyalvan/krtlwrkflw/internal/database"
)

// QuerierForTest exposes the internal querier(ctx) accessor for use by
// the package-level black-box conformance tests. It MUST NOT be called from
// non-test code.
func (s *Store) QuerierForTest(ctx context.Context) database.Querier {
	return s.querier(ctx)
}
