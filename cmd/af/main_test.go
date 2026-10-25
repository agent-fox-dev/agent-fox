package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The built binary takes its identity from the -ldflags of make build, offers the loops of
// afcmd and the client commands, and exits with the code of its envelope.
func TestBinary_HasTheIdentityOfItsBuildAndExitsWithTheCodeOfItsEnvelope(t *testing.T) {
	testutil.Require(t, "go")
	binary := filepath.Join(t.TempDir(), "af")
	build := exec.Command("go", "build", "-o", binary,
		"-ldflags", "-X main.version=9.9.9-test -X main.commit=abc1234 -X main.buildTime=2026-10-23T12:00:00Z", ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	svc := newService(t, answer(409, `{"error":{"code":"invalid_state","message":"no"}}`))
	dir := t.TempDir()
	testutil.WriteFile(t, dir, ".af/config.toml", "token = \"T\"\n")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var out, errb bytes.Buffer
		cmd := exec.Command(binary, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = dir, &out, &errb
		cmd.Env = append(os.Environ(), "HOME="+dir, "AF_URL="+svc.URL)
		var exit *exec.ExitError
		if err := cmd.Run(); err != nil && !errors.As(err, &exit) {
			t.Fatal(err)
		}
		return cmd.ProcessState.ExitCode(), out.String(), errb.String()
	}

	code, stdout, stderr := run("--version")
	doc := testutil.Decode[map[string]any](t, []byte(stdout))
	delete(doc, "go_version")
	if want := map[string]any{"name": "af", "version": "9.9.9-test", "commit": "abc1234", "build_time": "2026-10-23T12:00:00Z"}; code != 0 || stderr != "" || !reflect.DeepEqual(doc, want) {
		t.Errorf("--version: exit %d, stderr %q, %v; want 0 and %v", code, stderr, doc, want)
	}

	code, stdout, stderr = run("nosuch")
	env := testutil.Decode[map[string]any](t, []byte(stdout))
	msg, _ := field(env, "error", "message").(string)
	if code != 2 || stderr != "" || env["status"] != "usage" || !strings.Contains(msg, "triage") || !strings.Contains(msg, "jobs submit") {
		t.Errorf("an unknown command: exit %d, stderr %q, status %v, message %q; want 2, usage and the commands", code, stderr, env["status"], msg)
	}

	code, stdout, _ = run("jobs", "cancel", "j")
	if env := testutil.Decode[map[string]any](t, []byte(stdout)); code != 1 || field(env, "error", "category") != "conflict" {
		t.Errorf("a refused cancel: exit %d, error %v; want 1 and conflict", code, env["error"])
	}
	if got := svc.last(t).Header.Get("User-Agent"); got != "af/9.9.9-test" {
		t.Errorf("User-Agent %q, want af/9.9.9-test", got)
	}
}
