package fakehub

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

func TestLease_IsGrantedWithATokenForTheGateway(t *testing.T) {
	h := newHub(t, Workspace{})
	r := do(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{"profile": "big", "purpose": "fix", "labels": map[string]string{"job": "job_1"}})
	got := r.Object(t)
	expires, err := time.Parse(time.RFC3339, got["expires_at"].(string))
	if r.Status != 201 || !strings.HasPrefix(got["id"].(string), "sbx_") || !strings.HasPrefix(got["token"].(string), "sbl_") || got["gateway_url"] != h.Gateway().URL() ||
		got["state"] != "leased" || got["profile"] != "big" || got["purpose"] != "fix" || got["labels"].(map[string]any)["job"] != "job_1" ||
		err != nil || time.Until(expires) < 29*time.Minute {
		t.Errorf("the lease: %d %s", r.Status, r.Body)
	}
	if leases := h.Leases(); len(leases) != 1 || leases[0].ID != got["id"] || leases[0].Token != got["token"] || leases[0].Slug != "acme" || leases[0].Released || leases[0].Seq == 0 {
		t.Errorf("Leases = %+v", leases)
	}
	if r := do(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{"ttl": "1h"}); r.Status != 400 {
		t.Errorf("a lease with a field hub does not take: %d, want 400", r.Status)
	}
	h.SetLeaseTTL(90 * time.Second)
	r = do(t, h, "POST", "/api/v1/workspaces/acme/sandboxes", pat, map[string]any{})
	if expires, err := time.Parse(time.RFC3339, r.Object(t)["expires_at"].(string)); err != nil || time.Until(expires) > 90*time.Second || time.Until(expires) < 80*time.Second {
		t.Errorf("a lease after SetLeaseTTL ends %v, want in about 90s", expires)
	}
	if r := do(t, h, "PUT", "/api/v1/workspaces/acme/sandboxes", pat, nil); r.Status != 404 {
		t.Errorf("a route of the sandboxes that is not hub's: %d", r.Status)
	}
}

// The gateway takes the token of a lease that is held, records the boxes the lease creates and
// closes, and refuses the token once the lease is released.
func TestGateway_ServesTheBoxesOfAHeldLease(t *testing.T) {
	h := newHub(t, Workspace{})
	id, token := lease(t, h)
	if _, err := dial(t, h, "sbl_not-a-lease"); err == nil {
		t.Error("the gateway took a token that is no lease's")
	}
	p, err := dial(t, h, token)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := box(t, p)
	if err != nil {
		t.Fatal(err)
	}
	created := h.Gateway().Created()
	if len(created) != 1 || created[0].Lease != id || created[0].Spec.Labels["af.job"] != "job_1" || created[0].ID != sb.ID() {
		t.Errorf("Created = %+v", created)
	}
	if listed := do(t, h, "GET", "/api/v1/workspaces/acme/sandboxes", pat, nil).Objects(t); listed[0]["state"] != "active" {
		t.Errorf("a lease with a box is listed as %v, want active", listed[0]["state"])
	}
	if err := sb.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	closed := h.Gateway().Closed()
	if len(closed) != 1 || closed[0].ID != sb.ID() || closed[0].Seq <= created[0].Seq {
		t.Errorf("Closed = %+v, want the box after its creation", closed)
	}

	for path, want := range map[string]int{"/sbx_00000000000000000000000000000000": 404, "/" + id: 200} {
		if r := do(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes"+path, pat, nil); r.Status != want {
			t.Errorf("DELETE %s: %d, want %d", path, r.Status, want)
		}
	}
	if l := h.Leases()[0]; !l.Released || l.ReleasedSeq <= closed[0].Seq || !slices.Equal(h.Deleted(), []string{id}) {
		t.Errorf("the lease after its release = %+v, Deleted %v", l, h.Deleted())
	}
	if _, err := dial(t, h, token); err == nil {
		t.Error("the gateway took the token of a lease that was released")
	}
}

func TestListLeases_ListsTheHeldLeasesNewestFirstWithoutTheirToken(t *testing.T) {
	h := newHub(t, Workspace{})
	a := h.SeedLease("acme", map[string]string{"job": "job_1"}, "fix")
	b := h.SeedLease("acme", nil, "triage")
	c := h.SeedLease("acme", nil, "fix")
	h.SeedLease("other", nil, "fix")
	h.SetLeaseTTL(-time.Second)
	h.SeedLease("acme", nil, "ended")
	do(t, h, "DELETE", "/api/v1/workspaces/acme/sandboxes/"+c.ID, pat, nil)

	listed := do(t, h, "GET", "/api/v1/workspaces/acme/sandboxes", pat, nil).Objects(t)
	var ids []string
	for _, l := range listed {
		ids = append(ids, l["id"].(string))
		if _, ok := l["token"]; ok || l["labels"] == nil || l["state"] != "leased" || l["gateway_url"] != h.Gateway().URL() {
			t.Errorf("a lease is listed as %v, want its fields, labels and no token", l)
		}
	}
	if !slices.Equal(ids, []string{b.ID, a.ID}) || listed[1]["labels"].(map[string]any)["job"] != "job_1" || listed[1]["purpose"] != "fix" {
		t.Errorf("listed %v, want the two held leases of acme, newest first", listed)
	}
	if !slices.Equal(h.Deleted(), []string{c.ID}) {
		t.Errorf("Deleted = %v", h.Deleted())
	}
}

func TestGateway_RefusesBoxesWhenToldAndIsGoneOnceStopped(t *testing.T) {
	h := newHub(t, Workspace{})
	_, token := lease(t, h)
	p, err := dial(t, h, token)
	if err != nil {
		t.Fatal(err)
	}
	h.Gateway().RefuseBoxes("no room for another box")
	if _, err := box(t, p); err == nil || !strings.Contains(err.Error(), "no room for another box") || len(h.Gateway().Created()) != 0 {
		t.Errorf("a box on a gateway that refuses: %v, %d recorded; want the reason and none", err, len(h.Gateway().Created()))
	}
	h.Gateway().RefuseBoxes("")
	if sb, err := box(t, p); err != nil {
		t.Errorf("a box once the gateway takes them again: %v", err)
	} else {
		_ = sb.Close(context.Background())
	}
	h.Gateway().Stop()
	h.Gateway().Stop()
	if p, err := dial(t, h, token); err == nil {
		if _, err := box(t, p); err == nil {
			t.Error("a box was created on a gateway that is stopped")
		}
	}
}

// A killed sandbox is gone for the gateway while its lease is still held: what is gone is the box,
// and not the right to ask for another.
func TestKillSandbox_TakesTheBoxesOfTheLeaseAndLeavesTheLease(t *testing.T) {
	h := newHub(t, Workspace{})
	h.KillSandbox("sbx_none") // no gateway yet
	id, token := lease(t, h)
	p, err := dial(t, h, token)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := box(t, p)
	if err != nil {
		t.Fatal(err)
	}
	run := execute(t, sb)
	if r := run("echo alive"); !r.OK {
		t.Fatalf("a command in a box that was not killed: %+v", r)
	}
	h.KillSandbox(id)
	if r := run("echo alive"); r.OK || r.Error != "sandbox_unavailable" {
		t.Errorf("a command in the killed box = %+v, want sandbox_unavailable", r)
	}
	if _, err := sb.Sync(context.Background(), sandbox.Tar{R: strings.NewReader(""), Path: "."}); !errors.Is(err, sandbox.ErrUnavailable) {
		t.Errorf("a sync to the killed box = %v, want sandbox.ErrUnavailable", err)
	}
	if l := h.Leases()[0]; l.Released {
		t.Error("the lease of the killed sandbox was released")
	}
}

// An instrumented provider records the commands its boxes run, in the order of the gateway's own
// events, and the gateway can be made to go away before a command or after it.
func TestInstrument_RecordsTheCommandsAndKillsTheGatewayOnOrAfterOne(t *testing.T) {
	start := func(t *testing.T) (*Hub, sandbox.Provider, func(string) bool) {
		h := newHub(t, Workspace{})
		_, token := lease(t, h)
		p, err := dial(t, h, token)
		if err != nil {
			t.Fatal(err)
		}
		instrumented := h.Gateway().Instrument(p)
		sb, err := box(t, instrumented)
		if err != nil {
			t.Fatal(err)
		}
		run := execute(t, sb)
		return h, instrumented, func(command string) bool { return run(command).OK }
	}

	t.Run("the commands", func(t *testing.T) {
		h, instrumented, run := start(t)
		run("echo first")
		run("echo second")
		commands := h.Gateway().Commands()
		if len(commands) != 2 || commands[0].Command != "echo first" || commands[1].Command != "echo second" || commands[1].Seq <= commands[0].Seq || commands[0].Box == "" {
			t.Errorf("Commands = %+v, want the two in order", commands)
		}
		if u, ok := instrumented.(interface{ Unwrap() sandbox.Provider }); !ok || u.Unwrap() == nil {
			t.Error("the instrumented provider does not unwrap")
		}
		if c, ok := instrumented.(interface{ Close() error }); !ok || c.Close() != nil {
			t.Error("the instrumented provider does not close")
		}
	})
	t.Run("after a command", func(t *testing.T) {
		h, instrumented, run := start(t)
		h.Gateway().KillAfter("third")
		if !run("echo third") {
			t.Error("the command that the gateway was killed after did not return its result")
		}
		if _, err := box(t, instrumented); err == nil {
			t.Error("a box was made on a gateway that was killed")
		}
	})
	t.Run("on a command", func(t *testing.T) {
		h, _, run := start(t)
		h.Gateway().KillOn("fourth")
		if run("echo fourth") {
			t.Error("the command that the gateway was killed on ran")
		}
		if commands := h.Gateway().Commands(); len(commands) != 1 {
			t.Errorf("Commands = %+v, want the command that was to run", commands)
		}
	})
}
