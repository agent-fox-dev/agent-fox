package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-08-26 (smoke, entry point): issue reaches the shared App.Main unchanged,
// so --output is live on it: a usage-error envelope is persisted
// byte-for-byte, and an --output naming a directory is refused.
func TestTS08_Output_issue_EntryPointWritesEnvelopeFile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	out := filepath.Join(t.TempDir(), "nested", "result.json")
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--output", out, "--detail", "bogus", "some input"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want %d; stderr:\n%s", code, toolio.ExitUsage, stderr.String())
	}
	file, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("--output was not written: %v", err)
	}
	if !bytes.Equal(file, stdout.Bytes()) {
		t.Errorf("--output differs from stdout:\nfile:\n%s\nstdout:\n%s", file, stdout.String())
	}

	stdout.Reset()
	code = newApp().Main(context.Background(), []string{"--output", t.TempDir(), "some input"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Errorf("--output <directory>: code = %d, want %d", code, toolio.ExitUsage)
	}
}
