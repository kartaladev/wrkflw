package dbtest

// EnvDSNForTest exposes envDSN to the black-box test package. envDSN decides
// whether a test binary uses an already-running server or boots a container, so
// the decision is worth asserting directly rather than only through a helper that
// needs a live database.
var EnvDSNForTest = envDSN

// NextTestDBNameForTest exposes nextTestDBName to the black-box test package.
// The name it returns is the only thing keeping two concurrent `go test` binaries
// off each other's databases on a shared server, so it is asserted directly
// rather than only through a helper that needs a live database.
var NextTestDBNameForTest = nextTestDBName

// OwnedTestDBNameForTest exposes ownedTestDBName, the guard both DROP DATABASE
// cleanups go through, to the black-box test package.
var OwnedTestDBNameForTest = ownedTestDBName

// ProcessTagForTest exposes this process's tag so tests can construct the name
// ANOTHER process would have created.
var ProcessTagForTest = processTag
