package engine_test

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorePurityNoOTel asserts the pure core never imports OpenTelemetry.
// Observability lives strictly in the runtime and outer layers (spec §1).
func TestCorePurityNoOTel(t *testing.T) {
	for _, dir := range []string{".", "../model"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			path := filepath.Join(dir, name)
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			for _, imp := range f.Imports {
				if strings.Contains(imp.Path.Value, "go.opentelemetry.io") {
					t.Errorf("%s imports %s: the pure core must not import OpenTelemetry", path, imp.Path.Value)
				}
			}
		}
	}
}
