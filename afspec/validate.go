package afspec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ValidationResult holds the results of spec validation. Valid is true if and
// only if Errors is empty; warnings never block.
type ValidationResult struct {
	Valid    bool
	Errors   []ValidationEntry
	Warnings []ValidationEntry
}

// ValidationEntry is a single validation error or warning.
type ValidationEntry struct {
	Category string // "schema", "integrity" or "warning"
	Message  string
	Artifact string // the artifact file the issue was found in
	Path     string // JSON path (InstanceLocation) for schema errors
	Keyword  string // KeywordLocation for schema errors
	Check    string // rule name: "json_schema", "completeness", "C1".."C11", …
	EntityID string // the entity the entry is about
	Value    string // optional value context
}

// ---------------------------------------------------------------------------
// JSON Schema validation infrastructure
// ---------------------------------------------------------------------------

var (
	compiledSchemas     map[string]*jsonschema.Schema
	compiledSchemasErr  error
	compiledSchemasOnce sync.Once
)

// embeddedURLLoader implements jsonschema.URLLoader, serving schema bytes from
// the embedded schemaFS under the "https://agent-fox.dev/schemas/" namespace.
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

// getCompiledSchemas compiles the bundled schemas once and caches the result.
func getCompiledSchemas() (map[string]*jsonschema.Schema, error) {
	compiledSchemasOnce.Do(func() {
		schemas := Schemas()
		loader := &embeddedURLLoader{schemas: schemas}

		c := jsonschema.NewCompiler()
		c.UseLoader(jsonschema.SchemeURLLoader{"https": loader})

		compiled := make(map[string]*jsonschema.Schema, len(schemas))
		for name, data := range schemas {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compiledSchemasErr = &LoadError{
					Msg:  fmt.Sprintf("cannot parse schema %s: %s", name, err),
					File: name,
					Err:  &SpecError{Msg: fmt.Sprintf("cannot parse schema %s: %s", name, err)},
				}
				return
			}
			if err := c.AddResource("https://agent-fox.dev/schemas/"+name, doc); err != nil {
				compiledSchemasErr = &LoadError{
					Msg:  fmt.Sprintf("cannot add schema resource %s: %s", name, err),
					File: name,
					Err:  &SpecError{Msg: fmt.Sprintf("cannot add schema resource %s: %s", name, err)},
				}
				return
			}
		}

		for name := range schemas {
			sch, err := c.Compile("https://agent-fox.dev/schemas/" + name)
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

// flattenValidationError flattens a jsonschema.ValidationError tree into its
// leaf errors.
func flattenValidationError(ve *jsonschema.ValidationError, artifact string) []ValidationEntry {
	if len(ve.Causes) == 0 {
		instanceLoc := ""
		if len(ve.InstanceLocation) > 0 {
			instanceLoc = "/" + strings.Join(ve.InstanceLocation, "/")
		}
		keywordLoc, msg := "", ""
		if ve.ErrorKind != nil {
			keywordLoc = "/" + strings.Join(ve.ErrorKind.KeywordPath(), "/")
			msg = ve.Error()
		}
		return []ValidationEntry{{
			Category: "schema",
			Check:    "json_schema",
			Message:  msg,
			Artifact: artifact,
			Path:     instanceLoc,
			Keyword:  keywordLoc,
		}}
	}

	var entries []ValidationEntry
	for _, cause := range ve.Causes {
		entries = append(entries, flattenValidationError(cause, artifact)...)
	}
	return entries
}

// ValidateArtifactSchema validates one already-decoded artifact against the
// named bundled schema. It is exported so the generation pipeline can check a
// model's tool input before it is turned into a Spec.
func ValidateArtifactSchema(artifact any, schemaName, artifactName string) []ValidationEntry {
	schemas, err := getCompiledSchemas()
	if err != nil {
		return []ValidationEntry{{
			Category: "schema", Check: "json_schema", Artifact: artifactName,
			Message: fmt.Sprintf("schema compilation error: %s", err),
		}}
	}

	sch, ok := schemas[schemaName]
	if !ok {
		return []ValidationEntry{{
			Category: "schema", Check: "json_schema", Artifact: artifactName,
			Message: fmt.Sprintf("schema not found: %s", schemaName),
		}}
	}

	// The validator works on decoded JSON values, not on Go structs, so a
	// struct is round-tripped through encoding/json first. A value that is
	// already decoded JSON is passed straight through.
	instance := artifact
	switch artifact.(type) {
	case map[string]any, []any:
	default:
		data, marshalErr := json.Marshal(artifact)
		if marshalErr != nil {
			return []ValidationEntry{{
				Category: "schema", Check: "json_schema", Artifact: artifactName,
				Message: fmt.Sprintf("cannot marshal %s: %s", artifactName, marshalErr),
			}}
		}
		var decoded any
		if parseErr := json.Unmarshal(data, &decoded); parseErr != nil {
			return []ValidationEntry{{
				Category: "schema", Check: "json_schema", Artifact: artifactName,
				Message: fmt.Sprintf("cannot parse %s JSON: %s", artifactName, parseErr),
			}}
		}
		instance = decoded
	}

	validationErr := sch.Validate(instance)
	if validationErr == nil {
		return nil
	}

	ve, ok := validationErr.(*jsonschema.ValidationError)
	if !ok {
		return []ValidationEntry{{
			Category: "schema", Check: "json_schema", Artifact: artifactName,
			Message: fmt.Sprintf("validation error: %s", validationErr),
		}}
	}
	return flattenValidationError(ve, artifactName)
}

// Schema file names for the four v2 artifacts.
const (
	PRDFrontmatterSchemaName = "prd-frontmatter.v2.json"
	RequirementsSchemaName   = "requirements.v2.json"
	TestSpecSchemaName       = "test_spec.v2.json"
	TasksSchemaName          = "tasks.v2.json"
)

// prdFrontmatterForSchema carries exactly the fields of
// prd-frontmatter.v2.json, so that non-schema fields of Spec cannot produce
// spurious additionalProperties errors. The optional four are omitted when
// empty, matching what renderPRD writes.
type prdFrontmatterForSchema struct {
	SpecID        string   `json:"spec_id"`
	SpecName      string   `json:"spec_name"`
	Title         string   `json:"title"`
	Status        string   `json:"status"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	IntentHash    *string  `json:"intent_hash"`
	SchemaVersion int      `json:"schema_version"`
	Owner         string   `json:"owner,omitempty"`
	Source        string   `json:"source,omitempty"`
	Supersedes    []string `json:"supersedes,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// legacyVersionEntry describes a spec still written in an older format
// version. Validating it against the v2 schemas produces a schema error per
// artifact plus every shape difference underneath; one sentence naming the
// version and the command that converts it is more use than that list.
func legacyVersionEntry(version int) ValidationEntry {
	return ValidationEntry{
		Category: "integrity",
		Check:    "schema_version",
		Artifact: "prd.md",
		Message: fmt.Sprintf("spec declares schema_version %d; this library reads version %d — run `spec migrate` to convert it",
			version, SchemaVersion),
	}
}

// scaffoldEntry describes a spec that has been created but not yet generated.
// An empty artifact list violates the schemas' minItems, which would bury the
// operator in schema noise; §10 treats this as incompleteness instead.
func scaffoldEntry() ValidationEntry {
	return ValidationEntry{
		Category: "integrity",
		Check:    "completeness",
		Message:  "spec is an empty scaffold: run `spec generate` to produce requirements, tests and tasks",
	}
}

// ValidateSchema validates the PRD frontmatter and the three JSON artifacts
// against the bundled v2 schemas. The EARS field constraints of §6.2.1 are
// expressed as conditionals inside requirements.v2.json, so there is no
// separate EARS check.
func (s *Spec) ValidateSchema() ValidationResult {
	if s.SchemaVersion != 0 && s.SchemaVersion != SchemaVersion {
		return ValidationResult{Valid: false, Errors: []ValidationEntry{legacyVersionEntry(s.SchemaVersion)}}
	}
	if s.IsScaffold() {
		return ValidationResult{Valid: false, Errors: []ValidationEntry{scaffoldEntry()}}
	}

	var errors []ValidationEntry

	fm := prdFrontmatterForSchema{
		SpecID:        s.SpecID,
		SpecName:      s.SpecName,
		Title:         s.Title,
		Status:        s.Status,
		CreatedAt:     s.CreatedAt,
		UpdatedAt:     s.UpdatedAt,
		IntentHash:    s.IntentHash,
		SchemaVersion: s.SchemaVersion,
		Owner:         s.Owner,
		Source:        s.Source,
		Supersedes:    s.Supersedes,
		Tags:          s.Tags,
	}
	errors = append(errors, ValidateArtifactSchema(fm, PRDFrontmatterSchemaName, "prd.md")...)

	if s.Requirements != nil {
		errors = append(errors, ValidateArtifactSchema(s.Requirements, RequirementsSchemaName, "requirements.json")...)
	}
	if s.TestSpec != nil {
		errors = append(errors, ValidateArtifactSchema(s.TestSpec, TestSpecSchemaName, "test_spec.json")...)
	}
	if s.Tasks != nil {
		errors = append(errors, ValidateArtifactSchema(s.Tasks, TasksSchemaName, "tasks.json")...)
	}

	return ValidationResult{Valid: len(errors) == 0, Errors: errors}
}

// ---------------------------------------------------------------------------
// ID formats (Appendix A) and language heuristics
// ---------------------------------------------------------------------------

var (
	requirementIDPattern = regexp.MustCompile(`^[A-Za-z0-9_]+-REQ-\d+$`)
	criterionIDPattern   = regexp.MustCompile(`^[A-Za-z0-9_]+-REQ-\d+\.\d+$`)
	pathIDPattern        = regexp.MustCompile(`^[A-Za-z0-9_]+-PATH-\d+$`)
	testIDPattern        = regexp.MustCompile(`^TS-[A-Za-z0-9_]+-\d+$`)

	// entityPrefixRe captures the spec_id prefix of a REQ or PATH id.
	entityPrefixRe = regexp.MustCompile(`^([A-Za-z0-9_]+)-(?:REQ|PATH)-`)
	// testPrefixRe captures the spec_id prefix of a test id.
	testPrefixRe = regexp.MustCompile(`^TS-([A-Za-z0-9_]+)-\d+$`)
	// criterionParentRe captures the requirement id a criterion belongs to.
	criterionParentRe = regexp.MustCompile(`^(.+)\.\d+$`)

	// vagueLanguageRe matches the words §6.5.4 asks criteria to avoid.
	vagueLanguageRe = regexp.MustCompile(`(?i)\b(appropriate|properly|correctly|reasonable|relevant|adequate|suitable|as needed|if necessary|etc)\b`)

	// errorKeywordRe spots an action that describes an error outcome; §10.2
	// warns when such a criterion carries no contract.
	errorKeywordRe = regexp.MustCompile(`(?i)\b(error|fail|reject|denied|deny|invalid|unauthorized|unauthorised|forbidden|timeout|not found)\b`)

	// backtickTermRe extracts `term` spans for the glossary hint of §10.3.
	backtickTermRe = regexp.MustCompile("`([^`]+)`")
	// backtickNumericRe and backtickQuotedRe exclude literals from that hint.
	backtickNumericRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)
	backtickQuotedRe  = regexp.MustCompile(`^["'].*["']$`)
)

// Scope limits of §6.5.3, §7.3.4 and §8.6.4-5. Exceeding one is a warning.
const (
	maxRequirementsPerSpec = 10
	maxCriteriaPerReq      = 8
	maxCriteriaPerTest     = 4
	maxTestsPerTask        = 10
	maxStepsPerTask        = 12
	maxTasksPerSpec        = 12
	glossaryHintThreshold  = 3
)

func entityPrefix(id string) string {
	if m := entityPrefixRe.FindStringSubmatch(id); m != nil {
		return m[1]
	}
	return ""
}

func testPrefix(id string) string {
	if m := testPrefixRe.FindStringSubmatch(id); m != nil {
		return m[1]
	}
	return ""
}

// ---------------------------------------------------------------------------
// Spec.ValidateCrossFile — rules C1 to C11 of §10.2
// ---------------------------------------------------------------------------

// The rule functions below run over whatever artifacts the Spec carries, so
// that the generation pipeline can apply the decidable subset after each step
// (§12.2). These accessors return an empty slice for an absent artifact.

func requirementsOf(s *Spec) []Requirement {
	if s.Requirements == nil {
		return nil
	}
	return s.Requirements.Requirements
}

func executionPathsOf(s *Spec) []ExecutionPath {
	if s.Requirements == nil {
		return nil
	}
	return s.Requirements.ExecutionPaths
}

func glossaryOf(s *Spec) RequirementsV2JsonGlossary {
	if s.Requirements == nil {
		return nil
	}
	return s.Requirements.Glossary
}

func testsOf(s *Spec) []Test {
	if s.TestSpec == nil {
		return nil
	}
	return s.TestSpec.Tests
}

func tasksOf(s *Spec) []Task {
	if s.Tasks == nil {
		return nil
	}
	return s.Tasks.Tasks
}

// specIndex holds the entity sets the cross-file rules resolve references
// against, built once per validation run.
type specIndex struct {
	requirementIDs map[string]bool
	criterionIDs   map[string]bool
	criteriaOfReq  map[string][]string
	criterionOrder []string
	pathIDs        map[string]bool
	pathOrder      []string
	testByID       map[string]Test
	testOrder      []string
}

func (s *Spec) buildIndex() specIndex {
	idx := specIndex{
		requirementIDs: map[string]bool{},
		criterionIDs:   map[string]bool{},
		criteriaOfReq:  map[string][]string{},
		pathIDs:        map[string]bool{},
		testByID:       map[string]Test{},
	}
	if s.Requirements != nil {
		for _, r := range s.Requirements.Requirements {
			idx.requirementIDs[r.Id] = true
			for _, c := range r.Criteria {
				idx.criterionIDs[c.Id] = true
				idx.criterionOrder = append(idx.criterionOrder, c.Id)
				idx.criteriaOfReq[r.Id] = append(idx.criteriaOfReq[r.Id], c.Id)
			}
		}
		for _, p := range s.Requirements.ExecutionPaths {
			idx.pathIDs[p.Id] = true
			idx.pathOrder = append(idx.pathOrder, p.Id)
		}
	}
	if s.TestSpec != nil {
		for _, t := range s.TestSpec.Tests {
			idx.testByID[t.Id] = t
			idx.testOrder = append(idx.testOrder, t.Id)
		}
	}
	return idx
}

// ValidateCrossFile runs the cross-file integrity rules C1 to C11 of §10.2
// plus the warnings listed there. All rules are errors unless stated
// otherwise.
func (s *Spec) ValidateCrossFile() ValidationResult {
	if s.SchemaVersion != 0 && s.SchemaVersion != SchemaVersion {
		return ValidationResult{Valid: false, Errors: []ValidationEntry{legacyVersionEntry(s.SchemaVersion)}}
	}
	if s.IsScaffold() {
		return ValidationResult{Valid: false, Errors: []ValidationEntry{scaffoldEntry()}}
	}

	var incomplete []string
	if s.Requirements == nil {
		incomplete = append(incomplete, "requirements.json")
	}
	if s.TestSpec == nil {
		incomplete = append(incomplete, "test_spec.json")
	}
	if s.Tasks == nil {
		incomplete = append(incomplete, "tasks.json")
	}
	if len(incomplete) > 0 {
		return ValidationResult{
			Valid: false,
			Errors: []ValidationEntry{{
				Category: "integrity",
				Check:    "completeness",
				Message:  fmt.Sprintf("spec is incomplete: %s", strings.Join(incomplete, ", ")),
			}},
		}
	}

	idx := s.buildIndex()

	var errors []ValidationEntry
	errors = append(errors, s.checkC1()...)
	errors = append(errors, s.checkC2(idx)...)
	errors = append(errors, s.checkC3C5(idx)...)
	errors = append(errors, s.checkC6C9(idx)...)
	errors = append(errors, s.checkC10()...)
	errors = append(errors, s.checkC11()...)

	return ValidationResult{
		Valid:    len(errors) == 0,
		Errors:   errors,
		Warnings: s.crossFileWarnings(idx),
	}
}

// checkC1: spec_id and spec_name agree across prd.md, the three JSON files
// and the folder name.
func (s *Spec) checkC1() []ValidationEntry {
	var errors []ValidationEntry

	type artifactID struct {
		name     string
		specID   string
		specName string
	}
	var artifacts []artifactID
	if s.Requirements != nil {
		artifacts = append(artifacts, artifactID{"requirements.json", s.Requirements.SpecId, s.Requirements.SpecName})
	}
	if s.TestSpec != nil {
		artifacts = append(artifacts, artifactID{"test_spec.json", s.TestSpec.SpecId, s.TestSpec.SpecName})
	}
	if s.Tasks != nil {
		artifacts = append(artifacts, artifactID{"tasks.json", s.Tasks.SpecId, s.Tasks.SpecName})
	}
	for _, a := range artifacts {
		if a.specID != s.SpecID {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C1", Artifact: a.name,
				Message: fmt.Sprintf("%s declares spec_id %q but prd.md declares %q", a.name, a.specID, s.SpecID),
			})
		}
		if a.specName != s.SpecName {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C1", Artifact: a.name,
				Message: fmt.Sprintf("%s declares spec_name %q but prd.md declares %q", a.name, a.specName, s.SpecName),
			})
		}
	}

	// A spec inside a spec root lives in a {NN}_{snake_case_name} directory and
	// that name must agree with the identity. A spec loaded from anywhere else
	// — a fixture directory, a scratch copy — has no folder identity to
	// disagree with, and DiscoverSpecs would not pick it up either, so the
	// check applies only to directories that are named like spec directories.
	if s.Dir != "" {
		base := filepath.Base(s.Dir)
		if prefix, name, err := ParseSpecDirName(base); err == nil {
			if prefix != s.SpecID {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C1", Artifact: "prd.md",
					Message: fmt.Sprintf("spec directory %q has prefix %q but spec_id is %q", base, prefix, s.SpecID),
				})
			}
			if name != s.SpecName {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C1", Artifact: "prd.md",
					Message: fmt.Sprintf("spec directory %q has name %q but spec_name is %q", base, name, s.SpecName),
				})
			}
		}
	}

	return errors
}

// checkC2: every ID matches its format, carries this spec's prefix, and is
// unique within its family.
func (s *Spec) checkC2(idx specIndex) []ValidationEntry {
	var errors []ValidationEntry
	specID := s.SpecID

	bad := func(artifact, id, msg string) ValidationEntry {
		return ValidationEntry{
			Category: "integrity", Check: "C2", Artifact: artifact,
			Message: msg, EntityID: id,
		}
	}

	seenReq := map[string]bool{}
	seenCriterion := map[string]bool{}
	for _, r := range requirementsOf(s) {
		if !requirementIDPattern.MatchString(r.Id) {
			errors = append(errors, bad("requirements.json", r.Id,
				fmt.Sprintf("requirement ID %q does not match {spec_id}-REQ-{N}", r.Id)))
		} else if p := entityPrefix(r.Id); p != specID {
			errors = append(errors, bad("requirements.json", r.Id,
				fmt.Sprintf("requirement ID %q carries spec_id prefix %q but this spec is %q", r.Id, p, specID)))
		}
		if seenReq[r.Id] {
			errors = append(errors, bad("requirements.json", r.Id,
				fmt.Sprintf("duplicate requirement ID %q", r.Id)))
		}
		seenReq[r.Id] = true

		for _, c := range r.Criteria {
			if !criterionIDPattern.MatchString(c.Id) {
				errors = append(errors, bad("requirements.json", c.Id,
					fmt.Sprintf("criterion ID %q does not match {spec_id}-REQ-{N}.{C}", c.Id)))
				continue
			}
			if p := entityPrefix(c.Id); p != specID {
				errors = append(errors, bad("requirements.json", c.Id,
					fmt.Sprintf("criterion ID %q carries spec_id prefix %q but this spec is %q", c.Id, p, specID)))
			}
			if m := criterionParentRe.FindStringSubmatch(c.Id); m != nil && m[1] != r.Id {
				errors = append(errors, bad("requirements.json", c.Id,
					fmt.Sprintf("criterion ID %q is listed under requirement %q", c.Id, r.Id)))
			}
			if seenCriterion[c.Id] {
				errors = append(errors, bad("requirements.json", c.Id,
					fmt.Sprintf("duplicate criterion ID %q", c.Id)))
			}
			seenCriterion[c.Id] = true
		}
	}

	seenPath := map[string]bool{}
	for _, p := range executionPathsOf(s) {
		if !pathIDPattern.MatchString(p.Id) {
			errors = append(errors, bad("requirements.json", p.Id,
				fmt.Sprintf("execution path ID %q does not match {spec_id}-PATH-{N}", p.Id)))
		} else if pre := entityPrefix(p.Id); pre != specID {
			errors = append(errors, bad("requirements.json", p.Id,
				fmt.Sprintf("execution path ID %q carries spec_id prefix %q but this spec is %q", p.Id, pre, specID)))
		}
		if seenPath[p.Id] {
			errors = append(errors, bad("requirements.json", p.Id,
				fmt.Sprintf("duplicate execution path ID %q", p.Id)))
		}
		seenPath[p.Id] = true
	}

	seenTest := map[string]bool{}
	for _, t := range testsOf(s) {
		if !testIDPattern.MatchString(t.Id) {
			errors = append(errors, bad("test_spec.json", t.Id,
				fmt.Sprintf("test ID %q does not match TS-{spec_id}-{N}", t.Id)))
		} else if p := testPrefix(t.Id); p != specID {
			errors = append(errors, bad("test_spec.json", t.Id,
				fmt.Sprintf("test ID %q carries spec_id prefix %q but this spec is %q", t.Id, p, specID)))
		}
		if seenTest[t.Id] {
			errors = append(errors, bad("test_spec.json", t.Id,
				fmt.Sprintf("duplicate test ID %q", t.Id)))
		}
		seenTest[t.Id] = true
	}

	seenTask := map[int]bool{}
	for _, task := range tasksOf(s) {
		if task.Id < 1 {
			errors = append(errors, bad("tasks.json", fmt.Sprint(task.Id),
				fmt.Sprintf("task ID %d is not a positive integer", task.Id)))
		}
		if seenTask[task.Id] {
			errors = append(errors, bad("tasks.json", fmt.Sprint(task.Id),
				fmt.Sprintf("duplicate task ID %d", task.Id)))
		}
		seenTask[task.Id] = true
	}

	_ = idx
	return errors
}

// checkC3C5 covers the test-side coverage rules:
//
//	C3 every test.verifies entry resolves to a criterion or a path
//	C4 every criterion is verified by at least one test
//	C5 every path is verified by at least one smoke test, and every smoke
//	   test verifies at least one path
func (s *Spec) checkC3C5(idx specIndex) []ValidationEntry {
	var errors []ValidationEntry

	verifiedCriteria := map[string]bool{}
	verifiedPathsBySmoke := map[string]bool{}

	for _, t := range testsOf(s) {
		smokeVerifiesAPath := false
		for _, ref := range t.Verifies {
			switch {
			case idx.criterionIDs[ref]:
				verifiedCriteria[ref] = true
			case idx.pathIDs[ref]:
				smokeVerifiesAPath = true
				if t.Kind == TestKindSmoke {
					verifiedPathsBySmoke[ref] = true
				}
			default:
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C3", Artifact: "test_spec.json",
					EntityID: t.Id,
					Message: fmt.Sprintf("test %s verifies %q, which is neither a criterion nor an execution path of this spec",
						t.Id, ref),
				})
			}
		}
		if t.Kind == TestKindSmoke && !smokeVerifiesAPath {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C5", Artifact: "test_spec.json",
				EntityID: t.Id,
				Message:  fmt.Sprintf("smoke test %s verifies no execution path", t.Id),
			})
		}
	}

	for _, id := range idx.criterionOrder {
		if !verifiedCriteria[id] {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C4", Artifact: "requirements.json",
				EntityID: id,
				Message:  fmt.Sprintf("criterion %s is not verified by any test", id),
			})
		}
	}

	for _, id := range idx.pathOrder {
		if !verifiedPathsBySmoke[id] {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C5", Artifact: "requirements.json",
				EntityID: id,
				Message:  fmt.Sprintf("execution path %s is not verified by any smoke test", id),
			})
		}
	}

	return errors
}

// checkC6C9 covers the plan-side rules:
//
//	C6 every task.criteria, task.tests and depends_on entry resolves;
//	   depends_on forms a DAG over lower task IDs
//	C7 every test is owned by at least one task
//	C8 every criterion is owned by at least one implement task
//	C9 exactly one integration task, last, owning every smoke test
func (s *Spec) checkC6C9(idx specIndex) []ValidationEntry {
	var errors []ValidationEntry

	ownedTests := map[string]bool{}
	ownedCriteria := map[string]bool{}
	taskIDs := map[int]bool{}
	for _, task := range tasksOf(s) {
		taskIDs[task.Id] = true
	}

	for _, task := range tasksOf(s) {
		for _, ref := range task.Criteria {
			switch {
			case idx.criterionIDs[ref]:
				if task.Kind == TaskKindImplement {
					ownedCriteria[ref] = true
				}
			case idx.requirementIDs[ref]:
				// A requirement ID means all of its criteria (§8.3).
				if task.Kind == TaskKindImplement {
					for _, cid := range idx.criteriaOfReq[ref] {
						ownedCriteria[cid] = true
					}
				}
			default:
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C6", Artifact: "tasks.json",
					EntityID: fmt.Sprint(task.Id),
					Message: fmt.Sprintf("task %d claims criteria %q, which is neither a requirement nor a criterion of this spec",
						task.Id, ref),
				})
			}
		}

		for _, ref := range task.Tests {
			if _, ok := idx.testByID[ref]; !ok {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C6", Artifact: "tasks.json",
					EntityID: fmt.Sprint(task.Id),
					Message:  fmt.Sprintf("task %d owns test %q, which does not exist in test_spec.json", task.Id, ref),
				})
				continue
			}
			ownedTests[ref] = true
		}

		for _, dep := range task.DependsOn {
			if !taskIDs[dep] {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C6", Artifact: "tasks.json",
					EntityID: fmt.Sprint(task.Id),
					Message:  fmt.Sprintf("task %d depends on task %d, which does not exist", task.Id, dep),
				})
				continue
			}
			// Referencing only lower IDs makes the graph acyclic by
			// construction, so no separate cycle search is needed.
			if dep >= task.Id {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C6", Artifact: "tasks.json",
					EntityID: fmt.Sprint(task.Id),
					Message:  fmt.Sprintf("task %d depends on task %d; depends_on must reference lower task IDs", task.Id, dep),
				})
			}
		}
	}

	for _, id := range idx.testOrder {
		if !ownedTests[id] {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C7", Artifact: "test_spec.json",
				EntityID: id,
				Message:  fmt.Sprintf("test %s is not owned by any task", id),
			})
		}
	}

	for _, id := range idx.criterionOrder {
		if !ownedCriteria[id] {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C8", Artifact: "requirements.json",
				EntityID: id,
				Message:  fmt.Sprintf("criterion %s is not owned by any implement task", id),
			})
		}
	}

	errors = append(errors, s.checkC9(idx)...)
	return errors
}

// checkC9: exactly one task has kind integration, it is the last task, and its
// tests include every smoke test.
func (s *Spec) checkC9(idx specIndex) []ValidationEntry {
	var errors []ValidationEntry

	tasks := tasksOf(s)
	var integrationIdx []int
	for i, task := range tasks {
		if task.Kind == TaskKindIntegration {
			integrationIdx = append(integrationIdx, i)
		}
	}

	switch len(integrationIdx) {
	case 0:
		errors = append(errors, ValidationEntry{
			Category: "integrity", Check: "C9", Artifact: "tasks.json",
			Message: "no task has kind \"integration\"; the final task must be the integration task",
		})
		return errors
	case 1:
		// The expected shape.
	default:
		ids := make([]string, 0, len(integrationIdx))
		for _, i := range integrationIdx {
			ids = append(ids, fmt.Sprint(tasks[i].Id))
		}
		errors = append(errors, ValidationEntry{
			Category: "integrity", Check: "C9", Artifact: "tasks.json",
			Message: fmt.Sprintf("expected exactly one integration task, found %d (tasks %s)",
				len(integrationIdx), strings.Join(ids, ", ")),
		})
	}

	last := integrationIdx[len(integrationIdx)-1]
	integration := tasks[last]
	if last != len(tasks)-1 {
		errors = append(errors, ValidationEntry{
			Category: "integrity", Check: "C9", Artifact: "tasks.json",
			EntityID: fmt.Sprint(integration.Id),
			Message:  fmt.Sprintf("integration task %d is not the last task", integration.Id),
		})
	}

	owned := make(map[string]bool, len(integration.Tests))
	for _, id := range integration.Tests {
		owned[id] = true
	}
	for _, id := range idx.testOrder {
		if idx.testByID[id].Kind == TestKindSmoke && !owned[id] {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "C9", Artifact: "tasks.json",
				EntityID: id,
				Message:  fmt.Sprintf("smoke test %s is not owned by the integration task", id),
			})
		}
	}

	return errors
}

// checkC10: every unwanted criterion has a non-empty contract.
func (s *Spec) checkC10() []ValidationEntry {
	var errors []ValidationEntry
	for _, r := range requirementsOf(s) {
		for _, c := range r.Criteria {
			if c.Pattern == CriterionPatternUnwanted && c.ContractText() == "" {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C10", Artifact: "requirements.json",
					EntityID: c.Id,
					Message:  fmt.Sprintf("unwanted criterion %s has no contract", c.Id),
				})
			}
		}
	}
	return errors
}

// checkC11: real_components is present and non-empty exactly when kind is
// smoke.
func (s *Spec) checkC11() []ValidationEntry {
	var errors []ValidationEntry
	for _, t := range testsOf(s) {
		switch t.Kind {
		case TestKindSmoke:
			if len(t.RealComponents) == 0 {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C11", Artifact: "test_spec.json",
					EntityID: t.Id,
					Message:  fmt.Sprintf("smoke test %s lists no real_components", t.Id),
				})
			}
		default:
			if len(t.RealComponents) > 0 {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "C11", Artifact: "test_spec.json",
					EntityID: t.Id,
					Message:  fmt.Sprintf("test %s has kind %q but lists real_components, which only smoke tests may do", t.Id, t.Kind),
				})
			}
		}
	}
	return errors
}

// crossFileWarnings collects the never-blocking warnings of §10.2: scope
// limits, vague language, glossary hints, and criteria that describe an error
// outcome without a contract.
func (s *Spec) crossFileWarnings(idx specIndex) []ValidationEntry {
	var warnings []ValidationEntry
	warn := func(artifact, entity, msg string) {
		warnings = append(warnings, ValidationEntry{
			Category: "warning", Artifact: artifact, EntityID: entity, Message: msg,
		})
	}

	// §6.5.3 — spec and requirement scope.
	if n := len(requirementsOf(s)); n > maxRequirementsPerSpec {
		warn("requirements.json", s.SpecID,
			fmt.Sprintf("spec has %d requirements (limit %d); consider splitting it", n, maxRequirementsPerSpec))
	}
	for _, r := range requirementsOf(s) {
		if n := len(r.Criteria); n > maxCriteriaPerReq {
			warn("requirements.json", r.Id,
				fmt.Sprintf("requirement %s has %d criteria (limit %d); consider splitting it", r.Id, n, maxCriteriaPerReq))
		}
	}

	// §6.5.4 — vague language, and the contract hint of §10.2.
	for _, r := range requirementsOf(s) {
		for _, c := range r.Criteria {
			fields := map[string]string{"action": c.Action}
			if c.Condition != nil {
				fields["condition"] = *c.Condition
			}
			if c.Guard != nil {
				fields["guard"] = *c.Guard
			}
			names := make([]string, 0, len(fields))
			for name := range fields {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				for _, match := range vagueLanguageRe.FindAllString(fields[name], -1) {
					warn("requirements.json", c.Id,
						fmt.Sprintf("vague term %q in field %s of criterion %s", strings.ToLower(match), name, c.Id))
				}
			}

			if c.Pattern != CriterionPatternUnwanted && c.ContractText() == "" && errorKeywordRe.MatchString(c.Action) {
				warn("requirements.json", c.Id,
					fmt.Sprintf("criterion %s describes an error outcome but has no contract", c.Id))
			}
		}
	}

	// §7.3.4 — a test that verifies more than four criteria is usually two.
	for _, t := range testsOf(s) {
		criteriaCount := 0
		for _, ref := range t.Verifies {
			if idx.criterionIDs[ref] {
				criteriaCount++
			}
		}
		if criteriaCount > maxCriteriaPerTest {
			warn("test_spec.json", t.Id,
				fmt.Sprintf("test %s verifies %d criteria (limit %d); it is probably two tests", t.Id, criteriaCount, maxCriteriaPerTest))
		}
	}

	// §8.6.4-5 — task size and count.
	if n := len(tasksOf(s)); n > maxTasksPerSpec {
		warn("tasks.json", s.SpecID,
			fmt.Sprintf("spec has %d tasks (limit %d); consider splitting it", n, maxTasksPerSpec))
	}
	for _, task := range tasksOf(s) {
		if n := len(task.Tests); n > maxTestsPerTask {
			warn("tasks.json", fmt.Sprint(task.Id),
				fmt.Sprintf("task %d owns %d tests (limit %d); consider splitting it", task.Id, n, maxTestsPerTask))
		}
		if n := len(task.Steps); n > maxStepsPerTask {
			warn("tasks.json", fmt.Sprint(task.Id),
				fmt.Sprintf("task %d has %d steps (limit %d); consider splitting it", task.Id, n, maxStepsPerTask))
		}
	}

	warnings = append(warnings, s.glossaryHints()...)
	return warnings
}

// glossaryHints implements §10.3: at most a warning per backtick-wrapped term
// that appears in three or more criteria and has no glossary entry. The v1
// rule made this an error and produced most of the repair churn.
func (s *Spec) glossaryHints() []ValidationEntry {
	counts := map[string]int{}
	for _, r := range requirementsOf(s) {
		for _, c := range r.Criteria {
			seenInCriterion := map[string]bool{}
			for _, text := range []string{c.Action, c.ConditionText(), c.GuardText(), c.ContractText()} {
				for _, m := range backtickTermRe.FindAllStringSubmatch(text, -1) {
					term := strings.TrimSpace(m[1])
					if term == "" || backtickNumericRe.MatchString(term) || backtickQuotedRe.MatchString(term) {
						continue
					}
					seenInCriterion[term] = true
				}
			}
			for term := range seenInCriterion {
				counts[term]++
			}
		}
	}

	terms := make([]string, 0, len(counts))
	for term, n := range counts {
		if n < glossaryHintThreshold {
			continue
		}
		if _, defined := glossaryOf(s)[term]; defined {
			continue
		}
		terms = append(terms, term)
	}
	sort.Strings(terms)

	warnings := make([]ValidationEntry, 0, len(terms))
	for _, term := range terms {
		warnings = append(warnings, ValidationEntry{
			Category: "warning", Artifact: "requirements.json", EntityID: term,
			Message: fmt.Sprintf("term %q appears in %d criteria but has no glossary entry", term, counts[term]),
		})
	}
	return warnings
}

// Validate runs schema validation followed by the cross-file rules and
// returns the combined result.
func (s *Spec) Validate() ValidationResult {
	schemaResult := s.ValidateSchema()

	// Cross-file rules resolve IDs against artifacts the schema has already
	// rejected, so running them on a schema-invalid spec only adds noise.
	if !schemaResult.Valid {
		return ValidationResult{
			Valid:    false,
			Errors:   schemaResult.Errors,
			Warnings: schemaResult.Warnings,
		}
	}

	crossFile := s.ValidateCrossFile()
	return ValidationResult{
		Valid:    len(crossFile.Errors) == 0,
		Errors:   crossFile.Errors,
		Warnings: append(schemaResult.Warnings, crossFile.Warnings...),
	}
}

// ---------------------------------------------------------------------------
// ValidateCrossSpec — §10.4
// ---------------------------------------------------------------------------

// ValidateCrossSpec checks the rules that span specs: every declared
// dependency exists, the dependency graph is acyclic, glossary terms shared
// between specs agree (warning), and each dependency edge is reflected by a
// shared actor in the two specs' execution paths (error).
//
// The v1 external-API signature and return-contract comparisons are gone: they
// compared free text and never fired usefully.
func ValidateCrossSpec(specs []*Spec, graph *DependencyGraph) ValidationResult {
	var errors, warnings []ValidationEntry

	specByID := make(map[string]*Spec, len(specs))
	for _, s := range specs {
		specByID[s.SpecID] = s
	}

	// Every dependencies[].spec must exist in the spec root.
	for _, s := range specs {
		if s.Tasks == nil {
			continue
		}
		for _, dep := range s.Tasks.Dependencies {
			if _, ok := specByID[dep.Spec]; !ok {
				errors = append(errors, ValidationEntry{
					Category: "integrity", Check: "cross_spec_unknown_dependency",
					Artifact: "tasks.json", EntityID: s.SpecID,
					Message: fmt.Sprintf("spec %s declares a dependency on spec %q, which does not exist in the spec root", s.SpecID, dep.Spec),
				})
			}
		}
	}

	if len(specs) <= 1 {
		return ValidationResult{Valid: len(errors) == 0, Errors: errors, Warnings: warnings}
	}

	// The dependency graph must be acyclic.
	if graph != nil {
		if _, err := graph.TopologicalSort(); err != nil {
			errors = append(errors, ValidationEntry{
				Category: "integrity", Check: "cross_spec_cycle",
				Message: fmt.Sprintf("the spec dependency graph is not acyclic: %s", err),
			})
		}
	}

	// Glossary terms shared between specs should agree (warning only).
	for i := 0; i < len(specs); i++ {
		for j := i + 1; j < len(specs); j++ {
			a, b := specs[i], specs[j]
			if a.Requirements == nil || b.Requirements == nil {
				continue
			}
			terms := make([]string, 0, len(a.Requirements.Glossary))
			for term := range a.Requirements.Glossary {
				terms = append(terms, term)
			}
			sort.Strings(terms)
			for _, term := range terms {
				defB, ok := b.Requirements.Glossary[term]
				if ok && a.Requirements.Glossary[term] != defB {
					warnings = append(warnings, ValidationEntry{
						Category: "warning", EntityID: term,
						Message: fmt.Sprintf("glossary term %q is defined differently in spec %s and spec %s", term, a.SpecID, b.SpecID),
					})
				}
			}
		}
	}

	// Along each dependency edge, the downstream spec must have at least one
	// execution path step whose actor also appears in an upstream path.
	if graph != nil {
		seen := map[[2]string]bool{}
		for _, edge := range graph.Edges {
			key := [2]string{edge.FromSpec, edge.ToSpec}
			if seen[key] {
				continue
			}
			seen[key] = true

			// FromSpec depends on ToSpec, so ToSpec is upstream.
			downstream, upstream := specByID[edge.FromSpec], specByID[edge.ToSpec]
			if downstream == nil || upstream == nil ||
				downstream.Requirements == nil || upstream.Requirements == nil {
				continue
			}

			upstreamActors := map[string]bool{}
			for _, p := range upstream.Requirements.ExecutionPaths {
				for _, step := range p.Steps {
					upstreamActors[strings.ToLower(step.Actor)] = true
				}
			}
			if len(upstreamActors) == 0 {
				continue
			}

			found := false
			for _, p := range downstream.Requirements.ExecutionPaths {
				for _, step := range p.Steps {
					if upstreamActors[strings.ToLower(step.Actor)] {
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
					Category: "integrity", Check: "cross_spec_actor",
					EntityID: downstream.SpecID,
					Message: fmt.Sprintf("spec %s depends on spec %s but has no execution path step whose actor appears in spec %s",
						downstream.SpecID, upstream.SpecID, upstream.SpecID),
				})
			}
		}
	}

	return ValidationResult{Valid: len(errors) == 0, Errors: errors, Warnings: warnings}
}

// ---------------------------------------------------------------------------
// Spec.ValidateStructured
// ---------------------------------------------------------------------------

// ValidateStructured runs full validation and returns a structured map for
// CLI consumption with "valid", "errors" and, when non-empty, "warnings".
//
// Schema errors carry "artifact", "message" and optionally "path"; integrity
// errors carry "check" and "message".
func (s *Spec) ValidateStructured() map[string]any {
	result := s.Validate()

	errorMaps := make([]map[string]any, 0, len(result.Errors))
	for _, e := range result.Errors {
		var entry map[string]any
		if e.Category == "integrity" {
			entry = map[string]any{
				"category": "integrity",
				"check":    e.Check,
				"message":  e.Message,
			}
			if e.EntityID != "" {
				entry["entity_id"] = e.EntityID
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

	if len(result.Warnings) > 0 {
		warningMaps := make([]map[string]any, 0, len(result.Warnings))
		for _, w := range result.Warnings {
			warningMaps = append(warningMaps, map[string]any{
				"category":  "warning",
				"message":   w.Message,
				"entity_id": w.EntityID,
			})
		}
		output["warnings"] = warningMaps
	}

	return output
}

// DependencyGraph is the inter-spec dependency graph built from the
// dependencies list of each spec's tasks.json.
type DependencyGraph struct {
	Edges []DependencyEdge
}

// DependencyEdge is a directed edge: FromSpec depends on ToSpec.
type DependencyEdge struct {
	FromSpec string
	ToSpec   string
	Reason   string
}
