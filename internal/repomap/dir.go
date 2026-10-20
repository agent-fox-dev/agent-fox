package repomap

import (
	"context"
	"os"
	"path/filepath"
)

// BuildDir is the map of the files of the directory dir, which are the slash-separated
// paths in files, within the budget of tokens. It reads the size of each file, as
// Build wants, unless there are more files than are outlined, where no file is
// read and none is outlined.
func BuildDir(ctx context.Context, dir string, files []string, tokens int) (Result, error) {
	entries := make([]Entry, len(files))
	for i, f := range files {
		entries[i] = Entry{Path: f, Size: -1}
		if len(files) <= MaxOutlineFiles {
			if info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
				entries[i].Size = info.Size()
			}
		}
	}
	return Build(ctx, entries, NewOutliner(dir), tokens)
}
