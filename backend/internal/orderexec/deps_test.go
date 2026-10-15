package orderexec

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOrderexecDependencyRule: execution depends on model types and its own
// consumer interfaces, never on strategy (strategies produce model.Signal)
// or instruments (the strike picker is a ContractSource, lot sizes come in
// through Config.LotSizeOf).
func TestOrderexecDependencyRule(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"trading-systemv1/internal/strategy", "trading-systemv1/internal/instruments"}
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
