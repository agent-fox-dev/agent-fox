package fakehub

import (
	"reflect"
	"testing"
)

func TestSessions_AreOpenedReportedAndCompleted(t *testing.T) {
	h := newHub(t, Workspace{})
	r := do(t, h, "POST", "/api/v1/sessions", pat, map[string]string{"workspace_slug": "acme", "archetype": "fix", "model": "m1", "run_id": "r1"})
	opened := r.Object(t)
	if r.Status != 201 || opened["id"] != "ses_0001" || opened["status"] != "running" || opened["workspace_slug"] != "acme" {
		t.Fatalf("the open: %d %s", r.Status, r.Body)
	}
	if s := h.Sessions(); len(s) != 1 || s[0].Status != "open" || s[0].RunID != "r1" || s[0].Archetype != "fix" || s[0].Token != TokenHash(pat) || s[0].Seq == 0 {
		t.Errorf("sessions after the open = %+v", s)
	}
	usage := map[string]any{"model": "m1", "input_tokens": 10, "output_tokens": 5, "cost_usd": 0.5, "cost_source": "reported", "brain": "claude", "turns": 3}
	if r := do(t, h, "POST", "/api/v1/sessions/ses_0001/usage", pat, usage); r.Status != 201 {
		t.Errorf("the usage: %d %s", r.Status, r.Body)
	}
	if r := do(t, h, "POST", "/api/v1/sessions/ses_0001/complete", pat, map[string]string{"status": "failed", "error_message": "no lease"}); r.Status != 200 {
		t.Errorf("the completion: %d %s", r.Status, r.Body)
	}
	s := h.Sessions()
	want := []Usage{{Model: "m1", InputTokens: 10, OutputTokens: 5, CostUSD: 0.5, CostSource: "reported", Brain: "claude", Turns: 3}}
	if len(s) != 1 || !reflect.DeepEqual(s[0].Usage, want) || s[0].Status != "failed" || s[0].Message != "no lease" || s[0].CompletedSeq <= s[0].Seq {
		t.Errorf("sessions after the report = %+v", s)
	}
}

func TestSessions_RefuseWhatHubRefuses(t *testing.T) {
	h := newHub(t, Workspace{})
	h.AddUser("other", "usr_2", nil)
	do(t, h, "POST", "/api/v1/sessions", pat, map[string]string{"workspace_slug": "acme"})
	for _, c := range []struct {
		name, path, token string
		body              any
		want              int
	}{
		{"an unknown token", "/api/v1/sessions", "unknown", map[string]string{"workspace_slug": "acme"}, 401},
		{"no workspace", "/api/v1/sessions", pat, map[string]string{}, 400},
		{"a workspace the token does not see", "/api/v1/sessions", "other", map[string]string{"workspace_slug": "acme"}, 404},
		{"a malformed body", "/api/v1/sessions", pat, "{", 400},
		{"a usage with no model", "/api/v1/sessions/ses_0001/usage", pat, map[string]any{"input_tokens": 1}, 400},
		{"a malformed usage", "/api/v1/sessions/ses_0001/usage", pat, "{", 400},
		{"another's session", "/api/v1/sessions/ses_0001/usage", "other", map[string]any{"model": "m"}, 404},
		{"a status hub does not take", "/api/v1/sessions/ses_0001/complete", pat, map[string]string{"status": "done"}, 400},
		{"a malformed completion", "/api/v1/sessions/ses_0001/complete", pat, "{", 400},
		{"a session that is not there", "/api/v1/sessions/ses_0009/complete", pat, map[string]string{"status": "completed"}, 404},
		{"a route that is not hub's", "/api/v1/sessions/ses_0001/other", pat, map[string]string{}, 404},
	} {
		if r := do(t, h, "POST", c.path, c.token, c.body); r.Status != c.want {
			t.Errorf("%s: %d %s, want %d", c.name, r.Status, r.Body, c.want)
		}
	}
	if s := h.Sessions(); len(s) != 1 || len(s[0].Usage) != 0 || s[0].Status != "open" {
		t.Errorf("a refused request changed the sessions: %+v", s)
	}
}
