package box

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"
)

// specRecorder is a provider that notes the spec it is asked to create a sandbox
// with, and refuses to create one.
type specRecorder struct {
	caps sandbox.Capabilities
	got  *sandbox.Spec
}

func (r specRecorder) Capabilities() sandbox.Capabilities { return r.caps }

func (r specRecorder) Create(_ context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	*r.got = spec
	return nil, errors.New("recorded")
}

func (specRecorder) Reap(context.Context) ([]string, error) { return nil, nil }

// specOf is the spec that Open gives a provider with the capabilities for the request.
func specOf(t *testing.T, caps sandbox.Capabilities, req Request) sandbox.Spec {
	t.Helper()
	var got sandbox.Spec
	r := NewRegistry()
	r.Register(Entry{Name: "recording", New: func() sandbox.Provider { return specRecorder{caps: caps, got: &got} }})
	req.Provider, req.Registry = "recording", r
	req.HostDir, req.RunID, req.Tool = newRepo(t, 1, "main"), "20261021_100000_abcdef", "fix"
	if _, err := Open(context.Background(), req); err == nil {
		t.Fatal("Open succeeded with a provider that creates nothing")
	}
	return got
}

var (
	noneAndOpen = sandbox.Capabilities{Network: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted}}
	openOnly    = sandbox.Capabilities{Network: []sandbox.NetworkMode{sandbox.NetworkUnrestricted}}
)

// TestOpenGivesTheProviderTheSpecOfTheRequest_TS_10_13 verifies the part of TS-10-13
// and TS-10-12 that is the box's: the image, the environment, the time to live and the labels of the request reach the provider.
// Verifies: 10-REQ-2.4, 10-REQ-2.5
func TestOpenGivesTheProviderTheSpecOfTheRequest_TS_10_13(t *testing.T) {
	spec := specOf(t, noneAndOpen, Request{
		Image: "example.test/img:1", TTL: 15 * time.Minute, Env: map[string]string{"ANTHROPIC_API_KEY": "k"},
	})

	if spec.Image != "example.test/img:1" || spec.TTL != 15*time.Minute || spec.Env["ANTHROPIC_API_KEY"] != "k" || len(spec.Env) != 1 {
		t.Errorf("spec = %+v, want the image, the TTL and the environment of the request", spec)
	}
	if spec.Labels["af.run_id"] != "20261021_100000_abcdef" || spec.Labels["af.tool"] != "fix" || len(spec.Labels) != 2 {
		t.Errorf("labels = %v, want af.run_id and af.tool as spec 07 sets them", spec.Labels)
	}

	plain := specOf(t, noneAndOpen, Request{})
	if plain.Image != "" {
		t.Errorf("spec of a request that asks for neither = %+v", plain)
	}
}

// TestOpenAsksForTheNetworkOfTheRequest_TS_10_11 verifies the part of TS-10-11 that is
// the box's: none is no network where the provider can, open is an unrestricted
// one, and host, or nothing, takes none where it is offered and otherwise the
// mode the provider has.
// Verifies: 10-REQ-2.3
func TestOpenAsksForTheNetworkOfTheRequest_TS_10_11(t *testing.T) {
	for _, tc := range []struct {
		name string
		caps sandbox.Capabilities
		want string
		mode sandbox.NetworkMode
	}{
		{NetworkNone, noneAndOpen, NetworkNone, sandbox.NetworkNone},
		{NetworkOpen, noneAndOpen, NetworkOpen, sandbox.NetworkUnrestricted},
		{NetworkHost, openOnly, NetworkHost, sandbox.NetworkUnrestricted},
		{"", noneAndOpen, "", sandbox.NetworkNone},
		{"", openOnly, "", sandbox.NetworkUnrestricted},
		{NetworkNone, openOnly, NetworkNone, sandbox.NetworkUnrestricted}, // a provider that cannot enforce it takes what it has
	} {
		spec := specOf(t, tc.caps, Request{Network: tc.want})
		if spec.Network.Mode != tc.mode {
			t.Errorf("network %q from %v: mode %v, want %v", tc.want, tc.caps.Network, spec.Network.Mode, tc.mode)
		}
	}
}

// TestBoxOpenRecordsImageNetworkAndPlacement_TS_10_14 verifies the part of TS-10-14 that
// is the box's: the box_open entry of the ledger carries the image, the network and
// the placement of the request, and the session says the network.
// Verifies: 10-REQ-2.6
func TestBoxOpenRecordsImageNetworkAndPlacement_TS_10_14(t *testing.T) {
	l := &ledger{}
	dir := newRepo(t, 1, "main")
	s, err := Open(context.Background(), Request{
		Provider: "local", HostDir: dir, RunID: "20261021_100000_abcdef", Tool: "fix", Record: l.record, Warn: l.warn,
		Network: NetworkHost, Placement: "box",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	var entry map[string]any
	l.last(t, "box_open", &entry)
	for _, key := range []string{"provider", "image", "network", "placement"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("box_open = %v, has no %s", entry, key)
		}
	}
	if entry["provider"] != "local" || entry["network"] != "host" || entry["placement"] != "box" || entry["image"] != "" {
		t.Errorf("box_open = %v, want local, image empty, network host and placement box", entry)
	}
	if s.Network() != NetworkHost {
		t.Errorf("Network() = %q, want host", s.Network())
	}
}

// TestASessionWithNoRequestedNetworkSaysWhatItGot guards the network of a request that
// asks for none: an unconfined provider says host, and one that confines says the
// mode it was given.
func TestASessionWithNoRequestedNetworkSaysWhatItGot(t *testing.T) {
	l := &ledger{}
	s := open(t, Default, "local", newRepo(t, 1, "main"), l)
	if s.Network() != NetworkHost {
		t.Errorf("a local box says its network is %q, want host", s.Network())
	}
	var entry map[string]any
	l.last(t, "box_open", &entry)
	if entry["network"] != "host" {
		t.Errorf("box_open = %v, want network host", entry)
	}
}

// TestTheNetworkARequestAsksForIsTheOneTheSessionSays guards the name of the network
// of a box: the request's when it has one, host for a provider that enforces nothing,
// and otherwise the mode the sandbox was given.
func TestTheNetworkARequestAsksForIsTheOneTheSessionSays(t *testing.T) {
	r := NewRegistry()
	r.Register(Entry{Name: "confined", New: local.Unisolated})
	r.Register(Entry{Name: "unconfined", Unconfined: true, New: local.Unisolated})
	for _, tc := range []struct {
		provider, network, want string
	}{
		{"unconfined", "", NetworkHost},
		{"unconfined", NetworkOpen, NetworkOpen},
		{"confined", "", NetworkOpen}, // local offers an unrestricted network only
		{"confined", NetworkNone, NetworkNone},
	} {
		l := &ledger{}
		s, err := Open(context.Background(), Request{
			Provider: tc.provider, HostDir: newRepo(t, 1, "main"), RunID: "20261021_100000_abcdef", Tool: "fix",
			Record: l.record, Warn: l.warn, Registry: r, Network: tc.network,
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.Network() != tc.want {
			t.Errorf("%s asked %q: Network() = %q, want %q", tc.provider, tc.network, s.Network(), tc.want)
		}
		_ = s.Close()
	}
}
