package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

var ctx = context.Background()

// tomlDoc is a configuration file: tables of keys with their TOML values.
type tomlDoc map[string]map[string]string

func (d tomlDoc) String() string {
	var b strings.Builder
	tables := make([]string, 0, len(d))
	for table := range d {
		tables = append(tables, table)
	}
	slices.Sort(tables)
	for _, table := range tables {
		fmt.Fprintf(&b, "[%s]\n", table)
		keys := make([]string, 0, len(d[table]))
		for key := range d[table] {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "%s = %s\n", key, d[table][key])
		}
	}
	return b.String()
}

// q is a TOML string.
func q(s string) string { return strconv.Quote(s) }

// writeKey writes a key file of 32 random bytes with the mode and returns its path.
func writeKey(t *testing.T, dir string, mode os.FileMode) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "key")
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(raw)+"\n"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// setup is a service under test: a directory with a configuration file, a key and
// the place of the database. Doc is the configuration, which a test changes before
// it writes it.
type setup struct {
	Dir, Config, Key, DB string
	Doc                  tomlDoc
}

// newSetup makes a configuration that listens on a port of the loopback interface
// that the system picks, against the hub at hubURL, and writes it.
func newSetup(t *testing.T, hubURL string) *setup {
	t.Helper()
	dir := t.TempDir()
	s := &setup{Dir: dir, Config: filepath.Join(dir, "config.toml"), DB: filepath.Join(dir, "state", "agent-fox.db")}
	s.Key = writeKey(t, dir, 0o600)
	s.Doc = tomlDoc{
		"server":   {"listen": q("127.0.0.1:0")},
		"hub":      {"url": q(hubURL)},
		"secrets":  {"key_file": q(s.Key)},
		"database": {"path": q(s.DB)},
	}
	s.write(t)
	return s
}

// write writes the configuration.
func (s *setup) write(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(s.Config, []byte(s.Doc.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// run runs the service with the configuration until it ends by itself.
func (s *setup) run(t *testing.T, env []string) result {
	t.Helper()
	s.write(t)
	return execute(t, agentFox(t), s.Dir, env, "--config", s.Config)
}

// process is a running agent-fox, with its stderr read as it comes.
type process struct {
	t      *testing.T
	cmd    *exec.Cmd
	mu     sync.Mutex
	lines  []string
	arrive chan struct{}
	done   chan struct{}
	code   int
}

// start starts the service with the configuration and returns once it runs.
func (s *setup) start(t *testing.T, env []string) *process {
	t.Helper()
	s.write(t)
	p := &process{t: t, arrive: make(chan struct{}, 1), done: make(chan struct{})}
	p.cmd = exec.Command(agentFox(t), "--config", s.Config)
	p.cmd.Dir, p.cmd.Env = s.Dir, env
	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(p.done)
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
		for scanner.Scan() {
			p.mu.Lock()
			p.lines = append(p.lines, scanner.Text())
			p.mu.Unlock()
			select {
			case p.arrive <- struct{}{}:
			default:
			}
		}
		err := p.cmd.Wait()
		if exit, ok := err.(*exec.ExitError); ok {
			p.code = exit.ExitCode()
		} else if err != nil {
			p.code = -1
		}
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

// records are the lines of stderr so far, as log records.
func (p *process) records() []map[string]any {
	p.t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []map[string]any
	for _, line := range p.lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			p.t.Fatalf("a line of stderr is not a JSON record: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

// waitFor waits for a record with the message, and returns it.
func (p *process) waitFor(msg string) map[string]any {
	p.t.Helper()
	deadline := time.After(20 * time.Second)
	for {
		for _, rec := range p.records() {
			if rec["msg"] == msg {
				return rec
			}
		}
		select {
		case <-p.arrive:
		case <-p.done:
			for _, rec := range p.records() {
				if rec["msg"] == msg {
					return rec
				}
			}
			p.t.Fatalf("the service ended (exit %d) without a %q record; stderr:\n%s", p.code, msg, strings.Join(p.lines, "\n"))
		case <-deadline:
			p.t.Fatalf("no %q record within 20s; stderr:\n%s", msg, strings.Join(p.lines, "\n"))
		}
	}
}

// addr is the address the service says it listens on.
func (p *process) addr() string {
	p.t.Helper()
	return p.waitFor("listening")["addr"].(string)
}

// wait waits for the service to end and returns its exit code.
func (p *process) wait(within time.Duration) int {
	p.t.Helper()
	select {
	case <-p.done:
		return p.code
	case <-time.After(within):
		p.t.Fatalf("the service did not end within %v", within)
		return -1
	}
}

// signal sends the signal to the service.
func (p *process) signal(sig syscall.Signal) {
	p.t.Helper()
	if err := p.cmd.Process.Signal(sig); err != nil {
		p.t.Fatal(err)
	}
}

// oneStartFailure fails the test unless the run ended with the exit code and with
// exactly one stderr line, a JSON record at level ERROR with the message
// "start failed" whose error contains every needle, and nothing on stdout.
func oneStartFailure(t *testing.T, r result, code int, needles ...string) string {
	t.Helper()
	if r.Code != code {
		t.Errorf("exit code %d, want %d; stderr:\n%s", r.Code, code, r.Stderr)
	}
	lines := strings.Split(strings.TrimRight(r.Stderr, "\n"), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stderr holds %d lines, want exactly one:\n%s", len(lines), r.Stderr)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("the line is not a JSON record: %q", lines[0])
	}
	if rec["level"] != "ERROR" || rec["msg"] != "start failed" {
		t.Errorf("record = %v, want level ERROR and msg 'start failed'", rec)
	}
	message, _ := rec["error"].(string)
	for _, needle := range needles {
		if !strings.Contains(message, needle) {
			t.Errorf("the error attribute %q does not contain %q", message, needle)
		}
	}
	if r.Stdout != "" {
		t.Errorf("stdout = %q, want nothing", r.Stdout)
	}
	return message
}

// TestABadConfigurationKeyOrDatabaseIsOneStartFailureAndExit2_TS_11_4 verifies TS-11-4:
// Invalid configuration, key or database writes exactly one start failed ERROR
// record and exits 2.
// Verifies: 11-REQ-1.4
func TestABadConfigurationKeyOrDatabaseIsOneStartFailureAndExit2_TS_11_4(t *testing.T) {
	env := bareEnv(t)
	for name, tc := range map[string]struct {
		change func(*setup)
		needle func(*setup) string
	}{
		"an unknown key":                 {func(s *setup) { s.Doc["server"]["external_url"] = q("http://x.example.com") }, func(*setup) string { return "server.external_url" }},
		"a missing key file":             {func(s *setup) { s.Doc["secrets"]["key_file"] = q(filepath.Join(s.Dir, "nowhere", "key")) }, func(s *setup) string { return filepath.Join(s.Dir, "nowhere", "key") }},
		"a database that is a directory": {func(s *setup) { s.Doc["database"]["path"] = q(s.Dir) }, func(s *setup) string { return s.Dir }},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, "http://127.0.0.1:1")
			tc.change(s)
			oneStartFailure(t, s.run(t, env), 2, tc.needle(s))
		})
	}
}

// TestAConfigurationFileThatCannotBeUsedExits2_TS_11_10 verifies the binary's half
// of TS-11-10: no file on the lookup path, a --config that does not exist and a
// file that is not TOML each exit 2 and name what is wrong.
// Verifies: 11-REQ-2.2
func TestAConfigurationFileThatCannotBeUsedExits2_TS_11_10(t *testing.T) {
	env, empty := bareEnv(t), t.TempDir()
	r := execute(t, agentFox(t), empty, env)
	oneStartFailure(t, r, 2, "config.toml", "--config")

	missing := filepath.Join(empty, "nowhere.toml")
	oneStartFailure(t, execute(t, agentFox(t), empty, env, "--config", missing), 2, missing)

	bad := filepath.Join(empty, "bad.toml")
	if err := os.WriteFile(bad, []byte("[hub]\nurl = \"http://example.com\"\nsecrets = = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oneStartFailure(t, execute(t, agentFox(t), empty, env, "--config", bad), 2, bad, "line 3")
}

// TestAnUnsetVariableExits2NamingItAndTheKey_TS_11_13 verifies TS-11-13:
// An unset ${VAR} fails start naming the variable and the key, exit 2.
// Verifies: 11-REQ-2.5
func TestAnUnsetVariableExits2NamingItAndTheKey_TS_11_13(t *testing.T) {
	s := newSetup(t, "${NO_SUCH_VAR_XYZ}")
	oneStartFailure(t, s.run(t, bareEnv(t)), 2, "NO_SUCH_VAR_XYZ", "hub.url")

	// A variable that is set is expanded.
	s = newSetup(t, "${HUB_FOR_THE_TEST}")
	hub := fakehub.New(t)
	p := s.start(t, append(bareEnv(t), "HUB_FOR_THE_TEST="+hub.URL))
	p.addr()
	p.signal(syscall.SIGTERM)
	if code := p.wait(30 * time.Second); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// TestUnknownKeysAndWrongTypesExit2NamingTheKey_TS_11_14 verifies TS-11-14:
// Unknown keys and wrong-typed values are refused naming the dotted key, exit 2.
// Verifies: 11-REQ-2.6
func TestUnknownKeysAndWrongTypesExit2NamingTheKey_TS_11_14(t *testing.T) {
	env := bareEnv(t)
	for name, tc := range map[string]struct {
		change func(*setup)
		key    string
	}{
		"server.external_url": {func(s *setup) { s.Doc["server"]["external_url"] = q("http://x.example.com") }, "server.external_url"},
		"[workers]":           {func(s *setup) { s.Doc["workers"] = map[string]string{"count": "2"} }, "workers.count"},
		"database.retain":     {func(s *setup) { s.Doc["database"]["retain"] = q("30d") }, "database.retain"},
		"a misspelt key":      {func(s *setup) { s.Doc["hub"]["auth_cach_ttl"] = q("1m") }, "hub.auth_cach_ttl"},
		"a wrong type":        {func(s *setup) { s.Doc["hub"]["url"] = "5" }, "hub.url"},
		"a ttl of 16m":        {func(s *setup) { s.Doc["hub"]["auth_cache_ttl"] = q("16m") }, "hub.auth_cache_ttl"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, "http://127.0.0.1:1")
			tc.change(s)
			oneStartFailure(t, s.run(t, env), 2, tc.key)
			if _, err := os.Stat(s.DB); err == nil {
				t.Error("the database was created although the configuration is refused")
			}
		})
	}
}

// TestAKeyFileThatCannotBeUsedExits2NamingIt_TS_11_18 verifies the binary's half of
// TS-11-18 and TS-11-19: a key file that is missing, is the wrong size or is
// readable by others exits 2, naming the file, and the database is not created.
// Verifies: 11-REQ-3.1, 11-REQ-3.2
func TestAKeyFileThatCannotBeUsedExits2NamingIt_TS_11_18(t *testing.T) {
	env := bareEnv(t)
	for name, tc := range map[string]struct {
		prepare func(t *testing.T, s *setup)
		needle  string
	}{
		"a key of 31 bytes": {func(t *testing.T, s *setup) {
			os.WriteFile(s.Key, []byte(base64.StdEncoding.EncodeToString(make([]byte, 31))), 0o600)
		}, "32"},
		"a key that is not base64":    {func(t *testing.T, s *setup) { os.WriteFile(s.Key, []byte("not base64 !!!"), 0o600) }, "base64"},
		"a key readable by others":    {func(t *testing.T, s *setup) { os.Chmod(s.Key, 0o644) }, "0644"},
		"a key readable by the group": {func(t *testing.T, s *setup) { os.Chmod(s.Key, 0o640) }, "0640"},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, "http://127.0.0.1:1")
			tc.prepare(t, s)
			message := oneStartFailure(t, s.run(t, env), 2, s.Key, tc.needle)
			if strings.Contains(message, "not base64 !!!") {
				t.Errorf("the error holds the contents of the key file: %q", message)
			}
			if _, err := os.Stat(s.DB); err == nil {
				t.Error("the database was created although the key is refused")
			}
		})
	}
	for _, mode := range []os.FileMode{0o400, 0o700} {
		s := newSetup(t, fakehub.New(t).URL)
		os.Chmod(s.Key, mode)
		p := s.start(t, env)
		p.addr()
		p.signal(syscall.SIGTERM)
		if code := p.wait(30 * time.Second); code != 0 {
			t.Errorf("key mode %04o: exit code %d, want an owner-only mode accepted", mode, code)
		}
	}
}

// TestABindFailureExits1NamingTheAddress_TS_11_5 verifies TS-11-5:
// A bind failure exits 1 with exactly one start failed ERROR record naming the
// address.
// Verifies: 11-REQ-1.5
func TestABindFailureExits1NamingTheAddress_TS_11_5(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	for name, address := range map[string]string{
		"an address in use":            held.Addr().String(),
		"a host that does not resolve": "no-such-host.invalid:8090",
		"an address that is not local": "192.0.2.1:8090",
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, "http://127.0.0.1:1")
			s.Doc["server"]["listen"] = q(address)
			message := oneStartFailure(t, s.run(t, bareEnv(t)), 1, address)
			if len(message) <= len(address)+len("server.listen ") {
				t.Errorf("the error attribute %q gives no reason", message)
			}
		})
	}
}

// TestTheListeningRecordHasTheBoundAddress_TS_11_6 verifies TS-11-6:
// The listening record carries the actually bound address when listen port is 0.
// Verifies: 11-REQ-1.6
func TestTheListeningRecordHasTheBoundAddress_TS_11_6(t *testing.T) {
	s := newSetup(t, fakehub.New(t).URL)
	p := s.start(t, bareEnv(t))
	rec := p.waitFor("listening")
	addr, _ := rec["addr"].(string)
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" || port == "" || port == "0" {
		t.Fatalf("addr = %q (%v), want 127.0.0.1 and a port that is not 0", addr, err)
	}
	if rec["level"] != "INFO" {
		t.Errorf("listening record = %v, want level INFO", rec)
	}
	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /healthz = %d, want 200", resp.StatusCode)
	}
	p.signal(syscall.SIGTERM)
	if code := p.wait(30 * time.Second); code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
}

// TestASignalDrainsTheRequestsInFlightAndExits0_TS_11_7 verifies TS-11-7:
// SIGTERM and SIGINT drain in-flight requests, close the database and exit 0.
// Verifies: 11-REQ-1.7
func TestASignalDrainsTheRequestsInFlightAndExits0_TS_11_7(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			hub := fakehub.New(t)
			hub.AddUser("tok", "usr_1", nil)
			hub.Delay("/api/v1/user", 1500*time.Millisecond)
			s := newSetup(t, hub.URL)
			p := s.start(t, bareEnv(t))
			addr := p.addr()

			type answer struct {
				status int
				body   string
				err    error
			}
			got := make(chan answer, 1)
			go func() {
				req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/workspaces", nil)
				req.Header.Set("Authorization", "Bearer tok")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					got <- answer{err: err}
					return
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				got <- answer{resp.StatusCode, string(body), nil}
			}()
			// The request is in flight when hub has been asked.
			for deadline := time.Now().Add(10 * time.Second); hub.Total() == 0; {
				if time.Now().After(deadline) {
					t.Fatal("hub was never asked: the request did not arrive")
				}
				time.Sleep(10 * time.Millisecond)
			}
			started := time.Now()
			p.signal(sig)

			// New connections are refused once the signal is handled, while the
			// request goes on.
			refused := false
			for deadline := time.Now().Add(1200 * time.Millisecond); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
				conn, err := net.DialTimeout("tcp", addr, time.Second)
				if err != nil {
					refused = true
					break
				}
				conn.Close()
			}
			if !refused {
				t.Error("new connections were still accepted while the service shut down")
			}

			a := <-got
			if a.err != nil || a.status != 200 || !strings.Contains(a.body, `"workspaces"`) {
				t.Errorf("the request in flight: %d %q, %v; want its normal 200 response", a.status, a.body, a.err)
			}
			if code := p.wait(30 * time.Second); code != 0 {
				t.Errorf("exit code %d, want 0", code)
			}
			if elapsed := time.Since(started); elapsed > 10*time.Second {
				t.Errorf("the service took %v to stop, want it well before 30s", elapsed)
			}
			if _, err := os.Stat(s.DB + "-wal"); err == nil {
				t.Error("the WAL is still there: the database was not closed")
			}
			if !strings.Contains(strings.Join(p.lines, "\n"), "listening") {
				t.Error("no listening record")
			}
		})
	}
}

// failingListener is a listener whose Accept fails.
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed: injected") }

// syncBuffer is a log destination that the service's goroutines write to.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) records(t *testing.T) []map[string]any {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s.b.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("a log line is not a record: %q", line)
		}
		out = append(out, rec)
	}
	return out
}

// TestAListenerFailureAfterStartIsLoggedAndExits1_TS_11_8 verifies TS-11-8:
// A listener failure after start is logged at ERROR and the run function returns
// exit code 1.
// Verifies: 11-REQ-1.8
func TestAListenerFailureAfterStartIsLoggedAndExits1_TS_11_8(t *testing.T) {
	s := newSetup(t, "http://127.0.0.1:1")
	s.write(t)
	logs := &syncBuffer{}
	failing := func(network, address string) (net.Listener, error) {
		ln, err := net.Listen(network, address)
		if err != nil {
			return nil, err
		}
		return failingListener{ln}, nil
	}
	done := make(chan int, 1)
	go func() { done <- serve(ctx, s.Config, slog.New(slog.NewJSONHandler(logs, nil)), failing) }()
	var code int
	select {
	case code = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return after the listener failed")
	}
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	var listening, failed bool
	for _, rec := range logs.records(t) {
		listening = listening || rec["msg"] == "listening"
		if rec["level"] == "ERROR" {
			failed = true
			if text, _ := rec["error"].(string); !strings.Contains(text, "accept failed") {
				t.Errorf("the ERROR record = %v, want the error of the listener", rec)
			}
		}
	}
	if !listening || !failed {
		t.Errorf("records:\n%v\nwant a listening record and an ERROR record", logs.records(t))
	}
	if _, err := os.Stat(s.DB + "-wal"); err == nil {
		t.Error("the WAL is still there: the database was not closed")
	}
	// The file opens again: nothing holds it.
	key, err := secrets.LoadKey(s.Key)
	if err != nil {
		t.Fatal(err)
	}
	again, err := store.Open(ctx, s.DB, key)
	if err != nil {
		t.Fatalf("the database does not open after the failure: %v", err)
	}
	again.Close()
}

// TestCancellingTheContextDrainsTheRequestsInFlight checks the same stop as a
// signal makes, in process: the request in flight is answered, the service
// returns 0 and the database is closed.
// Verifies: 11-REQ-1.7
func TestCancellingTheContextDrainsTheRequestsInFlight(t *testing.T) {
	hub := fakehub.New(t)
	hub.AddUser("tok", "usr_1", nil)
	hub.Delay("/api/v1/user", 600*time.Millisecond)
	s := newSetup(t, hub.URL)
	s.write(t)
	logs := &syncBuffer{}
	stop, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- serve(stop, s.Config, slog.New(slog.NewJSONHandler(logs, nil)), net.Listen) }()

	var addr string
	for deadline := time.Now().Add(20 * time.Second); addr == ""; time.Sleep(10 * time.Millisecond) {
		for _, rec := range logs.records(t) {
			if rec["msg"] == "listening" {
				addr, _ = rec["addr"].(string)
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no listening record")
		}
	}
	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("GET", "http://"+addr+"/api/v1/workspaces", nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- -1
			return
		}
		resp.Body.Close()
		status <- resp.StatusCode
	}()
	for deadline := time.Now().Add(10 * time.Second); hub.Total() == 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("hub was never asked: the request did not arrive")
		}
	}
	cancel()
	if got := <-status; got != 200 {
		t.Errorf("the request in flight got %d, want 200", got)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code %d, want 0", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not return")
	}
	if _, err := os.Stat(s.DB + "-wal"); err == nil {
		t.Error("the WAL is still there: the database was not closed")
	}
	for _, rec := range logs.records(t) {
		if rec["level"] == "ERROR" {
			t.Errorf("an ERROR record on a clean stop: %v", rec)
		}
	}
}

// TestAPlainHTTPHubOnAnotherHostIsAWarningBeforeListening_TS_11_17 verifies TS-11-17:
// A non-loopback http hub.url logs a WARN after bind and before listening.
// Verifies: 11-REQ-2.8
func TestAPlainHTTPHubOnAnotherHostIsAWarningBeforeListening_TS_11_17(t *testing.T) {
	warnings := func(p *process) (warn, listening int) {
		recs := p.records()
		warn, listening = -1, -1
		for i, rec := range recs {
			if rec["level"] == "WARN" && warn < 0 {
				warn = i
			}
			if rec["msg"] == "listening" && listening < 0 {
				listening = i
			}
		}
		return warn, listening
	}

	s := newSetup(t, "http://hub.example.internal:8080")
	p := s.start(t, bareEnv(t))
	p.waitFor("listening")
	warn, listening := warnings(p)
	if warn < 0 || warn > listening {
		t.Fatalf("records:\n%s\nwant a WARN record before the listening record", strings.Join(p.lines, "\n"))
	}
	text, _ := p.records()[warn]["msg"].(string)
	if !strings.Contains(text, "clear") || !strings.Contains(text, "hub.url") {
		t.Errorf("WARN = %q, want it to say that tokens cross the link in clear", text)
	}
	p.signal(syscall.SIGTERM)
	p.wait(30 * time.Second)

	for _, url := range []string{"http://127.0.0.1:1", "http://localhost:1", "https://hub.example.com"} {
		s := newSetup(t, url)
		p := s.start(t, bareEnv(t))
		p.waitFor("listening")
		if warn, _ := warnings(p); warn >= 0 {
			t.Errorf("hub.url %s: a WARN record:\n%s", url, strings.Join(p.lines, "\n"))
		}
		p.signal(syscall.SIGTERM)
		p.wait(30 * time.Second)
	}

	// A start that fails writes its one ERROR record and no WARN.
	s = newSetup(t, "http://hub.example.internal:8080")
	s.Doc["secrets"]["key_file"] = q(filepath.Join(s.Dir, "nowhere"))
	oneStartFailure(t, s.run(t, bareEnv(t)), 2)
	s = newSetup(t, "http://hub.example.internal:8080")
	s.Doc["server"]["listen"] = q("no-such-host.invalid:8090")
	oneStartFailure(t, s.run(t, bareEnv(t)), 1)
}

// TestADatabaseFromANewerBinaryIsRefusedAndLeftAlone_TS_11_27 verifies TS-11-27:
// A database recording a newer migration than the binary knows fails start naming
// both versions and is unchanged.
// Verifies: 11-REQ-4.4
func TestADatabaseFromANewerBinaryIsRefusedAndLeftAlone_TS_11_27(t *testing.T) {
	s := newSetup(t, "http://127.0.0.1:1")
	key, err := secrets.LoadKey(s.Key)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := store.Open(ctx, s.DB, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seeded.DB().ExecContext(ctx, "INSERT INTO af_schema_migrations (version, applied_at) VALUES (999, 'x')"); err != nil {
		t.Fatal(err)
	}
	if err := seeded.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.DB)
	if err != nil {
		t.Fatal(err)
	}

	oneStartFailure(t, s.run(t, bareEnv(t)), 2, "999", "up to 1", s.DB)
	after, err := os.ReadFile(s.DB)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Error("the database file changed although start was refused")
	}
}

// TestADatabaseThatCannotBeOpenedExits2NamingIt_TS_11_28 verifies TS-11-28:
// A database that cannot be created or opened fails start with database.path in
// the error, exit 2.
// Verifies: 11-REQ-4.5
func TestADatabaseThatCannotBeOpenedExits2NamingIt_TS_11_28(t *testing.T) {
	for name, prepare := range map[string]func(t *testing.T, s *setup) string{
		"a parent that is a regular file": func(t *testing.T, s *setup) string {
			file := filepath.Join(s.Dir, "plain-file")
			if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(file, "agent-fox.db")
		},
		"a file of garbage": func(t *testing.T, s *setup) string {
			path := filepath.Join(s.Dir, "garbage.db")
			if err := os.WriteFile(path, bytes.Repeat([]byte("this is not a database "), 300), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := newSetup(t, "http://127.0.0.1:1")
			path := prepare(t, s)
			s.Doc["database"]["path"] = q(path)
			oneStartFailure(t, s.run(t, bareEnv(t)), 2, path)
		})
	}
}
