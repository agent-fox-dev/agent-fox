package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the golden files: go test ./internal/toolio ./cmd/af -update.
var update = flag.Bool("update", false, "rewrite the golden files")

// run runs the binary's shell with args and returns what it wrote.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// TestSchemaGolden_TS_05_70 verifies TS-05-70 for the real binary:
// The schema output matches the committed golden file, and -update rewrites
// it.
// Verifies: 05-REQ-10.3
func TestSchemaGolden_TS_05_70(t *testing.T) {
	code, stdout, stderr := run(t, "--schema")
	if code != 0 || stderr != "" {
		t.Fatalf("exit code %d, stderr %q; want 0 and nothing", code, stderr)
	}
	golden := filepath.Join("testdata", "schema.golden.json")

	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v; create it with go test -update", err)
	}
	if stdout != string(want) {
		t.Errorf("af --schema differs from %s (%d bytes, want %d); rewrite it with go test -update", golden, len(stdout), len(want))
	}
}
