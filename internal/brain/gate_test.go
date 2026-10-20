package brain

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentkit-go/core"
)

// spyTools is a box tool set that counts what is run.
type spyTools struct {
	calls  atomic.Int32
	result func(name string, in json.RawMessage) core.ToolResult
	mu     sync.Mutex
	ran    []string
}

func (s *spyTools) set(names ...string) []core.Tool {
	var tools []core.Tool
	for _, n := range names {
		tools = append(tools, core.Tool{Name: n, Execute: func(ctx context.Context, in json.RawMessage) core.ToolResult {
			s.calls.Add(1)
			s.mu.Lock()
			s.ran = append(s.ran, n)
			s.mu.Unlock()
			if s.result != nil {
				return s.result(n, in)
			}
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

// newGate is a gate over the spy tools with an allowlist, a hook that protects
// .agent-fox/ and a transcript in a temporary directory.
func newGate(t *testing.T, spy *spyTools, allowed []string, hook HookConfig, obs *observed) (*Gate, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	tr, err := NewTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tr.Close() })
	if hook.ProtectedPaths == nil {
		hook.ProtectedPaths = []string{".agent-fox/"}
	}
	g, err := NewGate(GateConfig{
		Brain: "scripted", Allowed: allowed, Hook: hook, Dir: "/work/box", Box: spy.set("read_file", "write_file", "execute", "list_files"),
		Observe: obs.observe, Transcript: tr,
	})
	if err != nil {
		t.Fatalf("NewGate: %v", err)
	}
	return g, path
}

// lines is the lines of a transcript file, decoded.
func lines(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("a transcript line is not JSON: %v\n%s", err, sc.Bytes())
		}
		out = append(out, m)
	}
	return out
}

// TestAToolOutsideTheAllowlistIsDenied_TS_07_57 verifies TS-07-57:
// a tool outside Brief.Tools is denied without invoking the box tool.
// Verifies: 07-REQ-8.1
func TestAToolOutsideTheAllowlistIsDenied_TS_07_57(t *testing.T) {
	spy := &spyTools{}
	obs := &observed{}
	g, _ := newGate(t, spy, []string{"read_file"}, HookConfig{}, obs)

	r := g.Call(context.Background(), "write_file", json.RawMessage(`{"path":"a.txt","content":"x"}`))

	const reason = "tool write_file is not in this run's allowlist"
	if r.OK || r.Text != reason || r.Detail != reason {
		t.Errorf("result = %+v, want an error result that says %q", r, reason)
	}
	if spy.calls.Load() != 0 {
		t.Error("the box's write_file was invoked")
	}
	if len(obs.calls) != 1 || obs.calls[0].Decision != DecisionDeny || obs.calls[0].Reason != reason || obs.calls[0].Tool != "write_file" || obs.calls[0].OK {
		t.Errorf("observed = %+v, want one denial", obs.calls)
	}
	if v := g.Decide("write_file", nil); v.Decision != DecisionDeny || v.Reason != reason {
		t.Errorf("Decide = %+v", v)
	}
	if r := g.Call(context.Background(), "read_file", json.RawMessage(`{"path":"a.txt"}`)); !r.OK || spy.calls.Load() != 1 {
		t.Errorf("an allowed call: %+v, %d invocations", r, spy.calls.Load())
	}
}

// TestTheGuardDecidesTheRest_TS_07_60 verifies TS-07-60:
// a call the ring-1 guard denies is not executed and returns the guard's
// reason.
// Verifies: 07-REQ-8.4
func TestTheGuardDecidesTheRest_TS_07_60(t *testing.T) {
	spy := &spyTools{}
	obs := &observed{}
	hook := HookConfig{ProtectedPaths: []string{".agent-fox/", ".specs/"}, AllowPrograms: []string{"git", "ls"}}
	g, _ := newGate(t, spy, []string{"execute", "write_file"}, hook, obs)

	denied := g.Call(context.Background(), "execute", json.RawMessage(`{"command":"rm -rf /"}`))
	ranAfterDenial := spy.calls.Load()
	allowed := g.Call(context.Background(), "execute", json.RawMessage(`{"command":"git status"}`))
	specs := g.Call(context.Background(), "write_file", json.RawMessage(`{"path":".specs/x.md","content":"x"}`))

	if denied.OK || !strings.Contains(denied.Text, "rm") || ranAfterDenial != 0 {
		t.Errorf("the denied call: %+v, %d invocations; want the guard's reason and no invocation", denied, ranAfterDenial)
	}
	if denied.Detail != denied.Text || denied.Error != core.BlockErrorCode {
		t.Errorf("the denial is %+v, want %s with the reason as its detail and its text", denied, core.BlockErrorCode)
	}
	if !allowed.OK || spy.calls.Load() != 1 || spy.ran[0] != "execute" {
		t.Errorf("the allowed call: %+v, ran %v", allowed, spy.ran)
	}
	if specs.OK || !strings.Contains(specs.Text, ".specs/x.md") || spy.calls.Load() != 1 {
		t.Errorf("a write to a path the hook protects: %+v", specs)
	}
	var decisions []string
	for _, c := range obs.calls {
		decisions = append(decisions, c.Decision)
	}
	if !slices.Equal(decisions, []string{DecisionDeny, DecisionAllow, DecisionDeny}) {
		t.Errorf("decisions = %v", decisions)
	}
}

// TestWritesUnderAgentFoxAreDenied_TS_07_61 verifies TS-07-61:
// writes and shell commands targeting .agent-fox/ are denied and the file is
// unchanged. It runs over the real tools of a real box.
// Verifies: 07-REQ-8.5
func TestWritesUnderAgentFoxAreDenied_TS_07_61(t *testing.T) {
	w := newWorld(t)
	b := w.brief(1)
	Execute(context.Background(), w.request(b, stubBrain(func(context.Context, Brief) (Outcome, error) { return Outcome{}, nil })))
	run := ".agent-fox/run/" + w.run.ID + "/run.json"
	before, err := w.boxFile(run)
	if err != nil {
		t.Fatal(err)
	}
	obs := &observed{}
	g, err := NewGate(GateConfig{
		Brain: "scripted", Allowed: []string{"write_file", "execute", "edit_file"}, Hook: HookConfig{ProtectedPaths: []string{".agent-fox/"}},
		Dir: w.session.Spec().Workdir, Box: w.session.Tools(), Observe: obs.observe,
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, call := range map[string]struct{ tool, args string }{
		"write run.json":      {"write_file", fmt.Sprintf(`{"path":%q,"content":"{}"}`, run)},
		"write through ..":    {"write_file", `{"path":"./sub/../.agent-fox/x","content":"x"}`},
		"edit run.json":       {"edit_file", fmt.Sprintf(`{"path":%q,"edits":[{"old_text":"run_id","new_text":"x"}]}`, run)},
		"redirect submission": {"execute", fmt.Sprintf(`{"command":"echo x > .agent-fox/run/%s/submission.json"}`, w.run.ID)},
		"sed in place":        {"execute", fmt.Sprintf(`{"command":"sed -i s/a/b/ %s"}`, run)},
		"rm the directory":    {"execute", `{"command":"rm -rf .agent-fox"}`},
	} {
		r := g.Call(context.Background(), call.tool, json.RawMessage(call.args))
		if r.OK || !strings.Contains(r.Text, ".agent-fox") {
			t.Errorf("%s: %+v, want a denial that names the protected path", name, r)
		}
	}

	if after, err := w.boxFile(run); err != nil || after != before {
		t.Errorf("run.json changed: %q, %v", after, err)
	}
	if _, err := w.boxFile(".agent-fox/x"); err == nil {
		t.Error(".agent-fox/x was made")
	}
	if _, err := w.boxFile(".agent-fox/run/" + w.run.ID + "/submission.json"); err == nil {
		t.Error("a submission file was made by a shell command")
	}
	// What is not a write is let through: the brief is for reading.
	if r := g.Call(context.Background(), "execute", json.RawMessage(fmt.Sprintf(`{"command":"cat %s"}`, run))); !r.OK || !strings.Contains(r.Text, w.run.ID) {
		t.Errorf("a read of run.json by a shell command: %+v, want it let through", r)
	}
}

// TestEveryDecidedCallIsObservedAndWritten_TS_07_63 verifies TS-07-63:
// each decided call yields one Observe and one transcript line, with args and
// result truncated to 4096 bytes.
// Verifies: 07-REQ-8.7
func TestEveryDecidedCallIsObservedAndWritten_TS_07_63(t *testing.T) {
	spy := &spyTools{result: func(name string, in json.RawMessage) core.ToolResult {
		if name == "read_file" {
			return core.ToolResult{OK: true, Text: strings.Repeat("é", 6000)} // 12000 bytes
		}
		return core.ToolResult{OK: true, Text: "written"}
	}}
	obs := &observed{}
	g, path := newGate(t, spy, []string{"read_file", "write_file"}, HookConfig{}, obs)
	big := `{"path":"a.txt","content":"` + strings.Repeat("☃", 3500) + `"}` // 10 KiB

	g.Call(context.Background(), "write_file", json.RawMessage(`{"path":"small.txt","content":"x"}`))
	g.Call(context.Background(), "execute", json.RawMessage(`{"command":"ls"}`))
	g.Call(context.Background(), "write_file", json.RawMessage(big))
	g.Call(context.Background(), "read_file", json.RawMessage(`{"path":"big.txt"}`))

	if len(obs.calls) != 4 {
		t.Fatalf("%d calls observed, want 4: %+v", len(obs.calls), obs.calls)
	}
	for i, c := range obs.calls {
		if c.Brain != "scripted" || c.Tool == "" || (c.Decision != DecisionAllow && c.Decision != DecisionDeny) || c.DurationMS < 0 {
			t.Errorf("observed[%d] = %+v", i, c)
		}
	}
	if obs.calls[1].Decision != DecisionDeny || obs.calls[1].Reason == "" || obs.calls[1].OK {
		t.Errorf("the call outside the allowlist: %+v", obs.calls[1])
	}
	got := lines(t, path)
	if len(got) != 4 {
		t.Fatalf("%d transcript lines, want 4", len(got))
	}
	prev := 0.0
	for i, l := range got {
		for _, key := range []string{"seq", "tool", "args", "decision", "reason", "ok", "result"} {
			if _, ok := l[key]; !ok {
				t.Errorf("line %d lacks %q: %v", i, key, l)
			}
		}
		seq, _ := l["seq"].(float64)
		if seq <= prev {
			t.Errorf("seq %v after %v", seq, prev)
		}
		prev = seq
		for _, key := range []string{"args", "result"} {
			s, _ := l[key].(string)
			if len(s) > 4096 || !utf8.ValidString(s) {
				t.Errorf("line %d: %s is %d bytes, valid UTF-8 %v; want at most 4096 and valid", i, key, len(s), utf8.ValidString(s))
			}
		}
	}
	if args, _ := got[2]["args"].(string); len(args) < 4000 || !strings.HasPrefix(args, `{"path":"a.txt"`) {
		t.Errorf("the large call's args = %.60q (%d bytes), want the start of them, cut", args, len(args))
	}
	if result, _ := got[3]["result"].(string); len(result) < 4000 {
		t.Errorf("the large result is %d bytes, want it cut to 4096 and not dropped", len(result))
	}
}

// TestValidateTools guards 07-REQ-8.3: an allowlist that names a tool the box
// does not provide is an error that names it, before any call.
func TestValidateTools(t *testing.T) {
	box := (&spyTools{}).set("read_file", "write_file")

	if err := ValidateTools([]string{"read_file", "write_file"}, box); err != nil {
		t.Errorf("a good allowlist: %v", err)
	}
	if err := ValidateTools(nil, box); err != nil {
		t.Errorf("an empty allowlist: %v", err)
	}
	err := ValidateTools([]string{"read_file", "teleport", "levitate"}, box)
	if err == nil || !strings.Contains(err.Error(), "teleport") || !strings.Contains(err.Error(), "levitate") {
		t.Errorf("ValidateTools = %v, want an error that names the unknown tools", err)
	}
	var f interface{ Error() string }
	if _, ok := err.(interface{ Category() string }); ok {
		t.Errorf("%v (%T) is a usage failure, want an internal error", f, err)
	}
	if _, err := NewGate(GateConfig{Allowed: []string{"teleport"}, Box: box, Dir: "/x"}); err == nil || !strings.Contains(err.Error(), "teleport") {
		t.Errorf("NewGate with an unknown tool: %v", err)
	}
}

// TestTheTerminatingToolBypassesTheAllowlist guards 07-REQ-8.2: the tool that
// ends the run is allowed with an empty allowlist.
func TestTheTerminatingToolBypassesTheAllowlist(t *testing.T) {
	spy := &spyTools{}
	g, err := NewGate(GateConfig{Brain: "scripted", Allowed: nil, Terminating: "submit_test", Box: spy.set("read_file"), Dir: "/work/box", Hook: HookConfig{ProtectedPaths: []string{".agent-fox/"}}})
	if err != nil {
		t.Fatal(err)
	}

	if v := g.Decide("submit_test", map[string]any{"summary": "x"}); v.Decision != DecisionAllow {
		t.Errorf("Decide(the terminating tool) = %+v", v)
	}
	if v := g.Decide("read_file", nil); v.Decision != DecisionDeny {
		t.Errorf("Decide(read_file) = %+v, want a denial: the allowlist is empty", v)
	}
}

// TestTheGateIsSafeForParallelCalls guards 07-REQ-8.7: calls that run at once
// each leave one line, and the sequence numbers are still strictly increasing.
func TestTheGateIsSafeForParallelCalls(t *testing.T) {
	spy := &spyTools{}
	obs := &observed{}
	g, path := newGate(t, spy, []string{"read_file", "list_files"}, HookConfig{}, obs)

	var wg sync.WaitGroup
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.Call(context.Background(), []string{"read_file", "list_files", "execute"}[i%3], json.RawMessage(fmt.Sprintf(`{"path":"f%d"}`, i)))
		}()
	}
	wg.Wait()

	got := lines(t, path)
	if len(got) != 40 || len(obs.calls) != 40 {
		t.Fatalf("%d lines and %d observed for 40 calls", len(got), len(obs.calls))
	}
	prev := 0.0
	for _, l := range got {
		if seq := l["seq"].(float64); seq <= prev {
			t.Fatalf("seq %v after %v", seq, prev)
		} else {
			prev = seq
		}
	}
}

// TestTruncateKeepsUTF8 guards 07-REQ-8.7: a cut that falls inside a character
// goes back to the character before it.
func TestTruncateKeepsUTF8(t *testing.T) {
	for _, s := range []string{strings.Repeat("a", 5000), strings.Repeat("é", 5000), strings.Repeat("☃", 5000), strings.Repeat("🙂", 5000), "short", ""} {
		got := truncate(s, 4096)
		if len(got) > 4096 || !utf8.ValidString(got) || (len(s) <= 4096 && got != s) || (len(s) > 4096 && !strings.HasSuffix(got, "…")) {
			t.Errorf("truncate(%.10q... of %d bytes) = %d bytes, valid %v", s, len(s), len(got), utf8.ValidString(got))
		}
	}
}
