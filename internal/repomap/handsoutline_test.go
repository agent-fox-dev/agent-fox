package repomap

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestHandsOutliner_ReadsTheDeclarationsOfTheHandsOutline: a method is named
// with its type, and a file the hands cannot outline is an error, which the
// map lists by name.
func TestHandsOutliner_ReadsTheDeclarationsOfTheHandsOutline(t *testing.T) {
	root := t.TempDir()
	testutil.WriteFile(t, root, "pkg/a.go", "package pkg\n\nfunc A() {}\n\ntype T struct{}\n\nfunc (T) M() {}\n")
	testutil.WriteFile(t, root, "notes.txt", "hello\n")
	testutil.WriteFile(t, root, "bad.go", "package \x00\x00")
	testutil.WriteFile(t, root, "empty.go", "package empty\n")
	o := NewOutliner(root)
	ctx := context.Background()

	if decls, err := o.Outline(ctx, "pkg/a.go"); err != nil || !slices.Equal(decls, []Decl{{"func", "A", 3}, {"type", "T", 5}, {"method", "T.M", 7}}) {
		t.Errorf("declarations = %v, %v", decls, err)
	}
	if decls, err := o.Outline(ctx, "empty.go"); err != nil || len(decls) != 0 {
		t.Errorf("a Go file with nothing declared = %v, %v; want no declarations and no error", decls, err)
	}
	for _, path := range []string{"notes.txt", "bad.go", "missing.go"} {
		if _, err := o.Outline(ctx, path); err == nil {
			t.Errorf("%s was outlined", path)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := o.Outline(cancelled, "pkg/a.go"); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled context = %v, want context.Canceled", err)
	}

	res := mustBuild(t, entries("pkg/a.go", "notes.txt"), o, 4000)
	if res.FilesOutlined != 1 || !strings.Contains(res.Map, "  a.go: func A L3 · type T L5 · method T.M L7\n") || !strings.Contains(res.Map, "  notes.txt\n") {
		t.Errorf("result = %+v", res)
	}
}
