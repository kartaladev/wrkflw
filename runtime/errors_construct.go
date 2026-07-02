package runtime

import "errors"

// ErrNilDependency is returned by runtime constructors when a required,
// non-nilable dependency (interface or pointer) is nil. Wrap it with the
// argument name via %w.
var ErrNilDependency = errors.New("workflow-runtime: nil required dependency")
