package afcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// testConfig is what the apps of these tests are made from. A test that needs a brain
// registry, brain options, a gate or a box registry of its own puts it here and restores
// the old value when it ends.
var testConfig Config

// newApp returns the shell with the commands registered, made from testConfig.
func newApp() *toolio.App { return New(&testConfig) }

// run runs the shell with args and returns what it wrote.
func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

// oneDocument fails the test unless s is exactly one JSON document.
func oneDocument(t *testing.T, s string) map[string]any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatalf("not a JSON document: %v\n%s", err, s)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		t.Fatalf("more than one JSON document (err = %v)\n%s", err, s)
	}
	return doc
}

// gitDo runs git in dir with an identity and none of the machine's configuration, and
// returns what it printed.
func gitDo(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func slicesContain(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
