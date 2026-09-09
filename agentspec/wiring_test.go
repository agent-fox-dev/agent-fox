package agentspec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/provider/faux"
)

// Wiring tests: the chain from a SpecSession method, through SpecAgent, to a
// provider that sits where a vendor sits.
//
// The old versions of these stopped one layer short — they mocked the Doer
// interface this package used to own, so they proved that AICall was reached
// and nothing about what it would have sent. Scripting provider/faux means the
// same chain is traced all the way through prompt assembly, tool declaration
// and the loop.

// newWiringSession creates a spec directory with a PRD and a session, and
// points it at a scripted provider.
func newWiringSession(t *testing.T, p *faux.Provider) (*SpecSession, string) {
	t.Helper()
	specDir := newSpecDir(t, "01", "widget_service")

	session, err := CreateSession(specDir, "interactive", "prd.md")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	session.Current = StateAssessing
	if err := session.persistSession(); err != nil {
		t.Fatalf("persistSession: %v", err)
	}
	session.SetRunOptions(fauxRun(p))
	return session, specDir
}

func TestAssessReachesTheProviderAndPersistsTheAssessment(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("needs_refinement")))
	session, specDir := newWiringSession(t, p)

	got, err := session.Assess(context.Background())
	if err != nil {
		t.Fatalf("Assess: %v", err)
	}
	if p.Calls() == 0 {
		t.Fatal("the chain never reached the provider")
	}
	if got.Quality != "needs_refinement" {
		t.Errorf("quality = %q", got.Quality)
	}
	if len(session.AssessmentHistory) != 1 {
		t.Errorf("the assessment history has %d entries, want 1", len(session.AssessmentHistory))
	}

	// The assessment has to survive a restart, or `spec refine` cannot answer
	// the questions it just asked.
	resumed, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession: %v", err)
	}
	if a := resumed.Assessment(); a == nil || a.Quality != "needs_refinement" {
		t.Errorf("the assessment did not survive persistence: %+v", a)
	}
}

func TestRefineReachesTheProviderAndRewritesThePRDOnDisk(t *testing.T) {
	p := faux.New(toolCallTurn("c1", ToolSubmitPRDUpdate, map[string]any{
		"updated_prd": "# Refined\n\nWith the answers folded in.",
		"assessment":  validAssessment("ready"),
	}))
	session, specDir := newWiringSession(t, p)
	session.AssessmentHistory = append(session.AssessmentHistory, Assessment{Quality: "needs_refinement"})

	got, err := session.Refine(context.Background(), map[string]string{"q1": "yes"})
	if err != nil {
		t.Fatalf("Refine: %v", err)
	}
	if got.Quality != "ready" {
		t.Errorf("quality = %q, want ready", got.Quality)
	}

	prd, err := os.ReadFile(filepath.Join(specDir, "prd.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prd), "With the answers folded in") {
		t.Error("the rewritten PRD was not written to disk")
	}
	if len(session.QAExchanges) != 1 {
		t.Errorf("the QA exchange was not recorded: %d", len(session.QAExchanges))
	}
}

func TestGenerateReachesTheProviderAndWritesTheArtifacts(t *testing.T) {
	p := generationScript("01", "widget_service")
	session, specDir := newWiringSession(t, p)
	session.Current = StatePRDAccepted
	if err := session.persistSession(); err != nil {
		t.Fatal(err)
	}

	res, err := session.Generate(context.Background())
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Artifacts) != 3 {
		t.Errorf("generated %d artifacts, want 3", len(res.Artifacts))
	}
	for _, name := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		if _, err := os.Stat(filepath.Join(specDir, name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}
	if session.State() != StateGenerated {
		t.Errorf("state = %q, want generated", session.State())
	}
}

func TestAProviderFailureReachesTheCallerAndIsRecorded(t *testing.T) {
	// A session that ends badly has to say so on disk: the CLI reads
	// last_error to explain a failed run that a human is no longer watching.
	p := faux.New(faux.Turn{Err: errUnauthorized{}})
	session, specDir := newWiringSession(t, p)

	if _, err := session.Assess(context.Background()); err == nil {
		t.Fatal("expected the provider failure to propagate")
	}
	resumed, err := ResumeSession(specDir)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.LastErr == nil {
		t.Error("the failure was not recorded in the session")
	}
}

type errUnauthorized struct{}

func (errUnauthorized) Error() string { return "unauthorized" }

func TestThePipelineEntryPointsCarryRunOptionsThrough(t *testing.T) {
	// AssessSpecWith and its siblings are how a CLI supplies the workspace,
	// the providers and the bounds without reaching into the session.
	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	_, specDir := newWiringSession(t, p)

	got, err := AssessSpecWith(context.Background(), specDir, fauxRun(p))
	if err != nil {
		t.Fatalf("AssessSpecWith: %v", err)
	}
	if got.Quality != "ready" {
		t.Errorf("quality = %q", got.Quality)
	}
}

func TestEveryPromptTemplateLoads(t *testing.T) {
	for _, name := range PromptTemplateNames {
		content, err := LoadPrompt(name, "")
		if err != nil {
			t.Errorf("LoadPrompt(%q): %v", name, err)
			continue
		}
		if strings.TrimSpace(content) == "" {
			t.Errorf("template %q is empty", name)
		}
		if strings.HasPrefix(content, "---") {
			t.Errorf("template %q still carries its frontmatter", name)
		}
	}
}

func TestAProjectOverrideReplacesTheEmbeddedTemplate(t *testing.T) {
	dir := t.TempDir()
	promptDir := filepath.Join(dir, ".spec", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "OVERRIDE-MARKER"
	if err := os.WriteFile(filepath.Join(promptDir, "assessment_system.md"),
		[]byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LoadPrompt("assessment_system", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != marker {
		t.Errorf("the override was not used: %q", got)
	}

	embedded, err := LoadPrompt("assessment_system", "")
	if err != nil {
		t.Fatal(err)
	}
	if embedded == marker {
		t.Error("the override leaked into the embedded default")
	}
}

func TestAnOverriddenSystemPromptReachesTheModel(t *testing.T) {
	// The override is only worth having if it lands in the request, which is
	// something only a test that reads the request can say.
	dir := t.TempDir()
	promptDir := filepath.Join(dir, ".spec", "prompts")
	if err := os.MkdirAll(promptDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const marker = "SYSTEM-OVERRIDE-MARKER"
	if err := os.WriteFile(filepath.Join(promptDir, "assessment_system.md"),
		[]byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}

	p := faux.New(toolCallTurn("c1", ToolSubmitAssessment, validAssessment("ready")))
	if _, err := newFauxAgent(p).AssessPRD(
		context.Background(), "# PRD", "01_x", WithProjectDir(dir)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(systemPromptOf(t, p, 0), marker) {
		t.Error("the overridden system prompt did not reach the model")
	}
}

func TestTheAssessmentSurvivesJSONRoundTrip(t *testing.T) {
	// SpecSession persists an Assessment verbatim, so a field that does not
	// round-trip is a field the next `spec refine` cannot see.
	a := Assessment{
		Quality:   "needs_refinement",
		Summary:   "s",
		Gaps:      []string{"g"},
		Questions: []map[string]any{{"id": "q1", "text": "t"}},
	}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var back Assessment
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Quality != a.Quality || len(back.Gaps) != 1 || len(back.Questions) != 1 {
		t.Errorf("the assessment did not round-trip: %+v", back)
	}
}
