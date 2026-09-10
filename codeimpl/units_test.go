package codeimpl

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestLocateResolvesEveryReferenceShape(t *testing.T) {
	root := t.TempDir()
	specs := filepath.Join(root, ".specs")
	mk := func(name, id, specName string) string {
		dir := filepath.Join(specs, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		prd := "---\nspec_id: \"" + id + "\"\nspec_name: \"" + specName + "\"\ntitle: \"T\"\nstatus: \"active\"\n" +
			"created_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\nintent_hash: null\nschema_version: 2\n---\n# T\n\n## Intent\n\nx\n"
		if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(prd), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	a := mk("09_agent_mode", "09", "agent_mode")
	mk("10_agent_mode", "10", "agent_mode")

	text := func(s string) toolio.Input { return toolio.Input{Kind: toolio.KindText, Origin: "argument", Body: s} }
	for _, ref := range []string{"09", "09_agent_mode", a, filepath.Join(".specs", "09_agent_mode")} {
		got, err := Locate(text(ref), root, specs)
		if err != nil || got != a {
			t.Errorf("Locate(%q) = %q, %v; want %q", ref, got, err, a)
		}
	}
	if got, err := Locate(toolio.Input{Kind: toolio.KindFile, Origin: filepath.Join(a, "tasks.json")}, root, specs); err != nil || got != a {
		t.Errorf("a file inside the package: %q, %v", got, err)
	}
	if _, err := Locate(text("agent_mode"), root, specs); err == nil || !strings.Contains(err.Error(), "2 spec packages") {
		t.Errorf("an ambiguous name must be refused naming both: %v", err)
	}
	if _, err := Locate(text("11"), root, specs); err == nil || !strings.Contains(err.Error(), "09_agent_mode") {
		t.Errorf("an unknown id must list what is there: %v", err)
	}
	if _, err := Locate(toolio.Input{Kind: toolio.KindIssue, Origin: "https://github.com/a/b/issues/1"}, root, specs); err == nil {
		t.Error("an issue URL must be refused")
	}
	if _, err := Locate(text("a\nb"), root, specs); err == nil {
		t.Error("a multi-line input must be refused")
	}
}

func TestResolveSpecsDir(t *testing.T) {
	t.Setenv(SpecDirEnv, "")
	if got := ResolveSpecsDir("", "/r"); got != filepath.Join("/r", ".specs") {
		t.Errorf("default = %q", got)
	}
	if got := ResolveSpecsDir("specs", "/r"); got != filepath.Join("/r", "specs") {
		t.Errorf("relative flag = %q", got)
	}
	t.Setenv(SpecDirEnv, "/elsewhere")
	if got := ResolveSpecsDir("", "/r"); got != "/elsewhere" {
		t.Errorf("env = %q", got)
	}
	if got := ResolveSpecsDir("/flag", "/r"); got != "/flag" {
		t.Errorf("the flag must win over the environment: %q", got)
	}
}

func TestGateCommandsAndShape(t *testing.T) {
	tc := afspec.TestCommands{AllTests: "make test", Linter: "make lint"}
	if got := gateCommands(tc, "", false); strings.Join(got, ",") != "make lint,make test" {
		t.Errorf("gate = %v; the linter runs first because it is cheap", got)
	}
	if got := gateCommands(tc, "go test ./...", false); len(got) != 1 || got[0] != "go test ./..." {
		t.Errorf("--verify must replace the pair: %v", got)
	}
	if got := gateCommands(tc, "x", true); got != nil {
		t.Errorf("--no-verify must run nothing: %v", got)
	}
	for _, bad := range []string{"make test && make lint", "go test ./... | tail", "go test -run 'X' ./...", "FOO=1 make test"} {
		if err := checkCommandShape("all_tests", bad); err == nil {
			t.Errorf("%q needs a shell and was accepted", bad)
		}
	}
	if err := checkCommandShape("all_tests", "go test ./... -count=1"); err != nil {
		t.Errorf("a plain command was refused: %v", err)
	}
}

func TestCompareGateTakesTheWorst(t *testing.T) {
	ok := checks.Result{Command: "c", OK: true}
	red := checks.Result{Command: "c", ExitCode: 1}
	cases := []struct {
		name   string
		before []checks.Result
		after  []checks.Result
		want   string
	}{
		{"all pass", []checks.Result{ok, ok}, []checks.Result{ok, ok}, "pass"},
		{"one regressed", []checks.Result{ok, ok}, []checks.Result{ok, red}, "regressed"},
		{"repaired and still failing", []checks.Result{red, red}, []checks.Result{ok, red}, "still_failing"},
		{"repaired only", []checks.Result{red, ok}, []checks.Result{ok, ok}, "pass_was_already_failing"},
		{"could not run", []checks.Result{ok}, []checks.Result{{Command: "c", ExitCode: -1}}, VerdictGateFailed},
		{"timed out beats regressed", []checks.Result{ok, ok}, []checks.Result{red, {Command: "c", ExitCode: 1, TimedOut: true}}, VerdictGateFailed},
		{"nothing ran", nil, nil, "unverified"},
	}
	for _, c := range cases {
		got := compareGate(GateResult{Checks: c.before}, GateResult{Checks: c.after})
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if landable("unverified", false) || !landable("unverified", true) {
		t.Error("unverified lands only when nothing was asked to run")
	}
	if landable(VerdictGateFailed, true) || landable("regressed", true) {
		t.Error("a failed gate never lands")
	}
}

func TestSubmitTaskToolEnforcesTheVerdicts(t *testing.T) {
	task := afspec.Task{Id: 2, Kind: afspec.TaskKindImplement, Tests: []string{"TS-09-4", "TS-09-5"},
		DoneWhen: []string{"the thing holds"}}
	var out sink[Submission]
	tool := submitTaskTool(&out, task)
	call := func(v any) (bool, string) {
		raw, _ := json.Marshal(v)
		res := tool.Execute(context.Background(), raw)
		return res.OK, res.Detail
	}
	good := "cmd/spec/agent_test.go TestTS094 passes under make test"
	base := map[string]any{
		"summary": "did it", "commit_subject": "route output", "changes": []map[string]string{{"path": "a.go", "change": "x"}},
	}
	with := func(tests, done []map[string]string) map[string]any {
		m := map[string]any{}
		for k, v := range base {
			m[k] = v
		}
		m["test_verdicts"] = tests
		m["done_when_verdicts"] = done
		return m
	}
	dw := []map[string]string{{"id": "DW-1", "verdict": "pass", "evidence": "ran it and it exited zero, twice"}}

	if ok, msg := call(with(nil, dw)); ok || !strings.Contains(msg, "missing TS-09-4, TS-09-5") {
		t.Errorf("a submission without verdicts was accepted: %v %q", ok, msg)
	}
	if ok, msg := call(with([]map[string]string{
		{"id": "TS-09-4", "verdict": "pass", "evidence": good}, {"id": "TS-09-9", "verdict": "pass", "evidence": good}}, dw)); ok ||
		!strings.Contains(msg, "TS-09-9") {
		t.Errorf("an unknown id was accepted: %v %q", ok, msg)
	}
	if ok, msg := call(with([]map[string]string{
		{"id": "TS-09-4", "verdict": "pass", "evidence": good}, {"id": "TS-09-5", "verdict": "partial", "evidence": good}}, dw)); ok ||
		!strings.Contains(msg, "must be one of pass, fail") {
		t.Errorf("a third verdict was accepted: %v %q", ok, msg)
	}
	if ok, msg := call(with([]map[string]string{
		{"id": "TS-09-4", "verdict": "pass", "evidence": good}, {"id": "TS-09-5", "verdict": "pass", "evidence": "yes"}}, dw)); ok ||
		!strings.Contains(msg, "says only") {
		t.Errorf("a one-word evidence was accepted: %v %q", ok, msg)
	}
	if ok, msg := call(with([]map[string]string{
		{"id": "TS-09-4", "verdict": "pass", "evidence": good}, {"id": "TS-09-5", "verdict": "pass", "evidence": good}}, nil)); ok ||
		!strings.Contains(msg, "missing DW-1") {
		t.Errorf("a missing done_when verdict was accepted: %v %q", ok, msg)
	}
	ok, msg := call(with([]map[string]string{
		{"id": "ts-09-5", "verdict": "FAIL", "evidence": "TestTS095 fails: the exit code is still 0 on a closed pipe"},
		{"id": "TS-09-4", "verdict": "pass", "evidence": good}}, dw))
	if !ok {
		t.Fatalf("a complete, honest submission was refused: %q", msg)
	}
	got, done := out.get()
	if !done || len(got.TestVerdicts) != 2 || got.TestVerdicts[0].ID != "TS-09-4" || got.TestVerdicts[1].Verdict != "fail" {
		t.Errorf("normalized verdicts = %+v", got.TestVerdicts)
	}
	if verdictsOutcome(task.Tests, got.TestVerdicts) != VerdictFail {
		t.Error("an honest fail must make the outcome fail")
	}

	// A blocker needs no verdicts: the phase is stopping, not reporting work.
	var blocked sink[Submission]
	if res := submitTaskTool(&blocked, task).Execute(context.Background(), json.RawMessage(
		`{"blocker":{"reason":"the spec assumes cobra; there is no CLI","needed":"decide"}}`)); !res.OK {
		t.Errorf("a blocker was refused: %s", res.Detail)
	}
	if res := submitTaskTool(&blocked, task).Execute(context.Background(), json.RawMessage(
		`{"blocker":{"reason":"","needed":"decide"}}`)); res.OK {
		t.Error("an empty blocker was accepted")
	}
}

func TestSubmitSurveyToolRequiresASummary(t *testing.T) {
	var out sink[Survey]
	tool := submitSurveyTool(&out)
	if res := tool.Execute(context.Background(), json.RawMessage(`{"summary":""}`)); res.OK {
		t.Error("an empty summary was accepted")
	}
	if res := tool.Execute(context.Background(), json.RawMessage(
		`{"summary":"fine","drift":[{"spec_ref":"09-REQ-1.1","finding":"no cmd/spec","resolution":"create it"}]}`)); !res.OK {
		t.Errorf("refused: %s", res.Detail)
	}
	if got, ok := out.get(); !ok || len(got.Drift) != 1 {
		t.Errorf("survey = %+v", got)
	}
}

func TestParkedTaskIsRecognizedByItsTrailer(t *testing.T) {
	spec := &afspec.Spec{Dir: "/r/.specs/09_agent_mode", SpecID: "09", Title: "T"}
	task := afspec.Task{Id: 2}
	msg := wipCommitMessage(spec, task, "the checks did not pass (regressed)")
	if n, ok := parkedTask(msg); !ok || n != 2 {
		t.Errorf("parkedTask = %d, %v from:\n%s", n, ok, msg)
	}
	if _, ok := parkedTask("wip: something a person wrote\n"); ok {
		t.Error("a person's wip commit was mistaken for a parked attempt")
	}
	if _, ok := parkedTask(commitMessage(spec, task, Submission{CommitSubject: "x.", Summary: "s"})); ok {
		t.Error("a landed commit was mistaken for a parked attempt")
	}
	if got := commitMessage(spec, task, Submission{CommitSubject: "route output to stderr.", Summary: "Done."}); got !=
		"feat: route output to stderr\n\nDone.\n\nSpec: 09_agent_mode, task 2\n" {
		t.Errorf("commit message =\n%s", got)
	}
}

func TestDependenciesDefaultToThePreviousTask(t *testing.T) {
	tasks := []afspec.Task{{Id: 1}, {Id: 2}, {Id: 3, DependsOn: []int{1}}}
	if d := dependenciesOf(tasks, 0); d != nil {
		t.Errorf("task 1 depends on %v", d)
	}
	if d := dependenciesOf(tasks, 1); len(d) != 1 || d[0] != 1 {
		t.Errorf("task 2 depends on %v, want the previous task", d)
	}
	if d := dependenciesOf(tasks, 2); len(d) != 1 || d[0] != 1 {
		t.Errorf("task 3 depends on %v, want its own list", d)
	}
	spec := &afspec.Spec{Tasks: &afspec.TasksV2Json{Tasks: []afspec.Task{
		{Id: 1, State: afspec.TaskStateDropped}, {Id: 2, State: afspec.TaskStatePending}}}}
	if dep, state, blocked := notReady(spec, 1); !blocked || dep != 1 || state != afspec.TaskStateDropped {
		t.Errorf("a dropped dependency must stop the dependent: %d %s %v", dep, state, blocked)
	}
}

func TestPullRequestBodyIsRenderedFromFacts(t *testing.T) {
	r := &Result{
		SpecDir: ".specs/09_agent_mode", Title: "Agent mode", TasksTotal: 3, TasksDone: 2, TasksSkipped: 1,
		Gate:     []string{"make lint", "make test"},
		Baseline: GateResult{Checks: []checks.Result{{Command: "make lint", OK: true}, {Command: "make test", ExitCode: 2}}},
		Verification: GateResult{Checks: []checks.Result{{Command: "make lint", OK: true, DurationMS: 12},
			{Command: "make test", OK: true, DurationMS: 2300}}},
		Tasks: []TaskReport{
			{ID: 1, Title: "a", Outcome: OutcomeSkipped},
			{ID: 2, Title: "b", Outcome: OutcomeDone, Commit: "abc1234", Verdict: "pass", TestsOutcome: "pass",
				Submission: &Submission{Summary: "did b", TestVerdicts: []Verdict{{ID: "TS-09-4", Verdict: "pass", Evidence: "ok in TestFoo"}}}},
			{ID: 3, Title: "c", Outcome: OutcomeDone, Commit: "def5678", Verdict: "pass_was_already_failing", TestsOutcome: "pass",
				Submission: &Submission{Summary: "did c", TestVerdicts: []Verdict{{ID: "TS-09-6", Verdict: "fail", Evidence: "flaky"}}}},
		},
		Survey: &Survey{Drift: []Drift{{SpecRef: "09-REQ-1", Finding: "no cobra", Resolution: "use flag"}}},
	}
	body := pullRequestBody(r)
	for _, want := range []string{
		"2 of 3 task(s) landed", "1 already done", "| 2 | b | done | `abc1234` | pass | pass |",
		"**09-REQ-1:** no cobra → use flag", "✅ **TS-09-4**: PASS", "❌ **TS-09-6**: FAIL",
		"✅ `make lint` passes (exit 0, 12ms)", "✅ `make test` passes (exit 0, 2.3s). It was **already failing before this branch** (exit 2)",
		footer,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if !strings.Contains(verificationLines(nil, GateResult{}, GateResult{}), "Unverified") {
		t.Error("no gate must render as unverified")
	}
}

func TestSteeringIgnoresThePlaceholder(t *testing.T) {
	dir := t.TempDir()
	if s := steering(dir); s != "" {
		t.Errorf("missing file = %q", s)
	}
	write(t, dir, "steering.md", "# Steering\n\n<!-- steering:placeholder -->\n")
	if s := steering(dir); s != "" {
		t.Errorf("placeholder = %q", s)
	}
	write(t, dir, "steering.md", "# Steering\n")
	if s := steering(dir); s != "" {
		t.Errorf("an empty heading = %q", s)
	}
	write(t, dir, "steering.md", "# Steering\n\nAlways run make check.\n")
	if s := steering(dir); !strings.Contains(s, "make check") {
		t.Errorf("real directives = %q", s)
	}
}

func TestParkedRepairIsRecognizedByItsTrailer(t *testing.T) {
	spec := &afspec.Spec{Dir: "/r/.specs/09_agent_mode", SpecID: "09", Title: "T"}
	msg := wipRepairMessage(spec, "the checks still do not pass (still_failing)")
	if !parkedRepair(msg) {
		t.Errorf("parkedRepair = false from:\n%s", msg)
	}
	if _, ok := parkedTask(msg); ok {
		t.Error("a parked repair was mistaken for a parked task")
	}
	if parkedRepair(wipCommitMessage(spec, afspec.Task{Id: 2}, "x")) {
		t.Error("a parked task was mistaken for a parked repair")
	}
	got := repairCommitMessage(spec, RepairSubmission{
		CommitSubject: "update the fixture.", Cause: "The fixture was stale.", Summary: "Regenerated it."})
	if parkedRepair(got) {
		t.Error("a landed repair was mistaken for a parked one")
	}
	if want := "fix: update the fixture\n\nThe fixture was stale.\n\nRegenerated it.\n\nSpec: 09_agent_mode, repair\n"; got != want {
		t.Errorf("repair commit message =\n%s\nwant\n%s", got, want)
	}
}

func TestSubmitRepairToolRefusesAnEmptyReport(t *testing.T) {
	var out sink[RepairSubmission]
	tool := submitRepairTool(&out)
	ctx := context.Background()
	for name, in := range map[string]string{
		"no cause":      `{"summary":"s","commit_subject":"x","changes":[{"path":"a","change":"b"}]}`,
		"no subject":    `{"cause":"c","summary":"s","changes":[{"path":"a","change":"b"}]}`,
		"no changes":    `{"cause":"c","summary":"s","commit_subject":"x","changes":[]}`,
		"empty blocker": `{"blocker":{"reason":"  ","needed":"x"}}`,
	} {
		if res := tool.Execute(ctx, json.RawMessage(in)); res.OK {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, ok := out.get(); ok {
		t.Fatal("a refused report reached the sink")
	}
	res := tool.Execute(ctx, json.RawMessage(
		`{"cause":"c","summary":"s","commit_subject":"x","changes":[{"path":"a","change":"b"}]}`))
	if !res.OK || !res.Terminate {
		t.Errorf("a complete report was refused: %s", res.Detail)
	}
	if got, ok := out.get(); !ok || got.Cause != "c" {
		t.Errorf("submission = %+v", got)
	}
	var blocked sink[RepairSubmission]
	res = submitRepairTool(&blocked).Execute(ctx, json.RawMessage(`{"blocker":{"reason":"needs Postgres","needed":"start one"}}`))
	if !res.OK || !res.Terminate {
		t.Errorf("a blocker was refused: %s", res.Detail)
	}
}
