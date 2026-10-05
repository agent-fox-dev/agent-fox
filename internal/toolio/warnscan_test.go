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

// warnViolation is one Run.Warn call site whose first argument is a string
// literal, bare or converted (WarnCode("x")), rather than a declared WarnCode
// constant.
type warnViolation struct {
	pos  string
	text string
}

// scanWarnCallSites walks every non-test .go file directly under each of
// dirs (relative to root) looking for a call shaped like `x.Warn(...)`, and
// flags one whose first argument is a raw string literal, or a literal
// converted to WarnCode: both still type-check against a WarnCode parameter
// (WarnCode's underlying type is string), so only a syntactic check catches
// them.
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
				if lit, ok := literalWarnCode(call.Args[0]); ok {
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

// literalWarnCode reports whether arg is a string literal, or a conversion of
// one to WarnCode (WarnCode("x") or toolio.WarnCode("x")), and returns it.
func literalWarnCode(arg ast.Expr) (*ast.BasicLit, bool) {
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return lit, true
	}
	conv, ok := arg.(*ast.CallExpr)
	if !ok || len(conv.Args) != 1 {
		return nil, false
	}
	name := ""
	switch f := conv.Fun.(type) {
	case *ast.Ident:
		name = f.Name
	case *ast.SelectorExpr:
		name = f.Sel.Name
	}
	if name != "WarnCode" {
		return nil, false
	}
	lit, ok := conv.Args[0].(*ast.BasicLit)
	return lit, ok && lit.Kind == token.STRING
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
	// Two violations: a bare literal, and a literal converted to WarnCode.
	if len(fixture) != 2 {
		t.Fatalf("the fixture should report exactly 2 violations, got %d: %v", len(fixture), fixture)
	}
	for i, lit := range []string{"some_code", "undeclared"} {
		if want := fmt.Sprintf("%q", lit); fixture[i].text != want {
			t.Errorf("fixture violation %d literal = %s, want %s", i, fixture[i].text, want)
		}
	}
}
