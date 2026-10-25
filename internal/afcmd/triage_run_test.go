package afcmd

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// placeTriage places a triage of a text in the checkout as af triage places it in a box, under
// the attempt and the nonce, and returns the run id and the brief.
func placeTriage(t *testing.T, dir string, attempt int, nonce string) (string, triage.Brief) {
	t.Helper()
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := triage.BuildBrief(context.Background(), triage.BriefParams{
		Dir: dir, Report: triage.Report{Kind: "text", Body: "The greeting says helo."}, RunID: rd.ID, TruncationMarker: toolio.TruncationMarker,
	})
	if err != nil {
		t.Fatal(err)
	}
	placeRun(t, dir, rd.ID, triage.KindName, attempt, nonce, brief.Prefix, brief.Task)
	return rd.ID, brief
}

func TestTriageRun_RunsTheAgentkitBrainOnThePlacedBriefAndSubmitsUnderItsNonce(t *testing.T) {
	e := newEnv(t, repoFiles(), false)
	id, brief := placeTriage(t, e.dir, 2, "0123456789abcdef")
	p := e.faux(faux.ToolCall("c1", "read_file", `{"path":"src/greet.sh"}`), faux.ToolCall("c2", "file_diagnosis", diagnosis(nil)))
	status := e.git("status", "--porcelain")

	o := e.af("triage", "run", "--run-id", id, "--dir", e.dir, "--budget", "3.5", "--max-turns", "40")

	if o.code != 0 || o.res()["run_id"] != id || o.res()["submitted"] != true || o.Usage.Brain != "agentkit" || o.Usage.Turns != 2 {
		t.Fatalf("%v, usage %+v; want the submission of the agentkit brain in two turns", o, o.Usage)
	}
	runDir := filepath.Join(e.dir, ".agent-fox", "run", id)
	file := testutil.Decode[submission.File](t, []byte(testutil.ReadFile(t, filepath.Join(runDir, "submission.json"))))
	if file.Nonce != "0123456789abcdef" || file.Attempt != 2 || !slices.Contains(ledger(t, runDir), "triage.submission") {
		t.Errorf("submission %+v, ledger %v; want it made under the placed attempt and nonce, and recorded", file, ledger(t, runDir))
	}
	req := p.Requests()[0]
	var tools []string
	for _, tool := range req.Tools {
		tools = append(tools, tool.Name)
	}
	if prefixOf(req) != brief.Prefix || !slices.Contains(tools, "file_diagnosis") || !slices.Contains(tools, "read_file") || slices.Contains(tools, "write_file") {
		t.Errorf("the model was given another prefix, or the tools %v", tools)
	}
	var task string
	for _, m := range req.Messages {
		if u, ok := m.(core.UserMessage); ok {
			task += u.Content.Text()
		}
	}
	if !strings.Contains(task, "You have at most 40 turns, $3.50 of model cost") {
		t.Errorf("the task does not hold the bounds of the flags:\n%s", task)
	}
	if got := e.git("status", "--porcelain"); got != status {
		t.Errorf("git status is %q, was %q", got, status)
	}
}

func TestTriageRun_RefusesWhatIsNoPlacedRunAndFailsWithoutASubmission(t *testing.T) {
	for name, tc := range map[string]struct {
		args     func(id string) []string
		place    func(t *testing.T, dir, id string)
		code     int
		category string
	}{
		"no run id":               {func(string) []string { return nil }, nil, 2, "usage"},
		"a run id that is a path": {func(string) []string { return []string{"--run-id", "../x"} }, nil, 2, "usage"},
		"a budget of 0":           {func(id string) []string { return []string{"--run-id", id, "--budget", "0"} }, nil, 2, "usage"},
		"no turns":                {func(id string) []string { return []string{"--run-id", id, "--max-turns", "0"} }, nil, 2, "usage"},
		"no brief placed": {func(id string) []string { return []string{"--run-id", id} },
			func(t *testing.T, dir, id string) { testutil.WriteFile(t, dir, ".agent-fox/run/"+id+"/run.json", "{}") }, 2, "usage"},
		"a run placed for a fix": {func(id string) []string { return []string{"--run-id", id} },
			func(t *testing.T, dir, id string) {
				placeRun(t, dir, id, "fix_report", 1, "0123456789abcdef", "P", "T")
			}, 2, "usage"},
		"a brain that does not submit": {func(id string) []string { return []string{"--run-id", id} }, nil, 1, "no_result"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, repoFiles(), false)
			e.faux(faux.Text("I could not decide."))
			id, _ := placeTriage(t, e.dir, 1, "0123456789abcdef")
			if tc.place != nil {
				tc.place(t, e.dir, id)
			}

			o := e.af(append([]string{"triage", "run", "--dir", e.dir}, tc.args(id)...)...)

			if o.code != tc.code || o.category() != tc.category || len(e.forge.Requests()) != 0 {
				t.Errorf("%v, %d requests to the forge; want exit code %d, category %s and no request", o, len(e.forge.Requests()), tc.code, tc.category)
			}
		})
	}
}
