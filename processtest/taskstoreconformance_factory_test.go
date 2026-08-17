package processtest_test

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/humantask"
	"github.com/kartaladev/wrkflw/processtest"
)

const (
	// factoryFatalEnv arms [TestConformanceFactoryFatalHelper]. That test FAILS by
	// design, so it must stay skipped in an ordinary run and be selected only by
	// the child `go test` invocation below.
	factoryFatalEnv = "WRKFLW_PROCESSTEST_FACTORY_FATAL"
	// factoryFatalMarker is the message the factory reports. The assertions below
	// are about WHERE it surfaces, so it has to be unmistakable in the output.
	factoryFatalMarker = "wrkflw-factory-setup-exploded"
	// factoryFatalHelper is the name of the helper test, used both to select it in
	// the child process and to attribute output lines to it.
	factoryFatalHelper = "TestConformanceFactoryFatalHelper"
)

// TestConformanceFactoryFatalHelper is the fixture, not an assertion: it hands
// [processtest.RunTaskStoreConformance] a factory that cannot provision its store
// and reports that with t.Fatalf — the failure mode a consumer hits when
// `newTestDB(t)` cannot reach its database. It fails on purpose, so it runs only
// in the child process spawned by the test below.
func TestConformanceFactoryFatalHelper(t *testing.T) {
	if os.Getenv(factoryFatalEnv) != "1" {
		t.Skipf("armed only by the child process of TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest (%s=1)", factoryFatalEnv)
	}

	processtest.RunTaskStoreConformance(t, func(t *testing.T) humantask.TaskStore {
		t.Fatalf("%s: this factory could not provision its store", factoryFatalMarker)
		return nil
	})
}

// TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest pins the
// signature contract of the factory: it receives the SUBTEST's *testing.T.
//
// The consumer pattern the doc comment shows is `mystore.New(newTestDB(t))`, and
// a helper like dbtest's fatals on a provisioning error. If the factory took no
// *testing.T, that closure would capture the PARENT T and call FailNow on it from
// the subtest's goroutine — unsupported by the testing package. Measured under
// the parameterless signature (Go 1.25):
//
//	=== NAME  TestX                              <- the message is re-attributed
//	    x_test.go:25: wrkflw-factory-setup-exploded   to the PARENT
//	=== NAME  TestX/case_one
//	    testing.go:1913: test executed panic(nil) or runtime.Goexit: subtest
//	                     may have called FailNow on a parent test
//
// …and the run stops at the first case, so the remaining shapes never report.
// The observable consequences are asserted below rather than the signature
// itself, which the compiler already pins.
//
// The child is a real `go test` run because a cross-goroutine FailNow cannot be
// observed in-process without failing this suite; `go` is already invoked from
// tests elsewhere in this module (service/vendorfree_test.go).
func TestRunTaskStoreConformanceAttributesAFactoryFailureToItsSubtest(t *testing.T) {
	if os.Getenv(factoryFatalEnv) == "1" {
		t.Skip("already inside the child process; running this again would spawn another")
	}
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "test", "-count=1", "-v", "-run", "^"+factoryFatalHelper+"$", ".")
	cmd.Env = append(os.Environ(), factoryFatalEnv+"=1")
	out, err := cmd.CombinedOutput()
	output := string(out)

	require.Error(t, err, "the helper test must FAIL — its factory fatals on every case:\n%s", output)
	require.NotContains(t, output, "no tests to run",
		"the -run filter selected nothing, so this test proves nothing:\n%s", output)
	require.NotContains(t, output, "SKIP",
		"the helper must be armed by %s, not skipped:\n%s", factoryFatalEnv, output)

	assert.Containsf(t, output, factoryFatalMarker,
		"the factory's own message must reach the output:\n%s", output)
	assert.NotContainsf(t, output, "runtime.Goexit",
		"a factory holding the SUBTEST's T calls FailNow on its own goroutine; the Goexit diagnostic means it held the parent's:\n%s", output)
	assert.Truef(t, strings.HasPrefix(attributedTest(output, factoryFatalMarker), factoryFatalHelper+"/"),
		"the factory's message must be attributed to the failing SUBTEST, not to %q:\n%s",
		attributedTest(output, factoryFatalMarker), output)
	assert.GreaterOrEqualf(t, failedSubtests(output), 2,
		"every case must still get its turn: a FailNow on the parent aborts the whole suite at the first one:\n%s", output)
}

// attributedTest returns the test name `go test -v` output attributes the first
// line containing marker to. Verbose output names the test whose buffer it is
// flushing with `=== RUN` / `=== CONT` / `=== NAME` lines; the most recent one
// before the marker owns it.
func attributedTest(output, marker string) string {
	current := ""
	for line := range strings.Lines(output) {
		if name, ok := verboseTestName(line); ok {
			current = name
			continue
		}
		if strings.Contains(line, marker) {
			return current
		}
	}
	return ""
}

// verboseTestName extracts the test name from a `go test -v` status line.
func verboseTestName(line string) (string, bool) {
	for _, prefix := range []string{"=== RUN", "=== CONT", "=== NAME", "=== PAUSE"} {
		if after, ok := strings.CutPrefix(line, prefix); ok {
			return strings.TrimSpace(after), true
		}
	}
	return "", false
}

// failedSubtests counts the `--- FAIL:` lines naming a subtest of the helper.
func failedSubtests(output string) int {
	n := 0
	for line := range strings.Lines(output) {
		if strings.Contains(line, "--- FAIL: "+factoryFatalHelper+"/") {
			n++
		}
	}
	return n
}
