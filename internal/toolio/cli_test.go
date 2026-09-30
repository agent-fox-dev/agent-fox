package toolio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/specgen"
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
