package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// TestServe_AStartThatFailsWritesOneRecordAndBindsNothing covers each step of the start: the record is the only one,
// so neither the warning of a hub in clear nor a listening record was written.
func TestServe_AStartThatFailsWritesOneRecordAndBindsNothing(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	for name, tc := range map[string]struct {
		change func(*service) string // changes the service after it is written, and is what the error names
		code   int
		noDB   bool
	}{
		"no configuration file":          {func(s *service) string { s.config = filepath.Join(s.dir, "none.toml"); return s.config }, 2, true},
		"a value out of range":           {func(s *service) string { s.extra = "[workers]\ncount = 0\n"; s.write(t); return "workers.count" }, 2, true},
		"no key file":                    {func(s *service) string { s.key = filepath.Join(s.dir, "nowhere"); s.write(t); return s.key }, 2, true},
		"a database that is a directory": {func(s *service) string { s.db = s.dir; s.write(t); return s.dir }, 2, false},
		"no room for the attempts": {func(s *service) string {
			testutil.WriteFile(t, filepath.Dir(s.db), "attempts", "a file")
			return "attempts"
		}, 2, false},
		"an address that is taken": {func(s *service) string { s.listen = taken.Addr().String(); s.write(t); return s.listen }, 1, false},
	} {
		t.Run(name, func(t *testing.T) {
			s := newService(t, "http://hub.example.internal:8080")
			s.write(t)
			needle := tc.change(s)
			log := &testutil.Log{}
			if code := serve(context.Background(), s.config, log.Logger(), net.Listen); code != tc.code {
				t.Errorf("exit code %d, want %d", code, tc.code)
			}
			recs := log.Records()
			if len(recs) != 1 || recs[0]["level"] != "ERROR" || recs[0]["msg"] != "start failed" ||
				!strings.Contains(recs[0]["error"].(string), needle) {
				t.Errorf("records %v, want one ERROR start failed naming %q", recs, needle)
			}
			if _, err := os.Stat(s.db); tc.noDB && err == nil {
				t.Error("the database was created")
			}
		})
	}
}

func TestServe_StopDrainsTheRequestsInFlightAndClosesTheDatabase(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", nil)
	hub.Delay("/api/v1/user", 300*time.Millisecond)
	s := newService(t, hub.URL)
	r := s.start(t, net.Listen)
	answered := make(chan int, 1)
	go func() {
		answered <- testutil.Request(t, "GET", "http://"+r.addr+"/api/v1/workspaces", nil, testutil.Bearer("tok")).Status
	}()
	testutil.Eventually(t, "the request to reach hub", func() bool { return len(hub.RequestsTo("GET", "/api/v1/user")) > 0 })
	r.stop()
	if status := <-answered; status != 200 {
		t.Errorf("the request in flight got %d, want 200", status)
	}
	if code := r.exit(t); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if bad := append(r.log.Find("ERROR", ""), r.log.Find("WARN", "")...); len(bad) > 0 {
		t.Errorf("records %v on a clean stop", bad)
	}
	if _, err := os.Stat(s.db + "-wal"); err == nil {
		t.Error("the WAL is still there: the database was not closed")
	}
}

func TestServe_AListenerThatFailsOnceServingExits1(t *testing.T) {
	s := newService(t, "http://127.0.0.1:1")
	r := s.start(t, failingListen)
	if code := r.exit(t); code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	failed := r.log.Find("ERROR", "the listener failed")
	if len(failed) != 1 || !strings.Contains(failed[0]["error"].(string), "accept failed") {
		t.Errorf("records %s, want the error of the listener", r.log)
	}
	if _, err := os.Stat(s.db + "-wal"); err == nil {
		t.Error("the WAL is still there: the database was not closed")
	}
}

func TestServe_APlainHTTPHubOnAnotherHostIsAWarningBeforeListening(t *testing.T) {
	for url, host := range map[string]string{
		"http://hub.example.internal:8080": "hub.example.internal:8080",
		"http://127.0.0.1:1":               "",
		"http://localhost:1":               "",
		"https://hub.example.com":          "",
	} {
		t.Run(url, func(t *testing.T) {
			r := newService(t, url).start(t, net.Listen)
			warns := r.log.Find("WARN", "in clear")
			switch {
			case host == "" && len(warns) > 0:
				t.Errorf("a warning %v", warns)
			case host != "" && (len(warns) != 1 || warns[0]["hub_host"] != host || r.at(warns[0]["msg"].(string)) > r.at("listening")):
				t.Errorf("records %s, want one warning naming %s before the listening record", r.log, host)
			}
		})
	}
}
