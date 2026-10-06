package repomap

import (
	"context"
	"slices"

	"github.com/agentfox/agentkit-go/tools"
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

// BuildFunc has the signature of Build. Pipelines hold one so tests can
// replace the walk with a spy or a failure.
type BuildFunc func(ctx context.Context, ws *tools.Workspace, budget int, inputPaths []string) (string, error)

// Refresher hands fix and impl the map for each phase: it builds before the
// first phase and rebuilds before a later one only when the tree changed since
// the last build (14-REQ-9.1, 14-REQ-9.2). One Refresher serves one run.
type Refresher struct {
	ws      *tools.Workspace
	budget  int
	build   BuildFunc
	tree    *TreeChangeDetector
	last    string
	hasLast bool
}

// NewRefresher returns a Refresher that builds with budget over ws and asks
// state whether the tree moved. A nil build means Build; a nil state means
// every call rebuilds, because the change check cannot run (14-REQ-9.4).
func NewRefresher(ws *tools.Workspace, budget int, build BuildFunc, state TreeState) *Refresher {
	if build == nil {
		build = Build
	}
	return &Refresher{ws: ws, budget: budget, build: build, tree: NewTreeChangeDetector(state)}
}

// Get returns the map for the next phase. inputPaths steer a rebuild only; a
// reused map keeps the hints it was built with, because new hints alone are
// not a tree change and the spec rebuilds on tree change only.
//
// On a build error it returns "" and the error and remembers nothing, so the
// phase runs without a map and the next phase tries again. The caller turns
// the error into a warning: the map never fails a run (14-REQ-10.1).
func (r *Refresher) Get(ctx context.Context, inputPaths []string) (string, error) {
	if r.hasLast {
		// A zero budget builds nothing, so there is nothing to refresh and no
		// reason to ask git.
		if r.budget <= 0 {
			return r.last, nil
		}
		if changed, _ := r.tree.Changed(ctx); !changed {
			return r.last, nil
		}
	}
	m, err := r.build(ctx, r.ws, r.budget, inputPaths)
	if err != nil {
		r.hasLast, r.last = false, ""
		return "", err
	}
	r.tree.Record(ctx)
	r.last, r.hasLast = m, true
	return m, nil
}
