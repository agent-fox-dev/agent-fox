package fix

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// failure is the *toolio.Failure of an error.
func failure(t *testing.T, err error) *toolio.Failure {
	t.Helper()
	var f *toolio.Failure
	if !errors.As(err, &f) {
		t.Fatalf("error = %v (%T), want a *toolio.Failure", err, err)
	}
	return f
}

// noForge is the client of a run that has no forge: it is never asked.
type noForge struct{ issuex.Client }

func (noForge) Authenticated() bool { return false }

func params(r *testRepo) (PreflightParams, *int) {
	built := new(int)
	return PreflightParams{
		Dir: r.Dir, Land: LandNone, Forge: noForge{},
		NewBrain: func() (brain.Brain, error) { *built++; return nil, nil },
		Warn:     func(string, string) {},
	}, built
}

// TestPreflightStopsAtTheFirstFailingCheckInOrder_TS_09_9 verifies the order of
// TS-09-9 at the function: a repository that is detached, dirty, without a gate and
// without an origin fails at the earliest check, and the brain is made last.
// Verifies: 09-REQ-2.1
func TestPreflightStopsAtTheFirstFailingCheckInOrder_TS_09_9(t *testing.T) {
	r := newTestRepo(t, map[string]string{"README.md": "# x\n"}, false)
	p, built := params(r)
	p.Land = LandBranch
	if err := os.WriteFile(filepath.Join(r.Dir, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dirty, no gate and no origin: the tree is the first of them.
	if _, err := Preflight(context.Background(), p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "dirty.txt") {
		t.Errorf("dirty, no gate, no origin: %v, want the dirty tree", err)
	}
	// Detached as well: the branch comes before the tree.
	gitDo(t, r.Dir, "checkout", "-q", "--detach")
	if _, err := Preflight(context.Background(), p); failure(t, err).Category != toolio.CategoryUsage {
		t.Errorf("detached and dirty: %v, want the usage failure of the branch", err)
	}
	gitDo(t, r.Dir, "checkout", "-q", "main")
	if err := os.Remove(filepath.Join(r.Dir, "dirty.txt")); err != nil {
		t.Fatal(err)
	}
	// Clean, no gate, no origin: the gate.
	if _, err := Preflight(context.Background(), p); failure(t, err).FixHint == nil || failure(t, err).FixHint.Flag != "--no-verify" {
		t.Errorf("no gate, no origin: %v, want the gate", err)
	}
	// No gate wanted: the origin.
	p.NoVerify = true
	if _, err := Preflight(context.Background(), p); failure(t, err).Category != toolio.CategoryPreflight || !strings.Contains(err.Error(), "origin") {
		t.Errorf("no origin: %v, want the origin", err)
	}
	if *built != 0 {
		t.Errorf("the brain was made %d times by runs that were refused before it", *built)
	}

	p.Land = LandNone
	if res, err := Preflight(context.Background(), p); err != nil || res.Base != "main" || res.Git == nil || *built != 1 {
		t.Errorf("nothing wrong: %+v, %v (brain made %d times), want it to pass and make the brain once", res, err, *built)
	}
}

// TestADirtyTreeNamesAtMostTwentyPaths_TS_09_11 verifies TS-09-11 at the function.
// Verifies: 09-REQ-2.3
func TestADirtyTreeNamesAtMostTwentyPaths_TS_09_11(t *testing.T) {
	r := newTestRepo(t, gateFiles, false)
	p, _ := params(r)
	for i := 1; i <= 25; i++ {
		if err := os.WriteFile(filepath.Join(r.Dir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(r.Dir, "src", "greet.sh"), []byte("echo changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.Dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Preflight(context.Background(), p)

	f := failure(t, err)
	if f.Category != toolio.CategoryPreflight || f.Retryable {
		t.Fatalf("failure = %+v", f)
	}
	// 27 paths are dirty: the 25 files, the change to the script and the new .gitignore.
	// The first 20 in the order git lists them are named, and the others counted.
	all := []string{"src/greet.sh", ".gitignore"}
	for i := 1; i <= 25; i++ {
		all = append(all, fmt.Sprintf("f%02d.txt", i))
	}
	named := 0
	for _, path := range all {
		if strings.Contains(f.Message, path) {
			named++
		}
	}
	if strings.Contains(f.Message, "?? ") || strings.Contains(f.Message, " M ") {
		t.Errorf("message = %q, want the paths, and not the status codes git lists them with", f.Message)
	}
	if named != 20 || !strings.Contains(f.Message, "f18.txt") || strings.Contains(f.Message, "f19.txt") || !strings.Contains(f.Message, "and 7 more") {
		t.Errorf("%d paths are named (f18 yes, f19 no, and 7 more): %s", named, f.Message)
	}

	// An ignored file alone does not make the tree dirty.
	clean := newTestRepo(t, gateFiles, false)
	if err := os.WriteFile(filepath.Join(clean.Dir, ".gitignore"), []byte("ignored.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, clean.Dir, "add", "-A")
	gitDo(t, clean.Dir, "commit", "-q", "-m", "chore: ignore")
	if err := os.WriteFile(filepath.Join(clean.Dir, "ignored.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cp, _ := params(clean)
	requireMake(t)
	if _, err := Preflight(context.Background(), cp); err != nil {
		t.Errorf("an ignored file made the tree dirty: %v", err)
	}
}
