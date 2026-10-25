package conform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// A check its context stops while the change is out puts the change back,
// ends the tests with everything they started, and returns the context's
// error.
func TestCheckStoppedByItsContextPutsTheTreeBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		want error
	}{
		{"a cancellation", context.Canceled},
		{"a deadline", context.DeadlineExceeded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, _ := fixed(t, "exit 0")
			ready := fifo(t)
			check := shCheck("tests", project.KindTest, fmt.Sprintf("if grep -q fixed lib.txt; then exit 0; fi; sleep 60 & echo $! > '%s'; wait", ready))
			before := snapshot(t, dir)
			ctx, cancel := context.WithCancel(t.Context())
			if tc.want == context.DeadlineExceeded {
				ctx, cancel = context.WithTimeout(t.Context(), 1500*time.Millisecond)
			}
			defer cancel()
			child := make(chan string, 1)
			go func() {
				data, _ := os.ReadFile(ready) // returns once the tests run on the reverted tree
				child <- string(data)
				if tc.want == context.Canceled {
					cancel()
				}
			}()

			_, err := Check(ctx, dir, base, Options{Table: tableOf(check)})

			if !errors.Is(err, tc.want) {
				t.Fatalf("Check = %v, want %v", err, tc.want)
			}
			requireState(t, dir, before)
			select {
			case pid := <-child:
				requireGone(t, pid)
			default:
				t.Fatal("the context ended before the tests ran on the reverted tree")
			}
		})
	}
}

// The code that runs on the reverted tree panics: the tree is given back, and
// the panic goes on.
func TestAPanicWhileRevertedPutsTheTreeBack(t *testing.T) {
	dir, base, check := fixed(t, "exit 0")
	before := snapshot(t, dir)

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the panic", r)
			}
		}()
		_, _ = Check(t.Context(), dir, base, Options{Table: tableOf(check), TableNow: func(context.Context) (project.Table, error) { panic("boom") }})
	}()

	requireState(t, dir, before)
}

// A patch that is there already, left by a check that was killed, is not
// touched, and nothing runs until someone has looked at it.
func TestCheckRefusesWhileAPatchIsLeft(t *testing.T) {
	dir, base, _ := fixed(t, "grep -q fixed lib.txt")
	old := testutil.WriteFile(t, dir, ".agent-fox/revert.patch", "old patch")
	before := snapshot(t, dir)
	counter, counted := counterCheck(t, "counted", project.KindTest)

	_, err := Check(t.Context(), dir, base, Options{Table: tableOf(counted)})

	var restore *RestoreError
	if !errors.As(err, &restore) || restore.Patch != old || !strings.Contains(err.Error(), old) || errors.Unwrap(err) == nil {
		t.Fatalf("Check = %v, want a restore error that names %s", err, old)
	}
	if runs(t, counter) != 0 || testutil.ReadFile(t, old) != "old patch" {
		t.Errorf("%d runs, the patch holds %q; want none, and the patch as it was", runs(t, counter), testutil.ReadFile(t, old))
	}
	if after := snapshot(t, dir); after.tree != before.tree || after.status != before.status {
		t.Error("the tree changed although the check refused")
	}
}

// While one check has the change out, another does not start from that state.
func TestACheckThatStartsDuringAnotherRefuses(t *testing.T) {
	dir, base, _ := fixed(t, "exit 0")
	ready, release := fifo(t), fifo(t)
	table := tableOf(shCheck("tests", project.KindTest, fmt.Sprintf(`if grep -q fixed lib.txt; then exit 0; fi; echo > '%s'; read x < '%s'; exit 1`, ready, release)))
	before := snapshot(t, dir)
	first := make(chan error, 1)
	go func() {
		_, err := Check(context.Background(), dir, base, Options{Table: table})
		first <- err
	}()
	if _, err := os.ReadFile(ready); err != nil { // the first check has the change out
		t.Fatal(err)
	}

	_, err := Check(t.Context(), dir, base, Options{Table: table})

	var restore *RestoreError
	if !errors.As(err, &restore) {
		t.Errorf("the second check = %v, want a refusal that names the first one's patch", err)
	}
	if got := testutil.ReadFile(t, filepath.Join(dir, "lib.txt")); got != "broken\n" {
		t.Errorf("lib.txt holds %q while the first check works: the second touched it", got)
	}
	if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Errorf("the first check = %v", err)
	}
	requireState(t, dir, before)
}

// Two checks started together: one runs, the other refuses, and the tree
// ends as it was.
func TestChecksStartedTogetherDoNotUndoEachOther(t *testing.T) {
	dir, base, check := fixed(t, "grep -q fixed lib.txt")
	before := snapshot(t, dir)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() { _, errs[i] = Check(context.Background(), dir, base, Options{Table: tableOf(check)}) })
	}
	wg.Wait()

	for _, err := range errs {
		var restore *RestoreError
		if err != nil && !errors.As(err, &restore) {
			t.Errorf("Check = %v, want success or a refusal", err)
		}
	}
	requireState(t, dir, before)
	if got := testutil.ReadFile(t, filepath.Join(dir, "lib.txt")); got != "fixed\n" {
		t.Errorf("lib.txt holds %q: the fix was lost (%v)", got, errs)
	}
}

// A check that changes the tree while the change is out leaves a tree the
// change cannot go back on as it was: what the check did is not overwritten,
// and the patch is kept for whoever puts the change back.
func TestCheckThatCannotPutTheTreeBackKeepsThePatch(t *testing.T) {
	for _, tc := range []struct{ name, script, says, lib string }{
		{"a file added", "echo stray > stray.txt; exit 1", "tree hash", "fixed\n"},
		{"a reverted file edited", `echo "my edit" > lib.txt; exit 1`, "lib.txt", "my edit\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, base, _ := fixed(t, "exit 0")
			check := shCheck("tests", project.KindTest, "if grep -q fixed lib.txt; then exit 0; fi; "+tc.script)

			_, err := Check(t.Context(), dir, base, Options{Table: tableOf(check)})

			var restore *RestoreError
			if !errors.As(err, &restore) || !strings.Contains(err.Error(), tc.says) || !strings.Contains(err.Error(), restore.Patch) {
				t.Fatalf("Check = %v, want a restore error that says %q and names the patch", err, tc.says)
			}
			if patch := testutil.ReadFile(t, restore.Patch); restore.Patch != filepath.Join(dir, ".agent-fox", "revert.patch") || !strings.Contains(patch, "+fixed") {
				t.Errorf("the patch %s holds %q, want the change", restore.Patch, patch)
			}
			if got := testutil.ReadFile(t, filepath.Join(dir, "lib.txt")); got != tc.lib {
				t.Errorf("lib.txt holds %q, want %q", got, tc.lib)
			}
		})
	}
}

// A check that runs git, or a build that does, rewrites the index with its
// stat data refreshed; the bytes the check started with are written back.
func TestAnIndexRewrittenByTheTestsIsPutBack(t *testing.T) {
	dir, base := newRepo(t, map[string]string{"lib.txt": "broken\n", "other.txt": "unchanged\n"})
	testutil.WriteFile(t, dir, "lib.txt", "fixed\n")
	testutil.WriteFile(t, dir, "tests/t.txt", "t\n")
	script := `if grep -q fixed lib.txt; then exit 0; fi; touch -d '2001-01-01' other.txt; git update-index --refresh >/dev/null 2>&1; exit 1`
	before := snapshot(t, dir)

	res, err := Check(t.Context(), dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, script))})

	if err != nil || res.Outcome != OutcomeProven {
		t.Fatalf("Check = %+v, %v; want proven", res, err)
	}
	requireState(t, dir, before)
}

// A revert that cannot write changes nothing, since git apply changes every
// path or none, and is an error of its own.
func TestARevertThatCannotWriteChangesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
	dir, base := newRepo(t, map[string]string{"ro/x.txt": "v1\n"})
	testutil.WriteFile(t, dir, "ro/x.txt", "v2\n")
	testutil.WriteFile(t, dir, "tests/t.txt", "t\n")
	if err := os.Chmod(filepath.Join(dir, "ro"), 0o555); err != nil {
		t.Skipf("cannot make a directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "ro"), 0o755) })
	before := snapshot(t, dir)

	_, err := Check(t.Context(), dir, base, Options{Table: tableOf(shCheck("tests", project.KindTest, "exit 0"))})

	var restore *RestoreError
	if err == nil || errors.As(err, &restore) {
		t.Fatalf("Check = %v, want the error of the write, and no restore error", err)
	}
	requireState(t, dir, before)
}

// git calls an entry racily clean unless it is older than the index file, so
// the copy that git refreshes in place of the real index is as old as it.
func TestCopyIndexKeepsTheModificationTime(t *testing.T) {
	dir := t.TempDir()
	src, dst := testutil.WriteFile(t, dir, "index", "index bytes"), filepath.Join(dir, "copy")
	old := time.Date(2021, 5, 6, 7, 8, 9, 123_000_000, time.UTC)
	if err := os.Chtimes(src, old, old); err != nil {
		t.Fatal(err)
	}

	if err := copyIndex(src, dst); err != nil {
		t.Fatal(err)
	}

	if info, err := os.Stat(dst); err != nil || !info.ModTime().Equal(old) || testutil.ReadFile(t, dst) != "index bytes" {
		t.Errorf("the copy: %v, %v", info, err)
	}
	if err := copyIndex(filepath.Join(dir, "missing"), filepath.Join(dir, "copy2")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("copying an index that is not there = %v", err)
	}
}
