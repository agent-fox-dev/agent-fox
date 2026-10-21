package fix

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// boxFilesFor opens a box of the provider over a new checkout, with files in .agent-fox/
// already, and writes the files of a round.
func boxFilesFor(t *testing.T, provider string, existing map[string]string, driverInBox bool) (dir string, sess *box.Session) {
	t.Helper()
	dir, sess, _ = boxFilesPlaced(t, provider, existing, driverInBox)
	return dir, sess
}

// boxFilesPlaced is boxFilesFor, and the files that the round put on a checkout that is the box.
func boxFilesPlaced(t *testing.T, provider string, existing map[string]string, driverInBox bool) (dir string, sess *box.Session, placed *Placed) {
	t.Helper()
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
	for name, content := range existing {
		path := filepath.Join(r.Dir, ".agent-fox", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	state := sampleState()
	sess, err := box.Open(context.Background(), box.Request{Provider: provider, HostDir: r.Dir, RunID: state.RunID, Tool: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	placed = &Placed{}
	if err := WriteBoxFiles(context.Background(), sess, state, driverInBox, placed); err != nil {
		t.Fatal(err)
	}
	return r.Dir, sess, placed
}

func boxHas(t *testing.T, sess *box.Session, rel string) bool {
	t.Helper()
	_, err := fs.Stat(sess.FS(context.Background()), rel)
	return err == nil
}

// TestTheBoxIsGivenTheTableTheRulesAndTheStateOfTheRun guards the files of a round in a box
// of its own: gate.json, guard.json and fix.json are all placed.
func TestTheBoxIsGivenTheTableTheRulesAndTheStateOfTheRun(t *testing.T) {
	_, sess := boxFilesFor(t, "local", nil, false)
	for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json", ".agent-fox/run/" + sampleState().RunID + "/" + StateFile} {
		if !boxHas(t, sess, rel) {
			t.Errorf("%s is not in the box", rel)
		}
	}
}

// TestTheDriverInTheBoxIsNotGivenTheStateOfTheRun guards the line between the round of the
// host and the driver the box executes: with fix.json in its run directory, af fix run
// would be the round of the host.
func TestTheDriverInTheBoxIsNotGivenTheStateOfTheRun(t *testing.T) {
	_, sess := boxFilesFor(t, "local", nil, true)
	if boxHas(t, sess, ".agent-fox/run/"+sampleState().RunID+"/"+StateFile) {
		t.Error("fix.json is in the box of the driver")
	}
	if !boxHas(t, sess, ".agent-fox/gate.json") || !boxHas(t, sess, ".agent-fox/guard.json") {
		t.Error("the table and the rules are still placed")
	}
}

// TestAGateAndGuardFileOfThePersonOnTheCheckoutAreNotOverwritten guards 10-REQ-5.3's spirit
// for the files of a round: under none the box is the checkout, and its gate.json and
// guard.json are the person's; where there are none, the run's are placed.
func TestAGateAndGuardFileOfThePersonOnTheCheckoutAreNotOverwritten(t *testing.T) {
	dir, _ := boxFilesFor(t, "none", map[string]string{"gate.json": "{\"mine\":1}\n", "guard.json": "{\"mine\":2}\n"}, false)
	for name, want := range map[string]string{"gate.json": "{\"mine\":1}\n", "guard.json": "{\"mine\":2}\n"} {
		if got, _ := os.ReadFile(filepath.Join(dir, ".agent-fox", name)); string(got) != want {
			t.Errorf("%s = %q, want the person's %q", name, got, want)
		}
	}

	fresh, sess := boxFilesFor(t, "none", nil, false)
	for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json", ".agent-fox/run/" + sampleState().RunID + "/" + StateFile} {
		if _, err := os.Stat(filepath.Join(fresh, filepath.FromSlash(rel))); err != nil || !boxHas(t, sess, rel) {
			t.Errorf("%s is not in the checkout: %v", rel, err)
		}
	}
}

// TestTheFilesThatAfPlacedOnTheCheckoutAreTakenOffItAndTheOnesOfThePersonAreNot guards the
// end of a run under none: gate.json and guard.json that af put there for the rounds are
// removed when the run ends, so that they do not pass for the person's own at the next one;
// a file that is as af wrote it goes, one that someone changed since stays, and the person's
// own, that af never touched, stays.
func TestTheFilesThatAfPlacedOnTheCheckoutAreTakenOffItAndTheOnesOfThePersonAreNot(t *testing.T) {
	dir, _, placed := boxFilesPlaced(t, "none", map[string]string{"guard.json": "{\"mine\":2}\n"}, false)
	gate := filepath.Join(dir, ".agent-fox", "gate.json")
	guard := filepath.Join(dir, ".agent-fox", "guard.json")
	state := filepath.Join(dir, ".agent-fox", "run", sampleState().RunID, StateFile)
	if _, err := os.Stat(gate); err != nil {
		t.Fatalf("af placed no gate.json: %v", err)
	}

	placed.Remove(dir)

	if _, err := os.Stat(gate); err == nil {
		t.Error("gate.json, which af placed, is still on the checkout")
	}
	if got, _ := os.ReadFile(guard); string(got) != "{\"mine\":2}\n" {
		t.Errorf("guard.json = %q, want the person's, which af did not place", got)
	}
	if _, err := os.Stat(state); err != nil {
		t.Errorf("fix.json of the run directory is gone: %v; it is the run's, not a file to take off", err)
	}

	// A file of the person's that is empty is theirs all the same.
	dirEmpty, _, placedEmpty := boxFilesPlaced(t, "none", map[string]string{"gate.json": ""}, false)
	placedEmpty.Remove(dirEmpty)
	if _, err := os.Stat(filepath.Join(dirEmpty, ".agent-fox", "gate.json")); err != nil {
		t.Errorf("an empty gate.json of the person's was taken off the checkout: %v", err)
	}

	// A file that was changed after af placed it is not af's any more.
	dir2, _, placed2 := boxFilesPlaced(t, "none", nil, false)
	changed := filepath.Join(dir2, ".agent-fox", "gate.json")
	if err := os.WriteFile(changed, []byte("{\"edited\":true}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	placed2.Remove(dir2)
	if got, _ := os.ReadFile(changed); string(got) != "{\"edited\":true}\n" {
		t.Errorf("gate.json = %q, want the file as it was changed", got)
	}
	if _, err := os.Stat(filepath.Join(dir2, ".agent-fox", "guard.json")); err == nil {
		t.Error("guard.json, as af placed it, is still on the checkout")
	}

	// A box of its own has no file to take off a checkout, and a nil list is none.
	_, _, none := boxFilesPlaced(t, "local", nil, false)
	none.Remove(t.TempDir())
	(*Placed)(nil).Remove(t.TempDir())
}
