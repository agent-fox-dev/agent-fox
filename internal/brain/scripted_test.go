package brain

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// writeScript writes a script file and returns its path.
func writeScript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// citeValidator is a kind whose payloads cite a path that must be in the tree.
func citeKind() submission.Kind {
	k := testKind()
	k.Validate = func(ctx context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		var p struct {
			Cite string `json:"cite"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, []submission.Problem{{Message: err.Error()}}
		}
		if p.Cite == "" {
			return payload, nil
		}
		if _, err := fs.Stat(tree, p.Cite); err != nil {
			return nil, []submission.Problem{{Path: "cite", Message: p.Cite + " is not in the tree"}}
		}
		return payload, nil
	}
	return k
}

// runScript runs a script through Execute, over a real box.
func runScript(t *testing.T, w *world, script string, mutate func(*Brief)) (Result, error) {
	t.Helper()
	brain, err := Default.New(ScriptedName, Options{Script: writeScript(t, script)})
	if err != nil {
		t.Fatal(err)
	}
	b := w.brief(1)
	b.Kind = citeKind()
	b.Tools = []string{"read_file", "write_file", "list_files", "execute"}
	if mutate != nil {
		mutate(&b)
	}
	req := w.request(b, brain)
	req.Name = ScriptedName
	return Execute(context.Background(), req)
}

// transcript is the lines of the transcript of attempt 1.
func (w *world) transcript() []map[string]any {
	w.t.Helper()
	return lines(w.t, filepath.Join(w.run.Path, "transcript.1.jsonl"))
}

// TestAScriptThatCannotBeUsedIsRefusedAtConstruction_TS_07_65 verifies TS-07-65:
// an unreadable or malformed script fails construction with a usage failure
// before any box opens.
// Verifies: 07-REQ-9.1
func TestAScriptThatCannotBeUsedIsRefusedAtConstruction_TS_07_65(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere.json")
	for name, tc := range map[string]struct {
		file    string
		content string
		mention string
	}{
		"missing":                           {file: missing, mention: "nowhere.json"},
		"not JSON":                          {content: `{steps`, mention: "script.json"},
		"an unknown field":                  {content: `{"steps":[],"extra":1}`, mention: "extra"},
		"an unknown step key":               {content: `{"steps":[{"tool":"read_file","args":{},"oops":1}]}`, mention: "oops"},
		"both forms":                        {content: `{"steps":[{"tool":"read_file"},{"tool":"read_file","submit":{"a":1}}]}`, mention: "step 1"},
		"neither form":                      {content: `{"steps":[{"expect":"ok"}]}`, mention: "step 0"},
		"a tool step that expects accepted": {content: `{"steps":[{"tool":"read_file","expect":"accepted"}]}`, mention: "step 0"},
		"a submit step that expects ok":     {content: `{"steps":[{"tool":"read_file"},{"submit":{"a":1},"expect":"ok"}]}`, mention: "step 1"},
		"an expect that is nothing":         {content: `{"steps":[{"tool":"read_file","expect":"maybe"}]}`, mention: "maybe"},
		"args on a submit":                  {content: `{"steps":[{"submit":{"a":1},"args":{}}]}`, mention: "step 0"},
		"steps that are no list":            {content: `{"steps":{"tool":"x"}}`, mention: "script.json"},
		"an empty tool name":                {content: `{"steps":[{"tool":""}]}`, mention: "step 0"},
	} {
		t.Run(name, func(t *testing.T) {
			file := tc.file
			if file == "" {
				file = writeScript(t, tc.content)
			}

			b, err := Default.New(ScriptedName, Options{Script: file})

			f := asFailure(t, err)
			if b != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, filepath.Base(file)) || !strings.Contains(f.Message, tc.mention) {
				t.Errorf("New = %v, %v; want a usage failure that names the file and %q", b, err, tc.mention)
			}
		})
	}
	if _, err := Default.New(ScriptedName, Options{}); asFailure(t, err).Category != toolio.CategoryUsage {
		t.Errorf("a scripted brain with no script: %v", err)
	}
	if b, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[]}`)}); err != nil || b == nil {
		t.Errorf("an empty script: %v, %v", b, err)
	}
	if e, err := Default.Lookup(ScriptedName); err != nil || e.CostSource != CostNone || len(e.BoxEnv) != 0 {
		t.Errorf("the entry = %+v, %v; want no cost and no box environment", e, err)
	}
}

// TestTheScriptReplaysInOrder_TS_07_66 verifies TS-07-66:
// the scripted brain replays steps in order and each effect is visible before
// the next step.
// Verifies: 07-REQ-9.2
func TestTheScriptReplaysInOrder_TS_07_66(t *testing.T) {
	w := newWorld(t)

	res, err := runScript(t, w, `{"steps":[
		{"tool":"write_file","args":{"path":"notes/a.txt","content":"hello from the script\n"},"expect":"ok"},
		{"tool":"read_file","args":{"path":"notes/a.txt"},"expect":"ok"},
		{"submit":{"summary":"done","cite":"notes/a.txt"},"expect":"accepted"}
	]}`, nil)

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	tr := w.transcript()
	if len(tr) != 3 {
		t.Fatalf("%d transcript lines, want 3: %v", len(tr), tr)
	}
	if result, _ := tr[1]["result"].(string); !strings.Contains(result, "hello from the script") {
		t.Errorf("the read step saw %q, want what the write step wrote", result)
	}
	if tr[0]["tool"] != "write_file" || tr[1]["tool"] != "read_file" || tr[2]["tool"] != "submit_test" {
		t.Errorf("the transcript tools are %v, %v, %v, want the steps in order, the submission as its tool", tr[0]["tool"], tr[1]["tool"], tr[2]["tool"])
	}
	if got, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err != nil || !strings.Contains(got, `"cite":"notes/a.txt"`) {
		t.Errorf("the box's submission = %q, %v", got, err)
	}
	if string(res.Payload) != `{"summary":"done","cite":"notes/a.txt"}` {
		t.Errorf("payload = %s", res.Payload)
	}
	if _, err := os.Stat(filepath.Join(w.dir, "notes")); err == nil {
		t.Error("the script's write reached the host checkout")
	}
}

// TestAnExpectThatDoesNotHoldStopsTheReplay_TS_07_67 verifies TS-07-67:
// a step whose actual result differs from expect stops the replay with an error
// naming index, expected and actual.
// Verifies: 07-REQ-9.3
func TestAnExpectThatDoesNotHoldStopsTheReplay_TS_07_67(t *testing.T) {
	for name, tc := range map[string]struct {
		script  string
		mention []string
	}{
		"an error that is ok": {`{"steps":[
			{"tool":"read_file","args":{"path":"README.md"}},
			{"tool":"read_file","args":{"path":"README.md"},"expect":"error"},
			{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, []string{"step 1", "expected error", "actual ok"}},
		"an ok that is denied": {`{"steps":[
			{"tool":"write_file","args":{"path":".agent-fox/x","content":"x"},"expect":"ok"},
			{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, []string{"step 0", "expected ok", "actual denied"}},
		"an ok that is an error": {`{"steps":[
			{"tool":"read_file","args":{"path":"missing.txt"},"expect":"ok"},
			{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, []string{"step 0", "expected ok", "actual error"}},
		"a denial that is not one": {`{"steps":[
			{"tool":"read_file","args":{"path":"README.md"},"expect":"denied"},
			{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, []string{"step 0", "expected denied", "actual ok"}},
		"an accepted that is refused": {`{"steps":[
			{"submit":{"cite":"missing.txt"},"expect":"accepted"},
			{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, []string{"step 0", "expected accepted", "actual refused"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)

			res, err := runScript(t, w, tc.script, nil)

			f := asFailure(t, err)
			if f.Category != toolio.CategoryBrain {
				t.Errorf("failure = %+v, want failed/brain, the error of Run", f)
			}
			for _, want := range tc.mention {
				if !strings.Contains(f.Message, want) {
					t.Errorf("the message %q lacks %q", f.Message, want)
				}
			}
			if _, err := w.boxFile("after.txt"); err == nil {
				t.Error("a step after the one that failed was run")
			}
			if res.Outcome.StopReason != StopError {
				t.Errorf("stop reason = %q, want error", res.Outcome.StopReason)
			}
		})
	}
}

// TestAnAcceptedSubmissionThatWasExpectedRefused guards 07-REQ-9.3 and 07-REQ-7.7:
// the step really did submit, so the submission wins, and the error that stopped
// the replay is a warning.
func TestAnAcceptedSubmissionThatWasExpectedRefused(t *testing.T) {
	w := newWorld(t)

	res, err := runScript(t, w, `{"steps":[
		{"submit":{"summary":"fine"},"expect":"refused"},
		{"tool":"write_file","args":{"path":"after.txt","content":"x"}}]}`, nil)

	if err != nil || res.Payload == nil {
		t.Fatalf("Execute = %+v, %v; want the submission that was made", res, err)
	}
	warned := w.report.warned("brain_error")
	if len(warned) != 1 || !strings.Contains(warned[0].Message, "step 0") || !strings.Contains(warned[0].Message, "expected refused") || !strings.Contains(warned[0].Message, "actual accepted") {
		t.Errorf("warnings = %+v, want brain_error naming the step, expected refused and actual accepted", w.report.warnings)
	}
	if _, err := w.boxFile("after.txt"); err == nil {
		t.Error("a step after the one that failed was run")
	}
}

// TestAnAcceptedSubmissionEndsTheReplay_TS_07_68 verifies TS-07-68:
// an accepted submit step ends the replay and lists the remaining steps as
// skipped.
// Verifies: 07-REQ-9.4
func TestAnAcceptedSubmissionEndsTheReplay_TS_07_68(t *testing.T) {
	w := newWorld(t)

	res, err := runScript(t, w, `{"steps":[
		{"submit":{"summary":"first"}},
		{"tool":"write_file","args":{"path":"never.txt","content":"x"}},
		{"tool":"write_file","args":{"path":"never2.txt","content":"x"}}
	]}`, nil)

	if err != nil || !res.Outcome.Submitted || res.Outcome.StopReason != StopSubmitted || res.Outcome.Turns != 1 {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	var skipped []float64
	for _, l := range w.transcript() {
		if l["skipped"] == true {
			skipped = append(skipped, l["step"].(float64))
		}
	}
	if fmt.Sprint(skipped) != "[1 2]" {
		t.Errorf("skipped steps = %v, want [1 2]", skipped)
	}
	for _, name := range []string{"never.txt", "never2.txt"} {
		if _, err := w.boxFile(name); err == nil {
			t.Errorf("%s was written by a step that should have been skipped", name)
		}
	}
}

// TestTheTurnBoundStopsTheReplay_TS_07_69 verifies TS-07-69:
// reaching MaxTurns before an accepted submission stops with StopReason turns.
// Verifies: 07-REQ-9.5
func TestTheTurnBoundStopsTheReplay_TS_07_69(t *testing.T) {
	w := newWorld(t)
	step := `{"tool":"list_files","args":{"path":"."}}`

	res, err := runScript(t, w, `{"steps":[`+strings.Repeat(step+",", 4)+step+`]}`, func(b *Brief) { b.MaxTurns = 3 })

	if res.Outcome.StopReason != StopTurns || res.Outcome.Turns != 3 || res.Outcome.Submitted {
		t.Errorf("Outcome = %+v, want turns, 3 turns, not submitted", res.Outcome)
	}
	if f := asFailure(t, err); f.Category != toolio.CategoryBudget || f.FixHint == nil || f.FixHint.Flag != "--max-turns" || f.FixHint.Suggest != "6" {
		t.Errorf("failure = %+v, want failed/budget with --max-turns 6", f)
	}
	if n := len(w.transcript()); n != 3 {
		t.Errorf("%d steps were run, want 3", n)
	}
}

// TestRunningOutOfStepsEnds_TS_07_70 verifies TS-07-70:
// running out of steps without a submission ends with StopReason ended.
// Verifies: 07-REQ-9.6
func TestRunningOutOfStepsEnds_TS_07_70(t *testing.T) {
	w := newWorld(t)
	step := `{"tool":"list_files","args":{"path":"."}}`

	res, err := runScript(t, w, `{"steps":[`+step+`,`+step+`]}`, func(b *Brief) { b.MaxTurns = 10 })

	if res.Outcome.StopReason != StopEnded || res.Outcome.Submitted || res.Outcome.Turns != 2 {
		t.Errorf("Outcome = %+v, want ended after 2 turns", res.Outcome)
	}
	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result: the script ended without a submission", f)
	}
	// As many steps as turns is the end of the script, not the turn bound.
	w2 := newWorld(t)
	res, _ = runScript(t, w2, `{"steps":[`+step+`,`+step+`]}`, func(b *Brief) { b.MaxTurns = 2 })
	if res.Outcome.StopReason != StopEnded || res.Outcome.Turns != 2 {
		t.Errorf("Outcome = %+v, want ended: the steps ran out as the turns did", res.Outcome)
	}
}

// TestTheScriptedBrainCostsNothing_TS_07_71 verifies TS-07-71:
// the scripted brain reports zero cost with source none, steps run as turns and
// writes the transcript.
// Verifies: 07-REQ-9.7
func TestTheScriptedBrainCostsNothing_TS_07_71(t *testing.T) {
	w := newWorld(t)

	res, err := runScript(t, w, `{"steps":[
		{"tool":"list_files","args":{"path":"."}},
		{"tool":"read_file","args":{"path":"README.md"}},
		{"submit":{"summary":"done"}}
	]}`, nil)

	o := res.Outcome
	want := filepath.Join(w.run.Path, "transcript.1.jsonl")
	if err != nil || o.CostUSD != 0 || o.CostSource != CostNone || o.Turns != 3 || o.Transcript != want {
		t.Errorf("Outcome = %+v, %v; want cost 0 from none, 3 turns and the transcript %s", o, err, want)
	}
	if n := len(w.transcript()); n != 3 {
		t.Errorf("%d transcript lines, want one for each step", n)
	}
	if u := w.report.lastUsage(); u.CostSource != CostNone || u.CostUSD != 0 || u.Turns != 3 || u.Brain != ScriptedName {
		t.Errorf("usage = %+v", u)
	}
}

// TestAScriptedBrainThatCannotStart guards 07-REQ-8.3: tools in the brief that
// the box does not have are an error before any step.
func TestAScriptedBrainThatCannotStart(t *testing.T) {
	w := newWorld(t)

	_, err := runScript(t, w, `{"steps":[{"tool":"read_file","args":{"path":"README.md"}}]}`, func(b *Brief) { b.Tools = []string{"read_file", "teleport"} })

	f := asFailure(t, err)
	if f.Category != toolio.CategoryBrain || !strings.Contains(f.Message, "teleport") {
		t.Errorf("failure = %+v, want failed/brain naming teleport", f)
	}
	if data, err := os.ReadFile(filepath.Join(w.run.Path, "transcript.1.jsonl")); err == nil && len(data) > 0 {
		t.Errorf("a step was run before the tools were checked: %s", data)
	}
}

// TestACancelledReplayStops guards 07-REQ-9.2: a context that is cancelled
// stops the replay between steps.
func TestACancelledReplayStops(t *testing.T) {
	w := newWorld(t)
	brain, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[{"tool":"list_files"},{"tool":"list_files"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := w.brief(1)
	b.Tools = []string{"list_files"}

	out, err := brain.Run(ctx, b)

	if err == nil || out.Turns != 0 {
		t.Errorf("Run = %+v, %v; want the context's error and no step", out, err)
	}
}

// TestTheScriptedBrainReplaysRoundKOnTheKthRun_TS_09_69 verifies TS-09-69:
// a script of rounds is replayed one round for each run of the same brain, a run
// past the last round ends at once, and a plain script is one round.
// Verifies: 09-REQ-10.3
func TestTheScriptedBrainReplaysRoundKOnTheKthRun_TS_09_69(t *testing.T) {
	w := newWorld(t)
	round := func(brain Brain, k int) Outcome {
		t.Helper()
		b := w.brief(k)
		b.Kind = citeKind()
		b.Tools = []string{"read_file", "write_file", "list_files", "execute"}
		res, _ := Execute(context.Background(), w.request(b, brain))
		return res.Outcome
	}
	inBox := func(name string) bool {
		_, err := w.boxFile(name)
		return err == nil
	}

	multi, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"rounds":[
		{"steps":[{"tool":"write_file","args":{"path":"r1.txt","content":"one\n"},"expect":"ok"}]},
		{"steps":[{"tool":"write_file","args":{"path":"r2.txt","content":"two\n"},"expect":"ok"},{"tool":"write_file","args":{"path":"r2b.txt","content":"two\n"},"expect":"ok"}]}
	]}`)})
	if err != nil {
		t.Fatal(err)
	}

	first := round(multi, 1)
	if first.Turns != 1 || first.StopReason != StopEnded || !inBox("r1.txt") || inBox("r2.txt") {
		t.Errorf("run 1: %+v, r1 %v, r2 %v; want round 1 alone", first, inBox("r1.txt"), inBox("r2.txt"))
	}
	second := round(multi, 2)
	if second.Turns != 2 || second.StopReason != StopEnded || !inBox("r2.txt") || !inBox("r2b.txt") {
		t.Errorf("run 2: %+v, want round 2: its two steps", second)
	}
	third := round(multi, 3)
	if third.Turns != 0 || third.StopReason != StopEnded || third.Submitted || third.CostUSD != 0 {
		t.Errorf("run 3: %+v, want it ended at once, with no step", third)
	}
	if again := round(multi, 4); again.Turns != 0 || again.StopReason != StopEnded {
		t.Errorf("run 4: %+v, want it ended at once too", again)
	}

	plain, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[{"tool":"write_file","args":{"path":"p.txt","content":"p\n"},"expect":"ok"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	if o := round(plain, 5); o.Turns != 1 || !inBox("p.txt") {
		t.Errorf("the plain script, run 1: %+v, want its one round", o)
	}
	if o := round(plain, 6); o.Turns != 0 || o.StopReason != StopEnded {
		t.Errorf("the plain script, run 2: %+v, want it ended at once: a plain script is one round", o)
	}
}

// TestAScriptOfRoundsIsCheckedAsAScriptIs guards 09-REQ-10.3 at construction: a
// script that mixes the forms, or holds a step that cannot be used in any round,
// is a usage failure that names the round.
func TestAScriptOfRoundsIsCheckedAsAScriptIs(t *testing.T) {
	for name, tc := range map[string]struct{ content, mention string }{
		"both forms":           {`{"steps":[],"rounds":[]}`, "steps"},
		"an unknown key":       {`{"rounds":[{"steps":[],"extra":1}]}`, "extra"},
		"a bad step in round":  {`{"rounds":[{"steps":[{"tool":"read_file"}]},{"steps":[{"expect":"ok"}]}]}`, "round 2 step 0"},
		"rounds that are none": {`{"rounds":{"steps":[]}}`, "script.json"},
	} {
		t.Run(name, func(t *testing.T) {
			b, err := Default.New(ScriptedName, Options{Script: writeScript(t, tc.content)})
			f := asFailure(t, err)
			if b != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, tc.mention) {
				t.Errorf("New = %v, %v; want a usage failure that mentions %q", b, err, tc.mention)
			}
		})
	}
	for _, ok := range []string{`{"rounds":[]}`, `{"rounds":[{"steps":[]},{"steps":[]}]}`} {
		if b, err := Default.New(ScriptedName, Options{Script: writeScript(t, ok)}); err != nil || b == nil {
			t.Errorf("%s: %v, %v", ok, b, err)
		}
	}
}
