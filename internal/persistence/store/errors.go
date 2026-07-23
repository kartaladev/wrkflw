package store

import (
	"errors"
	"reflect"
)

// ErrNilDependency is returned by store constructors when conn or dialect is nil.
var ErrNilDependency = errors.New("workflow-store: nil required dependency")

// ErrTxRolledBack is returned by Store.RunInTx when a joined participant
// marked the shared transaction rollback-only (ADR-0134) — success must mean
// COMMITTED, so RunInTx surfaces this instead of the nil the owner's honoring
// Commit would otherwise return.
var ErrTxRolledBack = errors.New("workflow-store: run in tx: rolled back by participant")

// isNilDep reports whether v is a nil dependency. It catches both an untyped nil
// (v == nil) and a typed nil boxed in an interface — e.g. a nil *sql.DB or
// *pgxpool.Pool passed as the `conn any` parameter, which a plain `v == nil`
// comparison does NOT detect. Non-nilable kinds (structs, etc.) are never nil.
func isNilDep(v any) bool {
	if v == nil {
		return true
	}
	switch rv := reflect.ValueOf(v); rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
