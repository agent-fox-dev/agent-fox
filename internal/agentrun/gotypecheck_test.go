package agentrun

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun/gotypechecktest"
)

// TS-17-16 (unit): The go_typecheck detail formatter produces
// '<N> packages checked, <M> errors' and appends ', partial' exactly when a
// bound was hit.
//
// Verifies: 17-REQ-4.3
func TestTS17_16_GoTypecheckDetailFormatter(t *testing.T) {
	cases := []struct {
		pkgs    int
		errs    int
		partial bool
		want    string
	}{
		{12, 0, false, "12 packages checked, 0 errors"},
		{1, 3, false, "1 packages checked, 3 errors"},
		{0, 0, false, "0 packages checked, 0 errors"},
		{12, 0, true, "12 packages checked, 0 errors, partial"},
		{1, 41, true, "1 packages checked, 41 errors, partial"},
	}
	for _, tc := range cases {
		got := goTypecheckDetail(tc.pkgs, tc.errs, tc.partial)
		if got != tc.want {
			t.Errorf("goTypecheckDetail(%d, %d, %v) = %q, want %q",
				tc.pkgs, tc.errs, tc.partial, got, tc.want)
		}
	}
}

// TS-17-17 (integration): The real DetectGoTypecheck reports
// '0 packages checked, 0 errors' with no error for a workspace that holds no
// Go package.
//
// Verifies: 17-REQ-4.4
func TestTS17_17_NoGoPackageReportsZero(t *testing.T) {
	type wsSetup struct {
		name  string
		files map[string]string
	}
	setups := []wsSetup{
		{"empty", nil},
		{"go.mod only", map[string]string{"go.mod": "module x\n\ngo 1.21\n"}},
		{"readme and py", map[string]string{"README.md": "hi", "tool.py": "print(1)"}},
	}
	for _, s := range setups {
		t.Run(s.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, content := range s.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			ws, err := tools.NewWorkspace(dir)
			if err != nil {
				t.Fatal(err)
			}
			skipIfNoFindReferences(t, ws)
			d, err := DetectGoTypecheck(ws)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d != "0 packages checked, 0 errors" {
				t.Errorf("detail = %q, want %q", d, "0 packages checked, 0 errors")
			}
		})
	}
}

// TS-17-18 (property): For any workspace, the real detail matches the fixed
// format, ', partial' is the only variant, and no text from the workspace
// appears in it.
//
// Verifies: 17-REQ-4.3
func TestTS17_18_DetailFormatProperty(t *testing.T) {
	// Skip if the replace target does not offer find_references.
	tmpWs, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, tmpWs)

	re := regexp.MustCompile(`^[0-9]+ packages checked, [0-9]+ errors(, partial)?$`)
	for i := 0; i < 25; i++ {
		root, texts, snapshot := gotypechecktest.Generate(t, int64(i))
		before := snapshot()
		ws, err := tools.NewWorkspace(root)
		if err != nil {
			t.Fatalf("seed %d: NewWorkspace: %v", i, err)
		}
		d, err := DetectGoTypecheck(ws)
		if err != nil {
			t.Fatalf("seed %d: unexpected error: %v", i, err)
		}
		if !re.MatchString(d) {
			t.Errorf("seed %d: detail %q does not match format", i, d)
		}
		if strings.Contains(d, "zq9x_") {
			t.Errorf("seed %d: detail %q contains marker zq9x_", i, d)
		}
		for _, txt := range texts {
			if txt != "" && strings.Contains(d, txt) {
				t.Errorf("seed %d: detail %q contains generated text %q", i, d, txt)
			}
		}
		after := snapshot()
		if before != after {
			t.Errorf("seed %d: workspace changed after call", i)
		}
	}
}

// TS-17-19 (unit): DetectGoTypecheck is a package variable of the type
// func(*tools.Workspace) (string, error) that a test can replace and restore.
//
// Verifies: 17-REQ-4.6
func TestTS17_19_DetectGoTypecheckIsReplaceableVariable(t *testing.T) {
	// Type assertion: the assignment compiles only if the types match.
	var _ func(*tools.Workspace) (string, error) = DetectGoTypecheck

	orig := DetectGoTypecheck
	t.Cleanup(func() { DetectGoTypecheck = orig })

	DetectGoTypecheck = func(*tools.Workspace) (string, error) {
		return "stub", nil
	}
	d, err := DetectGoTypecheck(nil)
	if d != "stub" || err != nil {
		t.Errorf("stub: got (%q, %v), want (\"stub\", nil)", d, err)
	}

	// Restore and confirm the real one refuses a nil workspace.
	DetectGoTypecheck = orig
	d, err = DetectGoTypecheck(nil)
	if err == nil {
		t.Error("real DetectGoTypecheck(nil) should return an error")
	}
	if d != "" {
		t.Errorf("real DetectGoTypecheck(nil) detail = %q, want empty", d)
	}
}

// TS-17-20 (integration): The real DetectGoTypecheck takes its three numbers
// from AgentKit's References result: a clean package reports zero errors, an
// ill-typed one at least one, under a 10 s probe timeout.
//
// Verifies: 17-REQ-4.7
func TestTS17_20_RealDetectGoTypecheckNumbers(t *testing.T) {
	// Workspace A: two small packages, no type errors.
	dirA := t.TempDir()
	writeFile(t, dirA, "go.mod", "module example.com/a\n\ngo 1.21\n")
	writeFile(t, dirA, "pkg1/pkg1.go", "package pkg1\n\nvar X int = 42\n")
	writeFile(t, dirA, "pkg2/pkg2.go", "package pkg2\n\nvar Y string = \"hello\"\n")

	wsA, err := tools.NewWorkspace(dirA)
	if err != nil {
		t.Fatal(err)
	}
	skipIfNoFindReferences(t, wsA)

	dA, errA := DetectGoTypecheck(wsA)
	if errA != nil {
		t.Fatalf("workspace A: %v", errA)
	}
	reA := regexp.MustCompile(`^[1-9][0-9]* packages checked, 0 errors$`)
	if !reA.MatchString(dA) {
		t.Errorf("workspace A: detail = %q, want match %s", dA, reA)
	}

	// Workspace B: one package with a type error.
	dirB := t.TempDir()
	writeFile(t, dirB, "go.mod", "module example.com/b\n\ngo 1.21\n")
	writeFile(t, dirB, "bad/bad.go", "package bad\n\nvar x int = \"text\"\n")

	wsB, err := tools.NewWorkspace(dirB)
	if err != nil {
		t.Fatal(err)
	}
	dB, errB := DetectGoTypecheck(wsB)
	if errB != nil {
		t.Fatalf("workspace B: %v", errB)
	}
	reB := regexp.MustCompile(`^[1-9][0-9]* packages checked, [1-9][0-9]* errors$`)
	if !reB.MatchString(dB) {
		t.Errorf("workspace B: detail = %q, want match %s", dB, reB)
	}

	// The non-test source contains the expected patterns.
	src := readNonTestSource(t, ".")
	for _, needle := range []string{"tools.All(", `"find_references"`, ".References("} {
		if !strings.Contains(src, needle) {
			t.Errorf("non-test source missing %q", needle)
		}
	}

	// The default timeout is 10 s.
	if goTypecheckTimeout != 10*time.Second {
		t.Errorf("goTypecheckTimeout = %v, want 10s", goTypecheckTimeout)
	}
}

// TS-17-21 (unit): DetectGoTypecheck returns an error, not a detail, for no
// workspace and for an elapsed probe timeout.
//
// Verifies: 17-REQ-4.5
func TestTS17_21_ErrorCases(t *testing.T) {
	t.Run("nil workspace", func(t *testing.T) {
		d, err := DetectGoTypecheck(nil)
		if err == nil {
			t.Fatal("expected error for nil workspace")
		}
		if d != "" {
			t.Errorf("detail = %q, want empty", d)
		}
		if !strings.Contains(strings.ToLower(err.Error()), "workspace") {
			t.Errorf("error %q should mention workspace", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		dir := t.TempDir()
		writeFile(t, dir, "go.mod", "module example.com/t\n\ngo 1.21\n")
		writeFile(t, dir, "p/p.go", "package p\n\nvar X = 1\n")
		ws, err := tools.NewWorkspace(dir)
		if err != nil {
			t.Fatal(err)
		}
		skipIfNoFindReferences(t, ws)

		orig := goTypecheckTimeout
		goTypecheckTimeout = time.Nanosecond
		t.Cleanup(func() { goTypecheckTimeout = orig })

		d, err := DetectGoTypecheck(ws)
		if err == nil {
			t.Fatal("expected error for timeout")
		}
		if d != "" {
			t.Errorf("detail = %q, want empty", d)
		}
		if !errors.Is(err, context.DeadlineExceeded) &&
			!strings.Contains(strings.ToLower(err.Error()), "timeout") &&
			!strings.Contains(strings.ToLower(err.Error()), "deadline") {
			t.Errorf("error %q should indicate timeout", err)
		}
	})
}

// TS-17-22 (unit): agent-fox runs no type-checker of its own: the non-test
// source of internal/agentrun imports none of go/types, go/parser,
// go/packages or golang.org/x/tools, and go.mod requires no type-checking
// module.
//
// Verifies: 17-REQ-4.8
func TestTS17_22_NoTypeCheckerImports(t *testing.T) {
	forbidden := []string{"go/types", "go/parser", "go/packages"}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		for _, imp := range f.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			for _, fb := range forbidden {
				if p == fb {
					t.Errorf("%s imports %q", e.Name(), p)
				}
			}
			if strings.HasPrefix(p, "golang.org/x/tools") {
				t.Errorf("%s imports %q", e.Name(), p)
			}
		}
	}

	// go.mod must not require golang.org/x/tools.
	gomod, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gomod), "golang.org/x/tools") {
		t.Error("go.mod requires golang.org/x/tools")
	}
}

// --- helpers ---

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// readNonTestSource concatenates every non-test .go file in the given
// directory (relative to the package).
func readNonTestSource(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(data)
	}
	return b.String()
}
