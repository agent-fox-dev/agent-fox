package main

import (
	"bytes"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestRun_TheCommandLine(t *testing.T) {
	for name, tc := range map[string]struct {
		args           []string
		code           int
		stdout, stderr string // what each stream holds; nothing when empty
	}{
		"help":                  {[]string{"--help"}, 0, "Usage: agent-fox", ""},
		"version":               {[]string{"--version"}, 0, `"name": "agent-fox"`, ""},
		"an unknown flag":       {[]string{"--port", "1"}, 2, "", "-port\n\nUsage: agent-fox"},
		"a positional argument": {[]string{"serve"}, 2, "", `unexpected argument "serve"` + "\n\nUsage: agent-fox"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, &stdout, &stderr); code != tc.code {
				t.Errorf("exit code %d, want %d", code, tc.code)
			}
			for stream, want := range map[*bytes.Buffer]string{&stdout: tc.stdout, &stderr: tc.stderr} {
				if (want == "") != (stream.Len() == 0) || !strings.Contains(stream.String(), want) {
					t.Errorf("stdout %q, stderr %q, want %q and %q", stdout.String(), stderr.String(), tc.stdout, tc.stderr)
				}
			}
			if name == "version" {
				testutil.Decode[map[string]any](t, stdout.Bytes())
			}
		})
	}
}

// TestRun_SIGTERMStopsTheServiceWithExit0 sends the signal to the test's own process, which the service catches.
func TestRun_SIGTERMStopsTheServiceWithExit0(t *testing.T) {
	s := newService(t, fakehub.New(t).URL)
	s.write(t)
	stderr := &testutil.Log{}
	done := make(chan int, 1)
	go func() { done <- run([]string{"--config", s.config}, &bytes.Buffer{}, stderr) }()
	testutil.Eventually(t, "the listening record", func() bool { return len(stderr.Find("INFO", "listening")) > 0 })
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if code := <-done; code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if lines := strings.Count(stderr.String(), "\n"); lines != len(stderr.Records()) {
		t.Errorf("stderr has %d lines and %d JSON records:\n%s", lines, len(stderr.Records()), stderr)
	}
}
