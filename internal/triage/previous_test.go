package triage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The question an answer is for is the last one asked by the newest earlier run
// of the same input; the run being made, other inputs, and directories that are
// no run are passed over.
func TestFindPreviousQuestion_TakesTheNewestRunOfTheInputThatAsked(t *testing.T) {
	dir := t.TempDir()
	run := func(id, fingerprint, ledger string) {
		t.Helper()
		run := filepath.Join(".agent-fox", "run", id)
		testutil.WriteFile(t, dir, filepath.Join(run, "triage.json"), string(testutil.JSON(t, Facts{Input: InputFact{Fingerprint: fingerprint}})))
		testutil.WriteFile(t, dir, filepath.Join(run, "ledger.jsonl"), ledger)
	}
	question := func(text string) string {
		return `{"seq":1,"ts":"t","kind":"triage.question","data":{"text":"` + text + `","options":["x","y"]}}` + "\n"
	}
	run("20261019_100000_aaaaaa", "f", question("older"))
	run("20261019_110000_bbbbbb", "f", question("first of the newest")+"\n"+question("last of the newest")+"not json\n")
	run("20261019_120000_cccccc", "other", question("another input's"))
	run("20261019_130000_dddddd", "f", `{"seq":1,"kind":"triage.input","data":{}}`+"\n")
	run("20261019_140000_eeeeee", "f", question("the run being made"))
	run("20261019_150000_ffffff", "f", "")
	run("not-a-run", "f", question("not a run"))
	testutil.WriteFile(t, dir, ".agent-fox/run/20261019_160000_000000/triage.json", "{")
	if err := os.Remove(filepath.Join(dir, ".agent-fox/run/20261019_150000_ffffff/ledger.jsonl")); err != nil {
		t.Fatal(err)
	}

	if got := FindPreviousQuestion(dir, "f", "20261019_140000_eeeeee"); got == nil || got.Text != "last of the newest" || len(got.Options) != 2 {
		t.Errorf("FindPreviousQuestion = %+v, want the last question of the newest run that asked", got)
	}
	if got := FindPreviousQuestion(dir, "never triaged", ""); got != nil {
		t.Errorf("an input that was never triaged: %+v", got)
	}
	if got := FindPreviousQuestion(t.TempDir(), "f", ""); got != nil {
		t.Errorf("no run directory: %+v", got)
	}
}
