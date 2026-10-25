package box

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

func TestHere_ServesTheDirectoryWhereItIs(t *testing.T) {
	dir := plainDir(t, map[string]string{"main.go": "package main\n", "sub/util.go": "package sub\n"})
	before := testutil.Files(t, dir)
	l := &ledger{}

	s := openHere(t, dir, l)

	if s.HostDir() != dir || s.Root() != dir || s.Provider() != "here" || s.Network() != NetworkHost || s.Image() != "" || s.Tool() != "triage" {
		t.Errorf("session: host %q, root %q, provider %q, network %q, image %q, tool %q", s.HostDir(), s.Root(), s.Provider(), s.Network(), s.Image(), s.Tool())
	}
	var names []string
	for _, tool := range s.Tools() {
		names = append(names, tool.Name)
	}
	for _, want := range []string{"read_file", "list_files", "write_file", "execute"} {
		if !slices.Contains(names, want) {
			t.Errorf("tools = %v, want %s among them", names, want)
		}
	}
	if got := inBox(t, s, "pwd; cat main.go"); got != dir+"\npackage main" {
		t.Errorf("a command in place says %q, want it run in the directory", got)
	}
	if err := s.WriteFile(context.Background(), ".agent-fox/run/x/note.txt", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got := testutil.ReadFile(t, filepath.Join(dir, ".agent-fox", "run", "x", "note.txt")); got != "hello" {
		t.Errorf("the run file in the directory holds %q", got)
	}
	if err := s.WriteFile(context.Background(), "main.go", []byte("x")); err == nil {
		t.Error("WriteFile wrote outside .agent-fox/")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if after := testutil.Files(t, dir, agentdir.Name); !maps.Equal(after, before) {
		t.Errorf("the directory changed outside .agent-fox/: %v", after)
	}
	if got := l.entries(); !slices.Equal(got, []string{"box_open", "box_close"}) {
		t.Errorf("ledger = %v, want box_open and box_close, and no box_sync", got)
	}
	if opened := l.last(t, "box_open"); opened["provider"] != "here" || opened["network"] != NetworkHost || opened["placement"] != "box" {
		t.Errorf("box_open = %v", opened)
	}
}

func TestHere_WarnsOnceThatTheTreeIsReadInPlace(t *testing.T) {
	l := &ledger{}

	openHere(t, plainDir(t, nil), l)

	if len(l.warnings) != 1 || l.warnings[0].Code != "box_unconfined" || !strings.Contains(l.warnings[0].Message, "in place") || !strings.Contains(l.warnings[0].Message, ".git/config") {
		t.Errorf("warnings = %+v, want one box_unconfined that says what the tools reach", l.warnings)
	}
}

func TestHere_WantsADirectory(t *testing.T) {
	dir := plainDir(t, map[string]string{"main.go": "package main\n"})
	for name, path := range map[string]string{"a file": filepath.Join(dir, "main.go"), "a missing path": filepath.Join(dir, "nowhere")} {
		s, err := Here(context.Background(), Request{HostDir: path})

		if f := asFailure(t, err); s != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "--dir "+path) {
			t.Errorf("%s: Here = %v, %+v, want no session and a usage failure", name, s, f)
		}
	}
}

func TestHere_LeavesGitAloneAndHasNothingToSync(t *testing.T) {
	dir := hostRepo(t, nil)
	testutil.Git(t, dir, "remote", "add", "origin", "https://user:SECRET@example.com/x.git")
	before := testutil.Files(t, dir)
	s := openHere(t, dir, &ledger{})

	err := s.Sync(context.Background())

	if f := asFailure(t, err); f.Category != toolio.CategoryInternal {
		t.Errorf("Sync in place = %+v, want an internal failure", f)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := testutil.Files(t, dir); !maps.Equal(after, before) {
		t.Error("the repository changed, its .git included")
	}
}

func TestHere_ExportsOneRegularFileAndNoLink(t *testing.T) {
	outside := testutil.WriteFile(t, t.TempDir(), "secret.txt", "the secret")
	dir := plainDir(t, map[string]string{"main.go": "package main\n"})
	if err := os.Symlink(outside, filepath.Join(dir, "leak.txt")); err != nil {
		t.Skipf("no symbolic links: %v", err)
	}
	s := openHere(t, dir, &ledger{})
	fsys := s.FS(context.Background())

	if data, err := fs.ReadFile(fsys, "leak.txt"); len(data) > 0 || !errors.Is(err, fs.ErrPermission) {
		t.Errorf("ReadFile through a link out of the directory = %q, %v, want fs.ErrPermission", data, err)
	}
	if _, err := s.Export(context.Background()); err == nil || !strings.Contains(err.Error(), "exports files only") {
		t.Errorf("Export of a tree in place = %v, want it refused", err)
	}
	dst := t.TempDir()
	for name, err := range map[string]error{
		"a path out of it":  s.sb.Export(context.Background(), "../x", sandbox.LocalDir{Path: dst}),
		"a missing file":    s.sb.Export(context.Background(), "nowhere", sandbox.LocalDir{Path: dst}),
		"no directory":      s.sb.Export(context.Background(), "main.go", sandbox.TarOut{}),
		"a directory to go": s.sb.Export(context.Background(), "main.go", sandbox.LocalDir{Path: filepath.Join(dst, "missing")}),
	} {
		if err == nil {
			t.Errorf("an export of %s succeeded", name)
		}
	}
}
