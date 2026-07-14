package runtime

import "github.com/kartaladev/wrkflw/definition/model"

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

// StartTimerFireFuncForTest exposes startTimerFireFunc to black-box tests in
// package runtime_test so they can invoke the timer-start fire callback directly
// and assert its admission behaviour during shutdown.
func (d *ProcessDriver) StartTimerFireFuncForTest(def *model.ProcessDefinition, nodeID, timerID string) func() {
	return d.startTimerFireFunc(def, nodeID, timerID)
}
