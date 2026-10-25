package afcmd

import (
	"bytes"
	"context"
	"os"
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
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

func TestConfig_TheZeroValueIsTheAfBinary(t *testing.T) {
	t.Setenv("AF_MODEL", "from-the-process")
	cfg := &Config{Version: "v1", Commit: "abc"}

	opts := cfg.brainOptions()

	if cfg.boxes() != box.Default || cfg.brains() != brain.Default || reflect.ValueOf(cfg.git()).Pointer() != reflect.ValueOf(gitx.ExecRunner).Pointer() {
		t.Error("the registries or the git runner are not af's")
	}
	if opts.Model != "from-the-process" || opts.AFVersion != "v1" || opts.AFCommit != "abc" || opts.Boxes != box.Default {
		t.Errorf("options %+v, want the model of AF_MODEL, the identity of the program and box.Default", opts)
	}
	if app := New(cfg); app.Version != "v1" || app.Commit != "abc" || app.NewForge != nil {
		t.Errorf("the app is %+v, want the identity of the Config and the forge of the environment", app)
	}
}

func TestConfig_TheBrainOptionsOfACallerAreKept(t *testing.T) {
	boxes := box.NewRegistry()
	cfg := &Config{Version: "v1", Boxes: boxes, Brain: brain.Options{Model: "m", AFVersion: "v0", Getenv: func(string) (string, bool) { return "x", true }}}

	opts := cfg.brainOptions()

	if opts.Model != "m" || opts.AFVersion != "v0" || opts.AFCommit != "" || opts.Boxes != boxes {
		t.Errorf("options %+v, want the model, the identity and the boxes of the caller", opts)
	}
}

func TestConfig_TheQuestionsOfACallerSayWhatEachContextValueAnswers(t *testing.T) {
	q := &toolio.Question{Text: "Which?", Options: []string{"a", "b"}}
	answers := []string{"material", "B", "past the end"}
	supplied := &Config{Earlier: &Earlier{Questions: []*toolio.Question{nil, q}}}
	want := []fix.Earlier{{Answer: "material", Material: true}, {Answer: "B", Question: "Which?", Options: []string{"a", "b"}, Found: true}, {Answer: "past the end", Material: true}}

	if got := supplied.suppliedEarlier(answers); !reflect.DeepEqual(got, want) {
		t.Errorf("earlier %+v, want %+v", got, want)
	}
	if got := supplied.materialFlags(3); !slices.Equal(got, []bool{true, false, true}) {
		t.Errorf("material %v", got)
	}
	if got := supplied.previousQuestion("", "", "", 3); !reflect.DeepEqual(got, &triage.Question{Text: "Which?", Options: []string{"a", "b"}}) {
		t.Errorf("the previous question of three values is %+v, want the one of the last that has one", got)
	}
	if got := supplied.previousQuestion("", "", "", 1); got != nil {
		t.Errorf("the previous question of a value that answers none is %+v", got)
	}

	none, dir := &Config{}, t.TempDir()
	if none.suppliedEarlier(answers) != nil || none.materialFlags(3) != nil || none.previousQuestion(dir, "f", "", 1) != nil {
		t.Error("a Config with no questions supplies some")
	}
	if got := none.earlierOf(dir, "key", []string{"B"}, ""); len(got) != 1 || got[0].Found || got[0].Material {
		t.Errorf("af's own search of no run directory = %+v, want an answer whose question is not found", got)
	}
}

func TestLoopOptions_OfACallStandInForTheFlags(t *testing.T) {
	s := brain.Settings{Brain: "agentkit", Budget: 10, MaxTurns: 150}
	LoopOptions{Brain: "scripted", Sandbox: "local", Budget: 2, MaxTurns: 3, Script: "s.json"}.apply(&s)
	if s != (brain.Settings{Brain: "scripted", Sandbox: "local", Budget: 2, MaxTurns: 3, Script: "s.json"}) {
		t.Errorf("settings %+v, want the options over the defaults", s)
	}

	e := newEnv(t, repoFiles(), true)
	spy := e.spy(&spyBrain{})
	call := func(path string, options any) *toolio.Envelope {
		env, _ := New(&e.cfg).Invoke(context.Background(), toolio.Call{Path: path, Args: []string{e.forge.IssueURL(7)}, Dir: e.dir, Options: options})
		return env
	}
	for _, path := range []string{"triage", "fix"} {
		env := call(path, LoopOptions{Brain: "spy", Sandbox: "local", Budget: 3, MaxTurns: 7, Land: "none"})
		if spy.brief == nil || spy.brief.MaxTurns != 7 || spy.brief.MaxCostUSD != 3 || env.Status == toolio.StatusUsage {
			t.Errorf("%s: envelope %+v, brief %+v; want the brain and the bounds of the options", path, env, spy.brief)
		}
		spy.brief = nil
	}
	for name, env := range map[string]*toolio.Envelope{
		"another type":              call("fix", "pr"),
		"a landing that is not one": call("fix", LoopOptions{Land: "somewhere"}),
		"a pointer":                 call("triage", &LoopOptions{}),
		"a command that takes none": call("triage run", LoopOptions{}),
	} {
		if env.Status != toolio.StatusUsage {
			t.Errorf("%s: status %s, want usage", name, env.Status)
		}
	}
}

func TestHelp_ShowsTheDefaultsOfTheLoopsAndHidesTheCommandsOfTheBox(t *testing.T) {
	help := func(args ...string) string {
		var out bytes.Buffer
		New(&Config{}).Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(""), &out, &out)
		return out.String()
	}
	for command, defaults := range map[string][]string{
		"fix":    {"(default agentkit)", "(default 10)", "(default 150)", "(default pr)", "(default 45m0s)"},
		"triage": {"(default 2)", "(default 100)", "(default issue)", "(default 15m0s)"},
	} {
		if text := help("help", command); !containsAll(text, defaults...) {
			t.Errorf("af help %s does not show %v:\n%s", command, defaults, text)
		}
	}
	if text := help("help"); strings.Contains(text, "fix run") || strings.Contains(text, "triage run") || !containsAll(text, "lang detect", "gate run", "revert-check") {
		t.Errorf("af help lists the commands of the box, or not the others:\n%s", text)
	}
}

func containsAll(s string, subs ...string) bool {
	return !slices.ContainsFunc(subs, func(sub string) bool { return !strings.Contains(s, sub) })
}

func TestConfig_TheSeamsOfACallerStandInForTheProcess(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	t.Setenv("AF_MODEL", "other")
	t.Setenv("AF_SANDBOX", "bogus")
	var forges, gits atomic.Int32
	build := e.cfg.Forge
	e.cfg.Forge = func(o issuex.Options) (issuex.Client, error) { forges.Add(1); return build(o) }
	e.cfg.Git = func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		gits.Add(1)
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	}
	var mu sync.Mutex
	var looked []string
	e.cfg.Brain.Getenv = func(name string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		looked = append(looked, name)
		return "", false
	}
	var model string
	spy := &spyBrain{}
	e.cfg.Brains.Register(brain.Entry{Name: "spy", CostSource: brain.CostNone, New: func(o brain.Options) (brain.Brain, error) { model = o.Model; return spy, nil }})

	o := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--land", "none")

	if spy.runs != 1 || forges.Load() == 0 || gits.Load() == 0 || e.opened.Load() != 1 || model != "" {
		t.Errorf("%v: %d runs, %d forges, %d gits, %d boxes, model %q; want each seam used, and no model of the process", o, spy.runs, forges.Load(), gits.Load(), e.opened.Load(), model)
	}
	if mu.Lock(); !slices.Contains(looked, "AF_MODEL") || !slices.Contains(looked, "AF_SANDBOX") {
		t.Errorf("the lookup of the caller was asked for %v, want AF_MODEL and AF_SANDBOX", looked)
	}
	mu.Unlock()
}

func TestConfig_InvocationsAtTheSameTimeShareNothing(t *testing.T) {
	cwd, _ := os.Getwd()
	triaged, fixed := newEnv(t, repoFiles(), false), newEnv(t, repoFiles(), true)
	fixed.spy(&spyBrain{})
	calls := []struct {
		e    *env
		args []string
	}{
		{triaged, append([]string{"triage", triaged.forge.IssueURL(7), "--dir", triaged.dir, "--sandbox", "local", "--land", "none"}, scripted(t, submitStep(diagnosis(nil), "accepted"))...)},
		{fixed, []string{"fix", fixed.forge.IssueURL(7), "--dir", fixed.dir, "--sandbox", "local", "--land", "none", "--brain", "spy"}},
	}
	outs := make([]bytes.Buffer, len(calls))
	var wg sync.WaitGroup
	for i, c := range calls {
		wg.Go(func() {
			New(&c.e.cfg).Main(context.Background(), append([]string{"af"}, c.args...), strings.NewReader(""), &outs[i], &bytes.Buffer{})
		})
	}
	wg.Wait()

	for i := range outs {
		env := testutil.Decode[toolio.Envelope](t, outs[i].Bytes())
		if env.Status == toolio.StatusUsage || env.Error != nil && env.Error.Category == toolio.CategoryInternal || env.RunID == nil {
			t.Errorf("invocation %d: %+v", i, env)
		}
	}
	if triaged.opened.Load() != 1 || fixed.opened.Load() != 1 {
		t.Errorf("boxes %d and %d, want one in the registry of each", triaged.opened.Load(), fixed.opened.Load())
	}
	if now, _ := os.Getwd(); now != cwd {
		t.Errorf("the working directory is %q, was %q", now, cwd)
	}
}

func TestTheLoops_ADefaultBoxThatCannotOpenNamesTheFlagAndTheVariable(t *testing.T) {
	e := newEnv(t, repoFiles(), true)
	spy := e.spy(&spyBrain{})
	e.cfg.Boxes.Register(box.Entry{Name: "brokenbox", Build: func(box.ProviderConfig) (sandbox.Provider, error) {
		return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: "no engine is running here"}
	}})
	e.vars["AF_SANDBOX"] = "brokenbox"
	for name, tc := range map[string]struct {
		args []string
		hint bool
	}{
		"af triage":           {[]string{"triage", e.forge.IssueURL(7)}, true},
		"af fix":              {[]string{"fix", e.forge.IssueURL(7)}, true},
		"af triage --sandbox": {[]string{"triage", e.forge.IssueURL(7), "--sandbox", "brokenbox"}, false},
	} {
		o := e.af(append(tc.args, "--dir", e.dir, "--brain", "spy", "--land", "none")...)

		if o.code != 1 || o.category() != "sandbox" || !strings.Contains(o.message(), "no engine is running here") {
			t.Errorf("%s: %v; want the sandbox failure of the provider", name, o)
		}
		if got := o.hint().Flag == "--sandbox" && o.hint().Env == "AF_SANDBOX"; got != tc.hint {
			t.Errorf("%s: hint %+v, want --sandbox and AF_SANDBOX: %v", name, o.hint(), tc.hint)
		}
	}
	if spy.runs != 0 {
		t.Error("a brain ran with no box")
	}
}
