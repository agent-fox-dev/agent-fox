package repomap

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/outline"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// hermetic pins the ignore engine's global layer to empty for the duration of
// a test, so a developer's own core.excludesFile cannot hide a fixture file.
func hermetic(t *testing.T) {
	t.Helper()
	old := ignore
	ignore = tools.NoGlobalExcludes()
	t.Cleanup(func() { ignore = old })
}

// newWS writes files (slash path -> content) under a fresh temp directory and
// returns a workspace rooted there.
func newWS(t *testing.T, files map[string]string) *tools.Workspace {
	t.Helper()
	hermetic(t)
	dir := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := tools.NewWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// goAt returns a Go source file whose statements sit on the given 1-based
// lines, so a test can pin the line numbers the map must show.
func goAt(stmts map[int]string) string {
	max := 1
	for ln := range stmts {
		if ln > max {
			max = ln
		}
	}
	lines := make([]string, max)
	lines[0] = "package x"
	for ln, s := range stmts {
		lines[ln-1] = s
	}
	return strings.Join(lines, "\n") + "\n"
}

// lineFor returns the map line that mentions name, or "" when none does.
func lineFor(m, name string) string {
	for _, l := range strings.Split(m, "\n") {
		if strings.Contains(l, name) {
			return l
		}
	}
	return ""
}

// bigTree is a fixture of 12 files across 4 directories.
func bigTree() map[string]string {
	files := map[string]string{}
	for _, d := range []string{"cmd/tool", "internal/alpha", "internal/beta", "internal/beta/deep"} {
		for i := 0; i < 3; i++ {
			files[fmt.Sprintf("%s/file%d.go", d, i)] = goAt(map[int]string{
				3: fmt.Sprintf("func Exported%d() {}", i),
				5: fmt.Sprintf("func private%d() {}", i),
				7: fmt.Sprintf("type Kind%d struct{}", i),
			})
		}
	}
	files["internal/alpha/file0_test.go"] = goAt(map[int]string{3: "func TestOnly(t int) {}"})
	return files
}

// TS-14-1: Build returns a non-empty map naming the workspace's files.
func TestTS14_1_BuildReturnsMap(t *testing.T) {
	ws := newWS(t, map[string]string{
		"internal/agentrun/phase.go":  goAt(map[int]string{3: "type Phase struct{}"}),
		"internal/agentrun/policy.go": goAt(map[int]string{3: "func AssertReadOnly() {}"}),
		"internal/toolio/cli.go":      goAt(map[int]string{3: "type Common struct{}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{"phase.go", "policy.go", "cli.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
}

// TS-14-2: a zero budget returns "" and never touches the workspace.
func TestTS14_2_ZeroBudgetDoesNotWalk(t *testing.T) {
	ws := newWS(t, map[string]string{"a.go": goAt(nil)})
	walks := 0
	old := walkFn
	walkFn = func(ctx context.Context, w *tools.Workspace, root string, o tools.WalkOptions, fn func(string, fs.DirEntry) error) error {
		walks++
		return old(ctx, w, root, o, fn)
	}
	t.Cleanup(func() { walkFn = old })

	got, err := Build(context.Background(), ws, 0, nil)
	if err != nil || got != "" {
		t.Fatalf("Build(budget 0) = %q, %v; want \"\", nil", got, err)
	}
	if walks != 0 {
		t.Errorf("walk called %d times for budget 0", walks)
	}
}

// TS-14-3: ignore rules and the hidden-entry rule of tools.Walk apply.
func TestTS14_3_IgnoreAndHidden(t *testing.T) {
	ws := newWS(t, map[string]string{
		".gitignore":          "ignored.go\n",
		".hidden/secret.go":   goAt(map[int]string{3: "func Secret() {}"}),
		"ignored.go":          goAt(map[int]string{3: "func Ignored() {}"}),
		"visible.go":          goAt(map[int]string{3: "func Visible() {}"}),
		"sub/also_visible.go": goAt(map[int]string{3: "func Also() {}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"visible.go", "also_visible.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{".hidden", "secret.go", "ignored.go", ".gitignore"} {
		if strings.Contains(got, bad) {
			t.Errorf("map contains %q:\n%s", bad, got)
		}
	}
}

// TS-14-4: declarations come from the outline package, with L-prefixed lines.
func TestTS14_4_Declarations(t *testing.T) {
	ws := newWS(t, map[string]string{
		"x.go": goAt(map[int]string{5: "func Foo() {}", 12: "type Bar struct{}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func Foo L5", "type Bar L12"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
}

// 14-REQ-4: methods render with their receiver, as in the spec's example.
func TestTS14_4_MethodRendering(t *testing.T) {
	ws := newWS(t, map[string]string{
		"r.go": goAt(map[int]string{
			3: "type Runner struct{}",
			4: "func (r *Runner) Run() {}",
			5: "func (r Runner) Name() string { return \"\" }",
		}),
	})
	got, _ := Build(context.Background(), ws, 6000, nil)
	for _, want := range []string{"func (*Runner) Run L4", "func (Runner) Name L5"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
}

// TS-14-5: the returned map never exceeds the budget.
func TestTS14_5_NeverExceedsBudget(t *testing.T) {
	ws := newWS(t, bigTree())
	full, err := Build(context.Background(), ws, 100000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if afspec.EstimateTokens(full) <= 200 {
		t.Fatalf("fixture too small: full map is %d tokens", afspec.EstimateTokens(full))
	}
	for _, budget := range []int{1, 2, 5, 10, 20, 50, 100, 200, 300, 500} {
		got, err := Build(context.Background(), ws, budget, nil)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if n := afspec.EstimateTokens(got); n > budget {
			t.Errorf("budget %d: map is %d tokens:\n%s", budget, n, got)
		}
	}
	got, _ := Build(context.Background(), ws, 200, nil)
	if got == "" {
		t.Error("budget 200 gave an empty map")
	}
}

// TS-14-6: an outline failure for one file lists the file without
// declarations and does not fail the build.
func TestTS14_6_OutlineErrorKeepsFile(t *testing.T) {
	ws := newWS(t, map[string]string{
		"valid.go":   goAt(map[int]string{3: "func ValidFunc() {}"}),
		"corrupt.go": goAt(map[int]string{3: "func Hidden() {}"}),
		"binary.go":  "\x00\x01\x02garbage",
	})
	old := outlineFn
	outlineFn = func(ctx context.Context, abs string, src []byte, o outline.Options) (outline.File, error) {
		if filepath.Base(abs) == "corrupt.go" {
			return outline.File{}, fmt.Errorf("simulated outline failure")
		}
		return old(ctx, abs, src, o)
	}
	t.Cleanup(func() { outlineFn = old })

	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, name := range []string{"corrupt.go", "binary.go"} {
		l := lineFor(got, name)
		if l == "" {
			t.Fatalf("map lacks %s:\n%s", name, got)
		}
		if strings.Contains(l, " L") || strings.Contains(l, "func") {
			t.Errorf("%s line carries declarations: %q", name, l)
		}
	}
	if strings.Contains(got, "Hidden") {
		t.Errorf("declarations of the failed file leaked:\n%s", got)
	}
	if !strings.Contains(got, "valid.go") || !strings.Contains(got, "func ValidFunc L3") {
		t.Errorf("valid file missing:\n%s", got)
	}
}

// TS-14-7: directories render with a trailing slash and files beneath them.
func TestTS14_7_TreeAndDeclarations(t *testing.T) {
	ws := newWS(t, map[string]string{
		"internal/agentrun/phase.go": goAt(map[int]string{3: "type Phase struct{}"}),
		"internal/toolio/cli.go":     goAt(map[int]string{3: "func Parse() {}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\ninternal/agentrun/\n", "\ninternal/toolio/\n", "\n  phase.go", "\n  cli.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("map lacks %q:\n%s", want, got)
		}
	}
	if !regexp.MustCompile(`func \w+ L\d+`).MatchString(got) {
		t.Errorf("no func declaration in:\n%s", got)
	}
	if !strings.HasPrefix(got, "```\n") || !strings.HasSuffix(got, "\n```\n") {
		t.Errorf("map is not a fenced block:\n%s", got)
	}
}

// TS-14-8: files sort by path; declarations sort exported-first, then by line.
func TestTS14_8_Ordering(t *testing.T) {
	ws := newWS(t, map[string]string{
		"example.go": goAt(map[int]string{
			3:  "func alpha() {}",
			5:  "func Foo() {}",
			10: "func bar() {}",
			20: "type Baz struct{}",
		}),
		"b/z.go": goAt(nil),
		"a/y.go": goAt(nil),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := lineFor(got, "example.go")
	idx := func(s string) int {
		i := strings.Index(line, s)
		if i < 0 {
			t.Fatalf("line %q lacks %q", line, s)
		}
		return i
	}
	foo, baz, alpha, bar := idx("func Foo L5"), idx("type Baz L20"), idx("func alpha L3"), idx("func bar L10")
	if !(foo < baz && baz < alpha && alpha < bar) {
		t.Errorf("declaration order wrong (Foo %d, Baz %d, alpha %d, bar %d): %q", foo, baz, alpha, bar, line)
	}
	if !strings.Contains(line, "func Foo L5 · type Baz L20 · func alpha L3 · func bar L10") {
		t.Errorf("declarations not separated by ' · ': %q", line)
	}
	if strings.Index(got, "\na/\n") > strings.Index(got, "\nb/\n") {
		t.Errorf("directories not sorted by path:\n%s", got)
	}
}

// TS-14-9: no timestamps, sizes or other metadata appear.
func TestTS14_9_NoMetadata(t *testing.T) {
	ws := newWS(t, bigTree())
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, re := range []string{`\d{4}-\d{2}-\d{2}`, `\d{1,2}:\d{2}`, `\d+ bytes`, `\d+\.?\d*[KMG]B`} {
		if regexp.MustCompile(re).MatchString(got) {
			t.Errorf("map matches %q:\n%s", re, got)
		}
	}
}

// TS-14-10: the same tree, inputs and budget give a byte-identical map.
func TestTS14_10_Deterministic(t *testing.T) {
	ws := newWS(t, bigTree())
	var paths []string
	for p := range bigTree() {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	subsets := [][]string{nil, {paths[0]}, {paths[3], paths[7]}, {"internal/beta"}, paths}
	for _, budget := range []int{100, 500, 1000, 6000} {
		for _, in := range subsets {
			r1, err1 := Build(context.Background(), ws, budget, in)
			r2, err2 := Build(context.Background(), ws, budget, in)
			if err1 != nil || err2 != nil {
				t.Fatalf("budget %d: %v %v", budget, err1, err2)
			}
			if r1 != r2 {
				t.Errorf("budget %d inputs %v: maps differ:\n%s\n---\n%s", budget, in, r1, r2)
			}
		}
	}
}

// 14-REQ-3.4: a budget that holds the full map yields it unreduced.
func TestTS14_FullMapWhenItFits(t *testing.T) {
	ws := newWS(t, bigTree())
	got, err := Build(context.Background(), ws, 100000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, " files)") {
		t.Errorf("full map has collapsed directories:\n%s", got)
	}
	for _, want := range []string{"func private0 L5", "func TestOnly L3"} {
		if !strings.Contains(got, want) {
			t.Errorf("full map lacks %q", want)
		}
	}
}

// 14-REQ-3.2: the reduction order drops unexported, then test-file
// declarations, then deep directories' declarations, then collapses.
func TestTS14_ReductionOrder(t *testing.T) {
	ws := newWS(t, bigTree())
	size := func(budget int) string {
		got, err := Build(context.Background(), ws, budget, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	full := size(100000)
	n := afspec.EstimateTokens(full)

	// Just under the full size: only unexported declarations go.
	step1 := size(n - 1)
	if strings.Contains(step1, "func private0") || !strings.Contains(step1, "func Exported0 L3") {
		t.Errorf("step 1 should drop only unexported declarations:\n%s", step1)
	}
	if !strings.Contains(step1, "func TestOnly L3") {
		t.Errorf("step 1 dropped test-file declarations too early:\n%s", step1)
	}
	// A tight budget collapses directories, deepest first.
	tight := size(40)
	if !strings.Contains(tight, "internal/beta/deep/ (3 files)") {
		t.Errorf("deepest directory not collapsed:\n%s", tight)
	}
	if afspec.EstimateTokens(tight) > 40 {
		t.Errorf("tight map over budget")
	}
}

// section returns the indented file lines under the directory header dir/.
func section(m, dir string) string {
	_, rest, ok := strings.Cut(m, "\n"+dir+"/\n")
	if !ok {
		return ""
	}
	var out []string
	for _, l := range strings.Split(rest, "\n") {
		if !strings.HasPrefix(l, "  ") {
			break
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

// 14-REQ-3.3: a directory holding an input path is reduced after its peers at
// the same depth.
func TestTS14_InputDirectoriesReducedLast(t *testing.T) {
	files := map[string]string{}
	for _, d := range []string{"pkg/aaa", "pkg/bbb", "pkg/ccc"} {
		for i := 0; i < 4; i++ {
			files[fmt.Sprintf("%s/f%d.go", d, i)] = goAt(map[int]string{3: fmt.Sprintf("func Exp%d() {}", i)})
		}
	}
	ws := newWS(t, files)
	full, _ := Build(context.Background(), ws, 100000, nil)
	witness := false
	for budget := afspec.EstimateTokens(full); budget > 30; budget-- {
		got, err := Build(context.Background(), ws, budget, []string{"pkg/ccc/f1.go"})
		if err != nil {
			t.Fatal(err)
		}
		has := func(dir string) bool { return strings.Contains(section(got, dir), "func Exp") }
		if !has("pkg/ccc") && (has("pkg/aaa") || has("pkg/bbb")) {
			t.Fatalf("budget %d: input directory reduced before its peers:\n%s", budget, got)
		}
		if has("pkg/ccc") && !has("pkg/aaa") {
			witness = true
		}
	}
	if !witness {
		t.Error("never saw a peer reduced while the input directory kept its declarations")
	}
}

// 14-REQ-1.3: Build rejects nothing outside the workspace and tolerates nil.
func TestTS14_NilWorkspace(t *testing.T) {
	if _, err := Build(context.Background(), nil, 100, nil); err == nil {
		t.Error("Build with nil workspace returned no error")
	}
}

// An empty workspace has no map.
func TestTS14_EmptyWorkspace(t *testing.T) {
	ws := newWS(t, nil)
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil || got != "" {
		t.Errorf("Build(empty) = %q, %v; want \"\", nil", got, err)
	}
}
