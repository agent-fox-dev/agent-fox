package agentspec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// validSessionStates enumerates all recognized SessionState values.
var validSessionStates = map[SessionState]bool{
	StateInit:        true,
	StateAssessing:   true,
	StateRefining:    true,
	StatePRDAccepted: true,
	StateGenerating:  true,
	StateGenerated:   true,
}

// SessionState enumerates the valid states of a SpecSession.
type SessionState string

const (
	StateInit        SessionState = "init"
	StateAssessing   SessionState = "assessing"
	StateRefining    SessionState = "refining"
	StatePRDAccepted SessionState = "prd_accepted"
	StateGenerating  SessionState = "generating"
	StateGenerated   SessionState = "generated"
)

// Assessment captures PRD quality assessment output: Quality, Summary,
// Gaps, and Questions.
type Assessment struct {
	Quality   string           `json:"quality"`
	Summary   string           `json:"summary"`
	Gaps      []string         `json:"gaps"`
	Questions []map[string]any `json:"questions"`
}

// QAExchange records a set of answers to assessment questions at a
// point in time, including the assessment index the answers correspond
// to and a UTC timestamp.
type QAExchange struct {
	AssessmentIndex int               `json:"assessment_index"`
	Answers         map[string]string `json:"answers"`
	Timestamp       time.Time         `json:"timestamp"`
}

// LastError records the most recent error encountered during a session
// operation.
type LastError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// assessor abstracts the SpecAgent AI pipeline for testability.
// SpecAgent satisfies this interface; tests provide a mock.
type assessor interface {
	AssessPRD(ctx context.Context, prdText, specName string, opts ...AgentOption) (Assessment, error)
	RefinePRD(ctx context.Context, prdText string, answers map[string]string, prevAssessment Assessment, opts ...AgentOption) (string, Assessment, error)
	GenerateArtifacts(ctx context.Context, prdText, specID, specName string, opts ...AgentOption) (map[string]any, error)
}

// SpecSession represents the stateful session for authoring a single spec,
// persisted to _session.json inside a spec directory.
type SpecSession struct {
	specDir            string
	agent              assessor
	runOpts            *RunOptions
	Current            SessionState `json:"state"`
	Mode               string       `json:"mode"`
	PRDPath            string       `json:"prd_path"`
	AssessmentHistory  []Assessment `json:"assessment_history"`
	QAExchanges        []QAExchange `json:"qa_exchanges"`
	GeneratedArtifacts []string     `json:"generated_artifacts"`
	LastErr            *LastError   `json:"last_error"`
}

// State returns the current SessionState of the session.
func (s *SpecSession) State() SessionState {
	return s.Current
}

// SpecDir returns the spec directory path associated with this session.
func (s *SpecSession) SpecDir() string {
	return s.specDir
}

// Assessment returns a pointer to the most recent Assessment in the
// session's assessment history, or nil if the history is empty.
func (s *SpecSession) Assessment() *Assessment {
	if len(s.AssessmentHistory) == 0 {
		return nil
	}
	return &s.AssessmentHistory[len(s.AssessmentHistory)-1]
}

// AcceptPRD transitions the session state to StatePRDAccepted and
// persists the updated state to _session.json atomically. Returns a
// SessionError if the current state is not StateAssessing or StateRefining.
// If the atomic persistence fails, the in-memory state is reverted.
func (s *SpecSession) AcceptPRD() error {
	if s.Current != StateAssessing && s.Current != StateRefining {
		return &SessionError{
			Msg: fmt.Sprintf("cannot accept PRD from state %q; must be %q or %q",
				s.Current, StateAssessing, StateRefining),
		}
	}

	prevState := s.Current
	s.Current = StatePRDAccepted

	if err := s.persistSession(); err != nil {
		// Revert in-memory state on persistence failure.
		s.Current = prevState
		return err
	}
	return nil
}

// PendingQuestions returns the questions from the most recent Assessment
// that have not yet been answered via QA exchanges. Returns an empty
// slice if there are no pending questions or the assessment history is empty.
func (s *SpecSession) PendingQuestions() []map[string]any {
	a := s.Assessment()
	if a == nil || len(a.Questions) == 0 {
		return []map[string]any{}
	}
	return a.Questions
}

// persistSession writes the session state to _session.json inside specDir
// using the atomic temp-file-and-rename pattern. Returns a SessionError
// on failure and cleans up the temporary file.
func (s *SpecSession) persistSession() error {
	data, err := json.Marshal(s)
	if err != nil {
		return &SessionError{
			Msg:   fmt.Sprintf("failed to marshal session: %v", err),
			Cause: err,
		}
	}

	sessionPath := filepath.Join(s.specDir, "_session.json")
	tmpFile, err := os.CreateTemp(s.specDir, "_session.json.tmp.*")
	if err != nil {
		return &SessionError{
			Msg:   fmt.Sprintf("failed to create temp file for _session.json: %v", err),
			Cause: err,
		}
	}
	tmpPath := tmpFile.Name()

	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return &SessionError{
			Msg:   fmt.Sprintf("failed to write _session.json temp file: %v", err),
			Cause: err,
		}
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close()
		return &SessionError{
			Msg:   fmt.Sprintf("failed to sync _session.json temp file: %v", err),
			Cause: err,
		}
	}
	if err := tmpFile.Close(); err != nil {
		return &SessionError{
			Msg:   fmt.Sprintf("failed to close _session.json temp file: %v", err),
			Cause: err,
		}
	}

	if err := os.Rename(tmpPath, sessionPath); err != nil {
		return &SessionError{
			Msg:   fmt.Sprintf("failed to rename temp file to _session.json: %v", err),
			Cause: err,
		}
	}

	success = true
	return nil
}

// CreateSession initializes a new SpecSession in StateInit and persists
// it to _session.json inside specDir using the atomic temp-file-and-rename
// pattern. Returns a SessionError on failure.
func CreateSession(specDir, mode, source string) (*SpecSession, error) {
	if specDir == "" {
		return nil, &SessionError{Msg: "specDir must not be empty"}
	}

	// Validate that specDir exists.
	if _, err := os.Stat(specDir); err != nil {
		return nil, &SessionError{
			Msg:   fmt.Sprintf("specDir does not exist: %s", specDir),
			Cause: err,
		}
	}

	session := &SpecSession{
		specDir:            specDir,
		Current:            StateInit,
		Mode:               mode,
		PRDPath:            source,
		AssessmentHistory:  []Assessment{},
		QAExchanges:        []QAExchange{},
		GeneratedArtifacts: []string{},
	}

	if err := session.persistSession(); err != nil {
		return nil, err
	}

	return session, nil
}

// ResumeSession reads _session.json from specDir and reconstructs a
// SpecSession. Returns a SessionError if the file is missing or invalid.
func ResumeSession(specDir string) (*SpecSession, error) {
	sessionPath := filepath.Join(specDir, "_session.json")

	data, err := os.ReadFile(sessionPath)
	if err != nil {
		return nil, &SessionError{
			Msg:   fmt.Sprintf("failed to read _session.json from %s: %v", specDir, err),
			Cause: err,
		}
	}

	var session SpecSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, &SessionError{
			Msg:   fmt.Sprintf("failed to parse _session.json from %s: %v", specDir, err),
			Cause: err,
		}
	}

	// Validate that the state is a recognized SessionState.
	if !validSessionStates[session.Current] {
		return nil, &SessionError{
			Msg: fmt.Sprintf("unrecognized session state %q in _session.json", session.Current),
		}
	}

	session.specDir = specDir
	return &session, nil
}

// SessionValidationResult holds the result of validating artifacts in a
// spec session directory. Valid is true when no schema or integrity
// errors are found.
type SessionValidationResult struct {
	Valid             bool
	SchemaErrors      []string
	IntegrityErrors   []string
	RepairSuggestions []RepairSuggestion
}

// RepairSuggestion describes a suggested fix for a validation error,
// including whether it can be applied automatically.
type RepairSuggestion struct {
	Description string
	AutoFixable bool
}

// knownArtifacts lists the JSON artifact filenames that LoadSpec expects.
var knownArtifacts = []string{
	"requirements.json",
	"test_spec.json",
	"tasks.json",
}

// GenerateResult holds the result of artifact generation: the artifacts
// written, the validation result, and the advisory warnings.
//
// A GenerateResult is only ever returned for a spec that validates. Format v2
// §12.2 makes an invalid spec a failed generation, so Generate returns an
// error and removes what it wrote rather than reporting the violations here.
type GenerateResult struct {
	Artifacts  []string                `json:"artifacts"`
	Validation SessionValidationResult `json:"validation"`
	Warnings   []string                `json:"warnings"`
}

// Validate loads the spec from the session's spec directory and runs
// validation, categorizing errors as schema or integrity errors. If
// LoadSpec fails (e.g., missing artifacts), it falls back to loading
// individual JSON artifact files and validating those.
func (s *SpecSession) Validate() (SessionValidationResult, error) {
	// Try the happy path: load the full spec and validate.
	spec, loadErr := afspec.LoadSpec(s.specDir)
	if loadErr == nil {
		vr := spec.Validate()
		return categorizeValidationResult(vr), nil
	}

	// LoadSpec failed — fall back to individual artifact validation.
	return s.validateFallback()
}

// ValidateCrossFile loads the spec from the session's spec directory and runs
// only the cross-file integrity rules, skipping schema validation. If LoadSpec
// fails (missing or unreadable artifacts), it reports the affected artifacts
// as integrity errors.
func (s *SpecSession) ValidateCrossFile() (SessionValidationResult, error) {
	// Try the happy path: load the full spec and run cross-file checks only.
	spec, loadErr := afspec.LoadSpec(s.specDir)
	if loadErr == nil {
		vr := spec.ValidateCrossFile()
		return categorizeValidationResult(vr), nil
	}

	// LoadSpec failed — fall back to reporting missing or unreadable artifacts
	// as integrity errors.
	return s.crossFileFallback()
}

// crossFileFallback checks which known artifact files are present in the spec
// directory and reports any missing or unreadable artifacts as integrity
// errors. It does not run schema validation.
func (s *SpecSession) crossFileFallback() (SessionValidationResult, error) {
	result := SessionValidationResult{
		Valid:           false,
		SchemaErrors:    []string{},
		IntegrityErrors: []string{},
	}

	foundAny := false
	for _, artifactName := range knownArtifacts {
		artifactPath := filepath.Join(s.specDir, artifactName)
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			if os.IsNotExist(err) {
				result.IntegrityErrors = append(result.IntegrityErrors,
					fmt.Sprintf("missing artifact: %s", artifactName))
			} else {
				result.IntegrityErrors = append(result.IntegrityErrors,
					fmt.Sprintf("cannot read %s: %v", artifactName, err))
			}
			continue
		}
		foundAny = true
		// If the file exists but cannot be parsed as JSON, LoadSpec would have
		// failed for that reason. Report as an integrity error since cross-file
		// checks cannot proceed without parseable content.
		var raw json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			result.IntegrityErrors = append(result.IntegrityErrors,
				fmt.Sprintf("cannot parse %s for cross-file check: %v", artifactName, err))
		}
	}

	if !foundAny {
		result.IntegrityErrors = append(result.IntegrityErrors,
			"no artifact files found in spec directory")
	}

	return result, nil
}

// categorizeValidationResult converts an afspec.ValidationResult into a
// SessionValidationResult, splitting entries by category.
func categorizeValidationResult(vr afspec.ValidationResult) SessionValidationResult {
	result := SessionValidationResult{
		Valid:           vr.Valid,
		SchemaErrors:    []string{},
		IntegrityErrors: []string{},
	}

	for _, entry := range vr.Errors {
		msg := formatValidationEntry(entry)
		switch entry.Category {
		case "schema":
			result.SchemaErrors = append(result.SchemaErrors, msg)
		case "integrity":
			result.IntegrityErrors = append(result.IntegrityErrors, msg)
			if entry.Check != "" {
				result.RepairSuggestions = append(result.RepairSuggestions, RepairSuggestion{
					Description: fmt.Sprintf("Fix %s: %s", entry.Check, entry.Message),
					AutoFixable: false,
				})
			}
		default:
			result.IntegrityErrors = append(result.IntegrityErrors, msg)
		}
	}

	return result
}

// formatValidationEntry formats a ValidationEntry into a human-readable string.
func formatValidationEntry(entry afspec.ValidationEntry) string {
	parts := []string{}
	if entry.Artifact != "" {
		parts = append(parts, entry.Artifact)
	}
	if entry.Path != "" {
		parts = append(parts, entry.Path)
	}
	if entry.Check != "" {
		parts = append(parts, entry.Check)
	}
	if len(parts) > 0 {
		return fmt.Sprintf("[%s] %s", strings.Join(parts, ": "), entry.Message)
	}
	return entry.Message
}

// validateFallback loads individual JSON artifact files that are present
// in the spec directory, validates each one, and returns a combined
// SessionValidationResult with Valid=false.
func (s *SpecSession) validateFallback() (SessionValidationResult, error) {
	result := SessionValidationResult{
		Valid:           false,
		SchemaErrors:    []string{},
		IntegrityErrors: []string{},
	}

	foundAny := false

	for _, artifactName := range knownArtifacts {
		artifactPath := filepath.Join(s.specDir, artifactName)
		data, err := os.ReadFile(artifactPath)
		if err != nil {
			// File doesn't exist or is unreadable — record as integrity error.
			if os.IsNotExist(err) {
				result.IntegrityErrors = append(result.IntegrityErrors,
					fmt.Sprintf("missing artifact: %s", artifactName))
			} else {
				result.IntegrityErrors = append(result.IntegrityErrors,
					fmt.Sprintf("cannot read %s: %v", artifactName, err))
			}
			continue
		}

		foundAny = true

		// Validate that the file contains well-formed JSON.
		var raw json.RawMessage
		if err := json.Unmarshal(data, &raw); err != nil {
			result.SchemaErrors = append(result.SchemaErrors,
				fmt.Sprintf("invalid JSON in %s: %v", artifactName, err))
			continue
		}

		// Validate this one artifact against its schema. The cross-file rules
		// are not meaningful here — the other artifacts may be missing — and
		// validating a Spec built from one artifact would also report the PRD
		// frontmatter it does not have.
		if entries := validateArtifactFile(artifactName, data); len(entries) > 0 {
			partial := categorizeValidationResult(afspec.ValidationResult{Errors: entries})
			result.SchemaErrors = append(result.SchemaErrors, partial.SchemaErrors...)
			result.IntegrityErrors = append(result.IntegrityErrors, partial.IntegrityErrors...)
			result.RepairSuggestions = append(result.RepairSuggestions, partial.RepairSuggestions...)
		}
	}

	if !foundAny {
		result.IntegrityErrors = append(result.IntegrityErrors,
			"no artifact files found in spec directory")
	}

	return result, nil
}

// validateArtifactFile validates one artifact file's bytes against its v2
// schema and returns the violations.
func validateArtifactFile(artifactName string, data []byte) []afspec.ValidationEntry {
	var content map[string]any
	if err := json.Unmarshal(data, &content); err != nil {
		return []afspec.ValidationEntry{{
			Category: "schema", Check: "json_schema", Artifact: artifactName,
			Message: fmt.Sprintf("cannot parse %s: %v", artifactName, err),
		}}
	}

	var schemaName string
	switch artifactName {
	case "requirements.json":
		schemaName = afspec.RequirementsSchemaName
	case "test_spec.json":
		schemaName = afspec.TestSpecSchemaName
	case "tasks.json":
		schemaName = afspec.TasksSchemaName
	default:
		return nil
	}
	return afspec.ValidateArtifactSchema(content, schemaName, artifactName)
}

// Render loads the spec from the session's spec directory and renders
// the artifacts. When combined is true, it delegates to
// afspec.RenderCombined and returns the combined string. When combined
// is false, it delegates to afspec.RenderIndividual and returns a
// map[string]string keyed by artifact name. If LoadSpec fails, it
// falls back to rendering only available artifact files.
func (s *SpecSession) Render(combined bool) (any, error) {
	// Try the happy path: load the full spec and render.
	spec, loadErr := afspec.LoadSpec(s.specDir)
	if loadErr == nil {
		if combined {
			return spec.RenderCombined(), nil
		}
		return spec.RenderIndividual(), nil
	}

	// LoadSpec failed — fall back to rendering available artifacts.
	return s.renderFallback(combined)
}

// renderFallback loads individual artifact files that are present in the
// spec directory, builds a partial Spec, and renders it.
func (s *SpecSession) renderFallback(combined bool) (any, error) {
	spec := s.loadPartialSpec()

	hasArtifacts := spec.Requirements != nil || spec.TestSpec != nil ||
		spec.Tasks != nil || spec.Architecture != ""

	if !hasArtifacts {
		if combined {
			return "", nil
		}
		return map[string]string{}, nil
	}

	if combined {
		return spec.RenderCombined(), nil
	}

	return spec.RenderIndividual(), nil
}

// loadPartialSpec attempts to load whatever artifact files are present in
// the spec directory into a partial Spec.
func (s *SpecSession) loadPartialSpec() *afspec.Spec {
	spec := &afspec.Spec{}

	// Try to load PRD body.
	prdPath := filepath.Join(s.specDir, "prd.md")
	if prdData, err := os.ReadFile(prdPath); err == nil {
		// Extract body after frontmatter (everything after second "---").
		body := extractPRDBody(string(prdData))
		spec.PRDBody = body
	}

	// Try to load each JSON artifact.
	reqPath := filepath.Join(s.specDir, "requirements.json")
	if data, err := os.ReadFile(reqPath); err == nil {
		var req afspec.RequirementsV2Json
		if json.Unmarshal(data, &req) == nil {
			spec.Requirements = &req
		}
	}

	tsPath := filepath.Join(s.specDir, "test_spec.json")
	if data, err := os.ReadFile(tsPath); err == nil {
		var ts afspec.TestSpecV2Json
		if json.Unmarshal(data, &ts) == nil {
			spec.TestSpec = &ts
		}
	}

	tasksPath := filepath.Join(s.specDir, "tasks.json")
	if data, err := os.ReadFile(tasksPath); err == nil {
		var tasks afspec.TasksV2Json
		if json.Unmarshal(data, &tasks) == nil {
			spec.Tasks = &tasks
		}
	}

	// Try to load architecture.
	archPath := filepath.Join(s.specDir, "architecture.md")
	if data, err := os.ReadFile(archPath); err == nil {
		spec.Architecture = string(data)
	}

	return spec
}

// extractPRDBody extracts the Markdown body from a PRD file, stripping
// the YAML frontmatter delimited by "---" lines.
func extractPRDBody(content string) string {
	// Find the opening "---"
	_, afterOpen, found := strings.Cut(content, "---")
	if !found {
		return content
	}
	// Find the closing "---"
	_, afterClose, found := strings.Cut(afterOpen, "---")
	if !found {
		return content
	}
	return strings.TrimSpace(afterClose)
}

// Assess transitions to StateAssessing, creates a SpecAgent (or uses an
// injected one), loads the spec landscape from sibling specs, calls
// AssessPRD, appends the result to the assessment history, and persists
// session state. Returns (Assessment, nil) on success.
//
// On error, persists the error as lastError in session state without
// appending to assessment history, then returns (Assessment{}, error).
func (s *SpecSession) Assess(ctx context.Context) (Assessment, error) {
	// Transition to StateAssessing.
	s.Current = StateAssessing

	// Read the PRD file.
	prdText, err := s.readPRD()
	if err != nil {
		s.persistError(err)
		return Assessment{}, err
	}

	// Load spec landscape from sibling specs (best-effort).
	landscape := s.loadSiblingLandscape()

	// Resolve the agent to use.
	agent := s.resolveAgent("assess")

	// Extract spec name from directory basename.
	specName := filepath.Base(s.specDir)

	// Build agent options.
	opts := []AgentOption{
		WithSpecLandscape(landscape),
		WithProjectDir(s.specDir),
	}

	// Call AssessPRD.
	assessment, err := agent.AssessPRD(ctx, prdText, specName, opts...)
	if err != nil {
		s.persistError(err)
		return Assessment{}, err
	}

	// Append the successful assessment to in-memory history.
	s.AssessmentHistory = append(s.AssessmentHistory, assessment)

	// Persist session state.
	if err := s.persistSession(); err != nil {
		// Per REQ-9.E1: return persist error; in-memory history retains the entry.
		return Assessment{}, err
	}

	return assessment, nil
}

// Refine transitions to StateRefining, calls RefinePRD with the current
// PRD text, user answers, and latest assessment; updates the PRD file on
// disk with the refined text; appends the new assessment to history;
// records a QA exchange with assessment_index, answers, and UTC
// timestamp; and persists session state.
//
// On error, persists the error as lastError without updating the PRD
// file, without appending to assessment history, and without recording
// a QA exchange.
func (s *SpecSession) Refine(ctx context.Context, answers map[string]string) (Assessment, error) {
	// Transition to StateRefining.
	s.Current = StateRefining

	// Read the current PRD file.
	prdText, err := s.readPRD()
	if err != nil {
		s.persistError(err)
		return Assessment{}, err
	}

	// Get the latest assessment from history (may be zero-value if empty).
	var prevAssessment Assessment
	if len(s.AssessmentHistory) > 0 {
		prevAssessment = s.AssessmentHistory[len(s.AssessmentHistory)-1]
	}

	// Load spec landscape from sibling specs (best-effort).
	landscape := s.loadSiblingLandscape()

	// Resolve the agent to use.
	agent := s.resolveAgent("refine")

	// Build agent options.
	opts := []AgentOption{
		WithSpecLandscape(landscape),
		WithProjectDir(s.specDir),
	}

	// Call RefinePRD.
	updatedPRD, newAssessment, err := agent.RefinePRD(ctx, prdText, answers, prevAssessment, opts...)
	if err != nil {
		s.persistError(err)
		return Assessment{}, err
	}

	// Write the updated PRD to disk.
	prdPath := filepath.Join(s.specDir, s.PRDPath)
	if err := os.WriteFile(prdPath, []byte(updatedPRD), 0o644); err != nil {
		// Per REQ-10.E1: do NOT record QA exchange or update assessment.
		s.persistError(err)
		return Assessment{}, err
	}

	// Append the new assessment to history.
	s.AssessmentHistory = append(s.AssessmentHistory, newAssessment)

	// Record the QA exchange.
	exchange := QAExchange{
		AssessmentIndex: len(s.AssessmentHistory) - 2, // index of the assessment the answers corresponded to
		Answers:         answers,
		Timestamp:       time.Now().UTC(),
	}
	s.QAExchanges = append(s.QAExchanges, exchange)

	// Persist session state.
	if err := s.persistSession(); err != nil {
		return Assessment{}, err
	}

	return newAssessment, nil
}

// readPRD reads the PRD file from disk and returns its text content.
func (s *SpecSession) readPRD() (string, error) {
	prdPath := filepath.Join(s.specDir, s.PRDPath)
	data, err := os.ReadFile(prdPath)
	if err != nil {
		return "", fmt.Errorf("failed to read PRD file %s: %w", prdPath, err)
	}
	return string(data), nil
}

// SetRunOptions overrides the run options the session's phases are built
// with. It is how a caller supplies the workspace the model may read, the
// providers it talks to, and the bounds it runs under; the session itself has
// no opinion about any of the three.
//
// Options set here win over the config file for every field they set, and
// leave the rest to it.
func (s *SpecSession) SetRunOptions(o RunOptions) { s.runOpts = &o }

// resolveAgent returns the assessor to use — the injected mock if set,
// or a new SpecAgent configured for the given phase.
// phase should be one of "assess", "refine", or "generate".
// If LoadConfig fails (e.g. no config file), it falls back to "STANDARD".
func (s *SpecSession) resolveAgent(phase string) assessor {
	if s.agent != nil {
		return s.agent
	}
	cfg, err := LoadConfig()
	if err != nil {
		cfg = AgentSpecConfig{Model: "STANDARD"}
	}
	opts := cfg.RunOptions()
	if s.runOpts != nil {
		opts = mergeRunOptions(opts, *s.runOpts)
	}
	return NewSpecAgentWith(cfg.ModelForPhase(phase), cfg.ModelVariant, opts)
}

// mergeRunOptions overlays the caller's options on the configured ones. A zero
// field in the overlay means "not set here", so a CLI flag that was not passed
// does not silently reset a value the config file provided.
func mergeRunOptions(base, over RunOptions) RunOptions {
	if over.Vendor != "" {
		base.Vendor = over.Vendor
	}
	if over.Model != nil {
		base.Model = over.Model
	}
	if over.Workspace != nil {
		base.Workspace = over.Workspace
	}
	if over.TrustProject {
		base.TrustProject = true
	}
	if over.WorkDir != "" {
		base.WorkDir = over.WorkDir
	}
	if over.MaxTurns > 0 {
		base.MaxTurns = over.MaxTurns
	}
	if over.MaxBudgetUSD > 0 {
		base.MaxBudgetUSD = over.MaxBudgetUSD
	}
	if over.MaxAttempts > 0 {
		base.MaxAttempts = over.MaxAttempts
	}
	if over.Providers != nil {
		base.Providers = over.Providers
	}
	if over.OnEvent != nil {
		base.OnEvent = over.OnEvent
	}
	return base
}

// persistError sets the LastErr field and persists the session state.
// If atomic persistence fails (e.g. directory is read-only), it falls
// back to a direct overwrite of the existing _session.json file. If
// both methods fail, the error is silently discarded (the caller
// already has an error to return).
func (s *SpecSession) persistError(err error) {
	s.LastErr = &LastError{
		Message: err.Error(),
		Type:    fmt.Sprintf("%T", err),
	}
	if persistErr := s.persistSession(); persistErr != nil {
		// Atomic persist failed — fall back to direct overwrite of
		// existing _session.json (works when the directory is read-only
		// but the file itself is writable).
		if data, marshalErr := json.Marshal(s); marshalErr == nil {
			_ = os.WriteFile(filepath.Join(s.specDir, "_session.json"), data, 0o644)
		}
	}
}

// loadSiblingLandscape attempts to load spec landscape entries from
// sibling spec directories (same parent directory). Each entry's status
// is read from the sibling's prd.md frontmatter. Returns nil on any
// directory-read error — landscape loading is best-effort; individual
// siblings with missing or malformed prd.md are silently skipped.
func (s *SpecSession) loadSiblingLandscape() []map[string]any {
	parentDir := filepath.Dir(s.specDir)
	currentName := filepath.Base(s.specDir)

	// Derive the current spec's ID from its directory name (best-effort)
	// so LoadSpecLandscape can exclude it from results.
	currentSpecID, _, _ := afspec.ParseSpecDirName(currentName)

	// LoadSpecLandscape scans parentDir, reads each sibling's prd.md
	// frontmatter to obtain the actual status, and excludes the current
	// spec. Errors from missing or malformed PRDs are silently ignored.
	metas, _ := afspec.LoadSpecLandscape(parentDir, false, currentSpecID)

	if len(metas) == 0 {
		return nil
	}

	landscape := make([]map[string]any, 0, len(metas))
	for _, m := range metas {
		landscape = append(landscape, map[string]any{
			"spec_id": m.SpecID,
			"title":   m.SpecName,
			"status":  m.Status,
		})
	}
	return landscape
}

// artifactNameToFile maps artifact names (as used by GenerateArtifacts)
// to their on-disk JSON filenames.
var artifactNameToFile = map[string]string{
	"requirements": "requirements.json",
	"test_spec":    "test_spec.json",
	"tasks":        "tasks.json",
}

// Generate transitions to StateGenerating immediately, checks for
// existing artifact files to skip already-generated artifacts (partial
// failure recovery), calls GenerateArtifacts with an OnArtifact callback
// that writes each artifact to disk via afspec MarshalJSON and records
// it in generatedArtifacts, transitions to StateGenerated after all
// artifacts are generated, runs Validate, and returns a GenerateResult.
//
// On error, does not transition to StateGenerated, persists the error
// as lastError, and returns (GenerateResult{}, error).
func (s *SpecSession) Generate(ctx context.Context) (GenerateResult, error) {
	// Transition to StateGenerating immediately.
	s.Current = StateGenerating

	// Read the PRD file.
	prdText, err := s.readPRD()
	if err != nil {
		s.persistError(err)
		return GenerateResult{}, err
	}

	// Check for existing artifact files on disk (partial failure recovery).
	existing := map[string]bool{}
	for name, fileName := range artifactNameToFile {
		artifactPath := filepath.Join(s.specDir, fileName)
		if _, statErr := os.Stat(artifactPath); statErr == nil {
			existing[name] = true
		}
	}

	// writtenPaths accumulates the artifact files this run created.
	var writtenPaths []string

	// Only call GenerateArtifacts if there are missing artifacts.
	if len(existing) < len(artifactNameToFile) {
		agent := s.resolveAgent("generate")

		// Extract spec ID and name from the directory basename.
		dirName := filepath.Base(s.specDir)
		specID, specName, parseErr := afspec.ParseSpecDirName(dirName)
		if parseErr != nil {
			// Fallback for non-standard directory names (e.g. in tests).
			specID = ""
			specName = dirName
		}

		landscape := s.loadSiblingLandscape()

		// writeErr captures any error from the OnArtifact callback so it
		// can be surfaced after GenerateArtifacts returns. writtenPaths
		// records what this run put on disk so that a spec which fails final
		// validation can be removed again (§12.2: a failed generation does not
		// leave partial output behind).
		var writeErr error

		onArtifact := func(name string, content any) {
			if writeErr != nil {
				return // A previous write failed; skip subsequent artifacts.
			}

			fileName, ok := artifactNameToFile[name]
			if !ok {
				fileName = name + ".json"
			}
			filePath := filepath.Join(s.specDir, fileName)

			data, marshalErr := afspec.MarshalJSON(content)
			if marshalErr != nil {
				writeErr = fmt.Errorf("failed to serialize %s: %w", name, marshalErr)
				return
			}

			if writeFileErr := os.WriteFile(filePath, data, 0o644); writeFileErr != nil {
				writeErr = fmt.Errorf("failed to write %s: %w", fileName, writeFileErr)
				return
			}

			writtenPaths = append(writtenPaths, filePath)
			s.GeneratedArtifacts = append(s.GeneratedArtifacts, name)
		}

		opts := []AgentOption{
			WithSpecLandscape(landscape),
			WithProjectDir(s.specDir),
			WithOnArtifact(onArtifact),
		}

		_, err = agent.GenerateArtifacts(ctx, prdText, specID, specName, opts...)
		if err != nil {
			s.persistError(err)
			return GenerateResult{}, err
		}

		// Check for write errors from the OnArtifact callback.
		if writeErr != nil {
			s.persistError(writeErr)
			return GenerateResult{}, writeErr
		}
	}

	// Record existing (pre-generated) artifacts that were skipped. They are
	// deliberately not added to writtenPaths: this run did not create them, so
	// a failed generation must not delete them.
	for name := range existing {
		if !slices.Contains(s.GeneratedArtifacts, name) {
			s.GeneratedArtifacts = append(s.GeneratedArtifacts, name)
		}
	}

	// Transition to StateGenerated.
	s.Current = StateGenerated

	// Validate the assembled spec. Each artifact was already checked against
	// its schema and against the rules decidable at its step, so this is the
	// final proof that the three together form a complete plan.
	validation, warnings, validateErr := s.validateGenerated()
	if validateErr != nil {
		validation.Valid = false
		validation.IntegrityErrors = append(validation.IntegrityErrors, validateErr.Error())
	}

	if !validation.Valid {
		// §12.2: a generation that ends with an invalid spec is a failed
		// generation. Remove what this run wrote so that no half-valid spec
		// is left for a coder to pick up, and report the violations as an
		// error rather than as warnings.
		for _, path := range writtenPaths {
			_ = os.Remove(path)
		}
		s.GeneratedArtifacts = nil

		err := &AgentError{
			Detail: fmt.Sprintf("generation produced an invalid spec:\n%s",
				strings.Join(append(validation.SchemaErrors, validation.IntegrityErrors...), "\n")),
			ErrorCategory: "validation",
		}
		s.persistError(err)
		return GenerateResult{}, err
	}

	// Persist final session state.
	if err := s.persistSession(); err != nil {
		return GenerateResult{}, err
	}

	return GenerateResult{
		Artifacts:  s.GeneratedArtifacts,
		Validation: validation,
		Warnings:   warnings,
	}, nil
}

// validateGenerated runs full validation on the spec the session just wrote
// and returns the categorised result along with the advisory warnings.
func (s *SpecSession) validateGenerated() (SessionValidationResult, []string, error) {
	spec, loadErr := afspec.LoadSpec(s.specDir)
	if loadErr != nil {
		result, err := s.crossFileFallback()
		return result, nil, err
	}

	vr := spec.Validate()
	warnings := make([]string, 0, len(vr.Warnings))
	for _, w := range vr.Warnings {
		warnings = append(warnings, formatValidationEntry(w))
	}
	return categorizeValidationResult(vr), warnings, nil
}
