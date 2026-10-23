package poll

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// The dials of the process through the default transport, which the hub client uses, are checked for the whole run of the
// tests of the package: TestMain notes each dial, and fails the run if one of them is not on the loopback interface.
var dials struct {
	sync.Mutex
	addresses []string
}

func TestMain(m *testing.M) {
	transport := http.DefaultTransport.(*http.Transport)
	was := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Lock()
		dials.addresses = append(dials.addresses, address)
		dials.Unlock()
		if was != nil {
			return was(ctx, network, address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	code := m.Run()
	dials.Lock()
	for _, address := range dials.addresses {
		if host, _, err := net.SplitHostPort(address); err != nil || !net.ParseIP(host).IsLoopback() {
			fmt.Fprintf(os.Stderr, "a test of the poll dialed %s, which is not on the loopback interface\n", address)
			code = 1
		}
	}
	dials.Unlock()
	os.Exit(code)
}

// TestEveryPollTestRunsOfflineAgainstLoopbackServers_TS_15_64 verifies TS-15-64, the guard under the rest of the tests of
// the poll: no test file names the address of a host that is not on the loopback interface, and the dials that the tests of
// the package make, a cycle that makes a job, absorbs a label and removes labels among them, are all on the loopback
// interface (TestMain fails the run for one that is not).
// Verifies: 15-REQ-10.3
func TestEveryPollTestRunsOfflineAgainstLoopbackServers_TS_15_64(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files: %v", err)
	}
	address := regexp.MustCompile(`https?://[^\s"'` + "`" + `/)]+`)
	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, found := range address.FindAllString(string(src), -1) {
			u, err := url.Parse(found)
			if err != nil {
				continue
			}
			if host := u.Hostname(); host != "localhost" && !net.ParseIP(host).IsLoopback() {
				t.Errorf("%s names %s, which is not on the loopback interface", name, found)
			}
		}
	}

	e := newEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	if u, err := url.Parse(e.Hub.URL); err != nil || !net.ParseIP(u.Hostname()).IsLoopback() {
		t.Fatalf("the fake hub is at %s, which is not on the loopback interface", e.Hub.URL)
	}

	e.Hub.AddIssue(slug, fakehub.Issue{Number: 7, Labels: []string{"af:triage"}})
	e.Hub.AddIssue(slug, fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
	e.cycle()
	e.Hub.AddLabel(slug, 42, "af:fix") // absorbed by the open fix job, or taken again
	e.cycle()

	if len(e.jobList()) == 0 || len(e.removals()) == 0 {
		t.Errorf("jobs %v, removals %v: the cycle did nothing, so the guard proves nothing", e.jobList(), e.removals())
	}
	dials.Lock()
	defer dials.Unlock()
	if len(dials.addresses) == 0 {
		t.Error("nothing was dialed through the default transport: the guard sees nothing")
	}
	for _, address := range dials.addresses {
		if host, _, _ := net.SplitHostPort(address); !slices.Contains([]string{"127.0.0.1", "::1"}, host) && !net.ParseIP(host).IsLoopback() {
			t.Errorf("the process dialed %s", address)
		}
	}
}
