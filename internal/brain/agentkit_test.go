package brain

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
)

// noEnv is a host environment with nothing in it.
func noEnv(string) (string, bool) { return "", false }

// fauxBrain is the agentkit brain over a faux provider that plays the turns.
func fauxBrain(t *testing.T, turns ...faux.Turn) (Brain, *faux.Provider) {
	t.Helper()
	p := faux.New(turns...)
	b, err := Default.New(AgentkitName, Options{
		Model: "faux-1", Getenv: noEnv,
		NewProvider: func(string) (core.ProviderClient, error) { return p, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

// usage is a faux usage with a cost.
func usage(cost float64, in, out int64) core.Usage {
	var u core.Usage
	u.SetField(core.UsageInputTokens, in)
	u.SetField(core.UsageOutputTokens, out)
	u.SetCost(cost)
	return u
}

// withUsage gives a turn a usage.
func withUsage(t faux.Turn, u core.Usage) faux.Turn {
	t.Usage = u
	return t
}

// call is a faux turn that calls a tool.
func call(id, name, args string) faux.Turn { return faux.ToolCall(id, name, args) }

// runFaux runs the brain through Execute over a real box, as the loop does.
func runFaux(t *testing.T, w *world, brain Brain, mutate func(*Brief)) (Result, error) {
	t.Helper()
	b := w.brief(1)
	b.Kind = citeKind()
	b.Tools = []string{"read_file", "list_files"}
	if mutate != nil {
		mutate(&b)
	}
	req := w.request(b, brain)
	req.Name = AgentkitName
	return Execute(context.Background(), req)
}

// toolResults are the texts of the tool results among the messages of a request.
func toolResults(req core.Request) []string {
	var out []string
	for _, m := range req.Messages {
		if tr, ok := m.(core.ToolResultMessage); ok {
			out = append(out, tr.Content.Text())
		}
	}
	return out
}

// lastToolResult is the text of the last tool result of a request.
func lastToolResult(t *testing.T, req core.Request) string {
	t.Helper()
	results := toolResults(req)
	if len(results) == 0 {
		t.Fatalf("the request has no tool result: %+v", req.Messages)
	}
	return results[len(results)-1]
}

// toolNames are the names of the tools a request offers.
func toolNames(req core.Request) []string {
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// TestTheDefaultRegistryHasBothBrains_TS_07_2 verifies TS-07-2:
// an unknown brain name is a usage failure that lists the registered names.
// Verifies: 07-REQ-1.2
func TestTheDefaultRegistryHasBothBrains_TS_07_2(t *testing.T) {
	if names := Default.Names(); !slices.Equal(names, []string{"agentkit", "scripted"}) {
		t.Errorf("the default registry has %v, want agentkit and scripted", names)
	}

	b, err := Default.New("claude-x", Options{})

	f := asFailure(t, err)
	if b != nil || f.Category != toolio.CategoryUsage || !strings.Contains(f.Message, "agentkit") || !strings.Contains(f.Message, "scripted") || !strings.Contains(f.Message, "claude-x") {
		t.Errorf("New = %v, %v; want a usage failure that lists agentkit and scripted", b, err)
	}
	for _, name := range []string{AgentkitName, ScriptedName} {
		if e, err := Default.Lookup(name); err != nil || len(e.CredentialNames(PlacementHost)) != 0 {
			t.Errorf("%s: %+v, %v; want no box environment for a brain on the host", name, e, err)
		}
	}
	if e, _ := Default.Lookup(AgentkitName); e.CostSource != CostMeasured {
		t.Errorf("the agentkit brain's cost source is %q, want measured", e.CostSource)
	}
}

// TestHostTokensDoNotReachTheBox_TS_07_9 verifies TS-07-9:
// with host tokens set, the agentkit and scripted boxes carry an empty
// environment.
// Verifies: 07-REQ-1.8
func TestHostTokensDoNotReachTheBox_TS_07_9(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	t.Setenv("GITLAB_TOKEN", "glpat_secret")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-secret")
	for _, name := range []string{AgentkitName, ScriptedName} {
		t.Run(name, func(t *testing.T) {
			entry, err := Default.Lookup(name)
			if err != nil {
				t.Fatal(err)
			}
			env := BuildBoxEnv(entry, PlacementHost, os.LookupEnv)
			dir := newRepo(t)
			rd, err := toolio.NewRunDir(dir, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			s, err := box.Open(context.Background(), box.Request{Provider: "local", HostDir: dir, RunID: rd.ID, Tool: "fix", Env: env})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			b := Brief{Box: s, Env: env}

			if len(s.Spec().Env) != 0 || len(b.Env) != 0 {
				t.Errorf("Spec().Env = %v, Brief.Env = %v, want both empty", s.Spec().Env, b.Env)
			}
		})
	}
}

// TestTheSubmitToolNeedsNoAllowlist_TS_07_58 verifies TS-07-58:
// the terminating submit tool works with an empty Brief.Tools.
// Verifies: 07-REQ-8.2
func TestTheSubmitToolNeedsNoAllowlist_TS_07_58(t *testing.T) {
	w := newWorld(t)
	brain, _ := fauxBrain(t, call("c1", "submit_test", `{"summary":"done"}`))

	res, err := runFaux(t, w, brain, func(b *Brief) { b.Tools = nil })

	if err != nil || !res.Outcome.Submitted || res.Outcome.StopReason != StopSubmitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	if got, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err != nil || !strings.Contains(got, `"summary":"done"`) {
		t.Errorf("the box's submission = %q, %v", got, err)
	}
}

// TestBothBrainsRefuseAnUnknownTool_TS_07_59 verifies TS-07-59:
// both in-process brains fail at Run start for an unknown tool name before any
// model call or step.
// Verifies: 07-REQ-8.3
func TestBothBrainsRefuseAnUnknownTool_TS_07_59(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	b.Tools = []string{"read_file", "teleport"}
	b.Box = w.session

	brain, p := fauxBrain(t, faux.Text("never asked"))
	_, err := brain.Run(context.Background(), b)
	var f *toolio.Failure
	if err == nil || !strings.Contains(err.Error(), "teleport") || errors.As(err, &f) {
		t.Errorf("agentkit Run = %v, want an internal error that names teleport", err)
	}
	if p.Calls() != 0 {
		t.Errorf("the faux provider got %d requests", p.Calls())
	}

	scripted, err := Default.New(ScriptedName, Options{Script: writeScript(t, `{"steps":[{"tool":"list_files"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	b.TranscriptPath = filepath.Join(t.TempDir(), "t.jsonl")
	out, err := scripted.Run(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "teleport") || errors.As(err, &f) || out.Turns != 0 {
		t.Errorf("scripted Run = %+v, %v; want an internal error that names teleport and no step", out, err)
	}
	if data, rerr := os.ReadFile(b.TranscriptPath); rerr == nil && len(data) > 0 {
		t.Errorf("the scripted brain wrote steps before it checked the tools: %s", data)
	}
}

// TestADenialIsTheNextTurn_TS_07_62 verifies TS-07-62:
// a denial reason is the brain's next turn for both in-process brains.
// Verifies: 07-REQ-8.6
func TestADenialIsTheNextTurn_TS_07_62(t *testing.T) {
	const reason = "tool write_file is not in this run's allowlist"
	t.Run("agentkit", func(t *testing.T) {
		w := newWorld(t)
		brain, p := fauxBrain(t,
			call("c1", "write_file", `{"path":"a.txt","content":"x"}`),
			call("c2", "submit_test", `{"summary":"done"}`))

		_, err := runFaux(t, w, brain, nil)

		if err != nil {
			t.Fatal(err)
		}
		reqs := p.Requests()
		if len(reqs) < 2 || !strings.Contains(lastToolResult(t, reqs[1]), reason) {
			t.Fatalf("the second request's tool result is not the denial: %v", toolResults(reqs[len(reqs)-1]))
		}
		if _, err := w.boxFile("a.txt"); err == nil {
			t.Error("the denied write was executed")
		}
	})
	t.Run("scripted", func(t *testing.T) {
		w := newWorld(t)

		res, err := runScript(t, w, `{"steps":[
			{"tool":"write_file","args":{"path":"a.txt","content":"x"},"expect":"denied"},
			{"submit":{"summary":"done"},"expect":"accepted"}]}`, func(b *Brief) { b.Tools = []string{"read_file"} })

		if err != nil || !res.Outcome.Submitted {
			t.Fatalf("Execute = %+v, %v", res, err)
		}
		tr := w.transcript()
		if tr[0]["decision"] != DecisionDeny || tr[0]["reason"] != reason || len(tr) != 2 {
			t.Errorf("the first transcript line = %v, want the denial with its reason, and the replay going on", tr[0])
		}
	})
}

// TestTheFirstRequestCarriesTheBrief_TS_07_72 verifies TS-07-72:
// the agentkit brain's first faux request carries the prefix, the task and the
// terminating tool.
// Verifies: 07-REQ-10.1
func TestTheFirstRequestCarriesTheBrief_TS_07_72(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t, faux.Text("nothing to do"))

	_, err := runFaux(t, w, brain, func(b *Brief) { b.Tools = []string{"read_file"} })

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("a brain that submits nothing: %+v, want no_result", f)
	}
	reqs := p.Requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	r := reqs[0]
	b := w.brief(1)
	if len(r.Prefix) != 1 || userText(r.Prefix[0]) != b.Prefix {
		t.Errorf("prefix = %v, want the brief's prefix byte for byte", r.Prefix)
	}
	if len(r.Messages) != 1 || userText(r.Messages[0]) != b.Task {
		t.Errorf("messages = %v, want the task as the first user message", r.Messages)
	}
	if got := toolNames(r); !slices.Equal(got, []string{"read_file", "submit_test"}) {
		t.Errorf("tools = %v, want read_file and the terminating tool, and no other box tool", got)
	}
	for _, tool := range r.Tools {
		if tool.Name == "submit_test" && (tool.Description != "a test kind" || tool.InputSchema == nil) {
			t.Errorf("the terminating tool = %+v, want the kind's description and schema", tool)
		}
	}
}

// userText is the text of a user message.
func userText(m core.Message) string {
	if u, ok := m.(core.UserMessage); ok {
		return u.Content.Text()
	}
	return ""
}

// TestAMissingCredentialIsAuth_TS_07_73 verifies TS-07-73:
// a missing vendor credential fails agentkit construction as failed/auth with
// an ANTHROPIC_API_KEY hint and no box.
// Verifies: 07-REQ-10.2
func TestAMissingCredentialIsAuth_TS_07_73(t *testing.T) {
	b, err := Default.New(AgentkitName, Options{Getenv: noEnv})

	f := asFailure(t, err)
	if b != nil || f.Category != toolio.CategoryAuth || f.FixHint == nil || f.FixHint.Env != "ANTHROPIC_API_KEY" {
		t.Errorf("New = %v, %v; want failed/auth with the hint ANTHROPIC_API_KEY", b, err)
	}
	// With a key the brain is built, and nothing is asked of the network.
	present := func(name string) (string, bool) {
		if name == "ANTHROPIC_API_KEY" {
			return "sk-ant-test", true
		}
		return "", false
	}
	if b, err := Default.New(AgentkitName, Options{Getenv: present}); err != nil || b == nil {
		t.Errorf("New with a key = %v, %v", b, err)
	}
	// An injected provider needs no credential.
	if _, p := fauxBrain(t); p == nil {
		t.Error("no faux provider")
	}
}

// twoProblemsKind refuses a payload that cites a path that is not in the tree,
// with two problems.
func twoProblemsKind() submission.Kind {
	k := testKind()
	k.Validate = func(_ context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		var p struct {
			Cite string `json:"cite"`
		}
		if err := json.Unmarshal(payload, &p); err != nil {
			return nil, []submission.Problem{{Message: err.Error()}}
		}
		if _, err := fs.Stat(tree, p.Cite); err != nil {
			return nil, []submission.Problem{
				{Path: "cite", Message: p.Cite + " is not in the tree"},
				{Path: "summary", Message: "must say what was done"},
			}
		}
		return payload, nil
	}
	return k
}

// TestARefusedSubmissionIsTheNextTurn_TS_07_74 verifies TS-07-74:
// a refused terminating-tool payload returns every problem as the next faux turn
// and writes no file.
// Verifies: 07-REQ-10.3
func TestARefusedSubmissionIsTheNextTurn_TS_07_74(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t, call("c1", "submit_test", `{"cite":"missing.txt"}`), faux.Text("I give up"))

	_, err := runFaux(t, w, brain, func(b *Brief) { b.Kind = twoProblemsKind() })

	if f := asFailure(t, err); f.Category != toolio.CategoryNoResult {
		t.Errorf("failure = %+v, want no_result: the brain gave up", f)
	}
	reqs := p.Requests()
	if len(reqs) != 2 {
		t.Fatalf("%d requests, want 2: the loop goes on after a refusal", len(reqs))
	}
	got := lastToolResult(t, reqs[1])
	for _, want := range []string{"missing.txt is not in the tree", "must say what was done"} {
		if !strings.Contains(got, want) {
			t.Errorf("the next turn %q does not carry %q", got, want)
		}
	}
	if _, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err == nil {
		t.Error("a submission file exists after a refusal")
	}
}

// TestAnAcceptedSubmissionEndsTheLoop_TS_07_75 verifies TS-07-75:
// an accepted terminating-tool submission ends the loop with StopReason
// submitted and no further request.
// Verifies: 07-REQ-10.4
func TestAnAcceptedSubmissionEndsTheLoop_TS_07_75(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t, call("c1", "submit_test", `{"summary":"done"}`), faux.Text("must never be requested"))

	res, err := runFaux(t, w, brain, nil)

	if err != nil || !res.Outcome.Submitted || res.Outcome.StopReason != StopSubmitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	if p.Calls() != 1 {
		t.Errorf("%d requests, want exactly 1", p.Calls())
	}
	if _, err := w.boxFile(submission.SubmissionPath(w.run.ID)); err != nil {
		t.Errorf("the submission file is not in the box: %v", err)
	}
}

// TestADriverThatStopsIsMapped_TS_07_76 verifies TS-07-76:
// the agentkit brain reports measured cost, turns and tokens and maps driver
// stops to budget, turns and ended.
// Verifies: 07-REQ-10.5
func TestADriverThatStopsIsMapped_TS_07_76(t *testing.T) {
	read := `{"path":"README.md"}`
	for name, tc := range map[string]struct {
		turns    []faux.Turn
		mutate   func(*Brief)
		stop     string
		category string
		suggest  string
	}{
		"the cost bound": {
			[]faux.Turn{withUsage(call("c1", "read_file", read), usage(0.5, 1000, 200)), faux.Text("never")},
			func(b *Brief) { b.MaxCostUSD = 0.01 }, StopBudget, toolio.CategoryBudget, "0.02"},
		"the turn bound": {
			[]faux.Turn{withUsage(call("c1", "read_file", read), usage(0.001, 1000, 200)), withUsage(faux.Text("never"), usage(0.001, 10, 10))},
			func(b *Brief) { b.MaxTurns = 1 }, StopTurns, toolio.CategoryBudget, "2"},
		"a natural end": {
			[]faux.Turn{withUsage(faux.Text("I am done, and have nothing to submit"), usage(0.002, 1000, 200))},
			nil, StopEnded, toolio.CategoryNoResult, ""},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			brain, _ := fauxBrain(t, tc.turns...)

			res, err := runFaux(t, w, brain, tc.mutate)

			o := res.Outcome
			if o.StopReason != tc.stop || o.CostSource != CostMeasured || o.Turns < 1 || o.CostUSD <= 0 || o.InputTokens <= 0 || o.OutputTokens <= 0 || o.Submitted {
				t.Errorf("Outcome = %+v, want stop %s, measured cost, turns and tokens", o, tc.stop)
			}
			f := asFailure(t, err)
			if f.Category != tc.category || (tc.suggest != "" && (f.FixHint == nil || f.FixHint.Suggest != tc.suggest)) {
				t.Errorf("failure = %+v, want %s %s", f, tc.category, tc.suggest)
			}
			if u := w.report.lastUsage(); u.CostSource != CostMeasured || u.CostUSD != o.CostUSD || u.Brain != AgentkitName {
				t.Errorf("usage = %+v, want the measured cost of the run", u)
			}
		})
	}
}

// TestTheInputTokensIncludeTheCache guards 07-REQ-10.5: the tokens of the
// outcome are what the driver counted, the reads and the writes of the cache
// among the input.
func TestTheInputTokensIncludeTheCache(t *testing.T) {
	w := newWorld(t)
	u := usage(0.01, 1000, 200)
	u.SetField(core.UsageCacheReadTokens, 2000)
	u.SetField(core.UsageCacheWriteTokens, 300)
	brain, _ := fauxBrain(t, withUsage(faux.Text("nothing to submit"), u))

	res, _ := runFaux(t, w, brain, nil)

	if o := res.Outcome; o.InputTokens != 3300 || o.OutputTokens != 200 {
		t.Errorf("tokens = %d in, %d out; want 3300 (1000 + 2000 cache reads + 300 cache writes) and 200", o.InputTokens, o.OutputTokens)
	}
}

// TestThePathOfARecovery guards 07-PATH-2: a denied tool, a refused submission
// and a corrected one, with every call told to Observe, in the transcript and as
// events that carry no arguments.
func TestThePathOfARecovery(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t,
		call("c1", "write_file", `{"path":"a.txt","content":"secret-content"}`),
		call("c2", "submit_test", `{"cite":"missing.txt"}`),
		call("c3", "submit_test", `{"cite":"README.md"}`))

	res, err := runFaux(t, w, brain, func(b *Brief) { b.Kind = twoProblemsKind() })

	if err != nil || !res.Outcome.Submitted || p.Calls() != 3 {
		t.Fatalf("Execute = %+v, %v, %d requests", res, err, p.Calls())
	}
	var decisions []string
	for _, e := range w.report.eventsOf("tool_call") {
		decisions = append(decisions, e.data["tool"].(string)+":"+e.data["decision"].(string))
		if strings.Contains(string(mustJSON(e.data)), "secret-content") {
			t.Errorf("a tool_call event carries an argument: %v", e.data)
		}
	}
	if !slices.Equal(decisions, []string{"write_file:deny", "submit_test:allow", "submit_test:allow"}) {
		t.Errorf("tool_call events = %v", decisions)
	}
	tr := w.transcript()
	if len(tr) != 3 || tr[0]["ok"] != false || tr[1]["ok"] != false || tr[2]["ok"] != true || !strings.Contains(tr[0]["args"].(string), "secret-content") {
		t.Errorf("transcript = %v, want a denial, a refusal and an acceptance, with the arguments", tr)
	}
	if string(res.Payload) != `{"cite":"README.md"}` {
		t.Errorf("payload = %s", res.Payload)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// TestASubmissionInAMixedBatchStillEndsTheLoop guards 07-REQ-10.4: a model that
// submits in the same turn as another call has submitted, and the loop does not
// go on to another request.
func TestASubmissionInAMixedBatchStillEndsTheLoop(t *testing.T) {
	w := newWorld(t)
	both := faux.FauxAssistantMessage(core.StopReasonToolUse,
		faux.FauxToolCall("c1", "read_file", `{"path":"README.md"}`),
		faux.FauxToolCall("c2", "submit_test", `{"summary":"done"}`),
		faux.FauxToolCall("c3", "list_files", `{"path":"."}`))
	brain, p := fauxBrain(t, both, faux.Text("must never be requested"))

	res, err := runFaux(t, w, brain, nil)

	if err != nil || !res.Outcome.Submitted || res.Outcome.StopReason != StopSubmitted || p.Calls() != 1 {
		t.Errorf("Execute = %+v, %v, %d requests; want a submission and one request", res, err, p.Calls())
	}
}

// TestAToolTheModelMakesUpIsDeniedLikeAny guards 07-REQ-8.1: a call to a tool the
// box has and the brief does not allow is a denial, not an unknown tool, and a
// call to one the box does not have is the driver's own error.
func TestAToolTheModelMakesUpIsDeniedLikeAny(t *testing.T) {
	w := newWorld(t)
	brain, p := fauxBrain(t,
		call("c1", "execute", `{"command":"echo hi > outside.txt"}`),
		call("c2", "teleport", `{}`),
		call("c3", "submit_test", `{"summary":"done"}`))

	res, err := runFaux(t, w, brain, nil)

	if err != nil || !res.Outcome.Submitted {
		t.Fatalf("Execute = %+v, %v", res, err)
	}
	reqs := p.Requests()
	if got := lastToolResult(t, reqs[1]); !strings.Contains(got, "tool execute is not in this run's allowlist") {
		t.Errorf("a call to execute: %q", got)
	}
	if got := lastToolResult(t, reqs[2]); !strings.Contains(got, "teleport") {
		t.Errorf("a call to a tool that is not there: %q", got)
	}
	if _, err := w.boxFile("outside.txt"); err == nil {
		t.Error("the denied command was executed")
	}
}
