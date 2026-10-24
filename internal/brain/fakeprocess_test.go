package brain

import (
	"os"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/fakeaf"
)

// The fake process is this test binary, run under the name af (see internal/fakeaf):
// TestMain sees the name and acts as the program instead of running the tests.
func TestMain(m *testing.M) {
	if fakeaf.Name() != "" {
		os.Exit(fakeaf.Run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// What the fake process is told, and what it noted.
type (
	fakeScript = fakeaf.Script
	fakeRecord = fakeaf.Record
)

const (
	fakeScriptPath = fakeaf.ScriptPath
	fakeRecordPath = fakeaf.RecordPath
	fakeChildPath  = fakeaf.ChildPath
)

// fakeBin makes a directory with af in it, a link to this test binary.
func fakeBin(t *testing.T) (dir, af string) {
	t.Helper()
	return fakeaf.Install(t)
}
