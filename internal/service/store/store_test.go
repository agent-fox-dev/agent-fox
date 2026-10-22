package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
)

var ctx = context.Background()

// keyOf makes a key that is the same on every run.
func keyOf(t *testing.T, seed byte) secrets.Key {
	t.Helper()
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	k, err := secrets.NewKey(b)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newStore opens a store on a new file.
func newStore(t *testing.T) (*Store, string, secrets.Key) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state", "agent-fox.db")
	key := keyOf(t, 1)
	s, err := Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path, key
}

// at is a moment of the tests' clock.
func at(minutes int) time.Time {
	return time.Date(2026, 10, 22, 8, minutes, 0, 0, time.UTC)
}

// column runs a query that answers one column and returns its values.
func column(t *testing.T, db interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v sql.NullString
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// mode is the permission bits of a file or directory.
func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// TestOpenCreatesTheDirectoryAndTheFileAndSetsThePragmas_TS_11_24 verifies TS-11-24:
// Opening a new database creates a 0700 directory, a 0600 file and sets the pragmas.
// Verifies: 11-REQ-4.1
func TestOpenCreatesTheDirectoryAndTheFileAndSetsThePragmas_TS_11_24(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state", "nested", "agent-fox.db")
	s, err := Open(ctx, path, keyOf(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	for _, dir := range []string{filepath.Join(root, "state"), filepath.Dir(path)} {
		if m := mode(t, dir); m != 0o700 {
			t.Errorf("directory %s has mode %04o, want 0700", dir, m)
		}
	}
	if m := mode(t, path); m != 0o600 {
		t.Errorf("the file has mode %04o, want 0600", m)
	}

	// Two connections held at once: the second is a fresh one of the pool, and the
	// pragmas must hold on it too.
	c1, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := s.DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	for _, c := range []*sql.Conn{c1, c2} {
		for pragma, want := range map[string]string{"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1"} {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil || got != want {
				t.Errorf("PRAGMA %s = %q, %v; want %q", pragma, got, err, want)
			}
		}
	}

	// The files that the WAL mode adds are as private as the file.
	if _, _, err := s.Upsert(ctx, "widgets", "af:", "tok", "", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	for _, extra := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(extra); err == nil {
			if m := mode(t, extra); m != 0o600 {
				t.Errorf("%s has mode %04o, want 0600", extra, m)
			}
		}
	}

	t.Run("a directory that exists keeps its mode", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "shared")
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other, err := Open(ctx, filepath.Join(dir, "af.db"), keyOf(t, 1))
		if err != nil {
			t.Fatal(err)
		}
		defer other.Close()
		if m := mode(t, dir); m != 0o755 {
			t.Errorf("an existing directory now has mode %04o, want it left at 0755", m)
		}
	})
}

// TestTheStoreOwnsOnlyAfTablesAndLeavesTheRestAlone_TS_11_25 verifies TS-11-25:
// Every owned table is af_-prefixed, user_version is untouched and a foreign
// table is never changed.
// Verifies: 11-REQ-4.2
func TestTheStoreOwnsOnlyAfTablesAndLeavesTheRestAlone_TS_11_25(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"CREATE TABLE jobs (id INTEGER PRIMARY KEY, payload TEXT)",
		"INSERT INTO jobs(payload) VALUES ('one'), ('two')",
		"PRAGMA user_version = 42",
	} {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path, keyOf(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Upsert(ctx, "widgets", "af:", "tok", "usr_9", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "widgets"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.Delete(ctx, "widgets"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}

	tables := column(t, s.DB(), "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
	for _, name := range tables {
		if name != "jobs" && !strings.HasPrefix(name, "af_") {
			t.Errorf("table %q is neither the foreign table nor af_-prefixed", name)
		}
	}
	if !slices.Contains(tables, "af_subscriptions") || !slices.Contains(tables, "af_schema_migrations") {
		t.Errorf("tables = %v, want af_subscriptions and af_schema_migrations among them", tables)
	}
	if got := column(t, s.DB(), "PRAGMA user_version"); !slices.Equal(got, []string{"42"}) {
		t.Errorf("user_version = %v, want 42, untouched", got)
	}
	if got := column(t, s.DB(), "SELECT payload FROM jobs ORDER BY id"); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("the foreign table holds %v, want it unchanged", got)
	}
}

// TestMigrationsApplyOnceInOrderAndRollBack_TS_11_26 verifies TS-11-26:
// Migrations apply once in order and reopening applies nothing.
// Verifies: 11-REQ-4.3
func TestMigrationsApplyOnceInOrderAndRollBack_TS_11_26(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	for range 2 {
		s, err := Open(ctx, path, keyOf(t, 1))
		if err != nil {
			t.Fatal(err)
		}
		got := column(t, s.DB(), "SELECT version FROM af_schema_migrations ORDER BY version")
		if want := []string{"1"}; !slices.Equal(got, want) {
			t.Errorf("the migration record lists %v, want each migration exactly once: %v", got, want)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("a migration that fails midway leaves no partial change", func(t *testing.T) {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "m.db")+"?_pragma=foreign_keys(1)")
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		migrations := []string{
			"CREATE TABLE af_first (x INTEGER)",
			"CREATE TABLE af_partial (x INTEGER); INSERT INTO af_does_not_exist VALUES (1)",
			"CREATE TABLE af_third (x INTEGER)",
		}
		if err := migrate(ctx, db, migrations, at(0)); err == nil {
			t.Fatal("migrate went on after a failing migration")
		}
		if got := column(t, db, "SELECT version FROM af_schema_migrations ORDER BY version"); !slices.Equal(got, []string{"1"}) {
			t.Errorf("the record lists %v, want only the migration that committed", got)
		}
		tables := column(t, db, "SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name")
		if slices.Contains(tables, "af_partial") || slices.Contains(tables, "af_third") || !slices.Contains(tables, "af_first") {
			t.Errorf("tables = %v, want af_first and neither af_partial (rolled back) nor af_third (never reached)", tables)
		}

		// The next start applies what is left, once the migration is mended.
		migrations[1] = "CREATE TABLE af_second (x INTEGER)"
		if err := migrate(ctx, db, migrations, at(1)); err != nil {
			t.Fatal(err)
		}
		if got := column(t, db, "SELECT version FROM af_schema_migrations ORDER BY version"); !slices.Equal(got, []string{"1", "2", "3"}) {
			t.Errorf("the record lists %v, want 1, 2 and 3", got)
		}
	})
}

// TestADatabaseNewerThanTheBinaryIsRefusedAndUnchanged_TS_11_27 verifies the store's
// half of TS-11-27: a database that records a newer migration than the binary
// knows fails naming both versions and is left as it was. The exit code of the
// binary is checked with the start.
// Verifies: 11-REQ-4.4
func TestADatabaseNewerThanTheBinaryIsRefusedAndUnchanged_TS_11_27(t *testing.T) {
	s, path, key := newStore(t)
	if _, err := s.DB().ExecContext(ctx, "INSERT INTO af_schema_migrations(version, applied_at) VALUES (999, 'x')"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	_, err = Open(ctx, path, key)
	if err == nil {
		t.Fatal("Open accepted a database from the future")
	}
	for _, want := range []string{"999", fmt.Sprint(len(migrations)), path} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to contain %s", err, want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Error("the file changed although the open was refused")
	}
}

// TestOpenFailuresNameTheFile_TS_11_28 verifies the store's half of TS-11-28:
// a database that cannot be created, opened or migrated fails with the path in
// the error. The exit code of the binary is checked with the start.
// Verifies: 11-REQ-4.5
func TestOpenFailuresNameTheFile_TS_11_28(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "plain-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	garbage := filepath.Join(root, "garbage.db")
	if err := os.WriteFile(garbage, bytes.Repeat([]byte("not a database "), 200), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"a parent that is a regular file": filepath.Join(file, "agent-fox.db"),
		"a file of garbage":               garbage,
		"a directory":                     root,
	} {
		t.Run(name, func(t *testing.T) {
			s, err := Open(ctx, path, keyOf(t, 1))
			if err == nil {
				s.Close()
				t.Fatal("Open succeeded")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name %s", err, path)
			}
		})
	}
}

// TestMigrationOneCreatesTheSubscriptionsTable_TS_11_29 verifies TS-11-29:
// Migration 1 creates a subscriptions table with unique slug and RFC 3339 UTC times.
// Verifies: 11-REQ-4.6
func TestMigrationOneCreatesTheSubscriptionsTable_TS_11_29(t *testing.T) {
	s, _, _ := newStore(t)

	rows, err := s.DB().QueryContext(ctx, "SELECT name, \"notnull\" FROM pragma_table_info('af_subscriptions')")
	if err != nil {
		t.Fatal(err)
	}
	notNull := map[string]bool{}
	for rows.Next() {
		var name string
		var nn bool
		if err := rows.Scan(&name, &nn); err != nil {
			t.Fatal(err)
		}
		notNull[name] = nn
	}
	rows.Close()
	for _, col := range []string{"slug", "label_prefix", "sealed_token", "token_user", "subscribed_by", "created_at", "updated_at"} {
		if _, ok := notNull[col]; !ok {
			t.Errorf("af_subscriptions has no column %s (it has %v)", col, notNull)
		}
	}
	if notNull["token_user"] {
		t.Error("token_user is NOT NULL, but a token's user can be unknown")
	}

	const insert = "INSERT INTO af_subscriptions(slug, label_prefix, sealed_token, subscribed_by, created_at, updated_at) VALUES ('widgets', 'af:', x'01', 'usr_1', '2026-10-22T08:00:00Z', '2026-10-22T08:00:00Z')"
	if _, err := s.DB().ExecContext(ctx, insert); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, insert); err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Errorf("a second row for the same slug: %v, want a uniqueness violation", err)
	}

	if _, _, err := s.Upsert(ctx, "gadgets", "af:", "tok", "usr_9", "usr_1", at(5).In(time.FixedZone("CEST", 2*3600))); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"created_at", "updated_at"} {
		stored := column(t, s.DB(), "SELECT "+col+" FROM af_subscriptions WHERE slug = 'gadgets'")
		if len(stored) != 1 {
			t.Fatalf("%s: %v", col, stored)
		}
		parsed, err := time.Parse(time.RFC3339, stored[0])
		if err != nil || !strings.HasSuffix(stored[0], "Z") || parsed.Location() != time.UTC || !parsed.Equal(at(5)) {
			t.Errorf("%s = %q (%v, %v), want RFC 3339 in UTC, ending in Z, at %v", col, stored[0], parsed, err, at(5))
		}
	}
}

// TestUpsertCreatesThenReplacesTheRowInFull checks the statement that stores a
// subscription: created says which it did, the row is replaced in full and
// created_at is kept.
// Verifies: 11-REQ-8.1, 11-REQ-8.2
func TestUpsertCreatesThenReplacesTheRowInFull(t *testing.T) {
	s, _, _ := newStore(t)

	created, isNew, err := s.Upsert(ctx, "widgets", "x:", "pat-1", "usr_9", "usr_1", at(0))
	if err != nil || !isNew {
		t.Fatalf("first Upsert = %v, new %v", err, isNew)
	}
	want := Subscription{Slug: "widgets", LabelPrefix: "x:", TokenUser: "usr_9", SubscribedBy: "usr_1", TokenState: TokenOK, CreatedAt: at(0), UpdatedAt: at(0)}
	if created != want {
		t.Errorf("created = %+v, want %+v", created, want)
	}

	replaced, isNew, err := s.Upsert(ctx, "widgets", "af:", "pat-2", "", "usr_2", at(7))
	if err != nil || isNew {
		t.Fatalf("second Upsert = %v, new %v; want a replacement", err, isNew)
	}
	want = Subscription{Slug: "widgets", LabelPrefix: "af:", TokenUser: "", SubscribedBy: "usr_2", TokenState: TokenOK, CreatedAt: at(0), UpdatedAt: at(7)}
	if replaced != want {
		t.Errorf("replaced = %+v, want %+v (created_at kept, token_user emptied)", replaced, want)
	}
	if got := column(t, s.DB(), "SELECT count(*) FROM af_subscriptions"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("rows = %v, want one", got)
	}
	if got := column(t, s.DB(), "SELECT token_user IS NULL FROM af_subscriptions"); !slices.Equal(got, []string{"1"}) {
		t.Errorf("token_user IS NULL = %v, want an unknown user stored as NULL", got)
	}

	// Two replacements in the same instant still say they replaced.
	if _, isNew, err := s.Upsert(ctx, "widgets", "af:", "pat-3", "", "usr_2", at(7)); err != nil || isNew {
		t.Errorf("third Upsert = %v, new %v; want a replacement", err, isNew)
	}

	got, err := s.Get(ctx, "widgets")
	if err != nil || got.UpdatedAt != at(7) || got.CreatedAt != at(0) {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, "nothing"); !errors.Is(err, ErrNotSubscribed) {
		t.Errorf("Get of a slug that is not subscribed: %v, want ErrNotSubscribed", err)
	}
}

// TestTheTokenIsSealedAtRest checks that the column holds a sealed value and the
// bytes of the token are in no file of the database.
// Verifies: 11-REQ-8.8
func TestTheTokenIsSealedAtRest(t *testing.T) {
	s, path, _ := newStore(t)
	const token = "pat-distinctive-0123456789"
	if _, _, err := s.Upsert(ctx, "widgets", "af:", token, "usr_9", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	var sealed []byte
	if err := s.DB().QueryRowContext(ctx, "SELECT sealed_token FROM af_subscriptions WHERE slug = 'widgets'").Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	if len(sealed) != 1+12+len(token)+16 || sealed[0] != 0x01 {
		t.Errorf("sealed token: %d bytes, first %#x; want a version 1 value of %d bytes", len(sealed), sealed[0], 1+12+len(token)+16)
	}
	// Before any checkpoint, the file and its WAL; then after the close.
	for _, when := range []string{"before the close", "after the close"} {
		if when == "after the close" {
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, file := range []string{path, path + "-wal"} {
			if data, err := os.ReadFile(file); err == nil && bytes.Contains(data, []byte(token)) {
				t.Errorf("%s holds the token %s", file, when)
			}
		}
	}
}

// TestListIsSortedBySlugAndDeleteSaysWhetherThereWasARow checks the reads and the
// removal.
// Verifies: 11-REQ-9.2, 11-REQ-9.5
func TestListIsSortedBySlugAndDeleteSaysWhetherThereWasARow(t *testing.T) {
	s, _, _ := newStore(t)
	if got, err := s.List(ctx); err != nil || len(got) != 0 {
		t.Errorf("List of an empty store = %v, %v", got, err)
	}
	for _, slug := range []string{"zeta", "widgets", "alpha", "Beta"} {
		if _, _, err := s.Upsert(ctx, slug, "af:", "tok-"+slug, "", "usr_1", at(0)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, sub := range list {
		slugs = append(slugs, sub.Slug)
	}
	if want := []string{"Beta", "alpha", "widgets", "zeta"}; !slices.Equal(slugs, want) {
		t.Errorf("List = %v, want %v, sorted by slug", slugs, want)
	}

	if ok, err := s.Delete(ctx, "widgets"); err != nil || !ok {
		t.Errorf("Delete = %v, %v; want true", ok, err)
	}
	if ok, err := s.Delete(ctx, "widgets"); err != nil || ok {
		t.Errorf("Delete again = %v, %v; want false", ok, err)
	}
	if _, err := s.Token(ctx, "widgets"); !errors.Is(err, ErrNotSubscribed) {
		t.Errorf("Token after Delete: %v, want ErrNotSubscribed", err)
	}
	if got := column(t, s.DB(), "SELECT count(*) FROM af_subscriptions"); !slices.Equal(got, []string{"3"}) {
		t.Errorf("rows = %v, want three", got)
	}
}

// TestTheInProcessReadReturnsTheOpenedToken checks the read that later specs use.
// Verifies: 11-REQ-9.7
func TestTheInProcessReadReturnsTheOpenedToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	key := keyOf(t, 1)
	s, err := Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Upsert(ctx, "widgets", "af:", "pat-1", "", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Token(ctx, "widgets"); err != nil || got != "pat-1" {
		t.Errorf("Token = %q, %v; want pat-1", got, err)
	}
	if _, _, err := s.Upsert(ctx, "widgets", "af:", "pat-2", "", "usr_1", at(1)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Token(ctx, "widgets"); err != nil || got != "pat-2" {
		t.Errorf("Token after a replace = %q, %v; want pat-2", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got, err := s.Token(ctx, "widgets"); err != nil || got != "pat-2" {
		t.Errorf("Token after a reopen = %q, %v; want pat-2", got, err)
	}
}

// TestTheInProcessReadFailsDistinguishablyAndWithoutThePlaintext_TS_11_70 verifies
// TS-11-70: The in-process PAT read errors distinguishably for an unsubscribed
// slug and an unreadable token, without the plaintext.
// Verifies: 11-REQ-9.8
func TestTheInProcessReadFailsDistinguishablyAndWithoutThePlaintext_TS_11_70(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent-fox.db")
	const pat = "pat-the-plaintext-0123"
	s, err := Open(ctx, path, keyOf(t, 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Upsert(ctx, "widgets", "af:", pat, "usr_9", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	other, err := Open(ctx, path, keyOf(t, 2))
	if err != nil {
		t.Fatalf("a store opened with another key must still open: %v", err)
	}
	defer other.Close()

	_, err = other.Token(ctx, "missing")
	if !errors.Is(err, ErrNotSubscribed) || errors.Is(err, ErrUnreadable) {
		t.Errorf("Token of a slug that is not subscribed: %v, want ErrNotSubscribed and not ErrUnreadable", err)
	}
	got, err := other.Token(ctx, "widgets")
	if !errors.Is(err, ErrUnreadable) || errors.Is(err, ErrNotSubscribed) || got != "" {
		t.Errorf("Token under another key = %q, %v; want ErrUnreadable, not ErrNotSubscribed, and no plaintext", got, err)
	}
	if err != nil && strings.Contains(err.Error(), pat) {
		t.Errorf("the error contains the plaintext: %q", err)
	}

	sub, err := other.Get(ctx, "widgets")
	if err != nil || sub.TokenState != TokenUnreadable {
		t.Errorf("Get under another key = %+v, %v; want token_state %s", sub, err, TokenUnreadable)
	}
	list, err := other.List(ctx)
	if err != nil || len(list) != 1 || list[0].TokenState != TokenUnreadable {
		t.Errorf("List under another key = %+v, %v; want token_state %s", list, err, TokenUnreadable)
	}

	// An unreadable subscription is replaced, and then reads.
	replaced, isNew, err := other.Upsert(ctx, "widgets", "af:", "pat-new", "", "usr_2", at(3))
	if err != nil || isNew || replaced.TokenState != TokenOK {
		t.Errorf("Upsert over an unreadable token = %+v, new %v, %v; want a replacement that reads", replaced, isNew, err)
	}
	if got, err := other.Token(ctx, "widgets"); err != nil || got != "pat-new" {
		t.Errorf("Token after the replace = %q, %v", got, err)
	}
}

// TestPingAnswersUntilTheStoreIsClosed checks what /readyz asks.
// Verifies: 11-REQ-7.2
func TestPingAnswersUntilTheStoreIsClosed(t *testing.T) {
	s, _, _ := newStore(t)
	if err := s.Ping(ctx); err != nil {
		t.Errorf("Ping of an open store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping of a closed store succeeded")
	}
}

// TestConcurrentWritesWaitForTheLockInsteadOfFailing checks that the busy timeout
// is in effect: writers from many goroutines all succeed.
// Verifies: 11-REQ-4.1
func TestConcurrentWritesWaitForTheLockInsteadOfFailing(t *testing.T) {
	s, _, _ := newStore(t)
	var wg sync.WaitGroup
	errs := make(chan error, 80)
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				slug := fmt.Sprintf("w%d-%d", g, i)
				if _, _, err := s.Upsert(ctx, slug, "af:", "tok", "", "usr_1", at(0)); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a concurrent Upsert failed: %v", err)
	}
	if list, err := s.List(ctx); err != nil || len(list) != 80 {
		t.Errorf("List = %d rows, %v; want 80", len(list), err)
	}
}
