package labels_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/labels"
)

// TestTheTableHoldsTheSevenLabelsInOrder_TS_17_1 verifies TS-17-1: the label table holds exactly seven labels in order,
// with the kind, colour and description of PRD 04's table.
// Verifies: 17-REQ-1.1
func TestTheTableHoldsTheSevenLabelsInOrder_TS_17_1(t *testing.T) {
	want := []labels.Label{
		{Suffix: "triage", Kind: labels.Request, Color: "0e8a16", Description: "Ask agent-fox to triage this issue"},
		{Suffix: "fix", Kind: labels.Request, Color: "0e8a16", Description: "Ask agent-fox to fix this issue"},
		{Suffix: "in-progress", Kind: labels.Status, Color: "fbca04", Description: "agent-fox is working on this issue"},
		{Suffix: "ready", Kind: labels.Status, Color: "1d76db", Description: "Triaged: the diagnosis is in the body"},
		{Suffix: "needs-human", Kind: labels.Status, Color: "d93f0b", Description: "agent-fox stopped with a question; see the last comment"},
		{Suffix: "landed", Kind: labels.Status, Color: "0e8a16", Description: "A branch and a pull request (or a carry patch) exist"},
		{Suffix: "failed", Kind: labels.Status, Color: "b60205", Description: "agent-fox could not finish; see the last comment"},
	}
	got := labels.Table()
	if !slices.Equal(got, want) {
		t.Fatalf("the table is\n%v\nwant\n%v", got, want)
	}
	hex := regexp.MustCompile(`^[0-9a-f]{6}$`)
	for _, l := range got {
		if !hex.MatchString(l.Color) {
			t.Errorf("%s: colour %q is not six lowercase hex digits without '#'", l.Suffix, l.Color)
		}
	}
	// The table is the package's own: a caller that changes its copy changes nothing.
	got[0].Suffix = "changed"
	if labels.Table()[0].Suffix != "triage" {
		t.Error("changing the table that Table returned changed the table")
	}
	if r, s := labels.Requests(), labels.Statuses(); !slices.Equal(r, []string{"triage", "fix"}) ||
		!slices.Equal(s, []string{"in-progress", "ready", "needs-human", "landed", "failed"}) {
		t.Errorf("Requests() = %v, Statuses() = %v", r, s)
	}
	for _, c := range []struct{ name, suffix string }{
		{labels.Triage, "triage"}, {labels.Fix, "fix"}, {labels.InProgress, "in-progress"}, {labels.Ready, "ready"},
		{labels.NeedsHuman, "needs-human"}, {labels.Landed, "landed"}, {labels.Failed, "failed"},
	} {
		if c.name != c.suffix {
			t.Errorf("the constant of %s is %q", c.suffix, c.name)
		}
	}
}

// TestEveryNameIsThePrefixFollowedByItsSuffix_TS_17_2 verifies TS-17-2, a property: for any label prefix, every name is the
// prefix followed by the suffix, whatever AF_LABEL_PREFIX says.
// Verifies: 17-REQ-1.2
func TestEveryNameIsThePrefixFollowedByItsSuffix_TS_17_2(t *testing.T) {
	t.Setenv("AF_LABEL_PREFIX", "unrelated/")
	prefixes := []string{"", "af:", "bot-", "ラベル:", "🦊-", strings.Repeat("x", 32)}
	rng := rand.New(rand.NewPCG(17, 2))
	alphabet := []rune("abcXYZ:-_/.é漢🦊0123456789")
	for range 500 {
		n := rng.IntN(33)
		p := make([]rune, n)
		for i := range p {
			p[i] = alphabet[rng.IntN(len(alphabet))]
		}
		prefixes = append(prefixes, string(p))
	}
	for _, p := range prefixes {
		names := labels.Names(p)
		if len(names) != 7 {
			t.Fatalf("Names(%q) has %d names", p, len(names))
		}
		for i, row := range labels.Table() {
			if got := labels.Name(p, row.Suffix); got != p+row.Suffix {
				t.Fatalf("Name(%q, %q) = %q, want %q", p, row.Suffix, got, p+row.Suffix)
			}
			if names[i] != p+row.Suffix {
				t.Fatalf("Names(%q)[%d] = %q, want %q", p, i, names[i], p+row.Suffix)
			}
		}
	}
}

// imports are the import paths of the non-test Go files of the package in dir, and the files parsed.
func imports(t *testing.T, dir string) (map[string]bool, []*ast.File) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]bool{}
	var files []*ast.File
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, parser.ImportsOnly|parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		full, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, full)
		for _, imp := range f.Imports {
			path, _ := strconv.Unquote(imp.Path.Value)
			out[path] = true
		}
	}
	return out, files
}

// tableUsers are the packages that name the labels, relative to this one. The cancel cleanup joins them in the task that
// adds it.
var tableUsers = []string{"../../worker", "../poll", "../api"}

// TestTheTableImportsOnlyTheStandardLibraryAndIsSharedByItsUsers_TS_17_3 verifies TS-17-3: the table package imports only
// the standard library, its users import it, and none of them defines a label name of its own.
// Verifies: 17-REQ-1.3
func TestTheTableImportsOnlyTheStandardLibraryAndIsSharedByItsUsers_TS_17_3(t *testing.T) {
	own, _ := imports(t, ".")
	for path := range own {
		if first, _, _ := strings.Cut(path, "/"); strings.Contains(first, ".") {
			t.Errorf("the label table imports %s, which is not the standard library", path)
		}
	}
	suffixes := append(labels.Requests(), labels.Statuses()...)
	for _, dir := range tableUsers {
		paths, files := imports(t, dir)
		if !paths["github.com/agent-fox-dev/agentfox/internal/service/labels"] {
			t.Errorf("%s does not import the label table", dir)
		}
		// A constant whose value is a label's suffix is a name of its own, which the table could not change.
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				spec, ok := n.(*ast.ValueSpec)
				if !ok {
					return true
				}
				for _, v := range spec.Values {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, _ := strconv.Unquote(lit.Value); slices.Contains(suffixes, s) {
							t.Errorf("%s: %s declares the label suffix %q itself instead of taking it from the table", dir, spec.Names[0].Name, s)
						}
					}
				}
				return true
			})
		}
	}
}
