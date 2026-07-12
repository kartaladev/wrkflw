package kernel

// InstrumentationScope is the OpenTelemetry instrumentation scope name shared by
// the runtime driver and its sibling components (the task service and the call
// notifier) so their metrics and spans aggregate under one scope in the backend.
//
// It intentionally holds the runtime module path rather than each component's
// own package path: keeping a single stable scope is the observability contract
// callers' dashboards and alerts depend on. Defining it once here (the leaf all
// those packages import) means a module rename touches exactly one line and the
// three call sites cannot silently drift (ADR-0087).
const InstrumentationScope = "github.com/kartaladev/wrkflw/runtime"
