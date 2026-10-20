// Package checks measures a tree: the files that make it up, a hash of them,
// and what the project's checks said about it.
package checks

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// skippedDirs are the directories a walk outside a work tree leaves out, at
// any depth.
var skippedDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true,
	"target": true, "dist": true, "build": true, "__pycache__": true, ".venv": true, "venv": true,
}

// FileView lists the files of the tree under dir, slash-separated, relative
// to dir and sorted byte-wise: the one view the tree hash, language detection
// and the map share.
//
// In a git work tree it is git's own view: the tracked files and the untracked
// files that are not ignored, read NUL-separated so that no name is quoted,
// less the files that were deleted from the working tree. Anywhere else it is
// a walk of dir that skips the directories of skippedDirs. Both leave out
// .git/ and .agent-fox/.
func FileView(ctx context.Context, dir string) ([]string, error) {
	g := gitx.New(dir, gitx.ExecRunner)
	if g.IsRepo(ctx) {
		return gitView(ctx, g, dir)
	}
	return walkView(ctx, dir)
}

// gitView is git's view of dir.
func gitView(ctx context.Context, g *gitx.Git, dir string) ([]string, error) {
	listed, err := g.ListFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("file view: %w", err)
	}
	view := make([]string, 0, len(listed))
	for _, p := range listed {
		// A nested repository is listed as a directory, with a slash.
		p = strings.TrimSuffix(p, "/")
		if p == "" || reserved(p) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p))); errors.Is(err, fs.ErrNotExist) {
			continue // tracked, and deleted from the working tree
		}
		view = append(view, p)
	}
	slices.Sort(view)
	return slices.Compact(view), nil
}

// reserved reports whether p is, or is under, a directory that af and git own
// at the top of the tree.
func reserved(p string) bool {
	for _, name := range []string{".git", agentdir.Name} {
		if p == name || strings.HasPrefix(p, name+"/") {
			return true
		}
	}
	return false
}

// walkView is the walk of dir.
func walkView(ctx context.Context, dir string) ([]string, error) {
	root := filepath.Clean(dir)
	var view []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir // a directory that cannot be read is left out
			}
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && (skippedDirs[d.Name()] || rel == agentdir.Name) {
				return fs.SkipDir
			}
			return nil
		}
		view = append(view, rel)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("file view: %w", err)
	}
	slices.Sort(view)
	return view, nil
}
