package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// writeStep is a step of a script that writes a file in the box.
func writeStep(path, content string) map[string]any {
	return map[string]any{"tool": "write_file", "args": map[string]any{"path": path, "content": content}, "expect": "ok"}
}

// helloReport is the report of the change that says hello.
const helloReport = `{"subject":"say hello","root_cause":"the script says helo","change":"say hello","files":[{"path":"src/greet.sh","why":"the word"}]}`

// TestFixRunRefusesWhatItCannotRun guards 09-REQ-1.3 and the refusals of a run that is
// not one: no run id, an id of no run, and a directory with no brief placed in it; and
// help, which lists the flags it takes and not the command itself.
func TestFixRunRefusesWhatItCannotRun(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	scripted := []string{"--brain", "scripted", "--sandbox", "local", "--script", script(t, submitStep(helloReport, "accepted"))}

	code, env := e.run("", append([]string{"fix", "run", "--dir", e.dir}, scripted...)...)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "--run-id") {
		t.Errorf("no run id: %q", msg)
	}
	code, env = e.run("", append([]string{"fix", "run", "--run-id", "20260101_000000_abcdef", "--dir", e.dir}, scripted...)...)
	refusedBy(t, code, env, 2, "usage")
	code, env = e.run("", append([]string{"fix", "run", "--run-id", "../x", "--dir", e.dir}, scripted...)...)
	refusedBy(t, code, env, 2, "usage")
	bare := "20260101_000000_abcdef"
	if err := os.MkdirAll(filepath.Join(e.dir, ".agent-fox", "run", bare), 0o755); err != nil {
		t.Fatal(err)
	}
	code, env = e.run("", append([]string{"fix", "run", "--run-id", bare, "--dir", e.dir}, scripted...)...)
	if msg := refusedBy(t, code, env, 2, "usage")["message"].(string); !strings.Contains(msg, "no brief placed") {
		t.Errorf("a run directory with no brief placed in it: %q, want it to say so", msg)
	}

	var out strings.Builder
	e.app.Main(t.Context(), []string{"af", "help", "fix", "run"}, strings.NewReader(""), &out, &out)
	for _, want := range []string{"--run-id", "--brain", "--sandbox", "--budget", "--max-turns", "--script", "--dir", "--timeout", "(default 10)", "(default 150)"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("af help fix run does not show %q:\n%s", want, out.String())
		}
	}
	for _, not := range []string{"--land", "--base", "--no-verify", "--repo", "--context"} {
		if strings.Contains(out.String(), not) {
			t.Errorf("af help fix run shows %q, which it does not take", not)
		}
	}
	out.Reset()
	e.app.Main(t.Context(), []string{"af", "help"}, strings.NewReader(""), &out, &out)
	if strings.Contains(out.String(), "fix run") {
		t.Errorf("af help lists fix run, which is no command for a person:\n%s", out.String())
	}
}
