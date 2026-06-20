// export_test.go exposes unexported methods on InstanceState for white-box
// testing from the engine_test package. This file is compiled only during
// `go test` (it belongs to package engine, not engine_test) and is therefore
// invisible to consumers of the library.
//
// Pattern: thin, named shim functions that forward to the unexported methods.
// No logic lives here — only delegation.
package engine

// OpenScope exposes (*InstanceState).openScope for engine_test.
func OpenScope(s *InstanceState, nodeID, parentScopeID string) string {
	return s.openScope(nodeID, parentScopeID)
}

// TokensInScope exposes (*InstanceState).tokensInScope for engine_test.
func TokensInScope(s *InstanceState, scopeID string) int {
	return s.tokensInScope(scopeID)
}

// CloseScope exposes (*InstanceState).closeScope for engine_test.
func CloseScope(s *InstanceState, scopeID string) {
	s.closeScope(scopeID)
}

// ScopeByID exposes (*InstanceState).scopeByID for engine_test.
func ScopeByID(s *InstanceState, id string) *Scope {
	return s.scopeByID(id)
}
