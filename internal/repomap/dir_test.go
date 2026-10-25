package repomap

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestBuildDir_IsTheMapOfTheFilesWithTheirSizes: the map Build gives for the
// files of the directory and their sizes, so that a file over the size that is
// outlined is listed by name only.
func TestBuildDir_IsTheMapOfTheFilesWithTheirSizes(t *testing.T) {
	dir := t.TempDir()
	var files []string
	for i := range 40 {
		name := fmt.Sprintf("pkg%02d/file.go", i)
		testutil.WriteFile(t, dir, name, fmt.Sprintf("package p%d\n\nfunc Do%d() {}\n\nfunc Other%d() {}\n", i, i, i))
		files = append(files, name)
	}
	testutil.WriteFile(t, dir, "big.go", "package big\n\nfunc Big() {}\n"+strings.Repeat("// padding\n", MaxOutlineBytes/8))
	files = append(files, "big.go", "gone.go")
	ctx := context.Background()

	res, err := BuildDir(ctx, dir, files, 4000)

	entries := make([]Entry, len(files))
	for i, f := range files {
		entries[i] = Entry{Path: f, Size: -1}
		if info, err := os.Lstat(filepath.Join(dir, f)); err == nil {
			entries[i].Size = info.Size()
		}
	}
	want, _ := Build(ctx, entries, NewOutliner(dir), 4000)
	if err != nil || res.Map != want.Map || res.FilesTotal != 42 || res.FilesOutlined != 40 || !strings.Contains(res.Map, "func Do0 L3") || strings.Contains(res.Map, "Big") {
		t.Errorf("BuildDir = %+v, %v; want the map of Build over the sizes, %q", res, err, want.Map)
	}

	if small, err := BuildDir(ctx, dir, files, 400); err != nil || small.EstimatedTokens > 400 || len(small.Map) >= len(res.Map) {
		t.Errorf("a budget of 400: %+v, %v; want a smaller map within it", small, err)
	}
	var budget *BudgetError
	if _, err := BuildDir(ctx, dir, files, 1); !errors.As(err, &budget) {
		t.Errorf("a budget of 1: %v, want the budget error", err)
	}
	if none, err := BuildDir(ctx, dir, nil, 4000); err != nil || none.FilesTotal != 0 {
		t.Errorf("no files: %+v, %v", none, err)
	}
}
