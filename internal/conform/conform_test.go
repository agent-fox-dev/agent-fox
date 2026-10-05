package conform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
)

// newRepo is a real repository with one commit, so that a change can be
// measured the way the pipelines measure one: git's diff against a base.
func newRepo(t *testing.T, files map[string]string) (*gitx.Git, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	ctx := context.Background()
	for _, argv := range [][]string{
		{"git", "init", "-q", "-b", "main"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "commit.gpgsign", "false"},
	} {
		if out, code, err := gitx.ExecRunner(ctx, dir, argv); err != nil || code != 0 {
			t.Fatalf("%v: %v (%d) %s", argv, err, code, out)
		}
	}
	writeFiles(t, dir, files)
	g := gitx.New(dir, gitx.ExecRunner)
	base, err := g.CommitAll(ctx, "chore: initial commit\n")
	if err != nil {
		t.Fatal(err)
	}
	return g, dir, base
}

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// scanChange measures the change in dir since base and scans it.
func scanChange(t *testing.T, g *gitx.Git, dir, base string, today time.Time) []Finding {
	t.Helper()
	ctx := context.Background()
	changed, err := g.ChangedFiles(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	_, patch, err := g.DiffSince(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := g.TrackedFiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return Scan(ctx, ScanInput{Root: dir, Changed: changed, Added: ParseAdded(patch), Tracked: tracked,
		Today: today, MaxFuncLines: 12})
}

func byCheck(fs []Finding) map[string][]Finding {
	out := map[string][]Finding{}
	for _, f := range fs {
		out[f.Check] = append(out[f.Check], f)
	}
	return out
}

func TestParseAddedReadsNewLineNumbers(t *testing.T) {
	patch := "diff --git a/x.go b/x.go\n--- a/x.go\n+++ b/x.go\n@@ -3,0 +4,2 @@ func f() {\n+\ta := 1\n+\tb := 2\n" +
		"@@ -9 +11 @@\n-old\n+new\ndiff --git a/gone.go b/gone.go\n--- a/gone.go\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n"
	got := ParseAdded(patch)
	if got["x.go"][4] != "\ta := 1" || got["x.go"][5] != "\tb := 2" || got["x.go"][11] != "new" || len(got["x.go"]) != 3 {
		t.Errorf("added = %#v", got["x.go"])
	}
	if _, ok := got["gone.go"]; ok {
		t.Error("a deleted file has added lines")
	}
	if !got.Touches("x.go", 1, 4) || got.Touches("x.go", 6, 10) {
		t.Error("Touches does not follow the added lines")
	}
}

// Every structural check, each on the shape that the audited pull requests
// shipped, and each silent on the shape that is fine.
func TestScanFindsWhatTheAuditFound(t *testing.T) {
	g, dir, base := newRepo(t, map[string]string{
		"go.mod":         "module x\n\ngo 1.22\n",
		"pkg/old.go":     "package pkg\n\nfunc Old() int {\n\ta := 1\n\tb := 2\n\tc := a + b\n\td := c * 2\n\te := d - 1\n\tf := e + c\n\tg := f * f\n\th := g + a\n\treturn h\n}\n",
		"docs/adr/01.md": "# ADR\n",
	})
	writeFiles(t, dir, map[string]string{
		"pkg/new.go": "package pkg\n\n" +
			"import \"os\"\n\n" +
			"// Copy repeats Old.\n" +
			"func Copy() int {\n\ta := 1\n\tb := 2\n\tc := a + b\n\td := c * 2\n\te := d - 1\n\tf := e + c\n\tg := f * f\n\th := g + a\n\treturn h\n}\n\n" +
			"func Close(f *os.File) {\n\t// log but don't fail\n\t_ = f.Close()\n\t_ = f.Sync() // ignored: the file is read-only\n}\n\n" +
			"// handled by a later task\n" +
			"func helper() {}\n\n" +
			"func Long() {\n" + strings.Repeat("\tprintln()\n", 14) + "}\n",
		"pkg/new_test.go": "package pkg\n\nimport (\n\t\"os/exec\"\n\t\"testing\"\n\t\"strings\"\n)\n\n" +
			"var _ = strings.TrimSpace\n\n" +
			"func TestNothing(t *testing.T) {\n\t_ = Copy()\n}\n\n" +
			"func TestHelper(t *testing.T) {\n\tcheck(t, Copy())\n}\n\n" +
			"func TestSub(t *testing.T) {\n\tt.Run(\"x\", func(t *testing.T) {\n\t\tif Copy() != 6 {\n\t\t\tt.Errorf(\"no\")\n\t\t}\n\t})\n}\n\n" +
			"func check(t *testing.T, n int) { t.Helper(); _ = n }\n\n" +
			"func TestFixture(t *testing.T) {\n\texec.Command(\"git\", \"init\", \"-q\", t.TempDir())\n" +
			"\texec.Command(\"git\", \"init\", \"-q\", \"-b\", \"main\", t.TempDir())\n\tt.Fatal(\"x\")\n}\n",
		"docs/adr/01.md":       "# ADR\n\nAccepted 2026-10-16.\nWritten 2026-10-05.\n",
		"docs/errata/01_x.md":  "# Erratum\n\nThe spec said exit 3; the code exits 2.\n",
		"docs/errata/02_ok.md": "# Erratum\n\nExits 2: `pkg/new.go:6`, proven by TestHelper.\n",
	})

	got := byCheck(scanChange(t, g, dir, base, time.Date(2026, 10, 5, 15, 0, 0, 0, time.UTC)))

	expect := func(check string, wantN int, refs ...string) {
		t.Helper()
		if len(got[check]) != wantN {
			t.Errorf("%s: %d finding(s), want %d: %+v", check, len(got[check]), wantN, got[check])
			return
		}
		for i, ref := range refs {
			if got[check][i].Ref() != ref {
				t.Errorf("%s finding %d at %s, want %s", check, i, got[check][i].Ref(), ref)
			}
		}
	}
	expect(CheckDuplicate, 1, "pkg/new.go:7")
	expect(CheckDiscardedError, 1, "pkg/new.go:20")
	expect(CheckLaterTask, 1, "pkg/new.go:24")
	expect(CheckUnused, 1, "pkg/new.go:25")
	expect(CheckLongFunction, 1, "pkg/new.go:27")
	expect(CheckImportSuppressor, 1, "pkg/new_test.go:9")
	expect(CheckNoAssertions, 1, "pkg/new_test.go:11")
	expect(CheckGitInitBranch, 1, "pkg/new_test.go:30")
	expect(CheckFutureDate, 1, "docs/adr/01.md:3")
	expect(CheckErrataCitation, 1, "docs/errata/01_x.md")
}

func TestScanRunsGofmtAndVetOnTheTouchedFilesOnly(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not installed")
	}
	g, dir, base := newRepo(t, map[string]string{
		"go.mod":       "module x\n\ngo 1.22\n",
		"a/untouch.go": "package a\nfunc  Messy( ) {}\n",
	})
	writeFiles(t, dir, map[string]string{
		"b/b.go": "package b\n\nimport \"fmt\"\n\nfunc  B() { fmt.Printf(\"%d\", \"s\") }\n",
	})
	ctx := context.Background()
	changed, _ := g.ChangedFiles(ctx, base)
	_, patch, _ := g.DiffSince(ctx, base)
	got := byCheck(Scan(ctx, ScanInput{Root: dir, Changed: changed, Added: ParseAdded(patch),
		Runner: gitx.ReducedEnvRunner, Today: time.Now()}))
	if len(got[CheckGofmt]) != 1 || got[CheckGofmt][0].Path != "b/b.go" {
		t.Errorf("gofmt findings = %+v; want b/b.go only", got[CheckGofmt])
	}
	if len(got[CheckVet]) != 1 || got[CheckVet][0].Ref() != "b/b.go:5" {
		t.Errorf("vet findings = %+v; want the Printf mismatch at b/b.go:5", got[CheckVet])
	}
}

func TestScopeAdmitsWhatTheSpecAllows(t *testing.T) {
	s := Scope{Allow: []string{"pkg/a.go", "cmd/", "internal/**", "docs/*.md"},
		Exempt: func(p string) bool { return p == ".specs/x/tasks.json" }}
	out := s.Outside([]string{"pkg/a.go", "pkg/a_test.go", "cmd/x/main.go", "internal/y/z.go", "docs/cli.md",
		"docs/adr/01.md", ".specs/x/tasks.json"})
	if strings.Join(out, ",") != "pkg/a_test.go,docs/adr/01.md" {
		t.Errorf("outside = %v", out)
	}
	if (Scope{}).Restricts() || len((Scope{}).Outside([]string{"anything"})) != 0 {
		t.Error("an empty scope restricts")
	}
}

// The revert check runs the checks with the implementation taken out and the
// tests left in, and leaves the tree exactly as it found it.
func TestRevertProvesATestThatDependsOnTheFix(t *testing.T) {
	g, dir, base := newRepo(t, map[string]string{
		"Makefile": "test:\n\t@grep -q fixed impl.txt || ! test -f tests/proof.txt\n",
		"impl.txt": "broken\n",
		"keep.md":  "docs\n",
	})
	writeFiles(t, dir, map[string]string{"impl.txt": "fixed\n", "new_impl.txt": "new\n", "tests/proof.txt": "t\n"})
	ctx := context.Background()
	changed, _ := g.ChangedFiles(ctx, base)
	run := func(ctx context.Context) checks.Result {
		return checks.Run(ctx, gitx.ExecRunner, dir, "make test", time.Minute)
	}

	res, err := Revert(ctx, g, dir, base, changed, run)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || !res.Proves || strings.Join(res.Tests, ",") != "tests/proof.txt" {
		t.Errorf("result = %+v; the test fails without the fix, so it proves it", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "impl.txt")); string(b) != "fixed\n" {
		t.Errorf("impl.txt = %q after the check; the tree was not restored", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "new_impl.txt")); err != nil {
		t.Errorf("new_impl.txt was not restored: %v", err)
	}

	// A test that does not depend on the fix proves nothing.
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("test:\n\t@true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CommitAll(ctx, "chore: green regardless\n"); err != nil {
		t.Fatal(err)
	}
	head, _ := g.Head(ctx)
	writeFiles(t, dir, map[string]string{"impl.txt": "fixed again\n", "tests/proof.txt": "t2\n"})
	changed, _ = g.ChangedFiles(ctx, head)
	res, err = Revert(ctx, g, dir, head, changed, run)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ran || res.Proves || !strings.Contains(res.Reason, "still pass") {
		t.Errorf("result = %+v; the checks pass without the fix, so the test proves nothing", res)
	}
}

func TestRevertWithOnlyTestsHasNothingToTakeOut(t *testing.T) {
	g, dir, base := newRepo(t, map[string]string{"README.md": "x\n"})
	writeFiles(t, dir, map[string]string{"a_test.go": "package a\n"})
	changed, _ := g.ChangedFiles(context.Background(), base)
	res, err := Revert(context.Background(), g, dir, base, changed, func(context.Context) checks.Result {
		t.Fatal("the check ran with nothing reverted")
		return checks.Result{}
	})
	if err != nil || res.Ran || res.Proves || res.Reason == "" {
		t.Errorf("result = %+v, %v", res, err)
	}
}
