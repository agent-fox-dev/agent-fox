package box

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

// commitFiles writes files into the repository on the host and commits them.
func commitFiles(t *testing.T, dir string, files map[string][]byte) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "more files")
}

// faults says how the sandboxes of a faulty provider misbehave, and what they
// were asked.
type faults struct {
	closeErr  error // what Close returns, after closing
	exportGit bool  // the export carries a .git, as no provider should let it
	syncErr   error // what Sync returns, instead of syncing

	// afterExport, when it is set, is called once the box has exported, before
	// Export returns to its caller.
	afterExport func()

	mu         sync.Mutex
	exports    []string    // the directories the box was asked to export into
	closeCalls []closeCall // what Close was called with
}

// closeCall is the state of the context of a call to Close, when it was made.
type closeCall struct {
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (f *faults) exported() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.exports)
}

type faultySandbox struct {
	*countingSandbox
	f *faults
}

func (s *faultySandbox) Sync(ctx context.Context, src sandbox.Source) (sandbox.SyncResult, error) {
	if s.f.syncErr != nil {
		return sandbox.SyncResult{}, s.f.syncErr
	}
	return s.countingSandbox.Sync(ctx, src)
}

func (s *faultySandbox) Export(ctx context.Context, path string, dst sandbox.Destination) error {
	err := s.Sandbox.Export(ctx, path, dst)
	if s.f.afterExport != nil {
		s.f.afterExport()
	}
	if ld, ok := dst.(sandbox.LocalDir); ok {
		s.f.mu.Lock()
		s.f.exports = append(s.f.exports, ld.Path)
		s.f.mu.Unlock()
		if s.f.exportGit {
			if merr := os.MkdirAll(filepath.Join(ld.Path, ".git"), 0o755); merr != nil {
				return merr
			}
		}
	}
	return err
}

func (s *faultySandbox) Close(ctx context.Context) error {
	deadline, ok := ctx.Deadline()
	s.f.mu.Lock()
	s.f.closeCalls = append(s.f.closeCalls, closeCall{err: ctx.Err(), deadline: deadline, hasDeadline: ok})
	s.f.mu.Unlock()
	err := s.countingSandbox.Close(ctx)
	if s.f.closeErr != nil {
		return s.f.closeErr
	}
	return err
}

type faultyProvider struct {
	sandbox.Provider
	c *counts
	f *faults
}

func (p faultyProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	sb, err := p.Provider.Create(ctx, spec)
	if err != nil {
		return nil, err
	}
	p.c.creates.Add(1)
	return &faultySandbox{countingSandbox: &countingSandbox{Sandbox: sb, c: p.c}, f: p.f}, nil
}

// faultyRegistry has the real local provider as `faulty`, with the faults.
func faultyRegistry(f *faults) (*Registry, *counts) {
	c := &counts{}
	r := NewRegistry()
	r.Register(Entry{Name: "faulty", Unconfined: true, New: func() sandbox.Provider {
		return faultyProvider{Provider: localEntry().New(), c: c, f: f}
	}})
	return r, c
}

func request(dir string, r *Registry, l *ledger) Request {
	return Request{Provider: "faulty", HostDir: dir, RunID: "20261020_130000_abcdef", Tool: "fix", Record: l.record, Warn: l.warn, Registry: r}
}

// TestFSIsReadOnly_TS_07_18 verifies TS-07-18:
// FS(ctx) is read-only, maps a missing path to fs.ErrNotExist and stats a
// directory as a directory.
// Verifies: 07-REQ-3.1
func TestFSIsReadOnly_TS_07_18(t *testing.T) {
	dir := newRepo(t, 1, "main")
	commitFiles(t, dir, map[string][]byte{"README.md": []byte("# readme\n"), "src/main.go": []byte("package main\n")})
	s := open(t, Default, "local", dir, &ledger{})
	fsys := s.FS(context.Background())

	for _, name := range []string{"missing", "src/missing", "src/missing/deeper", "README.md/under"} {
		if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want an error that is fs.ErrNotExist", name, err)
		}
		if _, err := fs.ReadFile(fsys, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("ReadFile(%q) = %v, want an error that is fs.ErrNotExist", name, err)
		}
	}
	for _, name := range []string{".", "src"} {
		if fi, err := fs.Stat(fsys, name); err != nil || !fi.IsDir() {
			t.Errorf("Stat(%q) = %v, %v, want a directory", name, fi, err)
		}
	}
	fi, err := fs.Stat(fsys, "README.md")
	if err != nil || fi.IsDir() || fi.Size() != int64(len("# readme\n")) || fi.Name() != "README.md" {
		t.Errorf("Stat(README.md) = %+v, %v, want a file of 9 bytes", fi, err)
	}
	if got, err := fs.ReadFile(fsys, "README.md"); err != nil || string(got) != "# readme\n" {
		t.Errorf("ReadFile(README.md) = %q, %v", got, err)
	}
	if got, err := fs.ReadFile(fsys, "src/main.go"); err != nil || string(got) != "package main\n" {
		t.Errorf("ReadFile(src/main.go) = %q, %v", got, err)
	}
	entries, err := fs.ReadDir(fsys, "src")
	if err != nil || len(entries) != 1 || entries[0].Name() != "main.go" || entries[0].IsDir() {
		t.Errorf("ReadDir(src) = %v, %v", entries, err)
	}
	root, err := fs.ReadDir(fsys, ".")
	var names []string
	for _, e := range root {
		names = append(names, e.Name()+map[bool]string{true: "/", false: ""}[e.IsDir()])
	}
	if err != nil || !slices.Equal(names, []string{".git/", "README.md", "file0.txt", "src/"}) {
		t.Errorf("ReadDir(.) = %v, %v, want the entries of the tree, sorted, directories as such", names, err)
	}
	if _, err := fs.ReadFile(fsys, "src"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile of a directory = %v, want an error that is not fs.ErrNotExist", err)
	}
	for _, name := range []string{"../x", "/abs", "a//b", "src/../README.md", ""} {
		if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Stat(%q) = %v, want fs.ErrInvalid", name, err)
		}
	}

	// A view that offers no write.
	if _, ok := fsys.(interface {
		WriteFile(string, []byte, fs.FileMode) error
	}); ok {
		t.Error("the FS has a write method")
	}
	f, err := fsys.Open("README.md")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, ok := f.(io.Writer); ok {
		t.Error("an open file has a write method")
	}
	if b, err := io.ReadAll(f); err != nil || string(b) != "# readme\n" {
		t.Errorf("reading an open file: %q, %v", b, err)
	}
}

// TestFSReadsTheExactBytes guards 07-REQ-3.1: what the host reads through the
// view is what is in the box, byte for byte, whatever the read tool would have
// made of it (it shows lines, 2000 at most, 50 KB at most, with U+FFFD for what
// is not UTF-8): a payload is hashed over what is read.
func TestFSReadsTheExactBytes(t *testing.T) {
	long := bytes.Repeat([]byte("x"), 120_000)
	var many bytes.Buffer
	for i := range 5000 {
		fmt.Fprintf(&many, "line %d\n", i)
	}
	files := map[string][]byte{
		"lf.txt":        []byte("one\ntwo\n"),
		"no-newline":    []byte("one\ntwo"),
		"empty":         {},
		"blank-lines":   []byte("\n\n\n"),
		"crlf.txt":      []byte("one\r\ntwo\r\n"),
		"latin1.txt":    {'c', 'a', 'f', 0xe9, '\n'},
		"binary.bin":    {0, 1, 2, 0xff, 0xfe, '\n', 0},
		"one-long-line": long,
		"many-lines":    many.Bytes(),
		"ünï/файл.txt":  []byte("unicode name\n"),
	}
	dir := newRepo(t, 1, "main")
	commitFiles(t, dir, files)
	s := open(t, Default, "local", dir, &ledger{})
	fsys := s.FS(context.Background())

	for name, want := range files {
		got, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Errorf("ReadFile(%q): %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("ReadFile(%q) = %d bytes %.40q, want %d bytes %.40q", name, len(got), got, len(want), want)
		}
		if fi, err := fs.Stat(fsys, name); err != nil || fi.Size() != int64(len(want)) {
			t.Errorf("Stat(%q) = %v, %v, want a size of %d", name, fi, err, len(want))
		}
	}
}

// TestWriteFileOnlyUnderAgentFox_TS_07_19 verifies TS-07-19:
// WriteFile refuses invalid paths and paths outside .agent-fox/ with an
// internal error and writes nothing.
// Verifies: 07-REQ-3.2
func TestWriteFileOnlyUnderAgentFox_TS_07_19(t *testing.T) {
	dir := newRepo(t, 1, "main")
	commitFiles(t, dir, map[string][]byte{"src/main.go": []byte("package main\n")})
	s := open(t, Default, "local", dir, &ledger{})
	ctx := context.Background()

	for _, rel := range []string{"../x", "/abs", "src/main.go", ".agent-fox/../x", ".agent-fox", "", ".agent-fox/", "a/.agent-fox/x", ".agent-fox/a/../../x"} {
		err := s.WriteFile(ctx, rel, []byte("data"))
		var f *toolio.Failure
		if err == nil || errors.As(err, &f) {
			t.Errorf("WriteFile(%q) = %v, want an internal error that is not a toolio.Failure", rel, err)
		}
	}
	fsys := s.FS(ctx)
	if got, _ := fs.ReadFile(fsys, "src/main.go"); string(got) != "package main\n" {
		t.Errorf("src/main.go = %q: a refused write changed the tree", got)
	}
	if _, err := fs.Stat(fsys, "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a refused write made x: %v", err)
	}
	if err := s.WriteFile(ctx, ".agent-fox/run/r/brief.md", []byte("# brief\n")); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadFile(fsys, ".agent-fox/run/r/brief.md"); err != nil || string(got) != "# brief\n" {
		t.Errorf("the run file reads back as %q, %v", got, err)
	}
	// A replacement is a whole file, and so is a large one, and the host's
	// checkout never gets any of it.
	big := bytes.Repeat([]byte(`{"k":"v"}`+"\n"), 200_000)
	if err := s.WriteFile(ctx, ".agent-fox/run/r/brief.md", big); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadFile(fsys, ".agent-fox/run/r/brief.md"); err != nil || !bytes.Equal(got, big) {
		t.Errorf("a large file reads back as %d bytes, %v; want %d", len(got), err, len(big))
	}
	if err := s.WriteFile(ctx, ".agent-fox/run/r/bad.bin", []byte{0xff, 0xfe}); err == nil {
		t.Error("WriteFile took bytes that are not text, which the write tool would have changed")
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox", "run", "r")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteFile reached the host checkout: %v", err)
	}
}

// TestExportWritesTheEditsWithoutGit_TS_07_20 verifies TS-07-20:
// Export writes the brain's edits to an af-export directory under the temp
// directory, without .git.
// Verifies: 07-REQ-3.3
func TestExportWritesTheEditsWithoutGit_TS_07_20(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}
	s := open(t, Default, "local", dir, l)
	if r := callTool(t, s, "write_file", map[string]any{"path": "new.txt", "content": "an edit\n"}); !r.OK {
		t.Fatal(r.Error)
	}
	if err := s.WriteFile(context.Background(), ".agent-fox/run/r/run.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}

	out, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(out) })

	if !strings.HasPrefix(filepath.Base(out), "af-export-20261020_120000_abcdef-") {
		t.Errorf("the export is in %s, want af-export-<run_id>-*", out)
	}
	if rel, err := filepath.Rel(os.TempDir(), out); err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("the export %s is not under the temporary directory %s", out, os.TempDir())
	}
	if within, err := filepath.Rel(dir, out); err == nil && !strings.HasPrefix(within, "..") {
		t.Errorf("the export %s is inside the checkout %s", out, dir)
	}
	if got, err := os.ReadFile(filepath.Join(out, "new.txt")); err != nil || string(got) != "an edit\n" {
		t.Errorf("new.txt in the export = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(out, "file0.txt")); err != nil || string(got) != "content 0\n" {
		t.Errorf("file0.txt in the export = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export has a .git: %v", err)
	}
	var exported struct{ Path string }
	l.last(t, "box_export", &exported)
	if exported.Path != out {
		t.Errorf("box_export says %q, want %q", exported.Path, out)
	}
}

// TestExportThatCarriesGitIsRemoved_TS_07_21 verifies TS-07-21:
// an export that carries .git is removed and fails as sandbox.
// Verifies: 07-REQ-3.4
func TestExportThatCarriesGitIsRemoved_TS_07_21(t *testing.T) {
	dir := newRepo(t, 1, "main")
	f := &faults{exportGit: true}
	r, _ := faultyRegistry(f)
	s := open(t, r, "faulty", dir, &ledger{})

	out, err := s.Export(context.Background())

	fail := asFailure(t, err)
	if fail.Category != toolio.CategorySandbox || !strings.Contains(fail.Message, "export carried .git") {
		t.Errorf("failure = %+v, want sandbox saying the export carried .git", fail)
	}
	if out != "" {
		t.Errorf("Export returned the directory %q with its error", out)
	}
	exports := f.exported()
	if len(exports) != 1 {
		t.Fatalf("the box was asked to export %d times", len(exports))
	}
	if _, err := os.Stat(exports[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export directory %s is still there: %v", exports[0], err)
	}
}

// TestCloseIsIdempotent_TS_07_22 verifies TS-07-22:
// Close is idempotent, removes export directories and records box_close once.
// Verifies: 07-REQ-3.5
func TestCloseIsIdempotent_TS_07_22(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}
	r, c := faultyRegistry(&faults{})
	s, err := Open(context.Background(), request(dir, r, l))
	if err != nil {
		t.Fatal(err)
	}
	d1, err1 := s.Export(context.Background())
	d2, err2 := s.Export(context.Background())
	if err1 != nil || err2 != nil || d1 == d2 {
		t.Fatalf("two exports: %q %v, %q %v", d1, err1, d2, err2)
	}

	first, second, third := s.Close(), s.Close(), s.Close()

	if first != nil || second != nil || third != nil {
		t.Errorf("Close returned %v, %v, %v", first, second, third)
	}
	if c.closes.Load() != 1 || c.creates.Load() != 1 {
		t.Errorf("%d created, %d closed, want 1 and 1", c.creates.Load(), c.closes.Load())
	}
	for _, d := range []string{d1, d2} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the export %s is still there after Close: %v", d, err)
		}
	}
	if l.count("box_close") != 1 {
		t.Errorf("box_close entries = %d, want 1", l.count("box_close"))
	}
}

// withOutcome is what fn of a With does.
type withOutcome int

const (
	returnsNil withOutcome = iota
	returnsError
	panics
	cancelsContext
)

var errFn = errors.New("fn failed")

// runWith runs With with fn doing the outcome, and says what came out of it:
// the error returned, or the value panicked with.
func runWith(t *testing.T, r *Registry, dir string, l *ledger, o withOutcome) (panicked any, err error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() { panicked = recover() }()
	err = With(ctx, request(dir, r, l), func(s *Session) error {
		switch o {
		case returnsError:
			return errFn
		case panics:
			panic("a value")
		case cancelsContext:
			cancel()
			return ctx.Err()
		}
		return nil
	})
	return nil, err
}

// TestWithClosesOnEveryPath_TS_07_23 verifies TS-07-23:
// box.With closes the session after a normal return, an error, a panic and a
// cancelled context.
// Verifies: 07-REQ-3.6
func TestWithClosesOnEveryPath_TS_07_23(t *testing.T) {
	dir := newRepo(t, 1, "main")
	for name, o := range map[string]withOutcome{"nil": returnsNil, "error": returnsError, "panic": panics, "cancel": cancelsContext} {
		t.Run(name, func(t *testing.T) {
			f := &faults{}
			r, c := faultyRegistry(f)
			start := time.Now()

			panicked, err := runWith(t, r, dir, &ledger{}, o)

			if c.creates.Load() != 1 || c.closes.Load() != 1 {
				t.Errorf("%d created, %d closed, want 1 and 1", c.creates.Load(), c.closes.Load())
			}
			switch o {
			case returnsNil:
				if err != nil || panicked != nil {
					t.Errorf("With = %v, panic %v", err, panicked)
				}
			case returnsError:
				if !errors.Is(err, errFn) {
					t.Errorf("With = %v, want fn's error", err)
				}
			case panics:
				if panicked != "a value" {
					t.Errorf("recovered %v, want the value fn panicked with", panicked)
				}
			case cancelsContext:
				if !errors.Is(err, context.Canceled) {
					t.Errorf("With = %v, want the cancellation", err)
				}
				f.mu.Lock()
				calls := slices.Clone(f.closeCalls)
				f.mu.Unlock()
				if len(calls) != 1 {
					t.Fatalf("Close was called %d times", len(calls))
				}
				if call := calls[0]; call.err != nil || !call.hasDeadline || call.deadline.After(start.Add(31*time.Second)) {
					t.Errorf("Close ran with a context that has error %v and deadline %v (%v), want one that is not cancelled and ends within 30 s", call.err, call.deadline, call.hasDeadline)
				}
			}
		})
	}
}

// TestWithClosesOncePerOpen_TS_07_24 verifies TS-07-24:
// for any fn outcome box.With closes once per open and returns the outcome
// unchanged.
// Verifies: 07-REQ-3.6
func TestWithClosesOncePerOpen_TS_07_24(t *testing.T) {
	dir := newRepo(t, 1, "main")
	rng := rand.New(rand.NewPCG(7, 24))
	for i := range 3 {
		r, c := faultyRegistry(&faults{})
		n := 3 + rng.IntN(4)
		for j := range n {
			o := withOutcome(rng.IntN(4))
			panicked, err := runWith(t, r, dir, &ledger{}, o)

			switch {
			case o == returnsNil && (err != nil || panicked != nil),
				o == returnsError && !errors.Is(err, errFn),
				o == panics && panicked != "a value",
				o == cancelsContext && !errors.Is(err, context.Canceled):
				t.Errorf("sequence %d, run %d, outcome %d: With = %v, panic %v", i, j, o, err, panicked)
			}
		}
		if c.creates.Load() != int32(n) || c.closes.Load() != int32(n) {
			t.Errorf("sequence %d: %d created, %d closed, want %d each", i, c.creates.Load(), c.closes.Load(), n)
		}
	}
}

// TestCloseErrorIsAWarning_TS_07_25 verifies TS-07-25:
// a close error becomes a box_close_failed warning and does not change With's
// result.
// Verifies: 07-REQ-3.7
func TestCloseErrorIsAWarning_TS_07_25(t *testing.T) {
	dir := newRepo(t, 1, "main")
	r, _ := faultyRegistry(&faults{closeErr: errors.New("the box would not go")})
	l := &ledger{}

	errNil := With(context.Background(), request(dir, r, l), func(*Session) error { return nil })
	errFnErr := With(context.Background(), request(dir, r, l), func(*Session) error { return errFn })

	if errNil != nil || !errors.Is(errFnErr, errFn) {
		t.Errorf("With returned %v and %v, want nil and fn's error", errNil, errFnErr)
	}
	warned := l.warned("box_close_failed")
	if len(warned) != 2 || !strings.Contains(warned[0].Message, "would not go") {
		t.Errorf("warnings = %+v, want two box_close_failed that carry the error", l.warnings)
	}
}

// TestWithRaisesBoxUnconfinedOnce_TS_07_7 completes TS-07-7 for box.With:
// opening a local box raises box_unconfined once for the run, and a Sync inside
// does not raise it again.
// Verifies: 07-REQ-1.7
func TestWithRaisesBoxUnconfinedOnce_TS_07_7(t *testing.T) {
	dir := newRepo(t, 1, "main")
	l := &ledger{}

	err := With(context.Background(), Request{Provider: "local", HostDir: dir, RunID: "r", Tool: "fix", Record: l.record, Warn: l.warn}, func(s *Session) error {
		return s.Sync(context.Background())
	})

	if err != nil {
		t.Fatal(err)
	}
	if got := l.warned("box_unconfined"); len(got) != 1 || len(l.warnings) != 1 {
		t.Errorf("warnings = %+v, want exactly one box_unconfined", l.warnings)
	}
	if l.count("box_close") != 1 {
		t.Errorf("box_close entries = %d, want 1", l.count("box_close"))
	}
}

// TestFSNeverFollowsLinks guards 07-REQ-3.1: a link in the box is not followed
// by what the host reads, whether it leads out of the workspace or not, whatever
// the provider does with the rest of the machine: a payload may cite one.
func TestFSNeverFollowsLinks(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("the host's secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := newRepo(t, 1, "main")
	s := open(t, Default, "local", dir, &ledger{})
	inBox(t, s, "ln -s '"+secret+"' escape && ln -s file0.txt inside && ln -s ../../.. up")
	fsys := s.FS(context.Background())

	for _, name := range []string{"escape", "inside"} {
		data, err := fs.ReadFile(fsys, name)
		if len(data) > 0 || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("ReadFile(%q) = %q, %v, want no content and fs.ErrPermission", name, data, err)
		}
	}
	for _, name := range []string{"escape", "inside", "up"} {
		if _, err := fs.Stat(fsys, name); err != nil {
			t.Errorf("Stat(%q): %v, want the link to exist", name, err)
		}
	}
	// A link to a directory lists as one, and is not listed when it leads out.
	if entries, err := fs.ReadDir(fsys, "up"); err == nil || len(entries) > 0 {
		t.Errorf("ReadDir of a link out of the box = %v, %v, want an error", entries, err)
	}
}
