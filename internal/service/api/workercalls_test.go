package api

import (
	"context"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// TestTheWorkersCallsDoNotTouchTheIdentityOrVisibilityCaches_TS_13_12 verifies TS-13-12: with
// the caches of the core warm, the calls of the worker reach hub and leave both caches as they
// were, and the caches go on answering without a call to hub, even when the PAT of the
// workspace is the very token that the caches hold an answer for.
// Verifies: 13-REQ-2.1
func TestTheWorkersCallsDoNotTouchTheIdentityOrVisibilityCaches_TS_13_12(t *testing.T) {
	h := fakehub.New(t)
	const token = "the-caller-and-the-pat"
	h.AddUser(token, "u1", []string{"acme"})
	h.AddWorkspace(t, fakehub.Workspace{Slug: "acme"})
	client := hub.New(h.URL, "1")
	a := newAuth(client, time.Hour, time.Now)
	ctx := context.Background()

	if _, err := a.identify(ctx, token); err != nil {
		t.Fatal(err)
	}
	if seen, err := a.visible(ctx, token, "acme"); err != nil || !seen {
		t.Fatalf("visible = %v, %v", seen, err)
	}
	identities, visibility, before := a.identities.Len(), a.visibility.Len(), h.Total()
	if identities != 1 || visibility != 1 {
		t.Fatalf("the caches hold %d and %d entries, want 1 and 1", identities, visibility)
	}

	if _, err := client.WorkspaceRecord(ctx, token, "acme"); err != nil {
		t.Fatalf("WorkspaceRecord: %v", err)
	}
	if _, err := client.ResolvedVars(ctx, token, "acme"); err != nil {
		t.Fatalf("ResolvedVars: %v", err)
	}
	id, err := client.OpenSession(ctx, token, hub.SessionRequest{WorkspaceSlug: "acme", Archetype: "fix", Model: "m", RunID: "20250102_030405_abc123"})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := client.CompleteSession(ctx, token, "acme", id, hub.SessionCompleted, ""); err != nil {
		t.Fatalf("CompleteSession: %v", err)
	}
	if err := client.PushBranch(ctx, token, "acme", "main"); err != nil {
		t.Fatalf("PushBranch: %v", err)
	}

	if got := h.Total() - before; got != 5 {
		t.Errorf("%d calls reached hub, want 5", got)
	}
	if a.identities.Len() != identities || a.visibility.Len() != visibility {
		t.Errorf("the caches hold %d and %d entries, were %d and %d", a.identities.Len(), a.visibility.Len(), identities, visibility)
	}
	after := h.Total()
	if _, err := a.identify(ctx, token); err != nil {
		t.Fatal(err)
	}
	if seen, err := a.visible(ctx, token, "acme"); err != nil || !seen {
		t.Fatalf("visible = %v, %v", seen, err)
	}
	if h.Total() != after {
		t.Errorf("the caches called hub again (%d requests): the worker's calls evicted an answer", h.Total()-after)
	}
}
