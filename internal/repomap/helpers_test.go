package repomap

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeOutliner answers from tables and records its calls.
type fakeOutliner struct {
	mu    sync.Mutex
	decls map[string][]Decl
	errs  map[string]error
	calls []string
}

func (f *fakeOutliner) Outline(_ context.Context, path string) ([]Decl, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, path)
	if err := f.errs[path]; err != nil {
		return nil, err
	}
	return f.decls[path], nil
}

func (f *fakeOutliner) called(path string) bool { return slices.Contains(f.calls, path) }

// entries are the entries of paths, each of 100 bytes.
func entries(paths ...string) []Entry {
	out := make([]Entry, len(paths))
	for i, p := range paths {
		out[i] = Entry{Path: p, Size: 100}
	}
	return out
}

// estimate is the tokens of a text: a token for each 4 bytes, rounded up.
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

// ladderView is a view in which every level of the ladder is reached: 40
// directories with long names, of 12 files each, a test in every 4, and 40
// declarations in each file.
func ladderView() ([]Entry, *fakeOutliner) {
	fake := &fakeOutliner{decls: map[string][]Decl{}}
	var files []Entry
	for d := range 40 {
		for f := range 12 {
			name := fmt.Sprintf("internal/module_%02d/handlers/file_%02d.go", d, f)
			if f%4 == 3 {
				name = strings.TrimSuffix(name, ".go") + "_test.go"
			}
			files = append(files, Entry{Path: name, Size: 100})
			for k := range 40 {
				fake.decls[name] = append(fake.decls[name], Decl{"func", fmt.Sprintf("Name%02d", k), 10 + k})
			}
		}
	}
	return files, fake
}

// randomView is a view of n files at random depths, with random names (some
// not ASCII), sizes (some over what is outlined) and declarations (some files
// rejected).
func randomView(rng *rand.Rand, n int) ([]Entry, *fakeOutliner) {
	fake := &fakeOutliner{decls: map[string][]Decl{}, errs: map[string]error{}}
	letters := []rune("abcdefghijklmnopqrstuvwxyz_-é")
	word := func() string {
		var b strings.Builder
		for range 3 + rng.IntN(30) {
			b.WriteRune(letters[rng.IntN(len(letters))])
		}
		return b.String()
	}
	seen := map[string]bool{}
	var files []Entry
	for len(files) < n {
		var parts []string
		for range rng.IntN(5) {
			parts = append(parts, word())
		}
		path := strings.Join(append(parts, word()+[]string{".go", "_test.go", ".py", ".txt", ".ts"}[rng.IntN(5)]), "/")
		if seen[path] {
			continue
		}
		seen[path] = true
		size := int64(rng.IntN(5000))
		if rng.IntN(60) == 0 {
			size = MaxOutlineBytes + 1
		}
		files = append(files, Entry{Path: path, Size: size})
		if rng.IntN(8) == 0 {
			fake.errs[path] = errors.New("rejected")
			continue
		}
		for k := range rng.IntN(50) {
			fake.decls[path] = append(fake.decls[path], Decl{[]string{"func", "type", "method", "class"}[rng.IntN(4)], word(), 1 + k*rng.IntN(7)})
		}
	}
	return files, fake
}

// rung is a step of the ladder: a level, the cap of declarations it keeps at
// level 3 (or 0), and the bytes of the map it renders when nothing limits it.
type rung struct {
	level, cap, bytes int
}

// ladderOf is the ladder of a view, rendered once from the files and the
// declarations as a build reads them, so that the level a build should take at
// any budget is known without building.
func ladderOf(t *testing.T, files []Entry, o Outliner) []rung {
	t.Helper()
	all, dirs := group(files)
	if _, err := outlineAll(context.Background(), all, dirs, o); err != nil {
		t.Fatal(err)
	}
	size := func(show shown) int {
		text, _ := render(dirs, 1<<30, show)
		return len(text)
	}
	rungs := []rung{{1, 0, size(allDecls)}, {2, 0, size(untestedDecls(-1))}}
	for _, k := range capsAtLevelThree {
		rungs = append(rungs, rung{3, k, size(untestedDecls(k))})
	}
	dirsOnly, _ := renderDirectories(dirs, 1<<30)
	return append(rungs, rung{4, 0, size(nil)}, rung{5, 0, len(dirsOnly)})
}

// expectedLevel is the first rung of the ladder that fits the budget: when
// the outliner was skipped, of the rungs that show no declarations.
func expectedLevel(ladder []rung, tokens int, skipped bool) int {
	for _, r := range ladder {
		if (!skipped || r.level >= 4) && r.bytes <= tokens*4 {
			return r.level
		}
	}
	return 6
}

// requireLadder builds the view at the budget, and fails the test unless the
// map fits it, says so, and is at the first level that fits.
func requireLadder(t *testing.T, files []Entry, o Outliner, ladder []rung, tokens int) Result {
	t.Helper()
	res := mustBuild(t, files, o, tokens)
	skipped := len(res.Warnings) > 0
	names := ladder[len(ladder)-2].bytes
	switch {
	case estimate(res.Map) > tokens || res.EstimatedTokens != estimate(res.Map) || res.Tokens != tokens:
		t.Fatalf("%d files, budget %d: the map is %d tokens (reported %d)", len(files), tokens, estimate(res.Map), res.EstimatedTokens)
	case skipped != (names > tokens*4 || len(files) > MaxOutlineFiles):
		t.Fatalf("%d files, budget %d: names of %d bytes, warnings %v", len(files), tokens, names, res.Warnings)
	case res.Level != expectedLevel(ladder, tokens, skipped) || res.Truncated != (res.Level > 1):
		t.Fatalf("%d files, budget %d: level %d (truncated %v), want the first that fits, %d", len(files), tokens, res.Level, res.Truncated, expectedLevel(ladder, tokens, skipped))
	case res.FilesTotal != len(files) || res.Map != "" && !strings.HasSuffix(res.Map, "\n"):
		t.Fatalf("%d files, budget %d: %+v", len(files), tokens, res)
	}
	return res
}

// boundaries are the budgets at which a rung of the ladder just fits, and one
// token less, within the range of budgets.
func boundaries(ladder []rung) []int {
	var out []int
	for _, r := range ladder {
		for _, tokens := range []int{(r.bytes + 3) / 4, (r.bytes+3)/4 - 1} {
			if tokens >= MinTokens && tokens <= MaxTokens {
				out = append(out, tokens)
			}
		}
	}
	return out
}
