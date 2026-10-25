package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestNewAgentkit_NeedsTheCredentialOfTheModelUnlessGivenAProvider(t *testing.T) {
	b, err := Default.New(AgentkitName, Options{Getenv: noEnv})
	if f := asFailure(t, err); b != nil || f.Category != toolio.CategoryAuth || f.FixHint == nil || f.FixHint.Env != VendorKeyVar {
		t.Errorf("no key: %v, %+v; want failed/auth with the hint %s", b, f, VendorKeyVar)
	}
	if b, err := Default.New(AgentkitName, Options{Getenv: envOf(map[string]string{VendorKeyVar: "sk-ant-test"})}); err != nil || b == nil {
		t.Errorf("with a key: %v, %v", b, err)
	}

	var read []string
	spy := func(name string) (string, bool) { read = append(read, name); return "", false }
	var model string
	provider := func(m string) (core.ProviderClient, error) { model = m; return faux.New(), nil }
	if _, err := Default.New(AgentkitName, Options{Getenv: spy, NewProvider: provider}); err != nil || model != defaultModel() || model == "" || len(read) != 0 {
		t.Errorf("with a provider: %v, model %q, read %v; want the catalog's default model and no variable read", err, model, read)
	}
	broken := func(string) (core.ProviderClient, error) { return nil, errors.New("no provider today") }
	if _, err := Default.New(AgentkitName, Options{Getenv: noEnv, NewProvider: broken}); err == nil || !strings.Contains(err.Error(), "no provider today") {
		t.Errorf("a provider that cannot be made: %v, want its error", err)
	}
}

func TestAgentkit_TheFirstRequestCarriesTheBriefAndOnlyTheAllowedTools(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t, faux.Text("nothing to do"))

	_, err := runFaux(w, brain, func(b *Brief) { b.Tools = []string{"read_file"} })

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("a brain that submits nothing: %+v, want no_result", f)
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	r, b := reqs[0], w.brief(1)
	if len(r.Prefix) != 1 || r.Prefix[0].(core.UserMessage).Content.Text() != b.Prefix || len(r.Messages) != 1 || r.Messages[0].(core.UserMessage).Content.Text() != b.Task {
		t.Errorf("prefix %v and messages %v, want the prefix of the brief byte for byte, then the task", r.Prefix, r.Messages)
	}
	var names []string
	for _, tool := range r.Tools {
		names = append(names, tool.Name)
		if tool.Name == "submit_test" && (tool.Description != "a test kind" || tool.InputSchema == nil) {
			t.Errorf("the terminating tool = %+v, want the kind's description and schema", tool)
		}
	}
	if slices.Sort(names); !slices.Equal(names, []string{"read_file", "submit_test"}) {
		t.Errorf("tools = %v, want read_file and the terminating tool", names)
	}
	var system strings.Builder
	for _, block := range r.System {
		if text, ok := block.(core.TextBlock); ok {
			system.WriteString(text.Text)
		}
	}
	for _, hidden := range []string{"write_file", "edit_file", "run_command"} {
		if strings.Contains(system.String(), hidden) {
			t.Errorf("the system prompt names %s, which the brief does not allow", hidden)
		}
	}
}

func TestAgentkit_ADeniedCallAndARefusedSubmissionAreTheNextTurns(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t,
		call("c1", "write_file", `{"path":"a.txt","content":"secret-content"}`),
		call("c2", "teleport", `{}`),
		call("c3", "submit_test", `{"cite":"missing.txt"}`),
		call("c4", "submit_test", `{"cite":"README.md"}`))

	res, err := runFaux(w, brain, nil)

	if err != nil || string(res.Payload) != `{"cite":"README.md"}` || p.Calls() != 4 {
		t.Fatalf("Execute = %+v, %v, %d requests", res, err, p.Calls())
	}
	reqs := p.Requests()
	for i, want := range []string{"tool write_file is not in this run's allowlist", "teleport", "missing.txt is not in the tree; summary: must say what was done"} {
		if got := toolResults(reqs[i+1]); !strings.Contains(got, want) {
			t.Errorf("request %d read %q, want %q", i+2, got, want)
		}
	}
	if _, err := w.boxFile("a.txt"); err == nil {
		t.Error("the denied write was run")
	}
	var decisions []string
	for _, e := range w.report.eventsOf("tool_call") {
		decisions = append(decisions, e["tool"].(string)+":"+e["decision"].(string))
		if strings.Contains(string(must(json.Marshal(e))), "secret-content") {
			t.Errorf("a tool_call event carries an argument: %v", e)
		}
	}
	if strings.Join(decisions, " ") != "write_file:deny submit_test:allow submit_test:allow" {
		t.Errorf("tool_call events = %v; the driver answers a tool it does not have itself", decisions)
	}
	lines := w.transcript(1)
	if len(lines) != 3 || lines[0]["ok"] != false || !strings.Contains(lines[0]["args"].(string), "secret-content") || lines[1]["ok"] != false || lines[2]["ok"] != true {
		t.Errorf("transcript = %v, want the denial with its arguments, the refusal and the acceptance", lines)
	}
}

func TestAgentkit_AnAcceptedSubmissionEndsTheRun(t *testing.T) {
	batch := func(calls ...[2]string) faux.Turn {
		var blocks []core.ContentBlock
		for i, c := range calls {
			blocks = append(blocks, faux.FauxToolCall(fmt.Sprintf("c%d", i), c[0], c[1]))
		}
		return faux.FauxAssistantMessage(core.StopReasonToolUse, blocks...)
	}
	for _, tc := range []struct {
		name string
		turn faux.Turn
		want string
	}{
		{"alone", call("c1", "submit_test", `{"summary":"done"}`), `{"summary":"done"}`},
		{"among other calls", batch([2]string{"read_file", `{"path":"README.md"}`}, [2]string{"submit_test", `{"summary":"done"}`}, [2]string{"list_files", `{"path":"."}`}), `{"summary":"done"}`},
		{"after a refusal in its turn", batch([2]string{"submit_test", `{"cite":"missing.go"}`}, [2]string{"submit_test", `{"cite":"README.md"}`}), `{"cite":"README.md"}`},
		{"before another", batch([2]string{"submit_test", `{"n":1}`}, [2]string{"submit_test", `{"n":2}`}), `{"n":1}`},
	} {
		w := newWorld(t)
		brain, p := fauxBrain(t, tc.turn, faux.Text("must never be requested"))

		res, err := runFaux(w, brain, func(b *Brief) { b.Tools = nil })

		if err != nil || string(res.Payload) != tc.want || res.Outcome.StopReason != StopSubmitted || p.Calls() != 1 {
			t.Errorf("%s: Execute = %+v, %v, %d requests; want %s and one request", tc.name, res, err, p.Calls(), tc.want)
		}
	}
}

func TestAgentkit_MapsTheStopOfTheDriver(t *testing.T) {
	read := call("c1", "read_file", `{"path":"README.md"}`)
	cached := costs(faux.Text("nothing to submit"), 0.002, 1000, 200)
	cached.Usage.SetField(core.UsageCacheReadTokens, 2000)
	cached.Usage.SetField(core.UsageCacheWriteTokens, 300)
	broken := faux.Turn{Err: errors.New("the provider fell over")}
	w := newWorld(t)
	for _, tc := range []struct {
		name          string
		turns         []faux.Turn
		cost          float64
		maxTurns      int
		stop          string
		category      string
		hint, mention string
		in            int
	}{
		{"the cost bound", []faux.Turn{costs(read, 0.5, 1000, 200), faux.Text("never")}, 0.01, 10, StopBudget, toolio.CategoryBudget, "0.02", "", 1000},
		{"the turn bound", []faux.Turn{costs(read, 0.001, 1000, 200), costs(faux.Text("never"), 0.001, 10, 10)}, 1, 1, StopTurns, toolio.CategoryBudget, "2", "", 1000},
		{"the end of the turns, cache included", []faux.Turn{cached}, 1, 10, StopEnded, toolio.CategoryNoResult, "", "", 3300},
		{"an error of the provider", []faux.Turn{costs(read, 0.001, 1000, 200), broken}, 1, 10, StopError, toolio.CategoryBrain, "", "the provider fell over", 1000},
	} {
		brain, _ := fauxBrain(t, tc.turns...)

		res, err := runFaux(w, brain, func(b *Brief) { b.MaxCostUSD, b.MaxTurns = tc.cost, tc.maxTurns })

		o := res.Outcome
		if o.StopReason != tc.stop || o.CostSource != CostMeasured || o.Turns < 1 || o.CostUSD <= 0 || o.InputTokens != tc.in || o.OutputTokens != 200 || o.Submitted {
			t.Errorf("%s: outcome = %+v, want %s with the measured cost, turns and tokens", tc.name, o, tc.stop)
		}
		f := asFailure(t, err)
		if f.Category != tc.category || (tc.hint != "" && (f.FixHint == nil || f.FixHint.Suggest != tc.hint)) || !strings.Contains(f.Message, tc.mention) {
			t.Errorf("%s: failure = %+v, want %s %s %s", tc.name, f, tc.category, tc.hint, tc.mention)
		}
		if u := w.report.lastUsage(); u.CostSource != CostMeasured || u.Brain != AgentkitName {
			t.Errorf("%s: usage = %+v, want the measured cost of the run", tc.name, u)
		}
	}
}

func TestAgentkit_AModelTheCatalogDoesNotPriceHasNoCost(t *testing.T) {
	w := newWorld(t)
	unlisted, err := Default.New(AgentkitName, Options{Model: "a-model-nobody-listed", Getenv: envOf(map[string]string{VendorKeyVar: "key-for-the-test"})})
	if err != nil {
		t.Fatal(err)
	}

	_, err = runFaux(w, unlisted, func(b *Brief) { b.MaxCostUSD = 2 })

	if f := asFailure(t, err); f.Category != toolio.CategoryUsage || f.FixHint == nil || f.FixHint.Env != modelVar || !strings.Contains(f.Message, "a-model-nobody-listed") {
		t.Errorf("a cost bound on it: %+v, want a usage failure that names the model and AF_MODEL", f)
	}

	p := faux.New(costs(call("c1", "submit_test", `{"summary":"done"}`), 0.5, 1000, 100))
	res, err := runFaux(w, &agentkitBrain{model: "faux-1", provider: p}, func(b *Brief) { b.MaxCostUSD = 0 })

	if u := w.report.lastUsage(); err != nil || !res.Outcome.Submitted || u.CostUSD != 0 || u.CostSource != CostNone || len(w.report.warned("model_unpriced")) != 1 {
		t.Errorf("no bound: %+v, %v, usage %+v, warnings %+v; want the submission, no cost, and model_unpriced", res, err, u, w.report.warnings)
	}
}

func TestAgentkit_RunsNothingThatCannotRun(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	b.Box = w.session
	noBox, unknown, schemaless := b, b, b
	noBox.Box = nil
	unknown.Tools = []string{"read_file", "teleport"}
	schemaless.Kind.Schema = []byte(`{"type":`)
	for _, tc := range []struct {
		name    string
		b       Brief
		mention string
	}{{"no box", noBox, "needs a box"}, {"a tool the box lacks", unknown, "teleport"}, {"a kind whose schema cannot be used", schemaless, "schema"}} {
		brain, p := fauxBrain(t, faux.Text("never asked"))

		out, err := brain.Run(context.Background(), tc.b)

		var f *toolio.Failure
		if err == nil || !strings.Contains(err.Error(), tc.mention) || errors.As(err, &f) || out.StopReason != StopError || p.Calls() != 0 {
			t.Errorf("%s: %+v, %v, %d requests; want an internal error that says %q and no request", tc.name, out, err, p.Calls(), tc.mention)
		}
	}
	if _, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err == nil {
		t.Error("a submission was made")
	}
}

func TestAgentkit_RunsOnTheHostUnderAProviderThatDoesNotIsolate(t *testing.T) {
	w := newWorld(t)
	p := faux.New(call("c1", "list_files", `{"path":"."}`), call("c2", "submit_test", `{"summary":"hosted"}`))
	brain, err := Default.New(AgentkitName, Options{Provider: "local", Getenv: noEnv, NewProvider: func(string) (core.ProviderClient, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}

	res, err := runFaux(w, brain, nil)

	if err != nil || !res.Outcome.Submitted || p.Calls() != 2 {
		t.Fatalf("Execute = %+v, %v, %d requests; want the driver on the host to submit", res, err, p.Calls())
	}
	if kinds := w.kinds(); slices.Contains(kinds, "box_probe") || slices.Contains(kinds, "brain_process") {
		t.Errorf("ledger = %v: a process was run in the box", kinds)
	}
}

// call is a faux turn that calls a tool.
func call(id, name, args string) faux.Turn { return faux.ToolCall(id, name, args) }
