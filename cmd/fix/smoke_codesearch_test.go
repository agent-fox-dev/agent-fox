package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/agentfox/agentkit-go/codesearch"
	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/provider/faux"
	"github.com/agentfox/agentkit-go/tools"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/agentrun/indextest"
	"github.com/agent-fox-dev/agentfox/internal/envtest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The smoke tests below run fix's own App — the one main runs: the shell, the
// entry point's index lifecycle, the real Runner, codefix.Run, the real tools
// and the real git — with two things stood in for: the index (the recording
// indextest.Probe takes the place of codesearch.New) and the model (a scripted
// provider/faux takes the place of a vendor, through toolio.SetRunnerConfigHook).

// fixTurns scripts a fix run that succeeds. navigate is the tool call each of
// the two phases opens with: code_search when the run has an index, read_file
// when it has not.
func fixTurns(navigate string) []faux.Turn {
	nav := func(id string) faux.Turn {
		args := map[string]any{"query": "Count"}
		if navigate == "read_file" {
			args = map[string]any{"path": "main.go"}
		}
		return indextest.ToolTurn(id, navigate, args)
	}
	return []faux.Turn{
		nav("a1"),
		indextest.ToolTurn("a2", "submit_analysis", map[string]any{
			"classification": "bug",
			"title":          "fix the counter",
			"summary":        "the counter double-counts",
			"root_cause":     "main.go counts twice",
			"approach":       "count once",
			"files":          []map[string]any{{"path": "main.go", "change": "count once"}},
		}),
		nav("b1"),
		indextest.ToolTurn("b2", "write_file", map[string]any{"path": "main.go", "content": "package x\n\nfunc Count() int { return 2 }\n"}),
		indextest.ToolTurn("b3", "submit_implementation", map[string]any{
			"commit_subject": "fix: count once",
			"summary":        "counted once",
			"changes":        []map[string]any{{"path": "main.go", "change": "count once"}},
		}),
	}
}

// scriptModel makes the shell build its Runner on a scripted provider for the test.
func scriptModel(t *testing.T, turns []faux.Turn) *faux.Provider {
	t.Helper()
	p := faux.New(turns...)
	t.Cleanup(toolio.SetRunnerConfigHook(func(cfg *agentrun.Config) {
		cfg.Model = faux.Model()
		cfg.Thinking = core.ThinkingUnset
		cfg.Providers = core.ProviderRegistry{faux.API: p.APIProvider()}
	}))
	return p
}

// useProbe makes the entry point's index builder return probe, rooted at the
// workspace it is asked to index, in place of codesearch.New.
func useProbe(t *testing.T, probe *indextest.Probe) {
	t.Helper()
	t.Cleanup(toolio.SetIndexBuilder(func(ws *tools.Workspace) (tools.Index, error) {
		probe.Root = ws.Root
		return probe, nil
	}))
}

// useBuildError makes the entry point's index builder fail with err.
func useBuildError(t *testing.T, err error) {
	t.Helper()
	t.Cleanup(toolio.SetIndexBuilder(func(*tools.Workspace) (tools.Index, error) { return nil, err }))
}

// smokeResult is one run of the real App.
type smokeResult struct {
	code int
	env  map[string]any
	out  string
	err  string
}

func (r smokeResult) warning(code, severity string) bool {
	return indextest.HasWarning(r.env, code, severity)
}

func (r smokeResult) stage() string {
	res, _ := r.env["result"].(map[string]any)
	s, _ := res["stage"].(string)
	return s
}

func (r smokeResult) preflightCheck(name string) (map[string]any, bool) {
	res, _ := r.env["result"].(map[string]any)
	list, _ := res["preflight"].([]any)
	for _, e := range list {
		m, _ := e.(map[string]any)
		if m["check"] == name {
			return m, true
		}
	}
	return nil, false
}

// runSmoke drives fix's real App on a fresh repository. It returns the
// repository root with the result.
func runSmoke(t *testing.T, extra ...string) (smokeResult, string) {
	t.Helper()
	envtest.Clean(t)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := preflightRepo(t)
	argv := append(extra, "--dir", dir, "--land", "none", "--verify", "true", "--no-review", "the counter double-counts")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var stdout, stderr bytes.Buffer
	code := newApp().Main(ctx, normalizeArgs(argv), strings.NewReader(""), &stdout, &stderr)
	r := smokeResult{code: code, out: stdout.String(), err: stderr.String()}
	if err := json.Unmarshal(stdout.Bytes(), &r.env); err != nil {
		t.Fatalf("stdout is not JSON: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	return r, dir
}

// TS-16-32 (smoke): a successful fix run offers code_search in every phase,
// the model's searches reach the index, the index is invalidated between the
// phases, and it is closed exactly once on the way out.
//
// Verifies: 16-PATH-1, 16-REQ-1.1, 16-REQ-2.1, 16-REQ-3.2
//
// Real components: the cmd/fix App, toolio.App.Main, agentrun.Runner, codefix.Run, tools.All
func TestTS16_32_FixOffersCodeSearchInEveryPhase_Smoke(t *testing.T) {
	probe := &indextest.Probe{}
	useProbe(t, probe)
	p := scriptModel(t, fixTurns("code_search"))

	r, _ := runSmoke(t)
	if r.code != toolio.ExitOK || r.stage() != "landed" {
		t.Fatalf("exit %d, stage %q; stdout:\n%s\nstderr:\n%s", r.code, r.stage(), r.out, r.err)
	}

	// Every request of both phases was offered code_search beside the read tools.
	reqs := indextest.Offered(p)
	if len(reqs) != 5 {
		t.Fatalf("the model received %d requests, want 5 (two phases): %v", len(reqs), reqs)
	}
	for i, names := range reqs {
		if !indextest.Has(names, "code_search") || !indextest.Has(names, "read_file") {
			t.Errorf("request %d offered %v: want code_search and read_file", i, names)
		}
	}

	// The model used it in each phase, and the call reached the index.
	searches := probe.Searches()
	if len(searches) != 2 {
		t.Fatalf("code_search reached the index %d times, want 2 (once per phase)", len(searches))
	}

	// The branch is created between the two searches, and the index was told.
	between := probe.Between(searches[0], searches[1])
	found := false
	for _, s := range between {
		if s.Rel == "" && strings.HasPrefix(s.Branch, "fix/") {
			found = true
		}
	}
	if !found {
		t.Errorf("no Invalidate(\"\") on the fix branch between the analyse and implement phases: %+v", probe.Snaps())
	}

	if n := probe.CloseCalls(); n != 1 {
		t.Errorf("the index was closed %d times, want exactly 1", n)
	}
}

// runWithoutIndex is a fix run whose index cannot be built: the model
// navigates with the read tools alone.
func runWithoutIndex(t *testing.T, buildErr error) (smokeResult, *faux.Provider) {
	t.Helper()
	useBuildError(t, buildErr)
	p := scriptModel(t, fixTurns("read_file"))
	r, _ := runSmoke(t)
	return r, p
}

func assertRunsWithoutCodeSearch(t *testing.T, r smokeResult, p *faux.Provider) {
	t.Helper()
	if r.code != toolio.ExitOK || r.stage() != "landed" {
		t.Fatalf("exit %d, stage %q; the run must complete without the index\nstdout:\n%s\nstderr:\n%s", r.code, r.stage(), r.out, r.err)
	}
	if !r.warning("code_search_unavailable", "low") {
		t.Errorf("no low code_search_unavailable warning in the envelope: %v", r.env["warnings"])
	}
	reqs := indextest.Offered(p)
	if len(reqs) != 5 {
		t.Fatalf("the model received %d requests, want 5: %v", len(reqs), reqs)
	}
	for i, names := range reqs {
		if indextest.Has(names, "code_search") {
			t.Errorf("request %d offered code_search without an index: %v", i, names)
		}
		if !indextest.Has(names, "read_file") || !indextest.Has(names, "search_files") {
			t.Errorf("request %d lost the read tools: %v", i, names)
		}
	}
}

// TS-16-31 (integration): the full pipeline completes when the index build
// fails, offers no phase code_search, and warns.
//
// Verifies: 16-REQ-3.4, 16-REQ-2.2
func TestTS16_31_FixCompletesWhenTheIndexBuildFails(t *testing.T) {
	r, p := runWithoutIndex(t, errors.New("disk full"))
	assertRunsWithoutCodeSearch(t, r, p)
	if !strings.Contains(r.out, "disk full") {
		t.Errorf("the warning does not say why the index is unavailable: %s", r.out)
	}
}

// TS-16-33 (smoke): the same when the platform is unsupported.
//
// Verifies: 16-PATH-2, 16-REQ-3.4
//
// Real components: the cmd/fix App, agentrun.Runner, toolio.Run's warning system
func TestTS16_33_FixRunsWithoutCodeSearchOnAnUnsupportedPlatform_Smoke(t *testing.T) {
	r, p := runWithoutIndex(t, codesearch.ErrUnsupported)
	assertRunsWithoutCodeSearch(t, r, p)
}

// TS-16-35 (smoke): --preflight reports whether the index was built, and is
// informational whichever way it went.
//
// Verifies: 16-PATH-4, 16-REQ-7.1, 16-REQ-7.3
//
// Real components: the cmd/fix App, codefix.RunPreflight, toolio.PreflightCheck
func TestTS16_35_FixPreflightReportsIndexAvailability_Smoke(t *testing.T) {
	check := func(t *testing.T) map[string]any {
		t.Helper()
		r, _ := runSmoke(t, "--preflight")
		if r.code != toolio.ExitOK {
			t.Fatalf("--preflight exited %d\nstdout:\n%s\nstderr:\n%s", r.code, r.out, r.err)
		}
		c, ok := r.preflightCheck("code_search_index")
		if !ok {
			t.Fatalf("no code_search_index entry in the preflight result: %s", r.out)
		}
		if c["ok"] != true {
			t.Errorf("code_search_index ok = %v, want true", c["ok"])
		}
		return c
	}

	t.Run("built", func(t *testing.T) {
		probe := &indextest.Probe{}
		useProbe(t, probe)
		if c := check(t); c["detail"] != "built" {
			t.Errorf("detail = %q, want built", c["detail"])
		}
		if n := probe.CloseCalls(); n != 1 {
			t.Errorf("--preflight closed the index %d times, want 1", n)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		useBuildError(t, codesearch.ErrUnsupported)
		c := check(t)
		want := "unavailable: " + codesearch.ErrUnsupported.Error()
		if c["detail"] != want {
			t.Errorf("detail = %q, want %q", c["detail"], want)
		}
	})
	t.Run("real index", func(t *testing.T) {
		// codesearch.New itself, on a real repository: built or unavailable,
		// the check is there and does not refuse.
		c := check(t)
		d, _ := c["detail"].(string)
		if d != "built" && !strings.HasPrefix(d, "unavailable: ") {
			t.Errorf("detail = %q, want built or unavailable: <reason>", d)
		}
	})
}
