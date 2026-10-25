package main

import (
	"net"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestServe_WiresTheReconcilerThePollTheJobsAndTheWorker follows a request label on a subscribed workspace: the
// reconciler and the poll ask hub with the token of the subscription, the poll makes a job of the label, and the worker
// runs its attempt and refuses the carry-patch workspace.
func TestServe_WiresTheReconcilerThePollTheJobsAndTheWorker(t *testing.T) {
	const pat = "pat-WIDGETS-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat, Mode: "carry_patch"})
	hub.AddIssue("widgets", fakehub.Issue{Number: 42, Labels: []string{"af:fix"}})
	s := newService(t, hub.URL)
	s.subscribe(t, "widgets", pat)
	previous := pollFirstDelay
	pollFirstDelay = func() time.Duration { return 0 }
	t.Cleanup(func() { pollFirstDelay = previous })

	r := s.start(t, net.Listen)
	testutil.Eventually(t, "the poll to make a job of the label", func() bool {
		return len(r.log.Find("INFO", "a request label became a job")) > 0
	})
	id, _ := r.log.Find("INFO", "a request label became a job")[0]["job"].(string)
	var job map[string]any
	testutil.Eventually(t, "the job to fail", func() bool {
		job = testutil.Request(t, "GET", "http://"+r.addr+"/api/v1/jobs/"+id, nil, testutil.Bearer("tok")).Object(t)
		return job["state"] == "failed"
	})
	result, _ := job["result"].(map[string]any)
	if failure, _ := result["error"].(map[string]any); failure["category"] != "unsupported" {
		t.Errorf("the job %v, want the worker's refusal of the carry-patch workspace", job)
	}
	for _, path := range []string{"/api/v1/workspaces/widgets/sandboxes", "/api/v1/workspaces/widgets/issues", "/api/v1/workspaces/widgets"} {
		if hub.Count(path, fakehub.TokenHash(pat)) == 0 {
			t.Errorf("hub was not asked for %s with the token of the subscription", path)
		}
	}
	r.stop()
	if code := r.exit(t); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if r.at("the label poll stopped") < 0 {
		t.Errorf("records %s, want the poll stopped", r.log)
	}
}
