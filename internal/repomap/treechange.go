package repomap

import (
	"context"
	"slices"
)

// TreeState is the slice of gitx.Git the tree-change detector needs. *gitx.Git
// satisfies it; tests supply a fake.
type TreeState interface {
	Head(ctx context.Context) (string, error)
	DirtyFiles(ctx context.Context) ([]string, error)
}

// treeSnapshot is HEAD plus the sorted dirty-file lines at one moment.
type treeSnapshot struct {
	head  string
	dirty []string
}

// TreeChangeDetector tells fix and impl whether the tree changed since the
// last map build (14-REQ-9.3). HEAD moves on every commit, checkout and reset,
// and the dirty set moves on every write, so the pair catches every mutation
// those tools make without hashing file contents. The check is conservative: a
// false positive only rebuilds a map that did not need it.
type TreeChangeDetector struct {
	git      TreeState
	recorded *treeSnapshot
}

// NewTreeChangeDetector returns a detector reading state from git.
func NewTreeChangeDetector(git TreeState) *TreeChangeDetector {
	return &TreeChangeDetector{git: git}
}

// snapshot reads the current state; it reports false when either git call
// fails.
func (d *TreeChangeDetector) snapshot(ctx context.Context) (treeSnapshot, bool) {
	if d == nil || d.git == nil {
		return treeSnapshot{}, false
	}
	head, err := d.git.Head(ctx)
	if err != nil {
		return treeSnapshot{}, false
	}
	dirty, err := d.git.DirtyFiles(ctx)
	if err != nil {
		return treeSnapshot{}, false
	}
	dirty = slices.Clone(dirty)
	slices.Sort(dirty)
	return treeSnapshot{head: head, dirty: dirty}, true
}

// Record stores the current HEAD and sorted dirty files as the state the map
// was built from. When git cannot be read nothing is recorded, so the next
// Changed reports true (14-REQ-9.4).
func (d *TreeChangeDetector) Record(ctx context.Context) {
	if d == nil {
		return
	}
	if s, ok := d.snapshot(ctx); ok {
		d.recorded = &s
		return
	}
	d.recorded = nil
}

// Changed reports whether the tree differs from the last Record. When either
// git call fails, or nothing was recorded, it returns true with a nil error:
// the caller rebuilds unconditionally and no error reaches the user
// (14-REQ-9.4). The error result is kept so callers can treat the check as
// fallible, but it is always nil.
func (d *TreeChangeDetector) Changed(ctx context.Context) (bool, error) {
	if d == nil || d.recorded == nil {
		return true, nil
	}
	cur, ok := d.snapshot(ctx)
	if !ok {
		return true, nil
	}
	return cur.head != d.recorded.head || !slices.Equal(cur.dirty, d.recorded.dirty), nil
}
