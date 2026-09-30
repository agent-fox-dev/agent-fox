package codeimpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-51 (integration): impl with --input-kind text on an ordinary spec
// reference is unaffected by a same-named file existing elsewhere on disk
// (06-REQ-8.7).
func TestTS06_51_InputKindTextReachesLocateUnmodified(t *testing.T) {
	root := t.TempDir()
	specs := filepath.Join(root, ".specs")
	pkg := filepath.Join(specs, "07_thing")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	prd := "---\nspec_id: \"07\"\nspec_name: \"thing\"\ntitle: \"T\"\nstatus: \"active\"\n" +
		"created_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\nintent_hash: null\nschema_version: 2\n---\n# T\n\n## Intent\n\nx\n"
	if err := os.WriteFile(filepath.Join(pkg, "prd.md"), []byte(prd), 0o644); err != nil {
		t.Fatal(err)
	}

	// A file named "07" sits in the working directory, where Resolve's own
	// file check would find it.
	cwd := t.TempDir()
	t.Chdir(cwd)
	if err := os.WriteFile("07", []byte("not a spec"), 0o644); err != nil {
		t.Fatal(err)
	}

	without, err := toolio.Resolve(context.Background(), "07", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if without.Kind != toolio.KindFile {
		t.Fatalf("setup: Resolve should shadow the reference with the file, got %s", without.Kind)
	}

	forced, err := toolio.ResolveForced(context.Background(), "text", "07", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if forced.Kind != toolio.KindText || forced.Body != "07" {
		t.Fatalf("forced input = %+v", forced)
	}
	got, err := Locate(forced, root, specs)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Locate(toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: "07"}, root, specs)
	if err != nil || got != want {
		t.Errorf("Locate(forced) = %q, %v; want %q", got, err, want)
	}
	abs, _ := filepath.Abs(pkg)
	if got != abs {
		t.Errorf("Locate = %q, want %q", got, abs)
	}
}
