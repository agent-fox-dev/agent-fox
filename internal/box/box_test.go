package box

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// git runs git in dir and returns what it printed; the test fails if it fails.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRepo is a temporary repository on the branch, with a local identity (the
// box has a private HOME, and a commit inside it needs the one in .git/config)
// and the number of commits.
func newRepo(t *testing.T, commits int, branch string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", branch)
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	git(t, dir, "config", "commit.gpgsign", "false")
	for i := range commits {
		name := fmt.Sprintf("file%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), fmt.Appendf(nil, "content %d\n", i), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", ".")
		git(t, dir, "commit", "-q", "-m", fmt.Sprintf("commit %d", i))
	}
	return dir
}

// hostState is what the box must never change on the host: the commit, the
// branch, the status, the index and the files.
type hostState struct {
	head, branch, status string
	index                [sha256.Size]byte
	files                string
}

func snapshot(t *testing.T, dir string) hostState {
	t.Helper()
	index, err := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	return hostState{
		head:   git(t, dir, "rev-parse", "--short", "HEAD"),
		branch: git(t, dir, "rev-parse", "--abbrev-ref", "HEAD"),
		status: git(t, dir, "status", "--porcelain"),
		index:  sha256.Sum256(index),
		files:  git(t, dir, "ls-files", "--others", "--cached", "--exclude-standard"),
	}
}

// callTool runs one of the box's tools, the way a brain with its own hands
// would, and returns its result.
func callTool(t *testing.T, s *Session, name string, args map[string]any) core.ToolResult {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range s.Tools() {
		if tool.Name == name {
			return tool.Execute(context.Background(), raw)
		}
	}
	t.Fatalf("the box has no tool %q", name)
	return core.ToolResult{}
}

// inBox runs a shell command in the box and returns its output.
func inBox(t *testing.T, s *Session, command string) string {
	t.Helper()
	r := callTool(t, s, "execute", map[string]any{"command": command})
	if !r.OK {
		t.Fatalf("%s in the box: %s %s", command, r.Error, r.Detail)
	}
	return strings.TrimSpace(r.LLMText())
}

// ledger records what the box notes, and the warnings it raises.
type ledger struct {
	mu       sync.Mutex
	kinds    []string
	data     []json.RawMessage
	warnings []toolio.Warning
}

func (l *ledger) record(kind string, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.kinds = append(l.kinds, kind)
	l.data = append(l.data, raw)
	return nil
}

func (l *ledger) warn(code, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, toolio.Warning{Code: code, Message: message})
}

func (l *ledger) count(kind string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, k := range l.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

func (l *ledger) warned(code string) []toolio.Warning {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []toolio.Warning
	for _, w := range l.warnings {
		if w.Code == code {
			out = append(out, w)
		}
	}
	return out
}

// last is the data of the last entry of the kind, decoded into v.
func (l *ledger) last(t *testing.T, kind string, v any) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for i := len(l.kinds) - 1; i >= 0; i-- {
		if l.kinds[i] == kind {
			if err := json.Unmarshal(l.data[i], v); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatalf("the ledger has no %s entry", kind)
}

// counts counts what a wrapped provider is asked to do.
type counts struct{ creates, closes atomic.Int32 }

// countingProvider wraps a provider, and makes the sandboxes it creates fail
// to sync when syncErr is set.
type countingProvider struct {
	sandbox.Provider
	c       *counts
	syncErr error
}

func (p countingProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	p.c.creates.Add(1)
	return &countingSandbox{Sandbox: sb, c: p.c, syncErr: p.syncErr}, nil
}

type countingSandbox struct {
	sandbox.Sandbox
	c       *counts
	syncErr error
}

func (s *countingSandbox) Close(ctx context.Context) error {
	s.c.closes.Add(1)
	return s.Sandbox.Close(ctx)
}

func (s *countingSandbox) Sync(ctx context.Context, src sandbox.Source) (sandbox.SyncResult, error) {
	if s.syncErr != nil {
		return sandbox.SyncResult{}, s.syncErr
	}
	return s.Sandbox.Sync(ctx, src)
}

// failingProvider is a provider that cannot create a sandbox.
type failingProvider struct{ err error }

func (failingProvider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{Provider: "failing", Network: []sandbox.NetworkMode{sandbox.NetworkNone}}
}

func (p failingProvider) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, p.err
}

func (failingProvider) Reap(context.Context) ([]string, error) { return nil, nil }

// testRegistry has the real local provider as `counting` (counted), one that
// cannot create, and one whose sandboxes cannot sync.
func testRegistry() (*Registry, *counts) {
	c := &counts{}
	r := NewRegistry()
	r.Register(Entry{Name: "counting", Unconfined: true, New: func() sandbox.Provider {
		return countingProvider{Provider: localEntry().New(), c: c}
	}})
	r.Register(Entry{Name: "failcreate", New: func() sandbox.Provider {
		return failingProvider{err: errors.New("no capacity")}
	}})
	r.Register(Entry{Name: "failsync", New: func() sandbox.Provider {
		return countingProvider{Provider: localEntry().New(), c: c, syncErr: errors.New("transfer broke")}
	}})
	return r, c
}

// open opens a box on dir with the provider of the registry, and closes it
// when the test ends.
func open(t *testing.T, r *Registry, provider, dir string, l *ledger) *Session {
	t.Helper()
	s, err := Open(context.Background(), Request{
		Provider: provider, HostDir: dir, RunID: "20261020_120000_abcdef", Tool: "fix",
		Record: l.record, Warn: l.warn, Registry: r,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.sb.Close(context.Background()) })
	return s
}

// asFailure is err as a *toolio.Failure.
func asFailure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

// TestUnknownProviderIsUsage_TS_07_5 verifies TS-07-5:
// an unknown --sandbox value is a usage failure listing registered providers.
// Verifies: 07-REQ-1.5
func TestUnknownProviderIsUsage_TS_07_5(t *testing.T) {
	r, c := testRegistry()
	l := &ledger{}

	s, err := Open(context.Background(), Request{Provider: "podman", HostDir: t.TempDir(), RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r})

	if s != nil {
		t.Error("a session was returned for an unknown provider")
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "counting") || !strings.Contains(f.Message, "failcreate") || !strings.Contains(f.Message, "podman") {
		t.Errorf("failure = %+v, want usage naming podman and listing the registered providers", f)
	}
	if c.creates.Load() != 0 {
		t.Errorf("%d sandboxes were created", c.creates.Load())
	}
	if _, err := Default.Lookup("podman"); asFailure(t, err).Category != toolio.CategoryUsage || !strings.Contains(err.Error(), "local") {
		t.Errorf("the default registry says %v, want a usage failure that lists local", err)
	}
}

// TestOpenRaisesBoxUnconfinedOnce_TS_07_7 verifies TS-07-7:
// opening a local box raises box_unconfined once, and a later Sync does not.
// Verifies: 07-REQ-1.7
func TestOpenRaisesBoxUnconfinedOnce_TS_07_7(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}

	s := open(t, Default, "local", dir, l)
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	got := l.warned("box_unconfined")
	if len(got) != 1 || !strings.Contains(got[0].Message, "isolation") {
		t.Errorf("warnings = %+v, want one box_unconfined that says the box has no isolation", l.warnings)
	}
	if len(l.warnings) != 1 {
		t.Errorf("warnings = %+v, want no other", l.warnings)
	}
}

// TestOpenSyncsWithGitAndRecordsLedger_TS_07_10 verifies TS-07-10:
// Open on a work tree's top level creates the spec, syncs with .git and
// records the ledger entries in order.
// Verifies: 07-REQ-2.1, 07-REQ-2.3
func TestOpenSyncsWithGitAndRecordsLedger_TS_07_10(t *testing.T) {
	dir := newRepo(t, 1, "main")
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r, c := testRegistry()

	s, err := Open(context.Background(), Request{
		Provider: "counting", HostDir: dir, RunID: rd.ID, Tool: "fix", TTL: 90 * time.Minute,
		Record: rd.Record, Warn: func(string, string) {}, Registry: r,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.sb.Close(context.Background()) })

	if s.Provider() != "counting" || c.creates.Load() != 1 {
		t.Errorf("provider %q, %d sandboxes created", s.Provider(), c.creates.Load())
	}
	spec := s.Spec()
	if spec.Labels["af.run_id"] != rd.ID || spec.Labels["af.tool"] != "fix" || spec.TTL != 90*time.Minute {
		t.Errorf("spec = %+v, want the labels af.run_id and af.tool and the TTL", spec)
	}
	if len(s.Tools()) == 0 {
		t.Error("the session has no tools")
	}
	if got := inBox(t, s, "test -d .git && echo yes"); got != "yes" {
		t.Errorf("there is no .git in the box: %q", got)
	}
	if s.HostDir() != dir {
		t.Errorf("HostDir = %q, want %q", s.HostDir(), dir)
	}
	entries, err := rd.Ledger()
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Equal(kinds, []string{"box_open", "box_sync"}) {
		t.Fatalf("ledger kinds = %v, want box_open then box_sync", kinds)
	}
	var opened struct{ Provider string }
	var synced struct{ Head string }
	if err := json.Unmarshal(entries[0].Data, &opened); err != nil || opened.Provider != "counting" {
		t.Errorf("box_open = %s (%v)", entries[0].Data, err)
	}
	host, err := gitx.New(dir, nil).Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(entries[1].Data, &synced); err != nil || synced.Head != host {
		t.Errorf("box_sync = %s (%v), want head %s", entries[1].Data, err, host)
	}
}

// TestOpenRefusesWhatIsNotATopLevel_TS_07_11 verifies TS-07-11:
// a HostDir that is not a work tree or not its top level is refused with a
// usage failure, and a link to the top level is accepted.
// Verifies: 07-REQ-2.2
func TestOpenRefusesWhatIsNotATopLevel_TS_07_11(t *testing.T) {
	repo := newRepo(t, 1, "main")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	r, c := testRegistry()
	l := &ledger{}
	request := func(dir string) Request {
		return Request{Provider: "counting", HostDir: dir, RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r}
	}

	plain, err1 := Open(context.Background(), request(t.TempDir()))
	inner, err2 := Open(context.Background(), request(sub))

	if plain != nil || inner != nil {
		t.Error("a session was returned for a directory that is not a top level")
	}
	if f := asFailure(t, err1); f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "work tree") {
		t.Errorf("a plain directory: %+v", f)
	}
	if f := asFailure(t, err2); f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, repo) {
		t.Errorf("a subdirectory: %+v, want a usage failure that names %s", f, repo)
	}
	if c.creates.Load() != 0 {
		t.Errorf("%d sandboxes were created for the refused calls", c.creates.Load())
	}
	s, err := Open(context.Background(), request(link))
	if err != nil {
		t.Fatalf("a link to the top level is refused: %v", err)
	}
	t.Cleanup(func() { _ = s.sb.Close(context.Background()) })
}

// TestProviderThatCannotCreate_TS_07_12 verifies TS-07-12:
// a provider that cannot create a sandbox is failed/sandbox, not retryable.
// Verifies: 07-REQ-2.4
func TestProviderThatCannotCreate_TS_07_12(t *testing.T) {
	dir := newRepo(t, 1, "main")
	r, _ := testRegistry()
	l := &ledger{}

	s, err := Open(context.Background(), Request{Provider: "failcreate", HostDir: dir, RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r})

	if s != nil {
		t.Error("a session was returned")
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || f.Retryable || !strings.Contains(f.Message, "no capacity") {
		t.Errorf("failure = %+v, want sandbox, not retryable, with the provider's reason", f)
	}
}

// TestSyncFailureClosesTheSandbox_TS_07_13 verifies TS-07-13:
// a sync failure closes the sandbox and is failed/sandbox, retryable.
// Verifies: 07-REQ-2.5
func TestSyncFailureClosesTheSandbox_TS_07_13(t *testing.T) {
	dir := newRepo(t, 1, "main")
	r, c := testRegistry()
	l := &ledger{}

	s, err := Open(context.Background(), Request{Provider: "failsync", HostDir: dir, RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r})

	if s != nil {
		t.Error("a session was returned")
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategorySandbox || !f.Retryable || !strings.Contains(f.Message, "transfer broke") {
		t.Errorf("failure = %+v, want sandbox, retryable, with the reason", f)
	}
	if c.creates.Load() != 1 || c.closes.Load() != 1 {
		t.Errorf("%d created, %d closed, want 1 and 1", c.creates.Load(), c.closes.Load())
	}
}

// TestSyncSyncsAgain_TS_07_14 verifies TS-07-14:
// Session.Sync re-syncs the host directory and records another box_sync.
// Verifies: 07-REQ-2.6
func TestSyncSyncsAgain_TS_07_14(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}
	s := open(t, Default, "local", dir, l)
	before := inBox(t, s, "git rev-parse --short HEAD")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "a new commit on the host")
	head := git(t, dir, "rev-parse", "--short", "HEAD")
	if head == before {
		t.Fatal("the host commit did not move HEAD")
	}

	err := s.Sync(context.Background())

	if err != nil {
		t.Fatal(err)
	}
	if l.count("box_sync") != 2 {
		t.Errorf("box_sync entries = %d, want 2", l.count("box_sync"))
	}
	var synced struct{ Head string }
	l.last(t, "box_sync", &synced)
	if synced.Head != head {
		t.Errorf("the last box_sync says head %q, want %q", synced.Head, head)
	}
	if got := inBox(t, s, "git rev-parse --short HEAD"); got != head {
		t.Errorf("the box is at %s after the sync, want %s", got, head)
	}
}

// TestBoxHeadAndHostUntouched_TS_07_15 verifies TS-07-15:
// the box HEAD equals the host HEAD and a commit inside leaves the host
// untouched.
// Verifies: 07-REQ-2.7, 07-REQ-2.8
func TestBoxHeadAndHostUntouched_TS_07_15(t *testing.T) {
	dir := newRepo(t, 2, "main")
	s := open(t, Default, "local", dir, &ledger{})
	before := snapshot(t, dir)

	boxHead := inBox(t, s, "git rev-parse --short HEAD")
	if r := callTool(t, s, "write_file", map[string]any{"path": "new.txt", "content": "from the box\n"}); !r.OK {
		t.Fatalf("write_file: %s %s", r.Error, r.Detail)
	}
	inBox(t, s, "git add -A && git commit -q -m 'inside the box'")
	boxCommit := inBox(t, s, "git rev-parse --short HEAD")

	if boxHead != before.head {
		t.Errorf("the box is at %s, the host at %s", boxHead, before.head)
	}
	if boxCommit == before.head {
		t.Error("the commit made inside the box did not move the box's HEAD")
	}
	if after := snapshot(t, dir); after != before {
		t.Errorf("the host changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the file written in the box is on the host: %v", err)
	}
	if out := git(t, dir, "log", "--oneline", "--all"); strings.Contains(out, "inside the box") {
		t.Errorf("the commit made in the box is in the host's history:\n%s", out)
	}
}

// TestBoxHeadForAnyRepository_TS_07_16 verifies TS-07-16:
// for any synced repository state the box's short HEAD equals the host's.
// Verifies: 07-REQ-2.7
func TestBoxHeadForAnyRepository_TS_07_16(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 16))
	branches := []string{"main", "feature/x", "release-1.2", "topic_3", "ünï"}
	for i := range 6 {
		commits, branch := 1+rng.IntN(6), branches[rng.IntN(len(branches))]
		t.Run(fmt.Sprintf("%d commits on %s", commits, branch), func(t *testing.T) {
			dir := newRepo(t, commits, branch)
			s := open(t, Default, "local", dir, &ledger{})

			host := git(t, dir, "rev-parse", "--short", "HEAD")

			if got := inBox(t, s, "git rev-parse --short HEAD"); got != host {
				t.Errorf("#%d: the box is at %s, the host at %s", i, got, host)
			}
		})
	}
}

// TestHostUntouchedByAnyWork_TS_07_17 verifies TS-07-17:
// for any file write or commit inside the box the host checkout is unchanged.
// Verifies: 07-REQ-2.8
func TestHostUntouchedByAnyWork_TS_07_17(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 17))
	dirs := []string{"", "src/", "deep/er/", "docs/"}
	for i := range 4 {
		t.Run(fmt.Sprintf("sequence %d", i), func(t *testing.T) {
			dir := newRepo(t, 2, "main")
			s := open(t, Default, "local", dir, &ledger{})
			before := snapshot(t, dir)
			var written []string

			for step := range 3 + rng.IntN(6) {
				if rng.IntN(3) == 0 {
					inBox(t, s, fmt.Sprintf("git add -A && git commit -q --allow-empty -m 'step %d'", step))
					continue
				}
				path := fmt.Sprintf("%sgen%d_%d.txt", dirs[rng.IntN(len(dirs))], i, step)
				content := fmt.Sprintf("content %d\n%s\n", rng.Int(), strings.Repeat("x", rng.IntN(200)))
				if r := callTool(t, s, "write_file", map[string]any{"path": path, "content": content}); !r.OK {
					t.Fatalf("write_file %s: %s %s", path, r.Error, r.Detail)
				}
				written = append(written, path)
			}

			if after := snapshot(t, dir); after != before {
				t.Errorf("the host changed:\nbefore %+v\nafter  %+v", before, after)
			}
			for _, path := range written {
				if _, err := os.Stat(filepath.Join(dir, path)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("%s is on the host: %v", path, err)
				}
			}
		})
	}
}

// TestSyncMirrorsTheHost guards 07-REQ-2.6: after Sync the box has what the
// host has: what the box added is gone, what it changed is back, and git's own
// state is the host's, also when the host moved its branch within the second of
// the last sync (the transfer takes a file of the same length and modification
// second for unchanged, and a branch file is always as long as a hash).
func TestSyncMirrorsTheHost(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}
	s := open(t, Default, "local", dir, l)
	for path, content := range map[string]string{"extra.txt": "only in the box\n", "file0.txt": "box edit\n"} {
		if r := callTool(t, s, "write_file", map[string]any{"path": path, "content": content}); !r.OK {
			t.Fatalf("write_file %s: %s", path, r.Error)
		}
	}
	inBox(t, s, "git add -A && git commit -q -m 'inside'")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "on the host")
	git(t, dir, "commit", "-q", "--allow-empty", "-m", "on the host again")

	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := inBox(t, s, "cat file0.txt"); got != "content 0" {
		t.Errorf("file0.txt in the box = %q, want the host's", got)
	}
	if got := inBox(t, s, "ls extra.txt 2>&1; true"); !strings.Contains(got, "No such file") {
		t.Errorf("extra.txt survived the sync: %q", got)
	}
	if got, want := inBox(t, s, "git rev-parse --short HEAD"), git(t, dir, "rev-parse", "--short", "HEAD"); got != want {
		t.Errorf("the box is at %s, the host at %s", got, want)
	}
	if got := inBox(t, s, "git status --porcelain; git log --oneline | head -3"); strings.Contains(got, "inside") || strings.Contains(got, "?? ") {
		t.Errorf("the box carries what it did itself:\n%s", got)
	}
}
