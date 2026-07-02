package database

import "errors"

// ErrUnsupportedConn is returned by From/BeginTx for a connection handle whose
// concrete type is not one of the supported driver types.
var ErrUnsupportedConn = errors.New("workflow-database: unsupported connection type")
