package repomap

import (
	"context"
	"errors"
	"testing"

	"github.com/agentfox/agentkit-go/tools"
)

// fakeGit is a TreeState whose answers a test can change between calls.
type fakeGit struct {
	head      string
	dirty     []string
	headErr   error
	dirtyErr  error
	headCalls int
}

func (f *fakeGit) Head(context.Context) (string, error) {
	f.headCalls++
	return f.head, f.headErr
}

func (f *fakeGit) DirtyFiles(context.Context) ([]string, error) {
	return append([]string(nil), f.dirty...), f.dirtyErr
}

// TS-14-31: tree change is detected by comparing HEAD and dirty files against
// the values recorded at the last build (14-REQ-9.3).
func TestTS14_31_TreeChangeDetected(t *testing.T) {
	ctx := context.Background()
	g := &fakeGit{head: "abc123", dirty: []string{" M a.go"}}
	d := NewTreeChangeDetector(g)
	d.Record(ctx)

	changed, err := d.Changed(ctx)
	if err != nil || changed {
		t.Fatalf("unchanged tree: changed=%v err=%v, want false, nil", changed, err)
	}

	g.head = "def456"
	g.dirty = []string{" M a.go", " M b.go"}
	changed, err = d.Changed(ctx)
	if err != nil || !changed {
		t.Fatalf("HEAD and dirty set differ: changed=%v err=%v, want true, nil", changed, err)
	}

	// Only the dirty set differs.
	g.head = "abc123"
	d.Record(ctx)
	g.dirty = []string{" M a.go", " M b.go", "?? c.go"}
	if changed, _ := d.Changed(ctx); !changed {
		t.Error("dirty set grew with HEAD unchanged: want changed")
	}

	// The order git reports dirty files in does not matter.
	d.Record(ctx)
	g.dirty = []string{"?? c.go", " M b.go", " M a.go"}
	if changed, _ := d.Changed(ctx); changed {
		t.Error("same dirty set in another order: want unchanged")
	}
}

// TS-14-32: when the tree-change check fails, Changed reports true with no
// error so the map is rebuilt unconditionally (14-REQ-9.4).
func TestTS14_32_CheckFailureRebuilds(t *testing.T) {
	ctx := context.Background()
	g := &fakeGit{headErr: errors.New("not a git repo")}
	d := NewTreeChangeDetector(g)
	d.Record(ctx)

	changed, err := d.Changed(ctx)
	if err != nil {
		t.Fatalf("Changed returned error %v, want nil", err)
	}
	if !changed {
		t.Error("Head failing: changed = false, want true")
	}

	// A DirtyFiles failure behaves the same.
	g2 := &fakeGit{head: "abc", dirtyErr: errors.New("boom")}
	d2 := NewTreeChangeDetector(g2)
	d2.Record(ctx)
	if changed, err := d2.Changed(ctx); err != nil || !changed {
		t.Errorf("DirtyFiles failing: changed=%v err=%v, want true, nil", changed, err)
	}

	// A detector that never recorded has nothing to compare against.
	d3 := NewTreeChangeDetector(&fakeGit{head: "abc"})
	if changed, err := d3.Changed(ctx); err != nil || !changed {
		t.Errorf("never recorded: changed=%v err=%v, want true, nil", changed, err)
	}
}

// The Refresher builds once, reuses the map while the tree is unchanged,
// rebuilds on a change, and forgets a failed build so the next phase retries
// (14-REQ-9.1, 14-REQ-9.2, 14-REQ-10.1).
func TestRefresherBuildsOnlyOnTreeChange(t *testing.T) {
	ctx := context.Background()
	g := &fakeGit{head: "a"}
	calls := 0
	var failNext bool
	build := func(context.Context, *tools.Workspace, int, []string) (string, error) {
		calls++
		if failNext {
			return "", errors.New("walk failed")
		}
		return "map", nil
	}
	r := NewRefresher(nil, 100, build, g)

	for i := 0; i < 3; i++ {
		if m, err := r.Get(ctx, nil); err != nil || m != "map" {
			t.Fatalf("Get = %q, %v", m, err)
		}
	}
	if calls != 1 {
		t.Errorf("an unchanged tree built %d times, want 1", calls)
	}
	g.head = "b"
	if _, err := r.Get(ctx, nil); err != nil || calls != 2 {
		t.Errorf("a changed tree: calls=%d err=%v, want 2", calls, err)
	}

	g.head, failNext = "c", true
	if m, err := r.Get(ctx, nil); err == nil || m != "" {
		t.Errorf("a failed build returned %q, %v", m, err)
	}
	failNext = false
	if m, err := r.Get(ctx, nil); err != nil || m != "map" || calls != 4 {
		t.Errorf("the next phase did not retry: %q, %v, calls=%d", m, err, calls)
	}
}
