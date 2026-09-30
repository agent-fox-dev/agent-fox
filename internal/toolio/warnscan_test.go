package toolio_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// warnViolation is one Run.Warn call site whose first argument is a bare
// string literal rather than a declared WarnCode.
type warnViolation struct {
	pos  string
	text string
}

// scanWarnCallSites walks every non-test .go file directly under each of
// dirs (relative to root) looking for a call shaped like `x.Warn(...)`, and
// flags one whose first argument is a raw string literal: that still
// type-checks against a WarnCode parameter (WarnCode's underlying type is
// string), so only a syntactic check catches it.
func scanWarnCallSites(t *testing.T, root string, dirs []string) []warnViolation {
	t.Helper()
	var violations []warnViolation
	fset := token.NewFileSet()

	for _, dir := range dirs {
		full := filepath.Join(root, dir)
		err := filepath.Walk(full, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("parsing %s: %v", path, perr)
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Warn" {
					return true
				}
				if len(call.Args) == 0 {
					return true
				}
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					violations = append(violations, warnViolation{
						pos:  fset.Position(call.Pos()).String(),
						text: lit.Value,
					})
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", full, err)
		}
	}
	return violations
}

// TS-05-36 (unit): a source scan flags a Run.Warn call site that passes a
// bare string literal instead of a declared WarnCode.
func TestTS05_36_SourceScanFlagsLiteralWarnCode(t *testing.T) {
	root := findWorkspaceRoot(t)

	real := scanWarnCallSites(t, root, []string{
		"internal/toolio", "codefix", "codeimpl", "specgen", "issuetriage",
	})
	if len(real) != 0 {
		t.Errorf("the real tree has %d Run.Warn call site(s) passing a bare string literal: %v", len(real), real)
	}

	fixtureDir := filepath.Join(root, "testdata", "warn_literal_fixture")
	fixture := scanWarnCallSites(t, root, []string{
		strings.TrimPrefix(strings.TrimPrefix(fixtureDir, root), string(filepath.Separator)),
	})
	if len(fixture) != 1 {
		t.Fatalf("the fixture should report exactly 1 violation, got %d: %v", len(fixture), fixture)
	}
	want := fmt.Sprintf("%q", "some_code")
	if fixture[0].text != want {
		t.Errorf("fixture violation literal = %s, want %s", fixture[0].text, want)
	}
}
