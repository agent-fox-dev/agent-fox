package box

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// placeTree makes a directory with files, the way the checkout of a run is.
func placeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// treeOf is every file under dir with its content, but for what is under skip.
func treeOf(t *testing.T, dir, skip string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == skip || strings.HasPrefix(rel, skip+string(filepath.Separator)) {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func openHere(t *testing.T, dir string, l *ledger) *Session {
	t.Helper()
	s, err := Here(context.Background(), Request{HostDir: dir, RunID: "20261020_120000_abcdef", Tool: "triage", Record: l.record, Warn: l.warn})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestHereServesTheDirectoryWhereItIs guards the box of `af triage run`, which is
// the process a box executes: the tools work on the directory itself, with no
// copy, no sync and no export of the tree.
func TestHereServesTheDirectoryWhereItIs(t *testing.T) {
	dir := placeTree(t, map[string]string{"main.go": "package main\n", "sub/util.go": "package sub\n", "latin1.txt": "caf\xe9\n"})
	before := treeOf(t, dir, "")
	l := &ledger{}

	s := openHere(t, dir, l)

	if s.HostDir() != dir || s.Provider() != "here" || s.Spec().Workdir != dir {
		t.Errorf("HostDir %q, provider %q, workdir %q, want the directory itself", s.HostDir(), s.Provider(), s.Spec().Workdir)
	}
	var names []string
	for _, tool := range s.Tools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "write_file", "execute"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools = %v, want %s", names, want)
		}
	}

	if r := callTool(t, s, "read_file", map[string]any{"path": "main.go"}); !r.OK || !strings.Contains(r.LLMText(), "package main") {
		t.Errorf("read_file = %+v", r)
	}
	fsys := s.FS(context.Background())
	if data, err := fs.ReadFile(fsys, "sub/util.go"); err != nil || string(data) != "package sub\n" {
		t.Errorf("ReadFile(sub/util.go) = %q, %v", data, err)
	}
	if data, err := fs.ReadFile(fsys, "latin1.txt"); err != nil || string(data) != "caf\xe9\n" {
		t.Errorf("ReadFile(latin1.txt) = %q, %v, want the exact bytes of a file that is not UTF-8", data, err)
	}
	if _, err := fs.Stat(fsys, "nowhere.go"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(nowhere.go) error = %v, want fs.ErrNotExist", err)
	}
	if info, err := fs.Stat(fsys, "sub"); err != nil || !info.IsDir() {
		t.Errorf("Stat(sub) = %v, %v, want a directory", info, err)
	}

	if err := s.WriteFile(context.Background(), ".agent-fox/run/x/note.txt", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".agent-fox", "run", "x", "note.txt")); err != nil || string(data) != "hello" {
		t.Errorf("WriteFile wrote %q, %v in the directory", data, err)
	}
	if err := s.WriteFile(context.Background(), "main.go", []byte("x")); err == nil {
		t.Error("WriteFile wrote outside .agent-fox/")
	}

	if err := s.Sync(context.Background()); err == nil {
		t.Error("Sync of a tree in place succeeded: there is nothing to sync")
	}
	if got := treeOf(t, dir, ".agent-fox"); !equalTrees(got, before) {
		t.Errorf("the directory changed outside .agent-fox/: %v, want %v", got, before)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("the second Close = %v, want nil", err)
	}
	if l.count("box_open") != 1 || l.count("box_close") != 1 || l.count("box_sync") != 0 {
		t.Errorf("ledger: %d box_open, %d box_sync, %d box_close, want 1, 0 and 1", l.count("box_open"), l.count("box_sync"), l.count("box_close"))
	}
}

func equalTrees(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestHereNeverFollowsALinkOutOfTheDirectory guards 08-REQ-4.7's reading side:
// a link that leaves the directory is refused by FS, as it is in a box.
func TestHereNeverFollowsALinkOutOfTheDirectory(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("the secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := placeTree(t, map[string]string{"main.go": "package main\n"})
	if err := os.Symlink(secret, filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	s := openHere(t, dir, &ledger{})

	data, err := fs.ReadFile(s.FS(context.Background()), "leak.txt")

	if err == nil || strings.Contains(string(data), "secret") {
		t.Errorf("ReadFile through a link out of the directory = %q, %v, want it refused", data, err)
	}
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("error = %v, want fs.ErrPermission", err)
	}
}

// TestHereLeavesGitAlone guards that a session in place is no box that scrubs
// or refreshes the git state it holds: the directory is the user's own.
func TestHereLeavesGitAlone(t *testing.T) {
	dir := newRepo(t, 1, "main")
	git(t, dir, "remote", "add", "origin", "https://user:ghp_SECRET@example.com/x.git")
	git(t, dir, "config", "http.extraheader", "Authorization: bearer SECRET")
	config := func() string {
		data, err := os.ReadFile(filepath.Join(dir, ".git", "config"))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	before := config()
	head := git(t, dir, "rev-parse", "HEAD")

	s := openHere(t, dir, &ledger{})
	if err := s.Sync(context.Background()); err == nil {
		t.Error("Sync of a tree in place succeeded")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if after := config(); after != before {
		t.Errorf(".git/config changed:\n%s\n%s", before, after)
	}
	if git(t, dir, "rev-parse", "HEAD") != head || git(t, dir, "status", "--porcelain") != "" {
		t.Error("the repository changed")
	}
}

// TestHereWantsADirectory guards the request: a path that is no directory is a
// usage failure that opens nothing.
func TestHereWantsADirectory(t *testing.T) {
	dir := placeTree(t, map[string]string{"main.go": "package main\n"})
	for name, path := range map[string]string{"a file": filepath.Join(dir, "main.go"), "a missing path": filepath.Join(dir, "nowhere")} {
		t.Run(name, func(t *testing.T) {
			s, err := Here(context.Background(), Request{HostDir: path})

			if s != nil {
				t.Error("a session was returned")
			}
			if f := asFailure(t, err); f.Category != toolio.CategoryUsage {
				t.Errorf("failure = %+v, want usage", f)
			}
		})
	}
}

// TestHereSaysThatTheTreeIsReadInPlace guards the honesty of a session with no
// copy: the tools reach what the process reaches, a .env and .git/config included,
// and every session says so once with box_unconfined.
func TestHereSaysThatTheTreeIsReadInPlace(t *testing.T) {
	dir := placeTree(t, map[string]string{"a.go": "package a\n"})
	var warnings [][2]string

	sess, err := Here(context.Background(), Request{HostDir: dir, RunID: "20260101_000000_aaaaaa", Tool: "triage",
		Warn: func(code, message string) { warnings = append(warnings, [2]string{code, message}) }})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if len(warnings) != 1 || warnings[0][0] != "box_unconfined" || !strings.Contains(warnings[0][1], ".git/config") || !strings.Contains(warnings[0][1], "in place") {
		t.Errorf("warnings = %v, want box_unconfined once, that says what is reachable", warnings)
	}
}
