package main

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	afclock "github.com/agent-fox-dev/agentfox/internal/afclient/fakeclock"
)

// echoing is a handler for the service of the secrecy test: for a request of the kind it answers
// the status, and what it says back repeats the request, its Authorization header and its body, in
// the messages of its errors and in the warnings of an envelope.
func echoing(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		echo := fmt.Sprintf("%s %s with %q and %s", r.Method, r.URL.Path, r.Header.Get("Authorization"), body)
		quoted := strings.ReplaceAll(echo, `"`, `\"`)
		switch {
		case strings.HasSuffix(r.URL.Path, "/events"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, eventFrame(1))
			_, _ = io.WriteString(w, "id: 2\nevent: step\ndata: {\"seq\":2,\"ts\":\"t\",\"job_id\":\"j\",\"attempt\":1,\"type\":\"step\",\"data\":{\"echo\":\""+quoted+"\"}}\n\n")
		case strings.Contains(r.URL.Path, "/attempts/"):
			answering(200, `{"status":"done","warnings":[{"code":"echo","message":"`+quoted+`"}]}`)(w, r)
		case status >= 400:
			answering(status, `{"error":{"code":"echo","message":"`+quoted+`"}}`)(w, r)
		case r.Method == "GET" && (strings.HasSuffix(r.URL.Path, "/jobs/j") || strings.HasSuffix(r.URL.Path, "/jobs/job_abc")):
			answering(200, jobDocument("failed", attemptList("failed"), `{"status":"failed","exit_code":1,"summary":"`+quoted+`","error":{"category":"brain","message":"`+quoted+`","retryable":false,"fix_hint":null}}`, ""))(w, r)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/workspaces"):
			answering(200, `{"workspaces":[]}`)(w, r)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/jobs"):
			answering(200, `{"jobs":[],"next_cursor":null}`)(w, r)
		case r.Method == "PUT":
			answering(201, subscriptionView)(w, r)
		default:
			answering(202, queued)(w, r)
		}
	}
}

// TestASentinelCredentialAndAPATNeverAppearInAnyOutput_TS_16_27 verifies TS-16-27: for random
// sentinels, commands and answers of a service that repeats the request into its messages and
// warnings, the credential and the token of a workspace are in none of stdout, stderr and the
// events.
// Verifies: 16-REQ-4.6
func TestASentinelCredentialAndAPATNeverAppearInAnyOutput_TS_16_27(t *testing.T) {
	statuses := []int{200, 400, 401, 404, 409, 422, 500, 502, 503}
	useClockNow(t)
	for seed := int64(0); seed < 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		cred := fmt.Sprintf("cred-%016x", rng.Uint64())
		pat := fmt.Sprintf("pat-%016x", rng.Uint64())
		status := statuses[rng.Intn(len(statuses))]
		inDir(t)
		svc := newFakeService(t, echoing(status))
		_, config := clientHome(t, nil)
		t.Setenv("AF_URL", svc.URL)
		patFile := tokenFile(t, pat+"\n")
		if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte("token = \""+cred+"\"\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		commands := [][]string{
			{"workspace", "subscribe", "w", "--hub-token-file", patFile},
			{"workspace", "list"}, {"workspace", "get", "w"}, {"workspace", "unsubscribe", "w"},
			{"jobs", "list", "--workspace", "w"}, {"jobs", "get", "j"}, {"jobs", "cancel", "j"}, {"jobs", "retry", "j"},
			{"jobs", "answer", "j", "--option", "A"}, {"jobs", "watch", "j"},
			{"jobs", "submit", "fix", "1", "--workspace", "w"}, {"jobs", "submit", "fix", "1", "--workspace", "w", "--wait"}, {"jobs", "submit", "triage", "some words", "--workspace", "w", "--wait"},
		}
		args := slices.Clone(commands[rng.Intn(len(commands))])
		res, events := afRunEvents(t, args...)

		files := map[string]string{"stdout": res.stdout, "stderr": res.stderr, "events": events}
		for name, content := range files {
			for _, secret := range []string{cred, pat} {
				if strings.Contains(content, secret) {
					t.Fatalf("seed %d, %v with the status %d: %s holds %q:\n%s", seed, args, status, name, secret, content)
				}
			}
		}
	}
}

// TestAnInterruptOrTheTimeoutOfFixWaitSendsNoCancelAndSuggestsWatchAndCancel_TS_16_75 verifies
// TS-16-75 for af fix --workspace --wait, which the same test checks for af jobs watch.
// Verifies: 16-REQ-10.7
func TestAnInterruptOrTheTimeoutOfFixWaitSendsNoCancelAndSuggestsWatchAndCancel_TS_16_75(t *testing.T) {
	for _, c := range []struct {
		name      string
		extra     []string
		fromFlag  bool
		interrupt bool
		category  string
	}{
		{"a signal", nil, false, true, "cancelled"},
		{"a signal, server from the flag", []string{"--server", "SERVER"}, true, true, "cancelled"},
		{"--timeout", []string{"--timeout", "400ms"}, false, false, "timeout"},
	} {
		t.Run(c.name, func(t *testing.T) {
			inDir(t)
			var streaming atomic.Bool
			svc := ready(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "POST":
					answering(202, queued)(w, r)
				case strings.HasSuffix(r.URL.Path, "/events"):
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, eventFrame(1))
					w.(http.Flusher).Flush()
					streaming.Store(true)
					<-r.Context().Done()
				default:
					answering(200, strings.ReplaceAll(jobDocument("running", attemptList("running"), "", ""), `"id":"j"`, `"id":"job_abc"`))(w, r)
				}
			})
			args := []string{"jobs", "submit", "fix", "42", "--workspace", "widgets", "--wait"}
			for _, a := range c.extra {
				args = append(args, strings.Replace(a, "SERVER", svc.URL, 1))
			}
			useClock(t, afclock.New(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var out, errb strings.Builder
			done := make(chan int, 1)
			go func() {
				done <- newApp().Main(ctx, append([]string{"af"}, args...), strings.NewReader(""), &out, &errb)
			}()
			if c.interrupt {
				for deadline := time.Now().Add(10 * time.Second); !streaming.Load() && time.Now().Before(deadline); {
					time.Sleep(time.Millisecond)
				}
				cancel()
			}
			select {
			case code := <-done:
				if code != 1 {
					t.Errorf("exit %d, want 1", code)
				}
			case <-time.After(20 * time.Second):
				t.Fatal("the command did not end")
			}
			env := oneDocument(t, out.String())
			if got := field(env, "error", "category"); got != c.category {
				t.Errorf("category %v, want %s", got, c.category)
			}
			for _, r := range svc.Requests() {
				if strings.HasSuffix(r.Path, "/cancel") {
					t.Errorf("a cancel request was sent: %s %s", r.Method, r.Path)
				}
			}
			if artifacts, _ := env["artifacts"].([]any); len(artifacts) != 1 || artifacts[0].(map[string]any)["name"] != "job_abc" {
				t.Errorf("artifacts = %v, want the job", artifacts)
			}
			var argvs []string
			for _, argv := range nextArgvs(env) {
				argvs = append(argvs, strings.Join(argv, " "))
			}
			suffix := ""
			if c.fromFlag {
				suffix = " --server " + svc.URL
			}
			if want := []string{"af jobs watch job_abc" + suffix, "af jobs cancel job_abc" + suffix}; !slices.Equal(argvs, want) {
				t.Errorf("next = %v, want %v", argvs, want)
			}
		})
	}
}
