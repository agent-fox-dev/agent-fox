package fakehub

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// postLabel posts a label to the label route of the slug with the token and returns the status and the body.
func postLabel(t *testing.T, h *Hub, slug, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, h.URL+"/api/v1/workspaces/"+slug+"/labels", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

// TestTheFakeHubServesLabelCreationForAnyGrantedSlugAndKeepsTheLabels_TS_17_25 verifies TS-17-25.
// Verifies: 17-REQ-4.6
func TestTheFakeHubServesLabelCreationForAnyGrantedSlugAndKeepsTheLabels_TS_17_25(t *testing.T) {
	h := New(t)
	h.AddUser("pat", "usr_1", []string{"widgets"}) // granted, never added with AddWorkspace
	h.AddUser("other", "usr_2", nil)
	h.AddWorkspaceToken("bound", []string{"gadgets"})

	if status, body := postLabel(t, h, "widgets", "pat", `{"name":"af:fix","color":"0e8a16","description":"Ask agent-fox to fix this issue"}`); status != 201 {
		t.Fatalf("a new label: %d %s, want 201", status, body)
	}
	if status, body := postLabel(t, h, "widgets", "pat", `{"name":"af:fix","color":"ff0000","description":"changed"}`); status != 200 || !strings.Contains(body, "0e8a16") {
		t.Errorf("the same name again: %d %s, want 200 with the stored label", status, body)
	}
	want := []Label{{Name: "af:fix", Color: "0e8a16", Description: "Ask agent-fox to fix this issue"}}
	if got := h.Labels("widgets"); len(got) != 1 || got[0] != want[0] {
		t.Errorf("Labels(widgets) = %v, want %v: the stored label unchanged", got, want)
	}
	if status, _ := postLabel(t, h, "gadgets", "bound", `{"name":"af:triage","color":"0e8a16"}`); status != 201 {
		t.Errorf("a token of AddWorkspaceToken: %d, want 201", status)
	}
	if status, _ := postLabel(t, h, "widgets", "other", `{"name":"af:ready","color":"1d76db"}`); status != h.HiddenStatus {
		t.Errorf("a token that is not granted the slug: %d, want %d as for other routes", status, h.HiddenStatus)
	}
	if status, _ := postLabel(t, h, "widgets", "unknown", `{"name":"af:ready","color":"1d76db"}`); status != 401 {
		t.Errorf("an unknown token: %d, want 401", status)
	}
	if n := len(h.RequestsTo(http.MethodPost, "/api/v1/workspaces/widgets/labels")); n != 4 {
		t.Errorf("%d requests to the label route of widgets recorded, want every one of the 4", n)
	}
	if got := h.Labels("widgets"); len(got) != 1 {
		t.Errorf("a refused request stored a label: %v", got)
	}
	// A test can change a stored label, as a person would on the forge.
	h.PutLabel("widgets", Label{Name: "af:fix", Color: "000000", Description: "mine"})
	if got := h.Labels("widgets"); got[0].Color != "000000" {
		t.Errorf("PutLabel did not change the label: %v", got)
	}
}

// TestTheFaultSeamsApplyToTheLabelRoute_TS_17_26 verifies TS-17-26.
// Verifies: 17-REQ-4.7
func TestTheFaultSeamsApplyToTheLabelRoute_TS_17_26(t *testing.T) {
	h := New(t)
	h.AddUser("pat", "usr_1", []string{"widgets"})
	const path = "/api/v1/workspaces/widgets/labels"
	body := `{"name":"af:fix","color":"0e8a16"}`

	h.Fail(path, 503)
	for range 2 {
		if status, _ := postLabel(t, h, "widgets", "pat", body); status != 503 {
			t.Errorf("Fail: %d, want 503", status)
		}
	}
	h.Clear()
	h.FailOnce(path, 403)
	if status, _ := postLabel(t, h, "widgets", "pat", body); status != 403 {
		t.Errorf("FailOnce: %d, want 403", status)
	}
	if status, _ := postLabel(t, h, "widgets", "pat", body); status != 201 {
		t.Errorf("after FailOnce: %d, want 201: the fault applies once", status)
	}
	h.FailFor(path, "pat", 401)
	if status, _ := postLabel(t, h, "widgets", "pat", body); status != 401 {
		t.Errorf("FailFor: %d, want 401", status)
	}
	h.Clear()
	h.Inject(http.MethodPost, path, Fault{Status: 418, Body: "any body at all"})
	if status, got := postLabel(t, h, "widgets", "pat", body); status != 418 || got != "any body at all" {
		t.Errorf("Inject: %d %q, want 418 with the body", status, got)
	}
	h.Clear()
	h.Inject(http.MethodPost, path, Fault{Status: 422})
	if status, got := postLabel(t, h, "widgets", "pat", body); status != 422 || !strings.Contains(got, `"error_type"`) || !strings.Contains(got, `"message"`) {
		t.Errorf("Inject with no body: %d %q, want 422 with a hub error object", status, got)
	}
}
