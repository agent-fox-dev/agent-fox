package worker

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// TestTheWorkerTestsRunOfflineWithLoopbackListenersTheScriptedBrainAndNoContainerEngine_TS_14_60 verifies TS-14-60,
// the guard under the end-to-end tests of the worker: with no key of a model vendor, with a container engine that
// fails the test when it is run, and with every dial of the process checked, a fix attempt comes to its end against
// the fake hub and the in-process gateway over the local provider, with the scripted brain, and dials the loopback
// interface only.
// Verifies: 14-REQ-10.2
func TestTheWorkerTestsRunOfflineWithLoopbackListenersTheScriptedBrainAndNoContainerEngine_TS_14_60(t *testing.T) {
	e := loopEnv(t, fixFiles, nil, nil, fixSteps()...)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	// A container engine is a program that fails the test, first on the PATH.
	tripped := filepath.Join(t.TempDir(), "engine-was-run")
	shims := t.TempDir()
	for _, engine := range []string{"podman", "docker"} {
		script := "#!/bin/sh\necho " + engine + " >> '" + tripped + "'\nexit 1\n"
		if err := os.WriteFile(filepath.Join(shims, engine), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", shims+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Every dial of the process through the default transport, which the hub client and the gateway use, is noted.
	transport := http.DefaultTransport.(*http.Transport)
	dialed := struct {
		sync.Mutex
		addresses []string
	}{}
	was := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dialed.Lock()
		dialed.addresses = append(dialed.addresses, address)
		dialed.Unlock()
		host, _, err := net.SplitHostPort(address)
		if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
			t.Errorf("the process dialed %s, which is not on the loopback interface", address)
		}
		if was != nil {
			return was(ctx, network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	t.Cleanup(func() { transport.DialContext = was })

	// The clone is made by git, a process the hook cannot see, from the address of the fake hub: that and the address of
	// the gateway of the leases are the only ones the attempt is given, and both are on the loopback interface.
	for _, raw := range []string{e.hub.URL, e.hub.Gateway().URL()} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
			t.Errorf("the address %s is not on the loopback interface", raw)
		}
	}

	res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

	if err != nil || envelope(t, res)["status"] != "done" {
		t.Fatalf("result %s, error %v", res.Envelope, err)
	}
	flags := strings.Join(e.runner.loopFlags(nil), " ")
	if !strings.Contains(flags, "--brain scripted") {
		t.Errorf("the loop was run with %q, want the scripted brain", flags)
	}
	if data, err := os.ReadFile(tripped); err == nil {
		t.Errorf("a container engine was run: %s", data)
	}
	dialed.Lock()
	defer dialed.Unlock()
	if len(dialed.addresses) == 0 {
		t.Error("nothing was dialed through the default transport: the guard sees nothing")
	}
	for _, address := range dialed.addresses {
		if host, _, _ := net.SplitHostPort(address); !slices.Contains([]string{"127.0.0.1", "::1"}, host) && !net.ParseIP(host).IsLoopback() {
			t.Errorf("the process dialed %s", address)
		}
	}
}
