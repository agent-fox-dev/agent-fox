package agentspec

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeSessionFile is a test helper that writes a _session.json file to
// specDir with the given JSON content string.
func writeSessionFile(t *testing.T, specDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(specDir, "_session.json"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write _session.json: %v", err)
	}
}

// --- CreateSession tests (06-REQ-7.1, 06-REQ-7.E1, 06-REQ-7.E3) ---

// TestTS06_22_CreateSessionInit verifies that CreateSession initializes a
// SpecSession in StateInit with empty history and persists it to
// _session.json atomically.
// Test Spec: TS-06-22, Requirement: 06-REQ-7.1
func TestTS06_22_CreateSessionInit(t *testing.T) {
	specDir := t.TempDir()

	session, err := CreateSession(specDir, "standard", "docs/prds/test.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("CreateSession() returned nil session; want non-nil")
	}
	if session.State() != StateInit {
		t.Errorf("session.State() = %q; want %q", session.State(), StateInit)
	}

	// Verify _session.json was written to disk.
	sessionPath := filepath.Join(specDir, "_session.json")
	data, readErr := os.ReadFile(sessionPath)
	if readErr != nil {
		t.Fatalf("_session.json not found at %s: %v", sessionPath, readErr)
	}

	// Parse the JSON and verify required fields.
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}

	if state, ok := parsed["state"].(string); !ok || state != "init" {
		t.Errorf("_session.json state = %v; want %q", parsed["state"], "init")
	}

	// assessment_history should be an empty array.
	ah, ok := parsed["assessment_history"].([]any)
	if !ok {
		t.Fatalf("_session.json assessment_history is not an array; got %T", parsed["assessment_history"])
	}
	if len(ah) != 0 {
		t.Errorf("_session.json assessment_history has %d entries; want 0", len(ah))
	}

	// qa_exchanges should be an empty array.
	qa, ok := parsed["qa_exchanges"].([]any)
	if !ok {
		t.Fatalf("_session.json qa_exchanges is not an array; got %T", parsed["qa_exchanges"])
	}
	if len(qa) != 0 {
		t.Errorf("_session.json qa_exchanges has %d entries; want 0", len(qa))
	}

	// generated_artifacts should be an empty array.
	ga, ok := parsed["generated_artifacts"].([]any)
	if !ok {
		t.Fatalf("_session.json generated_artifacts is not an array; got %T", parsed["generated_artifacts"])
	}
	if len(ga) != 0 {
		t.Errorf("_session.json generated_artifacts has %d entries; want 0", len(ga))
	}
}

// TestCreateSession_PersistsSnakeCaseFields verifies that _session.json
// contains the expected snake_case field names: state, prd_path, mode,
// assessment_history, qa_exchanges, generated_artifacts.
// Requirement: 06-REQ-7.1
func TestCreateSession_PersistsSnakeCaseFields(t *testing.T) {
	specDir := t.TempDir()

	session, err := CreateSession(specDir, "standard", "docs/prds/test.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("CreateSession() returned nil session; want non-nil")
	}

	sessionPath := filepath.Join(specDir, "_session.json")
	data, readErr := os.ReadFile(sessionPath)
	if readErr != nil {
		t.Fatalf("_session.json not found: %v", readErr)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}

	requiredKeys := []string{"state", "mode", "prd_path", "assessment_history", "qa_exchanges", "generated_artifacts"}
	for _, key := range requiredKeys {
		if _, ok := parsed[key]; !ok {
			t.Errorf("_session.json missing required key %q", key)
		}
	}
}

// TestCreateSession_EmptySpecDir verifies that CreateSession returns an
// error without creating any files when specDir is an empty string.
// Edge Case: 06-REQ-7.E3
func TestCreateSession_EmptySpecDir(t *testing.T) {
	session, err := CreateSession("", "standard", "prd.md")
	if session != nil {
		t.Errorf("CreateSession(\"\") returned non-nil session; want nil")
	}
	if err == nil {
		t.Error("CreateSession(\"\") returned nil error; want non-nil error")
	}
}

// TestCreateSession_NonexistentSpecDir verifies that CreateSession returns
// an error without creating any files when specDir does not exist.
// Edge Case: 06-REQ-7.E3
func TestCreateSession_NonexistentSpecDir(t *testing.T) {
	session, err := CreateSession("/tmp/nonexistent_session_dir_06_test", "standard", "prd.md")
	if session != nil {
		t.Errorf("CreateSession(nonexistent) returned non-nil session; want nil")
	}
	if err == nil {
		t.Error("CreateSession(nonexistent) returned nil error; want non-nil error")
	}

	// Verify no _session.json was created.
	sessionPath := filepath.Join("/tmp/nonexistent_session_dir_06_test", "_session.json")
	if _, statErr := os.Stat(sessionPath); statErr == nil {
		t.Error("_session.json should not exist for nonexistent specDir")
		os.Remove(sessionPath)
	}
}

// --- ResumeSession tests (06-REQ-7.2, 06-REQ-7.3, 06-REQ-7.4, 06-REQ-7.E2) ---

// TestTS06_23_ResumeSessionReconstructsFields verifies that ResumeSession
// reads _session.json and reconstructs the SpecSession with all persisted
// fields: state, mode, prd_path, assessment_history.
// Test Spec: TS-06-23, Requirement: 06-REQ-7.2
func TestTS06_23_ResumeSessionReconstructsFields(t *testing.T) {
	specDir := t.TempDir()

	// Write _session.json with known content matching the test spec preconditions.
	sessionJSON := `{
		"state": "assessing",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [
			{"quality": "ready", "summary": "ok", "gaps": [], "questions": []}
		],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil session; want non-nil")
	}
	if session.State() != StateAssessing {
		t.Errorf("session.State() = %q; want %q", session.State(), StateAssessing)
	}
	if session.SpecDir() != specDir {
		t.Errorf("session.SpecDir() = %q; want %q", session.SpecDir(), specDir)
	}

	// Verify assessment is reconstructed.
	a := session.Assessment()
	if a == nil {
		t.Fatal("session.Assessment() returned nil; want non-nil")
	}
	if a.Quality != "ready" {
		t.Errorf("Assessment().Quality = %q; want %q", a.Quality, "ready")
	}
}

// TestResumeSession_RoundTrip verifies that CreateSession followed by
// ResumeSession produces a SpecSession with identical fields (06-PROP-10).
// Correctness Property: 06-PROP-10
func TestResumeSession_RoundTrip(t *testing.T) {
	specDir := t.TempDir()

	original, err := CreateSession(specDir, "standard", "docs/prds/test.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}
	if original == nil {
		t.Fatal("CreateSession() returned nil session; want non-nil")
	}

	resumed, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() returned error: %v", err)
	}
	if resumed == nil {
		t.Fatal("ResumeSession() returned nil session; want non-nil")
	}

	if resumed.State() != original.State() {
		t.Errorf("resumed.State() = %q; want %q", resumed.State(), original.State())
	}
	if resumed.Mode != original.Mode {
		t.Errorf("resumed.Mode = %q; want %q", resumed.Mode, original.Mode)
	}
	if resumed.PRDPath != original.PRDPath {
		t.Errorf("resumed.PRDPath = %q; want %q", resumed.PRDPath, original.PRDPath)
	}
}

// TestTS06_24_ResumeSessionMissingFile verifies that ResumeSession returns
// a SessionError when _session.json does not exist in the spec directory.
// Test Spec: TS-06-24, Requirement: 06-REQ-7.3
func TestTS06_24_ResumeSessionMissingFile(t *testing.T) {
	specDir := t.TempDir() // empty directory, no _session.json

	session, err := ResumeSession(specDir)
	if session != nil {
		t.Errorf("ResumeSession() returned non-nil session; want nil")
	}

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if se.Category() != "state" {
		t.Errorf("SessionError.Category() = %q; want %q", se.Category(), "state")
	}
}

// TestTS06_25_ResumeSessionMalformedJSON verifies that ResumeSession returns
// a SessionError wrapping the parse error when _session.json contains
// malformed JSON.
// Test Spec: TS-06-25, Requirement: 06-REQ-7.4
func TestTS06_25_ResumeSessionMalformedJSON(t *testing.T) {
	specDir := t.TempDir()

	writeSessionFile(t, specDir, "{invalid json")

	session, err := ResumeSession(specDir)
	if session != nil {
		t.Errorf("ResumeSession() returned non-nil session; want nil")
	}

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if se.Category() != "state" {
		t.Errorf("SessionError.Category() = %q; want %q", se.Category(), "state")
	}

	// The SessionError should wrap the underlying JSON parse error.
	if errors.Unwrap(se) == nil {
		t.Error("SessionError.Unwrap() returned nil; want wrapped parse error")
	}
}

// TestResumeSession_UnrecognizedState verifies that ResumeSession returns
// a SessionError when _session.json contains a state value not in the
// defined SessionState constants.
// Edge Case: 06-REQ-7.E2
func TestResumeSession_UnrecognizedState(t *testing.T) {
	specDir := t.TempDir()

	sessionJSON := `{
		"state": "totally_unknown_state",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if session != nil {
		t.Errorf("ResumeSession() returned non-nil session; want nil")
	}

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if se.Category() != "state" {
		t.Errorf("SessionError.Category() = %q; want %q", se.Category(), "state")
	}
}

// --- AcceptPRD tests (06-REQ-8.1, 06-REQ-8.2, 06-REQ-8.E1) ---

// TestTS06_26_AcceptPRDFromAssessing verifies that SpecSession.AcceptPRD
// transitions state to StatePRDAccepted and persists the new state to
// _session.json when called from StateAssessing.
// Test Spec: TS-06-26, Requirement: 06-REQ-8.1
func TestTS06_26_AcceptPRDFromAssessing(t *testing.T) {
	specDir := t.TempDir()

	// Write initial _session.json with state=assessing.
	sessionJSON := `{
		"state": "assessing",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [
			{"quality": "ready", "summary": "ok", "gaps": [], "questions": []}
		],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() setup error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want session in StateAssessing")
	}

	// Call AcceptPRD.
	err = session.AcceptPRD()
	if err != nil {
		t.Fatalf("AcceptPRD() returned error: %v", err)
	}
	if session.State() != StatePRDAccepted {
		t.Errorf("session.State() = %q; want %q", session.State(), StatePRDAccepted)
	}

	// Verify _session.json was updated on disk.
	data, readErr := os.ReadFile(filepath.Join(specDir, "_session.json"))
	if readErr != nil {
		t.Fatalf("failed to read _session.json: %v", readErr)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}
	if state, ok := parsed["state"].(string); !ok || state != "prd_accepted" {
		t.Errorf("_session.json state = %v; want %q", parsed["state"], "prd_accepted")
	}
}

// TestAcceptPRD_FromRefining verifies that AcceptPRD also succeeds when
// called from StateRefining, transitioning to StatePRDAccepted.
// Requirement: 06-REQ-8.1
func TestAcceptPRD_FromRefining(t *testing.T) {
	specDir := t.TempDir()

	sessionJSON := `{
		"state": "refining",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() setup error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want session in StateRefining")
	}

	err = session.AcceptPRD()
	if err != nil {
		t.Fatalf("AcceptPRD() returned error: %v", err)
	}
	if session.State() != StatePRDAccepted {
		t.Errorf("session.State() = %q; want %q", session.State(), StatePRDAccepted)
	}

	// Verify _session.json reflects the new state.
	data, readErr := os.ReadFile(filepath.Join(specDir, "_session.json"))
	if readErr != nil {
		t.Fatalf("failed to read _session.json: %v", readErr)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}
	if state, ok := parsed["state"].(string); !ok || state != "prd_accepted" {
		t.Errorf("_session.json state = %v; want %q", parsed["state"], "prd_accepted")
	}
}

// TestTS06_27_AcceptPRDFromInitFails verifies that SpecSession.AcceptPRD
// returns a SessionError without modifying state or _session.json when
// called from StateInit.
// Test Spec: TS-06-27, Requirement: 06-REQ-8.2
func TestTS06_27_AcceptPRDFromInitFails(t *testing.T) {
	specDir := t.TempDir()

	// Write initial _session.json with state=init.
	sessionJSON := `{
		"state": "init",
		"mode": "standard",
		"prd_path": "prd.md",
		"assessment_history": [],
		"qa_exchanges": [],
		"generated_artifacts": [],
		"last_error": null
	}`
	writeSessionFile(t, specDir, sessionJSON)

	session, err := ResumeSession(specDir)
	if err != nil {
		t.Fatalf("ResumeSession() setup error: %v", err)
	}
	if session == nil {
		t.Fatal("ResumeSession() returned nil; want session in StateInit")
	}

	err = session.AcceptPRD()

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if se.Category() != "state" {
		t.Errorf("SessionError.Category() = %q; want %q", se.Category(), "state")
	}

	// Verify state was not changed.
	if session.State() != StateInit {
		t.Errorf("session.State() = %q; want %q (unchanged)", session.State(), StateInit)
	}

	// Verify _session.json was not modified.
	data, readErr := os.ReadFile(filepath.Join(specDir, "_session.json"))
	if readErr != nil {
		t.Fatalf("failed to read _session.json: %v", readErr)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to parse _session.json: %v", err)
	}
	if state, ok := parsed["state"].(string); !ok || state != "init" {
		t.Errorf("_session.json state = %v; want %q (unchanged)", parsed["state"], "init")
	}
}

// TestAcceptPRD_FromGenerating verifies that AcceptPRD returns a
// SessionError when called from StateGenerating.
// Requirement: 06-REQ-8.2
func TestAcceptPRD_FromGenerating(t *testing.T) {
	session := &SpecSession{
		specDir: t.TempDir(),
		Current: StateGenerating,
	}

	err := session.AcceptPRD()

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if session.State() != StateGenerating {
		t.Errorf("session.State() = %q; want %q (unchanged)", session.State(), StateGenerating)
	}
}

// TestAcceptPRD_FromGenerated verifies that AcceptPRD returns a
// SessionError when called from StateGenerated.
// Requirement: 06-REQ-8.2
func TestAcceptPRD_FromGenerated(t *testing.T) {
	session := &SpecSession{
		specDir: t.TempDir(),
		Current: StateGenerated,
	}

	err := session.AcceptPRD()

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if session.State() != StateGenerated {
		t.Errorf("session.State() = %q; want %q (unchanged)", session.State(), StateGenerated)
	}
}

// TestAcceptPRD_FromPRDAccepted verifies that AcceptPRD returns a
// SessionError when called from StatePRDAccepted (already accepted).
// Requirement: 06-REQ-8.2
func TestAcceptPRD_FromPRDAccepted(t *testing.T) {
	session := &SpecSession{
		specDir: t.TempDir(),
		Current: StatePRDAccepted,
	}

	err := session.AcceptPRD()

	var se *SessionError
	if !errors.As(err, &se) {
		t.Fatalf("error type = %T; want *SessionError", err)
	}
	if session.State() != StatePRDAccepted {
		t.Errorf("session.State() = %q; want %q (unchanged)", session.State(), StatePRDAccepted)
	}
}

// TestAcceptPRD_PersistenceFailureRevertsState verifies that when the
// atomic persistence of the state transition to _session.json fails,
// AcceptPRD reverts the in-memory state to its pre-transition value.
// Edge Case: 06-REQ-8.E1, Correctness Property: 06-PROP-5
func TestAcceptPRD_PersistenceFailureRevertsState(t *testing.T) {
	// Use a specDir that does not exist on disk — writing _session.json
	// will fail, triggering the revert path.
	specDir := filepath.Join(t.TempDir(), "nonexistent_subdir")

	session := &SpecSession{
		specDir: specDir,
		Current: StateAssessing,
	}

	err := session.AcceptPRD()
	if err == nil {
		t.Fatal("AcceptPRD() returned nil error; want error due to persistence failure")
	}

	// The in-memory state should be reverted to the pre-transition value.
	if session.State() != StateAssessing {
		t.Errorf("session.State() = %q; want %q (reverted)", session.State(), StateAssessing)
	}
}

// --- State accessor tests (06-REQ-8.3) ---

// TestTS06_28_StateReturnsCurrentState verifies that SpecSession.State()
// returns the current SessionState of the session at any point in time.
// Test Spec: TS-06-28, Requirement: 06-REQ-8.3
func TestTS06_28_StateReturnsCurrentState(t *testing.T) {
	specDir := t.TempDir()

	session, err := CreateSession(specDir, "standard", "prd.md")
	if err != nil {
		t.Fatalf("CreateSession() returned error: %v", err)
	}
	if session == nil {
		t.Fatal("CreateSession() returned nil session; want non-nil")
	}
	if session.State() != StateInit {
		t.Errorf("session.State() = %q; want %q", session.State(), StateInit)
	}
}

// TestState_AllValidStates verifies that State() correctly returns each
// valid SessionState value when set directly on the struct.
// Requirement: 06-REQ-8.3
func TestState_AllValidStates(t *testing.T) {
	states := []SessionState{
		StateInit,
		StateAssessing,
		StateRefining,
		StatePRDAccepted,
		StateGenerating,
		StateGenerated,
	}

	for _, want := range states {
		t.Run(string(want), func(t *testing.T) {
			session := &SpecSession{Current: want}
			if got := session.State(); got != want {
				t.Errorf("State() = %q; want %q", got, want)
			}
		})
	}
}

// --- Assessment accessor tests (06-REQ-8.4, 06-REQ-8.5) ---

// TestTS06_29_AssessmentReturnsLatest verifies that SpecSession.Assessment()
// returns a pointer to the most recent Assessment when assessmentHistory
// is non-empty (with two entries).
// Test Spec: TS-06-29, Requirement: 06-REQ-8.4
func TestTS06_29_AssessmentReturnsLatest(t *testing.T) {
	session := &SpecSession{
		Current: StateAssessing,
		AssessmentHistory: []Assessment{
			{Quality: "needs_refinement", Summary: "first", Gaps: []string{"gap1"}, Questions: nil},
			{Quality: "ready", Summary: "second", Gaps: nil, Questions: nil},
		},
	}

	a := session.Assessment()
	if a == nil {
		t.Fatal("Assessment() returned nil; want non-nil pointer to last assessment")
	}
	if a.Quality != "ready" {
		t.Errorf("Assessment().Quality = %q; want %q", a.Quality, "ready")
	}
	if a.Summary != "second" {
		t.Errorf("Assessment().Summary = %q; want %q", a.Summary, "second")
	}
}

// TestTS06_30_AssessmentReturnsNilWhenEmpty verifies that
// SpecSession.Assessment() returns nil when assessmentHistory is empty.
// Test Spec: TS-06-30, Requirement: 06-REQ-8.5
func TestTS06_30_AssessmentReturnsNilWhenEmpty(t *testing.T) {
	session := &SpecSession{
		Current:           StateInit,
		AssessmentHistory: []Assessment{},
	}

	a := session.Assessment()
	if a != nil {
		t.Errorf("Assessment() = %+v; want nil for empty assessmentHistory", a)
	}
}

// TestAssessment_SingleEntry verifies that Assessment() returns the
// single entry when assessmentHistory has exactly one item.
// Requirement: 06-REQ-8.4
func TestAssessment_SingleEntry(t *testing.T) {
	session := &SpecSession{
		Current: StateAssessing,
		AssessmentHistory: []Assessment{
			{Quality: "needs_refinement", Summary: "only one"},
		},
	}

	a := session.Assessment()
	if a == nil {
		t.Fatal("Assessment() returned nil; want non-nil pointer")
	}
	if a.Quality != "needs_refinement" {
		t.Errorf("Assessment().Quality = %q; want %q", a.Quality, "needs_refinement")
	}
}

// --- PendingQuestions tests (06-REQ-8.E2, 06-REQ-8.E3) ---

// TestPendingQuestions_EmptyHistory verifies that PendingQuestions()
// returns an empty slice when assessmentHistory is empty.
// Edge Case: 06-REQ-8.E3
func TestPendingQuestions_EmptyHistory(t *testing.T) {
	session := &SpecSession{
		Current:           StateInit,
		AssessmentHistory: []Assessment{},
	}

	pq := session.PendingQuestions()
	if pq == nil {
		t.Fatal("PendingQuestions() returned nil; want non-nil empty slice")
	}
	if len(pq) != 0 {
		t.Errorf("len(PendingQuestions()) = %d; want 0", len(pq))
	}
}

// TestPendingQuestions_NoQuestions verifies that PendingQuestions() returns
// an empty slice when the latest Assessment has no questions.
// Edge Case: 06-REQ-8.E2
func TestPendingQuestions_NoQuestions(t *testing.T) {
	session := &SpecSession{
		Current: StateAssessing,
		AssessmentHistory: []Assessment{
			{Quality: "ready", Summary: "all good", Gaps: nil, Questions: []map[string]any{}},
		},
	}

	pq := session.PendingQuestions()
	if pq == nil {
		t.Fatal("PendingQuestions() returned nil; want non-nil empty slice")
	}
	if len(pq) != 0 {
		t.Errorf("len(PendingQuestions()) = %d; want 0", len(pq))
	}
}

// --- SpecDir accessor test ---

// TestSpecDir_ReturnsSetPath verifies that SpecDir() returns the spec
// directory path set during session construction.
// Requirement: 06-REQ-7.2 (implied by ResumeSession contract)
func TestSpecDir_ReturnsSetPath(t *testing.T) {
	specDir := "/some/spec/path"
	session := &SpecSession{specDir: specDir}

	if got := session.SpecDir(); got != specDir {
		t.Errorf("SpecDir() = %q; want %q", got, specDir)
	}
}

// --- loadSiblingLandscape tests (Issue #82) ---

// writeSiblingPRD is a test helper that creates a spec directory under
// parentDir named dirName and writes a minimal prd.md with the given
// spec_id, spec_name, and status in the YAML frontmatter.
func writeSiblingPRD(t *testing.T, parentDir, dirName, specID, specName, status string) {
	t.Helper()
	dir := filepath.Join(parentDir, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("failed to create sibling dir %s: %v", dir, err)
	}
	content := "---\n" +
		"spec_id: \"" + specID + "\"\n" +
		"spec_name: \"" + specName + "\"\n" +
		"title: \"Test\"\n" +
		"status: \"" + status + "\"\n" +
		"created_at: \"2026-01-01T00:00:00Z\"\n" +
		"updated_at: \"2026-01-01T00:00:00Z\"\n" +
		"owner: \"tester\"\n" +
		"source: \"https://example.com\"\n" +
		"supersedes: []\n" +
		"tags: []\n" +
		"intent_hash: null\n" +
		"schema_version: 1\n" +
		"---\n# Test\n"
	if err := os.WriteFile(filepath.Join(dir, "prd.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write prd.md in %s: %v", dir, err)
	}
}

// TestLoadSiblingLandscape_DraftStatus verifies that a sibling with
// status: draft in its prd.md frontmatter is reported with "draft" in
// the landscape, not "active".
// Test Spec: TS-NS-1, Requirement: NS-REQ-1
func TestLoadSiblingLandscape_DraftStatus(t *testing.T) {
	parent := t.TempDir()

	// Create current spec dir (excluded from results).
	currentDir := filepath.Join(parent, "01_current")
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("failed to create current dir: %v", err)
	}

	// Create a sibling with status: draft.
	writeSiblingPRD(t, parent, "02_sibling", "02", "sibling", "draft")

	session := &SpecSession{specDir: currentDir}
	landscape := session.loadSiblingLandscape()

	if len(landscape) != 1 {
		t.Fatalf("loadSiblingLandscape() returned %d entries; want 1", len(landscape))
	}
	if got := landscape[0]["status"]; got != "draft" {
		t.Errorf("landscape[0][\"status\"] = %q; want %q", got, "draft")
	}
}

// TestLoadSiblingLandscape_SealedStatus verifies that a sibling with
// status: sealed in its prd.md frontmatter is reported with "sealed" in
// the landscape.
// Test Spec: TS-NS-2, Requirement: NS-REQ-2
func TestLoadSiblingLandscape_SealedStatus(t *testing.T) {
	parent := t.TempDir()

	currentDir := filepath.Join(parent, "01_current")
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("failed to create current dir: %v", err)
	}

	writeSiblingPRD(t, parent, "02_sibling", "02", "sibling", "sealed")

	session := &SpecSession{specDir: currentDir}
	landscape := session.loadSiblingLandscape()

	if len(landscape) != 1 {
		t.Fatalf("loadSiblingLandscape() returned %d entries; want 1", len(landscape))
	}
	if got := landscape[0]["status"]; got != "sealed" {
		t.Errorf("landscape[0][\"status\"] = %q; want %q", got, "sealed")
	}
}

// TestLoadSiblingLandscape_ActiveStatus verifies that a sibling with
// status: active in its prd.md frontmatter is reported with "active" in
// the landscape.
// Test Spec: TS-NS-3, Requirement: NS-REQ-3
func TestLoadSiblingLandscape_ActiveStatus(t *testing.T) {
	parent := t.TempDir()

	currentDir := filepath.Join(parent, "01_current")
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("failed to create current dir: %v", err)
	}

	writeSiblingPRD(t, parent, "02_sibling", "02", "sibling", "active")

	session := &SpecSession{specDir: currentDir}
	landscape := session.loadSiblingLandscape()

	if len(landscape) != 1 {
		t.Fatalf("loadSiblingLandscape() returned %d entries; want 1", len(landscape))
	}
	if got := landscape[0]["status"]; got != "active" {
		t.Errorf("landscape[0][\"status\"] = %q; want %q", got, "active")
	}
}

// TestLoadSiblingLandscape_ExcludesCurrentSpec verifies that the current
// spec's directory is not included in the returned landscape.
// Test Spec: TS-NS-4, Requirement: NS-REQ-4
func TestLoadSiblingLandscape_ExcludesCurrentSpec(t *testing.T) {
	parent := t.TempDir()

	// Current spec dir: 01_foo
	writeSiblingPRD(t, parent, "01_foo", "01", "foo", "active")
	currentDir := filepath.Join(parent, "01_foo")

	// Sibling: 02_bar
	writeSiblingPRD(t, parent, "02_bar", "02", "bar", "active")

	session := &SpecSession{specDir: currentDir}
	landscape := session.loadSiblingLandscape()

	// Only 02_bar should appear; 01_foo (current) must not.
	if len(landscape) != 1 {
		t.Fatalf("loadSiblingLandscape() returned %d entries; want 1", len(landscape))
	}
	for _, entry := range landscape {
		if entry["spec_id"] == "01" {
			t.Errorf("landscape contains current spec entry (spec_id %q); it must be excluded", "01")
		}
	}
	if got := landscape[0]["spec_id"]; got != "02" {
		t.Errorf("landscape[0][\"spec_id\"] = %q; want %q", got, "02")
	}
}

// TestLoadSiblingLandscape_SkipsMissingPRD verifies that a sibling
// directory whose prd.md is missing is silently skipped while valid
// siblings are still returned.
// Test Spec: TS-NS-5, Requirement: NS-REQ-5
func TestLoadSiblingLandscape_SkipsMissingPRD(t *testing.T) {
	parent := t.TempDir()

	currentDir := filepath.Join(parent, "01_current")
	if err := os.MkdirAll(currentDir, 0o755); err != nil {
		t.Fatalf("failed to create current dir: %v", err)
	}

	// Valid sibling.
	writeSiblingPRD(t, parent, "02_good", "02", "good", "active")

	// Sibling directory with no prd.md.
	badDir := filepath.Join(parent, "03_bad")
	if err := os.MkdirAll(badDir, 0o755); err != nil {
		t.Fatalf("failed to create bad sibling dir: %v", err)
	}

	session := &SpecSession{specDir: currentDir}
	landscape := session.loadSiblingLandscape()

	if len(landscape) != 1 {
		t.Fatalf("loadSiblingLandscape() returned %d entries; want 1 (only the valid sibling)", len(landscape))
	}
	if got := landscape[0]["spec_id"]; got != "02" {
		t.Errorf("landscape[0][\"spec_id\"] = %q; want %q", got, "02")
	}
}
