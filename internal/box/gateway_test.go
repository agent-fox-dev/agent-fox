package box

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

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

// gatewayRegistry holds the gateway provider of a lease, and no other.
func gatewayRegistry(lease hub.Lease) *Registry {
	r := NewRegistry()
	r.Register(GatewayEntry(lease.GatewayURL, lease.Token))
	return r
}

// TestTheGatewayProviderCreatesTheBoxWithTheFourLabelsAndNoEnvAndIsNotAnAfChoice_TS_13_44
// verifies TS-13-44: the box of a lease is created on the gateway with the labels af.run_id,
// af.tool, af.job and af.attempt and an empty Env, the provider counts as isolating and its
// processes are called by name, and the providers af offers do not include it.
// Verifies: 13-REQ-6.4
func TestTheGatewayProviderCreatesTheBoxWithTheFourLabelsAndNoEnvAndIsNotAnAfChoice_TS_13_44(t *testing.T) {
	h, lease := leased(t)
	registry := gatewayRegistry(lease)
	repo := newRepo(t, 1, "main")

	s, err := Open(context.Background(), Request{
		Provider: GatewayName, HostDir: repo, RunID: "20250102_030405_abc123", Tool: "fix", Registry: registry,
		Labels: map[string]string{"af.job": "job_0123456789abcdef0123", "af.attempt": "2"},
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	created := h.Gateway().Created()
	if len(created) != 1 {
		t.Fatalf("the gateway created %d boxes, want 1", len(created))
	}
	spec := created[0].Spec
	want := map[string]string{"af.run_id": "20250102_030405_abc123", "af.tool": "fix", "af.job": "job_0123456789abcdef0123", "af.attempt": "2"}
	if len(spec.Labels) != 4 {
		t.Errorf("labels = %v, want exactly %v", spec.Labels, want)
	}
	for k, v := range want {
		if spec.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, spec.Labels[k], v)
		}
	}
	if len(spec.Env) != 0 {
		t.Errorf("Spec.Env = %v, want it empty: the box gets its credentials from the lease's profile", spec.Env)
	}
	if created[0].Lease != lease.ID {
		t.Errorf("the box belongs to %q, want the lease %s", created[0].Lease, lease.ID)
	}

	entry, err := registry.Lookup(GatewayName)
	if err != nil {
		t.Fatal(err)
	}
	if !entry.Isolates() || !entry.ByName || !entry.SuppliesCredentials || !entry.Remote {
		t.Errorf("entry = %+v, want an isolating provider, called by name, that supplies its credentials and is remote", entry)
	}
	if !s.ByName() || !s.SuppliesCredentials() || s.Provider() != GatewayName {
		t.Errorf("the session: ByName %v, SuppliesCredentials %v, provider %q", s.ByName(), s.SuppliesCredentials(), s.Provider())
	}
	// The box works: it has the tree, and runs a command.
	if got := strings.TrimSpace(inBox(t, s, "cat file0.txt")); got != "content 0" {
		t.Errorf("the box holds %q in file0.txt, want the host's content", got)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if closed := h.Gateway().Closed(); len(closed) != 1 || closed[0].ID != created[0].ID {
		t.Errorf("closed = %+v, want the box closed with the session", closed)
	}

	// It is not one of af's choices.
	if _, err := Default.Lookup(GatewayName); err == nil {
		t.Error("box.Default offers the gateway provider")
	} else {
		var failure *toolio.Failure
		if !errors.As(err, &failure) || failure.Category != toolio.CategoryUsage || !strings.Contains(failure.Message, "--sandbox") {
			t.Errorf("Default.Lookup(gateway) = %v, want a usage failure", err)
		}
		if names := Default.Names(); strings.Contains(strings.Join(names, " "), GatewayName) {
			t.Errorf("the providers of af are %v", names)
		}
	}
}

// TestAGatewayThatCannotBeReachedOrRefusesTheBoxIsARetryableSandboxFailure verifies the
// failure side of 13-REQ-6.3 at the provider: a gateway that is not there, a token it does not
// take, and a gateway that is gone are each a sandbox failure that may be tried again.
func TestAGatewayThatCannotBeReachedOrRefusesTheBoxIsARetryableSandboxFailure(t *testing.T) {
	h, lease := leased(t)
	repo := newRepo(t, 1, "main")
	open := func(url, token string) error {
		r := NewRegistry()
		r.Register(GatewayEntry(url, token))
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		s, err := Open(ctx, Request{Provider: GatewayName, HostDir: repo, RunID: "20250102_030405_abc123", Tool: "fix", Registry: r})
		if err == nil {
			_ = s.Close()
		}
		return err
	}
	for name, err := range map[string]error{
		"no gateway at the address": open("http://127.0.0.1:1", lease.Token),
		"a token it does not take":  open(lease.GatewayURL, "sbl_not-a-token-of-a-lease"),
		"a URL that is none":        open("ftp://nowhere.example.com", lease.Token),
	} {
		var failure *toolio.Failure
		if !errors.As(err, &failure) || failure.Category != toolio.CategorySandbox || !failure.Retryable {
			t.Errorf("%s: %v, want a retryable sandbox failure", name, err)
		}
		if err != nil && (strings.Contains(err.Error(), lease.Token)) {
			t.Errorf("%s: the error holds the token: %v", name, err)
		}
	}

	// A lease that was released has no box to give.
	if err := hub.New(h.URL, "1").ReleaseLease(context.Background(), gatewayPAT, "acme", lease.ID); err != nil {
		t.Fatal(err)
	}
	var failure *toolio.Failure
	if err := open(lease.GatewayURL, lease.Token); !errors.As(err, &failure) || !failure.Retryable {
		t.Errorf("a released lease: %v, want a retryable sandbox failure", err)
	}
}

// closingProvider is the local provider that counts how often it is closed.
type closingProvider struct {
	sandbox.Provider
	closed *atomic.Int32
}

func (c closingProvider) Close() error { c.closed.Add(1); return nil }

// TestTheProviderIsClosedWithTheBoxOrWhenItMadeNone verifies that a provider that holds a
// connection (a gateway's does) is closed once, with the box it made, and when it made none.
func TestTheProviderIsClosedWithTheBoxOrWhenItMadeNone(t *testing.T) {
	var closed atomic.Int32
	local, err := Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	entry := local
	entry.Name = "closing"
	entry.New = func() sandbox.Provider { return closingProvider{Provider: local.New(), closed: &closed} }
	reg := NewRegistry()
	reg.Register(entry)
	repo := newRepo(t, 1, "main")

	s, err := Open(context.Background(), Request{Provider: "closing", HostDir: repo, RunID: "20250102_030405_abc123", Tool: "fix", Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 0 {
		t.Errorf("the provider was closed %d times while its box is open", closed.Load())
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if closed.Load() != 1 {
		t.Errorf("the provider was closed %d times with its box, want once", closed.Load())
	}

	// A box that could not be made ready is closed with its provider as well.
	closed.Store(0)
	entry.New = func() sandbox.Provider { return makesNone{closingProvider{Provider: local.New(), closed: &closed}} }
	reg.Register(entry)
	if _, err := Open(context.Background(), Request{Provider: "closing", HostDir: repo, RunID: "20250102_030405_abc123", Tool: "fix", Registry: reg}); err == nil {
		t.Fatal("the box was made by a provider that fails")
	}
	if closed.Load() != 1 {
		t.Errorf("the provider that made no box was closed %d times, want once", closed.Load())
	}

	// A box that was made and could not be made ready is closed with its provider.
	closed.Store(0)
	entry.New = func() sandbox.Provider { return syncFails{closingProvider{Provider: local.New(), closed: &closed}} }
	reg.Register(entry)
	if _, err := Open(context.Background(), Request{Provider: "closing", HostDir: repo, RunID: "20250102_030405_abc123", Tool: "fix", Registry: reg}); err == nil {
		t.Fatal("a box that cannot be synced was opened")
	}
	if closed.Load() != 1 {
		t.Errorf("the provider of a box that could not be made ready was closed %d times, want once", closed.Load())
	}
}

// syncFails is a closing provider whose box cannot be synced.
type syncFails struct{ closingProvider }

func (p syncFails) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	return noSync{sb}, nil
}

type noSync struct{ sandbox.Sandbox }

func (noSync) Sync(context.Context, sandbox.Source) (sandbox.SyncResult, error) {
	return sandbox.SyncResult{}, errors.New("the box cannot take the tree")
}

// makesNone is a closing provider that makes no box.
type makesNone struct{ closingProvider }

func (makesNone) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, errors.New("no box today")
}
