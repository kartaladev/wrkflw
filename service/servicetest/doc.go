// Package servicetest holds the generated gomock doubles for the admin ports
// declared in [github.com/kartaladev/wrkflw/service].
//
// It exists so those doubles are not part of the service package itself. They
// were generated into `service` as ordinary non-test files, which put 22
// exported Mock* types on a public package's API and pulled go.uber.org/mock
// into the dependency graph of every consumer binary — none of which has any
// use for a test double.
//
// The doubles could not simply be renamed to _test.go: their consumers are the
// transport adapters' tests (transport/http/httpcore, .../stdlib, .../fiber and
// .../gin), and a _test.go file is not importable from another package. A
// sibling package is what makes them shareable and simultaneously keeps them
// out of anything that does not ask for them — importing servicetest is opt-in,
// and only test binaries do it.
//
// ⚠ This deliberately departs from the repo's default, which is to generate a
// mock beside the interface it mocks, in the same package. That default earns
// its keep when the mock's consumers are the owning package's own black-box
// tests, which pick it up from an import they already have. Here every consumer
// is a different package, so the co-location buys nothing while the exported
// surface costs something real. Mocks for interfaces whose consumers ARE the
// owning package's tests should still be generated in place.
//
// The //go:generate directives live on the interface files in service, not
// here, so regenerating stays a single `go generate ./...` from the module root
// and the directive sits next to the thing that makes it stale.
package servicetest
