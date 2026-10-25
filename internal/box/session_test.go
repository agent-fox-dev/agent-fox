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
)

func TestSession_WorkInTheBoxLeavesTheHostUntouched(t *testing.T) {
	dir := hostRepo(t, map[string]string{"file0.txt": "content 0\n", "src/main.go": "package main\n"})
	before := testutil.Files(t, dir)
	s := openLocal(t, dir, &ledger{})

	for path, content := range map[string]string{"new.txt": "from the box\n", "src/main.go": "package changed\n", "deep/er/x.txt": "x\n"} {
		if r := callTool(t, s, "write_file", map[string]any{"path": path, "content": content}); !r.OK {
			t.Fatalf("write_file %s: %s %s", path, r.Error, r.Detail)
		}
	}
	inBox(t, s, "git add -A && git commit -q -m 'inside the box' && git checkout -q -b elsewhere")
	if err := s.WriteFile(context.Background(), ".agent-fox/run/r/brief.md", []byte("# brief\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Export(context.Background()); err != nil {
		t.Fatal(err)
	}

	if after := testutil.Files(t, dir); !maps.Equal(after, before) {
		t.Errorf("the host checkout changed, its .git included:\nbefore %v\nafter  %v", slices.Sorted(maps.Keys(before)), slices.Sorted(maps.Keys(after)))
	}
}

func TestSync_MakesTheBoxHoldWhatTheHostHolds(t *testing.T) {
	dir := hostRepo(t, nil)
	l := &ledger{}
	s := openLocal(t, dir, l)
	for path, content := range map[string]string{"extra.txt": "only in the box\n", "file0.txt": "box edit\n"} {
		if r := callTool(t, s, "write_file", map[string]any{"path": path, "content": content}); !r.OK {
			t.Fatalf("write_file %s: %s", path, r.Error)
		}
	}
	inBox(t, s, "git add -A && git commit -q -m inside")
	// The host's branch moves twice within the second of the first sync, and a
	// branch file is as long as a hash: the transfer alone would take it for unchanged.
	testutil.Commit(t, dir, "on the host")
	head := testutil.Commit(t, dir, "on the host again")

	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := inBox(t, s, "cat file0.txt; ls extra.txt 2>&1; true"); !strings.HasPrefix(got, "content 0\n") || !strings.Contains(got, "No such file") {
		t.Errorf("the box holds %q, want the host's file0.txt and no extra.txt", got)
	}
	if got := inBox(t, s, "git rev-parse HEAD; git status --porcelain; git log --oneline"); !strings.HasPrefix(got, head+"\n") || strings.Contains(got, "inside") || strings.Contains(got, "??") {
		t.Errorf("the box's git says\n%s\nwant the host's HEAD %s, clean", got, head)
	}
	short, _ := l.last(t, "box_sync")["head"].(string)
	if got := l.entries(); !slices.Equal(got, []string{"box_open", "box_sync", "box_sync"}) || short == "" || !strings.HasPrefix(head, short) {
		t.Errorf("ledger = %v with the last box_sync head %q, want a second box_sync of %s", got, short, head)
	}
}

func TestSync_TakesTheCredentialsOutOfTheBoxsGitConfiguration(t *testing.T) {
	dir := hostRepo(t, nil)
	testutil.Git(t, dir, "remote", "add", "origin", "https://user:SECRET1@example.com/x.git")
	testutil.Git(t, dir, "config", "remote.origin.pushurl", "https://user:SECRET1@example.com/x.git")
	testutil.Git(t, dir, "config", "http.extraheader", "Authorization: bearer SECRET2")
	testutil.Git(t, dir, "config", "credential.helper", "store --file=/nowhere SECRET3")
	testutil.Git(t, dir, "config", "url.https://tok:SECRET4@example.com/.insteadOf", "https://example.com/")
	testutil.Git(t, dir, "config", "core.sshCommand", "ssh -i SECRET5")
	testutil.Git(t, dir, "config", "branch.main.note", "kept")
	s := openLocal(t, dir, &ledger{})
	check := func(when string) {
		t.Helper()
		got := inBox(t, s, "git config --local --list")
		if strings.Contains(got, "SECRET") || !strings.Contains(got, "branch.main.note=kept") {
			t.Errorf("%s: the box's git configuration is\n%s\nwant no secret and the rest kept", when, got)
		}
	}

	check("after Open")
	testutil.Git(t, dir, "remote", "add", "second", "https://other:SECRET6@example.com/y.git")
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	check("after Sync")

	if got := testutil.Git(t, dir, "config", "--local", "--list"); !strings.Contains(got, "SECRET1") || !strings.Contains(got, "SECRET6") {
		t.Errorf("the host's configuration lost its remotes:\n%s", got)
	}
}

func TestSync_SendsTheTrackedFilesThatARuleIgnores(t *testing.T) {
	dir := hostRepo(t, map[string]string{"gen/api.pb.go": "v1\n", "gen/deep/x.go": "deep\n"})
	testutil.WriteFile(t, dir, ".gitignore", "gen/\n.env\n")
	testutil.Commit(t, dir, "ignore the generated code")
	testutil.WriteFile(t, dir, ".env", "KEY=1\n")
	s := openLocal(t, dir, &ledger{})

	if got := inBox(t, s, "cat gen/api.pb.go gen/deep/x.go; git status --porcelain"); got != "v1\ndeep" {
		t.Errorf("the box holds %q, want the tracked ignored files and a clean status", got)
	}
	if got := inBox(t, s, "test -e .env || echo none"); got != "none" {
		t.Errorf("an ignored file that git does not track went in: %q", got)
	}

	testutil.WriteFile(t, dir, "gen/api.pb.go", "v2 is longer\n")
	if err := s.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := inBox(t, s, "cat gen/api.pb.go"); got != "v2 is longer" {
		t.Errorf("after a Sync gen/api.pb.go = %q, want the host's", got)
	}
}

func TestSync_OfARepositoryWithNoCommitRecordsNoHead(t *testing.T) {
	testutil.Require(t, "git", "sh")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, dir, "init", "-q")
	l := &ledger{}

	openLocal(t, dir, l)

	if head := l.last(t, "box_sync")["head"]; head != "" {
		t.Errorf("box_sync head = %v, want none", head)
	}
}

func TestWriteFile_WritesTextUnderAgentFoxOnly(t *testing.T) {
	dir := hostRepo(t, map[string]string{"src/main.go": "package main\n"})
	s := openLocal(t, dir, &ledger{})
	ctx := context.Background()

	for _, rel := range []string{"../x", "/abs", "src/main.go", "x", ".agent-fox/../x", ".agent-fox", "", ".agent-fox/", "a/.agent-fox/x", ".agent-fox/a/../../x"} {
		var f *toolio.Failure
		if err := s.WriteFile(ctx, rel, []byte("data")); err == nil || errors.As(err, &f) {
			t.Errorf("WriteFile(%q) = %v, want an internal error, not a failure", rel, err)
		}
	}
	if err := s.WriteFile(ctx, ".agent-fox/run/r/bad.bin", []byte{0xff, 0xfe}); err == nil || !strings.Contains(err.Error(), "not valid UTF-8") {
		t.Errorf("WriteFile of bytes that are not text = %v, want it refused", err)
	}
	if got := inBox(t, s, "cat src/main.go; ls x .agent-fox 2>&1; true"); !strings.HasPrefix(got, "package main\n") || strings.Count(got, "No such file") != 2 {
		t.Errorf("the refused writes changed the box:\n%s", got)
	}

	big := strings.Repeat(`{"k":"v"}`+"\n", 200_000)
	for _, content := range []string{"# brief\n", big} {
		if err := s.WriteFile(ctx, ".agent-fox/run/r/brief.md", []byte(content)); err != nil {
			t.Fatal(err)
		}
		if got, err := fs.ReadFile(s.FS(ctx), ".agent-fox/run/r/brief.md"); err != nil || string(got) != content {
			t.Errorf("the run file reads back as %d bytes, %v; want %d", len(got), err, len(content))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-fox")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("WriteFile reached the host checkout: %v", err)
	}
}

func TestExport_TheTreeComesOutWithoutGitAndGoesOnClose(t *testing.T) {
	dir := hostRepo(t, nil)
	l := &ledger{}
	s := openLocal(t, dir, l)
	if r := callTool(t, s, "write_file", map[string]any{"path": "docs/edit.md", "content": "an edit\n"}); !r.OK {
		t.Fatal(r.Error)
	}

	out, err := s.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if rel, err := filepath.Rel(os.TempDir(), out); err != nil || strings.HasPrefix(rel, "..") || !strings.HasPrefix(filepath.Base(out), "af-export-"+runID+"-") {
		t.Errorf("the export is %s, want af-export-%s-* under %s", out, runID, os.TempDir())
	}
	if _, err := os.Lstat(filepath.Join(out, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export has a .git: %v", err)
	}
	if got := testutil.Files(t, out, agentdir.Name); !maps.Equal(got, map[string]string{"file0.txt": "content 0\n", "docs/edit.md": "an edit\n"}) {
		t.Errorf("the export holds %v, want the tree with the edit", got)
	}
	if l.last(t, "box_export")["path"] != out {
		t.Errorf("box_export = %v, want the path %s", l.last(t, "box_export"), out)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export is still there after Close: %v", err)
	}
	if got := l.entries(); !slices.Equal(got, []string{"box_open", "box_sync", "box_export", "box_close"}) {
		t.Errorf("ledger = %v, want box_open, box_sync, box_export, box_close", got)
	}
}

func TestExport_AnExportThatCarriesGitIsRemovedAndStopsTheRun(t *testing.T) {
	f := &fake{exportGit: true}
	s := f.open(t, hostRepo(t, nil), &ledger{})

	out, err := s.Export(context.Background())

	if fail := asFailure(t, err); out != "" || fail.Category != toolio.CategorySandbox || fail.Retryable || !strings.Contains(fail.Message, "export carried .git") {
		t.Errorf("Export = %q, %+v, want no directory and a sandbox failure that says .git came out", out, fail)
	}
	if exported := f.exported(); len(exported) != 1 {
		t.Fatalf("the box exported %d times, want 1", len(exported))
	} else if _, err := os.Stat(exported[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export %s is still there: %v", exported[0], err)
	}
}

func TestClose_IsIdempotentAndRemovesEveryExport(t *testing.T) {
	f := &fake{}
	l := &ledger{}
	req := request("fake", hostRepo(t, nil), registry(f.entry()), l)
	req.RunID = "run/1"
	s, err := Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	d1, err1 := s.Export(context.Background())
	d2, err2 := s.Export(context.Background())
	if err1 != nil || err2 != nil || d1 == d2 || !strings.HasPrefix(filepath.Base(d1), "af-export-run_1-") {
		t.Fatalf("two exports: %q %v, %q %v; want two af-export-run_1-* directories", d1, err1, d2, err2)
	}

	errs := []error{s.Close(), s.Close(), s.Close()}

	if slices.ContainsFunc(errs, func(err error) bool { return err != nil }) || f.closes.Load() != 1 || f.providerCloses.Load() != 1 {
		t.Errorf("Close = %v, box closed %d times, provider %d; want nil and once", errs, f.closes.Load(), f.providerCloses.Load())
	}
	for _, d := range []string{d1, d2} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the export %s is still there: %v", d, err)
		}
	}
	if got := l.entries(); slices.Index(got, "box_close") != len(got)-1 || strings.Count(strings.Join(got, " "), "box_close") != 1 {
		t.Errorf("ledger = %v, want one box_close, last", got)
	}
	if _, err := s.Export(context.Background()); err == nil {
		t.Error("Export on a closed session succeeded")
	}
}

func TestClose_ThatFailsSaysWhy(t *testing.T) {
	s := (&fake{closeErr: errors.New("the box would not go")}).open(t, hostRepo(t, nil), &ledger{})

	if err := s.Close(); err == nil || !strings.Contains(err.Error(), "would not go") {
		t.Errorf("Close = %v, want the box's error", err)
	}
}

func TestExport_ThatCloseOvertakesIsRemoved(t *testing.T) {
	f := &fake{}
	l := &ledger{}
	s := f.open(t, hostRepo(t, nil), l)
	f.afterExport = func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	}

	out, err := s.Export(context.Background())

	if err == nil {
		t.Errorf("Export = %q after the session was closed, want an error", out)
	}
	if exported := f.exported(); len(exported) != 1 {
		t.Fatalf("the box exported %d times, want 1", len(exported))
	} else if _, err := os.Stat(exported[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the export %s was left behind", exported[0])
	}
	if slices.Contains(l.entries(), "box_export") {
		t.Errorf("ledger = %v, want no box_export for an export thrown away", l.entries())
	}
}

func TestSession_RemembersTheProgramsProbedInIt(t *testing.T) {
	s := openHere(t, plainDir(t, nil), &ledger{})

	before := s.Probed("go")
	s.MarkProbed("go")

	if before || !s.Probed("go") || s.Probed("make") {
		t.Errorf("Probed(go) %v then %v, Probed(make) %v; want false, true, false", before, s.Probed("go"), s.Probed("make"))
	}
}
