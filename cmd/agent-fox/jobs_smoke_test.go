package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// jobIn reads the job at the path as a generic document.
func jobIn(t *testing.T, addr, token, id string) map[string]any {
	t.Helper()
	status, body := call(t, "GET", addr, "/api/v1/jobs/"+id, token, "")
	if status != 200 {
		t.Fatalf("GET /jobs/%s = %d %s", id, status, body)
	}
	return object(t, body)
}

// awaitJob reads the job until it is in the state, within 30 seconds.
func awaitJob(t *testing.T, addr, token, id, state string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if doc := jobIn(t, addr, token, id); doc["state"] == state {
			return doc
		}
		if time.Now().After(deadline) {
			doc, _ := json.Marshal(jobIn(t, addr, token, id))
			t.Fatalf("job %s did not reach %s in 30 s: %s", id, state, doc)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestTheBinaryWiresTheJobsLayerWithTheWorkerAndKeepsJobsAcrossARestart_TS_12_34
// is the part of TS-12-34 that only the binary can show, and the check of 12-REQ-3.1, 12-REQ-4.1 and
// 12-REQ-8.7 in production wiring: the process started from a configuration makes a job through the
// route, the queue it starts hands the attempt to the worker (it was the placeholder before the worker
// existed), which reads the workspace from hub with the token of the subscription and refuses a fix on
// a carry-patch workspace as unsupported and not retryable; the stored envelope reads back; a second
// job of the workspace is cancelled by the DELETE of the subscription; and after SIGTERM and a start
// on the same database the jobs are still there and the service stops with exit code 0 again.
// Verifies: 12-REQ-4.1, 12-REQ-5.7, 13-REQ-8.1
func TestTheBinaryWiresTheJobsLayerWithTheWorkerAndKeepsJobsAcrossARestart_TS_12_34(t *testing.T) {
	const pat = "pat-DISTINCT-34-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok-A", "usr_A", []string{"widgets"})
	hub.AddUser(pat, "usr_P", []string{"widgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat, Mode: "carry_patch"})
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()

	if status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok-A", `{"hub_token":"`+pat+`"}`); status != 201 {
		t.Fatalf("PUT = %d %s", status, body)
	}
	status, body := call(t, "POST", addr, "/api/v1/workspaces/widgets/fix", "tok-A", `{"issue": 42}`)
	if status != 202 {
		t.Fatalf("POST fix = %d %s, want 202", status, body)
	}
	job := object(t, body)
	id, _ := job["id"].(string)
	if job["state"] != "queued" || !strings.HasPrefix(id, "job_") {
		t.Fatalf("the job = %s, want a queued job", body)
	}

	failed := awaitJob(t, addr, "tok-A", id, "failed")
	result, _ := failed["result"].(map[string]any)
	failure, _ := result["error"].(map[string]any)
	if result["exit_code"] != float64(1) || failure["category"] != "unsupported" || failure["retryable"] != false {
		t.Errorf("result = %v, want exit code 1 and an error of category unsupported that is not retryable", result)
	}
	if msg, _ := failure["message"].(string); !strings.Contains(msg, "carry-patch") {
		t.Errorf("the failure says %q, want the worker's refusal of carry-patch landing and not the placeholder's", msg)
	}
	// Only the worker reads the workspace from hub with the token of the subscription.
	if n := hub.Count("/api/v1/workspaces/widgets", fakehub.TokenHash(pat)); n < 1 {
		t.Errorf("hub was asked %d times for the workspace with the token of the subscription, want the worker's read", n)
	}
	if n := hub.Count("/api/v1/workspaces/widgets/vars/resolved", fakehub.TokenHash(pat)); n < 1 {
		t.Errorf("hub was asked %d times for the variables with the token of the subscription", n)
	}
	if attempts, _ := failed["attempts"].([]any); len(attempts) != 1 {
		t.Errorf("the job has %d attempts, want only the first: an unsupported failure is not retried", len(attempts))
	}
	status, envelope := call(t, "GET", addr, "/api/v1/jobs/"+id+"/attempts/1/envelope", "tok-A", "")
	var env struct {
		Status string
		Error  struct{ Category string }
	}
	if err := json.Unmarshal([]byte(envelope), &env); status != 200 || err != nil || env.Status != "failed" || env.Error.Category != "unsupported" {
		t.Errorf("the stored envelope = %d %s (%v), want the failed envelope of the worker's refusal", status, envelope, err)
	}
	if status, _ := call(t, "GET", addr, "/api/v1/jobs/"+id+"/attempts/1/report", "tok-A", ""); status != 404 {
		t.Errorf("the report of an attempt that has none = %d, want 404", status)
	}
	if _, list := call(t, "GET", addr, "/api/v1/jobs", "tok-A", ""); !strings.Contains(list, id) {
		t.Errorf("GET /jobs = %s, want the job in it", list)
	}

	// A second job and the removal of the subscription: the DELETE cancels the open job.
	_, body = call(t, "POST", addr, "/api/v1/workspaces/widgets/triage", "tok-A", `{"text": "the pager skips an item"}`)
	second, _ := object(t, body)["id"].(string)
	if status, body := call(t, "DELETE", addr, "/api/v1/workspaces/widgets", "tok-A", ""); status != 204 {
		t.Fatalf("DELETE = %d %s, want 204", status, body)
	}
	if state := jobIn(t, addr, "tok-A", second)["state"]; state != "cancelled" && state != "failed" {
		t.Errorf("the second job reads %v after the unsubscribe, want it finished (cancelled, or failed if the runner got there first)", state)
	}

	stop(t, p)
	for _, rec := range p.records() {
		if rec["level"] == "ERROR" || rec["level"] == "WARN" {
			t.Errorf("a record at %v in a run with nothing wrong: %v", rec["level"], rec["msg"])
		}
	}

	// The same database, started again: the jobs are there, and nothing runs again.
	p2 := s.start(t, bareEnv(t))
	addr2 := p2.addr()
	if doc := jobIn(t, addr2, "tok-A", id); doc["state"] != "failed" {
		t.Errorf("after the restart the first job reads %v, want failed", doc["state"])
	}
	stop(t, p2)
}
