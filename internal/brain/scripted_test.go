package brain

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestNewScripted_RefusesAScriptThatCannotBeUsed(t *testing.T) {
	for name, tc := range map[string]struct{ content, mention string }{
		"not JSON":                          {`{steps`, "is not a script"},
		"an unknown field":                  {`{"steps":[],"extra":1}`, "extra"},
		"an unknown key of a step":          {`{"steps":[{"tool":"read_file","oops":1}]}`, "oops"},
		"text after the object":             {`{"steps":[]} trailing`, "more than one JSON document"},
		"steps that are no list":            {`{"steps":{"tool":"x"}}`, "is not a script"},
		"a step of both forms":              {`{"steps":[{"tool":"read_file"},{"tool":"read_file","submit":{}}]}`, "step 1 has both"},
		"a step of neither form":            {`{"steps":[{"expect":"ok"}]}`, "step 0 has neither"},
		"an empty tool name":                {`{"steps":[{"tool":""}]}`, "step 0 has an empty tool name"},
		"args on a submit":                  {`{"steps":[{"submit":{"a":1},"args":{}}]}`, "step 0 is a submit step with args"},
		"a tool step that expects accepted": {`{"steps":[{"tool":"read_file","expect":"accepted"}]}`, `step 0 expects "accepted"`},
		"a submit step that expects ok":     {`{"steps":[{"submit":{"a":1},"expect":"ok"}]}`, `step 0 expects "ok"`},
		"both steps and rounds":             {`{"steps":[],"rounds":[]}`, "has both steps and rounds"},
		"an unknown key of a round":         {`{"rounds":[{"steps":[],"extra":1}]}`, "extra"},
		"a bad step in a round":             {`{"rounds":[{"steps":[{"tool":"read_file"}]},{"steps":[{"expect":"ok"}]}]}`, "round 2 step 0"},
	} {
		path := writeScript(t, tc.content)
		b, err := Default.New(ScriptedName, Options{Script: path})
		f := asFailure(t, err)
		if b != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, path) || !strings.Contains(f.Message, tc.mention) {
			t.Errorf("%s: %v, %+v; want a usage failure that names the file and says %q", name, b, f, tc.mention)
		}
	}
	missing := filepath.Join(t.TempDir(), "nowhere.json")
	for _, opts := range []Options{{}, {Script: missing}} {
		if f := asFailure(t, func() error { _, err := Default.New(ScriptedName, opts); return err }()); f.Category != toolio.CategoryUsage {
			t.Errorf("script %q: %+v, want a usage failure", opts.Script, f)
		}
	}
	for _, ok := range []string{`{"steps":[]}`, `{"rounds":[]}`, `{"rounds":[{"steps":[]},{"steps":[]}]}`} {
		if b, err := Default.New(ScriptedName, Options{Script: writeScript(t, ok)}); err != nil || b == nil {
			t.Errorf("%s: %v, %v", ok, b, err)
		}
	}
}

func TestScripted_ReplaysTheStepsInOrderOverTheGate(t *testing.T) {
	w := newWorld(t)

	res, err := runScript(t, w, `{"steps":[
		{"tool":"write_file","args":{"path":"notes/a.txt","content":"hello from the script\n"},"expect":"ok"},
		{"tool":"read_file","args":{"path":"notes/a.txt"},"expect":"ok"},
		{"tool":"edit_file","args":{"path":"notes/a.txt"},"expect":"denied"},
		{"tool":"read_file","args":{"path":"missing.txt"},"expect":"error"},
		{"submit":{"cite":"missing.txt"},"expect":"refused"},
		{"submit":{"summary":"done","cite":"notes/a.txt"},"expect":"accepted"}
	]}`, nil)

	o := res.Outcome
	if err != nil || string(res.Payload) != `{"summary":"done","cite":"notes/a.txt"}` || !o.Submitted || o.StopReason != StopSubmitted || o.Turns != 6 ||
		o.CostUSD != 0 || o.CostSource != CostNone || o.Transcript != filepath.Join(w.run.Path, "transcript.1.jsonl") {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	lines := w.transcript(1)
	var tools []string
	for _, l := range lines {
		tools = append(tools, l["tool"].(string)+":"+l["decision"].(string))
	}
	if strings.Join(tools, " ") != "write_file:allow read_file:allow edit_file:deny read_file:allow submit_test:allow submit_test:allow" {
		t.Errorf("the transcript has %v, want the steps in order, a submission as the kind's tool", tools)
	}
	if result := lines[1]["result"].(string); !strings.Contains(result, "hello from the script") {
		t.Errorf("the read step saw %q, want what the write step wrote", result)
	}
	if lines[2]["reason"] != "tool edit_file is not in this run's allowlist" || !strings.Contains(lines[4]["result"].(string), "missing.txt is not in the tree") {
		t.Errorf("the denial %v and the refusal %v, want their reasons", lines[2], lines[4])
	}
	if _, err := os.Stat(filepath.Join(w.dir, "notes")); err == nil {
		t.Error("a write of the script reached the host checkout")
	}
	if u := w.report.lastUsage(); u.CostSource != CostNone || u.Turns != 6 {
		t.Errorf("usage = %+v", u)
	}
}

func TestScripted_AStepThatIsNotWhatItExpectsStopsTheReplay(t *testing.T) {
	w := newWorld(t)
	after := `{"tool":"write_file","args":{"path":"after.txt","content":"x"}}`
	for _, tc := range []struct{ step, mention string }{
		{`{"tool":"read_file","args":{"path":"README.md"},"expect":"error"}`, "expected error, actual ok"},
		{`{"tool":"write_file","args":{"path":".agent-fox/x","content":"x"},"expect":"ok"}`, "expected ok, actual denied"},
		{`{"tool":"read_file","args":{"path":"missing.txt"},"expect":"ok"}`, "expected ok, actual error"},
		{`{"tool":"read_file","args":{"path":"README.md"},"expect":"denied"}`, "expected denied, actual ok"},
		{`{"submit":{"cite":"missing.txt"},"expect":"accepted"}`, "expected accepted, actual refused"},
	} {
		res, err := runScript(t, w, `{"steps":[{"tool":"list_files"},`+tc.step+`,`+after+`]}`, nil)

		f := asFailure(t, err)
		if f.Category != toolio.CategoryBrain || !strings.Contains(f.Message, "step 1: "+tc.mention) || res.Outcome.StopReason != StopError {
			t.Errorf("%s: %+v, stop %q; want failed/brain that says step 1: %s", tc.step, f, res.Outcome.StopReason, tc.mention)
		}
		if _, err := w.boxFile("after.txt"); err == nil {
			t.Fatalf("%s: the step after the one that failed was run", tc.step)
		}
	}
}

func TestScripted_AnAcceptedSubmissionEndsTheReplay(t *testing.T) {
	never := `{"tool":"write_file","args":{"path":"never.txt","content":"x"}}`
	for _, tc := range []struct{ expect, warning string }{{"", ""}, {"refused", "step 0: expected refused, actual accepted"}} {
		w := newWorld(t)

		res, err := runScript(t, w, `{"steps":[{"submit":{"summary":"first"},"expect":"`+tc.expect+`"},`+never+`,{"submit":{"n":2}}]}`, nil)

		// A step that did submit has submitted, whatever it expected.
		if err != nil || string(res.Payload) != `{"summary":"first"}` || res.Outcome.Turns != 1 {
			t.Fatalf("expect %q: Execute = %+v, %v", tc.expect, res, err)
		}
		if got := w.report.warned("brain_error"); (tc.warning == "") != (len(got) == 0) || (len(got) > 0 && !strings.Contains(got[0], tc.warning)) {
			t.Errorf("expect %q: warnings = %+v, want brain_error saying %q, if anything", tc.expect, w.report.warnings, tc.warning)
		}
		if tc.expect == "" {
			lines := w.transcript(1)
			if len(lines) != 3 || lines[1]["skipped"] != true || lines[1]["step"] != 1.0 || lines[1]["what"] != "tool write_file" || lines[2]["what"] != "submit" {
				t.Errorf("the transcript = %v, want the steps after the submission listed as skipped", lines)
			}
		}
		if _, err := w.boxFile("never.txt"); err == nil {
			t.Error("a step after the submission was run")
		}
	}
}

func TestScripted_StopsAtTheTurnBoundOrTheEndOfItsSteps(t *testing.T) {
	step := `{"tool":"list_files","args":{"path":"."}}`
	w := newWorld(t)
	for _, tc := range []struct {
		steps, maxTurns, turns int
		stop, category         string
	}{
		{5, 3, 3, StopTurns, toolio.CategoryBudget},
		{2, 10, 2, StopEnded, toolio.CategoryNoResult},
		{2, 2, 2, StopEnded, toolio.CategoryNoResult}, // the steps ran out as the turns did
	} {
		script := `{"steps":[` + strings.TrimSuffix(strings.Repeat(step+",", tc.steps), ",") + `]}`

		res, err := runScript(t, w, script, func(b *Brief) { b.MaxTurns = tc.maxTurns })

		if o := res.Outcome; o.StopReason != tc.stop || o.Turns != tc.turns || o.Submitted || asFailure(t, err).Category != tc.category {
			t.Errorf("%d steps in %d turns: %+v, %v; want %s after %d turns", tc.steps, tc.maxTurns, o, err, tc.stop, tc.turns)
		}
		if n := len(w.transcript(1)); n != tc.turns {
			t.Errorf("%d steps in %d turns: %d steps were run", tc.steps, tc.maxTurns, n)
		}
	}
}

func TestScripted_ReplaysOneRoundForEachRun(t *testing.T) {
	w := newWorld(t)
	write := func(name string) string {
		return `{"tool":"write_file","args":{"path":"` + name + `","content":"x"},"expect":"ok"}`
	}
	brain, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"rounds":[{"steps":[`+write("r1.txt")+`]},{"steps":[`+write("r2.txt")+`,`+write("r2b.txt")+`]}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[`+write("p.txt")+`]}`)})
	if err != nil {
		t.Fatal(err)
	}

	for i, tc := range []struct {
		brain Brain
		turns int
		made  string
	}{{brain, 1, "r1.txt"}, {brain, 2, "r2b.txt"}, {brain, 0, ""}, {brain, 0, ""}, {plain, 1, "p.txt"}, {plain, 0, ""}} {
		before, _ := w.boxFile(tc.made)
		res, _ := Execute(context.Background(), w.request(w.brief(i+1), tc.brain))
		after, _ := w.boxFile(tc.made)
		if res.Outcome.Turns != tc.turns || res.Outcome.StopReason != StopEnded || (tc.made != "" && (before != "" || after != "x")) {
			t.Errorf("run %d: %+v, want %d turns that wrote %q", i+1, res.Outcome, tc.turns, tc.made)
		}
	}
	if _, err := w.boxFile("r2.txt"); err != nil {
		t.Error("the second run did not replay the first step of the second round")
	}
}

func TestScripted_RunsNothingThatCannotRun(t *testing.T) {
	w := newWorld(t)
	script := writeScript(t, `{"steps":[{"tool":"list_files"}]}`)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	b := w.brief(1)
	b.Box, b.Tools, b.TranscriptPath = w.session, []string{"list_files"}, filepath.Join(t.TempDir(), "t.jsonl")
	noBox, unknown, noTranscript := b, b, b
	noBox.Box = nil
	unknown.Tools = []string{"list_files", "teleport"}
	noTranscript.TranscriptPath = filepath.Join(b.TranscriptPath, "below")
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		b       Brief
		mention string
	}{
		{"no box", context.Background(), noBox, "needs a box"},
		{"a tool the box lacks", context.Background(), unknown, "teleport"},
		{"a transcript that cannot be written", context.Background(), noTranscript, "cannot be written"},
		{"a cancelled context", cancelled, b, "canceled"},
	} {
		brain, err := Default.New(ScriptedName, Options{Script: script})
		if err != nil {
			t.Fatal(err)
		}

		out, err := brain.Run(tc.ctx, tc.b)

		var f *toolio.Failure
		if err == nil || !strings.Contains(err.Error(), tc.mention) || errors.As(err, &f) || out.Turns != 0 || out.StopReason != StopError {
			t.Errorf("%s: %+v, %v; want an error that says %q and no step", tc.name, out, err, tc.mention)
		}
	}
	if data, _ := os.ReadFile(b.TranscriptPath); len(data) != 0 {
		t.Errorf("a step was run: %s", data)
	}
	if _, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err == nil {
		t.Error("a submission was made")
	}
}
