package repomap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildDirIsTheMapOfTheFilesOfADirectory guards the map of a fix run: the
// same map as Build gives for the sizes of the files, within the budget.
func TestBuildDirIsTheMapOfTheFilesOfADirectory(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := range 40 {
		name := fmt.Sprintf("pkg%02d/file.go", i)
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		src := fmt.Sprintf("package p%d\n\n// Do does.\nfunc Do%d() {}\n\nfunc Other%d() {}\n", i, i, i)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, name)
	}

	res, err := BuildDir(context.Background(), dir, files, 4000)

	if err != nil || res.FilesTotal != 40 || !strings.Contains(res.Map, "Do0()") && !strings.Contains(res.Map, "Do0") {
		t.Fatalf("BuildDir = %+v, %v, want the map of 40 files with their declarations", res, err)
	}
	entries := make([]Entry, len(files))
	for i, f := range files {
		info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f)))
		if err != nil {
			t.Fatal(err)
		}
		entries[i] = Entry{Path: f, Size: info.Size()}
	}
	want, err := Build(context.Background(), entries, NewOutliner(dir), 4000)
	if err != nil || want.Map != res.Map {
		t.Errorf("the map differs from Build's over the same sizes:\n%s\nvs\n%s", res.Map, want.Map)
	}

	// A file over the size that is outlined is listed by name only.
	big := filepath.Join(dir, "big.go")
	if err := os.WriteFile(big, []byte("package big\n\nfunc Big() {}\n"+strings.Repeat("// padding\n", MaxOutlineBytes/8)), 0o644); err != nil {
		t.Fatal(err)
	}
	withBig, err := BuildDir(context.Background(), dir, append([]string{"big.go"}, files...), 4000)
	if err != nil || withBig.FilesTotal != 41 || withBig.FilesOutlined != 40 || strings.Contains(withBig.Map, "Big()") {
		t.Errorf("with a file of %d bytes: %d files, %d outlined (%v), want 41 and 40 and the big file by name", MaxOutlineBytes*2, withBig.FilesTotal, withBig.FilesOutlined, err)
	}

	small, err := BuildDir(context.Background(), dir, files, 400)
	if err != nil || small.EstimatedTokens > 400 || len(small.Map) >= len(res.Map) {
		t.Errorf("a budget of 400: %d tokens (%d bytes), %v, want it within the budget and smaller", small.EstimatedTokens, len(small.Map), err)
	}
	var budget *BudgetError
	if _, err := BuildDir(context.Background(), dir, files, 1); err == nil || !asBudget(err, &budget) {
		t.Errorf("a budget of 1: %v, want the budget error", err)
	}
	if none, err := BuildDir(context.Background(), dir, nil, 4000); err != nil || none.FilesTotal != 0 {
		t.Errorf("no files: %+v, %v", none, err)
	}
}

func asBudget(err error, target **BudgetError) bool {
	b, ok := err.(*BudgetError)
	if ok {
		*target = b
	}
	return ok
}
