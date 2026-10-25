package afcmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

const placedID, placedNonce = "20261021_150000_abcdef", "fedcba9876543210"

func TestFixRun_RunsTheBrainOnThePlacedBriefAndSaysWhatItSubmitted(t *testing.T) {
	for name, tc := range map[string]struct{ payload, submission string }{
		"a report":   {fixReport, "report"},
		"a question": {`{"question":{"text":"Which greeting?","options":["x","y"]}}`, "question"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), false)
			runDir := placeRun(t, e.dir, placedID, "fix_report", 3, placedNonce, "PREFIX: the mandate\n", "TASK: fix the greeting\n")
			testutil.WriteFile(t, runDir, "hook.json", `{"run_id":"`+placedID+`","protected_paths":[".agent-fox/"]}`)
			p := e.faux(faux.ToolCall("c1", "submit_fix", tc.payload))
			before := testutil.Files(t, e.dir, ".git", ".agent-fox")

			o := e.af("fix", "run", "--run-id", placedID, "--dir", e.dir, "--sandbox", "local")

			res := o.res()
			if o.code != 0 || res["round"] != float64(3) || res["submission"] != tc.submission || res["submitted"] != true || o.Summary != "the agentkit brain submitted a "+tc.submission+" for run "+placedID {
				t.Fatalf("%v, summary %q; want a %s of round 3, the attempt of run.json", o, o.Summary, tc.submission)
			}
			file := testutil.Decode[submission.File](t, []byte(testutil.ReadFile(t, filepath.Join(runDir, "submission.json"))))
			if file.Nonce != placedNonce || file.Attempt != 3 || ledgerData(t, runDir, "fix.run")["submission"] != tc.submission {
				t.Errorf("submission %+v, fix.run %v; want it made under the placed run, and recorded", file, ledgerData(t, runDir, "fix.run"))
			}
			if reqs := p.Requests(); len(reqs) != 1 || prefixOf(reqs[0]) != "PREFIX: the mandate\n" {
				t.Errorf("the model was not given the placed prefix")
			}
			if _, err := os.Stat(filepath.Join(runDir, "fix.json")); err == nil || len(testutil.Files(t, e.dir, ".git", ".agent-fox")) != len(before) {
				t.Error("the driver wrote the state of the host's run, or a file of the checkout")
			}
		})
	}
}

func TestFixRun_RefusesWhatIsNoPlacedRun(t *testing.T) {
	for name, tc := range map[string]struct {
		args    []string
		place   bool
		mention string
	}{
		"no run id":                     {nil, true, "--run-id"},
		"an id of no run":               {[]string{"--run-id", "20260101_000000_abcdef"}, true, ""},
		"no brief placed":               {[]string{"--run-id", placedID}, false, "no brief placed"},
		"the scripted brain, no script": {[]string{"--run-id", placedID, "--brain", "scripted"}, true, "--script"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), false)
			e.faux()
			if tc.place {
				placeRun(t, e.dir, placedID, "fix_report", 1, placedNonce, "P", "T")
			} else if err := os.MkdirAll(filepath.Join(e.dir, ".agent-fox", "run", placedID), 0o755); err != nil {
				t.Fatal(err)
			}

			o := e.af(append([]string{"fix", "run", "--dir", e.dir, "--sandbox", "local"}, tc.args...)...)

			if o.code != 2 || o.category() != "usage" || !strings.Contains(o.message(), tc.mention) {
				t.Errorf("%v; want a usage failure that mentions %q", o, tc.mention)
			}
		})
	}
}
