package statetest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-12-50 (integration): The statetest helper isolates XDG_STATE_HOME,
// runs m.Run, cleans up and compares listings.
func TestTS12_50_StatetestHelperIsolatesAndCleansUp(t *testing.T) {
	// Use a temp HOME with no pre-existing state.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	var seenXDG string
	var seenDir string
	fake := fakeRunner(func() int {
		seenXDG = os.Getenv("XDG_STATE_HOME")
		// Write a file inside the isolated dir to prove it works.
		dir := filepath.Join(seenXDG, "agent-fox", "events")
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "test.jsonl"), []byte("ok"), 0o644)
		seenDir = seenXDG
		return 0
	})

	code := Run(fake)

	// XDG_STATE_HOME during the run is a directory created by os.MkdirTemp.
	if seenXDG == "" {
		t.Fatal("XDG_STATE_HOME was not set during the run")
	}
	if seenXDG == home {
		t.Error("XDG_STATE_HOME should not be the HOME directory")
	}

	// The directory is removed after the run.
	if _, err := os.Stat(seenDir); !os.IsNotExist(err) {
		t.Errorf("temp state dir should be removed after the run, stat err=%v", err)
	}

	// The returned code is the fake's code when the real directories are unchanged.
	if code != 0 {
		t.Errorf("code = %d, want 0", code)
	}
}

// TS-12-50 also verifies that TestMain in internal/toolio, cmd/fix, cmd/impl,
// cmd/triage and cmd/spec each call the helper. This is checked by TS-12-54.

// TS-12-51 (unit): The real directories are the HOME default ignoring
// XDG_STATE_HOME plus the developer's own XDG directory.
func TestTS12_51_RealDirsComputedCorrectly(t *testing.T) {
	// Case A: XDG_STATE_HOME unset before the run.
	dirs := realDirs("/h", "")
	want := []string{filepath.Join("/h", ".local", "state", "agent-fox")}
	if len(dirs) != 1 || dirs[0] != want[0] {
		t.Errorf("case A: realDirs('/h', '') = %v, want %v", dirs, want)
	}

	// Case B: XDG_STATE_HOME=/dev/x before the run.
	dirs = realDirs("/h", "/dev/x")
	want = []string{
		filepath.Join("/h", ".local", "state", "agent-fox"),
		filepath.Join("/dev/x", "agent-fox"),
	}
	if len(dirs) != 2 || dirs[0] != want[0] || dirs[1] != want[1] {
		t.Errorf("case B: realDirs('/h', '/dev/x') = %v, want %v", dirs, want)
	}

	// The temp dir the helper sets as XDG_STATE_HOME is never in the list.
	// (This is implicit: realDirs takes the original values, not the temp one.)
}

// TS-12-52 (integration): A new file under a real runs/ or events/ fails
// the package run and names the files.
func TestTS12_52_NewFileUnderRealDirFailsRun(t *testing.T) {
	// HOME is a temp dir standing in for the real home.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")

	// A fake m.Run that writes files to the "real" state directory.
	fake := fakeRunner(func() int {
		eventsDir := filepath.Join(home, ".local", "state", "agent-fox", "events")
		runsDir := filepath.Join(home, ".local", "state", "agent-fox", "runs")
		os.MkdirAll(eventsDir, 0o755)
		os.MkdirAll(runsDir, 0o755)
		os.WriteFile(filepath.Join(eventsDir, "x.jsonl"), []byte("e"), 0o644)
		os.WriteFile(filepath.Join(runsDir, "y.json"), []byte("r"), 0o644)
		return 0
	})

	code, msg := runCheck(fake)

	if code == 0 {
		t.Fatal("expected non-zero exit code when files appear under real dirs")
	}
	if !strings.Contains(msg, "events/x.jsonl") {
		t.Errorf("message should name events/x.jsonl, got: %s", msg)
	}
	if !strings.Contains(msg, "runs/y.json") {
		t.Errorf("message should name runs/y.json, got: %s", msg)
	}

	// A fake that writes nothing there yields the fake's own code.
	cleanFake := fakeRunner(func() int { return 0 })
	code2, _ := runCheck(cleanFake)
	if code2 != 0 {
		t.Errorf("clean fake: code = %d, want 0", code2)
	}
}

// TS-12-53 (unit): With no resolvable home directory the comparison is skipped.
func TestTS12_53_NoHomeSkipsComparison(t *testing.T) {
	// Unset HOME and XDG_STATE_HOME so os.UserHomeDir fails.
	t.Setenv("HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	// Also unset platform-specific home variables.
	for _, v := range []string{"USERPROFILE", "home"} {
		t.Setenv(v, "")
	}

	fake := fakeRunner(func() int { return 0 })
	code := Run(fake)

	if code != 0 {
		t.Errorf("code = %d, want 0 (comparison should be skipped)", code)
	}
}

// TS-12-54 (unit): Tests that assert on directory contents keep their own
// XDG_STATE_HOME, and TestTS06_7 points HOME at a temp dir.
func TestTS12_54_TestsKeepOwnXDGStateHome(t *testing.T) {
	// Scan the _test.go files in internal/toolio and cmd/* for tests that
	// read runs/ or events/ contents and for TestTS06_7.
	root := findRoot(t)

	// Check that TestMain in each required package calls statetest.
	pkgs := []string{
		"internal/toolio",
		"cmd/fix",
		"cmd/impl",
		"cmd/triage",
		"cmd/spec",
	}
	for _, pkg := range pkgs {
		dir := filepath.Join(root, pkg)
		found := false
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("cannot read %s: %v", dir, err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), "_test.go") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("cannot read %s/%s: %v", dir, e.Name(), err)
			}
			if strings.Contains(string(b), "statetest.") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: no _test.go file calls statetest", pkg)
		}
	}

	// Check that TestTS06_7 sets HOME to a temp dir.
	reportTestPath := filepath.Join(root, "internal", "toolio", "report_test.go")
	b, err := os.ReadFile(reportTestPath)
	if err != nil {
		t.Fatalf("cannot read report_test.go: %v", err)
	}
	src := string(b)
	// Find the TestTS06_7 function body.
	idx := strings.Index(src, "func TestTS06_7")
	if idx < 0 {
		t.Fatal("TestTS06_7 not found in report_test.go")
	}
	body := src[idx:]
	// Find the end of the function (next func or EOF).
	nextFunc := strings.Index(body[1:], "\nfunc ")
	if nextFunc > 0 {
		body = body[:nextFunc+1]
	}
	if !strings.Contains(body, `Setenv("HOME"`) {
		t.Error("TestTS06_7 does not set HOME to a temp dir")
	}

	// Check that tests TS-06-8 and TS-06-10 (which use --report-file but
	// call App.Main) set XDG_STATE_HOME.
	for _, name := range []string{"TestTS06_8", "TestTS06_10"} {
		idx := strings.Index(src, "func "+name)
		if idx < 0 {
			t.Errorf("%s not found in report_test.go", name)
			continue
		}
		fnBody := src[idx:]
		nextFn := strings.Index(fnBody[1:], "\nfunc ")
		if nextFn > 0 {
			fnBody = fnBody[:nextFn+1]
		}
		if !strings.Contains(fnBody, `Setenv("XDG_STATE_HOME"`) {
			t.Errorf("%s does not set XDG_STATE_HOME", name)
		}
	}
}

// findRoot walks up from the working directory to find the module root.
func findRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find the module root containing go.mod")
		}
		dir = parent
	}
}

// fakeRunner wraps a function as a Runner.
type fakeRunnerFunc struct {
	fn func() int
}

func (f fakeRunnerFunc) Run() int { return f.fn() }

func fakeRunner(fn func() int) Runner {
	return fakeRunnerFunc{fn: fn}
}
