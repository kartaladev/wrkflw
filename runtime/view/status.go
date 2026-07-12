package view

import "github.com/kartaladev/wrkflw/engine"

// StatusString converts an engine.Status to its canonical string representation
// ("running"/"completed"/"failed"/"compensating"/"terminated"; out-of-range →
// "unknown"). It delegates to engine.Status.String, the canonical source; it is
// retained as a named helper for the ActionableView DTO mapping and existing
// callers.
func StatusString(s engine.Status) string {
	return s.String()
}
