package persistence_test

import (
	"github.com/kartaladev/wrkflw/internal/persistence/store"
	"github.com/kartaladev/wrkflw/service"
)

// The three NewXxxTimerStore constructors return the kernel.TimerStore
// INTERFACE, so a consumer reaching Stats/ListArmedPage — as those constructors'
// doc comments instruct — must type-assert to service.TimerAdmin. Nothing in
// production code pins that assertion, so a signature drift in TimerAdmin would
// degrade to a runtime ok == false and an admin route that silently fails to
// register, with no compile error anywhere (ADR-0159).
//
// This assertion IS the test: it breaks the build of the persistence test binary
// the moment *store.TimerStore stops satisfying the port. There is deliberately
// no wrapping TestXxx — a runtime nil check on a typed-nil interface is never
// true (staticcheck SA4023) and would assert nothing the compiler has not
// already proven.
//
// The guard lives in the external TEST package on purpose: asserting it in
// persistence.go would make the storage façade import the service layer, and the
// day anything in service's transitive closure needs persistence that becomes a
// hard import cycle. A test-only edge gives the same compile-time protection
// with no production dependency.
var _ service.TimerAdmin = (*store.TimerStore)(nil)
