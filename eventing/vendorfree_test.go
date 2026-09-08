package eventing_test

import (
	"os/exec"
	"strings"
	"testing"
)

const eventingPkg = "github.com/kartaladev/wrkflw/eventing"

// goList runs `go list` with the given arguments and returns its output lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("go list %v: %v\n%s", args, err, out)
	}
	var lines []string
	for _, line := range strings.Split(string(out), "\n") {
		if dep := strings.TrimSpace(line); dep != "" {
			lines = append(lines, dep)
		}
	}
	return lines
}

// TestEventingDependencyGraphNamesNoVendorDirectly asserts that the eventing
// package's OWN import statements mention no third-party module beyond
// OpenTelemetry: everything it names is stdlib, in-repo, or otel.
//
// This is the statement #45 is actually about. eventing is the façade a consumer
// wires their broker into, and its whole point after this change is that the
// broker never appears in the API — so the package must not name one. An
// allow-list over DIRECT imports says exactly that, and nothing weaker: adding
// `import "github.com/segmentio/kafka-go"` to any file in this package fails
// here, whatever it is used for.
//
// It is deliberately NOT an allow-list over `go list -deps`. That form is
// unsatisfiable here and always will be: eventing → runtime/chain → engine pulls
// in expr-lang, clockwork, uuid, yaml.v3, xxhash, logr and singleflight, none of
// which #45 removes. The only version of a transitive allow-list that passes is
// one broad enough to pass anything, which is the vacuous shape this repo has
// rejected before. The transitive direction is covered by the deny-list below,
// which is falsifiable.
func TestEventingDependencyGraphNamesNoVendorDirectly(t *testing.T) {
	t.Parallel()

	for _, imp := range goList(t, "-f", `{{join .Imports "\n"}}`, eventingPkg) {
		switch {
		case !strings.Contains(strings.SplitN(imp, "/", 2)[0], "."):
			// No dot in the first path segment: a standard-library package.
		case strings.HasPrefix(imp, "github.com/kartaladev/wrkflw/"):
		case strings.HasPrefix(imp, "go.opentelemetry.io/otel"):
		default:
			t.Errorf("eventing must name no vendor in its own imports, but imports %q. "+
				"Reach the outside world through eventing.PublishFunc and eventing.Handler "+
				"instead: a consumer supplies the client, this package supplies the Envelope.", imp)
		}
	}
}

// TestEventingDependencyGraphIsMessagingVendorFree locks in the removal itself:
// no messaging library may return to eventing's compile graph, by any route.
//
// The four entries are watermill and the three modules the module graph carried
// ONLY because of it — oklog/ulid, lithammer/shortuuid and pkg/errors, all
// watermill's own transitive dependencies. Written before the deletion so it
// fails first (verified: it did, naming
// github.com/ThreeDotsLabs/watermill/message among others), which is what makes
// it a guard rather than a description of the tree as it happens to stand.
//
// Enforced with `go list -deps`, mirroring service/vendorfree_test.go and
// scripts/check-extraction.sh.
func TestEventingDependencyGraphIsMessagingVendorFree(t *testing.T) {
	t.Parallel()

	banned := []string{
		"github.com/ThreeDotsLabs/watermill",
		"github.com/oklog/ulid",
		"github.com/lithammer/shortuuid",
		"github.com/pkg/errors",
	}

	for _, dep := range goList(t, "-deps", eventingPkg) {
		for _, b := range banned {
			if dep == b || strings.HasPrefix(dep, b+"/") {
				t.Errorf("eventing must be free of messaging vendors but its dependency graph includes %q "+
					"(banned prefix %q)", dep, b)
			}
		}
	}
}
