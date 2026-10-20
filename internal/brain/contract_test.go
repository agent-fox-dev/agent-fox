package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentkit-go/core"
	"github.com/agent-fox-dev/agentkit-go/provider/faux"
)

// contractKind is the kind of the contract test: a payload cites a path that
// must be in the tree, and what it submits is normalised, whatever the layout of
// the text and the order of the keys it came in.
func contractKind() submission.Kind {
	k := testKind()
	k.Validate = func(_ context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
		dec := json.NewDecoder(bytes.NewReader(payload))
		dec.UseNumber()
		var v map[string]any
		if err := dec.Decode(&v); err != nil {
			return nil, []submission.Problem{{Message: err.Error()}}
		}
		cite, _ := v["cite"].(string)
		if _, err := fs.Stat(tree, cite); err != nil {
			return nil, []submission.Problem{{Path: "cite", Message: fmt.Sprintf("%q is not in the tree", cite)}}
		}
		out, err := json.Marshal(v)
		if err != nil {
			return nil, []submission.Problem{{Message: err.Error()}}
		}
		return out, nil
	}
	return k
}

// row is a brain of the contract test: how it is made, and the script it is
// given for the steps of a run.
type row struct {
	name string
	// run runs the brief through the brain with the steps, over the world.
	run func(t *testing.T, w *world, steps contractSteps, observe func(ToolCall)) (Result, error)
	// sawProblem says whether the problems of the refused submission reached the
	// brain as its next turn, after the run.
	sawProblem func(t *testing.T, w *world, problem string) bool
	costSource string
}

// contractSteps is what every brain is asked to do, in the words each takes: a
// call outside the allowlist, a write under .agent-fox/, a submission that cites
// a path that is not there, and the one that does.
type contractSteps struct {
	invalid, valid string // payloads, as JSON text
}

const (
	outsideCommand = `{"command":"echo leaked > leaked.txt"}`
	protectedWrite = `{"path":".agent-fox/planted.txt","content":"planted"}`
)

// contractBrief is the one brief that every row runs: the same tools, the same
// kind, the same task.
func contractBrief(w *world) Brief {
	b := w.brief(1)
	b.Kind = contractKind()
	b.Tools = []string{"read_file", "write_file", "list_files"}
	return b
}

// scriptedRow replays the steps as a script file.
func scriptedRow() row {
	return row{
		name:       "scripted",
		costSource: CostNone,
		run: func(t *testing.T, w *world, s contractSteps, observe func(ToolCall)) (Result, error) {
			script := fmt.Sprintf(`{"steps":[
				{"tool":"execute","args":%s,"expect":"denied"},
				{"tool":"write_file","args":%s,"expect":"denied"},
				{"submit":%s,"expect":"refused"},
				{"submit":%s,"expect":"accepted"}]}`, outsideCommand, protectedWrite, s.invalid, s.valid)
			brain, err := Default.New(ScriptedName, Options{Script: writeScript(t, script)})
			if err != nil {
				t.Fatal(err)
			}
			b := contractBrief(w)
			b.Observe = observe
			req := w.request(b, brain)
			req.Name = ScriptedName
			return Execute(context.Background(), req)
		},
		sawProblem: func(t *testing.T, w *world, problem string) bool {
			for _, l := range w.transcript() {
				if l["tool"] == "submit_test" && l["ok"] == false && strings.Contains(fmt.Sprint(l["result"]), problem) {
					return true
				}
			}
			return false
		},
	}
}

// agentkitRow plays the steps as the turns of a faux model.
func agentkitRow() row {
	var provider *faux.Provider
	return row{
		name:       "agentkit",
		costSource: CostMeasured,
		run: func(t *testing.T, w *world, s contractSteps, observe func(ToolCall)) (Result, error) {
			brain, p := fauxBrain(t,
				withUsage(call("c1", "execute", outsideCommand), usage(0.01, 100, 10)),
				withUsage(call("c2", "write_file", protectedWrite), usage(0.01, 100, 10)),
				withUsage(call("c3", "submit_test", s.invalid), usage(0.01, 100, 10)),
				withUsage(call("c4", "submit_test", s.valid), usage(0.01, 100, 10)))
			provider = p
			b := contractBrief(w)
			b.Observe = observe
			req := w.request(b, brain)
			req.Name = AgentkitName
			return Execute(context.Background(), req)
		},
		sawProblem: func(t *testing.T, w *world, problem string) bool {
			for _, req := range provider.Requests() {
				for _, text := range toolResults(req) {
					if strings.Contains(text, problem) {
						return true
					}
				}
			}
			return false
		},
	}
}

// hostPayload is the payload of the submission file in the run directory.
func hostPayload(t *testing.T, w *world) []byte {
	t.Helper()
	data, ok := w.hostSubmission()
	if !ok {
		t.Fatal("no submission in the run directory")
	}
	var file submission.File
	if err := json.Unmarshal([]byte(data), &file); err != nil {
		t.Fatal(err)
	}
	return file.Payload
}

// TestEveryBrainSubmitsTheSame_TS_07_77 verifies TS-07-77:
// the Brain contract test: scripted and agentkit leave identical payload bytes
// and agree on denials and the claim.
// Verifies: 07-REQ-10.6, 07-REQ-6.8, 07-REQ-8.1, 07-REQ-8.5
func TestEveryBrainSubmitsTheSame_TS_07_77(t *testing.T) {
	steps := contractSteps{
		invalid: `{"summary":"fixed","cite":"nowhere/missing.go"}`,
		valid:   `{ "summary": "fixed", "cite": "README.md", "files": ["src/main.go"] }`,
	}
	payloads := map[string]string{}
	usages := map[string]string{}
	for _, r := range []row{scriptedRow(), agentkitRow()} {
		t.Run(r.name, func(t *testing.T) {
			w := newWorld(t)
			boxHasNoSubmission := true
			observe := func(c ToolCall) {
				// A refusal is not a file: while the invalid submission is the last
				// thing that happened, there is none in the box.
				if c.Tool == "submit_test" && !c.OK {
					if _, err := w.boxFile(submission.SubmissionPath(w.run.ID)); !errors.Is(err, fs.ErrNotExist) {
						boxHasNoSubmission = false
					}
				}
			}

			res, err := r.run(t, w, steps, observe)

			if err != nil || !res.Outcome.Submitted {
				t.Fatalf("Execute = %+v, %v", res, err)
			}
			payloads[r.name] = string(hostPayload(t, w))
			usages[r.name] = w.report.lastUsage().CostSource
			if payloads[r.name] != string(res.Payload) {
				t.Errorf("the file's payload %s is not the result's %s", payloads[r.name], res.Payload)
			}
			if !boxHasNoSubmission {
				t.Error("a submission file existed after the invalid submission")
			}
			if !r.sawProblem(t, w, `"nowhere/missing.go" is not in the tree`) {
				t.Error("the problem of the invalid submission did not reach the brain as its next turn")
			}
			for _, rel := range []string{"leaked.txt", ".agent-fox/planted.txt"} {
				if _, err := w.boxFile(rel); err == nil {
					t.Errorf("%s was written: a denied call was executed", rel)
				}
			}
			if !res.Outcome.Submitted || w.report.lastUsage().Brain != r.name {
				t.Errorf("claim %v, usage %+v", res.Outcome.Submitted, w.report.lastUsage())
			}
			var denied []string
			for _, e := range w.report.eventsOf("tool_call") {
				if e.data["decision"] == DecisionDeny {
					denied = append(denied, fmt.Sprint(e.data["tool"]))
				}
			}
			if strings.Join(denied, ",") != "execute,write_file" {
				t.Errorf("denied calls = %v, want the one outside the allowlist and the one under .agent-fox/", denied)
			}
		})
	}
	if payloads["scripted"] != payloads["agentkit"] || payloads["scripted"] == "" {
		t.Errorf("the brains submitted different bytes:\nscripted %s\nagentkit %s", payloads["scripted"], payloads["agentkit"])
	}
	if usages["scripted"] != CostNone || usages["agentkit"] != CostMeasured {
		t.Errorf("cost sources = %v, want none for scripted and measured for agentkit", usages)
	}
}

// randomValue is a JSON value of the kinds a payload holds.
func randomValue(rng *rand.Rand, depth int) any {
	switch n := rng.IntN(7); {
	case n == 0 && depth < 3:
		m := map[string]any{}
		for range rng.IntN(4) {
			m[randomKey(rng)] = randomValue(rng, depth+1)
		}
		return m
	case n == 1 && depth < 3:
		var list []any
		for range rng.IntN(4) {
			list = append(list, randomValue(rng, depth+1))
		}
		return list
	case n == 2:
		return rng.Int64N(1 << 40)
	case n == 3:
		return rng.IntN(2) == 0
	case n == 4:
		return nil
	case n == 5:
		return float64(rng.IntN(1000)) / 8
	}
	return randomText(rng)
}

func randomKey(rng *rand.Rand) string {
	keys := []string{"summary", "files", "notes", "ünï", "日本語", "a b", "with\"quote", "x", "k1", "k2", "Z"}
	return keys[rng.IntN(len(keys))]
}

func randomText(rng *rand.Rand) string {
	pieces := []string{"plain", "ünï", "☃", "🙂", "line\nbreak", "tab\t", "<b>&</b>", "quote\"", "back\\slash", "\u2028", "  spaces  ", ""}
	var b strings.Builder
	for range 1 + rng.IntN(4) {
		b.WriteString(pieces[rng.IntN(len(pieces))])
	}
	return b.String()
}

// render writes a payload as JSON text in a layout the rng picks.
func render(t *testing.T, rng *rand.Rand, v any) string {
	t.Helper()
	var data []byte
	var err error
	switch rng.IntN(3) {
	case 0:
		data, err = json.Marshal(v)
	case 1:
		data, err = json.MarshalIndent(v, "", "  ")
	default:
		data, err = json.MarshalIndent(v, "\t", "    ")
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// TestAnyValidPayloadIsTheSameForBothBrains_TS_07_78 verifies TS-07-78:
// for any valid payload both brains leave identical bytes and Submitted true.
// Verifies: 07-REQ-10.6
func TestAnyValidPayloadIsTheSameForBothBrains_TS_07_78(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 78))
	for i := range 12 {
		payload := map[string]any{}
		for range rng.IntN(5) {
			payload[randomKey(rng)] = randomValue(rng, 0)
		}
		payload["cite"] = "README.md"
		steps := contractSteps{invalid: `{"cite":"missing.go"}`, valid: render(t, rng, payload)}

		got := map[string]string{}
		for _, r := range []row{scriptedRow(), agentkitRow()} {
			w := newWorld(t)
			res, err := r.run(t, w, steps, nil)
			if err != nil || !res.Outcome.Submitted {
				t.Fatalf("#%d %s: Execute = %+v, %v for the payload %s", i, r.name, res, err, steps.valid)
			}
			got[r.name] = string(hostPayload(t, w))
			if w.report.lastUsage().CostSource != r.costSource {
				t.Errorf("#%d %s: cost source %q, want %q", i, r.name, w.report.lastUsage().CostSource, r.costSource)
			}
		}
		if got["scripted"] != got["agentkit"] || !json.Valid([]byte(got["scripted"])) {
			t.Fatalf("#%d: the brains submitted different bytes for %s:\nscripted %s\nagentkit %s", i, steps.valid, got["scripted"], got["agentkit"])
		}
	}
}

// denyNetwork is an HTTP transport that fails every request, and counts them.
type denyNetwork struct{ attempts atomic.Int32 }

func (d *denyNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	d.attempts.Add(1)
	return nil, fmt.Errorf("no network in this test: %s", r.URL)
}

// TestTheBrainsNeedNoKeyAndNoNetwork_TS_07_79 verifies TS-07-79:
// the brain, box and submission suites run offline with no vendor key and no
// network.
// Verifies: 07-REQ-10.7
func TestTheBrainsNeedNoKeyAndNoNetwork_TS_07_79(t *testing.T) {
	if old, ok := os.LookupEnv("ANTHROPIC_API_KEY"); ok {
		_ = os.Unsetenv("ANTHROPIC_API_KEY")
		t.Cleanup(func() { _ = os.Setenv("ANTHROPIC_API_KEY", old) })
	}
	deny := &denyNetwork{}
	old := http.DefaultTransport
	http.DefaultTransport = deny
	t.Cleanup(func() { http.DefaultTransport = old })
	var mu sync.Mutex
	var read []string
	spy := func(name string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		read = append(read, name)
		return "", false
	}
	steps := contractSteps{invalid: `{"cite":"missing.go"}`, valid: `{"summary":"offline","cite":"README.md"}`}

	for _, r := range []row{scriptedRow(), agentkitRow()} {
		w := newWorld(t)
		res, err := r.run(t, w, steps, nil)
		if err != nil || !res.Outcome.Submitted {
			t.Errorf("%s: Execute = %+v, %v", r.name, res, err)
		}
	}
	// The brain that is made as the command makes it, with an injected provider,
	// reads no variable of a vendor.
	if _, err := Default.New(AgentkitName, Options{Getenv: spy, NewProvider: func(string) (core.ProviderClient, error) { return faux.New(), nil }}); err != nil {
		t.Fatal(err)
	}

	if n := deny.attempts.Load(); n != 0 {
		t.Errorf("%d requests went to the network", n)
	}
	for _, name := range read {
		if strings.Contains(name, "ANTHROPIC") {
			t.Errorf("the environment variable %s was read with a provider injected", name)
		}
	}
}
