package afcmd

import (
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestGateRun_AMeasuredGateIsDoneWhateverItsColour(t *testing.T) {
	testutil.Require(t, "git", "sh")
	for name, tc := range map[string]struct {
		check   []string
		summary string
		warning string
	}{
		"green":            {[]string{"ok", "sh", "-c", "exit 0"}, "gate passed: ok", ""},
		"red":              {[]string{"red", "sh", "-c", "echo broken; exit 1"}, "gate failed: red (fail)", ""},
		"nothing to run":   {[]string{"absent", "no-such-program-for-af"}, "no check could run: 1 skipped", "no_checks"},
		"changes the tree": {[]string{"grows", "sh", "-c", "echo more >> README.md"}, "gate passed: grows", "tree_changed_during_gate"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := testutil.Repo(t, nil)
			writeGate(t, dir, tc.check)

			o := measure(t, "gate", "run", "--dir", dir)

			id, _ := o.res()["id"].(string)
			if o.code != 0 || o.Status != "done" || o.Summary != tc.summary || o.RunID != nil {
				t.Errorf("%v, summary %q; want done, no run directory, and the summary %q", o, o.Summary, tc.summary)
			}
			if (tc.warning == "") != (len(o.Warnings) == 0) || tc.warning != "" && !o.warned(tc.warning) {
				t.Errorf("warnings %v, want %q", o.Warnings, tc.warning)
			}
			if o.artifacts("gate_result") != 1 || o.Artifacts[0].Path != filepath.Join(dir, ".agent-fox", "gate", id, "result.json") {
				t.Errorf("artifacts %v, want the result of the gate %s", o.Artifacts, id)
			}
		})
	}
}

func TestGateRun_ASecondRunOnTheSameTreeAnswersTheStoredResult(t *testing.T) {
	testutil.Require(t, "git", "sh")
	dir := testutil.Repo(t, nil)
	counter := filepath.Join(t.TempDir(), "counter")
	writeGate(t, dir, []string{"counted", "sh", "-c", "echo run >> '" + counter + "'"})

	first, second := measure(t, "gate", "run", "--dir", dir), measure(t, "gate", "run", "--dir", dir)

	if first.Summary != "gate passed: counted" || second.Summary != "gate passed (stored result): counted" || second.res()["id"] != first.res()["id"] {
		t.Errorf("summaries %q then %q, ids %v and %v; want the stored result of the same tree", first.Summary, second.Summary, first.res()["id"], second.res()["id"])
	}
	if runs := testutil.ReadFile(t, counter); runs != "run\n" {
		t.Errorf("the check ran %q, want once", runs)
	}
}

func TestGateRun_FailsWithTheCategoryOfWhatStoppedIt(t *testing.T) {
	testutil.Require(t, "git", "sh")
	for name, tc := range map[string]struct {
		prepare  func(dir string)
		args     []string
		category string
	}{
		"a gate.json that cannot be used": {func(dir string) { testutil.WriteFile(t, dir, ".agent-fox/gate.json", `{"version":3,"checks":[]}`) }, nil, "config"},
		"an .agent-fox that is a file":    {func(dir string) { testutil.WriteFile(t, dir, ".agent-fox", "not a directory") }, nil, "io"},
		"its timeout":                     {func(dir string) { writeGate(t, dir, []string{"slow", "sh", "-c", "sleep 60"}) }, []string{"--timeout", "1s"}, "timeout"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := testutil.Repo(t, nil)
			tc.prepare(dir)

			o := measure(t, append([]string{"gate", "run", "--dir", dir}, tc.args...)...)

			if o.code != 1 || o.category() != tc.category {
				t.Errorf("%v; want failed/%s", o, tc.category)
			}
			if results, _ := filepath.Glob(filepath.Join(dir, ".agent-fox", "gate", "*", "result.json")); len(results) != 0 {
				t.Errorf("a gate that did not finish stored %v", results)
			}
		})
	}
}
