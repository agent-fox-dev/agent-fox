package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

// call makes a request to the service at addr and returns the status and the body.
func call(t *testing.T, method, addr, path, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// object decodes a JSON object.
func object(t *testing.T, s string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("not a JSON object: %q", s)
	}
	return v
}

// slugsOf are the slugs in a list of workspaces.
func slugsOf(t *testing.T, s string) []string {
	t.Helper()
	var body struct {
		Workspaces []struct {
			Slug string `json:"slug"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal([]byte(s), &body); err != nil {
		t.Fatalf("not a list: %q", s)
	}
	var out []string
	for _, w := range body.Workspaces {
		out = append(out, w.Slug)
	}
	return out
}

// hashed is the hex SHA-256 of a token: how the fake hub keys what it counts.
func hashed(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// filesOf are the bytes of the database file and of its WAL, as they are now.
func filesOf(t *testing.T, db string) [][]byte {
	t.Helper()
	var out [][]byte
	for _, f := range []string{db, db + "-wal"} {
		if data, err := os.ReadFile(f); err == nil {
			out = append(out, data)
		}
	}
	return out
}

// stop sends SIGTERM and checks the exit code.
func stop(t *testing.T, p *process) {
	t.Helper()
	p.signal(syscall.SIGTERM)
	if code := p.wait(30 * time.Second); code != 0 {
		t.Errorf("exit code %d after SIGTERM, want 0; stderr:\n%s", code, strings.Join(p.lines, "\n"))
	}
}

// TestAnOperatorStartsProbesAndStopsTheService_TS_11_79 verifies TS-11-79:
// An operator starts agent-fox from a minimal config, probes it and stops it
// with SIGTERM.
// Verifies: 11-PATH-1, 11-REQ-1.6, 11-REQ-7.1
func TestAnOperatorStartsProbesAndStopsTheService_TS_11_79(t *testing.T) {
	hub := fakehub.New(t)
	s := newSetup(t, hub.URL)
	// The minimal config: where hub is, and the key; the key file is relative.
	delete(s.Doc, "database")
	s.Doc["secrets"]["key_file"] = q("key")
	state := t.TempDir()
	env := append(bareEnv(t), "XDG_STATE_HOME="+state)

	p := s.start(t, env)
	addr := p.addr()
	if !strings.HasPrefix(addr, "127.0.0.1:") || strings.HasSuffix(addr, ":0") {
		t.Fatalf("addr = %q, want a port the system picked", addr)
	}

	status, body := call(t, "GET", addr, "/healthz", "", "")
	if status != 200 || body != "{\"status\":\"ok\"}\n" {
		t.Errorf("/healthz = %d %q", status, body)
	}
	status, body = call(t, "GET", addr, "/readyz", "", "")
	if status != 200 || body != "{\"status\":\"ready\"}\n" {
		t.Errorf("/readyz = %d %q", status, body)
	}
	status, body = call(t, "GET", addr, "/version", "", "")
	if doc := object(t, body); status != 200 || doc["name"] != "agent-fox" || len(doc) != 5 {
		t.Errorf("/version = %d %q, want the version document of agent-fox", status, body)
	}
	if hub.Total() != 0 {
		t.Errorf("hub received %d requests for probes that need no token", hub.Total())
	}

	// The default place of the database, and how private it is.
	db := filepath.Join(state, "agent-fox", "agent-fox.db")
	if info, err := os.Stat(db); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the database %s: %v, %v; want a file of mode 0600", db, info, err)
	}
	if info, err := os.Stat(filepath.Dir(db)); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the directory of the database: %v, %v; want mode 0700", info, err)
	}

	stop(t, p)
	if _, err := os.Stat(db + "-wal"); err == nil {
		t.Error("the WAL is still there after the stop: the database was not closed")
	}
}

// TestABadConfigurationIsRefusedBeforeListening_TS_11_80 verifies TS-11-80:
// A config with server.external_url or auth_cache_ttl 16m is refused before
// listening.
// Verifies: 11-PATH-2, 11-REQ-1.4, 11-REQ-2.6, 11-REQ-2.7
func TestABadConfigurationIsRefusedBeforeListening_TS_11_80(t *testing.T) {
	state := t.TempDir()
	env := append(bareEnv(t), "XDG_STATE_HOME="+state)
	for key, change := range map[string]func(*setup){
		"server.external_url": func(s *setup) { s.Doc["server"]["external_url"] = q("https://agent-fox.example.com") },
		"hub.auth_cache_ttl":  func(s *setup) { s.Doc["hub"]["auth_cache_ttl"] = q("16m") },
	} {
		s := newSetup(t, "http://127.0.0.1:1")
		delete(s.Doc, "database")
		change(s)
		r := s.run(t, env)
		oneStartFailure(t, r, 2, key)
		if strings.Contains(r.Stderr, `"listening"`) {
			t.Errorf("%s: a listening record was written", key)
		}
		if _, err := os.Stat(filepath.Join(state, "agent-fox")); err == nil {
			t.Errorf("%s: a database was created", key)
		}
	}
}

// TestACallerSubscribesAndListsWhileAnotherCannotSee_TS_11_81 verifies TS-11-81:
// A caller subscribes widgets with a PAT and lists it, while a caller who cannot
// see widgets does not.
// Verifies: 11-PATH-3, 11-REQ-8.1, 11-REQ-9.2, 11-REQ-9.4
func TestACallerSubscribesAndListsWhileAnotherCannotSee_TS_11_81(t *testing.T) {
	const pat = "pat-DISTINCT-81-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok-A", "usr_A", []string{"widgets"})
	hub.AddUser("tok-B", "usr_B", nil)
	hub.AddUser(pat, "usr_P", []string{"widgets"})
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()

	status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok-A", `{"hub_token":"`+pat+`"}`)
	if status != 201 {
		t.Fatalf("PUT = %d %s, want 201", status, body)
	}
	view := object(t, body)
	if view["slug"] != "widgets" || view["token_user"] != "usr_P" || view["subscribed_by"] != "usr_A" || view["token_state"] != "ok" || view["label_prefix"] != "af:" {
		t.Errorf("view = %s", body)
	}
	if strings.Contains(body, pat) {
		t.Error("the answer holds the PAT")
	}

	// What hub saw: the caller's token asked about, and the token used twice.
	if hub.Count("/api/v1/user", hashed("tok-A")) != 1 || hub.Count("/api/v1/workspaces/widgets", hashed("tok-A")) != 1 {
		t.Errorf("hub saw %d and %d calls with the caller's token, want one each", hub.Count("/api/v1/user", hashed("tok-A")), hub.Count("/api/v1/workspaces/widgets", hashed("tok-A")))
	}
	var seen []string
	for _, r := range hub.RequestsFor(hashed(pat)) {
		seen = append(seen, r.Path)
	}
	// Since agent_fox_label_protocol, the seven label creations follow the two checks.
	want := []string{"/api/v1/workspaces/widgets", "/api/v1/user"}
	for range 7 {
		want = append(want, "/api/v1/workspaces/widgets/labels")
	}
	if !slices.Equal(seen, want) {
		t.Errorf("hub saw %v with the PAT, want the gate, the user call and the seven labels: %v", seen, want)
	}

	// The PAT is in neither the file nor its WAL, while the service runs.
	for _, data := range filesOf(t, s.DB) {
		if bytes.Contains(data, []byte(pat)) {
			t.Error("the database file or its WAL holds the PAT")
		}
	}
	if wal, err := os.Stat(s.DB + "-wal"); err != nil || wal.Size() == 0 {
		t.Errorf("the WAL: %v, %v; the database is expected to be in WAL mode with the subscription in it", wal, err)
	}

	_, listA := call(t, "GET", addr, "/api/v1/workspaces", "tok-A", "")
	_, listB := call(t, "GET", addr, "/api/v1/workspaces", "tok-B", "")
	if got := slugsOf(t, listA); !slices.Equal(got, []string{"widgets"}) {
		t.Errorf("A's list = %v, want widgets", got)
	}
	if got := slugsOf(t, listB); len(got) != 0 {
		t.Errorf("B's list = %v, want none", got)
	}
	status, body = call(t, "GET", addr, "/api/v1/workspaces/widgets", "tok-B", "")
	if e, _ := object(t, body)["error"].(map[string]any); status != 404 || e["code"] != "not_found" {
		t.Errorf("B on widgets = %d %s, want 404 not_found", status, body)
	}
	stop(t, p)
}

// TestASubscriptionIsOkWithTheSameKeyAndUnreadableWithAnother_TS_11_82 verifies TS-11-82:
// A subscription reads back ok after restart with the same key and unreadable
// with a different key.
// Verifies: 11-PATH-4, 11-REQ-9.6
func TestASubscriptionIsOkWithTheSameKeyAndUnreadableWithAnother_TS_11_82(t *testing.T) {
	const pat = "pat-DISTINCT-82-secret"
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets"})
	hub.AddUser(pat, "usr_9", []string{"widgets"})
	s := newSetup(t, hub.URL)
	keyB := writeKey(t, t.TempDir(), 0o600) // another key
	var logs strings.Builder
	view := func(p *process) map[string]any {
		t.Helper()
		status, body := call(t, "GET", p.addr(), "/api/v1/workspaces/widgets", "tok", "")
		if status != 200 {
			t.Fatalf("GET = %d %s", status, body)
		}
		if strings.Contains(body, pat) {
			t.Error("a view holds the PAT")
		}
		return object(t, body)
	}
	finish := func(p *process) {
		stop(t, p)
		logs.WriteString(strings.Join(p.lines, "\n"))
	}

	first := s.start(t, bareEnv(t))
	if status, body := call(t, "PUT", first.addr(), "/api/v1/workspaces/widgets", "tok", `{"hub_token":"`+pat+`"}`); status != 201 {
		t.Fatalf("PUT = %d %s", status, body)
	}
	finish(first)

	second := s.start(t, bareEnv(t)) // the same key
	if got := view(second)["token_state"]; got != "ok" {
		t.Errorf("token_state with the same key = %v, want ok", got)
	}
	finish(second)

	s.Doc["secrets"]["key_file"] = q(keyB) // another key
	third := s.start(t, bareEnv(t))
	v := view(third)
	if v["token_state"] != "unreadable" {
		t.Errorf("token_state with another key = %v, want unreadable", v["token_state"])
	}
	if status, _ := call(t, "GET", third.addr(), "/healthz", "", ""); status != 200 {
		t.Errorf("/healthz with another key = %d, want 200", status)
	}
	finish(third)

	if strings.Contains(logs.String(), pat) {
		t.Error("a log holds the PAT")
	}
}

// TestATokenRevokedOnHubIsEvictedAndRevalidated_TS_11_83 verifies TS-11-83:
// A token revoked on hub is evicted on its next hub 401 and revalidated on the next
// request.
// Verifies: 11-PATH-5, 11-REQ-6.6
func TestATokenRevokedOnHubIsEvictedAndRevalidated_TS_11_83(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets", "gizmos"})
	s := newSetup(t, hub.URL)
	s.Doc["hub"]["auth_cache_ttl"] = q("60s")
	p := s.start(t, bareEnv(t))
	addr := p.addr()

	if status, body := call(t, "GET", addr, "/api/v1/workspaces/widgets", "tok", ""); status != 404 {
		t.Fatalf("warming: %d %s, want 404 (widgets is not subscribed, but the token sees it)", status, body)
	}
	hub.FailOnce("/api/v1/workspaces/gizmos", 401)
	status, body := call(t, "GET", addr, "/api/v1/workspaces/gizmos", "tok", "")
	if e, _ := object(t, body)["error"].(map[string]any); status != 401 || e["code"] != "unauthenticated" {
		t.Fatalf("the request hub refuses = %d %s, want 401 unauthenticated", status, body)
	}
	users := hub.Count("/api/v1/user", hashed("tok"))

	hub.Revoke("tok") // hub has revoked it: /user refuses it now
	status, body = call(t, "GET", addr, "/api/v1/workspaces/widgets", "tok", "")
	if got := hub.Count("/api/v1/user", hashed("tok")); got != users+1 {
		t.Errorf("hub was asked %d times whom the token belongs to, want %d: the token was evicted and is revalidated", got, users+1)
	}
	if e, _ := object(t, body)["error"].(map[string]any); status != 401 || e["code"] != "unauthenticated" {
		t.Errorf("the request after the eviction = %d %s, want 401 unauthenticated", status, body)
	}
	stop(t, p)
}

// TestACallerRemovesASubscriptionAndThenCannotReadIt_TS_11_84 verifies TS-11-84:
// A caller removes a subscription and then cannot read it.
// Verifies: 11-PATH-6, 11-REQ-9.5
func TestACallerRemovesASubscriptionAndThenCannotReadIt_TS_11_84(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", []string{"widgets"})
	hub.AddUser("pat", "usr_9", []string{"widgets"})
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()
	if status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok", `{"hub_token":"pat"}`); status != 201 {
		t.Fatalf("PUT = %d %s", status, body)
	}

	// The test reads the file of the running service, which is in WAL mode.
	rows := func() int {
		t.Helper()
		db, err := sql.Open("sqlite", "file:"+s.DB+"?mode=ro&_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var n int
		if err := db.QueryRowContext(context.Background(), "SELECT count(*) FROM af_subscriptions").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := rows(); n != 1 {
		t.Fatalf("%d rows after the PUT, want 1", n)
	}

	status, body := call(t, "DELETE", addr, "/api/v1/workspaces/widgets", "tok", "")
	if status != 204 || body != "" {
		t.Errorf("DELETE = %d %q, want 204 and no body", status, body)
	}
	if n := rows(); n != 0 {
		t.Errorf("%d rows after the DELETE, want none: the sealed token must be gone", n)
	}
	status, body = call(t, "GET", addr, "/api/v1/workspaces/widgets", "tok", "")
	if e, _ := object(t, body)["error"].(map[string]any); status != 404 || e["code"] != "not_found" {
		t.Errorf("GET after the DELETE = %d %s, want 404 not_found", status, body)
	}
	stop(t, p)
}

// TestEveryLineOfStderrIsARecordAndEachRequestIsOne_TS_11_53 verifies TS-11-53:
// Every stderr line is a slog JSON record and each request logs method, path,
// status, duration and caller.
// Verifies: 11-REQ-7.7
func TestEveryLineOfStderrIsARecordAndEachRequestIsOne_TS_11_53(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok-DISTINCT-53", "usr_1", nil)
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()
	call(t, "GET", addr, "/api/v1/workspaces", "tok-DISTINCT-53", "") // authenticated
	call(t, "GET", addr, "/api/v1/workspaces", "", "")                // not
	call(t, "GET", addr, "/healthz", "", "")
	call(t, "GET", addr, "/nowhere", "", "")
	stop(t, p)

	var requests []map[string]any
	for _, line := range p.lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a line of stderr is not a JSON record: %q", line)
		}
		for _, field := range []string{"time", "level", "msg"} {
			if _, ok := rec[field]; !ok {
				t.Errorf("the record %q has no %s", line, field)
			}
		}
		if rec["msg"] == "request" {
			requests = append(requests, rec)
		}
	}
	if len(requests) != 4 {
		t.Fatalf("%d request records, want one for each of the 4 requests:\n%s", len(requests), strings.Join(p.lines, "\n"))
	}
	for i, want := range []struct {
		path   string
		status float64
		user   string
	}{{"/api/v1/workspaces", 200, "usr_1"}, {"/api/v1/workspaces", 401, ""}, {"/healthz", 200, ""}, {"/nowhere", 404, ""}} {
		rec := requests[i]
		if rec["method"] != "GET" || rec["path"] != want.path || rec["status"] != want.status {
			t.Errorf("record %d = %v, want GET %s %v", i, rec, want.path, want.status)
		}
		if _, ok := rec["duration"]; !ok {
			t.Errorf("record %d has no duration", i)
		}
		if user, has := rec["user"]; want.user == "" && has || want.user != "" && user != want.user {
			t.Errorf("record %d: user = %v (present %v), want %q", i, user, has, want.user)
		}
	}
	for _, needle := range []string{"tok-DISTINCT-53", "Bearer", "Authorization"} {
		if strings.Contains(strings.Join(p.lines, "\n"), needle) {
			t.Errorf("stderr holds %q", needle)
		}
	}
}

// TestACallerSubscribesAWorkspaceAndTheSevenLabelsAreCreated_TS_17_64 verifies TS-17-64 and 17-PATH-1 through the binary: a
// PUT creates the seven labels of the table on the workspace with their colours and descriptions, logs their counts and
// stores the subscription; a second PUT answers 200 and changes nothing on hub.
// Verifies: 17-PATH-1, 17-REQ-2.1, 17-REQ-2.2
func TestACallerSubscribesAWorkspaceAndTheSevenLabelsAreCreated_TS_17_64(t *testing.T) {
	const pat = "pat-DISTINCT-64-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok-A", "usr_A", []string{"widgets"})
	hub.AddUser(pat, "usr_P", []string{"widgets"})
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()

	if status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok-A", `{"hub_token":"`+pat+`","label_prefix":"af:"}`); status != 201 {
		t.Fatalf("PUT = %d %s, want 201", status, body)
	}
	want := []fakehub.Label{
		{Name: "af:triage", Color: "0e8a16", Description: "Ask agent-fox to triage this issue"},
		{Name: "af:fix", Color: "0e8a16", Description: "Ask agent-fox to fix this issue"},
		{Name: "af:in-progress", Color: "fbca04", Description: "agent-fox is working on this issue"},
		{Name: "af:ready", Color: "1d76db", Description: "Triaged: the diagnosis is in the body"},
		{Name: "af:needs-human", Color: "d93f0b", Description: "agent-fox stopped with a question; see the last comment"},
		{Name: "af:landed", Color: "0e8a16", Description: "A branch and a pull request (or a carry patch) exist"},
		{Name: "af:failed", Color: "b60205", Description: "agent-fox could not finish; see the last comment"},
	}
	if got := hub.Labels("widgets"); !slices.Equal(got, want) {
		t.Errorf("hub holds %v, want the seven af: labels of the table", got)
	}
	rec := p.waitFor("the labels of the workspace are in place")
	if rec["workspace"] != "widgets" || rec["created"] != float64(7) || rec["existing"] != float64(0) {
		t.Errorf("the record is %v, want widgets with 7 created and none already there", rec)
	}
	if status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok-A", `{"hub_token":"`+pat+`","label_prefix":"af:"}`); status != 200 {
		t.Fatalf("the second PUT = %d %s, want 200", status, body)
	}
	if got := hub.Labels("widgets"); !slices.Equal(got, want) {
		t.Errorf("after the second PUT hub holds %v", got)
	}
	if status, body := call(t, "GET", addr, "/api/v1/workspaces/widgets", "tok-A", ""); status != 200 || object(t, body)["label_prefix"] != "af:" {
		t.Errorf("GET = %d %s, want the stored subscription", status, body)
	}
	stop(t, p)
}

// TestTheBinaryWiresTheCancelCleanup is the part of the label protocol that only the binary can show: the jobs layer that
// cmd/agent-fox builds has the hub-backed cancel cleanup, so that a job cancelled while it waits behind another of its
// workspace gets its cleanup tried and done, and the job document shows it.
// Verifies: 17-REQ-5.1, 17-REQ-5.7
func TestTheBinaryWiresTheCancelCleanup(t *testing.T) {
	const pat = "pat-DISTINCT-CLEAN-0123456789"
	hub := fakehub.New(t)
	hub.AddUser("tok-A", "usr_A", []string{"widgets"})
	hub.AddUser(pat, "usr_P", []string{"widgets"})
	hub.AddWorkspace(t, fakehub.Workspace{Slug: "widgets", PAT: pat, Mode: "carry_patch"})
	hub.AddIssue("widgets", fakehub.Issue{Number: 43, Labels: []string{"af:in-progress"}})
	s := newSetup(t, hub.URL)
	p := s.start(t, bareEnv(t))
	addr := p.addr()
	if status, body := call(t, "PUT", addr, "/api/v1/workspaces/widgets", "tok-A", `{"hub_token":"`+pat+`"}`); status != 201 {
		t.Fatalf("PUT = %d %s", status, body)
	}
	// The worker reads the workspace record first: held, the first job keeps the workspace while the second waits.
	hub.Inject("GET", "/api/v1/workspaces/widgets", fakehub.Fault{Delay: 2 * time.Second})
	_, first := call(t, "POST", addr, "/api/v1/workspaces/widgets/fix", "tok-A", `{"issue": 42}`)
	awaitJob(t, addr, "tok-A", object(t, first)["id"].(string), "running")
	status, second := call(t, "POST", addr, "/api/v1/workspaces/widgets/fix", "tok-A", `{"issue": 43}`)
	if status != 202 {
		t.Fatalf("POST fix = %d %s", status, second)
	}
	id := object(t, second)["id"].(string)
	status, cancelled := call(t, "POST", addr, "/api/v1/jobs/"+id+"/cancel", "tok-A", "")
	if status != 200 {
		t.Fatalf("cancel = %d %s", status, cancelled)
	}
	if labels, _ := object(t, cancelled)["labels"].(map[string]any); labels["cleanup"] != "pending" {
		t.Errorf("the cancel answered labels %v, want cleanup pending", labels)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		labels, _ := jobIn(t, addr, "tok-A", id)["labels"].(map[string]any)
		if labels["cleanup"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cleanup did not end: labels %v", labels)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Another job of the issue's workspace ran, but none of issue 43: in-progress was left by an attempt that is gone.
	if i, _ := hub.IssueOf("widgets", 43); len(i.Labels) != 0 {
		t.Errorf("issue 43 carries %v, want in-progress removed", i.Labels)
	}
	stop(t, p)
}
