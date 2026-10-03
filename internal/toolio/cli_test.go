package toolio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
	"github.com/agentfox/agentkit-go/core"
)

type questionResult struct{}

func (q questionResult) NeedsHuman() (question string, options []toolio.Option, needed string, ok bool) {
	return "Which retry loop?", []toolio.Option{
		{ID: "A", Text: "the HTTP client's retry loop in client.go"},
		{ID: "B", Text: "the job queue's redelivery in worker.go"},
	}, "", true
}

// TS-05-13 (unit): needs_human carries question, options, needed, stage and a non-literal resume template when status is needs_human
func TestTS05_13_NeedsHumanCarriesFields(t *testing.T) {
	run := toolio.NewRun("fix", "v1")
	largeInputBody := strings.Repeat("x", 50000)
	run.SetInput(toolio.Input{
		Kind:   toolio.KindText,
		Origin: "argument",
		Body:   largeInputBody,
	})

	errInfo := &toolio.ErrorInfo{Stage: "analyse", Category: "ambiguous", Message: "ambiguous report"}
	env := run.Envelope(toolio.ExitNeedsHuman, questionResult{}, errInfo)

	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Stage != "analyse" {
		t.Errorf("expected Stage == %q, got %q", "analyse", env.NeedsHuman.Stage)
	}
	if env.NeedsHuman.Question != "Which retry loop?" {
		t.Errorf("expected Question == %q, got %q", "Which retry loop?", env.NeedsHuman.Question)
	}
	if len(env.NeedsHuman.Options) != 2 || env.NeedsHuman.Needed != "" {
		t.Errorf("expected 2 options and empty needed, got %d options and needed=%q", len(env.NeedsHuman.Options), env.NeedsHuman.Needed)
	}
	if strings.Contains(env.NeedsHuman.Resume, largeInputBody) {
		t.Errorf("resume template should not contain the large input body: %q", env.NeedsHuman.Resume)
	}
	expectedResume := `fix <same input> --context "<answer>"`
	if env.NeedsHuman.Resume != expectedResume {
		t.Errorf("expected resume template %q, got %q", expectedResume, env.NeedsHuman.Resume)
	}
}

// TS-05-14 (unit): needs_human is omitted entirely when status is not needs_human
func TestTS05_14_NeedsHumanOmittedWhenNotNeedsHuman(t *testing.T) {
	run := toolio.NewRun("fix", "v1")
	errInfoFor := func(code int) *toolio.ErrorInfo {
		if code == toolio.ExitOK {
			return nil
		}
		return &toolio.ErrorInfo{Stage: "step", Category: "cat", Message: "msg"}
	}

	for _, code := range []int{toolio.ExitOK, toolio.ExitFailed, toolio.ExitUsage, toolio.ExitUnverified} {
		env := run.Envelope(code, questionResult{}, errInfoFor(code))
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if bytes.Contains(b, []byte(`"needs_human"`)) {
			t.Errorf("code %d: unexpected needs_human in marshalled json: %s", code, string(b))
		}
		if env.NeedsHuman != nil {
			t.Errorf("code %d: env.NeedsHuman should be nil", code)
		}
	}
}

// TS-05-15 (integration): codefix's Ambiguity maps onto needs_human's question and options A/B
func TestTS05_15_CodefixAmbiguityMapsOntoNeedsHuman(t *testing.T) {
	run := toolio.NewRun("fix", "v1")
	result := &codefix.Result{
		Ambiguity: &codefix.Ambiguity{
			Question:        "Which retry loop?",
			InterpretationA: "the HTTP client's retry loop",
			InterpretationB: "the job queue's redelivery",
		},
	}
	env := run.Envelope(toolio.ExitNeedsHuman, result, &toolio.ErrorInfo{Stage: "analyse", Category: "ambiguous"})

	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Question != "Which retry loop?" {
		t.Errorf("expected Question %q, got %q", "Which retry loop?", env.NeedsHuman.Question)
	}
	if len(env.NeedsHuman.Options) != 2 {
		t.Fatalf("expected 2 options, got %d", len(env.NeedsHuman.Options))
	}
	wantOptA := toolio.Option{ID: "A", Text: "the HTTP client's retry loop"}
	wantOptB := toolio.Option{ID: "B", Text: "the job queue's redelivery"}
	if env.NeedsHuman.Options[0] != wantOptA {
		t.Errorf("option A: got %+v, want %+v", env.NeedsHuman.Options[0], wantOptA)
	}
	if env.NeedsHuman.Options[1] != wantOptB {
		t.Errorf("option B: got %+v, want %+v", env.NeedsHuman.Options[1], wantOptB)
	}
	if env.NeedsHuman.Needed != "" {
		t.Errorf("expected Needed to be empty, got %q", env.NeedsHuman.Needed)
	}
}

// TS-05-16 (integration): codeimpl's Blocker maps onto needs_human's question and needed, with no options
func TestTS05_16_CodeimplBlockerMapsOntoNeedsHuman(t *testing.T) {
	run := toolio.NewRun("impl", "v1")
	result := &codeimpl.Result{
		Blocker: &codeimpl.Blocker{
			Reason: "the spec names a module the PRD does not create",
			Needed: "a decision on module X",
		},
	}
	env := run.Envelope(toolio.ExitNeedsHuman, result, &toolio.ErrorInfo{Stage: "survey", Category: codeimpl.CategoryBlocked})

	if env.NeedsHuman == nil {
		t.Fatal("expected env.NeedsHuman to be non-nil")
	}
	if env.NeedsHuman.Question != result.Blocker.Reason {
		t.Errorf("expected Question %q, got %q", result.Blocker.Reason, env.NeedsHuman.Question)
	}
	if env.NeedsHuman.Needed != result.Blocker.Needed {
		t.Errorf("expected Needed %q, got %q", result.Blocker.Needed, env.NeedsHuman.Needed)
	}
	if len(env.NeedsHuman.Options) != 0 {
		t.Errorf("expected len(Options) == 0, got %d", len(env.NeedsHuman.Options))
	}
}

// TS-05-18 (unit): specgen never sets needs_human even when open_questions is non-empty; the summary reports only the count
func TestTS05_18_SpecgenNeverSetsNeedsHuman(t *testing.T) {
	run := toolio.NewRun("spec", "v1")
	threeQuestions := []specgen.OpenQuestion{
		{Question: "Q1?"},
		{Question: "Q2?"},
		{Question: "Q3?"},
	}
	result := &specgen.Result{
		Package: specgen.Package{
			SpecID:        "01",
			OpenQuestions: threeQuestions,
		},
	}
	env := run.Envelope(toolio.ExitOK, result, nil)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if bytes.Contains(b, []byte(`"needs_human"`)) {
		t.Errorf("unexpected needs_human in json: %s", string(b))
	}
	if len(result.OpenQuestions) != 3 {
		t.Errorf("expected 3 open questions, got %d", len(result.OpenQuestions))
	}
	if !strings.Contains(env.Summary, "3 open questions") {
		t.Errorf("expected summary to contain '3 open questions', got %q", env.Summary)
	}
}

// TS-05-19 (unit): Repeated --context flags render one labelled block appended to the phase prompt, independently of Body, and are reported as input.context_bytes
func TestTS05_19_ContextFlagsRenderBlock(t *testing.T) {
	var common toolio.Common
	common.Context = []string{"first note", "second note"}
	block := common.ContextBlock()
	if !strings.HasPrefix(block, "## Additional context from the caller") {
		t.Errorf("expected prefix '## Additional context from the caller', got: %q", block)
	}
	if !strings.Contains(block, "first note") || !strings.Contains(block, "second note") {
		t.Errorf("expected block to contain both notes, got: %q", block)
	}

	run := toolio.NewRun("fix", "v1")
	originalBody := "the original report"
	in := toolio.Input{
		Kind:    toolio.KindText,
		Origin:  "argument",
		Body:    originalBody,
		Context: block,
	}
	run.SetInput(in)
	env := run.Envelope(toolio.ExitOK, nil, nil)
	if env.Input == nil {
		t.Fatal("expected env.Input to be non-nil")
	}
	if env.Input.ContextBytes != len(block) {
		t.Errorf("expected env.Input.ContextBytes == %d, got %d", len(block), env.Input.ContextBytes)
	}
}

// TS-05-20 (unit): An input plus context block exceeding MaxInputBytes is refused as a usage error before anything is fetched
func TestTS05_20_ContextExceedingMaxInputBytesRefused(t *testing.T) {
	body := strings.Repeat("a", 200000)
	ctxVal := strings.Repeat("b", 70000)

	var execCalled bool
	app := toolio.App{
		Name:    "fix",
		Version: "v1",
		Exec: func(ctx context.Context, d toolio.Deps) (int, any, *toolio.ErrorInfo) {
			execCalled = true
			return toolio.ExitOK, nil, nil
		},
	}

	var stdout, stderr bytes.Buffer
	code := app.Main(context.Background(), []string{"--context", ctxVal, body}, strings.NewReader(""), &stdout, &stderr)

	if code != toolio.ExitUsage {
		t.Fatalf("expected code %d (ExitUsage), got %d", toolio.ExitUsage, code)
	}
	if execCalled {
		t.Error("Exec should not have been called when input+context exceeded MaxInputBytes")
	}

	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal stdout failed: %v\nstdout: %s", err, stdout.String())
	}
	if env.Error == nil || env.Error.Category != "usage" {
		t.Errorf("expected error.category == 'usage', got %+v", env.Error)
	}
}

// TS-06-1 (unit): --detail is registered on Common, defaulting to summary and accepting full
func TestTS06_1_DetailRegisteredDefaultSummary(t *testing.T) {
	var c toolio.Common
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	if err := fs.Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Detail != "summary" {
		t.Errorf("expected default Detail == %q, got %q", "summary", c.Detail)
	}

	var c2 toolio.Common
	fs2 := flag.NewFlagSet("t2", flag.ContinueOnError)
	c2.Register(fs2)
	if err := fs2.Parse([]string{"--detail", "full"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c2.Detail != "full" {
		t.Errorf("expected Detail == %q, got %q", "full", c2.Detail)
	}
}

// TS-06-1 (unit, continued): Common.ValidDetail refuses anything other than summary/full
func TestTS06_1_ValidDetailRefusesUnknownValues(t *testing.T) {
	for _, v := range []string{"summary", "full", ""} {
		c := toolio.Common{Detail: v}
		if err := c.ValidDetail(); err != nil {
			t.Errorf("Detail=%q: expected no error, got %v", v, err)
		}
	}
	c := toolio.Common{Detail: "wrong"}
	if err := c.ValidDetail(); err == nil {
		t.Error("expected an error for Detail=\"wrong\"")
	}
}

// jsonKeysOf walks t's fields (recursing into anonymous embedded structs,
// the way specgen.Result embeds Package) and returns every json tag name it
// finds, skipping "-". It is a static view of what a type CAN emit, not what
// one populated value happens to: an omitempty field with a zero value is
// still counted, which is what makes it usable as a superset check
// regardless of how a fixture value is populated.
func jsonKeysOf(t reflect.Type) []string {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" && !f.Anonymous {
			continue // unexported
		}
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		_ = opts
		if name == "-" {
			continue
		}
		if name == "" && f.Anonymous {
			out = append(out, jsonKeysOf(f.Type)...)
			continue
		}
		if name == "" {
			name = f.Name
		}
		out = append(out, name)
	}
	return out
}

// TS-06-3 (property): The --detail full view's keys are always a superset of
// what full carried before this spec, for every tool.
//
// preKeys is a fixture snapshot of every JSON key each Result type carried
// before this task added "detail": read directly off issuetriage/pipeline.go,
// codefix/types.go, codeimpl/types.go and specgen/pipeline.go as they stood
// going into this task.
func TestTS06_3_FullViewKeysSupersetOfPreSpecKeys(t *testing.T) {
	cases := []struct {
		tool    string
		typ     reflect.Type
		preKeys []string
	}{
		{
			tool: "issue",
			typ:  reflect.TypeOf(issuetriage.Result{}),
			preKeys: []string{
				"action", "repo", "url", "number", "upstream_url", "title", "body",
				"severity", "severity_rationale", "confidence", "problem", "reproduction",
				"root_cause", "related_instances", "affected_files", "suggested_fix",
				"acceptance_criteria", "labels", "rejected_path_calls", "rejected_paths",
			},
		},
		{
			tool: "fix",
			typ:  reflect.TypeOf(codefix.Result{}),
			preKeys: []string{
				"stage", "repo", "issue_url", "issue_number", "classification", "title",
				"summary", "root_cause", "approach", "assumptions", "acceptance_criteria",
				"criteria_outcome", "branch", "base_branch", "commit", "pushed",
				"changed_files", "diff_stat", "baseline", "verification", "verdict",
				"pull_request_url", "pull_request_number", "comments", "implementation",
				"ambiguity", "dry_run",
			},
		},
		{
			tool: "impl",
			typ:  reflect.TypeOf(codeimpl.Result{}),
			preKeys: []string{
				"stage", "spec_dir", "spec_id", "spec_name", "title", "status", "repo",
				"branch", "base_branch", "resumed", "tasks_total", "tasks_done",
				"tasks_skipped", "tasks_remaining", "tasks", "gate", "baseline",
				"verification", "verdict", "pushed", "pull_request_url",
				"pull_request_number", "repair", "survey", "blocker", "cost_usd", "dry_run",
			},
		},
		{
			tool: "spec",
			typ:  reflect.TypeOf(specgen.Result{}),
			preKeys: []string{
				"spec_dir", "spec_id", "spec_name", "title", "status", "source",
				"artifacts", "requirements", "criteria", "execution_paths", "tests",
				"tasks", "validation", "traceability", "open_questions", "comment_url",
				"follow_on_specs", "split", "split_plan", "dry_run",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			postKeys := jsonKeysOf(c.typ)
			postSet := map[string]bool{}
			for _, k := range postKeys {
				postSet[k] = true
			}
			for _, k := range c.preKeys {
				if !postSet[k] {
					t.Errorf("%s: pre-06 key %q is missing from the post-06 full view", c.tool, k)
				}
			}
			if !postSet["detail"] {
				t.Errorf("%s: expected the new \"detail\" key to be present", c.tool)
			}
		})
	}
}

// TS-06-4 (unit): Every tool's Result records which detail view was emitted,
// under both summary and full.
func TestTS06_4_ResultRecordsDetailView(t *testing.T) {
	t.Run("issue", func(t *testing.T) {
		r := &issuetriage.Result{}
		assertDetailRoundTrips(t, r)
	})
	t.Run("fix", func(t *testing.T) {
		r := &codefix.Result{}
		assertDetailRoundTrips(t, r)
	})
	t.Run("impl", func(t *testing.T) {
		r := &codeimpl.Result{}
		assertDetailRoundTrips(t, r)
	})
	t.Run("spec", func(t *testing.T) {
		r := &specgen.Result{}
		assertDetailRoundTrips(t, r)
	})
}

// assertDetailRoundTrips exercises toolio.DetailedResult on r, then checks
// the emitted JSON carries the "detail" key with the value that was set, for
// both legal views.
func assertDetailRoundTrips(t *testing.T, r toolio.DetailedResult) {
	t.Helper()
	for _, view := range []string{"summary", "full"} {
		r.SetDetail(view)
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		got, ok := m["detail"]
		if !ok {
			t.Fatalf("view %q: \"detail\" key missing from %s", view, string(b))
		}
		if got != view {
			t.Errorf("view %q: result.detail == %v", view, got)
		}
	}
}

// TS-06-4 (unit, continued): toolio.FullView sets Detail to "full" via the
// DetailedResult interface and returns the value unchanged otherwise.
func TestTS06_4_FullViewSetsDetailFull(t *testing.T) {
	r := &codefix.Result{Stage: "landed"}
	out := toolio.FullView(r)
	got, ok := out.(*codefix.Result)
	if !ok {
		t.Fatalf("FullView changed the type: %T", out)
	}
	if got != r {
		t.Error("FullView should return the same value")
	}
	if got.Detail != "full" {
		t.Errorf("expected Detail == full, got %q", got.Detail)
	}
	if got.Stage != "landed" {
		t.Errorf("FullView should not touch other fields, got Stage=%q", got.Stage)
	}
}

// toolFlagSetNames are the four tools whose flag sets share Common.
var toolFlagSetNames = []string{"issue", "fix", "spec", "impl"}

// TS-06-52 (unit): --dry-run is registered exactly once, on Common, with the
// one make-no-remote-change definition.
func TestTS06_52_DryRunRegisteredOnceOnCommon(t *testing.T) {
	var usages []string
	for _, tool := range toolFlagSetNames {
		var c toolio.Common
		fs := flag.NewFlagSet(tool, flag.ContinueOnError)
		// Registering twice would panic ("flag redefined"); a single call
		// must therefore define the flag.
		c.Register(fs)
		f := fs.Lookup("dry-run")
		if f == nil {
			t.Fatalf("%s: Common.Register did not define --dry-run", tool)
		}
		if f.Usage != toolio.DryRunUsage {
			t.Errorf("%s: --dry-run usage = %q, want the one shared definition", tool, f.Usage)
		}
		usages = append(usages, f.Usage)
		if err := fs.Parse([]string{"--dry-run"}); err != nil {
			t.Fatal(err)
		}
		if !c.DryRun {
			t.Errorf("%s: --dry-run did not set Common.DryRun", tool)
		}
	}
	for _, u := range usages[1:] {
		if u != usages[0] {
			t.Errorf("help text differs across tools: %q vs %q", u, usages[0])
		}
	}
	if !strings.Contains(toolio.DryRunUsage, "no remote change") {
		t.Errorf("DryRunUsage = %q", toolio.DryRunUsage)
	}
}

// TS-06-54 (unit): --total-budget is registered once on Common, default zero.
func TestTS06_54_TotalBudgetDefaultsToZero(t *testing.T) {
	var c toolio.Common
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	c.Register(fs)
	if fs.Lookup("total-budget") == nil {
		t.Fatal("Common.Register did not define --total-budget")
	}
	if err := fs.Parse([]string{"--budget", "3"}); err != nil {
		t.Fatal(err)
	}
	if c.TotalBudgetUSD != 0 {
		t.Errorf("TotalBudgetUSD = %v, want 0", c.TotalBudgetUSD)
	}
	if err := c.ValidTotalBudget(); err != nil {
		t.Errorf("zero total budget refused: %v", err)
	}
	// With the per-phase budget set and no total, nothing lowers the ceiling.
	b := c.SinglePhaseBounds(agentrun.Bounds{MaxBudgetUSD: 2})
	if b.MaxBudgetUSD != 3 {
		t.Errorf("effective ceiling = %v, want the per-phase 3", b.MaxBudgetUSD)
	}

	neg := toolio.Common{TotalBudgetUSD: -1}
	if neg.ValidTotalBudget() == nil {
		t.Error("a negative --total-budget should be refused")
	}
}

// TS-06-55 (unit): a one-phase tool applies min(--budget, --total-budget).
func TestTS06_55_SinglePhaseCeilingIsTheLowerOfTheTwo(t *testing.T) {
	defaults := agentrun.Bounds{MaxTurns: 100, MaxBudgetUSD: 2}
	cases := []struct {
		budget, total, want float64
	}{
		{10, 4, 4},
		{4, 10, 4},
		{0, 1, 1}, // the tool default (2) is above the total
		{0, 9, 2}, // the total is above the tool default: the default stays
		{0, 0, 2}, // neither given
		{5, 0, 5}, // no total
	}
	for _, tc := range cases {
		c := toolio.Common{Budget: tc.budget, TotalBudgetUSD: tc.total}
		got := c.SinglePhaseBounds(defaults).MaxBudgetUSD
		if got != tc.want {
			t.Errorf("budget=%v total=%v: ceiling = %v, want %v", tc.budget, tc.total, got, tc.want)
		}
	}
	// Bounds (the multi-phase path) is untouched by --total-budget.
	c := toolio.Common{Budget: 10, TotalBudgetUSD: 4}
	if got := c.Bounds(defaults).MaxBudgetUSD; got != 10 {
		t.Errorf("Bounds ceiling = %v, want 10", got)
	}
}

// TS-13-1 (unit): --effort is registered as a string flag with DeclareEnum
// listing the seven accepted values.
func TestTS13_1_EffortRegisteredWithDeclareEnum(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &toolio.Common{}
	c.Register(fs)

	f := fs.Lookup("effort")
	if f == nil {
		t.Fatal("expected a flag named 'effort' to be registered")
	}
	if f.DefValue != "" {
		t.Errorf("expected default value to be empty, got %q", f.DefValue)
	}

	// Check enum values via BuildFlagsDocument.
	doc := toolio.BuildFlagsDocument(fs)
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal flags doc: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatalf("unmarshal flags doc: %v", err)
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("no properties in schema")
	}
	effortProp, ok := props["effort"].(map[string]any)
	if !ok {
		t.Fatal("no effort property in schema")
	}
	gotEnum, ok := effortProp["enum"]
	if !ok {
		t.Fatal("effort property has no enum")
	}
	wantEnum := []any{"off", "minimal", "low", "medium", "high", "xhigh", "max"}
	if !reflect.DeepEqual(gotEnum, wantEnum) {
		t.Errorf("effort enum = %v, want %v", gotEnum, wantEnum)
	}
}

// TS-13-2 (unit): Common carries an Effort field and no Variant field.
func TestTS13_2_CommonHasEffortNoVariant(t *testing.T) {
	v := reflect.TypeOf(toolio.Common{})
	if _, hasEffort := v.FieldByName("Effort"); !hasEffort {
		t.Error("Common should have an Effort field")
	}
	if _, hasVariant := v.FieldByName("Variant"); hasVariant {
		t.Error("Common should not have a Variant field")
	}
}

// TS-13-5 (unit): --effort value is matched case-insensitively and stored in
// canonical lower-case.
func TestTS13_5_EffortCaseInsensitive(t *testing.T) {
	for _, v := range []string{"HIGH", "High", "hIgH"} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)
		c := &toolio.Common{}
		c.Register(fs)
		if err := fs.Parse([]string{"--effort", v}); err != nil {
			t.Fatalf("parsing --effort %s: %v", v, err)
		}
		if c.Effort != "high" {
			t.Errorf("--effort %s: got Effort=%q, want %q", v, c.Effort, "high")
		}
	}
}

// TS-13-6 (unit): An invalid --effort value exits 2 and lists the accepted
// values.
func TestTS13_6_InvalidEffortValueIsError(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &toolio.Common{}
	c.Register(fs)
	err := fs.Parse([]string{"--effort", "turbo"})
	if err == nil {
		t.Fatal("expected an error for --effort turbo")
	}
	for _, want := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain accepted value %q", err.Error(), want)
		}
	}
}

// TS-13-7 (unit): An invalid $AF_MODEL_EFFORT value exits 2 and names the
// environment variable.
func TestTS13_7_InvalidEnvEffortValueIsUsageError(t *testing.T) {
	t.Setenv("AF_MODEL_EFFORT", "turbo")
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	c := &toolio.Common{Model: "STANDARD"}
	_, err := c.ResolveModelNamed("STANDARD")
	if err == nil {
		t.Fatal("expected an error for AF_MODEL_EFFORT=turbo")
	}
	var ue *toolio.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("expected a *UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "AF_MODEL_EFFORT") {
		t.Errorf("error %q should name AF_MODEL_EFFORT", err.Error())
	}
	for _, want := range []string{"off", "max"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should contain accepted value %q", err.Error(), want)
		}
	}
}

// TS-13-3 (unit): Effort precedence: flag > env > tier > unset, as a table test
func TestTS13_3_EffortPrecedence(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	cases := []struct {
		name  string
		flag  string
		env   string
		model string
		want  core.ThinkingLevel
	}{
		{"flag wins over tier", "low", "", "STANDARD", core.ThinkingLow},
		{"env wins over tier", "", "low", "STANDARD", core.ThinkingLow},
		{"tier's own effort", "", "", "STANDARD", core.ThinkingHigh},
		{"model id gets unset", "", "", "anthropic/claude-opus-5-5", core.ThinkingUnset},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AF_MODEL_EFFORT", tc.env)
			c := &toolio.Common{Effort: tc.flag, Model: tc.model}
			choice, err := c.ResolveModelNamed(tc.model)
			if err != nil {
				t.Fatalf("ResolveModelNamed(%q): %v", tc.model, err)
			}
			if choice.Thinking != tc.want {
				t.Errorf("Thinking = %q, want %q", choice.Thinking, tc.want)
			}
		})
	}
}

// TS-13-4 (unit): --effort applies to a model named by catalog id
func TestTS13_4_EffortAppliesToCatalogID(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	c := &toolio.Common{Effort: "max", Model: "anthropic/claude-opus-5-5"}
	choice, err := c.ResolveModelNamed("anthropic/claude-opus-5-5")
	if err != nil {
		t.Fatalf("ResolveModelNamed: %v", err)
	}
	if choice.Thinking == core.ThinkingUnset {
		t.Error("expected Thinking to be set when --effort is given with a catalog id")
	}
	// claude-opus-5-5 supports max, so it should be max
	if choice.Thinking != core.ThinkingMax {
		t.Errorf("Thinking = %q, want %q", choice.Thinking, core.ThinkingMax)
	}
}

// TS-13-8 (unit): ClampThinkingLevel is called for any set effort and the
// clamped level is the effective one reported in model.thinking
func TestTS13_8_ClampedLevelIsEffective(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	// claude-opus-4-5 supports off through high but NOT xhigh or max.
	// Requesting xhigh should clamp downward to high.
	c := &toolio.Common{Effort: "xhigh", Model: "anthropic/claude-opus-4-5"}
	choice, err := c.ResolveModelNamed("anthropic/claude-opus-4-5")
	if err != nil {
		t.Fatalf("ResolveModelNamed: %v", err)
	}
	if choice.Thinking != core.ThinkingHigh {
		t.Errorf("Thinking = %q, want %q (clamped from xhigh)", choice.Thinking, core.ThinkingHigh)
	}
}

// TS-13-9 (unit): When clamping changes the level, an effort_clamped warning
// is recorded naming both levels and the model
func TestTS13_9_EffortClampedWarningRecorded(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	// claude-opus-4-5 supports off through high but NOT xhigh or max.
	c := &toolio.Common{Effort: "xhigh", Model: "anthropic/claude-opus-4-5"}
	choice, err := c.ResolveModelNamed("anthropic/claude-opus-4-5")
	if err != nil {
		t.Fatalf("ResolveModelNamed: %v", err)
	}
	var found bool
	for _, w := range choice.Warnings {
		if w.Code == toolio.WarnEffortClamped {
			found = true
			if !strings.Contains(w.Message, "xhigh") {
				t.Errorf("warning message should name the requested level xhigh: %q", w.Message)
			}
			if !strings.Contains(w.Message, "high") {
				t.Errorf("warning message should name the clamped level high: %q", w.Message)
			}
			if !strings.Contains(w.Message, "claude-opus-4-5") {
				t.Errorf("warning message should name the model: %q", w.Message)
			}
		}
	}
	if !found {
		t.Error("expected an effort_clamped warning in ModelChoice.Warnings")
	}
}

// TS-13-10 (unit): ClampThinkingLevel returning ok==false with explicit effort
// fails before the first request
func TestTS13_10_NoReachableLevelFailsWithExplicitEffort(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "test-key")
	t.Setenv("AF_MODEL_EFFORT", "")
	// google/gemini-3.8-flash supports no thinking level (all null).
	c := &toolio.Common{Effort: "high", Model: "google/gemini-3.8-flash"}
	_, err := c.ResolveModelNamed("google/gemini-3.8-flash")
	if err == nil {
		t.Fatal("expected an error when no reachable level exists with explicit effort")
	}
	var ue *toolio.UsageError
	if !errors.As(err, &ue) {
		t.Errorf("expected a *UsageError, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "gemini-3.8-flash") {
		t.Errorf("error should name the model: %q", err.Error())
	}
}

// TS-13-17 (unit): --variant is no longer registered and Variant field is
// absent from Common.
func TestTS13_17_VariantNotRegistered(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &toolio.Common{}
	c.Register(fs)
	if fs.Lookup("variant") != nil {
		t.Error("--variant should not be registered")
	}
}

// TS-13-20 (unit): ResolveModelNamed no longer passes a variant argument to ModelSpec/ResolveModel
func TestTS13_20_ResolveModelNamedNoVariantReference(t *testing.T) {
	// Source inspection: read cli.go and confirm that ResolveModelNamed does
	// not reference c.Variant. Since the Variant field was removed from Common
	// in task 2, any reference would be a compile error. This test reads the
	// source to verify no string "c.Variant" appears.
	src, err := os.ReadFile("cli.go")
	if err != nil {
		t.Fatalf("reading cli.go: %v", err)
	}
	if strings.Contains(string(src), "c.Variant") {
		t.Error("cli.go still references c.Variant")
	}

	// Also a compile-time check: ResolveModel(spec, vendor) compiles with
	// two arguments. If it still took a variant, this would not compile.
	_, _, compileErr := agentrun.ResolveModel("STANDARD", "")
	if compileErr != nil {
		t.Fatalf("ResolveModel(STANDARD, \"\"): %v", compileErr)
	}
}
