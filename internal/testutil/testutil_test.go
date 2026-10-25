package testutil

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepoCommitsTheFilesOnMainWithoutTheUsersConfiguration(t *testing.T) {
	dir := Repo(t, map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"})
	if branch := strings.TrimSpace(Git(t, dir, "branch", "--show-current")); branch != "main" {
		t.Errorf("branch = %q, want main", branch)
	}
	if author := strings.TrimSpace(Git(t, dir, "log", "-1", "--format=%an <%ae>")); author != "Test <test@example.com>" {
		t.Errorf("author = %q", author)
	}
	if status := Git(t, dir, "status", "--porcelain"); status != "" {
		t.Errorf("the tree is not clean: %q", status)
	}
	WriteFile(t, dir, "c.txt", "c\n")
	head := Commit(t, dir, "second")
	if got := strings.TrimSpace(Git(t, dir, "rev-parse", "HEAD")); got != head || len(head) != 40 {
		t.Errorf("Commit = %q, HEAD = %q", head, got)
	}
	if got := Files(t, dir, ".git"); len(got) != 3 || got["sub/b.txt"] != "b\n" {
		t.Errorf("Files = %v", got)
	}
	if _, err := GitErr(t, dir, "rev-parse", "nothing-here"); err == nil {
		t.Error("GitErr of a failing command returned no error")
	}
}

func TestLogRecordsAndFindsByLevelAndMessage(t *testing.T) {
	var l Log
	logger := l.Logger()
	logger.Debug("starting", "n", 1)
	logger.Warn("the disk is full", "path", "/x")
	logger.Log(t.Context(), slog.LevelError, "the disk is gone")
	if n := len(l.Records()); n != 3 {
		t.Fatalf("records = %d, want 3: %s", n, l.String())
	}
	if got := l.Find("WARN", "disk"); len(got) != 1 || got[0]["path"] != "/x" {
		t.Errorf(`Find("WARN", "disk") = %v`, got)
	}
	if got := l.Find("", "disk"); len(got) != 2 {
		t.Errorf(`Find("", "disk") = %v, want both levels`, got)
	}
}

func TestJSONHelpersRoundTrip(t *testing.T) {
	type doc struct{ A int }
	if got := Decode[doc](t, JSON(t, doc{A: 7})); got.A != 7 {
		t.Errorf("Decode(JSON(x)) = %+v", got)
	}
	path := WriteFile(t, t.TempDir(), "events.jsonl", "{\"seq\":1}\nnot json\n{\"seq\":2}\n")
	if got := JSONLines(t, path); len(got) != 2 || got[1]["seq"] != 2.0 {
		t.Errorf("JSONLines = %v", got)
	}
	if got := JSONLines(t, filepath.Join(t.TempDir(), "missing")); got != nil {
		t.Errorf("JSONLines of a missing file = %v", got)
	}
	if SHA256("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Error("SHA256 is not the hex SHA-256")
	}
	if k := Key(); len(k) != 32 || string(k) != string(Key()) {
		t.Error("Key is not 32 stable bytes")
	}
}

func TestDatabaseHelpers(t *testing.T) {
	db := OpenDB(t)
	Exec(t, db, "CREATE TABLE t (name TEXT)")
	Exec(t, db, "INSERT INTO t VALUES (?), (?), (NULL)", "a", "b")
	if n := Count(t, db, "t", ""); n != 3 {
		t.Errorf("Count = %d, want 3", n)
	}
	if n := Count(t, db, "t", "name = ?", "a"); n != 1 {
		t.Errorf("Count where = %d, want 1", n)
	}
	if got := Column(t, db, "SELECT name FROM t ORDER BY rowid"); strings.Join(got, ",") != "a,b," {
		t.Errorf("Column = %q", got)
	}
}

func TestRequestSendsJSONAndReadsTheErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"auth":"` + r.Header.Get("Authorization") + `","type":"` + r.Header.Get("Content-Type") + `"}`))
		case "/list":
			_, _ = w.Write([]byte(`[{"a":1}]`))
		case "/moved":
			http.Redirect(w, r, "/echo", http.StatusFound)
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such route"}}`))
		}
	}))
	defer srv.Close()

	echo := Request(t, http.MethodPost, srv.URL+"/echo", map[string]int{"n": 1}, Bearer("tok"))
	if got := echo.Object(t); got["auth"] != "Bearer tok" || got["type"] != "application/json" {
		t.Errorf("echo = %v", got)
	}
	if got := Request(t, http.MethodGet, srv.URL+"/list", nil).Objects(t); len(got) != 1 {
		t.Errorf("Objects = %v", got)
	}
	if r := Request(t, http.MethodGet, srv.URL+"/moved", nil); r.Status != http.StatusFound {
		t.Errorf("a redirect was followed: %d", r.Status)
	}
	r := Request(t, http.MethodGet, srv.URL+"/nope", "")
	if r.Status != http.StatusNotFound || r.Error(t) != "not_found" {
		t.Errorf("error = %d %s", r.Status, r.Body)
	}
}
