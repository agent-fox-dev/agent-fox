package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

var ctx = context.Background()

// keyOf is the key of testutil, or another key when other is set.
func keyOf(t *testing.T, other bool) secrets.Key {
	t.Helper()
	raw := testutil.Key()
	if other {
		raw[0] ^= 0xff
	}
	k, err := secrets.NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// open opens the store at path with the key, and closes it when the test ends.
func open(t *testing.T, path string, key secrets.Key) *Store {
	t.Helper()
	s, err := Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// newStore opens a store on a new file under a directory that does not exist yet.
func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "agent-fox.db")
	return open(t, path, keyOf(t, false)), path
}

// at is a moment of the tests' clock.
func at(minutes int) time.Time { return time.Date(2026, 10, 22, 8, minutes, 0, 0, time.UTC) }

// mode is the permission bits of a file or directory.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
