package toolio_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-09-30 (integration): all four tools set Envelope.SchemaVersion from the
// same shared constant. Each binary is built and driven to a usage error,
// which still goes through Run.Envelope.
func TestTS09_30_AllFourToolsAdvertiseTheSameSchemaVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds four binaries")
	}
	// Each invocation is a usage error the tool reports as an envelope.
	cases := []struct {
		tool string
		args []string
	}{
		{"spec", []string{"--name", "BAD!", "x"}},
		{"issue", []string{"--repo", "single", "x"}},
		{"fix", []string{"--land", "bogus", "x"}},
		{"impl", []string{"--land", "bogus", "x"}},
	}
	binDir := t.TempDir()
	seen := map[string]string{}
	for _, c := range cases {
		bin := filepath.Join(binDir, c.tool)
		build := exec.Command("go", "build", "-o", bin, "github.com/agent-fox-dev/agentfox/cmd/"+c.tool)
		build.Dir = "."
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", c.tool, err, out)
		}
		cmd := exec.Command(bin, append([]string{"--dir", t.TempDir()}, c.args...)...)
		cmd.Env = append(cmd.Environ(), "XDG_STATE_HOME="+t.TempDir())
		var stdout bytes.Buffer
		cmd.Stdout = &stdout
		_ = cmd.Run()
		var env map[string]any
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("%s: stdout is not one JSON object (%v):\n%s", c.tool, err, stdout.String())
		}
		if env["exit_code"] != float64(toolio.ExitUsage) {
			t.Fatalf("%s: exit_code = %v, want a usage error:\n%s", c.tool, env["exit_code"], stdout.String())
		}
		sv, ok := env["schema_version"].(string)
		if !ok {
			t.Fatalf("%s: envelope has no schema_version:\n%s", c.tool, stdout.String())
		}
		seen[c.tool] = sv
	}
	for tool, sv := range seen {
		if sv != toolio.SchemaVersion {
			t.Errorf("%s advertises schema_version %q, want %q", tool, sv, toolio.SchemaVersion)
		}
	}
}
