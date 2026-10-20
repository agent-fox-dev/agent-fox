package box

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TestSmokeAnExportWithoutGitIsRemovedOnClose_TS_07_84 verifies TS-07-84
// (07-PATH-5): a brain's edits are exported without .git and the export is
// removed on close, with the host untouched.
// Verifies: 07-PATH-5, 07-REQ-3.3, 07-REQ-3.5
func TestSmokeAnExportWithoutGitIsRemovedOnClose_TS_07_84(t *testing.T) {
	dir := newRepo(t, 2, "main")
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, dir)
	s, err := Open(context.Background(), Request{Provider: "local", HostDir: dir, RunID: rd.ID, Tool: "fix", Record: rd.Record, Warn: func(string, string) {}})
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = s.Close()
		}
	})

	// The brain's hands: write a file and commit it, inside the box.
	if r := callTool(t, s, "write_file", map[string]any{"path": "docs/edit.md", "content": "an edit from the box\n"}); !r.OK {
		t.Fatalf("write_file: %s %s", r.Error, r.Detail)
	}
	inBox(t, s, "git add -A && git commit -q -m 'edit inside the box'")

	out, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(out) })

	if rel, err := filepath.Rel(os.TempDir(), out); err != nil || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.Base(out), "af-export-"+rd.ID+"-") {
		t.Errorf("the export %s is not af-export-%s-* under %s", out, rd.ID, os.TempDir())
	}
	if got, err := os.ReadFile(filepath.Join(out, "docs", "edit.md")); err != nil || string(got) != "an edit from the box\n" {
		t.Errorf("the edit in the export = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export has a .git: %v", err)
	}
	entries, err := rd.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	var exported struct{ Path string }
	for _, e := range entries {
		if e.Kind == "box_export" {
			if err := json.Unmarshal(e.Data, &exported); err != nil {
				t.Fatal(err)
			}
		}
	}
	if exported.Path != out {
		t.Errorf("box_export says %q, want %q", exported.Path, out)
	}
	if after := snapshot(t, dir); after != before {
		t.Errorf("the host changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "edit.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the edit is in the host checkout: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	closed = true

	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export %s is still there after Close: %v", out, err)
	}
	entries, _ = rd.Ledger()
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Equal(kinds, []string{"box_open", "box_sync", "box_export", "box_close"}) {
		t.Errorf("ledger = %v, want box_open, box_sync, box_export, box_close", kinds)
	}
}
