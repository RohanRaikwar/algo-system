package orderexec

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOrderexecDependencyRule: execution depends on model types, never on
// the strategy package (strategies produce model.Signal; the executor
// consumes it).
func TestOrderexecDependencyRule(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"trading-systemv1/internal/strategy"}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			for _, bad := range forbidden {
				if path == bad {
					t.Errorf("%s imports %s", f, path)
				}
			}
		}
	}
}
