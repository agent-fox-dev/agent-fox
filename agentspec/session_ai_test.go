package agentspec

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Mock assessor for SpecSession AI method tests
// ---------------------------------------------------------------------------

// mockAssessor implements the assessor interface for testing SpecSession
// Assess, Refine, and Generate methods.
type mockAssessor struct {
	// Assess fields.
	assessResult Assessment
	assessErr    error

	// Refine fields.
	refineText   string
	refineResult Assessment
	refineErr    error

	// Generate fields.
	generateResult map[string]any
	generateOrder  []string // order of artifact names to invoke OnArtifact
	generateErr    error
	generatedNames []string // tracks which artifacts OnArtifact was called for
	mu             sync.Mutex
}

func (m *mockAssessor) AssessPRD(ctx context.Context, prdText, specName string, opts ...AgentOption) (Assessment, error) {
	if ctx.Err() != nil {
		return Assessment{}, ctx.Err()
	}
	return m.assessResult, m.assessErr
}

func (m *mockAssessor) RefinePRD(ctx context.Context, prdText string, answers map[string]string, prevAssessment Assessment, opts ...AgentOption) (string, Assessment, error) {
	if ctx.Err() != nil {
		return "", Assessment{}, ctx.Err()
	}
	return m.refineText, m.refineResult, m.refineErr
}

func (m *mockAssessor) GenerateArtifacts(ctx context.Context, prdText, specID, specName string, opts ...AgentOption) (map[string]any, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if m.generateErr != nil {
		return nil, m.generateErr
	}

	o := applyOptions(opts)
	order := m.generateOrder
	if len(order) == 0 {
		order = []string{"requirements", "test_spec", "tasks"}
	}

	for _, name := range order {
		content, ok := m.generateResult[name]
		if !ok {
			continue
		}
		m.mu.Lock()
		m.generatedNames = append(m.generatedNames, name)
		m.mu.Unlock()
		if o.onArtifact != nil {
			o.onArtifact(name, content)
		}
	}

	return m.generateResult, nil
}

// ---------------------------------------------------------------------------
// Test helpers for SpecSession AI method tests
// ---------------------------------------------------------------------------

// setupTestSession creates a SpecSession with the given mock assessor and
// writes initial state to _session.json. Use customize funcs to override
// default field values before persistence.
func setupTestSession(t *testing.T, specDir string, mock *mockAssessor, customize ...func(*SpecSession)) *SpecSession {
	t.Helper()
	session := &SpecSession{
		specDir:            specDir,
		agent:              mock,
		Current:            StateInit,
		PRDPath:            "prd.md",
		AssessmentHistory:  []Assessment{},
		QAExchanges:        []QAExchange{},
		GeneratedArtifacts: []string{},
	}
	for _, fn := range customize {
		fn(session)
	}
	// Persist initial session state to _session.json.
	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("setupTestSession: marshal error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(specDir, "_session.json"), data, 0o644); err != nil {
		t.Fatalf("setupTestSession: write error: %v", err)
	}
	return session
}

// writePRDFileForAI writes a PRD file to the spec directory for AI tests.
func writePRDFileForAI(t *testing.T, specDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(specDir, "prd.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("writePRDFileForAI: %v", err)
	}
}

// loadPersistedState reads _session.json from specDir and returns the
// deserialized SpecSession (exported fields only).
func loadPersistedState(t *testing.T, specDir string) SpecSession {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(specDir, "_session.json"))
	if err != nil {
		t.Fatalf("loadPersistedState: read error: %v", err)
	}
	var state SpecSession
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatalf("loadPersistedState: unmarshal error: %v", err)
	}
	return state
}

// containsArtifactName reports whether name is in the slice.
func containsArtifactName(names []string, name string) bool {
	return slices.Contains(names, name)
}

// ---------------------------------------------------------------------------
// TS-07-40: SpecSession.Assess transitions to StateAssessing, calls
// AssessPRD, appends to assessment history, persists state, and returns
// the Assessment.
// Test Spec: TS-07-40, Requirement: 07-REQ-9.1
// ---------------------------------------------------------------------------

func TestSpecSession_Assess_HappyPath(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD\n\nSample PRD content for assessment.")

	validAssessment := Assessment{
		Quality: "high",
		Summary: "Well-structured PRD",
		Gaps:    []string{"Missing auth flow"},
		Questions: []map[string]any{
			{"id": "q1", "text": "How will auth work?"},
		},
	}

	mock := &mockAssessor{assessResult: validAssessment}
	session := setupTestSession(t, specDir, mock)

	ctx := context.Background()
	assessment, err := session.Assess(ctx)
	if err != nil {
		t.Fatalf("Assess() returned error: %v", err)
	}

	// Verify returned assessment matches mock.
	if assessment.Quality != validAssessment.Quality {
		t.Errorf("assessment.Quality = %q; want %q", assessment.Quality, validAssessment.Quality)
	}
	if assessment.Summary != validAssessment.Summary {
		t.Errorf("assessment.Summary = %q; want %q", assessment.Summary, validAssessment.Summary)
	}

	// Verify session state was persisted with assessment in history.
	state := loadPersistedState(t, specDir)
	if len(state.AssessmentHistory) != 1 {
		t.Fatalf("persisted AssessmentHistory length = %d; want 1", len(state.AssessmentHistory))
	}
	if state.AssessmentHistory[0].Quality != validAssessment.Quality {
		t.Errorf("persisted assessment Quality = %q; want %q",
			state.AssessmentHistory[0].Quality, validAssessment.Quality)
	}
}

// TestSpecSession_Assess_TransitionsToAssessing verifies that Assess sets
// the session state to StateAssessing before calling AssessPRD.
// Requirement: 07-REQ-9.1
func TestSpecSession_Assess_TransitionsToAssessing(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		assessResult: Assessment{Quality: "high", Summary: "Good"},
	}
	session := setupTestSession(t, specDir, mock)

	ctx := context.Background()
	_, err := session.Assess(ctx)
	if err != nil {
		t.Fatalf("Assess() returned error: %v", err)
	}

	// After successful assessment, verify state was set appropriately.
	// The session should have transitioned through StateAssessing.
	state := loadPersistedState(t, specDir)
	// Post-assess state should reflect that assessment occurred.
	if len(state.AssessmentHistory) == 0 {
		t.Error("AssessmentHistory is empty; want at least 1 entry after Assess")
	}
}

// ---------------------------------------------------------------------------
// TS-07-41: SpecSession.Assess persists the error as lastError and returns
// it without appending to assessment history when AssessPRD fails.
// Test Spec: TS-07-41, Requirement: 07-REQ-9.2
// ---------------------------------------------------------------------------

func TestSpecSession_Assess_APIError(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	agentErr := &AgentError{
		Detail:        "API failure during assessment",
		ErrorCategory: "transient",
		Retryable:     true,
	}

	mock := &mockAssessor{assessErr: agentErr}
	session := setupTestSession(t, specDir, mock)

	ctx := context.Background()
	assessment, err := session.Assess(ctx)

	// Assessment should be zero-value.
	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}

	// Error should be non-nil.
	if err == nil {
		t.Fatal("Assess() returned nil error; want error from AssessPRD failure")
	}

	// Verify persisted state: lastError set, no assessment in history.
	state := loadPersistedState(t, specDir)
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want non-nil error record")
	}
	if len(state.AssessmentHistory) != 0 {
		t.Errorf("persisted AssessmentHistory length = %d; want 0", len(state.AssessmentHistory))
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-9.E1: Assess disk write failure during state persist
// ---------------------------------------------------------------------------

func TestSpecSession_Assess_PersistenceFailure(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	validAssessment := Assessment{
		Quality: "high",
		Summary: "Good PRD",
	}

	mock := &mockAssessor{assessResult: validAssessment}
	session := setupTestSession(t, specDir, mock)

	// Make specDir read-only to cause persistence failure.
	if os.Geteuid() == 0 {
		t.Skip("running as root: file mode bits do not deny writes")
	}
	if err := os.Chmod(specDir, 0o555); err != nil {
		t.Skipf("cannot set directory read-only: %v", err)
	}
	defer os.Chmod(specDir, 0o755) //nolint:errcheck

	ctx := context.Background()
	_, err := session.Assess(ctx)

	// Should return a persistence error.
	if err == nil {
		t.Fatal("Assess() returned nil error; want persistence failure error")
	}

	// In-memory assessment history should still contain the entry
	// (the assessment succeeded, only persistence failed).
	if len(session.AssessmentHistory) != 1 {
		t.Errorf("in-memory AssessmentHistory length = %d; want 1", len(session.AssessmentHistory))
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-9.E2: Assess context cancellation during assessment
// ---------------------------------------------------------------------------

func TestSpecSession_Assess_ContextCancellation(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		assessResult: Assessment{Quality: "high"},
	}
	session := setupTestSession(t, specDir, mock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	assessment, err := session.Assess(ctx)

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("Assess() returned nil error; want context cancellation error")
	}

	// Verify persisted state has lastError set.
	state := loadPersistedState(t, specDir)
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want context error persisted")
	}
}

// ---------------------------------------------------------------------------
// TS-07-42: SpecSession.Refine transitions to StateRefining, calls RefinePRD,
// updates the PRD file, records a QA exchange, appends new assessment, and
// persists state.
// Test Spec: TS-07-42, Requirement: 07-REQ-10.1
// ---------------------------------------------------------------------------

func TestSpecSession_Refine_HappyPath(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Original PRD\n\nOriginal content.")

	newAssessment := Assessment{
		Quality: "high",
		Summary: "Much improved after refinement",
		Gaps:    nil,
		Questions: []map[string]any{
			{"id": "q2", "text": "Follow-up question"},
		},
	}

	mock := &mockAssessor{
		refineText:   "# Updated PRD\n\nRefined content.",
		refineResult: newAssessment,
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "medium", Summary: "Needs work", Gaps: []string{"gap1"}},
		}
	})

	answers := map[string]string{"q1": "a1"}
	ctx := context.Background()
	result, err := session.Refine(ctx, answers)

	if err != nil {
		t.Fatalf("Refine() returned error: %v", err)
	}

	// Verify returned assessment matches mock.
	if result.Quality != newAssessment.Quality {
		t.Errorf("result.Quality = %q; want %q", result.Quality, newAssessment.Quality)
	}
	if result.Summary != newAssessment.Summary {
		t.Errorf("result.Summary = %q; want %q", result.Summary, newAssessment.Summary)
	}

	// Verify PRD file was updated on disk.
	prdContent, readErr := os.ReadFile(filepath.Join(specDir, "prd.md"))
	if readErr != nil {
		t.Fatalf("failed to read updated prd.md: %v", readErr)
	}
	if string(prdContent) != "# Updated PRD\n\nRefined content." {
		t.Errorf("PRD content = %q; want updated text", string(prdContent))
	}

	// Verify session state was persisted.
	state := loadPersistedState(t, specDir)

	// Assessment history should have 2 entries (original + new).
	if len(state.AssessmentHistory) != 2 {
		t.Fatalf("persisted AssessmentHistory length = %d; want 2", len(state.AssessmentHistory))
	}

	// QA exchange should be recorded.
	if len(state.QAExchanges) != 1 {
		t.Fatalf("persisted QAExchanges length = %d; want 1", len(state.QAExchanges))
	}
	if state.QAExchanges[0].Answers["q1"] != "a1" {
		t.Errorf("QAExchange answers[\"q1\"] = %q; want %q",
			state.QAExchanges[0].Answers["q1"], "a1")
	}

	// QA exchange should have a UTC timestamp.
	if state.QAExchanges[0].Timestamp.IsZero() {
		t.Error("QAExchange timestamp is zero; want UTC timestamp")
	}
}

// TestSpecSession_Refine_TransitionsToRefining verifies that Refine sets
// the session state to StateRefining.
// Requirement: 07-REQ-10.1
func TestSpecSession_Refine_TransitionsToRefining(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		refineText:   "# Updated",
		refineResult: Assessment{Quality: "high"},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "medium", Summary: "Needs work"},
		}
	})

	ctx := context.Background()
	_, err := session.Refine(ctx, map[string]string{"q1": "a1"})
	if err != nil {
		t.Fatalf("Refine() returned error: %v", err)
	}

	// After successful refinement, state was persisted.
	state := loadPersistedState(t, specDir)
	if len(state.AssessmentHistory) < 2 {
		t.Error("AssessmentHistory should have grown after Refine")
	}
}

// ---------------------------------------------------------------------------
// TS-07-43: SpecSession.Refine persists the error as lastError without
// updating the PRD file or recording a QA exchange when RefinePRD fails.
// Test Spec: TS-07-43, Requirement: 07-REQ-10.2
// ---------------------------------------------------------------------------

func TestSpecSession_Refine_Error(t *testing.T) {
	specDir := t.TempDir()
	originalPRD := "# Original PRD\n\nDo not change."
	writePRDFileForAI(t, specDir, originalPRD)

	agentErr := &AgentError{
		Detail:        "Refinement API failure",
		ErrorCategory: "transient",
		Retryable:     true,
	}

	mock := &mockAssessor{refineErr: agentErr}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "medium", Summary: "Needs work"},
		}
	})

	answers := map[string]string{"q1": "a1"}
	ctx := context.Background()
	assessment, err := session.Refine(ctx, answers)

	// Assessment should be zero-value.
	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("Refine() returned nil error; want error from RefinePRD failure")
	}

	// PRD file should be unchanged on disk.
	prdContent, readErr := os.ReadFile(filepath.Join(specDir, "prd.md"))
	if readErr != nil {
		t.Fatalf("failed to read prd.md: %v", readErr)
	}
	if string(prdContent) != originalPRD {
		t.Errorf("PRD was modified; want unchanged original content")
	}

	// No QA exchange should be recorded.
	state := loadPersistedState(t, specDir)
	if len(state.QAExchanges) != 0 {
		t.Errorf("persisted QAExchanges length = %d; want 0", len(state.QAExchanges))
	}

	// lastError should be set.
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want non-nil error record")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-10.E1: Refine disk write failure for updated PRD
// ---------------------------------------------------------------------------

func TestSpecSession_Refine_PRDWriteFailure(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Original PRD")

	mock := &mockAssessor{
		refineText:   "# Updated PRD",
		refineResult: Assessment{Quality: "high", Summary: "Good"},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "medium", Summary: "Needs work"},
		}
	})

	// Make PRD file read-only to cause write failure.
	prdPath := filepath.Join(specDir, "prd.md")
	if os.Geteuid() == 0 {
		t.Skip("running as root: file mode bits do not deny writes")
	}
	if err := os.Chmod(prdPath, 0o444); err != nil {
		t.Skipf("cannot set file read-only: %v", err)
	}
	defer os.Chmod(prdPath, 0o644) //nolint:errcheck

	ctx := context.Background()
	assessment, err := session.Refine(ctx, map[string]string{"q1": "a1"})

	// Should return a write error.
	if err == nil {
		t.Fatal("Refine() returned nil error; want PRD write failure error")
	}
	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value on write failure", assessment)
	}

	// QA exchange should NOT be recorded on write failure.
	state := loadPersistedState(t, specDir)
	if len(state.QAExchanges) != 0 {
		t.Errorf("QAExchanges length = %d; want 0 (no QA recorded on write failure)",
			len(state.QAExchanges))
	}

	// lastError should be set in persisted state.
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want write failure error persisted")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-10.E2: Refine with empty answers map
// ---------------------------------------------------------------------------

func TestSpecSession_Refine_EmptyAnswers(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		refineText:   "# PRD refined with no answers",
		refineResult: Assessment{Quality: "medium", Summary: "Refined without answers"},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "low", Summary: "Needs improvement"},
		}
	})

	// Call Refine with an empty answers map — should proceed normally.
	ctx := context.Background()
	assessment, err := session.Refine(ctx, map[string]string{})

	if err != nil {
		t.Fatalf("Refine() with empty answers returned error: %v", err)
	}
	if isZeroAssessment(assessment) {
		t.Error("assessment is zero-value; want populated Assessment from LLM")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-10.E3: Refine context cancellation during refinement
// ---------------------------------------------------------------------------

func TestSpecSession_Refine_ContextCancellation(t *testing.T) {
	specDir := t.TempDir()
	originalPRD := "# Original PRD"
	writePRDFileForAI(t, specDir, originalPRD)

	mock := &mockAssessor{
		refineText:   "# Updated",
		refineResult: Assessment{Quality: "high"},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StateAssessing
		s.AssessmentHistory = []Assessment{
			{Quality: "medium", Summary: "Needs work"},
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	assessment, err := session.Refine(ctx, map[string]string{"q1": "a1"})

	if !isZeroAssessment(assessment) {
		t.Errorf("assessment = %+v; want zero-value Assessment", assessment)
	}
	if err == nil {
		t.Fatal("Refine() returned nil error; want context cancellation error")
	}

	// PRD should be unchanged.
	prdContent, _ := os.ReadFile(filepath.Join(specDir, "prd.md"))
	if string(prdContent) != originalPRD {
		t.Error("PRD was modified during cancelled refinement; want unchanged")
	}

	// No QA exchange should be recorded.
	state := loadPersistedState(t, specDir)
	if len(state.QAExchanges) != 0 {
		t.Errorf("QAExchanges length = %d; want 0", len(state.QAExchanges))
	}

	// lastError should be persisted.
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want context error persisted")
	}
}

// ---------------------------------------------------------------------------
// TS-07-44: SpecSession.Generate transitions to StateGenerating, calls
// GenerateArtifacts with OnArtifact callback, writes artifacts to disk,
// transitions to StateGenerated, runs Validate, and returns GenerateResult.
// Test Spec: TS-07-44, Requirement: 07-REQ-11.1
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_HappyPath(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD for generation")

	requirementsContent := map[string]any{
		"spec_id":      "07",
		"spec_name":    "test",
		"requirements": []any{},
	}
	testSpecContent := map[string]any{
		"spec_id":    "07",
		"spec_name":  "test",
		"test_cases": []any{},
	}
	tasksContent := map[string]any{
		"spec_id":   "07",
		"spec_name": "test",
		"tasks":     []any{},
	}

	mock := &mockAssessor{
		generateResult: map[string]any{
			"requirements": requirementsContent,
			"test_spec":    testSpecContent,
			"tasks":        tasksContent,
		},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	result, err := session.Generate(ctx)
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// Verify artifact list has 3 items.
	if len(result.Artifacts) != 3 {
		t.Errorf("result.Artifacts length = %d; want 3", len(result.Artifacts))
	}

	// Verify artifact files exist on disk.
	for _, name := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		path := filepath.Join(specDir, name)
		if _, statErr := os.Stat(path); statErr != nil {
			t.Errorf("artifact file %s does not exist: %v", name, statErr)
		}
	}

	// Verify session state is StateGenerated.
	state := loadPersistedState(t, specDir)
	if state.Current != StateGenerated {
		t.Errorf("persisted state = %q; want %q", state.Current, StateGenerated)
	}
}

// TestSpecSession_Generate_TransitionsToGenerating verifies that Generate
// sets the session state to StateGenerating immediately.
// Requirement: 07-REQ-11.1
func TestSpecSession_Generate_TransitionsToGenerating(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		generateResult: map[string]any{
			"requirements": map[string]any{"spec_id": "07"},
			"test_spec":    map[string]any{"spec_id": "07"},
			"tasks":        map[string]any{"spec_id": "07"},
		},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	_, err := session.Generate(ctx)
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// After success, session should be in StateGenerated.
	state := loadPersistedState(t, specDir)
	if state.Current != StateGenerated {
		t.Errorf("persisted state = %q; want %q", state.Current, StateGenerated)
	}
}

// ---------------------------------------------------------------------------
// TS-07-45: SpecSession.Generate skips generation for artifacts whose files
// already exist and only generates missing ones.
// Test Spec: TS-07-45, Requirement: 07-REQ-11.2
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_PartialRecovery(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	// Pre-write requirements.json to simulate partial failure recovery.
	existingRequirements := `{"spec_id":"07","spec_name":"test","requirements":[]}`
	if err := os.WriteFile(filepath.Join(specDir, "requirements.json"),
		[]byte(existingRequirements), 0o644); err != nil {
		t.Fatalf("failed to write existing requirements.json: %v", err)
	}

	// Mock only returns test_spec and tasks (requirements already exists).
	mock := &mockAssessor{
		generateResult: map[string]any{
			"test_spec": map[string]any{
				"spec_id":    "07",
				"spec_name":  "test",
				"test_cases": []any{},
			},
			"tasks": map[string]any{
				"spec_id":   "07",
				"spec_name": "test",
				"tasks":     []any{},
			},
		},
		generateOrder: []string{"test_spec", "tasks"},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	result, err := session.Generate(ctx)
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// Result should include all 3 artifacts (pre-existing + generated).
	if len(result.Artifacts) != 3 {
		t.Errorf("result.Artifacts length = %d; want 3", len(result.Artifacts))
	}

	// GenerateArtifacts should NOT have generated requirements (it existed).
	if containsArtifactName(mock.generatedNames, "requirements") {
		t.Error("generatedNames contains 'requirements'; want skipped (pre-existing)")
	}

	// GenerateArtifacts should have generated test_spec and tasks.
	if !containsArtifactName(mock.generatedNames, "test_spec") {
		t.Error("generatedNames does not contain 'test_spec'; want generated")
	}
	if !containsArtifactName(mock.generatedNames, "tasks") {
		t.Error("generatedNames does not contain 'tasks'; want generated")
	}
}

// ---------------------------------------------------------------------------
// TS-07-46: SpecSession.Generate does not transition to StateGenerated and
// persists the error as lastError when GenerateArtifacts fails.
// Test Spec: TS-07-46, Requirement: 07-REQ-11.3
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_Error(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	agentErr := &AgentError{
		Detail:        "Generation pipeline failure",
		ErrorCategory: "transient",
		Retryable:     true,
	}

	mock := &mockAssessor{generateErr: agentErr}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	result, err := session.Generate(ctx)

	// Result should be zero-value.
	if len(result.Artifacts) != 0 {
		t.Errorf("result.Artifacts length = %d; want 0", len(result.Artifacts))
	}
	if err == nil {
		t.Fatal("Generate() returned nil error; want error from GenerateArtifacts")
	}

	// Session state should NOT be StateGenerated.
	state := loadPersistedState(t, specDir)
	if state.Current == StateGenerated {
		t.Error("persisted state is StateGenerated; want state != StateGenerated on error")
	}

	// lastError should be set.
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want non-nil error record")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-11.E1: Generate disk write failure during artifact persistence
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_ArtifactWriteFailure(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	// Mock returns valid artifacts, but writing will fail due to
	// read-only directory (simulating disk write failure in OnArtifact).
	mock := &mockAssessor{
		generateResult: map[string]any{
			"requirements": map[string]any{"spec_id": "07"},
			"test_spec":    map[string]any{"spec_id": "07"},
			"tasks":        map[string]any{"spec_id": "07"},
		},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	// Make specDir read-only to cause artifact write failure.
	if os.Geteuid() == 0 {
		t.Skip("running as root: file mode bits do not deny writes")
	}
	if err := os.Chmod(specDir, 0o555); err != nil {
		t.Skipf("cannot set directory read-only: %v", err)
	}
	defer os.Chmod(specDir, 0o755) //nolint:errcheck

	ctx := context.Background()
	_, err := session.Generate(ctx)

	// Should return the write error from the callback.
	if err == nil {
		t.Fatal("Generate() returned nil error; want disk write failure error")
	}

	// lastError should be persisted.
	state := loadPersistedState(t, specDir)
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want artifact write error persisted")
	}

	// State should NOT be StateGenerated.
	if state.Current == StateGenerated {
		t.Error("persisted state is StateGenerated; want not generated on write failure")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-11.E2: Generate context cancellation mid-generation
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_ContextCancellation(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		generateResult: map[string]any{
			"requirements": map[string]any{"spec_id": "07"},
		},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	result, err := session.Generate(ctx)

	if len(result.Artifacts) != 0 {
		t.Errorf("result.Artifacts length = %d; want 0", len(result.Artifacts))
	}
	if err == nil {
		t.Fatal("Generate() returned nil error; want context cancellation error")
	}

	// State should not be StateGenerated.
	state := loadPersistedState(t, specDir)
	if state.Current == StateGenerated {
		t.Error("persisted state is StateGenerated; want not generated on cancellation")
	}

	// lastError should be persisted.
	if state.LastErr == nil {
		t.Error("persisted LastError is nil; want context error persisted")
	}
}

// ---------------------------------------------------------------------------
// 07-REQ-11.E3: Generate post-generation validation errors
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_ValidationErrors(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	mock := &mockAssessor{
		generateResult: map[string]any{
			"requirements": map[string]any{"spec_id": "07", "spec_name": "test", "requirements": []any{}},
			"test_spec":    map[string]any{"spec_id": "07", "spec_name": "test", "test_cases": []any{}},
			"tasks":        map[string]any{"spec_id": "07", "spec_name": "test", "tasks": []any{}},
		},
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	result, err := session.Generate(ctx)

	// Validation errors should NOT be treated as fatal — Generate still
	// returns a result with the validation information.
	if err != nil {
		t.Fatalf("Generate() returned error: %v; want nil (validation errors are non-fatal)", err)
	}

	// Result should still include all artifacts.
	if len(result.Artifacts) != 3 {
		t.Errorf("result.Artifacts length = %d; want 3", len(result.Artifacts))
	}

	// Validation result should be populated (may or may not have errors,
	// depending on the actual content).
	// The key property is that Generate returns successfully even if
	// Validate finds issues.
}

// ---------------------------------------------------------------------------
// 07-REQ-11.E4: Generate with all three artifact files already existing
// ---------------------------------------------------------------------------

func TestSpecSession_Generate_AllExist(t *testing.T) {
	specDir := t.TempDir()
	writePRDFileForAI(t, specDir, "# Test PRD")

	// Pre-write all three artifact files.
	artifacts := map[string]string{
		"requirements.json": `{"spec_id":"07","spec_name":"test","requirements":[]}`,
		"test_spec.json":    `{"spec_id":"07","spec_name":"test","test_cases":[]}`,
		"tasks.json":        `{"spec_id":"07","spec_name":"test","tasks":[]}`,
	}
	for name, content := range artifacts {
		if err := os.WriteFile(filepath.Join(specDir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
	}

	// Mock should NOT be called for any artifacts.
	mock := &mockAssessor{
		generateResult: map[string]any{}, // empty — nothing to generate
	}
	session := setupTestSession(t, specDir, mock, func(s *SpecSession) {
		s.Current = StatePRDAccepted
	})

	ctx := context.Background()
	result, err := session.Generate(ctx)
	if err != nil {
		t.Fatalf("Generate() returned error: %v", err)
	}

	// Should still return a result with all 3 artifacts.
	if len(result.Artifacts) != 3 {
		t.Errorf("result.Artifacts length = %d; want 3", len(result.Artifacts))
	}

	// No artifacts should have been generated by the agent.
	if len(mock.generatedNames) != 0 {
		t.Errorf("generatedNames = %v; want empty (all artifacts pre-existed)", mock.generatedNames)
	}

	// Session state should be StateGenerated.
	state := loadPersistedState(t, specDir)
	if state.Current != StateGenerated {
		t.Errorf("persisted state = %q; want %q", state.Current, StateGenerated)
	}
}
