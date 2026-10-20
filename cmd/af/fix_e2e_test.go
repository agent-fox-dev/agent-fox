package main

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// fixFauxBrains makes the brains of the fix commands run on the faux provider, which
// answers with the turns it was given: no model, no key and no network.
func fixFauxBrains(t *testing.T, p *faux.Provider) {
	t.Helper()
	old := fixBrainOptions
	fixBrainOptions = func() brain.Options {
		return brain.Options{Model: "faux-1", Getenv: noEnv, NewProvider: func(string) (core.ProviderClient, error) { return p, nil }}
	}
	t.Cleanup(func() { fixBrainOptions = old })
}

// allUserText is the text of every user message of a request, in order: the prefix of a
// fix brief is sent ahead of the messages, and the task is the first of them.
func allUserText(req core.Request) string {
	var parts []string
	for _, m := range append(append(core.Messages{}, req.Prefix...), req.Messages...) {
		if u, ok := m.(core.UserMessage); ok {
			parts = append(parts, u.Content.Text())
		}
	}
	return strings.Join(parts, "\n")
}

// TestTheFixSuiteRunsOffline_TS_09_70 verifies TS-09-70: with no token and no key in
// the environment, a fix on GitHub and one on GitLab, each with the agentkit brain on
// the faux provider and the local box, end done with a pull or merge request opened,
// and every request they make goes to this machine.
// Verifies: 09-REQ-10.4
func TestTheFixSuiteRunsOffline_TS_09_70(t *testing.T) {
	requireMake(t)
	guard := &loopbackOnly{next: http.DefaultTransport}
	old := http.DefaultTransport
	http.DefaultTransport = guard
	t.Cleanup(func() { http.DefaultTransport = old })

	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run(kind, func(t *testing.T) {
			e := newFixEnvWith(t, kind, redFixFiles(), true) // clears the tokens and the key of the environment
			for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"} {
				if v := os.Getenv(name); v != "" {
					t.Fatalf("%s is set", name)
				}
			}
			p := faux.New(
				call("c1", "write_file", `{"path":"src/greet.sh","content":"echo hello\n"}`),
				call("c2", "write_file", `{"path":"tests/hello_test.sh","content":"[ \"$(sh src/greet.sh)\" = hello ] || { echo \"FAIL: not hello\"; exit 1; }\n"}`),
				call("c3", "submit_fix", spyReport),
			)
			fixFauxBrains(t, p)

			code, env := e.fix(e.forge.IssueURL(7), "--brain", "agentkit", "--sandbox", "local")

			if code != 0 || env["status"] != "done" {
				t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
			}
			if n := len(e.forge.PullRequests()); n != 1 {
				t.Errorf("%d pull or merge requests were opened, want 1", n)
			}
			if p.Calls() != 3 {
				t.Errorf("the model was asked %d times, want 3 (two writes, then the report)", p.Calls())
			}
			if reqs := p.Requests(); len(reqs) == 0 {
				t.Error("the model saw no request")
			} else if first := allUserText(reqs[0]); !strings.Contains(first, "# Fix mandate") {
				t.Errorf("the first request does not carry the mandate:\n%.400s", first)
			}
		})
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if len(guard.seen) == 0 {
		t.Error("the guard saw no request: it is not in the way of the forge clients")
	}
	for _, host := range guard.seen {
		if host != "127.0.0.1" && host != "localhost" && host != "::1" {
			t.Errorf("a request went to %q", host)
		}
	}
}
