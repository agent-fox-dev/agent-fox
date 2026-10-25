package repomap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestBuild_ListsDirectoriesFilesAndDeclarations: the map's format, and the
// result that describes it.
func TestBuild_ListsDirectoriesFilesAndDeclarations(t *testing.T) {
	fake := &fakeOutliner{decls: map[string][]Decl{"a.go": {{"func", "A", 3}, {"type", "T", 10}}, "pkg/b.go": {{"func", "B", 1}}}}

	res := mustBuild(t, entries("pkg/b_test.go", "a.go", "pkg/b.go"), fake, 4000)

	want := Result{Map: "./\n  a.go: func A L3 · type T L10\npkg/\n  b.go: func B L1\n  b_test.go\n", Tokens: 4000, Level: 1, FilesTotal: 3, FilesOutlined: 3}
	want.EstimatedTokens = estimate(want.Map)
	if !reflect.DeepEqual(res, want) {
		t.Errorf("result = %+v, want %+v", res, want)
	}
}

// TestBuild_OrdersTheTreeWhateverTheInputOrder: the root first, a directory
// before its subdirectories, files by name, a path given twice once.
func TestBuild_OrdersTheTreeWhateverTheInputOrder(t *testing.T) {
	paths := []string{"z.go", "b/y.go", "b/a.go", "a/x/f.go", "a-b/g.go", "a/f.go", "b/c/d.go", "A.go", "é/x.go", "z.go", ""}
	want := "./\n  A.go\n  z.go\na/\n  f.go\na/x/\n  f.go\na-b/\n  g.go\nb/\n  a.go\n  y.go\nb/c/\n  d.go\né/\n  x.go\n"
	rng := rand.New(rand.NewPCG(1, 2))
	for range 5 {
		res := mustBuild(t, entries(paths...), &fakeOutliner{}, 4000)
		if res.Map != want || res.FilesTotal != 9 {
			t.Errorf("map of %q = %q (%d files), want %q", paths, res.Map, res.FilesTotal, want)
		}
		rng.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
	}
}

func TestBuild_RefusesABudgetOutsideTheRange(t *testing.T) {
	for _, n := range []int{-1, 0, 199, 100001} {
		_, err := Build(context.Background(), entries("a.go"), &fakeOutliner{}, n)
		var budget *BudgetError
		if !errors.As(err, &budget) || budget.Tokens != n || !strings.Contains(err.Error(), "200") || !strings.Contains(err.Error(), "100000") {
			t.Errorf("Build with %d tokens = %v, want a *BudgetError that names the range", n, err)
		}
	}
	for _, n := range []int{MinTokens, MaxTokens} {
		if _, err := Build(context.Background(), entries("a.go"), &fakeOutliner{}, n); err != nil {
			t.Errorf("Build with %d tokens: %v", n, err)
		}
	}
}

// TestBuild_ListsAFileItCannotOutlineByName: one the outliner rejects, or over
// the size that is outlined, which it is not asked about; the build goes on.
func TestBuild_ListsAFileItCannotOutlineByName(t *testing.T) {
	fake := &fakeOutliner{
		decls: map[string][]Decl{"ok.go": {{"func", "Ok", 2}}},
		errs:  map[string]error{"x.txt": errors.New("unsupported language"), "bad.go": errors.New("parse error")},
	}
	files := []Entry{{"ok.go", 100}, {"x.txt", 100}, {"bad.go", 100}, {"big.go", MaxOutlineBytes + 1}, {"edge.go", MaxOutlineBytes}}

	res := mustBuild(t, files, fake, 4000)

	if want := "./\n  bad.go\n  big.go\n  edge.go\n  ok.go: func Ok L2\n  x.txt\n"; res.Map != want {
		t.Errorf("map = %q, want %q", res.Map, want)
	}
	// edge.go has no declarations, and counts as outlined.
	if res.FilesOutlined != 2 || res.FilesTotal != 5 || res.Level != 1 || fake.called("big.go") || !fake.called("edge.go") {
		t.Errorf("outlined %d of %d at level %d, calls %v", res.FilesOutlined, res.FilesTotal, res.Level, fake.calls)
	}
}

// TestBuild_AnEndedContextIsTheErrorOfTheBuild: not a file listed by name.
func TestBuild_AnEndedContextIsTheErrorOfTheBuild(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Build(ctx, entries("a.go", "b.go"), &fakeOutliner{errs: map[string]error{"a.go": context.Canceled}}, 4000); !errors.Is(err, context.Canceled) {
		t.Errorf("Build = %v, want context.Canceled", err)
	}
}

// TestBuild_TakesTheFirstLevelOfTheLadderThatFits: at the budget that just
// fits each rung, and one token less, and what each level shows.
func TestBuild_TakesTheFirstLevelOfTheLadderThatFits(t *testing.T) {
	files, fake := ladderView()
	ladder := ladderOf(t, files, fake)
	at := map[int]Result{} // the map at the budget that just fits each rung, by its cap or level
	for _, r := range ladder {
		tokens := (r.bytes + 3) / 4
		if tokens > MaxTokens {
			t.Fatalf("rung %+v does not fit any budget", r)
		}
		res := requireLadder(t, files, fake, ladder, tokens)
		if res.Level != r.level {
			t.Fatalf("budget %d just fits rung %+v, but the level is %d", tokens, r, res.Level)
		}
		at[r.cap*10+r.level] = res
		if tokens > MinTokens {
			if less := requireLadder(t, files, fake, ladder, tokens-1); less.Level < r.level {
				t.Errorf("budget %d: level %d, before the %d of a larger budget", tokens-1, less.Level, r.level)
			}
		}
	}

	if l2 := at[2].Map; !strings.Contains(l2, "  file_03_test.go\n") || !strings.Contains(l2, "  file_00.go: func Name00 L10 · ") || strings.Contains(l2, "_test.go:") || strings.Contains(l2, "… +") {
		t.Errorf("level 2, declarations of the files that are not tests:\n%.400s", l2)
	}
	for _, k := range capsAtLevelThree {
		l3 := at[k*10+3].Map
		if !strings.Contains(l3, fmt.Sprintf(" L%d · … +%d more\n", 10+k-1, 40-k)) || strings.Contains(l3, "_test.go:") {
			t.Errorf("level 3 with a cap of %d:\n%.400s", k, l3)
		}
	}
	if l4 := at[4].Map; strings.Contains(l4, ":") || !strings.Contains(l4, "internal/module_00/handlers/\n  file_00.go\n") {
		t.Errorf("level 4, names:\n%.300s", l4)
	}
	if lines := strings.Split(strings.TrimSuffix(at[5].Map, "\n"), "\n"); len(lines) != 40 || lines[0] != "internal/module_00/handlers/ (12 files)" {
		t.Errorf("level 5, directories: %d lines, first %q", len(lines), lines[0])
	}

	l6 := requireLadder(t, files, fake, ladder, MinTokens)
	lines := strings.Split(strings.TrimSuffix(l6.Map, "\n"), "\n")
	var more int
	if _, err := fmt.Sscanf(lines[len(lines)-1], "… %d more directories", &more); err != nil || more == 0 || more+len(lines)-1 != 40 || lines[0] != "internal/module_00/handlers/ (12 files)" || l6.Level != 6 {
		t.Errorf("level 6: %q ... %q after %d directories", lines[0], lines[len(lines)-1], len(lines)-1)
	}
}

// TestBuild_NeverExceedsTheBudgetAndIsTheSameForTheSameTree: over random
// views, at a random budget and at every boundary of the ladder, the map fits
// and is the first level that does, and building again gives the same bytes.
func TestBuild_NeverExceedsTheBudgetAndIsTheSameForTheSameTree(t *testing.T) {
	rng := rand.New(rand.NewPCG(6, 67))
	tried := 0
	for i := range 16 {
		files, fake := randomView(rng, 1+rng.IntN(160))
		ladder := ladderOf(t, files, fake)
		budget := int(math.Exp(math.Log(MinTokens) + rng.Float64()*(math.Log(MaxTokens)-math.Log(MinTokens))))
		if i < 4 {
			budget = []int{MinTokens, MinTokens + 1, MaxTokens, MaxTokens - 1}[i]
		}

		a := requireLadder(t, files, fake, ladder, budget)
		if b := mustBuild(t, files, fake, budget); !reflect.DeepEqual(a, b) {
			t.Fatalf("#%d: two builds of the same tree differ", i)
		}
		for _, tokens := range boundaries(ladder) {
			requireLadder(t, files, fake, ladder, tokens)
			tried++
		}
	}
	if tried < 100 {
		t.Errorf("only %d budgets at a boundary were tried", tried)
	}
}

// TestBuild_SkipsTheOutlinerWhenItCannotHelp: when the names alone do not fit
// or the view has more than MaxOutlineFiles files, no file is outlined and the
// result says so; at MaxOutlineFiles every file is.
func TestBuild_SkipsTheOutlinerWhenItCannotHelp(t *testing.T) {
	view := func(n int, format string) []Entry {
		files := make([]Entry, n)
		for i := range files {
			files[i] = Entry{Path: fmt.Sprintf(format, i%50, i), Size: 100}
		}
		return files
	}
	for name, tc := range map[string]struct {
		files  []Entry
		budget int
	}{
		"names alone exceed the budget": {view(3000, "some/long/directory/name/number_%04d/file_with_a_long_name_%05d.go"), MinTokens},
		"more files than are outlined":  {view(MaxOutlineFiles+1, "d%d/f%d.go"), MaxTokens},
	} {
		fake := &fakeOutliner{}
		res := mustBuild(t, tc.files, fake, tc.budget)
		if len(fake.calls) != 0 || res.FilesOutlined != 0 || len(res.Warnings) != 1 || res.Warnings[0].Code != WarnOutlineSkipped || res.Warnings[0].Message == "" ||
			estimate(res.Map) > tc.budget || res.Level < 4 {
			t.Errorf("%s: %d calls, level %d, %d tokens, %+v", name, len(fake.calls), res.Level, estimate(res.Map), res)
		}
	}

	fake := &fakeOutliner{}
	if res := mustBuild(t, view(MaxOutlineFiles, "d%d/f%d.go"), fake, MaxTokens); len(fake.calls) != MaxOutlineFiles || len(res.Warnings) != 0 || res.FilesOutlined != MaxOutlineFiles {
		t.Errorf("%d files: %d calls, warnings %v, outlined %d", MaxOutlineFiles, len(fake.calls), res.Warnings, res.FilesOutlined)
	}
}

func TestBuild_AnEmptyViewIsAnEmptyMap(t *testing.T) {
	if res := mustBuild(t, nil, &fakeOutliner{}, MinTokens); !reflect.DeepEqual(res, Result{Tokens: MinTokens, Level: 1}) {
		t.Errorf("result = %+v", res)
	}
}

// TestBuild_TheLastLevelFitsWhateverTheNames: a directory whose name is
// longer than the budget is left out, and the line that says so still fits.
func TestBuild_TheLastLevelFitsWhateverTheNames(t *testing.T) {
	huge := strings.Repeat("d", 900) + "/" + strings.Repeat("e", 900)
	res := mustBuild(t, []Entry{{huge + "/a.go", 10}, {"b/c.go", 10}, {"a/x.go", 10}}, &fakeOutliner{}, MinTokens)

	if res.Map != "a/ (1 files)\nb/ (1 files)\n… 1 more directories\n" || res.Level != 6 {
		t.Errorf("level %d:\n%.200s", res.Level, res.Map)
	}
	// With no room even for that line, the map is empty.
	if got := cutDirectories([]directory{{path: huge}}, 10); got != "" {
		t.Errorf("cutDirectories = %q, want nothing", got)
	}
}

// TestBuild_WritesANameAsTextOnOneLine: a byte that is no text is a
// replacement character, and a control character or line separator a question
// mark, so that the map is text, keeps its lines, and stays within the budget
// once the document that carries it is encoded.
func TestBuild_WritesANameAsTextOnOneLine(t *testing.T) {
	fake := &fakeOutliner{decls: map[string][]Decl{"ok/e.go": {{"func\n", "A\u2028B\tC", 1}}}}
	files := []Entry{{strings.Repeat("\xff\xfe", 400) + "/a.go", 10}, {"b\xffc/d.go", 10}, {"ok/e.go", 10}, {"ok/new\nline\x1b.go", 10}}

	if res := mustBuild(t, files, fake, 4000); strings.Count(res.Map, "\n") != 7 || !strings.Contains(res.Map, "b\uFFFDc/\n") || !strings.Contains(res.Map, "  e.go: func? A?B?C L1\n") || !strings.Contains(res.Map, "  new?line?.go\n") {
		t.Errorf("map = %q", res.Map)
	}
	for _, budget := range []int{MinTokens, 500, 4000} {
		res := mustBuild(t, files, fake, budget)
		encoded, err := json.Marshal(res.Map)
		if err != nil {
			t.Fatal(err)
		}
		var decoded string
		if err := json.Unmarshal(encoded, &decoded); err != nil || !utf8.ValidString(res.Map) || decoded != res.Map || estimate(decoded) > budget {
			t.Errorf("budget %d: the map is not text within the budget once encoded: %.80q", budget, res.Map)
		}
	}
}
