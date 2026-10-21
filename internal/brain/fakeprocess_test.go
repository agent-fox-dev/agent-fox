package brain

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/fakeclaude"
	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// The fake process is this test binary, run under the name af or claude (see
// internal/fakeclaude): TestMain sees the name and acts as the program instead of
// running the tests. What it submits it submits to the contract kind.
func TestMain(m *testing.M) {
	if name := fakeclaude.Name(); name != "" {
		os.Exit(fakeclaude.Run(name, os.Args[1:], fakeclaude.Options{Submit: submitToTheContractKind}))
	}
	code := m.Run()
	removeBuiltAF()
	os.Exit(code)
}

// What a fake process is told, what it answered and what it noted.
type (
	fakeScript = fakeclaude.Script
	fakeAnswer = fakeclaude.Answer
	fakeSubmit = fakeclaude.Submit
	fakeRecord = fakeclaude.Record
)

const (
	fakeScriptPath = fakeclaude.ScriptPath
	fakeRecordPath = fakeclaude.RecordPath
	fakeChildPath  = fakeclaude.ChildPath
	fakeHooksPath  = fakeclaude.HooksPath
	fakeSubmitPath = fakeclaude.SubmitsPath
)

// submitToTheContractKind submits the payload of the run into the tree the fake runs in,
// which is the cwd of the process, as af <agent> submit does for the kind of its agent.
func submitToTheContractKind(runID string, payload []byte) fakeSubmit {
	root, err := os.OpenRoot(".")
	if err != nil {
		return fakeSubmit{Problems: []string{err.Error()}}
	}
	defer root.Close()
	target := submission.Target{Tree: root.FS(), Write: submission.RootWriter(root), RunID: runID}
	_, err = submission.Submit(context.Background(), target, contractKind(), payload, time.Now())
	var refused *submission.Refused
	switch {
	case err == nil:
		return fakeSubmit{Accepted: true}
	case errors.As(err, &refused):
		var problems []string
		for _, p := range refused.Problems {
			problems = append(problems, p.String())
		}
		return fakeSubmit{Problems: problems}
	}
	return fakeSubmit{Problems: []string{err.Error()}}
}

// fakeBin makes a directory with af and claude in it, each a link to this test binary.
func fakeBin(t *testing.T) (dir, af, claude string) {
	t.Helper()
	return fakeclaude.Install(t)
}
