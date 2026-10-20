package box

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The tests of this file hold the box to what it promises where the checkout
// is not the plain clone of the happy path: a work tree that keeps its git
// state elsewhere, a remote with a token in its URL, a tracked file a rule
// ignores, and a directory the list tool cannot list whole.

func TestOpenRefusesAWorkTreeThatKeepsItsGitElsewhere(t *testing.T) {
	main := newRepo(t, 1, "main")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", "-b", "feature", wt)
	r, c := testRegistry()
	l := &ledger{}
	before := git(t, main, "rev-parse", "feature")

	s, err := Open(context.Background(), Request{Provider: "counting", HostDir: wt, RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r})

	if s != nil {
		t.Fatal("a session was returned for a linked worktree")
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, ".git") {
		t.Errorf("failure = %+v, want a usage failure that names .git", f)
	}
	if c.creates.Load() != 0 {
		t.Errorf("%d sandboxes were created, want none", c.creates.Load())
	}
	if after := git(t, main, "rev-parse", "feature"); after != before {
		t.Errorf("the branch of the worktree moved from %s to %s", before, after)
	}
}

func TestOpenTakesADirectoryThatIsRelative(t *testing.T) {
	dir := newRepo(t, 1, "main")
	t.Chdir(dir)
	r, _ := testRegistry()

	s := open(t, r, "counting", ".", &ledger{})

	if got := s.HostDir(); got != dir {
		t.Errorf("HostDir = %q, want %q", got, dir)
	}
}

func TestTheBoxHoldsNoCredentialOfTheHostsGitConfiguration(t *testing.T) {
	dir := newRepo(t, 1, "main")
	git(t, dir, "remote", "add", "origin", "https://user:ghp_SECRET1@example.com/x.git")
	git(t, dir, "config", "http.extraheader", "Authorization: bearer SECRET2")
	git(t, dir, "config", "credential.helper", "store --file=/nowhere SECRET3")
	git(t, dir, "config", "url.https://tok:SECRET4@example.com/.insteadOf", "https://example.com/")
	git(t, dir, "config", "branch.main.note", "kept")
	s := open(t, Default, "local", dir, &ledger{})
	hostConfig := git(t, dir, "config", "--local", "--list")

	check := func(when string) {
		t.Helper()
		got := inBox(t, s, "git config --local --list")
		for _, secret := range []string{"SECRET", "extraheader", "credential", "insteadof"} {
			if strings.Contains(strings.ToLower(got), strings.ToLower(secret)) {
				t.Errorf("%s: the box's git configuration holds %q:\n%s", when, secret, got)
			}
		}
		if !strings.Contains(got, "branch.main.note=kept") {
			t.Errorf("%s: the rest of the configuration is gone:\n%s", when, got)
		}
	}
	check("after Open")

	git(t, dir, "remote", "add", "second", "https://other:SECRET5@example.com/y.git")
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("after Sync")
	if got := git(t, dir, "config", "--local", "--list"); !strings.Contains(got, "SECRET1") || !strings.Contains(hostConfig, "SECRET1") {
		t.Errorf("the host's own configuration lost its remote:\n%s", got)
	}
}

func TestATrackedFileThatARuleIgnoresIsInTheBox(t *testing.T) {
	dir := newRepo(t, 1, "main")
	commitFiles(t, dir, map[string][]byte{"gen/api.pb.go": []byte("v1\n"), "gen/deep/x.go": []byte("deep\n")})
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("gen/\n.env\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", ".gitignore")
	git(t, dir, "commit", "-q", "-m", "ignore the generated code")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("KEY=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := open(t, Default, "local", dir, &ledger{})

	if got := inBox(t, s, "cat gen/api.pb.go gen/deep/x.go"); got != "v1\ndeep" {
		t.Errorf("the tracked files of an ignored directory = %q, want them", got)
	}
	if got := inBox(t, s, `test -z "$(git status --porcelain)" && echo clean || git status --porcelain`); got != "clean" {
		t.Errorf("git in the box sees a change the host does not:\n%s", got)
	}
	if got := inBox(t, s, "ls .env 2>&1; true"); !strings.Contains(got, "No such file") {
		t.Errorf("an ignored file that git does not track went in: %q", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "gen", "api.pb.go"), []byte("v2 is longer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := inBox(t, s, "cat gen/api.pb.go"); got != "v2 is longer" {
		t.Errorf("after a Sync the tracked ignored file = %q, want the host's", got)
	}
}

func TestSyncOfARepositoryWithNoCommitRecordsNoHead(t *testing.T) {
	dir := newRepo(t, 0, "main")
	l := &ledger{}

	open(t, Default, "local", dir, l)

	var sync struct{ Head string }
	l.last(t, "box_sync", &sync)
	if sync.Head != "" {
		t.Errorf("box_sync head = %q, want none for a repository with no commit", sync.Head)
	}
}

func TestFilesAreReadTogether(t *testing.T) {
	dir := newRepo(t, 8, "main")
	s := open(t, Default, "local", dir, &ledger{})
	fsys := s.FS(context.Background())

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := fmt.Sprintf("file%d.txt", i)
			data, err := fs.ReadFile(fsys, name)
			if err == nil && string(data) != fmt.Sprintf("content %d\n", i) {
				err = fmt.Errorf("%s holds %q", name, data)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestStatInADirectoryTooLongToList(t *testing.T) {
	dir := newRepo(t, 1, "main")
	files := map[string][]byte{"big/sub/x.txt": []byte("x\n")}
	for i := range listLimit + 21 {
		files[fmt.Sprintf("big/f%03d.txt", i)] = []byte("f\n")
	}
	commitFiles(t, dir, files)
	s := open(t, Default, "local", dir, &ledger{})
	fsys := s.FS(context.Background())

	if info, err := fs.Stat(fsys, "big/sub"); err != nil || !info.IsDir() {
		t.Errorf("Stat(big/sub) = %v, %v, want a directory", info, err)
	}
	if info, err := fs.Stat(fsys, "big/f010.txt"); err != nil || info.IsDir() || info.Size() != 2 {
		t.Errorf("Stat(big/f010.txt) = %v, %v, want a file of 2 bytes", info, err)
	}
	if data, err := fs.ReadFile(fsys, "big/sub/x.txt"); err != nil || string(data) != "x\n" {
		t.Errorf("ReadFile(big/sub/x.txt) = %q, %v", data, err)
	}
	if _, err := fs.Stat(fsys, "big/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(big/missing) error = %v, want fs.ErrNotExist", err)
	}
	if _, err := fs.Stat(fsys, "big/sub/missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(big/sub/missing.txt) error = %v, want fs.ErrNotExist", err)
	}
}

func TestAnExportThatCloseOvertakesIsRemoved(t *testing.T) {
	dir := newRepo(t, 1, "main")
	f := &faults{}
	r, _ := faultyRegistry(f)
	l := &ledger{}
	s, err := Open(context.Background(), request(dir, r, l))
	if err != nil {
		t.Fatal(err)
	}
	// Close runs after the box has exported and before Export has noted the directory.
	f.afterExport = func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}

	path, err := s.Export(context.Background())

	if err == nil {
		t.Errorf("Export returned %q after the session was closed, want an error", path)
	}
	exported := f.exported()
	if len(exported) != 1 {
		t.Fatalf("the box exported %d times, want 1", len(exported))
	}
	if _, err := os.Stat(exported[0]); err == nil {
		t.Errorf("the export directory %s was left behind", exported[0])
	}
	if l.count("box_export") != 0 {
		t.Errorf("box_export was recorded for an export that was thrown away")
	}
}

func TestACloseThatFailsAfterAFailedSyncIsAWarning(t *testing.T) {
	dir := newRepo(t, 1, "main")
	r, _ := faultyRegistry(&faults{syncErr: errors.New("transfer broke"), closeErr: errors.New("the box would not go")})
	l := &ledger{}

	_, err := Open(context.Background(), request(dir, r, l))

	if f := asFailure(t, err); !strings.Contains(f.Message, "transfer broke") {
		t.Errorf("failure = %+v, want the failure of the sync", f)
	}
	warned := l.warned("box_close_failed")
	if len(warned) != 1 || !strings.Contains(warned[0].Message, "would not go") {
		t.Errorf("warnings = %+v, want one box_close_failed that carries the error", l.warnings)
	}
}
