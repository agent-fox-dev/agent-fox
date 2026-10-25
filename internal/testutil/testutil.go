// Package testutil holds what the tests of every package need and would
// otherwise each write again: git run without the user's configuration, a
// repository with a first commit, files, a wait with a deadline, a log that
// the code under test writes JSON records to, JSON helpers, a SQLite database,
// and HTTP requests and the service's error envelope.
//
// It imports no package of this repository, so the tests of any package can
// use it. What one package alone needs stays in that package's
// helpers_test.go.
package testutil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // registers the database/sql driver "sqlite"
)

// Require skips the test unless every program is on PATH.
func Require(t testing.TB, programs ...string) {
	t.Helper()
	for _, p := range programs {
		if _, err := exec.LookPath(p); err != nil {
			t.Skipf("%s is not installed", p)
		}
	}
}

// Git runs git in dir with an identity of its own, no signing, and none of
// the user's or the system's configuration, and returns what it printed. It
// fails the test when git fails.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := GitErr(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// GitErr is Git for a command that may fail: it returns what git printed and
// its error.
func GitErr(t testing.TB, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main",
	}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// IsolateGit makes the git that the code under test runs itself, outside Git,
// read none of the user's or the system's configuration for the rest of the
// test, with a home of its own, no prompt, and an identity to commit with. It
// sets the environment with t.Setenv, so the test must not be parallel.
func IsolateGit(t testing.TB) {
	t.Helper()
	home := t.TempDir()
	for name, value := range map[string]string{
		"HOME": home, "XDG_CONFIG_HOME": home, "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_AUTHOR_NAME":     "Test", "GIT_AUTHOR_EMAIL": "test@example.com",
		"GIT_COMMITTER_NAME": "Test", "GIT_COMMITTER_EMAIL": "test@example.com",
	} {
		t.Setenv(name, value)
	}
}

// Repo makes a git repository on the branch main whose first commit holds
// files (path to content), and returns its directory. It skips the test when
// git is not installed. With no files the commit holds a README.md.
func Repo(t testing.TB, files map[string]string) string {
	t.Helper()
	Require(t, "git")
	dir := t.TempDir()
	Git(t, dir, "init", "-q", "-b", "main")
	if len(files) == 0 {
		files = map[string]string{"README.md": "# test\n"}
	}
	for name, content := range files {
		WriteFile(t, dir, name, content)
	}
	Commit(t, dir, "base")
	return dir
}

// Commit adds everything in dir and commits it with the message, and returns
// the full hash of the new HEAD.
func Commit(t testing.TB, dir, message string) string {
	t.Helper()
	Git(t, dir, "add", "-A")
	Git(t, dir, "commit", "-q", "--allow-empty", "-m", message)
	return strings.TrimSpace(Git(t, dir, "rev-parse", "HEAD"))
}

// WriteFile writes content to name under dir, making the directories it
// needs, and returns the file's path.
func WriteFile(t testing.TB, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ReadFile is the content of the file, and fails the test when it cannot be
// read.
func ReadFile(t testing.TB, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Files are the regular files under dir, by slash-separated path, with their
// content. A directory named in skip (".git", say) is not entered.
func Files(t testing.TB, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			for _, s := range skip {
				if d.Name() == s && rel != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if d.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[filepath.ToSlash(rel)] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// WaitTimeout is how long Eventually waits.
const WaitTimeout = 10 * time.Second

// Eventually waits until cond holds, polling it, and fails the test naming
// what it waited for when WaitTimeout passes first.
func Eventually(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(WaitTimeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("gave up waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// Log is a destination for JSON log records that goroutines may write to at
// once, and the records written so far.
type Log struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write takes the bytes of one or more records.
func (l *Log) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// Logger is a logger that writes JSON records to l, at every level from Debug.
func (l *Log) Logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// Records are the records written so far, each decoded; a line that is not a
// JSON object is left out.
func (l *Log) Records() []map[string]any {
	l.mu.Lock()
	text := l.buf.String()
	l.mu.Unlock()
	return objectLines(text)
}

// Find are the records of the level (INFO, WARN, ERROR, ...; any level when
// it is empty) whose message contains text.
func (l *Log) Find(level, text string) []map[string]any {
	var out []map[string]any
	for _, rec := range l.Records() {
		msg, _ := rec["msg"].(string)
		if (level == "" || rec["level"] == level) && strings.Contains(msg, text) {
			out = append(out, rec)
		}
	}
	return out
}

// String is everything written so far.
func (l *Log) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// JSON is v encoded, and fails the test when it cannot be.
func JSON(t testing.TB, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// Decode is data decoded into a T, and fails the test when it does not decode.
func Decode[T any](t testing.TB, data []byte) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%v: %s", err, data)
	}
	return v
}

// JSONLines are the lines of the file at path that are JSON objects, decoded,
// such as the lines of events.jsonl or ledger.jsonl. A missing file has none.
func JSONLines(t testing.TB, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return objectLines(string(data))
}

func objectLines(text string) []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(text, "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec != nil {
			out = append(out, rec)
		}
	}
	return out
}

// SHA256 is the lower-case hex SHA-256 of s.
func SHA256(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Key is 32 bytes that no file or log holds by chance, for a key the code
// under test seals with.
func Key() []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = byte(0xA1 + 7*i)
	}
	return b
}

// OpenDB opens a SQLite database in a new file, with the pragmas the
// service's state file has (WAL, a busy timeout, foreign keys), and closes it
// when the test ends. The tables are the caller's to make, with the schema
// function of the package that owns them.
func OpenDB(t testing.TB) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatal(err)
	}
	return db
}

// Querier is a *sql.DB, a *sql.Tx or a *sql.Conn.
type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Exec runs a statement, and fails the test when it fails.
func Exec(t testing.TB, db Querier, query string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// Column runs a query that answers one column and returns its values, a NULL
// as "".
func Column(t testing.TB, db Querier, query string, args ...any) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query, args...)
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

// Count runs SELECT COUNT(*) FROM table [WHERE where] with args.
func Count(t testing.TB, db Querier, table, where string, args ...any) int {
	t.Helper()
	query := "SELECT COUNT(*) FROM " + table
	if where != "" {
		query += " WHERE " + where
	}
	rows, err := db.QueryContext(context.Background(), query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var n int
	if !rows.Next() || rows.Scan(&n) != nil {
		t.Fatalf("%s: no count", query)
	}
	return n
}

// Response is what an HTTP request was answered.
type Response struct {
	Status int
	Header http.Header
	Body   string
}

// Object is the body decoded as a JSON object; it fails the test when the
// body is not one.
func (r Response) Object(t testing.TB) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(r.Body), &v); err != nil || v == nil {
		t.Fatalf("the body is not a JSON object: %q", r.Body)
	}
	return v
}

// Objects is the body decoded as a JSON array of objects; it fails the test
// when the body is not one.
func (r Response) Objects(t testing.TB) []map[string]any {
	t.Helper()
	var v []map[string]any
	if err := json.Unmarshal([]byte(r.Body), &v); err != nil || v == nil {
		t.Fatalf("the body is not a JSON array of objects: %q", r.Body)
	}
	return v
}

// Error is the code of the service's error envelope, {"error": {"code",
// "message"}}, and fails the test unless the body is that envelope and
// nothing else, with a JSON content type, a code and a message.
func (r Response) Error(t testing.TB) string {
	t.Helper()
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	body := r.Object(t)
	detail, _ := body["error"].(map[string]any)
	code, _ := detail["code"].(string)
	msg, _ := detail["message"].(string)
	if len(body) != 1 || code == "" || msg == "" {
		t.Errorf("body = %s, want only an error with a code and a message", r.Body)
	}
	return code
}

// Bearer is the header pair of a bearer token, for Request.
func Bearer(token string) []string { return []string{"Authorization", "Bearer " + token} }

// Request sends a request to url and returns the answer, without following a
// redirect. headers are name and value pairs (see Bearer); a body given as a
// string, []byte or io.Reader is sent as it is, anything else as JSON with
// that content type.
func Request(t testing.TB, method, url string, body any, headers ...[]string) Response {
	t.Helper()
	var reader io.Reader
	isJSON := false
	switch b := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(b)
	case []byte:
		reader = bytes.NewReader(b)
	case io.Reader:
		reader = b
	default:
		reader, isJSON = bytes.NewReader(JSON(t, b)), true
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if isJSON {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, h := range headers {
		for i := 0; i+1 < len(h); i += 2 {
			req.Header.Set(h[i], h[i+1])
		}
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return Response{Status: resp.StatusCode, Header: resp.Header, Body: string(data)}
}
