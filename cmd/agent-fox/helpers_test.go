package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// service is the files of a service under test: a key file, the place of the database and the configuration that
// write makes of the fields, with extra appended to it.
type service struct {
	dir, config, key, db string
	hubURL, listen       string
	extra                string
}

func newService(t *testing.T, hubURL string) *service {
	t.Helper()
	dir := t.TempDir()
	key := testutil.WriteFile(t, dir, "key", base64.StdEncoding.EncodeToString(testutil.Key())+"\n")
	if err := os.Chmod(key, 0o600); err != nil {
		t.Fatal(err)
	}
	return &service{dir: dir, config: filepath.Join(dir, "config.toml"), key: key,
		db: filepath.Join(dir, "state", "agent-fox.db"), hubURL: hubURL, listen: "127.0.0.1:0"}
}

func (s *service) write(t *testing.T) {
	t.Helper()
	testutil.WriteFile(t, s.dir, "config.toml", fmt.Sprintf("[server]\nlisten = %q\n[hub]\nurl = %q\n"+
		"[secrets]\nkey_file = %q\n[database]\npath = %q\n%s", s.listen, s.hubURL, s.key, s.db, s.extra))
}

// subscribe stores the subscription of the workspace in the database, as the API does, before the service starts.
func (s *service) subscribe(t *testing.T, slug, token string) {
	t.Helper()
	key, err := secrets.LoadKey(s.key)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), s.db, key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, _, err := st.Upsert(context.Background(), slug, token, "usr_1", "usr_1", time.Now()); err != nil {
		t.Fatal(err)
	}
}

// running is a service that serve runs in the test's process.
type running struct {
	addr string
	log  *testutil.Log
	stop context.CancelFunc
	done chan struct{}
	code int
}

// start writes the configuration and runs serve with listen until it says that it listens.
func (s *service) start(t *testing.T, listen listenFunc) *running {
	t.Helper()
	s.write(t)
	ctx, cancel := context.WithCancel(context.Background())
	r := &running{log: &testutil.Log{}, stop: cancel, done: make(chan struct{})}
	go func() { defer close(r.done); r.code = serve(ctx, s.config, r.log.Logger(), listen) }()
	t.Cleanup(func() { cancel(); <-r.done })
	testutil.Eventually(t, "the listening record", func() bool { return len(r.log.Find("INFO", "listening")) > 0 })
	r.addr, _ = r.log.Find("INFO", "listening")[0]["addr"].(string)
	return r
}

// exit waits for serve to return, and is its exit code.
func (r *running) exit(t *testing.T) int {
	t.Helper()
	select {
	case <-r.done:
		return r.code
	case <-time.After(testutil.WaitTimeout):
		t.Fatal("serve did not return")
		return -1
	}
}

// at is where the first record with the message msg is in the log, or -1.
func (r *running) at(msg string) int {
	for i, rec := range r.log.Records() {
		if m, _ := rec["msg"].(string); m == msg {
			return i
		}
	}
	return -1
}

// failingListener is a listener whose Accept fails.
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, fmt.Errorf("accept failed: injected") }

func failingListen(network, address string) (net.Listener, error) {
	ln, err := net.Listen(network, address)
	return failingListener{ln}, err
}
