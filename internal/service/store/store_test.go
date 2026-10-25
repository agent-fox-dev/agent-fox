package store

import (
	"bytes"
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

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestOpen_MakesAPrivateDirectoryAndFileWithThePragmasOnEveryConnection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "state", "nested", "agent-fox.db")
	s := open(t, path, keyOf(t, false))
	for _, dir := range []string{filepath.Join(root, "state"), filepath.Dir(path)} {
		if m := mode(t, dir); m != 0o700 {
			t.Errorf("directory %s has mode %04o, want 0700", dir, m)
		}
	}
	if _, _, err := s.Upsert(ctx, "widgets", "tok", "", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if _, err := os.Stat(f); err == nil && mode(t, f) != 0o600 {
			t.Errorf("%s has mode %04o, want 0600", f, mode(t, f))
		}
	}
	// Two connections held at once: the second is a fresh one of the pool.
	var conns []*sql.Conn
	for range 2 {
		c, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		conns = append(conns, c)
	}
	for _, c := range conns {
		for pragma, want := range map[string]string{"journal_mode": "wal", "busy_timeout": "5000", "foreign_keys": "1"} {
			if got := testutil.Column(t, c, "PRAGMA "+pragma); !slices.Equal(got, []string{want}) {
				t.Errorf("PRAGMA %s = %v, want %s", pragma, got, want)
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
		open(t, filepath.Join(dir, "af.db"), keyOf(t, false))
		if m := mode(t, dir); m != 0o755 {
			t.Errorf("an existing directory now has mode %04o", m)
		}
	})
}

func TestUpsert_CreatesThenReplacesTheRowInFullKeepingCreatedAt(t *testing.T) {
	s, _ := newStore(t)
	created, isNew, err := s.Upsert(ctx, "widgets", "pat-1", "usr_9", "usr_1", at(0).In(time.FixedZone("CEST", 2*3600)))
	want := Subscription{Slug: "widgets", TokenUser: "usr_9", SubscribedBy: "usr_1", TokenState: TokenOK, CreatedAt: at(0), UpdatedAt: at(0)}
	if err != nil || !isNew || created != want {
		t.Fatalf("first Upsert = %+v, new %v, %v; want %+v", created, isNew, err, want)
	}
	if got := testutil.Column(t, s.DB(), "SELECT created_at FROM af_subscriptions"); !slices.Equal(got, []string{"2026-10-22T08:00:00Z"}) {
		t.Errorf("created_at is stored as %v, want RFC 3339 in UTC", got)
	}
	for range 2 { // a replacement in the same instant is a replacement too
		replaced, isNew, err := s.Upsert(ctx, "widgets", "pat-2", "", "usr_2", at(7))
		want := Subscription{Slug: "widgets", SubscribedBy: "usr_2", TokenState: TokenOK, CreatedAt: at(0), UpdatedAt: at(7)}
		if err != nil || isNew || replaced != want {
			t.Errorf("Upsert again = %+v, new %v, %v; want %+v", replaced, isNew, err, want)
		}
	}
	if n := testutil.Count(t, s.DB(), "af_subscriptions", "token_user IS NULL"); n != 1 {
		t.Errorf("%d rows with a NULL token_user, want the one row, its unknown user stored as NULL", n)
	}
	if got, err := s.Get(ctx, "widgets"); err != nil || got.UpdatedAt != at(7) || got.CreatedAt != at(0) {
		t.Errorf("Get = %+v, %v", got, err)
	}
	if _, err := s.Get(ctx, "nothing"); !errors.Is(err, ErrNotSubscribed) {
		t.Errorf("Get of a slug that is not subscribed: %v, want ErrNotSubscribed", err)
	}
	if _, _, err := (&Store{db: s.db}).Upsert(ctx, "widgets", "pat", "", "usr_1", at(0)); err == nil {
		t.Error("a store without a key stored a token")
	}
}

func TestToken_IsTheOpenedTokenAcrossAReopen(t *testing.T) {
	s, path := newStore(t)
	for i, pat := range []string{"pat-1", "pat-2"} {
		if _, _, err := s.Upsert(ctx, "widgets", pat, "", "usr_1", at(i)); err != nil {
			t.Fatal(err)
		}
		if got, err := s.Token(ctx, "widgets"); err != nil || got != pat {
			t.Errorf("Token = %q, %v; want %s", got, err, pat)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := open(t, path, keyOf(t, false)).Token(ctx, "widgets"); err != nil || got != "pat-2" {
		t.Errorf("Token after a reopen = %q, %v; want pat-2", got, err)
	}
}

func TestToken_IsSealedAtRest(t *testing.T) {
	s, path := newStore(t)
	const token = "pat-distinctive-0123456789"
	if _, _, err := s.Upsert(ctx, "widgets", token, "usr_9", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	check := func(when string) {
		for _, file := range []string{path, path + "-wal"} {
			if data, err := os.ReadFile(file); err == nil && bytes.Contains(data, []byte(token)) {
				t.Errorf("%s holds the token %s", file, when)
			}
		}
	}
	check("before the close")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	check("after the close")
}

func TestATokenOfAnotherKeyIsUnreadableAndCanBeReplaced(t *testing.T) {
	s, path := newStore(t)
	const pat = "pat-the-plaintext-0123"
	if _, _, err := s.Upsert(ctx, "widgets", pat, "usr_9", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	other := open(t, path, keyOf(t, true))

	if _, err := other.Token(ctx, "missing"); !errors.Is(err, ErrNotSubscribed) || errors.Is(err, ErrUnreadable) {
		t.Errorf("Token of a slug that is not subscribed: %v, want ErrNotSubscribed alone", err)
	}
	got, err := other.Token(ctx, "widgets")
	if !errors.Is(err, ErrUnreadable) || errors.Is(err, ErrNotSubscribed) || got != "" || strings.Contains(fmt.Sprint(err), pat) {
		t.Errorf("Token under another key = %q, %v; want ErrUnreadable alone and no plaintext", got, err)
	}
	if sub, err := other.Get(ctx, "widgets"); err != nil || sub.TokenState != TokenUnreadable {
		t.Errorf("Get under another key = %+v, %v; want the token unreadable", sub, err)
	}
	if list, err := other.List(ctx); err != nil || len(list) != 1 || list[0].TokenState != TokenUnreadable {
		t.Errorf("List under another key = %+v, %v", list, err)
	}
	if replaced, isNew, err := other.Upsert(ctx, "widgets", "pat-new", "", "usr_2", at(3)); err != nil || isNew || replaced.TokenState != TokenOK {
		t.Errorf("Upsert over an unreadable token = %+v, new %v, %v", replaced, isNew, err)
	}
	if got, err := other.Token(ctx, "widgets"); err != nil || got != "pat-new" {
		t.Errorf("Token after the replace = %q, %v", got, err)
	}
}

func TestList_IsSortedBySlugAndDeleteSaysWhetherThereWasARow(t *testing.T) {
	s, _ := newStore(t)
	if got, err := s.List(ctx); err != nil || got == nil || len(got) != 0 {
		t.Errorf("List of an empty store = %#v, %v; want an empty list", got, err)
	}
	for _, slug := range []string{"zeta", "widgets", "alpha", "Beta"} {
		if _, _, err := s.Upsert(ctx, slug, "tok-"+slug, "", "usr_1", at(0)); err != nil {
			t.Fatal(err)
		}
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
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var slugs []string
	for _, sub := range list {
		slugs = append(slugs, sub.Slug)
	}
	if want := []string{"Beta", "alpha", "zeta"}; !slices.Equal(slugs, want) {
		t.Errorf("List = %v, want %v", slugs, want)
	}
}

func TestReads_RefuseATimeThatIsNotRFC3339(t *testing.T) {
	s, _ := newStore(t)
	if _, _, err := s.Upsert(ctx, "widgets", "tok", "", "usr_1", at(0)); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"created_at", "updated_at"} {
		testutil.Exec(t, s.DB(), "UPDATE af_subscriptions SET created_at = '2026-10-22T08:00:00Z', updated_at = '2026-10-22T08:00:00Z'")
		testutil.Exec(t, s.DB(), "UPDATE af_subscriptions SET "+col+" = 'yesterday'")
		if _, err := s.Get(ctx, "widgets"); err == nil || !strings.Contains(err.Error(), col) {
			t.Errorf("Get with a bad %s: %v, want an error naming it", col, err)
		}
		if _, err := s.List(ctx); err == nil {
			t.Errorf("List with a bad %s succeeded", col)
		}
	}
}

func TestPing_AnswersUntilTheStoreIsClosed(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Ping(ctx); err != nil {
		t.Errorf("Ping of an open store: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Ping(ctx); err == nil {
		t.Error("Ping of a closed store succeeded")
	}
	if _, err := s.List(ctx); err == nil {
		t.Error("List of a closed store succeeded")
	}
	if _, err := s.Delete(ctx, "widgets"); err == nil {
		t.Error("Delete on a closed store succeeded")
	}
	if _, err := s.Token(ctx, "widgets"); err == nil || errors.Is(err, ErrNotSubscribed) {
		t.Errorf("Token on a closed store = %v, want the error of the database", err)
	}
}

func TestUpsert_ConcurrentWritersWaitForTheLock(t *testing.T) {
	s, _ := newStore(t)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 10 {
				if _, _, err := s.Upsert(ctx, fmt.Sprintf("w%d-%d", g, i), "tok", "", "usr_1", at(0)); err != nil {
					t.Errorf("a concurrent Upsert failed: %v", err)
				}
			}
		})
	}
	wg.Wait()
	if n := testutil.Count(t, s.DB(), "af_subscriptions", ""); n != 80 {
		t.Errorf("%d rows, want 80", n)
	}
}
