package store

import "errors"

// ErrNilDependency is returned by store constructors when conn or dialect is nil.
var ErrNilDependency = errors.New("workflow-store: nil required dependency")
