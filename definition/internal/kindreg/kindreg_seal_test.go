package kindreg_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The seal this package implements is a property of the build graph, not of any
// running code, so no in-module test can fail on it: in-module code may import
// internal/ freely, which is exactly why an in-module test of the seal would
// compile and prove nothing. (runtime/monitor/internal_leak_test.go makes the
// same point about the same blind spot.) The only way to observe the rule is to
// compile a package that is genuinely outside this module and read what the
// compiler says — which is what this test does.
//
// It asserts on the DIAGNOSTIC TEXT, never merely on a non-zero exit. A build
// can fail for reasons that have nothing to do with sealing — an unpopulated
// module cache, no network, a toolchain mismatch — and scoring any failure as a
// pass would make this test green precisely when it is least able to see.

// moduleRoot walks up from the test's working directory to the directory holding
// go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		require.NotEqual(t, dir, parent, "walked past the filesystem root without finding go.mod")
		dir = parent
	}
}

// TestRegistrationIsSealedAgainstConsumers compiles throwaway modules that sit
// outside this one and asserts the compiler refuses both doors into the node
// registry: importing the token package, and forging a token without it.
func TestRegistrationIsSealedAgainstConsumers(t *testing.T) {
	t.Parallel()

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH; this test compiles a probe module")
	}
	root := moduleRoot(t)

	type testCase struct {
		name   string
		probe  string
		assert func(t *testing.T, out string, err error)
	}

	cases := []testCase{
		{
			name: "a consumer cannot import the token package",
			probe: `package probe

import "github.com/kartaladev/wrkflw/definition/internal/kindreg"

var _ = kindreg.Grant()
`,
			assert: func(t *testing.T, out string, err error) {
				require.Error(t, err, "importing an internal package from outside must not compile")
				assert.Contains(t, out, "use of internal package",
					"build must fail on the internal-package rule specifically, not for some unrelated reason")
				assert.Contains(t, out, "definition/internal/kindreg")
			},
		},
		{
			name: "a consumer cannot forge a token to reach RegisterKind",
			// The token's only field is an unexported zero-width struct. A
			// consumer can write the structurally identical type, but Go's
			// assignability rules reject it at the call: struct types from
			// different packages are not interchangeable when a field is
			// unexported. This is the door that stays shut even for someone who
			// has read the source.
			probe: `package probe

import "github.com/kartaladev/wrkflw/definition/model"

func init() {
	model.RegisterKind(struct{ _ struct{} }{}, model.NodeKind(9001), model.NodeSpec{Name: "forged"})
}
`,
			assert: func(t *testing.T, out string, err error) {
				require.Error(t, err, "forging the capability token must not compile")
				assert.Contains(t, out, "cannot use",
					"build must fail on assignability to kindreg.Token, not for some unrelated reason")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			// A replace directive points the probe at this working tree, so the
			// test measures the seal as it stands right now rather than as some
			// published version has it. The repository's own go.sum is reused so
			// the probe resolves the module graph offline.
			write := func(name, content string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
			}
			// The probe's go.mod is DERIVED from this module's, not hand-written:
			// it needs the same transitive requirements to resolve the graph
			// offline, and a hand-written stub fails with "updates to go.mod
			// needed" long before the compiler ever reaches the import rule —
			// which would be a build failure that proves nothing about sealing.
			repoMod, readErr := os.ReadFile(filepath.Join(root, "go.mod"))
			require.NoError(t, readErr)
			probeMod := strings.Replace(string(repoMod),
				"module github.com/kartaladev/wrkflw", "module probe", 1)
			probeMod += "\nrequire github.com/kartaladev/wrkflw v0.0.0\n" +
				"replace github.com/kartaladev/wrkflw => " + root + "\n"
			write("go.mod", probeMod)
			write("probe.go", tc.probe)

			sum, readErr := os.ReadFile(filepath.Join(root, "go.sum"))
			require.NoError(t, readErr)
			write("go.sum", string(sum))

			cmd := exec.CommandContext(t.Context(), goBin, "build", "./...")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOFLAGS=", "GOWORK=off")
			out, buildErr := cmd.CombinedOutput()

			t.Logf("probe build output:\n%s", strings.TrimSpace(string(out)))
			tc.assert(t, string(out), buildErr)
		})
	}
}
