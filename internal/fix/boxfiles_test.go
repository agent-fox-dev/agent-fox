package fix

import (
	"context"
	"io/fs"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// boxFilesFor opens a box of the provider over a new checkout and writes the files of a round.
func boxFilesFor(t *testing.T, provider string) (dir string, sess *box.Session) {
	t.Helper()
	r := newTestRepo(t, map[string]string{"a.txt": "a\n"}, false)
	state := sampleState()
	sess, err := box.Open(context.Background(), box.Request{Provider: provider, HostDir: r.Dir, RunID: state.RunID, Tool: "fix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	if err := WriteBoxFiles(context.Background(), sess, state); err != nil {
		t.Fatal(err)
	}
	return r.Dir, sess
}

func boxHas(t *testing.T, sess *box.Session, rel string) bool {
	t.Helper()
	_, err := fs.Stat(sess.FS(context.Background()), rel)
	return err == nil
}

// TestTheBoxIsGivenTheTableAndTheRulesAndNotTheStateOfTheRun guards the files of a round in a
// box of its own: gate.json and guard.json are placed, and fix.json is not, for with it in its
// run directory the af fix run of a driver in the box would be the round of the host.
func TestTheBoxIsGivenTheTableAndTheRulesAndNotTheStateOfTheRun(t *testing.T) {
	_, sess := boxFilesFor(t, "local")
	for _, rel := range []string{".agent-fox/gate.json", ".agent-fox/guard.json"} {
		if !boxHas(t, sess, rel) {
			t.Errorf("%s is not in the box", rel)
		}
	}
	if boxHas(t, sess, ".agent-fox/run/"+sampleState().RunID+"/"+StateFile) {
		t.Error("fix.json is in the box")
	}
}
