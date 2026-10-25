package box

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

const gatewayPAT = "pat-for-the-gateway-tests"

// leased is a fake hub with a workspace, and a lease of a sandbox on its gateway.
func leased(t *testing.T) (*fakehub.Hub, hub.Lease) {
	t.Helper()
	h := fakehub.New(t)
	h.AddWorkspace(t, fakehub.Workspace{Slug: "acme", PAT: gatewayPAT})
	lease, err := hub.New(h.URL, "1").Lease(context.Background(), gatewayPAT, "acme", hub.LeaseRequest{Purpose: "fix"})
	if err != nil {
		t.Fatalf("lease: %v", err)
	}
	return h, lease
}

// openGateway opens a box on the gateway at url with the token.
func openGateway(t *testing.T, dir, url, token string, labels map[string]string) (*Session, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req := request(GatewayName, dir, registry(GatewayEntry(url, token)), &ledger{})
	req.Labels = labels
	s, err := Open(ctx, req)
	if err == nil {
		t.Cleanup(func() { _ = s.Close() })
	}
	return s, err
}

func TestGateway_TheBoxOfALeaseIsMadeOnTheGatewayAndClosedThere(t *testing.T) {
	h, lease := leased(t)
	labels := map[string]string{"af.job": "job_0123456789abcdef0123", "af.attempt": "2"}

	s, err := openGateway(t, hostRepo(t, nil), lease.GatewayURL, lease.Token, labels)
	if err != nil {
		t.Fatal(err)
	}

	created := h.Gateway().Created()
	if len(created) != 1 || created[0].Lease != lease.ID || len(created[0].Spec.Env) != 0 {
		t.Fatalf("created = %+v, want one box of the lease, with no environment of the host's", created)
	}
	want := map[string]string{"af.run_id": runID, "af.tool": "fix", "af.job": labels["af.job"], "af.attempt": "2"}
	if got := created[0].Spec.Labels; len(got) != len(want) || got["af.run_id"] != runID || got["af.job"] != want["af.job"] || got["af.attempt"] != "2" || got["af.tool"] != "fix" {
		t.Errorf("labels = %v, want %v", got, want)
	}
	if s.Provider() != GatewayName || !s.ByName() || !s.SuppliesCredentials() {
		t.Errorf("session: provider %q, by name %v, supplies credentials %v", s.Provider(), s.ByName(), s.SuppliesCredentials())
	}
	if got := inBox(t, s, "cat file0.txt"); got != "content 0" {
		t.Errorf("the box holds %q, want the host's tree", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if closed := h.Gateway().Closed(); len(closed) != 1 || closed[0].ID != created[0].ID {
		t.Errorf("closed = %+v, want the box closed with the session", closed)
	}
}

func TestGateway_IsAnIsolatingRemoteProviderThatAfDoesNotOffer(t *testing.T) {
	e := GatewayEntry("http://127.0.0.1:1", "t")

	if !e.Isolates() || !e.ByName || !e.SuppliesCredentials || !e.Remote {
		t.Errorf("entry = %+v, want isolating, by name, supplying credentials, remote", e)
	}
	if _, err := Default.Lookup(GatewayName); err == nil || slices.Contains(Default.Names(), GatewayName) {
		t.Errorf("Default offers the gateway: %v, %v", err, Default.Names())
	}
}

func TestGateway_ThatCannotGiveABoxIsARetryableSandboxFailureWithoutTheToken(t *testing.T) {
	h, lease := leased(t)
	dir := hostRepo(t, nil)
	refuse := func(reason string) func() { return func() { h.Gateway().RefuseBoxes(reason) } }
	release := func() {
		if err := hub.New(h.URL, "1").ReleaseLease(context.Background(), gatewayPAT, "acme", lease.ID); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, url, token string
		before           func()
	}{
		{"no gateway at the address", "http://127.0.0.1:1", lease.Token, nil},
		{"a token it does not take", lease.GatewayURL, "sbl_not-a-lease", nil},
		{"a URL that is none", "ftp://nowhere.example.com", lease.Token, nil},
		{"a box it refuses", lease.GatewayURL, lease.Token, refuse("no capacity")},
		{"a lease that was released", lease.GatewayURL, lease.Token, func() { refuse("")(); release() }},
	} {
		if tc.before != nil {
			tc.before()
		}

		_, err := openGateway(t, dir, tc.url, tc.token, nil)

		if f := asFailure(t, err); f.Category != toolio.CategorySandbox || !f.Retryable || strings.Contains(err.Error(), lease.Token) {
			t.Errorf("%s: %v, want a retryable sandbox failure that does not hold the token", tc.name, err)
		}
	}
}

func TestGateway_ThatGoesAwayLosesTheBox(t *testing.T) {
	h, lease := leased(t)
	s, err := openGateway(t, hostRepo(t, nil), lease.GatewayURL, lease.Token, nil)
	if err != nil {
		t.Fatal(err)
	}

	h.Gateway().Stop()
	_, _, err = s.Exec(context.Background(), "true", 0)

	if f := asFailure(t, err); !f.Retryable || !errors.Is(err, ErrUnavailable) || !errors.Is(s.Lost(), ErrUnavailable) {
		t.Errorf("Exec = %+v, Lost = %v; want a retryable failure of a box that is gone, and the loss noted", f, s.Lost())
	}
}

func TestExplainGateway_HidesTheTokenAndLeavesAStoppedRunAlone(t *testing.T) {
	f := asFailure(t, explainGateway("sbl_secret", errors.New("dial with sbl_secret: refused")))
	if f.Category != toolio.CategorySandbox || !f.Retryable || f.Message != "the gateway cannot give a box: dial with [token]: refused" {
		t.Errorf("failure = %+v", f)
	}
	for _, ctxErr := range []error{context.Canceled, context.DeadlineExceeded} {
		if got := explainGateway("t", ctxErr); got != ctxErr {
			t.Errorf("explainGateway(%v) = %v, want it as it is", ctxErr, got)
		}
	}
	if _, err := GatewayEntry("http://127.0.0.1:1", "t").Build(ProviderConfig{}); err == nil {
		t.Error("a gateway that is not there was dialled")
	}
}
