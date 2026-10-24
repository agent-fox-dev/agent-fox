package afcmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/provider/faux"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/triage"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// spyBrain is a brain that remembers the brief it was given, and does what its
// function says.
type spyBrain struct {
	brief *brain.Brief
	runs  int
	news  int // how many times the registry made it
	run   func(ctx context.Context, b brain.Brief) (brain.Outcome, error)
}

func (s *spyBrain) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	s.runs++
	cp := b
	s.brief = &cp
	if s.run != nil {
		return s.run(ctx, b)
	}
	return brain.Outcome{StopReason: brain.StopEnded}, nil
}

// spyRegistry is a registry with the brains of the release and the spy, as "spy".
func spyRegistry(t *testing.T, s *spyBrain) *brain.Registry {
	t.Helper()
	reg := brain.NewRegistry()
	for _, name := range []string{brain.AgentkitName, brain.ScriptedName} {
		entry, err := brain.Default.Lookup(name)
		if err != nil {
			t.Fatal(err)
		}
		reg.Register(entry)
	}
	reg.Register(brain.Entry{Name: "spy", CostSource: brain.CostNone, New: func(brain.Options) (brain.Brain, error) { s.news++; return s, nil }})
	return reg
}

// useSpy puts the spy in the registry of the loop as the brain "spy", beside the
// brains of the release.
func useSpy(t *testing.T, s *spyBrain) {
	t.Helper()
	old := testConfig.Brains
	testConfig.Brains = spyRegistry(t, s)
	t.Cleanup(func() { testConfig.Brains = old })
}

// submitting is a spy function that submits the payload the way a brain's tool
// does, through the submit core against the box.
func submitting(payload string) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		target := submission.Target{Tree: b.Box.FS(ctx), Write: b.Box.WriteFile, RunID: b.RunID}
		if _, err := submission.Submit(ctx, target, b.Kind, []byte(payload), time.Now()); err != nil {
			return brain.Outcome{StopReason: brain.StopError}, err
		}
		return brain.Outcome{Submitted: true, StopReason: brain.StopSubmitted}, nil
	}
}

// script writes a script for the scripted brain and returns its path.
func script(t *testing.T, steps ...map[string]any) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"steps": steps})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func submitStep(payload string, expect string) map[string]any {
	var p any
	_ = json.Unmarshal([]byte(payload), &p)
	step := map[string]any{"submit": p}
	if expect != "" {
		step["expect"] = expect
	}
	return step
}

// loop runs af triage with args, in the local box: a test about another provider
// says so in its args, and the last --sandbox is the one that counts.
func (e *triageEnv) loop(args ...string) (int, map[string]any) {
	e.t.Helper()
	return e.run("", append([]string{"triage", "--sandbox", "local"}, args...)...)
}

// TestTheCommandsAreRegisteredWithTheirTimeoutsAndFlags_TS_08_1 verifies TS-08-1:
// the triage commands exist with their default timeouts, and take the flags
// they are said to.
// Verifies: 08-REQ-1.1
func TestTheCommandsAreRegisteredWithTheirTimeoutsAndFlags_TS_08_1(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	common := []string{"--timeout"}
	for _, c := range []struct {
		path    string
		timeout string
		flags   []string
	}{
		{"triage", "15m0s", []string{"--brain", "--budget", "--context", "--dir", "--dry-run", "--label", "--land", "--max-turns", "--repo", "--sandbox", "--script"}},
		{"triage run", "15m0s", []string{"--budget", "--dir", "--max-turns", "--run-id"}},
	} {
		t.Run(c.path, func(t *testing.T) {
			var out strings.Builder
			code := e.app.Main(context.Background(), append([]string{"af"}, append(strings.Fields(c.path), "--help")...), strings.NewReader(""), &out, &out)
			help := out.String()
			if code != 0 {
				t.Fatalf("--help: exit code %d\n%s", code, help)
			}
			// The flags are those of the Flags section, and exactly those: the usage line may name more.
			var got []string
			_, section, _ := strings.Cut(help, "\nFlags:\n")
			for _, line := range strings.Split(section, "\n") {
				if name, _, _ := strings.Cut(strings.TrimSpace(line), " "); strings.HasPrefix(name, "--") {
					got = append(got, name)
				}
			}
			want := append(slices.Clone(c.flags), common...)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("flags = %v, want %v", got, want)
			}
			if want := "(default " + c.timeout + ")"; !strings.Contains(help, want) {
				t.Errorf("--help does not give the default timeout %s:\n%s", want, help)
			}
		})
	}

	// What the loop declares of itself shows in what it does: the input, the forge client
	// and the run directory.
	before := len(e.forge.Requests())
	code, env := e.run("", "triage", "--sandbox", "local", "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")), "--land", "none", e.forge.IssueURL(7), "--dir", e.dir)
	input, _ := env["input"].(map[string]any)
	if code != 0 || env["run_id"] == nil || input["kind"] != "issue" || len(e.forge.Requests()) == before {
		t.Errorf("exit code %d, run_id %v, input %v, %d new requests: want the issue read through the forge client into a run directory",
			code, env["run_id"], input, len(e.forge.Requests())-before)
	}

	// af triage run is the box's, and no command for a person.
	var out strings.Builder
	e.app.Main(context.Background(), []string{"af", "help"}, strings.NewReader(""), &out, &out)
	if strings.Contains(out.String(), "triage run") {
		t.Errorf("af help lists triage run:\n%s", out.String())
	}
}

// TestRunRefusesABadRunID_TS_08_2 verifies TS-08-2 as it stands: a missing, malformed
// or unknown run id is a usage failure of af triage run, with nothing written under
// .agent-fox/run and no request to the forge.
// Verifies: 08-REQ-1.2
func TestRunRefusesABadRunID_TS_08_2(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	for _, command := range []string{"run"} {
		for name, id := range map[string][]string{"no run id": nil, "a malformed one": {"--run-id", "bad-id"}, "an unknown one": {"--run-id", "20240101_000000_abcdef"}} {
			t.Run(command+" with "+name, func(t *testing.T) {
				args := append([]string{"triage", command, "--dir", e.dir}, id...)

				code, env := e.run(`{"title":"x"}`, args...)

				errObj, _ := env["error"].(map[string]any)
				if code != 2 || env["status"] != "usage" || errObj["category"] != "usage" {
					t.Errorf("exit code %d, status %v, error %v, want 2, usage", code, env["status"], errObj)
				}
				if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", "run")); err == nil {
					t.Error("a run directory was made")
				}
				if n := len(e.forge.Requests()); n != 0 {
					t.Errorf("%d requests to the forge", n)
				}
			})
		}
	}
}

// TestTheLedgerHoldsTheTriageRecordsInOrder_TS_08_4 verifies TS-08-4:
// a full loop records triage.input, triage.brief, triage.submission and
// triage.land in that order, and a run that stops on a question records
// triage.question and no triage.land.
// Verifies: 08-REQ-1.4
func TestTheLedgerHoldsTheTriageRecordsInOrder_TS_08_4(t *testing.T) {
	triageKinds := func(runDir string) []string {
		var out []string
		for _, k := range ledgerKinds(t, runDir) {
			if strings.HasPrefix(k, "triage.") {
				out = append(out, k)
			}
		}
		return out
	}

	e := newTriageEnv(t, fakeforge.GitHub)
	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))
	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if got := triageKinds(e.runDir(env)); !slices.Equal(got, []string{"triage.input", "triage.brief", "triage.submission", "triage.land"}) {
		t.Errorf("ledger = %v, want triage.input, triage.brief, triage.submission, triage.land", got)
	}

	question := diagnosisPayload(func(m map[string]any) {
		m["confidence"] = "suspected"
		m["question"] = map[string]any{"text": "Which session?", "options": []any{"old", "new"}}
	})
	e = newTriageEnv(t, fakeforge.GitHub)
	code, env = e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--script", script(t, submitStep(question, "accepted")))
	if code != 3 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if got := triageKinds(e.runDir(env)); !slices.Equal(got, []string{"triage.input", "triage.brief", "triage.submission", "triage.question"}) {
		t.Errorf("ledger = %v, want triage.input, triage.brief, triage.submission, triage.question and no triage.land", got)
	}
}

// TestTriageRunsOnADirtyTreeAndChangesNothingInIt_TS_08_5 verifies TS-08-5:
// a loop with --land none and a dry run both work on a dirty tree, and
// leave git's status and HEAD as they were, adding files only under .agent-fox/.
// Verifies: 08-REQ-1.5
func TestTriageRunsOnADirtyTreeAndChangesNothingInIt_TS_08_5(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	// Uncommitted work in a file the diagnosis cites: the run reads the tree as it is.
	main := filepath.Join(e.dir, "main.go")
	if err := os.WriteFile(main, append(readFile(t, main), []byte("\n// uncommitted\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dir, "untracked.txt"), []byte("not added\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	status, head := git2(t, e.dir, "status", "--porcelain"), git2(t, e.dir, "rev-parse", "HEAD")
	refs, config := git2(t, e.dir, "for-each-ref"), git2(t, e.dir, "config", "--local", "--list")
	files := func() []string {
		var out []string
		_ = filepath.WalkDir(e.dir, func(path string, d os.DirEntry, err error) error {
			rel, _ := filepath.Rel(e.dir, path)
			if d.IsDir() && (rel == ".git" || rel == ".agent-fox") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				out = append(out, rel)
			}
			return nil
		})
		return out
	}
	before := files()
	scripted := []string{"--sandbox", "local", "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted"))}

	for name, args := range map[string][]string{
		"a loop, no land": append([]string{"triage", e.forge.IssueURL(7), "--dir", e.dir, "--land", "none"}, scripted...),
		"a dry run":       append([]string{"triage", e.forge.IssueURL(7), "--dir", e.dir, "--dry-run"}, scripted...),
	} {
		t.Run(name, func(t *testing.T) {
			code, env := e.run("", args...)

			if code != 0 || env["status"] != "done" {
				t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
			}
			if got := git2(t, e.dir, "status", "--porcelain"); got != status {
				t.Errorf("git status changed:\n%s\n%s", status, got)
			}
			if got := git2(t, e.dir, "rev-parse", "HEAD"); got != head {
				t.Errorf("HEAD moved from %s to %s", head, got)
			}
			if got := git2(t, e.dir, "for-each-ref"); got != refs {
				t.Errorf("the refs changed:\n%s\n%s", refs, got)
			}
			if got := git2(t, e.dir, "config", "--local", "--list"); got != config {
				t.Errorf("the git configuration changed:\n%s\n%s", config, got)
			}
			if after := files(); !slices.Equal(after, before) {
				t.Errorf("the files outside .git and .agent-fox changed:\n%v\n%v", before, after)
			}
		})
	}
}

// TestARefusedPreflightRendersNoBriefAndCostsNothing_TS_08_8 verifies TS-08-8:
// a preflight refusal leaves no brief.md, asks the model nothing, writes nothing
// to the forge, and the flags are reported before the input.
// Verifies: 08-REQ-2.1
func TestARefusedPreflightRendersNoBriefAndCostsNothing_TS_08_8(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	p := faux.New(faux.Text("unused"))
	fauxBrains(t, p)
	pull := e.forge.IssueURL(7)
	pull = strings.Replace(pull, "/issues/", "/pull/", 1)

	for name, tc := range map[string]struct {
		args    []string
		mention string
	}{
		"a bad label":                    {[]string{"triage", e.forge.IssueURL(7), "--label", "a,b", "--sandbox", "local"}, "a,b"},
		"a pull request":                 {[]string{"triage", pull, "--sandbox", "local"}, "pull or merge request"},
		"a bad label and a pull request": {[]string{"triage", pull, "--label", "a,b", "--sandbox", "local"}, "a,b"},
		"no credential":                  {[]string{"triage", e.forge.IssueURL(7), "--sandbox", "local"}, "needs a credential"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "no credential" {
				e.app.NewForge = e.forge.NewForge("")
				defer func() { e.app.NewForge = e.forge.NewForge("test-token") }()
			}
			code, env := e.run("", append(tc.args, "--dir", e.dir)...)

			errObj, _ := env["error"].(map[string]any)
			if code == 0 || !strings.Contains(errObj["message"].(string), tc.mention) {
				t.Fatalf("exit code %d, error %v, want a refusal that mentions %q", code, errObj, tc.mention)
			}
			matches, _ := filepath.Glob(filepath.Join(e.dir, ".agent-fox", "run", "*", "brief.md"))
			if len(matches) != 0 {
				t.Errorf("a brief was rendered: %v", matches)
			}
			if n := len(p.Requests()); n != 0 {
				t.Errorf("the model got %d requests", n)
			}
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
			if steps := stepNames(t, e.runDir(env)); !slices.Equal(steps, []string{"preflight"}) {
				t.Errorf("steps = %v, want preflight alone", steps)
			}
		})
	}
}

// stepNames are the names of the step events of a run, in order.
func stepNames(t *testing.T, runDir string) []string {
	t.Helper()
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(runDir, "events.jsonl")))), "\n") {
		var ev struct {
			Type string
			Data struct{ Name string }
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Type == "step" {
			names = append(names, ev.Data.Name)
		}
	}
	return names
}

// TestTheBrainIsHandedTheBriefTheToolsAndTheBounds_TS_08_24 verifies TS-08-24:
// the Brief the brain gets has the prefix and the document of brief.md, the triage
// tools, the read-only hook and the bounds of the flags.
// Verifies: 08-REQ-4.1
func TestTheBrainIsHandedTheBriefTheToolsAndTheBounds_TS_08_24(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	spy := &spyBrain{}
	useSpy(t, spy)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--budget", "3", "--max-turns", "40", "--timeout", "10m")

	if code != 1 || spy.runs != 1 || spy.brief == nil {
		t.Fatalf("exit code %d (%v), the spy ran %d times", code, env["error"], spy.runs)
	}
	b := spy.brief
	brief := string(readFile(t, filepath.Join(e.runDir(env), "brief.md")))
	var facts triage.Facts
	if err := json.Unmarshal(readFile(t, filepath.Join(e.runDir(env), "triage.json")), &facts); err != nil {
		t.Fatal(err)
	}
	if b.Document() != brief || b.Prefix != brief[:facts.Prefix.Bytes] || sha256Of([]byte(b.Prefix)) != facts.Prefix.SHA256 {
		t.Errorf("the brief the brain got is not brief.md: prefix %d bytes, document %d bytes, brief.md %d bytes", len(b.Prefix), len(b.Document()), len(brief))
	}
	if !strings.Contains(b.Task, "file_diagnosis") || strings.Contains(b.Task, "af triage submit") {
		t.Errorf("the task block should name the tool of an in-process brain:\n%s", b.Task)
	}
	for _, name := range []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references"} {
		if !slices.Contains(b.Tools, name) {
			t.Errorf("tools = %v, want %s", b.Tools, name)
		}
	}
	for _, banned := range []string{"write_file", "edit_file", "execute", "run_command", "file_diagnosis"} {
		if slices.Contains(b.Tools, banned) {
			t.Errorf("tools = %v: %s is not a tool of the box a triage brain reads with", b.Tools, banned)
		}
	}
	if b.Kind.ToolName != "file_diagnosis" || b.Kind.Name != "triage" || !slices.Contains(b.Hook.ProtectedPaths, ".agent-fox/") || b.Hook.RunID != env["run_id"] || len(b.Hook.AllowPrograms) != 0 {
		t.Errorf("kind %q/%q, hook %+v", b.Kind.Name, b.Kind.ToolName, b.Hook)
	}
	if !strings.Contains(b.Task, "You have at most 40 turns, $3.00 of model cost and ") {
		t.Errorf("the task block should remind the brain of the bounds of this run:\n%s", b.Task)
	}
	if labels := b.Box.Spec().Labels; labels["af.tool"] != "triage" || labels["af.run_id"] != env["run_id"] {
		t.Errorf("the box is labelled %v, want af.tool triage and the run id", labels)
	}
	if b.MaxCostUSD != 3 || b.MaxTurns != 40 || b.Timeout <= 0 || b.Timeout > 10*time.Minute || b.Attempt != 1 || b.RunID != env["run_id"] {
		t.Errorf("bounds: cost %v, turns %d, timeout %v, attempt %d", b.MaxCostUSD, b.MaxTurns, b.Timeout, b.Attempt)
	}
}

// TestOmittedBoundsDefaultAndAgentkitIsTheDefaultBrain_TS_08_25 verifies TS-08-25:
// without --budget, --max-turns, --timeout and --brain the loop runs the agentkit
// brain with USD 2, 100 turns and 15 minutes.
// Verifies: 08-REQ-4.2
func TestOmittedBoundsDefaultAndAgentkitIsTheDefaultBrain_TS_08_25(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	p := faux.New(call("c1", "file_diagnosis", diagnosisPayload(nil)))
	fauxBrains(t, p)
	spy := &spyBrain{}
	useSpy(t, spy)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--land", "none")

	if code != 0 || p.Calls() != 1 {
		t.Fatalf("exit code %d (%v), the faux provider got %d requests: the default brain is agentkit", code, env["error"], p.Calls())
	}
	if env["usage"].(map[string]any)["brain"] != "agentkit" {
		t.Errorf("usage = %v", env["usage"])
	}

	code, env = e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--land", "none")
	if spy.brief == nil {
		t.Fatalf("exit code %d, error %v: the spy did not run", code, env["error"])
	}
	if spy.brief.MaxCostUSD != 2 || spy.brief.MaxTurns != 100 || spy.brief.Timeout <= 14*time.Minute || spy.brief.Timeout > 15*time.Minute {
		t.Errorf("defaults: cost %v, turns %d, timeout %v, want 2, 100 and just under 15 minutes", spy.brief.MaxCostUSD, spy.brief.MaxTurns, spy.brief.Timeout)
	}
}

// TestTheLoopReadsBackBothFilesAndChecksTheHostTree_TS_08_28 verifies TS-08-28:
// after the brain the submission and the refusals are in the run directory, and
// a submission that cites a file the host no longer has is treated as absent.
// Verifies: 08-REQ-4.5
func TestTheLoopReadsBackBothFilesAndChecksTheHostTree_TS_08_28(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	missing := diagnosisPayload(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "nowhere.go", "note": "x"}}
	})

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--land", "none",
		"--script", script(t, submitStep(missing, "refused"), submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	dir := e.runDir(env)
	var file struct{ Payload triage.Diagnosis }
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "submission.json")), &file); err != nil || file.Payload.Title != "Run starts the wrong thing" {
		t.Errorf("submission.json = %v (%v)", file, err)
	}
	rejections := string(readFile(t, filepath.Join(dir, "rejections.jsonl")))
	if strings.Count(rejections, "\n") != 1 || !strings.Contains(rejections, `"unresolved_paths":["nowhere.go"]`) {
		t.Errorf("rejections.jsonl:\n%s", rejections)
	}
	if got := resultOf(t, env); got["rejected_path_calls"] != float64(1) || got["rejected_submissions"] != float64(1) {
		t.Errorf("result = %v, want 1 rejected path call and 1 rejected submission", got)
	}

	t.Run("a file the host no longer has", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		spy := &spyBrain{}
		submit := submitting(diagnosisPayload(nil))
		spy.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			out, err := submit(ctx, b)
			// The file is deleted from the checkout of the host after the box had it.
			if rerr := os.Remove(filepath.Join(e.dir, "util", "util.go")); rerr != nil {
				t.Error(rerr)
			}
			if rerr := os.Remove(filepath.Join(e.dir, "main.go")); rerr != nil {
				t.Error(rerr)
			}
			return out, err
		}
		useSpy(t, spy)

		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--land", "none")

		errObj, _ := env["error"].(map[string]any)
		if code != 1 || errObj["category"] != "no_result" || env["result"] != nil {
			t.Fatalf("exit code %d, error %v, want failed/no_result", code, errObj)
		}
		if _, err := os.Stat(filepath.Join(e.runDir(env), "submission.json")); err == nil {
			t.Error("the stale submission is in the run directory")
		}
		if n := len(e.forge.Writes()); n != 0 {
			t.Errorf("%d writes to the forge", n)
		}
	})
}

// TestNoSubmissionOrASpentBoundLandsNothing_TS_08_29 verifies TS-08-29:
// a brain that never submits is failed/no_result; one that runs out of turns or
// of budget is failed/budget with the flag to raise and twice the value; none
// writes to the forge, and the brief is an artifact.
// Verifies: 08-REQ-4.6
func TestNoSubmissionOrASpentBoundLandsNothing_TS_08_29(t *testing.T) {
	read := func() map[string]any {
		return map[string]any{"tool": "read_file", "args": map[string]any{"path": "main.go"}}
	}
	for name, tc := range map[string]struct {
		args     func() []string
		prepare  func(*testing.T)
		category string
		flag     string
		suggest  string
	}{
		"a brain that never submits": {func() []string { return []string{"--brain", "scripted", "--script", script(t, read())} }, nil, "no_result", "", ""},
		"a brain out of turns": {func() []string {
			return []string{"--brain", "scripted", "--max-turns", "3", "--script", script(t, read(), read(), read(), read(), read())}
		}, nil, "budget", "--max-turns", "6"},
		"a brain out of budget": {func() []string { return []string{"--budget", "0.5"} }, func(t *testing.T) {
			fauxBrains(t, faux.New(fauxUsage(call("c1", "read_file", `{"path":"main.go"}`), 0.6, 1000, 100), faux.Text("never")))
		}, "budget", "--budget", "1"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newTriageEnv(t, fakeforge.GitHub)
			if tc.prepare != nil {
				tc.prepare(t)
			}

			code, env := e.loop(append([]string{e.forge.IssueURL(7), "--dir", e.dir}, tc.args()...)...)

			errObj, _ := env["error"].(map[string]any)
			if code != 1 || errObj["category"] != tc.category || env["result"] != nil {
				t.Fatalf("exit code %d, error %v, result %v, want failed/%s and no result", code, errObj, env["result"], tc.category)
			}
			if tc.flag != "" {
				hint, _ := errObj["fix_hint"].(map[string]any)
				got, _ := hint["suggest"].(string)
				if hint["flag"] != tc.flag || strings.TrimSuffix(got, ".0") != tc.suggest {
					t.Errorf("fix_hint = %v, want %s suggesting %s", hint, tc.flag, tc.suggest)
				}
			}
			if len(artifactsOf(env, "brief")) != 1 {
				t.Errorf("artifacts = %v, want the brief", env["artifacts"])
			}
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
			if steps := stepNames(t, e.runDir(env)); !slices.Equal(steps, []string{"preflight", "brief", "brain"}) {
				t.Errorf("steps = %v, want preflight, brief, brain", steps)
			}
		})
	}
}

// TestAnUnknownBrainOrSandboxIsRefusedBeforeABriefIsRendered guards 07-REQ-1.2 and
// 1.5 through the loop: the registries are asked in the preflight.
func TestAnUnknownBrainOrSandboxIsRefusedBeforeABriefIsRendered(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	for name, args := range map[string][]string{
		"an unknown brain":                    {"--brain", "nosuch"},
		"an unknown sandbox":                  {"--sandbox", "nosuch"},
		"a script without the scripted brain": {"--script", "x.json"},
		"the scripted brain without a script": {"--brain", "scripted"},
	} {
		t.Run(name, func(t *testing.T) {
			code, env := e.loop(append([]string{e.forge.IssueURL(7), "--dir", e.dir}, args...)...)
			if code != 2 || env["status"] != "usage" {
				t.Errorf("exit code %d, status %v, error %v, want usage", code, env["status"], env["error"])
			}
			if matches, _ := filepath.Glob(filepath.Join(e.dir, ".agent-fox", "run", "*", "brief.md")); len(matches) != 0 {
				t.Errorf("a brief was rendered: %v", matches)
			}
		})
	}
}

// ledgerData is the data of the first ledger entry of a kind.
func ledgerData(t *testing.T, runDir, kind string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(runDir, "ledger.jsonl")))), "\n") {
		var entry struct {
			Kind string
			Data map[string]any
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Kind == kind {
			return entry.Data
		}
	}
	t.Fatalf("the ledger of %s has no %s entry", runDir, kind)
	return nil
}

// TestTheLoopLandsWhatTheBrainSubmitted guards 08-REQ-1.4 and 4.1 through the
// loop: the diagnosis is written to the issue with the labels, the steps are
// the loop's own, and each file of the run is listed once.
func TestTheLoopLandsWhatTheBrainSubmitted(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--label", "bug", "--label", "bug",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	got := resultOf(t, env)
	if got["action"] != "replaced_body" || !slices.Equal(anyStrings(got["labels"]), []string{"bug"}) {
		t.Errorf("result = %v, want the body replaced and the label bug once", got)
	}
	issue := e.forge.Issue(7)
	if !strings.Contains(issue.Body, "Run calls Help before it checks its arguments.") || !slices.Contains(issue.Labels, "bug") {
		t.Errorf("the issue = %+v", issue)
	}
	dir := e.runDir(env)
	if steps := stepNames(t, dir); !slices.Equal(steps, []string{"preflight", "brief", "brain", "land"}) {
		t.Errorf("steps = %v, want preflight, brief, brain, land", steps)
	}
	for _, kind := range []string{"brief", "submission"} {
		if n := len(artifactsOf(env, kind)); n != 1 {
			t.Errorf("%d %s artifacts, want 1", n, kind)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "rejections.jsonl")); err == nil {
		t.Error("a run that was refused nothing has a rejections.jsonl")
	}
	var file struct{ Payload json.RawMessage }
	if err := json.Unmarshal(readFile(t, filepath.Join(dir, "submission.json")), &file); err != nil {
		t.Fatal(err)
	}
	entry := ledgerData(t, dir, "triage.submission")
	if entry["submission"] != filepath.Join(dir, "submission.json") || entry["sha256"] != submission.PayloadSHA256(file.Payload) {
		t.Errorf("triage.submission = %v, want the path of submission.json and the hash of its payload", entry)
	}
}

func anyStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, s := range list {
		out = append(out, s.(string))
	}
	return out
}

// TestTheLoopOnATextReportCreatesTheIssueInTheRepoItWasGiven guards 08-REQ-2.5
// and 2.1 through the loop: the target of the shell and its forge client are
// what the landing goes to.
func TestTheLoopOnATextReportCreatesTheIssueInTheRepoItWasGiven(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.loop("the app crashes on submit", "--repo", "o/r", "--dir", e.dir, "--brain", "scripted",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 || resultOf(t, env)["action"] != "created_issue" {
		t.Fatalf("exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	if got := e.forge.Count(fakeforge.OpCreate); got != 1 {
		t.Errorf("%d create requests, want 1", got)
	}

	t.Run("without a repository there is nowhere to land", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		code, env := e.loop("the app crashes on submit", "--dir", e.dir, "--brain", "scripted", "--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))
		errObj, _ := env["error"].(map[string]any)
		if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "--land none") {
			t.Errorf("exit code %d, error %v, want usage that names --land none", code, errObj)
		}
	})
}

// TestADryRunLoopNeedsNoCredentialAndWritesNothing guards 08-REQ-2.6 and 4.1:
// the preflight of a dry run does not ask for a credential, and the landing is
// planned and not sent.
func TestADryRunLoopNeedsNoCredentialAndWritesNothing(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	e.app.NewForge = e.forge.NewForge("")

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--dry-run", "--brain", "scripted",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 || resultOf(t, env)["action"] != "planned" {
		t.Fatalf("exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestTheHostChecksTheLinesOfTheFilesAgain guards 08-REQ-4.5: a file the host
// shortened while the brain ran is still there, so the view of files holds, and
// the line the diagnosis cites is gone; the submission is treated as absent.
func TestTheHostChecksTheLinesOfTheFilesAgain(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	spy := &spyBrain{}
	submit := submitting(diagnosisPayload(nil))
	spy.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		out, err := submit(ctx, b)
		if werr := os.WriteFile(filepath.Join(e.dir, "main.go"), []byte("package main\n"), 0o644); werr != nil {
			t.Error(werr)
		}
		return out, err
	}
	useSpy(t, spy)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy")

	errObj, _ := env["error"].(map[string]any)
	if code != 1 || errObj["category"] != "no_result" || env["result"] != nil || !strings.Contains(errObj["message"].(string), "/affected_files/0") {
		t.Fatalf("exit code %d, error %v, want failed/no_result that names the field", code, errObj)
	}
	if _, err := os.Stat(filepath.Join(e.runDir(env), "submission.json")); err == nil {
		t.Error("the stale submission is in the run directory")
	}
	if n := len(artifactsOf(env, "submission")); n != 0 {
		t.Errorf("%d submission artifacts, want none: the file was taken away", n)
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
}

// TestRefusedCallsAreBroughtOutOfTheBoxWhateverTheRunCameTo guards 08-REQ-4.5
// for a run that ends without a submission, and the warning for a file that
// cannot be brought out.
func TestRefusedCallsAreBroughtOutOfTheBoxWhateverTheRunCameTo(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	missing := diagnosisPayload(func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "nowhere.go", "note": "x"}}
	})

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "scripted", "--land", "none",
		"--script", script(t, submitStep(missing, "refused")))

	if code != 1 {
		t.Fatalf("exit code %d, error %v, want a run with no submission", code, env["error"])
	}
	if got := string(readFile(t, filepath.Join(e.runDir(env), "rejections.jsonl"))); !strings.Contains(got, `"unresolved_paths":["nowhere.go"]`) {
		t.Errorf("rejections.jsonl:\n%s", got)
	}

	t.Run("a file that cannot be read", func(t *testing.T) {
		e := newTriageEnv(t, fakeforge.GitHub)
		spy := &spyBrain{}
		submit := submitting(diagnosisPayload(nil))
		spy.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
			// A directory where the file of refusals should be.
			if err := b.Box.WriteFile(ctx, triage.RejectionsPath(b.RunID)+"/x", []byte("x")); err != nil {
				t.Error(err)
			}
			return submit(ctx, b)
		}
		useSpy(t, spy)

		code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--land", "none")

		if code != 0 || !slices.Contains(warningCodes(env), triage.WarnRejectionsUnreadable) {
			t.Errorf("exit code %d, error %v, warnings %v, want a run that went on and the warning %s", code, env["error"], warningCodes(env), triage.WarnRejectionsUnreadable)
		}
	})
}

// TestTheLoopOpensTheBoxOfTheSandboxItWasGiven guards 08-REQ-4.1: --sandbox names
// the provider of the box the brain works in.
func TestTheLoopOpensTheBoxOfTheSandboxItWasGiven(t *testing.T) {
	twin, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	twin.Name = "twin"
	box.Default.Register(twin)
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--sandbox", "twin", "--land", "none", "--brain", "scripted",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 {
		t.Fatalf("exit code %d, error %v", code, env["error"])
	}
	if got := ledgerData(t, e.runDir(env), "box_open")["provider"]; got != "twin" {
		t.Errorf("the box was opened with the %v provider, want twin", got)
	}
}

// TestLandNoneKeepsTheDiagnosisInTheRunDirectory guards 08-REQ-4.1 and 2.2 for the
// loop: nothing is sent to the forge, and diagnosis.md is the result.
func TestLandNoneKeepsTheDiagnosisInTheRunDirectory(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.loop(e.forge.IssueURL(7), "--dir", e.dir, "--land", "none", "--brain", "scripted",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 || resultOf(t, env)["action"] != "none" {
		t.Fatalf("exit code %d, error %v, result %v", code, env["error"], env["result"])
	}
	if n := len(e.forge.Writes()); n != 0 {
		t.Errorf("%d writes to the forge", n)
	}
	if _, err := os.Stat(filepath.Join(e.runDir(env), "diagnosis.md")); err != nil {
		t.Errorf("diagnosis.md: %v", err)
	}
}

// TestAClosedIssueRaisesAWarningAndTheRunGoesOn guards 08-REQ-2.4 through the loop.
func TestAClosedIssueRaisesAWarningAndTheRunGoesOn(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)
	e.forge.AddIssue(fakeforge.Issue{Number: 9, Title: "Old", Body: "It broke.", State: "closed"})

	code, env := e.loop(e.forge.IssueURL(9), "--dir", e.dir, "--land", "none", "--brain", "scripted",
		"--script", script(t, submitStep(diagnosisPayload(nil), "accepted")))

	if code != 0 || !slices.Contains(warningCodes(env), triage.WarnIssueClosed) {
		t.Errorf("exit code %d, error %v, warnings %v, want a run that went on and the warning %s", code, env["error"], warningCodes(env), triage.WarnIssueClosed)
	}
}

// TestAReportTheShellCutCannotLandWhole guards 08-REQ-2.8 through the loop: the
// preflight is told that the shell cut the input.
func TestAReportTheShellCutCannotLandWhole(t *testing.T) {
	e := newTriageEnv(t, fakeforge.GitHub)

	code, env := e.run(strings.Repeat("a crash\n", 20000), "triage", "-", "--repo", "o/r", "--dir", e.dir, "--sandbox", "local")

	errObj, _ := env["error"].(map[string]any)
	if code != 2 || errObj["category"] != "usage" || !strings.Contains(errObj["message"].(string), "after it was cut") {
		t.Errorf("exit code %d, error %v, want usage that says the report was cut", code, errObj)
	}
}
