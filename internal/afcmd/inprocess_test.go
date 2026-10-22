package afcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// inProcess is the seams an in-process caller supplies, each one a spy, and what they saw.
type inProcess struct {
	cfg Config

	forgeBuilt   atomic.Int32
	boxesCreated atomic.Int32
	gitRuns      atomic.Int32
	brainsMade   atomic.Int32

	mu         sync.Mutex
	models     []string // the model each brain was made with
	keys       []string // the vendor key each brain could see
	boxEnvs    []map[string]string
	envLookups []string // the names read through the lookup of the caller
}

// newInProcess makes the seams of a caller that offers the local box as "inbox" (a provider
// that af does not offer), the scripted brain through a registry of its own, a lookup of the
// environment that yields nothing, the forge of the env, and a git runner that counts.
func newInProcess(t *testing.T, forge *fakeforge.Forge) *inProcess {
	t.Helper()
	p := &inProcess{}

	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	inbox := local
	inbox.Name = "inbox"
	inbox.New = func() sandbox.Provider { return spyProvider{Provider: local.New(), p: p} }
	p.cfg.Boxes = box.NewRegistry()
	p.cfg.Boxes.Register(inbox)

	scriptedEntry, err := brain.Default.Lookup(brain.ScriptedName)
	if err != nil {
		t.Fatal(err)
	}
	spy := scriptedEntry
	spy.New = func(opts brain.Options) (brain.Brain, error) {
		p.brainsMade.Add(1)
		key, _ := opts.Getenv("ANTHROPIC_API_KEY")
		p.mu.Lock()
		p.models = append(p.models, opts.Model)
		p.keys = append(p.keys, key)
		p.mu.Unlock()
		return scriptedEntry.New(opts)
	}
	p.cfg.Brains = brain.NewRegistry()
	p.cfg.Brains.Register(spy)

	p.cfg.Brain = brain.Options{Getenv: func(name string) (string, bool) {
		p.mu.Lock()
		p.envLookups = append(p.envLookups, name)
		p.mu.Unlock()
		return "", false
	}}
	p.cfg.Git = func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		p.gitRuns.Add(1)
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	}
	if forge != nil {
		build := forge.NewForge("test-token")
		p.cfg.Forge = func(o issuex.Options) (issuex.Client, error) {
			p.forgeBuilt.Add(1)
			return build(o)
		}
	}
	return p
}

type spyProvider struct {
	sandbox.Provider
	p *inProcess
}

func (s spyProvider) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	s.p.boxesCreated.Add(1)
	s.p.mu.Lock()
	s.p.boxEnvs = append(s.p.boxEnvs, spec.Env)
	s.p.mu.Unlock()
	return s.Provider.Create(ctx, spec)
}

// main runs argv on a fresh App made from the seams.
func (p *inProcess) main(stdin string, args ...string) (int, map[string]any, error) {
	var out, errb bytes.Buffer
	code := New(&p.cfg).Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	var env map[string]any
	err := json.Unmarshal(out.Bytes(), &env)
	return code, env, err
}

// sentinels sets the variables of the process that an in-process invocation must not read.
func sentinels(t *testing.T) {
	t.Helper()
	t.Setenv("AF_MODEL", "other")
	t.Setenv("AF_SANDBOX", "bogus")
	t.Setenv("ANTHROPIC_API_KEY", "sentinel-vendor-key")
}

// TestSuppliedSeamsReplaceThePackageVariablesAndTheEnvironment_TS_13_2 verifies TS-13-2: a
// fix run in-process uses the forge factory, the box registry (with a provider af does not
// offer), the brain registry, the lookup of the environment and the git runner it is given,
// reads none of AF_MODEL, AF_SANDBOX and ANTHROPIC_API_KEY of the process, and leaves the
// registries of the package alone.
// Verifies: 13-REQ-1.2
func TestSuppliedSeamsReplaceThePackageVariablesAndTheEnvironment_TS_13_2(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	sentinels(t)
	boxesBefore, brainsBefore := box.Default.Names(), brain.Default.Names()
	p := newInProcess(t, e.forge)
	e.app = New(&p.cfg)

	// AF_SANDBOX=bogus would be refused if it were read, even with --sandbox given.
	code, env := e.run("", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--sandbox", "inbox", "--script", script(t, fixSteps()...))

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	if p.forgeBuilt.Load() == 0 || p.boxesCreated.Load() != 1 || p.gitRuns.Load() == 0 || p.brainsMade.Load() == 0 {
		t.Errorf("the forge factory was called %d times, %d boxes were created in the registry of the caller, git ran %d times, %d brains were made: want each used",
			p.forgeBuilt.Load(), p.boxesCreated.Load(), p.gitRuns.Load(), p.brainsMade.Load())
	}
	for i, model := range p.models {
		if model == "other" || p.keys[i] != "" {
			t.Errorf("a brain was made with model %q and could see the key %q: the lookup of the caller yields nothing", model, p.keys[i])
		}
	}
	for _, env := range p.boxEnvs {
		if len(env) != 0 {
			t.Errorf("a box was created with the environment %v, want none", env)
		}
	}
	if !reflect.DeepEqual(box.Default.Names(), boxesBefore) || !reflect.DeepEqual(brain.Default.Names(), brainsBefore) {
		t.Errorf("the registries of the package are %v and %v, were %v and %v", box.Default.Names(), brain.Default.Names(), boxesBefore, brainsBefore)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Contains(p.envLookups, "AF_MODEL") || !slices.Contains(p.envLookups, "AF_SANDBOX") {
		t.Errorf("the lookup of the caller was asked for %v, want AF_MODEL and AF_SANDBOX among them", p.envLookups)
	}
}

// TestSeamsNotSuppliedDefaultToAfs_TS_13_3 verifies TS-13-3: a Config with nothing in it
// is the af binary: box.Default, brain.Default, the process environment and ExecRunner,
// and a run on a scripted brain of the package defaults lands.
// Verifies: 13-REQ-1.2
func TestSeamsNotSuppliedDefaultToAfs_TS_13_3(t *testing.T) {
	cfg := &Config{Version: "v1", Commit: "abc"}
	if cfg.boxes() != box.Default || cfg.brains() != brain.Default {
		t.Errorf("registries = %p and %p, want box.Default and brain.Default", cfg.boxes(), cfg.brains())
	}
	if got, want := reflect.ValueOf(cfg.git()).Pointer(), reflect.ValueOf(gitx.ExecRunner).Pointer(); got != want {
		t.Errorf("the git runner is not gitx.ExecRunner")
	}
	t.Setenv("AF_MODEL", "from-the-process")
	if v, ok := cfg.getenv()("AF_MODEL"); !ok || v != "from-the-process" {
		t.Errorf("the environment lookup gave %q, %v, want the process's", v, ok)
	}
	if opts := cfg.brainOptions(); opts.Model != "from-the-process" || opts.AFVersion != "v1" || opts.AFCommit != "abc" {
		t.Errorf("options = %+v, want the model of AF_MODEL and the identity of the program", opts)
	}
	if reflect.ValueOf(cfg.gate()).Pointer() == 0 {
		t.Error("no gate")
	}
	if app := New(cfg); app.NewForge != nil || app.NoSignals || app.EventSink != nil || app.RunID != "" || app.Target.Valid() {
		t.Errorf("the app has in-process options set: %+v", app)
	}

	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
	e.app = New(&Config{})
	e.app.NewForge = e.forge.NewForge("test-token")
	code, env := e.fix(e.forge.IssueURL(7), scripted(script(t, fixSteps()...))...)
	if code != 0 || env["status"] != "done" || resultOf(t, env)["outcome"] != "landed" {
		t.Errorf("a run with the defaults: exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
}

// earlierRun writes the run directory of an earlier run that ended in a question, which a
// search of the run directories would find for the input, so that a run that reads it shows.
func earlierRun(t *testing.T, dir, question string) {
	t.Helper()
	run := filepath.Join(dir, ".agent-fox", "run", "20200101_000000_aaaaaa")
	if err := os.MkdirAll(run, 0o755); err != nil {
		t.Fatal(err)
	}
	env := map[string]any{"status": "needs_human", "needs_human": map[string]any{"question": question, "options": []any{map[string]any{"id": "A", "label": "decoy option"}}}}
	data, _ := json.Marshal(env)
	if err := os.WriteFile(filepath.Join(run, "envelope.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEarlierQuestionsRenderInTheFixBrief_TS_13_4 verifies TS-13-4: of two --context values,
// the one given with a question appears as that question, its options and the answer, the
// one given with none as material from a person, no run directory is searched and nothing
// is warned.
// Verifies: 13-REQ-1.3
func TestEarlierQuestionsRenderInTheFixBrief_TS_13_4(t *testing.T) {
	requireMake(t)
	e := newFixEnv(t, fakeforge.GitHub)
	earlierRun(t, e.dir, "the decoy question")
	spy := &spyBrain{}
	p := newInProcess(t, e.forge)
	p.cfg.Brains = spyRegistry(t, spy)
	p.cfg.Earlier = &Earlier{Questions: []*toolio.Question{nil, {Text: "Which greeting?", Options: []string{"hello", "hi"}}}}
	e.app = New(&p.cfg)

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "inbox", "--context", "ctx1: the report is old", "--context", "B")
	_ = code

	task := spy.brief.Task
	for _, want := range []string{"Which greeting?", "A. hello", "B. hi", "Material from a person", "ctx1: the report is old", "A person's answer"} {
		if !strings.Contains(task, want) {
			t.Errorf("the task block lacks %q:\n%s", want, task)
		}
	}
	if strings.Contains(task, "decoy") || strings.Contains(task, "could not be found") {
		t.Errorf("the task block reads a run directory or says a question could not be found:\n%s", task)
	}
	if slices.Contains(warningCodes(env), "question_not_found") {
		t.Errorf("warnings = %v, want no question_not_found", warningCodes(env))
	}

	// The brief of af fix brief is the same.
	code, env = e.run("", "fix", "brief", e.forge.IssueURL(7), "--dir", e.dir, "--context", "ctx1: the report is old", "--context", "B")
	if code != 0 {
		t.Fatalf("fix brief: exit code %d, error %v", code, env["error"])
	}
	brief := string(readFile(t, resultOf(t, env)["brief_path"].(string)))
	if !strings.Contains(brief, "Which greeting?") || !strings.Contains(brief, "Material from a person") || strings.Contains(brief, "decoy") {
		t.Errorf("the brief of af fix brief:\n%s", brief[max(0, len(brief)-900):])
	}
	if slices.Contains(warningCodes(env), "question_not_found") {
		t.Errorf("fix brief warnings = %v, want no question_not_found", warningCodes(env))
	}

	// Without the caller's questions the run directories are searched, as af does.
	p.cfg.Earlier = nil
	_, env = e.run("", "fix", "brief", e.forge.IssueURL(7), "--dir", e.dir, "--context", "B")
	if !slices.Contains(warningCodes(env), "question_not_found") {
		t.Errorf("af's own search: warnings = %v, want question_not_found for an answer no run asked about", warningCodes(env))
	}
}

// TestTriageBriefHoldsTheQuestionOfTheLastValueThatHasOne_TS_13_5 verifies TS-13-5: of three
// --context values, the first two with the questions Q1 and Q2 and the third with none, the
// triage brief holds exactly Q2, follows it with all three values, and warns of nothing.
// Verifies: 13-REQ-1.3
func TestTriageBriefHoldsTheQuestionOfTheLastValueThatHasOne_TS_13_5(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	earlierRun(t, e.dir, "the decoy question")
	cfg := &Config{Earlier: &Earlier{Questions: []*toolio.Question{{Text: "Question one?", Options: []string{"x"}}, {Text: "Question two?", Options: []string{"y", "z"}}, nil}}}
	e.app = New(cfg)
	e.app.NewForge = e.forge.NewForge("test-token")

	code, env := e.run("", "triage", "brief", e.forge.IssueURL(7), "--dir", e.dir, "--context", "first", "--context", "second", "--context", "third")

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	brief := string(readFile(t, resultOf(t, env)["brief"].(string)))
	if strings.Count(brief, "The question you asked:") != 1 || !strings.Contains(brief, "Question two?") || !strings.Contains(brief, "A. y") || !strings.Contains(brief, "B. z") {
		t.Errorf("the brief does not hold exactly the question Q2:\n%s", brief[max(0, len(brief)-1200):])
	}
	if strings.Contains(brief, "Question one?") || strings.Contains(brief, "decoy") {
		t.Errorf("the brief holds the question of an earlier value or of a run directory:\n%s", brief[max(0, len(brief)-1200):])
	}
	for _, value := range []string{"first", "second", "third"} {
		if !strings.Contains(brief, value) {
			t.Errorf("the brief lacks the value %q", value)
		}
	}
	if len(codes(env)) != 0 {
		t.Errorf("warnings = %v, want none", codes(env))
	}
	if pq := resultOf(t, env)["previous_question"]; pq == nil {
		t.Errorf("the result names no previous question")
	}
}

// TestConcurrentInvocationsShareNothing_TS_13_10 verifies TS-13-10: for any pair of
// invocations (triage or fix, an issue or text), each with an App and seams of its own, run
// at the same time, both complete under -race, the working directory is where it was,
// and what the process environment holds is read by neither, whose lookups are the
// callers'.
// Verifies: 13-REQ-1.8
func TestConcurrentInvocationsShareNothing_TS_13_10(t *testing.T) {
	requireMake(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	type invocation struct {
		name string
		args func(e *fixEnv) []string
	}
	invocations := []invocation{
		{"triage of an issue", func(e *fixEnv) []string {
			return []string{"triage", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--sandbox", "inbox", "--script", script(e.t, submitStep(`{}`, "refused"))}
		}},
		{"triage of text", func(e *fixEnv) []string {
			return []string{"triage", "the greeting is wrong", "--dir", e.dir, "--repo", "o/r", "--land", "none", "--brain", "scripted", "--sandbox", "inbox", "--script", script(e.t, submitStep(`{}`, "refused"))}
		}},
		{"fix of an issue", func(e *fixEnv) []string {
			return []string{"fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--sandbox", "inbox", "--script", script(e.t, fixSteps()...), "--attempts", "1"}
		}},
		{"fix of text", func(e *fixEnv) []string {
			return []string{"fix", "the greeting is wrong", "--dir", e.dir, "--repo", "o/r", "--land", "none", "--brain", "scripted", "--sandbox", "inbox", "--script", script(e.t, fixSteps()...), "--attempts", "1"}
		}},
	}

	rng := rand.New(rand.NewSource(13))
	for round := range 4 {
		a, b := invocations[rng.Intn(len(invocations))], invocations[rng.Intn(len(invocations))]
		t.Run(a.name+" beside "+b.name, func(t *testing.T) {
			type outcome struct {
				p    *inProcess
				code int
				env  map[string]any
				err  error
			}
			results := make([]outcome, 2)
			var calls [2][]string
			for i, inv := range []invocation{a, b} {
				e := newFixEnvWith(t, fakeforge.GitHub, smokeFiles(), true)
				results[i].p = newInProcess(t, e.forge)
				calls[i] = inv.args(e)
			}
			sentinels(t) // after the environments, which blank the variables of a forge
			var wg sync.WaitGroup
			for i := range results {
				wg.Add(1)
				go func() {
					defer wg.Done()
					results[i].code, results[i].env, results[i].err = results[i].p.main("", calls[i]...)
				}()
			}
			wg.Wait()

			for i, r := range results {
				if r.err != nil || r.env == nil || r.env["status"] == nil {
					t.Errorf("invocation %d: no envelope (%v)", i, r.err)
					continue
				}
				if r.env["status"] == "usage" || r.env["status"] == "failed" && resultFailure(r.env) == "internal" {
					t.Errorf("invocation %d did not complete: status %v, error %v", i, r.env["status"], r.env["error"])
				}
				r.p.mu.Lock()
				for _, model := range r.p.models {
					if model == "other" {
						t.Errorf("invocation %d made a brain with the model of the process environment", i)
					}
				}
				for _, boxEnv := range r.p.boxEnvs {
					if len(boxEnv) != 0 {
						t.Errorf("invocation %d created a box with the environment %v", i, boxEnv)
					}
				}
				r.p.mu.Unlock()
			}
			if now, err := os.Getwd(); err != nil || now != cwd {
				t.Errorf("the working directory is %q (%v), was %q", now, err, cwd)
			}
			if got := os.Getenv("AF_MODEL"); got != "other" {
				t.Errorf("AF_MODEL is %q, was set to other: an invocation changed the environment", got)
			}
		})
		_ = round
	}
}

// resultFailure is the category of the error of an envelope, or "".
func resultFailure(env map[string]any) string {
	errObj, _ := env["error"].(map[string]any)
	category, _ := errObj["category"].(string)
	return category
}
