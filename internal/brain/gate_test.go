package brain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/core"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// spyTools are box tools that count the calls they run.
type spyTools struct{ ran atomic.Int32 }

func (s *spyTools) tools(names ...string) []core.Tool {
	var tools []core.Tool
	for _, n := range names {
		tools = append(tools, core.Tool{Name: n, Execute: func(context.Context, json.RawMessage) core.ToolResult {
			s.ran.Add(1)
			return core.ToolResult{OK: true, Text: "done " + n}
		}})
	}
	return tools
}

// observed collects what a gate tells Observe.
type observed struct {
	mu    sync.Mutex
	calls []ToolCall
}

func (o *observed) observe(c ToolCall) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.calls = append(o.calls, c)
}

// spyGate is a gate over spy tools in /work/box, with a transcript.
func spyGate(t *testing.T, allowed []string, hook HookConfig) (*Gate, *spyTools, *observed, string) {
	t.Helper()
	spy, obs := &spyTools{}, &observed{}
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	tr, err := NewTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	g, err := NewGate(GateConfig{
		Brain: ScriptedName, Allowed: allowed, Terminating: "submit_test", Hook: hook, Dir: "/work/box",
		Box: spy.tools("read_file", "write_file", "edit_file", "execute", "list_files"), Observe: obs.observe, Transcript: tr,
	})
	if err != nil {
		t.Fatal(err)
	}
	return g, spy, obs, path
}

func TestGate_DeniesAToolOutsideTheAllowlistWithoutRunningIt(t *testing.T) {
	g, spy, obs, _ := spyGate(t, []string{"read_file"}, HookConfig{})

	r := g.Call(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`))

	const reason = "tool write_file is not in this run's allowlist"
	if r.OK || r.Error != core.BlockErrorCode || r.Text != reason || r.Detail != reason || spy.ran.Load() != 0 {
		t.Errorf("result = %+v, %d run; want a block that says %q and nothing run", r, spy.ran.Load(), reason)
	}
	if len(obs.calls) != 1 || obs.calls[0] != (ToolCall{Brain: ScriptedName, Tool: "write_file", Decision: DecisionDeny, Reason: reason, DurationMS: obs.calls[0].DurationMS}) {
		t.Errorf("observed = %+v, want the denial", obs.calls)
	}
	if r := g.Call(context.Background(), "read_file", json.RawMessage(`{"path":"a.txt"}`)); !r.OK || spy.ran.Load() != 1 {
		t.Errorf("an allowed call = %+v, %d run", r, spy.ran.Load())
	}
}

func TestGate_AllowsTheTerminatingToolWhateverTheAllowlist(t *testing.T) {
	g, _, _, _ := spyGate(t, nil, HookConfig{})

	if v := g.Decide("submit_test", map[string]any{"summary": "x"}); v.Decision != DecisionAllow {
		t.Errorf("Decide(submit_test) = %+v, want allowed with an empty allowlist", v)
	}
	if v := g.Decide("read_file", nil); v.Decision != DecisionDeny {
		t.Errorf("Decide(read_file) = %+v, want denied with an empty allowlist", v)
	}
	// The brain wires the terminating tool in itself; the box has no tool of that name.
	if r := g.Call(context.Background(), "submit_test", json.RawMessage(`{}`)); r.OK || r.Error != "unknown_tool" {
		t.Errorf("Call(submit_test) = %+v, want the box's unknown_tool", r)
	}
}

func TestGate_DeniesWhatTheGuardRefuses(t *testing.T) {
	g, spy, _, _ := spyGate(t, []string{"execute", "write_file", "edit_file"}, HookConfig{ProtectedPaths: []string{".specs/"}, AllowPrograms: []string{"git", "ls"}})
	run := ".agent-fox/run/r/run.json"
	for _, tc := range []struct {
		tool  string
		args  map[string]any
		allow bool
	}{
		{"execute", map[string]any{"command": "git status"}, true},
		{"execute", map[string]any{"command": "rm -rf /"}, false},
		{"write_file", map[string]any{"path": ".specs/x.md"}, false},
		{"write_file", map[string]any{"path": " src/ok.go "}, true},
		// .agent-fox/ is protected though the hook does not say so, however the path is spelled.
		{"write_file", map[string]any{"path": run}, false},
		{"edit_file", map[string]any{"path": "  " + run + "\n"}, false},
		{"write_file", map[string]any{"path": "@" + run}, false},
		{"write_file", map[string]any{"file_path": `"` + run + `"`}, false},
		{"write_file", map[string]any{"path": " @'" + run + "'"}, false},
		{"write_file", map[string]any{"path": "file:///work/box/" + run}, false},
		{"write_file", map[string]any{"path": "./sub/../" + run}, false},
	} {
		v := g.Decide(tc.tool, tc.args)
		if (v.Decision == DecisionAllow) != tc.allow || (!tc.allow && v.Reason == "") {
			t.Errorf("%s %v: %+v, want allowed %v", tc.tool, tc.args, v, tc.allow)
		}
	}
	if r := g.Call(context.Background(), "execute", json.RawMessage(`{"command":"rm -rf /"}`)); r.OK || !strings.Contains(r.Text, "rm") || spy.ran.Load() != 0 {
		t.Errorf("a call the guard refuses: %+v, %d run; want the guard's reason and nothing run", r, spy.ran.Load())
	}
}

func TestGate_RecordsEveryCallItDecidesOnce(t *testing.T) {
	g, _, obs, path := spyGate(t, []string{"read_file", "list_files"}, HookConfig{})

	var wg sync.WaitGroup
	for i := range 30 {
		wg.Go(func() {
			g.Call(context.Background(), []string{"read_file", "list_files", "execute"}[i%3], json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, i)))
		})
	}
	wg.Wait()

	lines := testutil.JSONLines(t, path)
	if len(lines) != 30 || len(obs.calls) != 30 {
		t.Fatalf("%d lines and %d observed for 30 calls", len(lines), len(obs.calls))
	}
	denied := 0
	for i, l := range lines {
		if l["seq"] != float64(i+1) {
			t.Fatalf("line %d has seq %v", i, l["seq"])
		}
		if l["decision"] == DecisionDeny {
			denied++
			if l["tool"] != "execute" || l["ok"] != false || l["result"] != l["reason"] {
				t.Errorf("a denied line = %v, want execute, not ok, with the reason as its result", l)
			}
		} else if l["ok"] != true || !strings.HasPrefix(l["result"].(string), "done ") || !strings.HasPrefix(l["args"].(string), `{"path":"f`) {
			t.Errorf("an allowed line = %v, want its arguments and its result", l)
		}
	}
	if denied != 10 {
		t.Errorf("%d denied lines, want 10", denied)
	}
}

func TestGate_RunsAToolThatHasAHandler(t *testing.T) {
	handler := func(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
		if strings.Contains(string(in), "fail") {
			return nil, errors.New("the handler broke")
		}
		return json.RawMessage(`"handled"`), nil
	}
	g, err := NewGate(GateConfig{Allowed: []string{"h"}, Box: []core.Tool{{Name: "h", Handler: handler}}})
	if err != nil {
		t.Fatal(err)
	}

	if r := g.Call(context.Background(), "h", json.RawMessage(`{}`)); !r.OK || r.Text != `"handled"` {
		t.Errorf("Call = %+v, want the handler's output", r)
	}
	if r := g.Call(context.Background(), "h", json.RawMessage(`{"x":"fail"}`)); r.OK || r.Error != "handler_error" || !strings.Contains(r.LLMText(), "the handler broke") {
		t.Errorf("Call = %+v, want the handler's error", r)
	}
}

func TestNewGate_RefusesToolsTheBoxCannotHonour(t *testing.T) {
	box := (&spyTools{}).tools("write_file", "read_file")

	_, err := NewGate(GateConfig{Allowed: []string{"read_file", "teleport", "levitate", "teleport"}, Box: box})
	if err == nil || !strings.Contains(err.Error(), "name teleport, levitate, which") || !strings.Contains(err.Error(), "it has read_file, write_file") {
		t.Errorf("an allowlist with tools the box lacks: %v, want an error that names each once, and the tools there are", err)
	}
	var f *toolio.Failure
	if errors.As(err, &f) {
		t.Errorf("%v is a failure of the run, want an internal error: the brief is the caller's mistake", err)
	}
	if _, err := NewGate(GateConfig{Allowed: []string{"read_file"}, Terminating: "read_file", Box: box}); err == nil || !strings.Contains(err.Error(), "read_file") {
		t.Errorf("a kind whose tool has the name of a box tool: %v, want an error that names it", err)
	}
	if err := ValidateTools(nil, box); err != nil {
		t.Errorf("an empty allowlist: %v", err)
	}
}

func TestGate_OverTheToolsOfABoxLeavesAgentFoxUnwritten(t *testing.T) {
	w := newWorld(t)
	if _, err := Execute(context.Background(), w.request(w.brief(1), idle)); err == nil {
		t.Fatal("an idle brain submitted")
	}
	b := w.brief(1)
	b.Box, b.Tools = w.session, []string{"write_file", "edit_file", "execute"}
	g, err := GateFor(context.Background(), b, ScriptedName, nil)
	if err != nil {
		t.Fatal(err)
	}
	run := ".agent-fox/run/" + w.run.ID + "/run.json"
	before, _ := w.boxFile(run)

	for _, c := range []struct{ tool, args string }{
		{"write_file", fmt.Sprintf(`{"path":%q,"content":"{}"}`, run)},
		{"write_file", `{"path":"./sub/../.agent-fox/x","content":"x"}`},
		{"edit_file", fmt.Sprintf(`{"path":%q,"edits":[{"old_text":"run_id","new_text":"x"}]}`, run)},
		{"execute", fmt.Sprintf(`{"command":"echo x > .agent-fox/run/%s/submission.json"}`, w.run.ID)},
		{"execute", fmt.Sprintf(`{"command":"sed -i s/a/b/ %s"}`, run)},
		{"execute", `{"command":"rm -rf .agent-fox"}`},
	} {
		if r := g.Call(context.Background(), c.tool, json.RawMessage(c.args)); r.OK || !strings.Contains(r.Text, ".agent-fox") {
			t.Errorf("%s %s: %+v, want a denial that names the protected path", c.tool, c.args, r)
		}
	}

	if after, err := w.boxFile(run); err != nil || after != before {
		t.Errorf("run.json is now %q, %v", after, err)
	}
	if r := g.Call(context.Background(), "execute", json.RawMessage(`{"command":"cat `+run+`"}`)); !r.OK || !strings.Contains(r.Text, w.run.ID) {
		t.Errorf("a read of run.json = %+v, want it let through", r)
	}
}

func TestGateFor_AGuardPolicyThatCannotBeUsedIsAConfigFailure(t *testing.T) {
	w := newWorld(t)
	testutil.WriteFile(t, w.dir, ".agent-fox/guard.json", "{")

	_, err := runScript(t, w, `{"steps":[{"tool":"read_file","args":{"path":"README.md"}}]}`, nil)

	if f := asFailure(t, err); f.Category != toolio.CategoryConfig || f.Retryable || !strings.Contains(f.Message, "guard.json") {
		t.Errorf("failure = %+v, want config, not retryable, naming guard.json", f)
	}
	if lines := w.transcript(1); len(lines) != 0 {
		t.Errorf("the transcript = %v, want no step run", lines)
	}
}
