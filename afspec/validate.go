package afspec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationResult holds the results of spec validation, including errors
// and warnings. Valid is true if and only if Errors is empty.
type ValidationResult struct {
	Valid    bool
	Errors   []ValidationEntry
	Warnings []ValidationEntry
}

// ValidationEntry represents a single validation error or warning.
type ValidationEntry struct {
	Category      string // "schema" or "integrity" or "warning"
	Message       string
	Artifact      string // which artifact file had the issue
	Path          string // JSON path (InstanceLocation) for schema errors
	Keyword       string // KeywordLocation for schema errors
	Check         string // check name for integrity errors (e.g. "dangling_reference")
	RequirementID string // for integrity errors referencing a requirement
	EntityID      string // for warnings referencing an entity
	Value         string // optional value context
}

// ---------------------------------------------------------------------------
// JSON Schema validation infrastructure
// ---------------------------------------------------------------------------

// compiledSchemas holds the compiled JSON schemas, lazily initialized.
var (
	compiledSchemas     map[string]*jsonschema.Schema
	compiledSchemasErr  error
	compiledSchemasOnce sync.Once
)

// embeddedURLLoader implements jsonschema.URLLoader, serving schema bytes
// from the embedded schemaFS. It loads schemas from the
// "https://agent-fox.dev/schemas/" URL namespace.
type embeddedURLLoader struct {
	schemas map[string][]byte
}

func (l *embeddedURLLoader) Load(url string) (any, error) {
	const prefix = "https://agent-fox.dev/schemas/"
	if !strings.HasPrefix(url, prefix) {
		return nil, fmt.Errorf("unsupported schema URL: %s", url)
	}
	name := strings.TrimPrefix(url, prefix)
	data, ok := l.schemas[name]
	if !ok {
		return nil, fmt.Errorf("schema not found: %s", name)
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(data))
}

// getCompiledSchemas returns the compiled JSON schemas, initializing them
// on the first call. Returns an error (wrapping LoadError) if any schema
// fails to compile.
func getCompiledSchemas() (map[string]*jsonschema.Schema, error) {
	compiledSchemasOnce.Do(func() {
		schemas := Schemas()
		loader := &embeddedURLLoader{schemas: schemas}

		c := jsonschema.NewCompiler()
		c.UseLoader(jsonschema.SchemeURLLoader{
			"https": loader,
		})

		compiled := make(map[string]*jsonschema.Schema, len(schemas))
		for name, data := range schemas {
			// Parse and add the resource so the compiler knows about it
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compiledSchemasErr = &LoadError{
					Msg:  fmt.Sprintf("cannot parse schema %s: %s", name, err),
					File: name,
					Err:  &SpecError{Msg: fmt.Sprintf("cannot parse schema %s: %s", name, err)},
				}
				return
			}
			url := "https://agent-fox.dev/schemas/" + name
			if err := c.AddResource(url, doc); err != nil {
				compiledSchemasErr = &LoadError{
					Msg:  fmt.Sprintf("cannot add schema resource %s: %s", name, err),
					File: name,
					Err:  &SpecError{Msg: fmt.Sprintf("cannot add schema resource %s: %s", name, err)},
				}
				return
			}
		}

		for name := range schemas {
			url := "https://agent-fox.dev/schemas/" + name
			sch, err := c.Compile(url)
			if err != nil {
				compiledSchemasErr = &LoadError{
					Msg:  fmt.Sprintf("cannot compile schema %s: %s", name, err),
					File: name,
					Err:  &SpecError{Msg: fmt.Sprintf("cannot compile schema %s: %s", name, err)},
				}
				return
			}
			compiled[name] = sch
		}
		compiledSchemas = compiled
	})
	return compiledSchemas, compiledSchemasErr
}

// flattenValidationError recursively flattens a jsonschema.ValidationError
// tree into a slice of leaf errors (those without causes or with direct
// error messages).
func flattenValidationError(ve *jsonschema.ValidationError, artifact string) []ValidationEntry {
	var entries []ValidationEntry

	// If there are no causes, this is a leaf error
	if len(ve.Causes) == 0 {
		instanceLoc := "/" + strings.Join(ve.InstanceLocation, "/")
		if len(ve.InstanceLocation) == 0 {
			instanceLoc = ""
		}
		keywordLoc := ""
		msg := ""
		if ve.ErrorKind != nil {
			keywordLoc = "/" + strings.Join(ve.ErrorKind.KeywordPath(), "/")
			msg = ve.Error()
		}
		entries = append(entries, ValidationEntry{
			Category: "schema",
			Check:    "json_schema",
			Message:  msg,
			Artifact: artifact,
			Path:     instanceLoc,
			Keyword:  keywordLoc,
		})
		return entries
	}

	// Recurse into causes
	for _, cause := range ve.Causes {
		entries = append(entries, flattenValidationError(cause, artifact)...)
	}
	return entries
}

// validateArtifactSchema marshals the artifact to JSON, validates it against
// the named schema, and returns any validation errors as ValidationEntry slices.
func validateArtifactSchema(artifact any, schemaName, artifactName string) []ValidationEntry {
	schemas, err := getCompiledSchemas()
	if err != nil {
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  fmt.Sprintf("schema compilation error: %s", err),
			Artifact: artifactName,
		}}
	}

	sch, ok := schemas[schemaName]
	if !ok {
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  fmt.Sprintf("schema not found: %s", schemaName),
			Artifact: artifactName,
		}}
	}

	// Marshal the artifact to JSON, then unmarshal to any for validation.
	// We must use encoding/json (not MarshalJSON) to produce standard JSON
	// that the validator can consume as map[string]any.
	data, marshalErr := json.Marshal(artifact)
	if marshalErr != nil {
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  fmt.Sprintf("cannot marshal %s: %s", artifactName, marshalErr),
			Artifact: artifactName,
		}}
	}

	var instance any
	if parseErr := json.Unmarshal(data, &instance); parseErr != nil {
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  fmt.Sprintf("cannot parse %s JSON: %s", artifactName, parseErr),
			Artifact: artifactName,
		}}
	}

	validationErr := sch.Validate(instance)
	if validationErr == nil {
		return nil
	}

	ve, ok := validationErr.(*jsonschema.ValidationError)
	if !ok {
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  fmt.Sprintf("validation error: %s", validationErr),
			Artifact: artifactName,
		}}
	}

	return flattenValidationError(ve, artifactName)
}

// ---------------------------------------------------------------------------
// PRD frontmatter schema validation helper
// ---------------------------------------------------------------------------

// prdFrontmatterForSchema is a lightweight struct containing only the 12
// fields defined in prd-frontmatter.v1.json. It is used exclusively by
// ValidateSchema to marshal the Spec's PRD frontmatter fields to JSON
// for schema validation, avoiding spurious additionalProperties errors
// from non-schema fields on the Spec struct.
type prdFrontmatterForSchema struct {
	SpecID        string   `json:"spec_id"`
	SpecName      string   `json:"spec_name"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	Owner         string   `json:"owner"`
	Source        string   `json:"source"`
	Supersedes    []string `json:"supersedes"`
	Tags          []string `json:"tags,omitempty"`
	IntentHash    *string  `json:"intent_hash"`
	SchemaVersion int      `json:"schema_version"`
}

// ---------------------------------------------------------------------------
// Spec.ValidateSchema
// ---------------------------------------------------------------------------

// ValidateSchema compiles each bundled schema using jsonschema compiler
// with a SchemeURLLoader backed by embedded bytes, validates each
// artifact's JSON representation against its schema, and returns a
// ValidationResult.
func (s *Spec) ValidateSchema() ValidationResult {
	var allErrors []ValidationEntry

	// Validate prd.md frontmatter
	fm := prdFrontmatterForSchema{
		SpecID:        s.SpecID,
		SpecName:      s.SpecName,
		Title:         s.Title,
		Status:        s.Status,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
		Owner:         s.Owner,
		Source:        s.Source,
		Supersedes:    s.Supersedes,
		Tags:          s.Tags,
		IntentHash:    s.IntentHash,
		SchemaVersion: s.SchemaVersion,
	}
	errs := validateArtifactSchema(fm, "prd-frontmatter.v1.json", "prd.md")
	allErrors = append(allErrors, errs...)

	// Validate requirements.json
	if s.Requirements != nil {
		errs := validateArtifactSchema(s.Requirements, "requirements.v1.json", "requirements.json")
		allErrors = append(allErrors, errs...)
	}

	// Validate test_spec.json
	if s.TestSpec != nil {
		errs := validateArtifactSchema(s.TestSpec, "test_spec.v1.json", "test_spec.json")
		allErrors = append(allErrors, errs...)
	}

	// Validate tasks.json
	if s.Tasks != nil {
		errs := validateArtifactSchema(s.Tasks, "tasks.v1.json", "tasks.json")
		allErrors = append(allErrors, errs...)
	}

	// Validate EARS pattern field constraints on criteria
	if s.Requirements != nil {
		allErrors = append(allErrors, validateEarsConstraints(s.Requirements)...)
	}

	return ValidationResult{
		Valid:  len(allErrors) == 0,
		Errors: allErrors,
	}
}

// ---------------------------------------------------------------------------
// EARS pattern field constraint validation
// ---------------------------------------------------------------------------

// earsRequiredFields maps each valid EARS pattern to its required
// pattern-specific fields, matching the Python _EARS_REQUIRED_FIELDS.
var earsRequiredFields = map[CriterionEarsPattern][]string{
	CriterionEarsPatternUbiquitous:   {},
	CriterionEarsPatternEventDriven:  {"trigger"},
	CriterionEarsPatternComplexEvent: {"trigger", "condition"},
	CriterionEarsPatternStateDriven:  {"state"},
	CriterionEarsPatternUnwanted:     {"error_condition"},
	CriterionEarsPatternOptional:     {"feature"},
}

// allPatternFields is the set of all pattern-specific fields on a Criterion.
var allPatternFields = []string{"trigger", "condition", "error_condition", "state", "feature"}

// criterionFieldValue returns the *string value of the named
// pattern-specific field on the given Criterion.
func criterionFieldValue(c *Criterion, field string) *string {
	switch field {
	case "trigger":
		return c.Trigger
	case "condition":
		return c.Condition
	case "error_condition":
		return c.ErrorCondition
	case "state":
		return c.State
	case "feature":
		return c.Feature
	default:
		return nil
	}
}

// ValidateRequirementsMap performs targeted validation on a requirements
// artifact represented as a raw map[string]any. It checks EARS pattern field
// constraints and requirement ID format patterns without requiring every
// JSON schema field to be present. This is used by the AI generation pipeline
// to validate AI-produced content that may lack optional schema fields.
//
// Checks performed:
//   - Requirement ID format: must match NN-REQ-N[.N|.EN] (id_format check)
//   - Requirement ID prefix consistency with spec_id (id_format check)
//   - EARS pattern field constraints on acceptance_criteria and edge_cases
//     (ears_constraint check)
//
// Returns a slice of ValidationEntry describing any violations. An empty
// slice means the content passed all checks.
func ValidateRequirementsMap(content map[string]any) []ValidationEntry {
	var entries []ValidationEntry

	specID, _ := content["spec_id"].(string)
	reqs, _ := content["requirements"].([]any)

	for reqIdx, rawReq := range reqs {
		req, ok := rawReq.(map[string]any)
		if !ok {
			continue
		}
		reqID, _ := req["id"].(string)

		// Check requirement ID format.
		if reqID != "" && !requirementIDPattern.MatchString(reqID) {
			entries = append(entries, ValidationEntry{
				Category: "integrity",
				Check:    "id_format",
				Message:  fmt.Sprintf("requirement ID %q does not match expected format NN-REQ-N[.N|.EN]", reqID),
				Artifact: "requirements.json",
				EntityID: reqID,
				Path:     fmt.Sprintf("requirements[%d].id", reqIdx),
			})
		}

		// Check spec_id prefix consistency.
		if reqID != "" && specID != "" {
			if prefix := extractReqPrefix(reqID); prefix != "" && prefix != specID {
				entries = append(entries, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("requirement ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", reqID, prefix, specID),
					Artifact: "requirements.json",
					EntityID: reqID,
					Path:     fmt.Sprintf("requirements[%d].id", reqIdx),
				})
			}
		}

		// Check EARS constraints for acceptance_criteria and edge_cases.
		for _, groupName := range []string{"acceptance_criteria", "edge_cases"} {
			criteria, _ := req[groupName].([]any)
			for cIdx, rawC := range criteria {
				c, ok := rawC.(map[string]any)
				if !ok {
					continue
				}
				cID, _ := c["id"].(string)
				patternStr, _ := c["ears_pattern"].(string)
				pattern := CriterionEarsPattern(patternStr)

				required, ok := earsRequiredFields[pattern]
				if !ok {
					entries = append(entries, ValidationEntry{
						Category: "schema",
						Check:    "ears_constraint",
						Message:  fmt.Sprintf("Criterion %s: invalid ears_pattern value %q", cID, patternStr),
						Artifact: "requirements.json",
						Path:     fmt.Sprintf("requirements[%d].%s[%d].ears_pattern", reqIdx, groupName, cIdx),
					})
					continue
				}

				// Build set of required field names for quick lookup.
				requiredSet := make(map[string]bool, len(required))
				for _, f := range required {
					requiredSet[f] = true
				}

				// Check required fields are present (non-nil).
				for _, field := range required {
					val, exists := c[field]
					if !exists || val == nil {
						entries = append(entries, ValidationEntry{
							Category: "schema",
							Check:    "ears_constraint",
							Message:  fmt.Sprintf("Criterion %s: pattern %q requires field '%s' but it is missing", cID, patternStr, field),
							Artifact: "requirements.json",
							Path:     fmt.Sprintf("requirements[%d].%s[%d].%s", reqIdx, groupName, cIdx, field),
						})
					}
				}

				// Check forbidden fields are absent (nil or not present).
				for _, field := range allPatternFields {
					if requiredSet[field] {
						continue
					}
					val, exists := c[field]
					if exists && val != nil {
						entries = append(entries, ValidationEntry{
							Category: "schema",
							Check:    "ears_constraint",
							Message:  fmt.Sprintf("Criterion %s: pattern %q must not have field '%s' but it is set", cID, patternStr, field),
							Artifact: "requirements.json",
							Path:     fmt.Sprintf("requirements[%d].%s[%d].%s", reqIdx, groupName, cIdx, field),
						})
					}
				}
			}
		}
	}

	return entries
}

// subtaskIDPattern matches valid subtask IDs like "1.1" or "2.3" ({group}.{N}).
var subtaskIDPattern = regexp.MustCompile(`^\d+\.\d+$`)

// verificationIDPattern matches valid verification IDs like "1.V" or "2.V" ({group}.V).
var verificationIDPattern = regexp.MustCompile(`^\d+\.V$`)

// validTestCaseKinds is the set of allowed values for a test case's "kind" field.
var validTestCaseKinds = map[string]bool{
	"unit":        true,
	"integration": true,
	"smoke":       true,
	"property":    true,
	"edge_case":   true,
}

// ValidateTestSpecMap performs targeted validation on a test_spec artifact
// represented as a raw map[string]any. It checks test case ID format and
// kind enum values without requiring every JSON schema field to be present.
// This is used by the AI generation pipeline to validate AI-produced content
// that may lack optional schema fields.
//
// Checks performed:
//   - Test case ID format: must match testCaseIDPattern (^TS-\w+-\d+$) (id_format check)
//   - Test case kind enum: must be one of unit/integration/smoke/property/edge_case (kind_enum check)
//
// Returns a slice of ValidationEntry describing any violations. An empty
// slice means the content passed all checks.
func ValidateTestSpecMap(content map[string]any) []ValidationEntry {
	var entries []ValidationEntry

	testCases, _ := content["test_cases"].([]any)
	for tcIdx, rawTC := range testCases {
		tc, ok := rawTC.(map[string]any)
		if !ok {
			continue
		}
		tcID, _ := tc["id"].(string)

		// Check test case ID format.
		if tcID != "" && !testCaseIDPattern.MatchString(tcID) {
			entries = append(entries, ValidationEntry{
				Category: "integrity",
				Check:    "id_format",
				Message:  fmt.Sprintf("test case ID %q does not match expected format TS-<spec>-<N>", tcID),
				Artifact: "test_spec.json",
				EntityID: tcID,
				Path:     fmt.Sprintf("test_cases[%d].id", tcIdx),
			})
		}

		// Check kind enum value.
		kind, hasKind := tc["kind"].(string)
		if hasKind && kind != "" && !validTestCaseKinds[kind] {
			entries = append(entries, ValidationEntry{
				Category: "schema",
				Check:    "kind_enum",
				Message:  fmt.Sprintf("test case %q has invalid kind %q; must be one of: unit, integration, smoke, property, edge_case", tcID, kind),
				Artifact: "test_spec.json",
				EntityID: tcID,
				Path:     fmt.Sprintf("test_cases[%d].kind", tcIdx),
			})
		}
	}

	return entries
}

// ValidateTasksMap performs targeted validation on a tasks artifact
// represented as a raw map[string]any. It checks subtask ID format and
// verification ID format without requiring every JSON schema field to be
// present. This is used by the AI generation pipeline to validate AI-produced
// content that may lack optional schema fields.
//
// Checks performed:
//   - Subtask ID format: must match {group}.{N} (^\d+\.\d+$) (id_format check)
//   - Verification ID format: must match {group}.V (^\d+\.V$) (id_format check)
//
// Returns a slice of ValidationEntry describing any violations. An empty
// slice means the content passed all checks.
func ValidateTasksMap(content map[string]any) []ValidationEntry {
	var entries []ValidationEntry

	taskGroups, _ := content["task_groups"].([]any)
	for gIdx, rawGroup := range taskGroups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			continue
		}

		// Check verification ID format if present.
		if rawVerification, exists := group["verification"]; exists {
			if verification, ok := rawVerification.(map[string]any); ok {
				verID, _ := verification["id"].(string)
				if verID != "" && !verificationIDPattern.MatchString(verID) {
					entries = append(entries, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("verification ID %q does not match expected format {group}.V", verID),
						Artifact: "tasks.json",
						EntityID: verID,
						Path:     fmt.Sprintf("task_groups[%d].verification.id", gIdx),
					})
				}
			}
		}

		// Check subtask ID format.
		subtasks, _ := group["subtasks"].([]any)
		for sIdx, rawSub := range subtasks {
			sub, ok := rawSub.(map[string]any)
			if !ok {
				continue
			}
			subID, _ := sub["id"].(string)
			if subID != "" && !subtaskIDPattern.MatchString(subID) {
				entries = append(entries, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("subtask ID %q does not match expected format {group}.{N}", subID),
					Artifact: "tasks.json",
					EntityID: subID,
					Path:     fmt.Sprintf("task_groups[%d].subtasks[%d].id", gIdx, sIdx),
				})
			}
		}
	}

	return entries
}

// validateEarsConstraints checks that each criterion's pattern-specific
// fields match the required set for its ears_pattern. For each criterion:
//   - Required fields must be non-nil
//   - Forbidden fields (not required) must be nil
//   - The ears_pattern value must be one of the six valid enum values
//
// This mirrors the Python _validate_ears_constraints function.
func validateEarsConstraints(req *RequirementsV1Json) []ValidationEntry {
	var errors []ValidationEntry

	for _, r := range req.Requirements {
		type criteriaGroup struct {
			criteria []Criterion
			listName string
		}
		groups := []criteriaGroup{
			{r.AcceptanceCriteria, "acceptance_criteria"},
			{r.EdgeCases, "edge_cases"},
		}

		for _, g := range groups {
			for idx, c := range g.criteria {
				pattern := c.EarsPattern

				required, ok := earsRequiredFields[pattern]
				if !ok {
					errors = append(errors, ValidationEntry{
						Category: "schema",
						Check:    "ears_constraint",
						Message:  fmt.Sprintf("Criterion %s: invalid ears_pattern value %q", c.Id, string(pattern)),
						Artifact: "requirements.json",
						Path:     fmt.Sprintf("requirements.%s.%s[%d].ears_pattern", r.Id, g.listName, idx),
					})
					continue
				}

				// Build set of required field names for quick lookup.
				requiredSet := make(map[string]bool, len(required))
				for _, f := range required {
					requiredSet[f] = true
				}

				// Check required fields are present (non-nil).
				for _, field := range required {
					if criterionFieldValue(&c, field) == nil {
						errors = append(errors, ValidationEntry{
							Category: "schema",
							Check:    "ears_constraint",
							Message:  fmt.Sprintf("Criterion %s: pattern %q requires field '%s' but it is missing", c.Id, string(pattern), field),
							Artifact: "requirements.json",
							Path:     fmt.Sprintf("requirements.%s.%s[%d].%s", r.Id, g.listName, idx, field),
						})
					}
				}

				// Check forbidden fields are nil.
				for _, field := range allPatternFields {
					if requiredSet[field] {
						continue
					}
					if criterionFieldValue(&c, field) != nil {
						errors = append(errors, ValidationEntry{
							Category: "schema",
							Check:    "ears_constraint",
							Message:  fmt.Sprintf("Criterion %s: pattern %q must not have field '%s' but it is set", c.Id, string(pattern), field),
							Artifact: "requirements.json",
							Path:     fmt.Sprintf("requirements.%s.%s[%d].%s", r.Id, g.listName, idx, field),
						})
					}
				}
			}
		}
	}

	return errors
}

// ---------------------------------------------------------------------------
// Spec.ValidateCrossFile
// ---------------------------------------------------------------------------

// requirementIDPattern matches valid requirement IDs like "01-REQ-1", "abc-REQ-1.1", or "myspec-REQ-1.E1".
var requirementIDPattern = regexp.MustCompile(`^\w+-REQ-\d+(\.\d+|\.E\d+)?$`)

// testCaseIDPattern matches valid test case IDs like "TS-01-1" or "TS-abc-1".
var testCaseIDPattern = regexp.MustCompile(`^TS-\w+-\d+$`)

// propertyIDPattern matches valid property IDs like "01-PROP-1" or "abc-PROP-1".
var propertyIDPattern = regexp.MustCompile(`^\w+-PROP-\d+$`)

// pathIDPattern matches valid execution path IDs like "01-PATH-1" or "abc-PATH-1".
var pathIDPattern = regexp.MustCompile(`^\w+-PATH-\d+$`)

// errorHandlingIDPattern matches valid error handling IDs like "01-ERR-1" or "abc-ERR-1".
var errorHandlingIDPattern = regexp.MustCompile(`^\w+-ERR-\d+$`)

// smokeTestIDPattern matches valid smoke test IDs like "TS-01-SMOKE-1" or "TS-abc-SMOKE-1".
var smokeTestIDPattern = regexp.MustCompile(`^TS-\w+-SMOKE-\d+$`)

// propertyTestIDPattern matches valid property test IDs like "TS-01-P1" or "TS-abc-P1".
var propertyTestIDPattern = regexp.MustCompile(`^TS-\w+-P\d+$`)

// edgeCaseTestIDPattern matches valid edge case test IDs like "TS-01-E1" or "TS-abc-E1".
var edgeCaseTestIDPattern = regexp.MustCompile(`^TS-\w+-E\d+$`)

// criterionIDPattern matches valid criterion IDs like "01-REQ-1.1", "abc-REQ-1.E1".
var criterionIDPattern = regexp.MustCompile(`^\w+-REQ-\d+\.\d+$|^\w+-REQ-\d+\.E\d+$`)

// wiringSmokeRefPattern matches smoke test references in wiring_verification
// group test_spec_refs (e.g. "TS-04-SMOKE-1"). Compiled at package level.
var wiringSmokeRefPattern = regexp.MustCompile(`^TS-.*-SMOKE-.*$`)

// stubDeadCodeRe matches stub or dead-code references using the same pattern
// as Python's _STUB_DEAD_CODE_RE: "stub" or "dead" followed by optional
// whitespace/underscore/hyphen and "code". Bare "dead" (e.g. in "deadline")
// does not match.
var stubDeadCodeRe = regexp.MustCompile(`(?i)stub|dead[\s_-]?code`)

// backtickTermRe extracts backtick-wrapped terms from text fields.
// Compiled at package initialization time per 04-REQ-4.2.
var backtickTermRe = regexp.MustCompile("`([^`]+)`")

// backtickNumericRe matches numeric values including negatives and decimals (e.g. -1, 3.14).
var backtickNumericRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

// backtickQuotedRe matches terms wrapped in single or double quotes.
var backtickQuotedRe = regexp.MustCompile(`^["'].*["']$`)

// vagueLanguageRe matches vague words that reduce testability in criterion fields.
// Compiled at package initialization time per 04-REQ-18.2.
// Aligned with Python's _VAGUE_WORDS_RE.
var vagueLanguageRe = regexp.MustCompile(`(?i)\b(appropriate|properly|correctly|reasonable|relevant|adequate|suitable|as needed|if necessary|etc)\b`)

// errorKeywordRe matches error-indicating keywords in criterion action fields.
// Used for 04-REQ-17 to detect error paths that should have a return_contract.
// Aligned with Python's _ERROR_PATH_RE.
var errorKeywordRe = regexp.MustCompile(`(?i)\b(error|fail|reject|denied|deny|invalid|unauthorized|unauthorised|forbidden|timeout|not found)\b`)

// extractReqPrefix extracts the spec_id prefix from a requirement-like ID
// (e.g. "01-REQ-1" → "01", "abc-PROP-1" → "abc"). It searches for markers
// -REQ-, -PROP-, -PATH-, -ERR- and returns the text before the first match.
// Returns "" if no marker is found.
func extractReqPrefix(entityID string) string {
	for _, marker := range []string{"-REQ-", "-PROP-", "-PATH-", "-ERR-"} {
		idx := strings.Index(entityID, marker)
		if idx > 0 {
			return entityID[:idx]
		}
	}
	return ""
}

// extractTestPrefix extracts the spec_id prefix from a test-like ID
// (e.g. "TS-01-1" → "01", "TS-abc-SMOKE-1" → "abc"). It strips the
// leading "TS-" then finds the first marker (-SMOKE-, -P, -E) or falls
// back to the last dash to isolate the prefix.
// Returns "" if no prefix can be extracted.
func extractTestPrefix(entityID string) string {
	if !strings.HasPrefix(entityID, "TS-") {
		return ""
	}
	remainder := entityID[3:]
	for _, marker := range []string{"-SMOKE-", "-P", "-E"} {
		idx := strings.Index(remainder, marker)
		if idx > 0 {
			return remainder[:idx]
		}
	}
	if lastDash := strings.LastIndex(remainder, "-"); lastDash > 0 {
		return remainder[:lastDash]
	}
	return ""
}

// ValidateCrossFile checks dangling references, coverage gaps, glossary
// completeness, and ID format validity across all artifacts and returns
// a ValidationResult.
func (s *Spec) ValidateCrossFile() ValidationResult {
	var errors []ValidationEntry
	var warnings []ValidationEntry

	// --- Completeness guard ---
	// If any artifact pointer is nil or has an empty SpecId, the spec is
	// incomplete and downstream cross-file checks would produce misleading
	// errors.  Return a single completeness error listing the incomplete
	// artifacts.
	{
		var incomplete []string
		if s.Requirements == nil {
			incomplete = append(incomplete, "requirements.json")
		} else if s.Requirements.SpecId == "" {
			incomplete = append(incomplete, "requirements")
		}
		if s.TestSpec == nil {
			incomplete = append(incomplete, "test_spec.json")
		} else if s.TestSpec.SpecId == "" {
			incomplete = append(incomplete, "test_spec")
		}
		if s.Tasks == nil {
			incomplete = append(incomplete, "tasks.json")
		} else if s.Tasks.SpecId == "" {
			incomplete = append(incomplete, "tasks")
		}
		if len(incomplete) > 0 {
			return ValidationResult{
				Valid: false,
				Errors: []ValidationEntry{{
					Category: "integrity",
					Check:    "completeness",
					Message:  fmt.Sprintf("Spec is incomplete: %s", strings.Join(incomplete, ", ")),
				}},
			}
		}
	}

	// Collect all known requirement IDs (including criterion IDs)
	reqIDs := map[string]bool{}
	topReqIDs := map[string]bool{}    // top-level requirement IDs only
	criterionIDs := map[string]bool{} // acceptance_criteria + edge_case IDs only
	if s.Requirements != nil {
		for _, req := range s.Requirements.Requirements {
			reqIDs[req.Id] = true
			topReqIDs[req.Id] = true
			for _, ac := range req.AcceptanceCriteria {
				reqIDs[ac.Id] = true
				criterionIDs[ac.Id] = true
			}
			for _, ec := range req.EdgeCases {
				reqIDs[ec.Id] = true
				criterionIDs[ec.Id] = true
			}
		}
	}

	// Collect all known execution path IDs
	pathIDs := map[string]bool{}
	if s.Requirements != nil {
		for _, ep := range s.Requirements.ExecutionPaths {
			pathIDs[ep.Id] = true
		}
	}

	// Collect all known correctness property IDs
	propIDs := map[string]bool{}
	if s.Requirements != nil {
		for _, cp := range s.Requirements.CorrectnessProperties {
			propIDs[cp.Id] = true
		}
	}

	// --- Cross-file rule 1: Traceability requirement_id resolution ---
	// Each traceability entry's requirement_id must resolve to a known
	// requirement, criterion (acceptance_criteria), or edge_case ID.
	if s.Tasks != nil {
		for _, te := range s.Tasks.Traceability {
			if te.RequirementId != "" && !reqIDs[te.RequirementId] {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "cross_file_1",
					Message:       fmt.Sprintf("traceability entry references requirement_id '%s' which does not exist in requirements", te.RequirementId),
					Artifact:      "tasks.json",
					RequirementID: te.RequirementId,
				})
			}
		}
	}

	// --- Cross-file rule 1: ErrorHandling requirement_id resolution ---
	// Each error_handling entry's requirement_id must resolve to a known
	// requirement, criterion, or edge_case ID.
	if s.Requirements != nil {
		for _, eh := range s.Requirements.ErrorHandling {
			if eh.RequirementId != "" && !reqIDs[eh.RequirementId] {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "cross_file_1",
					Message:       fmt.Sprintf("error handling entry %s references requirement_id '%s' which does not exist in requirements", eh.Id, eh.RequirementId),
					Artifact:      "requirements.json",
					RequirementID: eh.RequirementId,
					EntityID:      eh.Id,
				})
			}
		}
	}

	// --- ID format validation ---
	// Validates regex format, spec_id prefix match, and duplicate detection
	// within scoped seen sets (matching Python _validate_id_formats).
	if s.Requirements != nil {
		reqSpecID := s.Requirements.SpecId
		seenReqs := map[string]bool{}
		for _, req := range s.Requirements.Requirements {
			if !requirementIDPattern.MatchString(req.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("requirement ID %q does not match expected format NN-REQ-N[.N|.EN]", req.Id),
					Artifact: "requirements.json",
					EntityID: req.Id,
				})
			}
			if prefix := extractReqPrefix(req.Id); prefix != "" && prefix != reqSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("requirement ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", req.Id, prefix, reqSpecID),
					Artifact: "requirements.json",
					EntityID: req.Id,
				})
			}
			if seenReqs[req.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate requirement ID '%s'", req.Id),
					Artifact: "requirements.json",
					EntityID: req.Id,
				})
			}
			seenReqs[req.Id] = true

			seenCriteria := map[string]bool{}
			for _, ac := range req.AcceptanceCriteria {
				if !criterionIDPattern.MatchString(ac.Id) {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("criterion ID %q does not match expected format NN-REQ-N.N or NN-REQ-N.EN", ac.Id),
						Artifact: "requirements.json",
						EntityID: ac.Id,
					})
				}
				if prefix := extractReqPrefix(ac.Id); prefix != "" && prefix != reqSpecID {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("criterion ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", ac.Id, prefix, reqSpecID),
						Artifact: "requirements.json",
						EntityID: ac.Id,
					})
				}
				if seenCriteria[ac.Id] {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("Duplicate criterion ID '%s'", ac.Id),
						Artifact: "requirements.json",
						EntityID: ac.Id,
					})
				}
				seenCriteria[ac.Id] = true
			}

			seenEdgeCases := map[string]bool{}
			for _, ec := range req.EdgeCases {
				if !criterionIDPattern.MatchString(ec.Id) {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("edge case criterion ID %q does not match expected format", ec.Id),
						Artifact: "requirements.json",
						EntityID: ec.Id,
					})
				}
				if prefix := extractReqPrefix(ec.Id); prefix != "" && prefix != reqSpecID {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("edge_case ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", ec.Id, prefix, reqSpecID),
						Artifact: "requirements.json",
						EntityID: ec.Id,
					})
				}
				if seenEdgeCases[ec.Id] {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("Duplicate edge_case ID '%s'", ec.Id),
						Artifact: "requirements.json",
						EntityID: ec.Id,
					})
				}
				seenEdgeCases[ec.Id] = true
			}
		}
		seenProps := map[string]bool{}
		for _, cp := range s.Requirements.CorrectnessProperties {
			if !propertyIDPattern.MatchString(cp.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("correctness property ID %q does not match expected format NN-PROP-N", cp.Id),
					Artifact: "requirements.json",
					EntityID: cp.Id,
				})
			}
			if prefix := extractReqPrefix(cp.Id); prefix != "" && prefix != reqSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("property ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", cp.Id, prefix, reqSpecID),
					Artifact: "requirements.json",
					EntityID: cp.Id,
				})
			}
			if seenProps[cp.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate property ID '%s'", cp.Id),
					Artifact: "requirements.json",
					EntityID: cp.Id,
				})
			}
			seenProps[cp.Id] = true
			// Check that each validates entry resolves to a known criterion ID
			// (acceptance_criteria or edge_case). Top-level requirement IDs are
			// not valid targets (spec section 6.3).
			for _, vid := range cp.Validates {
				if !criterionIDs[vid] {
					errors = append(errors, ValidationEntry{
						Category:      "integrity",
						Check:         "validates_ref",
						Message:       fmt.Sprintf("correctness property '%s' validates unknown criterion ID '%s'", cp.Id, vid),
						Artifact:      "requirements.json",
						EntityID:      cp.Id,
						RequirementID: vid,
					})
				}
			}
		}
		seenPaths := map[string]bool{}
		for _, ep := range s.Requirements.ExecutionPaths {
			if !pathIDPattern.MatchString(ep.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("execution path ID %q does not match expected format NN-PATH-N", ep.Id),
					Artifact: "requirements.json",
					EntityID: ep.Id,
				})
			}
			if prefix := extractReqPrefix(ep.Id); prefix != "" && prefix != reqSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("path ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", ep.Id, prefix, reqSpecID),
					Artifact: "requirements.json",
					EntityID: ep.Id,
				})
			}
			if seenPaths[ep.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate path ID '%s'", ep.Id),
					Artifact: "requirements.json",
					EntityID: ep.Id,
				})
			}
			seenPaths[ep.Id] = true
		}
		seenErrors := map[string]bool{}
		for _, eh := range s.Requirements.ErrorHandling {
			if !errorHandlingIDPattern.MatchString(eh.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("error handling ID %q does not match expected format NN-ERR-N", eh.Id),
					Artifact: "requirements.json",
					EntityID: eh.Id,
				})
			}
			if prefix := extractReqPrefix(eh.Id); prefix != "" && prefix != reqSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("error ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", eh.Id, prefix, reqSpecID),
					Artifact: "requirements.json",
					EntityID: eh.Id,
				})
			}
			if seenErrors[eh.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate error ID '%s'", eh.Id),
					Artifact: "requirements.json",
					EntityID: eh.Id,
				})
			}
			seenErrors[eh.Id] = true
		}
	}

	// --- Test spec ID format validation and dangling reference checks ---
	coveredReqIDs := map[string]bool{}

	if s.TestSpec != nil {
		tsSpecID := s.TestSpec.SpecId
		seenTestCases := map[string]bool{}
		for _, tc := range s.TestSpec.TestCases {
			if !testCaseIDPattern.MatchString(tc.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("test case ID %q does not match expected format TS-NN-N", tc.Id),
					Artifact: "test_spec.json",
					EntityID: tc.Id,
				})
			}
			if prefix := extractTestPrefix(tc.Id); prefix != "" && prefix != tsSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("test_case ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", tc.Id, prefix, tsSpecID),
					Artifact: "test_spec.json",
					EntityID: tc.Id,
				})
			}
			if seenTestCases[tc.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate test_case ID '%s'", tc.Id),
					Artifact: "test_spec.json",
					EntityID: tc.Id,
				})
			}
			seenTestCases[tc.Id] = true
			// Check dangling requirement reference
			if tc.RequirementId != "" && !reqIDs[tc.RequirementId] {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "dangling_reference",
					Message:       fmt.Sprintf("test case %s references non-existent requirement %s", tc.Id, tc.RequirementId),
					Artifact:      "test_spec.json",
					RequirementID: tc.RequirementId,
					EntityID:      tc.Id,
				})
			}
			if tc.RequirementId != "" {
				coveredReqIDs[tc.RequirementId] = true
			}
		}

		seenPropertyTests := map[string]bool{}
		for _, pt := range s.TestSpec.PropertyTests {
			if !propertyTestIDPattern.MatchString(pt.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("property test ID %q does not match expected format TS-NN-PN", pt.Id),
					Artifact: "test_spec.json",
					EntityID: pt.Id,
				})
			}
			if prefix := extractTestPrefix(pt.Id); prefix != "" && prefix != tsSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("property_test ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", pt.Id, prefix, tsSpecID),
					Artifact: "test_spec.json",
					EntityID: pt.Id,
				})
			}
			if seenPropertyTests[pt.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate property_test ID '%s'", pt.Id),
					Artifact: "test_spec.json",
					EntityID: pt.Id,
				})
			}
			seenPropertyTests[pt.Id] = true
			// Check dangling property reference
			if pt.PropertyId != "" && !propIDs[pt.PropertyId] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "dangling_reference",
					Message:  fmt.Sprintf("property test %s references non-existent property %s", pt.Id, pt.PropertyId),
					Artifact: "test_spec.json",
					EntityID: pt.Id,
				})
			}
		}

		seenEdgeCaseTests := map[string]bool{}
		for _, ec := range s.TestSpec.EdgeCaseTests {
			if !edgeCaseTestIDPattern.MatchString(ec.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("edge case test ID %q does not match expected format TS-NN-EN", ec.Id),
					Artifact: "test_spec.json",
					EntityID: ec.Id,
				})
			}
			if prefix := extractTestPrefix(ec.Id); prefix != "" && prefix != tsSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("edge_case_test ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", ec.Id, prefix, tsSpecID),
					Artifact: "test_spec.json",
					EntityID: ec.Id,
				})
			}
			if seenEdgeCaseTests[ec.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate edge_case_test ID '%s'", ec.Id),
					Artifact: "test_spec.json",
					EntityID: ec.Id,
				})
			}
			seenEdgeCaseTests[ec.Id] = true
			// Check dangling requirement reference
			if ec.RequirementId != "" && !reqIDs[ec.RequirementId] {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "dangling_reference",
					Message:       fmt.Sprintf("edge case test %s references non-existent requirement %s", ec.Id, ec.RequirementId),
					Artifact:      "test_spec.json",
					RequirementID: ec.RequirementId,
					EntityID:      ec.Id,
				})
			}
			if ec.RequirementId != "" {
				coveredReqIDs[ec.RequirementId] = true
			}
		}

		seenSmokeTests := map[string]bool{}
		for _, sm := range s.TestSpec.SmokeTests {
			if !smokeTestIDPattern.MatchString(sm.Id) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("smoke test ID %q does not match expected format TS-NN-SMOKE-N", sm.Id),
					Artifact: "test_spec.json",
					EntityID: sm.Id,
				})
			}
			if prefix := extractTestPrefix(sm.Id); prefix != "" && prefix != tsSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("smoke_test ID '%s' has spec_id prefix '%s' but artifact spec_id is '%s'", sm.Id, prefix, tsSpecID),
					Artifact: "test_spec.json",
					EntityID: sm.Id,
				})
			}
			if seenSmokeTests[sm.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("Duplicate smoke_test ID '%s'", sm.Id),
					Artifact: "test_spec.json",
					EntityID: sm.Id,
				})
			}
			seenSmokeTests[sm.Id] = true
			// Check dangling execution path reference
			if sm.ExecutionPathId != "" && !pathIDs[sm.ExecutionPathId] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "dangling_reference",
					Message:  fmt.Sprintf("smoke test %s references non-existent execution path %s", sm.Id, sm.ExecutionPathId),
					Artifact: "test_spec.json",
					EntityID: sm.Id,
				})
			}
		}
	}

	// --- Tasks: subtask and verification ID format validation ---
	// Validates subtask IDs match {group}.{N} (^\d+\.\d+$) and also checks
	// that the numeric prefix matches the parent group's ID. Validates
	// verification subtask IDs match {group}.V (^\d+\.V$).
	if s.Tasks != nil {
		for gIdx, group := range s.Tasks.TaskGroups {
			groupIDStr := fmt.Sprintf("%d", group.Id)

			for sIdx, sub := range group.Subtasks {
				if sub.Id == "" {
					continue
				}
				if !subtaskIDPattern.MatchString(sub.Id) {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "id_format",
						Message:  fmt.Sprintf("subtask ID %q does not match expected format {group}.{N}", sub.Id),
						Artifact: "tasks.json",
						EntityID: sub.Id,
						Path:     fmt.Sprintf("task_groups[%d].subtasks[%d].id", gIdx, sIdx),
					})
				} else {
					// Verify the numeric prefix matches the parent group ID.
					if prefix, _, ok := strings.Cut(sub.Id, "."); ok && prefix != groupIDStr {
						errors = append(errors, ValidationEntry{
							Category: "integrity",
							Check:    "id_format",
							Message:  fmt.Sprintf("subtask ID %q has group prefix %q but parent group ID is %q", sub.Id, prefix, groupIDStr),
							Artifact: "tasks.json",
							EntityID: sub.Id,
							Path:     fmt.Sprintf("task_groups[%d].subtasks[%d].id", gIdx, sIdx),
						})
					}
				}
			}

			// Check verification subtask ID format.
			verID := group.Verification.Id
			if verID != "" && !verificationIDPattern.MatchString(verID) {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "id_format",
					Message:  fmt.Sprintf("verification ID %q does not match expected format {group}.V", verID),
					Artifact: "tasks.json",
					EntityID: verID,
					Path:     fmt.Sprintf("task_groups[%d].verification.id", gIdx),
				})
			}
		}
	}

	// --- Coverage gap errors ---
	// Check all acceptance criteria and edge case criteria for test coverage.
	// Coverage gaps are blocking errors (matching Python cross-file-2 behavior).
	if s.Requirements != nil {
		for _, req := range s.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				if !coveredReqIDs[ac.Id] {
					errors = append(errors, ValidationEntry{
						Category:      "integrity",
						Check:         "coverage_gap",
						Message:       fmt.Sprintf("requirement criterion %s has no test coverage", ac.Id),
						Artifact:      "test_spec.json",
						RequirementID: ac.Id,
						EntityID:      ac.Id,
					})
				}
			}
			for _, ec := range req.EdgeCases {
				if !coveredReqIDs[ec.Id] {
					errors = append(errors, ValidationEntry{
						Category:      "integrity",
						Check:         "coverage_gap",
						Message:       fmt.Sprintf("edge case criterion %s has no test coverage", ec.Id),
						Artifact:      "test_spec.json",
						RequirementID: ec.Id,
						EntityID:      ec.Id,
					})
				}
			}
		}
	}

	// --- No criteria check ---
	// Requirements with no acceptance_criteria AND no edge_cases are errors
	// (matching Python cross-file-2 behavior).
	if s.Requirements != nil {
		for _, req := range s.Requirements.Requirements {
			if len(req.AcceptanceCriteria) == 0 && len(req.EdgeCases) == 0 {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "no_criteria",
					Message:       fmt.Sprintf("requirement %s has no acceptance criteria and no edge cases", req.Id),
					Artifact:      "requirements.json",
					RequirementID: req.Id,
					EntityID:      req.Id,
				})
			}
		}
	}

	// --- Cross-file rule 3: Property test coverage ---
	// Each correctness_property must have a matching property_test (by property_id).
	if s.Requirements != nil && len(s.Requirements.CorrectnessProperties) > 0 {
		// Build set of covered property_ids from property_tests
		coveredPropertyIDs := map[string]bool{}
		if s.TestSpec != nil {
			for _, pt := range s.TestSpec.PropertyTests {
				coveredPropertyIDs[pt.PropertyId] = true
			}
		}
		for i, cp := range s.Requirements.CorrectnessProperties {
			if !coveredPropertyIDs[cp.Id] {
				errors = append(errors, ValidationEntry{
					Category:      "integrity",
					Check:         "cross_file_3",
					Message:       fmt.Sprintf("correctness property %s has no matching property_test", cp.Id),
					Artifact:      "requirements.json",
					Path:          fmt.Sprintf("requirements.correctness_properties[%d]", i),
					RequirementID: cp.Id,
				})
			}
		}
	}

	// --- Cross-file rule 4: Execution path smoke test coverage ---
	// Each execution_path must have a matching smoke_test (by execution_path_id).
	if s.Requirements != nil && len(s.Requirements.ExecutionPaths) > 0 {
		// Build set of covered execution_path_ids from smoke_tests
		coveredPathIDs := map[string]bool{}
		if s.TestSpec != nil {
			for _, sm := range s.TestSpec.SmokeTests {
				coveredPathIDs[sm.ExecutionPathId] = true
			}
		}
		for _, ep := range s.Requirements.ExecutionPaths {
			if !coveredPathIDs[ep.Id] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_4",
					Message:  fmt.Sprintf("execution path %s has no matching smoke_test", ep.Id),
					Artifact: "requirements.json",
					EntityID: ep.Id,
				})
			}
		}
	}

	// --- Cross-file rule 5: test_spec_id resolution ---
	// Every test_spec_id in traceability entries and subtask test_spec_refs
	// must resolve to a known test entry in test_spec.
	{
		// Build set of all known test IDs from test_spec
		knownTestIDs := map[string]bool{}
		if s.TestSpec != nil {
			for _, tc := range s.TestSpec.TestCases {
				knownTestIDs[tc.Id] = true
			}
			for _, pt := range s.TestSpec.PropertyTests {
				knownTestIDs[pt.Id] = true
			}
			for _, ec := range s.TestSpec.EdgeCaseTests {
				knownTestIDs[ec.Id] = true
			}
			for _, sm := range s.TestSpec.SmokeTests {
				knownTestIDs[sm.Id] = true
			}
		}

		// Check traceability entries
		if s.Tasks != nil {
			for _, te := range s.Tasks.Traceability {
				if te.TestSpecId != "" && !knownTestIDs[te.TestSpecId] {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "cross_file_5",
						Message:  fmt.Sprintf("traceability entry references unresolvable test_spec_id %s", te.TestSpecId),
						Artifact: "tasks.json",
						EntityID: te.TestSpecId,
					})
				}
			}

			// Check subtask test_spec_refs
			for _, group := range s.Tasks.TaskGroups {
				for _, sub := range group.Subtasks {
					for _, ref := range sub.TestSpecRefs {
						if ref != "" && !knownTestIDs[ref] {
							errors = append(errors, ValidationEntry{
								Category: "integrity",
								Check:    "cross_file_5",
								Message:  fmt.Sprintf("subtask %s test_spec_refs contains unresolvable test_spec_id %s", sub.Id, ref),
								Artifact: "tasks.json",
								EntityID: ref,
							})
						}
					}
				}
			}
		}
	}

	// --- Cross-file rule 6: Glossary backtick term check ---
	// Extract backtick-wrapped terms from criterion and correctness property
	// fields; flag any non-excluded term not present in the glossary.
	if s.Requirements != nil {
		glossary := s.Requirements.Glossary

		// isExcludedBacktickTerm returns true if the term should be excluded
		// from glossary checks: numeric, single character, quoted, or > 80 chars.
		isExcluded := func(term string) bool {
			if len([]rune(term)) == 1 {
				return true
			}
			if len([]rune(term)) > 80 {
				return true
			}
			if backtickNumericRe.MatchString(term) {
				return true
			}
			if backtickQuotedRe.MatchString(term) {
				return true
			}
			return false
		}

		// checkFieldForBacktickTerms extracts backtick terms from a field value
		// and appends validation errors for undefined glossary terms.
		checkField := func(fieldValue string) {
			matches := backtickTermRe.FindAllStringSubmatch(fieldValue, -1)
			for _, m := range matches {
				term := m[1]
				if isExcluded(term) {
					continue
				}
				if _, ok := glossary[term]; !ok {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "cross_file_6",
						Message:  fmt.Sprintf("backtick term %q is not defined in glossary", term),
						Artifact: "requirements.json",
					})
				}
			}
		}

		// Check criterion fields across all requirements
		for _, req := range s.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				checkField(ac.Action)
				if ac.Trigger != nil {
					checkField(*ac.Trigger)
				}
				if ac.Condition != nil {
					checkField(*ac.Condition)
				}
				if ac.ErrorCondition != nil {
					checkField(*ac.ErrorCondition)
				}
				if ac.State != nil {
					checkField(*ac.State)
				}
				if ac.Feature != nil {
					checkField(*ac.Feature)
				}
			}
			for _, ec := range req.EdgeCases {
				checkField(ec.Action)
				if ec.Trigger != nil {
					checkField(*ec.Trigger)
				}
				if ec.Condition != nil {
					checkField(*ec.Condition)
				}
				if ec.ErrorCondition != nil {
					checkField(*ec.ErrorCondition)
				}
				if ec.State != nil {
					checkField(*ec.State)
				}
				if ec.Feature != nil {
					checkField(*ec.Feature)
				}
			}
		}

		// Check correctness property fields (for_any, invariant)
		for _, cp := range s.Requirements.CorrectnessProperties {
			checkField(cp.ForAny)
			checkField(cp.Invariant)
		}
	}

	// --- Cross-file rule 8: Traceability deduplication ---
	// Flag duplicate (requirement_id, test_spec_id) pairs in traceability.
	if s.Tasks != nil && len(s.Tasks.Traceability) > 0 {
		seen := map[string]bool{}
		for _, te := range s.Tasks.Traceability {
			key := te.RequirementId + "|" + te.TestSpecId
			if seen[key] {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_8",
					Message:  fmt.Sprintf("duplicate traceability pair (%s, %s)", te.RequirementId, te.TestSpecId),
					Artifact: "tasks.json",
				})
			} else {
				seen[key] = true
			}
		}
	}

	// --- Cross-file rule 9: Subtask requirement_refs resolution ---
	// Every requirement_refs entry must resolve to a known requirement ID,
	// criterion ID, or edge case ID.
	if s.Tasks != nil && s.Requirements != nil {
		for _, group := range s.Tasks.TaskGroups {
			for _, sub := range group.Subtasks {
				for _, ref := range sub.RequirementRefs {
					if ref != "" && !reqIDs[ref] {
						errors = append(errors, ValidationEntry{
							Category: "integrity",
							Check:    "cross_file_9",
							Message:  fmt.Sprintf("subtask %s requirement_refs contains unresolvable reference %s", sub.Id, ref),
							Artifact: "tasks.json",
							EntityID: ref,
						})
					}
				}
			}
		}
	}

	// --- Cross-file rule 10: Unwanted pattern return_contract check ---
	// Every criterion with ears_pattern='unwanted' must have a non-empty
	// return_contract.
	if s.Requirements != nil {
		for _, req := range s.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				if ac.EarsPattern == CriterionEarsPatternUnwanted {
					if ac.ReturnContract == nil || *ac.ReturnContract == "" {
						errors = append(errors, ValidationEntry{
							Category:      "integrity",
							Check:         "cross_file_10",
							Message:       fmt.Sprintf("criterion %s has ears_pattern 'unwanted' but missing return_contract", ac.Id),
							Artifact:      "requirements.json",
							RequirementID: req.Id,
							EntityID:      ac.Id,
						})
					}
				}
			}
			for _, ec := range req.EdgeCases {
				if ec.EarsPattern == CriterionEarsPatternUnwanted {
					if ec.ReturnContract == nil || *ec.ReturnContract == "" {
						errors = append(errors, ValidationEntry{
							Category:      "integrity",
							Check:         "cross_file_10",
							Message:       fmt.Sprintf("criterion %s has ears_pattern 'unwanted' but missing return_contract", ec.Id),
							Artifact:      "requirements.json",
							RequirementID: req.Id,
							EntityID:      ec.Id,
						})
					}
				}
			}
		}
	}

	// --- Task group structure validation (04-REQ-8) ---
	// First group must have kind='tests'; last group must have kind='wiring_verification'.
	// Skip if no task groups exist.
	if s.Tasks != nil && len(s.Tasks.TaskGroups) > 0 {
		groups := s.Tasks.TaskGroups
		if groups[0].Kind != TaskGroupKindTests {
			errors = append(errors, ValidationEntry{
				Category: "schema",
				Check:    "task_group_structure",
				Message:  fmt.Sprintf("first task group must have kind 'tests', got '%s'", groups[0].Kind),
				Artifact: "tasks.json",
			})
		}
		if groups[len(groups)-1].Kind != TaskGroupKindWiringVerification {
			errors = append(errors, ValidationEntry{
				Category: "schema",
				Check:    "task_group_structure",
				Message:  fmt.Sprintf("last task group must have kind 'wiring_verification', got '%s'", groups[len(groups)-1].Kind),
				Artifact: "tasks.json",
			})
		}
		// Count wiring_verification groups; no more than one is allowed (spec 8.3).
		wiringCount := 0
		for _, g := range groups {
			if g.Kind == TaskGroupKindWiringVerification {
				wiringCount++
			}
		}
		if wiringCount > 1 {
			errors = append(errors, ValidationEntry{
				Category: "schema",
				Check:    "task_group_structure",
				Message:  fmt.Sprintf("at most one wiring_verification group is allowed, found %d", wiringCount),
				Artifact: "tasks.json",
			})
		}
	}

	// --- Wiring verification group semantics (04-REQ-9) ---
	// Check wiring_verification group for meaningful content:
	//   A. at least one subtask has non-empty test_spec_refs
	//   B. at least one test_spec_refs entry matches TS-*-SMOKE-*
	//   C. at least one subtask title or details mentions 'stub' or 'dead'
	//
	// Checks A and B are only enforced when the spec has at least one smoke
	// test. A freshly scaffolded spec (spec new) has no smoke tests yet, so
	// these checks are deferred per section 3.3 (bootstrap mode). Check C
	// always runs because wiring_verification must acknowledge stub removal.
	if s.Tasks != nil && len(s.Tasks.TaskGroups) > 0 {
		lastGroup := s.Tasks.TaskGroups[len(s.Tasks.TaskGroups)-1]
		if lastGroup.Kind == TaskGroupKindWiringVerification {
			hasSmokeTests := s.TestSpec != nil && len(s.TestSpec.SmokeTests) > 0

			if hasSmokeTests {
				// Sub-check A: at least one subtask has non-empty test_spec_refs
				hasRefs := false
				for _, sub := range lastGroup.Subtasks {
					if len(sub.TestSpecRefs) > 0 {
						hasRefs = true
						break
					}
				}
				if !hasRefs {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "wiring_verification",
						Message:  "wiring_verification group: no subtask has non-empty test_spec_refs",
						Artifact: "tasks.json",
					})
				}

				// Sub-check B: at least one test_spec_refs entry matches TS-*-SMOKE-*
				hasSmokeRef := false
				for _, sub := range lastGroup.Subtasks {
					for _, ref := range sub.TestSpecRefs {
						if wiringSmokeRefPattern.MatchString(ref) {
							hasSmokeRef = true
							break
						}
					}
					if hasSmokeRef {
						break
					}
				}
				if !hasSmokeRef {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "wiring_verification",
						Message:  "wiring_verification group: no test_spec_refs entry matches smoke test pattern TS-*-SMOKE-*",
						Artifact: "tasks.json",
					})
				}
			}

			// Sub-check C: at least one subtask title/details or verification check
			// mentions stub or dead-code (using regex aligned with Python's pattern).
			hasStubMention := false
			for _, sub := range lastGroup.Subtasks {
				if stubDeadCodeRe.MatchString(sub.Title) {
					hasStubMention = true
					break
				}
				for _, detail := range sub.Details {
					if stubDeadCodeRe.MatchString(detail) {
						hasStubMention = true
						break
					}
				}
				if hasStubMention {
					break
				}
			}
			// Fallback: check Verification.Checks of the wiring group
			if !hasStubMention {
				for _, check := range lastGroup.Verification.Checks {
					if stubDeadCodeRe.MatchString(check) {
						hasStubMention = true
						break
					}
				}
			}
			if !hasStubMention {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "wiring_verification",
					Message:  "wiring_verification group: no subtask mentions stub or dead-code audit in title or details",
					Artifact: "tasks.json",
				})
			}
		}
	}

	// --- Cross-file rule 7: Spec ID/name consistency across artifacts ---
	// Compare each artifact's spec_id and spec_name against the PRD
	// frontmatter values (s.SpecID and s.SpecName). These fields are
	// populated from prd.md frontmatter by LoadSpec.
	{
		prdSpecID := s.SpecID
		prdSpecName := s.SpecName

		if s.Requirements != nil {
			if s.Requirements.SpecId != prdSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_id mismatch: prd.md has '%s' but requirements.json has '%s'", prdSpecID, s.Requirements.SpecId),
					Artifact: "requirements.json",
				})
			}
			if s.Requirements.SpecName != prdSpecName {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_name mismatch: prd.md has '%s' but requirements.json has '%s'", prdSpecName, s.Requirements.SpecName),
					Artifact: "requirements.json",
				})
			}
		}
		if s.TestSpec != nil {
			if s.TestSpec.SpecId != prdSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_id mismatch: prd.md has '%s' but test_spec.json has '%s'", prdSpecID, s.TestSpec.SpecId),
					Artifact: "test_spec.json",
				})
			}
			if s.TestSpec.SpecName != prdSpecName {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_name mismatch: prd.md has '%s' but test_spec.json has '%s'", prdSpecName, s.TestSpec.SpecName),
					Artifact: "test_spec.json",
				})
			}
		}
		if s.Tasks != nil {
			if s.Tasks.SpecId != prdSpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_id mismatch: prd.md has '%s' but tasks.json has '%s'", prdSpecID, s.Tasks.SpecId),
					Artifact: "tasks.json",
				})
			}
			if s.Tasks.SpecName != prdSpecName {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_file_7",
					Message:  fmt.Sprintf("spec_name mismatch: prd.md has '%s' but tasks.json has '%s'", prdSpecName, s.Tasks.SpecName),
					Artifact: "tasks.json",
				})
			}
		}
	}

	// --- Folder-name rule: spec_id and spec_name must match the folder prefix/suffix ---
	// When the spec was loaded from a directory whose name follows the
	// NN_snake_case pattern, the numeric prefix must equal SpecID and the
	// snake_case suffix must equal SpecName. If the directory name does not
	// match the pattern (e.g. ad-hoc or non-spec directories), this check
	// is skipped so that non-spec directories are not rejected.
	if s.Dir != "" {
		dirBase := filepath.Base(s.Dir)
		if IsSpecDirName(dirBase) {
			folderPrefix, folderSuffix, _ := ParseSpecDirName(dirBase)
			if folderPrefix != s.SpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "folder_name",
					Message:  fmt.Sprintf("spec_id '%s' does not match folder prefix '%s' in '%s'", s.SpecID, folderPrefix, dirBase),
					Artifact: "prd.md",
				})
			}
			if folderSuffix != s.SpecName {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "folder_name",
					Message:  fmt.Sprintf("spec_name '%s' does not match folder suffix '%s' in '%s'", s.SpecName, folderSuffix, dirBase),
					Artifact: "prd.md",
				})
			}
		}
	}

	return ValidationResult{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: warnings,
	}
}

// ---------------------------------------------------------------------------
// Spec.Validate
// ---------------------------------------------------------------------------

// Validate runs schema validation, EARS constraint checks, task group
// structure rules, and cross-file consistency checks, then returns a
// ValidationResult. Valid is true if and only if Errors is empty.
func (s *Spec) Validate() ValidationResult {
	schemaResult := s.ValidateSchema()
	crossFileResult := s.ValidateCrossFile()

	var allErrors []ValidationEntry
	allErrors = append(allErrors, schemaResult.Errors...)
	allErrors = append(allErrors, crossFileResult.Errors...)

	var allWarnings []ValidationEntry
	allWarnings = append(allWarnings, schemaResult.Warnings...)
	allWarnings = append(allWarnings, crossFileResult.Warnings...)

	// Check for subtasks with missing refs (produces warnings, not errors).
	if s.Tasks != nil {
		for _, group := range s.Tasks.TaskGroups {
			allWarnings = append(allWarnings, checkMissingSubtaskRefs(group)...)
		}
	}

	// --- Warning: group test_spec_refs ceiling (04-REQ-14) ---
	// For each task group, sum test_spec_refs across all subtasks; warn if > 15.
	if s.Tasks != nil {
		for _, group := range s.Tasks.TaskGroups {
			total := 0
			for _, sub := range group.Subtasks {
				total += len(sub.TestSpecRefs)
			}
			if total > 15 {
				allWarnings = append(allWarnings, ValidationEntry{
					Category: "warning",
					Message:  fmt.Sprintf("task group %d has %d total test_spec_refs across subtasks (exceeds 15)", group.Id, total),
					EntityID: fmt.Sprintf("%d", group.Id),
				})
			}
		}
	}

	// --- Warning: group subtask count (04-REQ-15) ---
	// For each task group, count non-verification subtasks; warn if > 6.
	// The Verification field on TaskGroup is the verification subtask;
	// group.Subtasks contains only non-verification subtasks.
	if s.Tasks != nil {
		for _, group := range s.Tasks.TaskGroups {
			count := len(group.Subtasks)
			if count > 6 {
				allWarnings = append(allWarnings, ValidationEntry{
					Category: "warning",
					Message:  fmt.Sprintf("task group %d has %d non-verification subtasks (exceeds 6)", group.Id, count),
					EntityID: fmt.Sprintf("%d", group.Id),
				})
			}
		}
	}

	// --- Warning: subtask test_spec_refs ceiling (04-REQ-16) ---
	// For each subtask, warn if test_spec_refs count > 8.
	if s.Tasks != nil {
		for _, group := range s.Tasks.TaskGroups {
			for _, sub := range group.Subtasks {
				if len(sub.TestSpecRefs) > 8 {
					allWarnings = append(allWarnings, ValidationEntry{
						Category: "warning",
						Message:  fmt.Sprintf("subtask %s has %d test_spec_refs (exceeds 8)", sub.Id, len(sub.TestSpecRefs)),
						EntityID: sub.Id,
					})
				}
			}
		}
	}

	// --- Warning: error path return_contract (04-REQ-17) ---
	// For each criterion with a non-null error_condition or error-indicating
	// keywords in action, warn if return_contract is null.
	if s.Requirements != nil {
		for _, req := range s.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				hasErrorIndicator := ac.ErrorCondition != nil && *ac.ErrorCondition != ""
				if !hasErrorIndicator {
					hasErrorIndicator = errorKeywordRe.MatchString(ac.Action)
				}
				if hasErrorIndicator && (ac.ReturnContract == nil || *ac.ReturnContract == "") {
					allWarnings = append(allWarnings, ValidationEntry{
						Category: "warning",
						Message:  fmt.Sprintf("criterion %s has error path but missing return_contract", ac.Id),
						EntityID: ac.Id,
					})
				}
			}
			for _, ec := range req.EdgeCases {
				hasErrorIndicator := ec.ErrorCondition != nil && *ec.ErrorCondition != ""
				if !hasErrorIndicator {
					hasErrorIndicator = errorKeywordRe.MatchString(ec.Action)
				}
				if hasErrorIndicator && (ec.ReturnContract == nil || *ec.ReturnContract == "") {
					allWarnings = append(allWarnings, ValidationEntry{
						Category: "warning",
						Message:  fmt.Sprintf("criterion %s has error path but missing return_contract", ec.Id),
						EntityID: ec.Id,
					})
				}
			}
		}
	}

	// --- Warning: vague language detection (04-REQ-18) ---
	// Scan criterion fields for vague words; one warning per occurrence.
	if s.Requirements != nil {
		type fieldEntry struct {
			name  string
			value string
		}
		for _, req := range s.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				fields := []fieldEntry{
					{"action", ac.Action},
				}
				if ac.Trigger != nil {
					fields = append(fields, fieldEntry{"trigger", *ac.Trigger})
				}
				if ac.Condition != nil {
					fields = append(fields, fieldEntry{"condition", *ac.Condition})
				}
				if ac.ErrorCondition != nil {
					fields = append(fields, fieldEntry{"error_condition", *ac.ErrorCondition})
				}
				if ac.State != nil {
					fields = append(fields, fieldEntry{"state", *ac.State})
				}
				if ac.Feature != nil {
					fields = append(fields, fieldEntry{"feature", *ac.Feature})
				}
				for _, f := range fields {
					matches := vagueLanguageRe.FindAllString(f.value, -1)
					for _, match := range matches {
						allWarnings = append(allWarnings, ValidationEntry{
							Category: "warning",
							Message:  fmt.Sprintf("vague term %q in field %s of criterion %s", strings.ToLower(match), f.name, ac.Id),
							EntityID: ac.Id,
						})
					}
				}
			}
			for _, ec := range req.EdgeCases {
				fields := []fieldEntry{
					{"action", ec.Action},
				}
				if ec.Trigger != nil {
					fields = append(fields, fieldEntry{"trigger", *ec.Trigger})
				}
				if ec.Condition != nil {
					fields = append(fields, fieldEntry{"condition", *ec.Condition})
				}
				if ec.ErrorCondition != nil {
					fields = append(fields, fieldEntry{"error_condition", *ec.ErrorCondition})
				}
				if ec.State != nil {
					fields = append(fields, fieldEntry{"state", *ec.State})
				}
				if ec.Feature != nil {
					fields = append(fields, fieldEntry{"feature", *ec.Feature})
				}
				for _, f := range fields {
					matches := vagueLanguageRe.FindAllString(f.value, -1)
					for _, match := range matches {
						allWarnings = append(allWarnings, ValidationEntry{
							Category: "warning",
							Message:  fmt.Sprintf("vague term %q in field %s of criterion %s", strings.ToLower(match), f.name, ec.Id),
							EntityID: ec.Id,
						})
					}
				}
			}
		}
		// Scan error_handling entries' Behavior field for vague language.
		for _, eh := range s.Requirements.ErrorHandling {
			matches := vagueLanguageRe.FindAllString(eh.Behavior, -1)
			for _, match := range matches {
				allWarnings = append(allWarnings, ValidationEntry{
					Category: "warning",
					Message:  fmt.Sprintf("vague term %q in field behavior of error handling %s", strings.ToLower(match), eh.Id),
					EntityID: eh.Id,
				})
			}
		}
	}

	// --- Warning: spec scope limit (04-REQ-19) ---
	// Warn when a spec has more than 10 requirements.
	if s.Requirements != nil {
		count := len(s.Requirements.Requirements)
		if count > 10 {
			allWarnings = append(allWarnings, ValidationEntry{
				Category: "warning",
				Message:  fmt.Sprintf("spec has %d requirements; consider splitting into smaller specs — spec may be too large", count),
				EntityID: s.SpecID,
			})
		}
	}

	return ValidationResult{
		Valid:    len(allErrors) == 0,
		Errors:   allErrors,
		Warnings: allWarnings,
	}
}

// ---------------------------------------------------------------------------
// ValidateCrossSpec
// ---------------------------------------------------------------------------

// ValidateCrossSpec checks API symbol consistency, glossary conflicts,
// and contract mismatches across all provided specs using the dependency
// graph to determine which specs interact, and returns a ValidationResult.
func ValidateCrossSpec(specs []*Spec, graph *DependencyGraph) ValidationResult {
	var errors []ValidationEntry

	if len(specs) <= 1 {
		return ValidationResult{Valid: true}
	}

	// Build spec-by-ID lookup map for cross-spec checks.
	specByID := make(map[string]*Spec, len(specs))
	for _, s := range specs {
		specByID[s.SpecID] = s
	}

	type specPair struct{ a, b string }

	// Check glossary conflicts between ALL spec pairs (not just
	// dependency-connected), matching the Python cross-spec-2 semantics.
	for i := 0; i < len(specs); i++ {
		for j := i + 1; j < len(specs); j++ {
			specA := specs[i]
			specB := specs[j]

			if specA.Requirements == nil || specB.Requirements == nil {
				continue
			}

			// Check glossary conflicts
			for term, defA := range specA.Requirements.Glossary {
				if defB, ok := specB.Requirements.Glossary[term]; ok && defA != defB {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "glossary_conflict",
						Message:  fmt.Sprintf("glossary term %q has conflicting definitions between spec %s and spec %s", term, specA.SpecID, specB.SpecID),
						EntityID: term,
					})
				}
			}
		}
	}

	// --- Cross-spec rule 1: Duplicate API symbol signature check ---
	// For each pair of specs connected by a DependencyEdge, compare their
	// external API symbol signatures. If a symbol name appears in both specs
	// but with different signatures, produce an integrity error.
	if graph != nil {
		processedRule1 := map[specPair]bool{}
		for _, edge := range graph.Edges {
			pair := specPair{edge.FromSpec, edge.ToSpec}
			rpair := specPair{edge.ToSpec, edge.FromSpec}
			if processedRule1[pair] || processedRule1[rpair] {
				continue
			}
			processedRule1[pair] = true

			sA := specByID[edge.FromSpec]
			sB := specByID[edge.ToSpec]
			if sA == nil || sB == nil || sA.Requirements == nil || sB.Requirements == nil {
				continue
			}

			// Collect symbol signatures from spec A
			symbolsA := map[string]string{} // name -> signature
			for _, api := range sA.Requirements.ExternalApis {
				for _, sym := range api.Symbols {
					symbolsA[sym.Name] = sym.Signature
				}
			}

			// Compare against spec B symbols
			for _, api := range sB.Requirements.ExternalApis {
				for _, sym := range api.Symbols {
					if sigA, ok := symbolsA[sym.Name]; ok && sigA != sym.Signature {
						errors = append(errors, ValidationEntry{
							Category: "integrity",
							Check:    "cross_spec_1",
							Message:  fmt.Sprintf("API symbol %q has mismatched signatures between spec %s (%s) and spec %s (%s)", sym.Name, edge.FromSpec, sigA, edge.ToSpec, sym.Signature),
						})
					}
				}
			}
		}
	}

	// --- Cross-spec rule 3: Unknown dependency check ---
	// For each spec, inspect all task dependency depends_on_spec values.
	// If a value does not match any spec ID in the provided set, produce
	// an integrity error.
	for _, spec := range specs {
		if spec.Tasks == nil {
			continue
		}
		for _, dep := range spec.Tasks.Dependencies {
			if _, ok := specByID[dep.DependsOnSpec]; !ok {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_spec_3",
					Message:  fmt.Sprintf("spec %s references unknown dependency spec_id %q", spec.SpecID, dep.DependsOnSpec),
				})
			}
		}
	}

	// --- Cross-spec rule 4: Interface contract mismatch ---
	// Extract backtick-wrapped terms from criterion action fields paired with
	// their return_contract values as sets. Along each DependencyEdge, compare
	// shared symbol contract sets between upstream and downstream specs.
	// Error only when the sets are disjoint (no common contract value),
	// matching Python's cross-spec-4 semantics.
	if graph != nil {
		// Build per-spec maps of backtick term -> set of return_contracts.
		// Only include terms where the criterion has a non-nil return_contract.
		specContracts := make(map[string]map[string]map[string]bool) // specID -> (term -> set of contracts)
		for _, spec := range specs {
			if spec.Requirements == nil {
				continue
			}
			contracts := map[string]map[string]bool{}
			for _, req := range spec.Requirements.Requirements {
				for _, ac := range req.AcceptanceCriteria {
					if ac.ReturnContract == nil {
						continue
					}
					matches := backtickTermRe.FindAllStringSubmatch(ac.Action, -1)
					for _, m := range matches {
						if contracts[m[1]] == nil {
							contracts[m[1]] = map[string]bool{}
						}
						contracts[m[1]][string(*ac.ReturnContract)] = true
					}
				}
				for _, ec := range req.EdgeCases {
					if ec.ReturnContract == nil {
						continue
					}
					matches := backtickTermRe.FindAllStringSubmatch(ec.Action, -1)
					for _, m := range matches {
						if contracts[m[1]] == nil {
							contracts[m[1]] = map[string]bool{}
						}
						contracts[m[1]][string(*ec.ReturnContract)] = true
					}
				}
			}
			specContracts[spec.SpecID] = contracts
		}

		// Check along dependency edges for disjoint contract sets.
		processedRule4 := map[specPair]bool{}
		for _, edge := range graph.Edges {
			pair := specPair{edge.FromSpec, edge.ToSpec}
			rpair := specPair{edge.ToSpec, edge.FromSpec}
			if processedRule4[pair] || processedRule4[rpair] {
				continue
			}
			processedRule4[pair] = true

			upstreamContracts := specContracts[edge.FromSpec]
			downstreamContracts := specContracts[edge.ToSpec]
			if len(upstreamContracts) == 0 || len(downstreamContracts) == 0 {
				continue
			}

			for term, upSet := range upstreamContracts {
				downSet, ok := downstreamContracts[term]
				if !ok {
					continue
				}
				// Check if sets overlap (any common value)
				hasOverlap := false
				for contract := range upSet {
					if downSet[contract] {
						hasOverlap = true
						break
					}
				}
				if !hasOverlap {
					errors = append(errors, ValidationEntry{
						Category: "integrity",
						Check:    "cross_spec_4",
						Message:  fmt.Sprintf("symbol %q has mismatched return_contract between spec %s and spec %s", term, edge.FromSpec, edge.ToSpec),
					})
				}
			}
		}
	}

	// --- Cross-spec rule 5: Missing actor reference in downstream spec ---
	// For each dependency edge, collect actor names from the upstream spec's
	// execution path steps. Then check that the downstream spec has at least
	// one execution path with a step referencing one of those upstream actors
	// (case-insensitive). This matches the Python cross-spec-5 semantics.
	if graph != nil {
		processedRule5 := map[specPair]bool{}
		for _, edge := range graph.Edges {
			pair := specPair{edge.FromSpec, edge.ToSpec}
			if processedRule5[pair] {
				continue
			}
			processedRule5[pair] = true

			upstream := specByID[edge.FromSpec]
			downstream := specByID[edge.ToSpec]
			if upstream == nil || downstream == nil {
				continue
			}
			if upstream.Requirements == nil || downstream.Requirements == nil {
				continue
			}

			// Collect all actors from upstream execution path steps (lowercased).
			upstreamActors := map[string]bool{}
			for _, ep := range upstream.Requirements.ExecutionPaths {
				for _, step := range ep.Steps {
					if step.Actor != "" {
						upstreamActors[strings.ToLower(step.Actor)] = true
					}
				}
			}
			if len(upstreamActors) == 0 {
				continue
			}

			// Check if downstream has at least one execution path step
			// referencing any upstream actor (case-insensitive).
			found := false
			for _, ep := range downstream.Requirements.ExecutionPaths {
				for _, step := range ep.Steps {
					if step.Actor != "" && upstreamActors[strings.ToLower(step.Actor)] {
						found = true
						break
					}
				}
				if found {
					break
				}
			}

			if !found {
				errors = append(errors, ValidationEntry{
					Category: "integrity",
					Check:    "cross_spec_5",
					Message:  fmt.Sprintf("spec %s depends on spec %s but has no execution path with a step referencing an actor from spec %s", edge.ToSpec, edge.FromSpec, edge.FromSpec),
				})
			}
		}
	}

	return ValidationResult{
		Valid:  len(errors) == 0,
		Errors: errors,
	}
}

// ---------------------------------------------------------------------------
// Spec.ValidateStructured
// ---------------------------------------------------------------------------

// ValidateStructured runs full validation and returns a structured
// map[string]any for CLI consumption, with 'valid', 'errors', and
// optionally 'warnings' keys matching the Python validate_structured shape.
//
// Error maps use two distinct shapes based on category:
//   - Schema errors: {"category": "schema", "artifact": ..., "message": ...}
//     with optional "path" and "value" fields.
//   - Integrity errors: {"category": "integrity", "check": ..., "message": ...}
//
// Warning maps include: 'category' ("warning"), 'message', 'entity_id'.
// The 'warnings' key is omitted when no warnings exist.
// 'valid' is true when errors is empty.
func (s *Spec) ValidateStructured() map[string]any {
	result := s.Validate()

	errorMaps := make([]map[string]any, 0, len(result.Errors))
	for _, e := range result.Errors {
		var entry map[string]any
		// Use two distinct shapes matching Python's validate_structured:
		// cross-file/cross-spec rules and integrity-category entries → integrity shape
		// everything else → schema shape
		if e.Category == "integrity" ||
			strings.HasPrefix(e.Check, "cross_file") ||
			strings.HasPrefix(e.Check, "cross_spec") {
			entry = map[string]any{
				"category": "integrity",
				"check":    e.Check,
				"message":  e.Message,
			}
		} else {
			entry = map[string]any{
				"category": "schema",
				"artifact": e.Artifact,
				"message":  e.Message,
			}
			if e.Path != "" {
				entry["path"] = e.Path
			}
			if e.Value != "" {
				entry["value"] = e.Value
			}
		}
		errorMaps = append(errorMaps, entry)
	}

	output := map[string]any{
		"valid":  len(result.Errors) == 0,
		"errors": errorMaps,
	}

	// Only include warnings key when warnings exist (matching Python behavior).
	if len(result.Warnings) > 0 {
		warningMaps := make([]map[string]any, 0, len(result.Warnings))
		for _, w := range result.Warnings {
			entry := map[string]any{
				"category":  "warning",
				"message":   w.Message,
				"entity_id": w.EntityID,
			}
			warningMaps = append(warningMaps, entry)
		}
		output["warnings"] = warningMaps
	}

	return output
}

// DependencyGraph represents the inter-spec dependency graph built from
// tasks.json declarations. It is used by ValidateCrossSpec to determine
// which specs interact.
type DependencyGraph struct {
	Edges []DependencyEdge
}

// DependencyEdge represents a directed dependency edge between two specs.
type DependencyEdge struct {
	FromSpec     string
	ToSpec       string
	FromGroup    int
	ToGroup      int
	Relationship string
}
