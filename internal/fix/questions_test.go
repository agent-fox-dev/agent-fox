package fix

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// earlierRun makes a run directory with a fix.json and an envelope, as an earlier run left
// them.
func earlierRun(t *testing.T, dir, id, key, status, question string, options, context []string) {
	t.Helper()
	run := filepath.Join(dir, ".agent-fox", "run", id)
	s := sampleState()
	s.RunID, s.Input.Key, s.Context = id, key, context
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(run); err != nil {
		t.Fatal(err)
	}
	env := map[string]any{"status": status}
	if question != "" {
		var opts []map[string]string
		for i, o := range options {
			opts = append(opts, map[string]string{"id": string(rune('A' + i)), "label": o})
		}
		env["needs_human"] = map[string]any{"question": question, "options": opts}
	}
	testutil.WriteFile(t, run, "envelope.json", string(testutil.JSON(t, env)))
}

func TestFindEarlier_TakesTheNewestQuestionOfTheInputForEachCountOfAnswers(t *testing.T) {
	dir := t.TempDir()
	const key = "https://github.com/o/r/issues/7"
	earlierRun(t, dir, "20261019_100000_aaaaaa", key, "needs_human", "Which session, long ago?", []string{"old"}, nil)
	earlierRun(t, dir, "20261019_110000_bbbbbb", key, "needs_human", "Which session?", []string{"the old one", "the new one"}, nil)
	earlierRun(t, dir, "20261019_120000_cccccc", key, "needs_human", "Which browser?", []string{"Safari", "Firefox"}, []string{"A"})
	earlierRun(t, dir, "20261019_130000_dddddd", "https://github.com/o/r/issues/8", "needs_human", "Another issue?", nil, nil)
	earlierRun(t, dir, "20261019_140000_eeeeee", key, "done", "", nil, nil)
	earlierRun(t, dir, "20261019_150000_ffffff", key, "needs_human", "The run being made?", nil, nil)
	earlierRun(t, dir, "20261019_160000_999999", key, "failed", "A run that failed?", nil, nil)
	earlierRun(t, dir, "20261019_170000_888888", key, "needs_human", "Two answers given already?", nil, []string{"A", "B"})
	earlierRun(t, dir, "scratch", key, "needs_human", "No run directory?", nil, nil)

	got := FindEarlier(dir, key, []string{"A", "B"}, "20261019_150000_ffffff")

	want := []Earlier{
		{Question: "Which session?", Options: []string{"the old one", "the new one"}, Answer: "A", Found: true},
		{Question: "Which browser?", Options: []string{"Safari", "Firefox"}, Answer: "B", Found: true},
	}
	if !slices.EqualFunc(got, want, func(a, b Earlier) bool {
		return a.Question == b.Question && a.Answer == b.Answer && a.Found == b.Found && slices.Equal(a.Options, b.Options)
	}) {
		t.Errorf("FindEarlier = %+v, want %+v", got, want)
	}
}

func TestFindEarlier_AnAnswerNoRunAskedForStandsAlone(t *testing.T) {
	if got := FindEarlier(t.TempDir(), "key", []string{"B"}, ""); len(got) != 1 || got[0].Found || got[0].Answer != "B" || got[0].Question != "" {
		t.Errorf("FindEarlier = %+v, want the answer alone", got)
	}
	if got := FindEarlier(t.TempDir(), "key", nil, ""); len(got) != 0 {
		t.Errorf("FindEarlier with no answer = %+v, want none", got)
	}
}
