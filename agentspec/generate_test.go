package agentspec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// ---------------------------------------------------------------------------
// Sequential generation with full context (format v2 §12.1)
// ---------------------------------------------------------------------------

func TestGenerateArtifacts_HappyPath(t *testing.T) {
	capture := &aiCallCapture{}

	var callbackNames []string
	var mu sync.Mutex

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(capture, "07", "test")

	result, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test",
		WithOnArtifact(func(name string, _ any) {
			mu.Lock()
			defer mu.Unlock()
			callbackNames = append(callbackNames, name)
		}))
	if err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}

	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		if _, ok := result[name]; !ok {
			t.Errorf("result has no %q artifact", name)
		}
	}

	want := []string{"requirements", "test_spec", "tasks"}
	if len(callbackNames) != len(want) {
		t.Fatalf("callbacks = %v; want one per artifact", callbackNames)
	}
	for i, name := range want {
		if callbackNames[i] != name {
			t.Errorf("callback %d = %q; want %q", i, callbackNames[i], name)
		}
	}

	if capture.count() != 3 {
		t.Errorf("AICall count = %d; want one per artifact", capture.count())
	}
	if temp := capture.get(0).Temperature; temp == nil || *temp != 0.2 {
		t.Errorf("temperature = %v; want 0.2", temp)
	}
}

// TestGenerateArtifactsIsSequential guards §12.1. The v1 pipeline ran test_spec
// and tasks concurrently, which is why the task generator never saw a test ID.
func TestGenerateArtifactsIsSequential(t *testing.T) {
	var order []string
	var mu sync.Mutex

	agent := NewSpecAgent("STANDARD")
	inner := mockGenerationCall(nil, "07", "test")
	agent.aiCallFunc = func(ctx context.Context, opts AICallOptions) (string, any, error) {
		mu.Lock()
		order = append(order, artifactNameFromContext(opts.Context))
		mu.Unlock()
		return inner(ctx, opts)
	}

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}

	want := []string{"requirements", "test_spec", "tasks"}
	if len(order) != len(want) {
		t.Fatalf("calls = %v; want %v", order, want)
	}
	for i, name := range want {
		if order[i] != name {
			t.Fatalf("call order = %v; want %v", order, want)
		}
	}
}

// TestTaskPromptCarriesEveryTestID is the prompt-side half of the same guard:
// the tasks step must be able to see the IDs it has to own (rules C7 and C9).
func TestTaskPromptCarriesEveryTestID(t *testing.T) {
	capture := &aiCallCapture{}
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(capture, "07", "test")

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}

	var tasksPrompt string
	for i := 0; i < capture.count(); i++ {
		opts := capture.get(i)
		if artifactNameFromContext(opts.Context) == "tasks" {
			tasksPrompt = messageContentString(opts.Messages[0])
		}
	}
	if tasksPrompt == "" {
		t.Fatal("no tasks call was captured")
	}

	for _, id := range []string{"TS-07-1", "TS-07-2", "TS-07-3"} {
		if !strings.Contains(tasksPrompt, id) {
			t.Errorf("the tasks prompt does not carry test ID %s; the task generator cannot own what it cannot see", id)
		}
	}
	for _, id := range []string{"07-REQ-1.1", "07-REQ-1.2", "07-PATH-1"} {
		if !strings.Contains(tasksPrompt, id) {
			t.Errorf("the tasks prompt does not carry %s", id)
		}
	}
	if !strings.Contains(tasksPrompt, "smoke") {
		t.Error("the tasks prompt does not say which test is the smoke test")
	}
}

// TestTestSpecPromptCarriesFullRequirements guards the other half of §12.1: a
// compact Markdown rendering is acceptable, an ID-only summary is not.
func TestTestSpecPromptCarriesFullRequirements(t *testing.T) {
	capture := &aiCallCapture{}
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(capture, "07", "test")

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}

	var prompt string
	for i := 0; i < capture.count(); i++ {
		if opts := capture.get(i); artifactNameFromContext(opts.Context) == "test_spec" {
			prompt = messageContentString(opts.Messages[0])
		}
	}
	if prompt == "" {
		t.Fatal("no test_spec call was captured")
	}

	for _, want := range []string{
		"07-REQ-1.1",
		"WHEN a client submits a widget with a name", // the criterion sentence
		"HTTP 400 with body {error: string}",         // the contract
		"07-PATH-1",                                  // the path ID
		"A client stores a widget and reads it back", // the path title
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the test_spec prompt is missing %q", want)
		}
	}
}

// ---------------------------------------------------------------------------
// Inline validation and repair (§12.2)
// ---------------------------------------------------------------------------

// brokenArtifact returns the valid artifact for a step with one mutation
// applied, so a test exercises exactly one rule.
func brokenArtifact(step afspec.GenerationStep, specID, specName string, mutate func(map[string]any)) map[string]any {
	m := v2Artifact(step, specID, specName)
	mutate(m)
	return m
}

func TestGenerateArtifactsRepairsAnInvalidArtifact(t *testing.T) {
	capture := &aiCallCapture{}
	attempts := map[string]int{}
	var mu sync.Mutex

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = func(_ context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		name := artifactNameFromContext(opts.Context)

		mu.Lock()
		attempts[name]++
		attempt := attempts[name]
		mu.Unlock()

		content := v2ArtifactByName(name, "07", "test")
		if name == "tasks" && attempt == 1 {
			// First attempt orphans the smoke test: C7 and C9 both fire.
			content = brokenArtifact(afspec.StepTasks, "07", "test", func(m map[string]any) {
				tasks := m["tasks"].([]any)
				tasks[1].(map[string]any)["tests"] = []any{"TS-07-1"}
			})
		}
		return "", makeToolCallResponse("end_turn", makeArtifactToolCall(name, content)), nil
	}

	result, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test")
	if err != nil {
		t.Fatalf("GenerateArtifacts = %v; the repair attempt should have succeeded", err)
	}
	if result["tasks"] == nil {
		t.Fatal("no tasks artifact in the result")
	}

	// One repair round: 3 artifacts + 1 retry.
	if capture.count() != 4 {
		t.Errorf("AICall count = %d; want 4 (three artifacts plus one repair)", capture.count())
	}

	// The repair call must carry the rule that failed.
	repair := capture.get(capture.count() - 1)
	if !strings.Contains(repair.Context, "repair") {
		t.Fatalf("the last call is not a repair: context %q", repair.Context)
	}
	feedback := messageContentString(repair.Messages[len(repair.Messages)-1])
	if !strings.Contains(feedback, "C7") && !strings.Contains(feedback, "C9") {
		t.Errorf("the repair feedback names no rule:\n%s", feedback)
	}
	if !strings.Contains(feedback, "TS-07-3") {
		t.Errorf("the repair feedback does not name the offending test:\n%s", feedback)
	}
}

func TestGenerateArtifactsRepairConversationShape(t *testing.T) {
	capture := &aiCallCapture{}
	attempts := 0

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = func(_ context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		name := artifactNameFromContext(opts.Context)
		content := v2ArtifactByName(name, "07", "test")
		if name == "requirements" {
			attempts++
			if attempts == 1 {
				content = brokenArtifact(afspec.StepRequirements, "07", "test", func(m map[string]any) {
					m["introduction"] = ""
				})
			}
		}
		return "", makeToolCallResponse("end_turn", makeArtifactToolCall(name, content)), nil
	}

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}

	var repair AICallOptions
	for i := 0; i < capture.count(); i++ {
		if strings.Contains(capture.get(i).Context, "repair") {
			repair = capture.get(i)
			break
		}
	}
	if len(repair.Messages) != 3 {
		t.Fatalf("repair messages = %d; want the original prompt, the model's reply and a tool_result", len(repair.Messages))
	}
	if repair.Messages[0].Role != "user" || repair.Messages[1].Role != "assistant" || repair.Messages[2].Role != "user" {
		t.Errorf("repair roles = %q/%q/%q; want user/assistant/user",
			repair.Messages[0].Role, repair.Messages[1].Role, repair.Messages[2].Role)
	}
	blocks, ok := repair.Messages[2].Content.([]ContentBlock)
	if !ok || len(blocks) == 0 || blocks[0].Type != "tool_result" {
		t.Fatalf("the third message is not a tool_result: %#v", repair.Messages[2].Content)
	}
	if blocks[0].ToolUseID == "" {
		t.Error("the tool_result carries no tool_use_id")
	}
}

func TestGenerateArtifactsFailsAfterRepairsAreExhausted(t *testing.T) {
	capture := &aiCallCapture{}

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = func(_ context.Context, opts AICallOptions) (string, any, error) {
		capture.record(opts)
		name := artifactNameFromContext(opts.Context)
		content := v2ArtifactByName(name, "07", "test")
		if name == "requirements" {
			content = brokenArtifact(afspec.StepRequirements, "07", "test", func(m map[string]any) {
				// An unwanted criterion with no contract: rule C10.
				reqs := m["requirements"].([]any)
				criteria := reqs[0].(map[string]any)["criteria"].([]any)
				delete(criteria[1].(map[string]any), "contract")
			})
		}
		return "", makeToolCallResponse("end_turn", makeArtifactToolCall(name, content)), nil
	}

	_, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test")
	if err == nil {
		t.Fatal("GenerateArtifacts accepted an artifact that never became valid")
	}
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error is %T; want *AgentError", err)
	}
	if agentErr.ErrorCategory != "validation" {
		t.Errorf("category = %q; want validation", agentErr.ErrorCategory)
	}
	if !strings.Contains(err.Error(), "C10") {
		t.Errorf("the error does not name the rule that failed: %v", err)
	}

	// One initial call plus maxRepairs, and nothing after the failure.
	if capture.count() != 1+maxRepairs {
		t.Errorf("AICall count = %d; want %d", capture.count(), 1+maxRepairs)
	}
}

func TestGenerateArtifactsStopsAtTheFirstFailedStep(t *testing.T) {
	var generated []string
	var mu sync.Mutex

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = func(_ context.Context, opts AICallOptions) (string, any, error) {
		name := artifactNameFromContext(opts.Context)
		mu.Lock()
		generated = append(generated, name)
		mu.Unlock()

		content := v2ArtifactByName(name, "07", "test")
		if name == "test_spec" {
			content = brokenArtifact(afspec.StepTestSpec, "07", "test", func(m map[string]any) {
				// A dangling verifies reference: rule C3.
				tests := m["tests"].([]any)
				tests[0].(map[string]any)["verifies"] = []any{"07-REQ-9.9"}
			})
		}
		return "", makeToolCallResponse("end_turn", makeArtifactToolCall(name, content)), nil
	}

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err == nil {
		t.Fatal("a spec with a dangling reference was generated")
	}
	for _, name := range generated {
		if name == "tasks" {
			t.Fatal("the tasks step ran after the test_spec step had failed")
		}
	}
}

func TestGenerateArtifactsRejectsAMissingToolCall(t *testing.T) {
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = func(_ context.Context, _ AICallOptions) (string, any, error) {
		return "", &MessageResponse{StopReason: "end_turn"}, nil
	}

	_, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test")
	if err == nil {
		t.Fatal("a response with no tool call was accepted")
	}
	if !strings.Contains(err.Error(), "no requirements artifact") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGenerateArtifactsHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(nil, "07", "test")

	if _, err := agent.GenerateArtifacts(ctx, "PRD", "07", "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v; want context.Canceled", err)
	}
}

func TestGenerateArtifactsSurfacesACallbackPanic(t *testing.T) {
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(nil, "07", "test")

	_, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test",
		WithOnArtifact(func(string, any) { panic("boom") }))
	if err == nil {
		t.Fatal("a panicking callback did not produce an error")
	}
	if !strings.Contains(err.Error(), "panicked") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGenerateArtifactsWithNoCallbackDoesNotPanic(t *testing.T) {
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(nil, "07", "test")

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test", WithOnArtifact(nil)); err != nil {
		t.Fatalf("GenerateArtifacts = %v", err)
	}
}

func TestGenerateArtifactsLeavesMaxTokensToTheDefault(t *testing.T) {
	capture := &aiCallCapture{}
	agent := NewSpecAgent("STANDARD")
	agent.aiCallFunc = mockGenerationCall(capture, "07", "test")

	if _, err := agent.GenerateArtifacts(context.Background(), "PRD", "07", "test"); err != nil {
		t.Fatal(err)
	}
	if got := capture.get(0).MaxTokens; got != 0 {
		t.Errorf("MaxTokens = %d; want 0 so that ApplyDefaults sets it", got)
	}
}

// ---------------------------------------------------------------------------
// validateArtifactContent
// ---------------------------------------------------------------------------

func TestValidateArtifactContentAcceptsValidArtifacts(t *testing.T) {
	partial := afspec.PartialSpec{SpecID: "07", SpecName: "test"}
	for _, step := range afspec.GenerationSteps {
		content, err := validateArtifactContent(v2Artifact(step, "07", "test"), step, &partial)
		if err != nil {
			t.Fatalf("step %s: %v", step, err)
		}
		if content == nil {
			t.Fatalf("step %s returned nil content", step)
		}
	}
	if partial.Requirements == nil || partial.TestSpec == nil || partial.Tasks == nil {
		t.Error("a successful run did not record every artifact in the partial spec")
	}
}

func TestValidateArtifactContentRejectsBadInput(t *testing.T) {
	partial := afspec.PartialSpec{SpecID: "07", SpecName: "test"}

	if _, err := validateArtifactContent(nil, afspec.StepRequirements, &partial); err == nil {
		t.Error("nil content was accepted")
	}
	if _, err := validateArtifactContent("not a map", afspec.StepRequirements, &partial); err == nil {
		t.Error("a non-object tool input was accepted")
	}
}

// TestValidateArtifactContentLeavesPartialUntouchedOnFailure keeps a rejected
// artifact from poisoning the next repair attempt.
func TestValidateArtifactContentLeavesPartialUntouchedOnFailure(t *testing.T) {
	partial := afspec.PartialSpec{SpecID: "07", SpecName: "test"}

	broken := brokenArtifact(afspec.StepRequirements, "07", "test", func(m map[string]any) {
		m["introduction"] = ""
	})
	if _, err := validateArtifactContent(broken, afspec.StepRequirements, &partial); err == nil {
		t.Fatal("a schema-invalid artifact was accepted")
	}
	if partial.Requirements != nil {
		t.Error("a rejected artifact was recorded in the partial spec")
	}
}

func TestValidateArtifactContentReportsTheRule(t *testing.T) {
	cases := []struct {
		name   string
		step   afspec.GenerationStep
		steps  []afspec.GenerationStep
		break_ func(map[string]any)
		want   string
	}{
		{
			name:  "unwanted criterion without a contract",
			step:  afspec.StepRequirements,
			steps: []afspec.GenerationStep{},
			break_: func(m map[string]any) {
				reqs := m["requirements"].([]any)
				criteria := reqs[0].(map[string]any)["criteria"].([]any)
				delete(criteria[1].(map[string]any), "contract")
			},
			want: "C10",
		},
		{
			name:  "criterion verified by no test",
			step:  afspec.StepTestSpec,
			steps: []afspec.GenerationStep{afspec.StepRequirements},
			break_: func(m map[string]any) {
				tests := m["tests"].([]any)
				m["tests"] = []any{tests[0], tests[2]}
			},
			want: "C4",
		},
		{
			name:  "unit test carrying real_components",
			step:  afspec.StepTestSpec,
			steps: []afspec.GenerationStep{afspec.StepRequirements},
			break_: func(m map[string]any) {
				tests := m["tests"].([]any)
				tests[0].(map[string]any)["real_components"] = []any{"a database"}
			},
			want: "C11",
		},
		{
			name:  "test owned by no task",
			step:  afspec.StepTasks,
			steps: []afspec.GenerationStep{afspec.StepRequirements, afspec.StepTestSpec},
			break_: func(m map[string]any) {
				tasks := m["tasks"].([]any)
				tasks[0].(map[string]any)["tests"] = []any{"TS-07-1"}
			},
			want: "C7",
		},
		{
			name:  "malformed test ID",
			step:  afspec.StepTestSpec,
			steps: []afspec.GenerationStep{afspec.StepRequirements},
			break_: func(m map[string]any) {
				tests := m["tests"].([]any)
				tests[0].(map[string]any)["id"] = "TS-07-E1"
			},
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			partial := afspec.PartialSpec{SpecID: "07", SpecName: "test"}
			for _, step := range tc.steps {
				if _, err := validateArtifactContent(v2Artifact(step, "07", "test"), step, &partial); err != nil {
					t.Fatalf("setting up %s: %v", step, err)
				}
			}

			content := brokenArtifact(tc.step, "07", "test", tc.break_)
			_, err := validateArtifactContent(content, tc.step, &partial)
			if err == nil {
				t.Fatal("the broken artifact was accepted")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not name rule %s: %v", tc.want, err)
			}
		})
	}
}

// TestArtifactToolUsesTheV2Schema checks that the tool the model is given
// describes the v2 shape and carries no conditional keywords.
func TestArtifactToolUsesTheV2Schema(t *testing.T) {
	for _, name := range []string{"requirements", "test_spec", "tasks"} {
		t.Run(name, func(t *testing.T) {
			defs := ArtifactTool(name)
			if len(defs) != 1 {
				t.Fatalf("ArtifactTool(%q) returned %d definitions", name, len(defs))
			}
			if defs[0]["name"] != "submit_"+name {
				t.Errorf("tool name = %v", defs[0]["name"])
			}
			schema, ok := defs[0]["input_schema"].(map[string]any)
			if !ok {
				t.Fatalf("input_schema is %T", defs[0]["input_schema"])
			}
			props, ok := schema["properties"].(map[string]any)
			if !ok {
				t.Fatal("the schema has no properties")
			}
			if _, present := props["schema_version"]; !present {
				t.Error("the schema has no schema_version property")
			}
			assertNoConditionals(t, schema, "")
		})
	}

	if got := ArtifactTool("nonsense"); len(got) != 0 {
		t.Errorf("ArtifactTool on an unknown name returned %v", got)
	}
}

// assertNoConditionals walks a schema and fails on any keyword the Anthropic
// tool schema does not support.
func assertNoConditionals(t *testing.T, node any, path string) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "allOf" || key == "anyOf" || key == "oneOf" || key == "if" || key == "then" || key == "else" || key == "not" {
				t.Errorf("the tool schema still carries %q at %s", key, path)
			}
			assertNoConditionals(t, child, fmt.Sprintf("%s/%s", path, key))
		}
	case []any:
		for i, child := range v {
			assertNoConditionals(t, child, fmt.Sprintf("%s/%d", path, i))
		}
	}
}
