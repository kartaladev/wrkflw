package runtime

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// ExportForceTerminationWarnings exposes forceTerminationWarnings to black-box
// tests in package runtime_test.
func ExportForceTerminationWarnings(d *model.ProcessDefinition) []string {
	return forceTerminationWarnings(d)
}

// ExportMixedSubprocessStartWarnings exposes mixedSubprocessStartWarnings to
// black-box tests in package runtime_test.
func ExportMixedSubprocessStartWarnings(d *model.ProcessDefinition) []string {
	return mixedSubprocessStartWarnings(d)
}
