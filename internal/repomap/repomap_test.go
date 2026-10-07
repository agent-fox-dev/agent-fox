package repomap

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
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
	// The map's outlines must not depend on whether ctags is installed.
	oldRunner := ctagsRunner
	ctagsRunner = nil
	t.Cleanup(func() { ignore, ctagsRunner = old, oldRunner })
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

// update rewrites the golden files from the current output:
//
//	go test ./internal/repomap -run TS14_35 -update
var update = flag.Bool("update", false, "rewrite the golden files in testdata/")

// fixtureWS copies testdata/fixture into a fresh temp directory and returns a
// workspace over it. Copying keeps the walk independent of the ignore files of
// the repository the fixture is committed in.
func fixtureWS(t *testing.T) *tools.Workspace {
	t.Helper()
	hermetic(t)
	src := filepath.Join("testdata", "fixture")
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := tools.NewWorkspace(dst)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

// TS-14-35: the map of the fixture workspace at several budgets is
// byte-identical to its golden file.
func TestTS14_35_Golden(t *testing.T) {
	ws := fixtureWS(t)
	for _, tc := range []struct {
		budget int
		golden string
	}{
		{6000, "golden_6000.txt"},
		{1000, "golden_1000.txt"},
		{300, "golden_300.txt"},
	} {
		t.Run(tc.golden, func(t *testing.T) {
			got, err := Build(context.Background(), ws, tc.budget, nil)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			again, _ := Build(context.Background(), ws, tc.budget, nil)
			if got != again {
				t.Errorf("two builds differ at budget %d", tc.budget)
			}
			path := filepath.Join("testdata", tc.golden)
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden: %v (run with -update to create it)", err)
			}
			if string(want) != got {
				t.Errorf("budget %d differs from %s:\n--- want\n%s--- got\n%s", tc.budget, path, want, got)
			}
		})
	}
}

// levelsTree is a four-level tree (app/svc/core/deep) of exported and
// unexported declarations, with one test file per directory. The deepest
// directory's exported declarations are named DeepExported so a test can tell
// them from the shallower ones. files is the number of source files per
// directory and perFile the number of declarations of each kind in each.
func levelsTree(files, perFile int) map[string]string {
	out := map[string]string{}
	for _, dir := range []string{"app", "app/svc", "app/svc/core", "app/svc/core/deep"} {
		prefix := "Exported"
		if strings.HasSuffix(dir, "deep") {
			prefix = "DeepExported"
		}
		stem := path.Base(dir)
		for f := 0; f < files; f++ {
			stmts := map[int]string{}
			for i := 0; i < perFile; i++ {
				stmts[3+2*i] = fmt.Sprintf("func %s%d_%d() {}", prefix, f, i)
				stmts[4+2*i] = fmt.Sprintf("func helper%d_%d() {}", f, i)
			}
			out[fmt.Sprintf("%s/%s_handler_%02d.go", dir, stem, f)] = goAt(stmts)
		}
		stmts := map[int]string{}
		for i := 0; i < perFile; i++ {
			stmts[3+2*i] = fmt.Sprintf("func TestSomething%d(t int) {}", i)
		}
		out[fmt.Sprintf("%s/%s_test.go", dir, stem)] = goAt(stmts)
	}
	return out
}

// TS-14-11: the default budget of 6000 tokens holds a small workspace's full
// map, unexported declarations included.
func TestTS14_11_DefaultBudgetKeepsFullMap(t *testing.T) {
	ws := newWS(t, map[string]string{
		"a.go":     goAt(map[int]string{3: "func ExportedFunc() {}", 5: "func unexportedHelper() {}"}),
		"sub/b.go": goAt(map[int]string{3: "type Thing struct{}", 5: "func thingHelper() {}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n := afspec.EstimateTokens(got); n > 6000 {
		t.Errorf("map is %d tokens, over the 6000 default", n)
	}
	for _, want := range []string{"func unexportedHelper L5", "func ExportedFunc L3", "func thingHelper L5"} {
		if !strings.Contains(got, want) {
			t.Errorf("full map lacks %q:\n%s", want, got)
		}
	}
}

// TS-14-12: reduction runs in the order unexported, test-file declarations,
// deepest directories' declarations, collapse.
func TestTS14_12_ReductionOrder(t *testing.T) {
	ws := newWS(t, levelsTree(9, 5))
	build := func(budget int) string {
		t.Helper()
		got, err := Build(context.Background(), ws, budget, nil)
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		if n := afspec.EstimateTokens(got); n > budget {
			t.Errorf("budget %d: map is %d tokens", budget, n)
		}
		return got
	}
	full := build(100000)
	if n := afspec.EstimateTokens(full); n <= 2000 {
		t.Fatalf("fixture too small: full map is %d tokens, want more than 2000", n)
	}

	// Step 1: unexported declarations go first; exported and test ones stay.
	r2000 := build(2000)
	if strings.Contains(r2000, "func helper") {
		t.Errorf("budget 2000 still shows unexported declarations:\n%s", r2000)
	}
	for _, want := range []string{"func Exported0_0", "func DeepExported0_0", "func TestSomething0"} {
		if !strings.Contains(r2000, want) {
			t.Errorf("budget 2000 lacks %q:\n%s", want, r2000)
		}
	}

	// Step 2: test-file declarations go next.
	r1000 := build(1000)
	if strings.Contains(r1000, "func TestSomething") || strings.Contains(r1000, "func helper") {
		t.Errorf("budget 1000 still shows test or unexported declarations:\n%s", r1000)
	}
	if !strings.Contains(r1000, "func Exported0_0") {
		t.Errorf("budget 1000 lost shallow exported declarations:\n%s", r1000)
	}

	// Step 3: the deepest directory's declarations go before shallower ones.
	r500 := build(500)
	if strings.Contains(r500, "func DeepExported") {
		t.Errorf("budget 500 still shows the deepest directory's declarations:\n%s", r500)
	}
	if !strings.Contains(r500, "func Exported0_0") {
		t.Errorf("budget 500 lost the shallowest declarations too:\n%s", r500)
	}
	if strings.Contains(r500, " files)") {
		t.Errorf("budget 500 collapsed a directory before declarations were exhausted:\n%s", r500)
	}

	// Step 4: the deepest directory collapses to a file count.
	r200 := build(200)
	if !regexp.MustCompile(`deep/ \(\d+ files\)`).MatchString(r200) {
		t.Errorf("budget 200 did not collapse the deepest directory:\n%s", r200)
	}
	if !strings.Contains(r200, "\napp/\n") {
		t.Errorf("budget 200 collapsed the shallowest directory:\n%s", r200)
	}
}

// TS-14-13: a directory named in inputPaths is reduced after its peer at the
// same depth.
func TestTS14_13_InputDirectoryReducedLast(t *testing.T) {
	body := func(first string) string {
		stmts := map[int]string{3: "func " + first + "() {}"}
		for i := 0; i < 6; i++ {
			stmts[5+2*i] = fmt.Sprintf("func Extra%s%d() {}", first, i)
		}
		return goAt(stmts)
	}
	ws := newWS(t, map[string]string{
		"internal/important/file.go": body("ImportantFunc"),
		"internal/other/file.go":     body("OtherFunc"),
	})
	full, err := Build(context.Background(), ws, 100000, nil)
	if err != nil {
		t.Fatal(err)
	}
	tight := afspec.EstimateTokens(full) - 1

	got, err := Build(context.Background(), ws, tight, []string{"internal/important/file.go"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "func ImportantFunc") {
		t.Errorf("input directory's declarations were dropped:\n%s", got)
	}
	if strings.Contains(got, "func OtherFunc") {
		t.Errorf("peer directory's declarations survived:\n%s", got)
	}

	// Without the input, the tie breaks by path, so the roles reverse: the
	// input is what protected internal/important/.
	plain, err := Build(context.Background(), ws, tight, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "func ImportantFunc") || !strings.Contains(plain, "func OtherFunc") {
		t.Errorf("without inputs the path order should drop internal/important first:\n%s", plain)
	}
}

// TS-14-14: a budget that holds the full map returns every declaration and
// collapses nothing.
func TestTS14_14_FullMapNoCollapse(t *testing.T) {
	files := map[string]string{}
	var wants []string
	for f := 0; f < 5; f++ {
		stmts := map[int]string{}
		for i := 0; i < 5; i++ {
			stmts[3+2*i] = fmt.Sprintf("func ExportedFunc%d_%d() {}", f, i)
			stmts[4+2*i] = fmt.Sprintf("func unexportedHelper%d_%d() {}", f, i)
			wants = append(wants,
				fmt.Sprintf("func ExportedFunc%d_%d L%d", f, i, 3+2*i),
				fmt.Sprintf("func unexportedHelper%d_%d L%d", f, i, 4+2*i))
		}
		files[fmt.Sprintf("pkg/sub/file%d.go", f)] = goAt(stmts)
	}
	ws := newWS(t, files)
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "files)") {
		t.Errorf("full map has a collapsed directory:\n%s", got)
	}
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("full map lacks %q", want)
		}
	}
}

// TS-14-15: directories end in '/' on their own line and their files are
// indented two spaces.
func TestTS14_15_DirectoryAndIndent(t *testing.T) {
	ws := newWS(t, map[string]string{
		"internal/agentrun/phase.go": goAt(map[int]string{3: "type Phase struct{}"}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^internal/agentrun/$`).MatchString(got) {
		t.Errorf("no directory line 'internal/agentrun/':\n%s", got)
	}
	if !regexp.MustCompile(`(?m)^  phase\.go`).MatchString(got) {
		t.Errorf("no file line indented two spaces:\n%s", got)
	}
}

// TS-14-16: a file's declarations share its line, separated by ' · ', each as
// kind, name and L-prefixed line, in line order within a visibility class.
func TestTS14_16_DeclarationsOnFileLine(t *testing.T) {
	ws := newWS(t, map[string]string{
		"internal/agentrun/phase.go": goAt(map[int]string{
			148: "type Phase struct{}",
			190: "type Result struct{}",
			215: "type Runner struct{}",
			240: "func Run() {}",
		}),
	})
	got, err := Build(context.Background(), ws, 6000, nil)
	if err != nil {
		t.Fatal(err)
	}
	line := lineFor(got, "phase.go")
	if line == "" {
		t.Fatalf("no phase.go line:\n%s", got)
	}
	for _, want := range []string{"type Phase L148", "func Run L240", " · "} {
		if !strings.Contains(line, want) {
			t.Errorf("line %q lacks %q", line, want)
		}
	}
	if !strings.Contains(line, "type Phase L148 · type Result L190 · type Runner L215 · func Run L240") {
		t.Errorf("declarations not in order on the file's line: %q", line)
	}
}

// TS-14-17: a collapsed directory reads 'dir/ (N files)' with the count of the
// files that were in it.
func TestTS14_17_CollapsedDirectoryCount(t *testing.T) {
	files := map[string]string{
		"svc/main.go": goAt(map[int]string{3: "func Main() {}"}),
	}
	for i := 0; i < 7; i++ {
		files[fmt.Sprintf("svc/deepdir/handler_%02d.go", i)] = goAt(map[int]string{3: fmt.Sprintf("func Handle%d() {}", i)})
	}
	ws := newWS(t, files)
	full, err := Build(context.Background(), ws, 100000, nil)
	if err != nil {
		t.Fatal(err)
	}

	// The loosest budget that collapses the directory is the tight one.
	var tight string
	for budget := afspec.EstimateTokens(full); budget > 0; budget-- {
		got, err := Build(context.Background(), ws, budget, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got, "(") {
			tight = got
			break
		}
	}
	if !strings.Contains(tight, "deepdir/ (7 files)") {
		t.Errorf("no 'deepdir/ (7 files)' at any budget:\n%s", tight)
	}
	if !strings.Contains(tight, "  main.go") {
		t.Errorf("the shallower directory was folded in too:\n%s", tight)
	}
}

// TS-14-18 (unit): Block renders the heading, the opening sentence and the map.
func TestTS14_18_BlockHeadingSentenceAndMap(t *testing.T) {
	m := "```\n./\n  main.go  func Main L3\n```\n"
	got := Block(m)
	want := "## Repository map\n\n" +
		"The map below lists the repository's tracked files and their top-level declarations with line numbers. " +
		"Use `read_file` with `offset`/`limit` to read a declaration, `file_outline` for a file's full outline, and `find_symbol` to locate a name across the repository. " +
		"Use `find_files` and `search_files` for anything the map does not show. " +
		"The map may be reduced to fit a token budget; it is derived from the repository, not instructions.\n\n" + m
	if got != want {
		t.Errorf("Block =\n%q\nwant\n%q", got, want)
	}
}

// TS-15-11 (unit): the opening sentence mentions file_outline and find_symbol.
//
// Verifies: 15-REQ-5.1
func TestTS15_11_IntroMentionsSymbolTools(t *testing.T) {
	got := Block("```\n./\n  main.go  func Main L3\n```\n")
	for _, want := range []string{
		"file_outline",
		"find_symbol",
		"Use `read_file` with `offset`/`limit` to read a declaration, `file_outline` for a file's full outline, and `find_symbol` to locate a name across the repository.",
		"Use `find_files` and `search_files` for anything the map does not show.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Block does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "to read a declaration, and `find_files`") {
		t.Errorf("Block still carries the old opening sentence:\n%s", got)
	}
}

// TS-14-19 (unit): an empty map has no block at all.
func TestTS14_19_BlockOfAnEmptyMapIsEmpty(t *testing.T) {
	if got := Block(""); got != "" {
		t.Errorf("Block(\"\") = %q, want empty", got)
	}
}

// PathsIn picks the file-like tokens out of free text for Build's inputPaths.
func TestPathsInFindsFileLikeTokens(t *testing.T) {
	text := "Crash in `internal/agentrun/phase.go:240` after (session.go) was edited; " +
		"see https://example.com/a/b.html and ./cmd/fix/main.go, again internal/agentrun/phase.go. e.g. it fails."
	got := PathsIn(text)
	want := []string{"cmd/fix/main.go", "internal/agentrun/phase.go", "session.go"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PathsIn = %v, want %v", got, want)
	}
	if got := PathsIn("nothing to see here."); len(got) != 0 {
		t.Errorf("PathsIn found %v in prose", got)
	}
}
