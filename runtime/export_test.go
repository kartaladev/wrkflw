package runtime

import "github.com/zakyalvan/krtlwrkflw/definition/model"

// ExportForceTerminationWarnings exposes forceTerminationWarnings to black-box
// tests in package runtime_test.
func ExportForceTerminationWarnings(d *model.ProcessDefinition) []string {
	return forceTerminationWarnings(d)
}
