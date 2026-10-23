package repomap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

// fakeOutliner answers from tables and counts its calls.
type fakeOutliner struct {
	mu    sync.Mutex
	decls map[string][]Decl
	errs  map[string]error
	calls []string
}

func (f *fakeOutliner) Outline(ctx context.Context, path string) ([]Decl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if err := f.errs[path]; err != nil {
		return nil, err
	}
	return f.decls[path], nil
}

func (f *fakeOutliner) called(path string) bool { return slices.Contains(f.calls, path) }

// entries makes the entries of paths, each of 100 bytes.
func entries(paths ...string) []Entry {
	out := make([]Entry, len(paths))
	for i, p := range paths {
		out[i] = Entry{Path: p, Size: 100}
	}
	return out
}

// estimate is ceil(len/4).
func estimate(s string) int { return (len(s) + 3) / 4 }

// mustBuild is Build, failing the test on an error.
func mustBuild(t *testing.T, files []Entry, o Outliner, tokens int) Result {
	t.Helper()
	res, err := Build(context.Background(), files, o, tokens)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return res
}

// TestBuildFormat_TS_06_63 verifies TS-06-63:
// the map renders directories, indented files and declarations in the
// specified format and returns the full result shape.
// Verifies: 06-REQ-9.1, 06-REQ-9.7
func TestBuildFormat_TS_06_63(t *testing.T) {
	fake := &fakeOutliner{decls: map[string][]Decl{
		"a.go":     {{"func", "A", 3}, {"type", "T", 10}},
		"pkg/b.go": {{"func", "B", 1}},
	}}

	res := mustBuild(t, entries("pkg/b_test.go", "a.go", "pkg/b.go"), fake, 4000)

	want := "./\n  a.go: func A L3 · type T L10\npkg/\n  b.go: func B L1\n  b_test.go\n"
	if res.Map != want {
		t.Errorf("map = %q, want %q", res.Map, want)
	}
	if res.Tokens != 4000 || res.EstimatedTokens != estimate(res.Map) || res.Level != 1 || res.FilesTotal != 3 || res.FilesOutlined != 3 || res.Truncated {
		t.Errorf("result = %+v", res)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}

	// A budget that forces a level above 1 says so.
	files, many := ladderView()
	cut := mustBuild(t, files, many, MinTokens)
	if cut.Level <= 1 || !cut.Truncated || estimate(cut.Map) > MinTokens {
		t.Errorf("a budget of %d tokens for %d files: level %d, truncated %v, %d tokens", MinTokens, len(files), cut.Level, cut.Truncated, estimate(cut.Map))
	}
}

// TestBuildOrder guards 06-REQ-9.1: directories come in path order, the root
// first and a directory before its subdirectories, files in each by name, and
// whatever the order the files were given in.
func TestBuildOrder(t *testing.T) {
	paths := []string{"z.go", "b/y.go", "b/a.go", "a/x/f.go", "a-b/g.go", "a/f.go", "b/c/d.go", "A.go", "é/x.go"}
	fake := &fakeOutliner{}

	res := mustBuild(t, entries(paths...), fake, 4000)

	want := "./\n  A.go\n  z.go\na/\n  f.go\na/x/\n  f.go\na-b/\n  g.go\nb/\n  a.go\n  y.go\nb/c/\n  d.go\né/\n  x.go\n"
	if res.Map != want {
		t.Errorf("map = %q, want %q", res.Map, want)
	}
	shuffled := slices.Clone(paths)
	rand.New(rand.NewPCG(1, 2)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	if again := mustBuild(t, entries(shuffled...), fake, 4000); again.Map != res.Map {
		t.Error("the order of the input changes the map")
	}
}

// TestBuildRefusesBudgetsOutsideTheRange guards 06-REQ-9.2.
func TestBuildRefusesBudgetsOutsideTheRange(t *testing.T) {
	for _, n := range []int{-1, 0, 199, 100001} {
		_, err := Build(context.Background(), entries("a.go"), &fakeOutliner{}, n)
		var budget *BudgetError
		if !errors.As(err, &budget) || budget.Tokens != n || !strings.Contains(err.Error(), "200") || !strings.Contains(err.Error(), "100000") {
			t.Errorf("Build with %d tokens = %v, want a *BudgetError that names the range", n, err)
		}
	}
	for _, n := range []int{200, 100000} {
		if _, err := Build(context.Background(), entries("a.go"), &fakeOutliner{}, n); err != nil {
			t.Errorf("Build with %d tokens: %v", n, err)
		}
	}
}

// TestRejectedFiles_TS_06_65 verifies TS-06-65:
// files the outliner rejects are listed by name only, not counted as outlined,
// and the build still succeeds.
// Verifies: 06-REQ-9.3
func TestRejectedFiles_TS_06_65(t *testing.T) {
	fake := &fakeOutliner{
		decls: map[string][]Decl{"ok.go": {{"func", "Ok", 2}}},
		errs:  map[string]error{"x.txt": errors.New("unsupported language"), "bad.go": errors.New("parse error")},
	}
	files := []Entry{{"ok.go", 100}, {"x.txt", 100}, {"bad.go", 100}, {"big.go", MaxOutlineBytes + 1}, {"edge.go", MaxOutlineBytes}}

	res, err := Build(context.Background(), files, fake, 4000)

	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"  x.txt\n", "  bad.go\n", "  big.go\n", "  ok.go: func Ok L2\n", "  edge.go\n"} {
		if !strings.Contains(res.Map, line) {
			t.Errorf("map lacks %q:\n%s", line, res.Map)
		}
	}
	if res.FilesOutlined != 2 || res.FilesTotal != 5 || res.Level != 1 {
		t.Errorf("files outlined %d, total %d, level %d; want 2, 5, 1 (edge.go has no declarations and counts)", res.FilesOutlined, res.FilesTotal, res.Level)
	}
	if fake.called("big.go") {
		t.Error("the outliner was called for a file over 1 MiB")
	}
	if !fake.called("edge.go") {
		t.Error("the outliner was not called for a file of exactly 1 MiB")
	}
}

// TestCancellationIsNotARejection guards 06-REQ-9.3: an outliner that stops
// because the context ended is an error of the build, not a file listed by
// name.
func TestCancellationIsNotARejection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fake := &fakeOutliner{errs: map[string]error{"a.go": context.Canceled}}

	_, err := Build(ctx, entries("a.go", "b.go"), fake, 4000)

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Build = %v, want context.Canceled", err)
	}
}

// ladderView is a view with every level of the ladder in it: many directories
// with long names, files and tests with many declarations.
func ladderView() ([]Entry, *fakeOutliner) {
	fake := &fakeOutliner{decls: map[string][]Decl{}}
	var files []Entry
	for d := range 40 {
		dir := fmt.Sprintf("internal/module_%02d/handlers", d)
		for f := range 12 {
			name := fmt.Sprintf("file_%02d.go", f)
			if f%4 == 3 {
				name = fmt.Sprintf("file_%02d_test.go", f)
			}
			path := dir + "/" + name
			files = append(files, Entry{Path: path, Size: 100})
			for k := range 40 {
				fake.decls[path] = append(fake.decls[path], Decl{"func", fmt.Sprintf("Name%02d", k), 10 + k})
			}
		}
	}
	return files, fake
}

// levelAt is the result of the ladder view at the budget.
func levelAt(t *testing.T, budget int) Result {
	t.Helper()
	files, fake := ladderView()
	return mustBuild(t, files, fake, budget)
}

// threshold is the smallest budget at which the ladder is at the level or
// above it (a lower number), found by bisection: the level can only fall as
// the budget rises.
func threshold(t *testing.T, level int) int {
	t.Helper()
	lo, hi := MinTokens, MaxTokens
	if levelAt(t, hi).Level > level {
		return -1
	}
	for lo < hi {
		mid := (lo + hi) / 2
		if levelAt(t, mid).Level <= level {
			hi = mid
		} else {
			lo = mid + 1
		}
	}
	return lo
}

// TestLadder_TS_06_66 verifies TS-06-66:
// each ladder level is chosen when it is the first to fit the budget.
// Verifies: 06-REQ-9.4
func TestLadder_TS_06_66(t *testing.T) {
	more := regexp.MustCompile(`· … \+(\d+) more\n`)
	dirLine := regexp.MustCompile(`^\S+/ \(\d+ files\)$`)
	var firstFit [7]int
	for level := 1; level <= 6; level++ {
		firstFit[level] = threshold(t, level)
		if firstFit[level] < 0 {
			t.Fatalf("level %d is not reached by any budget up to %d", level, MaxTokens)
		}
	}
	if !slices.IsSortedFunc(firstFit[1:], func(a, b int) int { return b - a }) {
		t.Fatalf("the budgets at which the levels begin are %v: a level above must need no more than the one below", firstFit[1:])
	}

	// At the budget that just fits a level, that is the level; one token less,
	// and it is a later one.
	for level := 1; level <= 5; level++ {
		if firstFit[level] == firstFit[level+1] {
			continue // no budget fits this level and not the next
		}
		if got := levelAt(t, firstFit[level]); got.Level != level || got.Truncated != (level > 1) {
			t.Errorf("budget %d: level %d, truncated %v; want level %d", firstFit[level], got.Level, got.Truncated, level)
		}
		if got := levelAt(t, firstFit[level]-1); got.Level <= level {
			t.Errorf("budget %d: level %d, want a level above %d", firstFit[level]-1, got.Level, level)
		}
	}
	if got := levelAt(t, MinTokens); got.Level != 6 {
		t.Errorf("the smallest budget gives level %d, want 6", got.Level)
	}

	// Level 2: no declarations on test files, all on the others.
	l2 := levelAt(t, firstFit[2])
	if l2.Level != 2 || !strings.Contains(l2.Map, "  file_03_test.go\n") || !strings.Contains(l2.Map, "  file_00.go: func Name00 L10 · ") || strings.Contains(l2.Map, "_test.go:") || strings.Contains(l2.Map, "… +") {
		t.Errorf("level 2:\n%.400s", l2.Map)
	}

	// Level 3: the declarations of the others are cut to 20, 10, 5, 2 and 1, in
	// that order as the budget falls, and the tests have none.
	var cuts []int
	for budget := float64(firstFit[2] - 1); budget >= float64(firstFit[3]); budget *= 0.985 {
		got := levelAt(t, int(budget))
		if got.Level != 3 {
			continue
		}
		m := more.FindStringSubmatch(got.Map)
		if m == nil {
			t.Fatalf("budget %d, level 3, but no … +N more:\n%.400s", int(budget), got.Map)
		}
		var n int
		if _, err := fmt.Sscan(m[1], &n); err != nil {
			t.Fatal(err)
		}
		if len(cuts) == 0 || cuts[len(cuts)-1] != n {
			cuts = append(cuts, n)
		}
		if strings.Contains(got.Map, "_test.go:") {
			t.Fatalf("budget %d: a test file has declarations at level 3", int(budget))
		}
	}
	if want := []int{20, 30, 35, 38, 39}; !slices.Equal(cuts, want) {
		t.Errorf("the declarations cut away of a file of 40, as the budget falls: %v, want %v (K = 20, 10, 5, 2, 1)", cuts, want)
	}

	// Level 4: names.
	if l4 := levelAt(t, firstFit[4]); l4.Level != 4 || strings.Contains(l4.Map, ":") || !strings.Contains(l4.Map, "  file_00.go\n") {
		t.Errorf("level 4:\n%.300s", l4.Map)
	}

	// Level 5: directories and their number of files.
	l5 := levelAt(t, firstFit[5])
	lines := strings.Split(strings.TrimSuffix(l5.Map, "\n"), "\n")
	if l5.Level != 5 || len(lines) != 40 || !dirLine.MatchString(lines[0]) || lines[0] != "internal/module_00/handlers/ (12 files)" {
		t.Errorf("level 5: %d lines, first %q", len(lines), lines[0])
	}

	// Level 6: directories cut off, and how many.
	l6 := levelAt(t, MinTokens)
	lines = strings.Split(strings.TrimSuffix(l6.Map, "\n"), "\n")
	last := lines[len(lines)-1]
	var more6 int
	if _, err := fmt.Sscanf(last, "… %d more directories", &more6); err != nil || more6+len(lines)-1 != 40 || more6 == 0 {
		t.Errorf("level 6: last line %q after %d directories; want … N more directories with N + the directories listed = 40", last, len(lines)-1)
	}
	if lines[0] != "internal/module_00/handlers/ (12 files)" {
		t.Errorf("level 6 begins %q", lines[0])
	}
}

// view is a random file view.
func randomView(rng *rand.Rand, n int) ([]Entry, *fakeOutliner) {
	fake := &fakeOutliner{decls: map[string][]Decl{}, errs: map[string]error{}}
	seen := map[string]bool{}
	var files []Entry
	word := func() string {
		letters := "abcdefghijklmnopqrstuvwxyz_-é"
		var b strings.Builder
		for range 3 + rng.IntN(30) {
			b.WriteString(string([]rune(letters)[rng.IntN(len([]rune(letters)))]))
		}
		return b.String()
	}
	for len(files) < n {
		var parts []string
		for range rng.IntN(5) {
			parts = append(parts, word())
		}
		name := word() + []string{".go", "_test.go", ".py", ".txt", ".ts"}[rng.IntN(5)]
		path := strings.Join(append(parts, name), "/")
		if seen[path] {
			continue
		}
		seen[path] = true
		size := int64(rng.IntN(5000))
		if rng.IntN(60) == 0 {
			size = MaxOutlineBytes + 1
		}
		files = append(files, Entry{Path: path, Size: size})
		switch rng.IntN(8) {
		case 0:
			fake.errs[path] = errors.New("rejected")
		default:
			for k := range rng.IntN(50) {
				fake.decls[path] = append(fake.decls[path], Decl{[]string{"func", "type", "method", "class"}[rng.IntN(4)], word(), 1 + k*rng.IntN(7)})
			}
		}
	}
	return files, fake
}

// TestBuildProperty_TS_06_67 verifies TS-06-67:
// the map never exceeds the token budget and is byte-identical for the same
// tree and budget.
// Verifies: 06-REQ-9.5
func TestBuildProperty_TS_06_67(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 67))
	for i := range 40 {
		files, fake := randomView(rng, 1+rng.IntN(3000))
		budget := int(math.Exp(math.Log(MinTokens) + rng.Float64()*(math.Log(MaxTokens)-math.Log(MinTokens))))
		if i < 4 {
			budget = []int{MinTokens, MinTokens + 1, MaxTokens, MaxTokens - 1}[i]
		}

		a := mustBuild(t, files, fake, budget)
		b := mustBuild(t, files, fake, budget)

		if estimate(a.Map) > budget || a.EstimatedTokens != estimate(a.Map) || a.Tokens != budget {
			t.Fatalf("#%d: %d files, budget %d: the map is %d bytes = %d tokens (reported %d)", i, len(files), budget, len(a.Map), estimate(a.Map), a.EstimatedTokens)
		}
		if a.Map != b.Map || a.Level != b.Level || a.FilesOutlined != b.FilesOutlined {
			t.Fatalf("#%d: two builds of the same tree differ", i)
		}
		if a.FilesTotal != len(files) || a.Truncated != (a.Level > 1) || a.Level < 1 || a.Level > 6 {
			t.Fatalf("#%d: result %+v", i, a)
		}
		if !strings.HasSuffix(a.Map, "\n") && a.Map != "" {
			t.Fatalf("#%d: the map does not end in a newline", i)
		}

		// The level can only fall as the budget rises.
		more := budget + 1 + rng.IntN(budget)
		if more > MaxTokens {
			more = MaxTokens
		}
		if bigger := mustBuild(t, files, fake, more); bigger.Level > a.Level {
			t.Fatalf("#%d: budget %d gives level %d and %d gives %d", i, budget, a.Level, more, bigger.Level)
		}
		// The level reported is the first that fits: no level before it does.
		requireFirstFit(t, i, files, fake, a, budget)
		// And the next smaller budget than the map needs is a later level or
		// the same level cut more finely, never an earlier one.
		if need := a.EstimatedTokens; need > MinTokens {
			if smaller := mustBuild(t, files, fake, need-1); smaller.Level < a.Level {
				t.Fatalf("#%d: budget %d gives level %d, but %d gives %d", i, budget, a.Level, need-1, smaller.Level)
			}
		}
	}
}

// TestBuildFirstFitAtEveryBoundary guards 06-REQ-9.5: at the budget that is
// just enough for a level, and at one token less, the level is the first that
// fits. Random budgets seldom land between two levels, which is where an
// engine that skips one goes wrong.
func TestBuildFirstFitAtEveryBoundary(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 6767))
	boundaries := 0
	for i := range 30 {
		files, fake := randomView(rng, 20+rng.IntN(300))
		entries, dirs := group(files)
		if _, err := outlineAll(context.Background(), entries, dirs, fake); err != nil {
			t.Fatal(err)
		}
		var sizes []int
		for _, show := range []shown{allDecls, untestedDecls(-1)} {
			text, _ := render(dirs, MaxTokens*4, show)
			sizes = append(sizes, len(text))
		}
		for _, k := range capsAtLevelThree {
			text, _ := render(dirs, MaxTokens*4, untestedDecls(k))
			sizes = append(sizes, len(text))
		}
		text, _ := render(dirs, MaxTokens*4, nil)
		dirsOnly, _ := renderDirectories(dirs, MaxTokens*4)
		sizes = append(sizes, len(text), len(dirsOnly))

		for _, size := range sizes {
			for _, tokens := range []int{(size + 3) / 4, (size+3)/4 - 1} {
				if tokens < MinTokens || tokens > MaxTokens {
					continue
				}
				boundaries++
				res := mustBuild(t, files, fake, tokens)
				requireFirstFit(t, i, files, fake, res, tokens)
			}
		}
	}
	if boundaries < 100 {
		t.Errorf("only %d budgets at a boundary were tried", boundaries)
	}
}

// requireFirstFit says that none of the levels before the one a build reported
// fits the budget, rendered from the files and the declarations as the build
// had them.
func requireFirstFit(t *testing.T, i int, files []Entry, o Outliner, res Result, tokens int) {
	t.Helper()
	entries, dirs := group(files)
	skipped := len(res.Warnings) > 0
	if !skipped {
		if _, err := outlineAll(context.Background(), entries, dirs, o); err != nil {
			t.Fatal(err)
		}
	}
	budget := tokens * 4
	fits := func(show shown) bool { _, ok := render(dirs, budget, show); return ok }
	levels := []struct {
		level int
		fits  func() bool
	}{
		{1, func() bool { return !skipped && fits(allDecls) }},
		{2, func() bool { return !skipped && fits(untestedDecls(-1)) }},
		{3, func() bool {
			for _, k := range capsAtLevelThree {
				if !skipped && fits(untestedDecls(k)) {
					return true
				}
			}
			return false
		}},
		{4, func() bool { return fits(nil) }},
		{5, func() bool { _, ok := renderDirectories(dirs, budget); return ok }},
	}
	for _, l := range levels {
		switch {
		case l.level < res.Level && l.fits():
			t.Fatalf("#%d: budget %d: level %d fits, but the build reported level %d", i, tokens, l.level, res.Level)
		case l.level == res.Level && !l.fits():
			t.Fatalf("#%d: budget %d: the build reported level %d, which does not fit", i, tokens, l.level)
		}
	}
}

// TestOutlinerIsSkipped_TS_06_68 verifies TS-06-68:
// the outliner is skipped with map_outline_skipped when names alone exceed the
// budget or the view has more than 5000 files.
// Verifies: 06-REQ-9.6
func TestOutlinerIsSkipped_TS_06_68(t *testing.T) {
	long := func(n int) []Entry {
		files := make([]Entry, n)
		for i := range files {
			files[i] = Entry{Path: fmt.Sprintf("some/long/directory/name/number_%04d/file_with_a_long_name_%05d.go", i%50, i), Size: 100}
		}
		return files
	}
	short := func(n int) []Entry {
		files := make([]Entry, n)
		for i := range files {
			files[i] = Entry{Path: fmt.Sprintf("d%d/f%d.go", i%40, i), Size: 100}
		}
		return files
	}

	for name, tc := range map[string]struct {
		files  []Entry
		budget int
	}{
		"A, names alone exceed the budget": {long(3000), 200},
		"B, more than 5000 files":          {short(MaxOutlineFiles + 1), 100000},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeOutliner{}

			res, err := Build(context.Background(), tc.files, fake, tc.budget)

			if err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != 0 || res.FilesOutlined != 0 {
				t.Errorf("the outliner was called %d times, %d files outlined; want none", len(fake.calls), res.FilesOutlined)
			}
			if len(res.Warnings) != 1 || res.Warnings[0].Code != WarnOutlineSkipped || res.Warnings[0].Message == "" {
				t.Errorf("warnings = %+v, want map_outline_skipped with a message", res.Warnings)
			}
			if estimate(res.Map) > tc.budget || res.Level < 4 {
				t.Errorf("level %d, %d tokens for a budget of %d", res.Level, estimate(res.Map), tc.budget)
			}
		})
	}

	// 5000 files are outlined; a small view gives no warning.
	fake := &fakeOutliner{}
	res := mustBuild(t, short(MaxOutlineFiles), fake, 100000)
	if len(fake.calls) != MaxOutlineFiles || len(res.Warnings) != 0 || res.FilesOutlined != MaxOutlineFiles {
		t.Errorf("5000 files: %d calls, warnings %v, outlined %d", len(fake.calls), res.Warnings, res.FilesOutlined)
	}
	if res := mustBuild(t, short(3), &fakeOutliner{}, 200); len(res.Warnings) != 0 {
		t.Errorf("a small view warns: %v", res.Warnings)
	}
}

// TestEmptyView: nothing to map is an empty map that fits any budget.
func TestEmptyView(t *testing.T) {
	res := mustBuild(t, nil, &fakeOutliner{}, MinTokens)

	if res.Map != "" || res.Level != 1 || res.FilesTotal != 0 || res.Truncated || res.EstimatedTokens != 0 {
		t.Errorf("result = %+v", res)
	}
}

// TestLevelSixFitsWhateverTheNames guards 06-REQ-9.5: a directory with a name
// longer than the budget is not printed, and the cut-off line still fits.
func TestLevelSixFitsWhateverTheNames(t *testing.T) {
	huge := strings.Repeat("d", 900) + "/" + strings.Repeat("e", 900)
	files := []Entry{{huge + "/a.go", 10}, {"b/c.go", 10}, {"a/x.go", 10}}

	res := mustBuild(t, files, &fakeOutliner{}, MinTokens)

	if estimate(res.Map) > MinTokens || res.Level != 6 || !strings.HasSuffix(res.Map, "more directories\n") {
		t.Errorf("level %d, %d tokens:\n%.200s", res.Level, estimate(res.Map), res.Map)
	}
	if !strings.Contains(res.Map, "a/ (1 files)\n") {
		t.Errorf("the directories that fit are not listed:\n%s", res.Map)
	}
}

// TestNamesThatAreNotUTF8 guards 06-REQ-9.5: the budget holds for the map as a
// consumer reads it, and a name with bytes that are no text is the same name
// with a replacement character for each, so that the document that carries
// the map does not grow past the budget when it is encoded.
func TestNamesThatAreNotUTF8(t *testing.T) {
	bad := strings.Repeat("\xff\xfe", 400)
	files := []Entry{{bad + "/a.go", 10}, {"b\xffc/d.go", 10}, {"ok/e.go", 10}}

	for _, budget := range []int{MinTokens, 500, 4000} {
		res := mustBuild(t, files, &fakeOutliner{}, budget)

		encoded, err := json.Marshal(res.Map)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(res.Map) || decoded != res.Map {
			t.Errorf("budget %d: the map is not text that survives encoding: %.80q", budget, res.Map)
		}
		if estimate(decoded) > budget {
			t.Errorf("budget %d: the map decoded is %d tokens", budget, estimate(decoded))
		}
	}
}

// TestHandsOutliner guards 06-REQ-9.3: the adapter reads the declarations of
// the hands' outline, names a method with its type, and rejects what the hands
// cannot outline.
func TestHandsOutliner(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("pkg/a.go", "package pkg\n\nfunc A() {}\n\ntype T struct{}\n\nfunc (T) M() {}\n")
	write("notes.txt", "hello\n")
	write("bad.go", "package \x00\x00")
	write("empty.go", "package empty\n")
	o := NewOutliner(root)

	decls, err := o.Outline(context.Background(), "pkg/a.go")
	if err != nil {
		t.Fatal(err)
	}
	want := []Decl{{"func", "A", 3}, {"type", "T", 5}, {"method", "T.M", 7}}
	if !slices.Equal(decls, want) {
		t.Errorf("declarations = %v, want %v", decls, want)
	}

	if decls, err := o.Outline(context.Background(), "empty.go"); err != nil || len(decls) != 0 {
		t.Errorf("a Go file with nothing declared = %v, %v; want no declarations and no error", decls, err)
	}
	for _, path := range []string{"notes.txt", "bad.go", "missing.go"} {
		if _, err := o.Outline(context.Background(), path); err == nil {
			t.Errorf("%s was outlined", path)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := o.Outline(ctx, "pkg/a.go"); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context = %v, want context.Canceled", err)
	}

	// Through Build, with a file the hands reject in the view.
	res := mustBuild(t, entries("pkg/a.go", "notes.txt"), o, 4000)
	if res.FilesOutlined != 1 || !strings.Contains(res.Map, "  a.go: func A L3 · type T L5 · method T.M L7\n") || !strings.Contains(res.Map, "  notes.txt\n") {
		t.Errorf("result = %+v\n%s", res, res.Map)
	}
}
