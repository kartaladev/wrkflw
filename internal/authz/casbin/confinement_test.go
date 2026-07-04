package casbin_test

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// TestCasbinConfinement asserts that github.com/casbin/casbin is NOT transitively
// imported by the engine core, model, runtime, or persistence packages. casbin
// must remain confined to casbinauthz/ and internal/authz/casbin/ (ADR-0023).
//
// It also asserts no ORM (gorm/go-pg/jmoiron/sqlx/ent) has leaked into go.mod or
// those package transitive deps (the project uses raw pgx/v5 per ADR-0006).
func TestCasbinConfinement(t *testing.T) {
	t.Parallel()

	// Packages whose transitive deps must NOT include casbin or any ORM.
	// The public service facade + the mountable transports must stay casbin-free:
	// they depend only on the authz.Authorizer / service.PolicyAdmin interfaces, so
	// casbin remains confined to casbinauthz/ and internal/authz/casbin/ (ADR-0023/0036).
	targets := []string{
		"./engine/...",
		"./definition/...",
		"./runtime/...",
		"./internal/persistence/...",
		"./service/...",
		"./transport/rest/...",
	}

	var deps []string
	for _, pkg := range targets {
		out, err := goList(t, "-f", "{{range .Deps}}{{.}}\n{{end}}", pkg)
		if err != nil {
			t.Fatalf("go list %s: %v", pkg, err)
		}
		for _, dep := range strings.Fields(out) {
			if dep != "" {
				deps = append(deps, dep)
			}
		}
	}

	forbiddenCasbin := []string{"github.com/casbin/casbin"}
	forbiddenORM := []string{
		"gorm.io/gorm",
		"github.com/go-pg/",
		"github.com/jmoiron/sqlx",
		"entgo.io/ent",
	}

	for _, dep := range deps {
		for _, banned := range forbiddenCasbin {
			if strings.Contains(dep, banned) {
				t.Errorf("CONFINEMENT VIOLATION: %q was found in transitive deps of engine/model/runtime/persistence\n"+
					"  casbin must be confined to casbinauthz/ and internal/authz/casbin/ (ADR-0023)", dep)
			}
		}
		for _, banned := range forbiddenORM {
			if strings.Contains(dep, banned) {
				t.Errorf("ORM VIOLATION: %q was found in transitive deps — project uses raw pgx/v5 only (ADR-0006)", dep)
			}
		}
	}

	// Also assert go.mod contains no ORM direct/indirect entries.
	gomod, err := goList(t, "-m", "-f", "{{.Path}}", "all")
	if err != nil {
		t.Fatalf("go list -m all: %v", err)
	}
	for _, mod := range strings.Fields(gomod) {
		for _, banned := range forbiddenORM {
			if strings.Contains(mod, banned) {
				t.Errorf("ORM in go.mod: %q — project uses raw pgx/v5 only", mod)
			}
		}
	}
}

// goList runs `go list <args...>` in the module root (two levels up from this
// file's internal/authz/casbin/ package directory) and returns its stdout.
func goList(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	// The test binary runs with cwd = the package directory; adjust to module root.
	cmd.Dir = moduleRoot(t)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Logf("go list stderr: %s", stderr.String())
		return "", err
	}
	return stdout.String(), nil
}

// moduleRoot returns the absolute path to the Go module root by calling `go env GOMOD`.
func moduleRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	// GOMOD returns the path to go.mod; strip the filename to get the dir.
	p := strings.TrimSpace(string(out))
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[:idx]
	}
	return "."
}
