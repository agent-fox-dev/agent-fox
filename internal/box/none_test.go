package box

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// openNone opens a box with the none provider over the checkout.
func openNone(t *testing.T, dir string, l *ledger, env map[string]string) *Session {
	t.Helper()
	s, err := Open(context.Background(), Request{
		Provider: "none", HostDir: dir, RunID: "20261021_140000_abcdef", Tool: "fix", Record: l.record, Warn: l.warn,
		Env: env, Network: NetworkHost, Placement: "checkout",
	})
	if err != nil {
		t.Fatalf("Open(none): %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// TestNoneReturnsAnInPlaceSessionAndOtherProvidersDoNot_TS_10_24 verifies TS-10-24:
// none gives a session over the checkout, whose Root is the checkout, whose spec
// says network host and holds the environment of the request; local gives one that
// is not in place and whose Root is the workspace of the box.
// Verifies: 10-REQ-5.1
func TestNoneReturnsAnInPlaceSessionAndOtherProvidersDoNot_TS_10_24(t *testing.T) {
	dir := newRepo(t, 1, "main")
	env := map[string]string{"ANTHROPIC_API_KEY": "sk-key"}

	l := &ledger{}
	none := openNone(t, dir, l, env)
	var opened map[string]any
	l.last(t, "box_open", &opened)
	if opened["provider"] != "none" || opened["network"] != "host" || opened["placement"] != "checkout" || opened["image"] != "" {
		t.Errorf("box_open = %v, want none, no image, network host and placement checkout", opened)
	}

	if !none.InPlace() || none.Root() != dir || none.HostDir() != dir || none.Provider() != "none" {
		t.Errorf("none: InPlace %v, Root %q, HostDir %q, provider %q; want in place over %q", none.InPlace(), none.Root(), none.HostDir(), none.Provider(), dir)
	}
	if none.Network() != NetworkHost {
		t.Errorf("none: Network() = %q, want host", none.Network())
	}
	if spec := none.Spec(); spec.Env["ANTHROPIC_API_KEY"] != "sk-key" || len(spec.Env) != 1 || spec.Workdir != dir {
		t.Errorf("none: Spec() = %+v, want the environment of the request and the checkout as workdir", spec)
	}

	boxed := open(t, Default, "local", dir, &ledger{})
	if boxed.InPlace() {
		t.Error("local: InPlace() is true")
	}
	if boxed.Root() == "" || boxed.Root() == dir || boxed.Root() != inBox(t, boxed, "pwd") {
		t.Errorf("local: Root() = %q, want the workspace as a process in the box sees it (%q)", boxed.Root(), inBox(t, boxed, "pwd"))
	}

	// A directory that is not the top level of a work tree is refused, as for any provider.
	_, err := Open(context.Background(), Request{Provider: "none", HostDir: t.TempDir(), RunID: "r", Tool: "fix"})
	if f := asFailure(t, err); f.Category != toolio.CategoryUsage {
		t.Errorf("none over a directory that is no checkout: %+v, want usage", f)
	}
}

// TestAnInPlaceSessionServesToolsOverTheCheckoutAndAReadOnlyFS_TS_10_25 verifies TS-10-25:
// the tools see the files of the checkout, the FS view reads them and cannot write,
// and nothing leads out of the checkout through it.
// Verifies: 10-REQ-5.2
func TestAnInPlaceSessionServesToolsOverTheCheckoutAndAReadOnlyFS_TS_10_25(t *testing.T) {
	dir := newRepo(t, 1, "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "leak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := openNone(t, dir, &ledger{}, nil)

	if r := callTool(t, s, "read_file", map[string]any{"path": "a.txt"}); !r.OK || !strings.Contains(r.LLMText(), "alpha") {
		t.Errorf("the read tool = %+v, want the file of the checkout", r)
	}
	if out := inBox(t, s, "pwd"); out != dir {
		t.Errorf("the shell tool runs in %q, want the checkout %q", out, dir)
	}

	// What the tools keep in the temporary directory is theirs; the view makes nothing there.
	beforeView, _ := os.ReadDir(tmp)
	fsys := s.FS(context.Background())
	if data, err := fs.ReadFile(fsys, "a.txt"); err != nil || string(data) != "alpha\n" {
		t.Errorf("ReadFile = %q, %v", data, err)
	}
	if _, err := fs.Stat(fsys, "missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing) = %v, want fs.ErrNotExist", err)
	}
	if _, ok := fsys.(interface {
		WriteFile(string, []byte, fs.FileMode) error
	}); ok {
		t.Error("the FS view has a WriteFile")
	}
	f, err := fsys.Open("a.txt")
	if err != nil {
		t.Fatal(err)
	}
	if w, ok := f.(io.Writer); ok {
		// The file is the checkout's own, opened for reading: writing to it fails.
		if _, err := w.Write([]byte("changed")); err == nil {
			t.Error("a write through the FS view succeeded")
		}
	}
	_ = f.Close()
	if data, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(data) != "alpha\n" {
		t.Errorf("a.txt = %q after the reads, want it unchanged", data)
	}
	if data, err := fs.ReadFile(fsys, "alias"); err != nil || string(data) != "alpha\n" {
		t.Errorf("ReadFile(alias) = %q, %v, want the file of the checkout that the link names: the view is the checkout itself, through os.Root", data, err)
	}
	if _, err := fs.ReadFile(fsys, "leak"); err == nil {
		t.Error("the FS view followed a link that leads out of the checkout")
	}
	if _, err := fsys.Open("../outside.txt"); err == nil {
		t.Error("the FS view opened a path outside the checkout")
	}
	if afterView, _ := os.ReadDir(tmp); len(afterView) != len(beforeView) {
		t.Errorf("reading the checkout made %d entries in the temporary directory: the view is the checkout, not a copy", len(afterView)-len(beforeView))
	}
}

// TestAnInPlaceWriteFileWritesOnlyUnderAgentFox_TS_10_26 verifies TS-10-26:
// run files are written atomically under .agent-fox/, a path elsewhere is refused
// and writes nothing, and a .gitignore that is there is not overwritten.
// Verifies: 10-REQ-5.3
func TestAnInPlaceWriteFileWritesOnlyUnderAgentFox_TS_10_26(t *testing.T) {
	dir := newRepo(t, 1, "main")
	custom := filepath.Join(dir, ".agent-fox", ".gitignore")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("custom\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := openNone(t, dir, &ledger{}, nil)
	ctx := context.Background()

	if err := s.WriteFile(ctx, ".agent-fox/run/x/brief.md", []byte("# brief\n")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, ".agent-fox/run/x/brief.md")); err != nil || string(data) != "# brief\n" {
		t.Errorf("brief.md = %q, %v", data, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".agent-fox/run/x")); len(entries) != 1 {
		t.Errorf(".agent-fox/run/x holds %d entries, want brief.md alone (no temporary file)", len(entries))
	}
	if err := s.WriteFile(ctx, ".agent-fox/run/x/brief.md", []byte("# second\n")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, ".agent-fox/run/x/brief.md")); string(data) != "# second\n" {
		t.Errorf("brief.md = %q after a second write", data)
	}

	before := treeOf(t, dir, ".git")
	for _, rel := range []string{"README.md", "../x", ".agent-fox/../README.md", "/etc/x", ".agent-fox"} {
		if err := s.WriteFile(ctx, rel, []byte("x")); err == nil {
			t.Errorf("WriteFile(%q) succeeded", rel)
		}
	}
	if err := s.WriteFile(ctx, ".agent-fox/binary", []byte{0xff, 0xfe}); err == nil {
		t.Error("WriteFile of bytes that are not text succeeded")
	}
	if after := treeOf(t, dir, ".git"); !equalTrees(before, after) {
		t.Errorf("a refused write changed the tree:\nbefore %v\nafter  %v", before, after)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "x")); err == nil {
		t.Error("a path with .. wrote outside the checkout")
	}

	if err := s.WriteFile(ctx, ".agent-fox/.gitignore", []byte("*")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(custom); string(data) != "custom\n" {
		t.Errorf(".gitignore = %q, want the bytes that were there", data)
	}

	// Where there is none, it is made.
	fresh := newRepo(t, 1, "main")
	if err := openNone(t, fresh, &ledger{}, nil).WriteFile(ctx, ".agent-fox/.gitignore", []byte("*")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(fresh, ".agent-fox", ".gitignore")); err != nil || string(data) != "*" {
		t.Errorf("a new .gitignore = %q, %v, want the bytes that were written", data, err)
	}
}

// TestAnInPlaceWriteFileDoesNotFollowALinkOutOfTheCheckout guards the write: a link in
// .agent-fox/ that leads out is not written through.
func TestAnInPlaceWriteFileDoesNotFollowALinkOutOfTheCheckout(t *testing.T) {
	dir := newRepo(t, 1, "main")
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".agent-fox"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, ".agent-fox", "run")); err != nil {
		t.Fatal(err)
	}
	s := openNone(t, dir, &ledger{}, nil)

	err := s.WriteFile(context.Background(), ".agent-fox/run/x/brief.md", []byte("planted"))

	if err == nil {
		t.Error("the write went through a link that leads out of the checkout")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("%d entries were written outside the checkout", len(entries))
	}
}

// TestAnInPlaceSyncRecordsTheHeadAndCopiesNothingAndCloseRecordsTheEnd_TS_10_27 verifies
// TS-10-27: Sync notes box_sync with the head and changes nothing, and Close notes
// box_close once.
// Verifies: 10-REQ-5.4
func TestAnInPlaceSyncRecordsTheHeadAndCopiesNothingAndCloseRecordsTheEnd_TS_10_27(t *testing.T) {
	dir := newRepo(t, 2, "main")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	l := &ledger{}
	s := openNone(t, dir, l, nil)
	before := treeOf(t, dir, ".git")

	for range 2 {
		if err := s.Sync(context.Background()); err != nil {
			t.Fatalf("Sync: %v", err)
		}
	}

	if got := l.count("box_sync"); got != 2 {
		t.Errorf("%d box_sync entries, want one for each Sync", got)
	}
	var entry struct{ Head string }
	l.last(t, "box_sync", &entry)
	if want := git(t, dir, "rev-parse", "--short", "HEAD"); entry.Head != want {
		t.Errorf("box_sync head = %q, want %q", entry.Head, want)
	}
	if after := treeOf(t, dir, ".git"); !equalTrees(before, after) {
		t.Errorf("Sync changed the tree:\nbefore %v\nafter  %v", before, after)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("Sync made %d entries in the temporary directory", len(entries))
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("the second Close = %v, want nil", err)
	}
	if got := l.count("box_close"); got != 1 {
		t.Errorf("%d box_close entries, want one", got)
	}
	if _, err := fs.ReadFile(s.FS(context.Background()), "file0.txt"); err == nil {
		t.Error("the FS view still reads after Close")
	}
}

// TestExportOfAnInPlaceSessionIsAnInternalError_TS_10_28 verifies TS-10-28: Export says
// "in place: nothing to export", as an internal failure, and copies nothing.
// Verifies: 10-REQ-5.5
func TestExportOfAnInPlaceSessionIsAnInternalError_TS_10_28(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	s := openNone(t, newRepo(t, 1, "main"), &ledger{}, nil)

	dir, err := s.Export(context.Background())

	if dir != "" {
		t.Errorf("Export returned the directory %q", dir)
	}
	f := asFailure(t, err)
	if f.Category != toolio.CategoryInternal || f.Message != "in place: nothing to export" {
		t.Errorf("failure = %+v, want internal, \"in place: nothing to export\"", f)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("Export made %v in the temporary directory", entries)
	}
}

// TestBoxUnconfinedIsRaisedOnceForLocalAndForNone_TS_10_6 verifies TS-10-6: a run that
// opens a local box and one that opens a none box, each running two attempts (a
// sync for the second), raise box_unconfined once; under none the message says that
// the brain works on the checkout itself and that the hook is the only guard.
// Verifies: 10-REQ-1.6
func TestBoxUnconfinedIsRaisedOnceForLocalAndForNone_TS_10_6(t *testing.T) {
	var messages [2]string
	for i, provider := range []string{"local", "none"} {
		l := &ledger{}
		var s *Session
		if provider == "none" {
			s = openNone(t, newRepo(t, 1, "main"), l, nil)
		} else {
			s = open(t, Default, provider, newRepo(t, 1, "main"), l)
		}
		for range 2 { // two attempts of one run
			if err := s.Sync(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		w := l.warned("box_unconfined")
		if len(w) != 1 {
			t.Fatalf("%s: %d box_unconfined warnings, want exactly one", provider, len(w))
		}
		messages[i] = w[0].Message
	}
	if !strings.Contains(messages[0], "local provider") {
		t.Errorf("the local message = %q, want the one that names the local provider", messages[0])
	}
	if !strings.Contains(messages[1], "checkout itself") || !strings.Contains(messages[1], "hook is the only guard") || messages[1] == messages[0] {
		t.Errorf("the none message = %q, want one that says the brain works on the checkout itself and that the hook is the only guard", messages[1])
	}
}

// TestAHereSessionIsNotTheSessionOfNone guards the difference: the session of a
// process that is already in a box refuses a sync, which is still an error.
func TestAHereSessionIsNotTheSessionOfNone(t *testing.T) {
	s, err := Here(context.Background(), Request{HostDir: newRepo(t, 1, "main"), RunID: "r", Tool: "triage"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if s.InPlace() {
		t.Error("a Here session says it is the session of none")
	}
	if s.Root() != s.HostDir() || s.Network() != NetworkHost {
		t.Errorf("Root %q, Network %q", s.Root(), s.Network())
	}
	if err := s.Sync(context.Background()); err == nil {
		t.Error("Sync of a Here session succeeded")
	}
}
