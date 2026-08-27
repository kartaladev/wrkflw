package authz_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestAuthzPurity pins that authz depends on nothing in this repo but internal/expreval.
//
// It mirrors engine/purity_test.go, which was the repo's ONLY purity guard until ADR-0190.
// authz was pure in fact and unguarded in practice; since engine imports authz, anything
// added here propagates straight into the engine core the other guard protects.
//
// ⚠ Ablate this with a NON-CYCLIC forbidden import — definition/model works. Importing
// engine would be an import CYCLE (engine imports authz), so the package would fail to
// build and the assertion would never run: "[setup failed]" is not a RED.
func TestAuthzPurity(t *testing.T) {
	t.Parallel()

	out, err := exec.Command("go", "list", "-f", "{{join .Imports \"\\n\"}}", "./").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	const (
		modulePrefix = "github.com/kartaladev/wrkflw/"
		allowed      = modulePrefix + "internal/expreval"
	)
	var inRepo []string
	for imp := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		// stdlib and third-party dependencies are governed by the tech-stack ADRs, not by
		// this guard; it exists to stop authz growing an in-repo dependency.
		if !strings.HasPrefix(imp, modulePrefix) {
			continue
		}
		inRepo = append(inRepo, imp)
		if imp != allowed {
			t.Errorf("authz must not import %q — only %q is permitted", imp, allowed)
		}
	}

	// Assert the guard actually inspected something. Without this, a `go list` that
	// silently returned nothing would pass as vacuously clean.
	if len(inRepo) == 0 {
		t.Fatal("no in-repo imports observed at all — the guard inspected nothing, " +
			"so a passing result here would be meaningless")
	}
}
