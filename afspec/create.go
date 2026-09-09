package afspec

// Schema URIs for each artifact type (format version 2).
const (
	requirementsSchemaURI = "https://agent-fox.dev/schemas/requirements.v2.json"
	testSpecSchemaURI     = "https://agent-fox.dev/schemas/test_spec.v2.json"
	tasksSchemaURI        = "https://agent-fox.dev/schemas/tasks.v2.json"
)

// SchemaVersion is the format version this library reads and writes.
const SchemaVersion = 2

// CreateSpec creates a new draft Spec whose three artifacts carry a valid
// $schema, spec_id, spec_name and schema_version so that Save + LoadSpec
// round-trips. The artifact bodies are empty: a spec with no requirements, no
// tests and no tasks is *incomplete*, not schema-invalid, and Validate
// reports it as such (§10, bootstrap mode) until `spec generate` fills it in.
//
// Validation of specID and specName is deferred to Spec.Validate.
func CreateSpec(specID, specName string) *Spec {
	return &Spec{
		SpecID:        specID,
		SpecName:      specName,
		Status:        "draft",
		SchemaVersion: SchemaVersion,
		Requirements: &RequirementsV2Json{
			Schema:         requirementsSchemaURI,
			SpecId:         specID,
			SpecName:       specName,
			SchemaVersion:  SchemaVersion,
			Glossary:       RequirementsV2JsonGlossary{},
			Requirements:   []Requirement{},
			ExecutionPaths: []ExecutionPath{},
		},
		TestSpec: &TestSpecV2Json{
			Schema:        testSpecSchemaURI,
			SpecId:        specID,
			SpecName:      specName,
			SchemaVersion: SchemaVersion,
			Tests:         []Test{},
		},
		Tasks: &TasksV2Json{
			Schema:        tasksSchemaURI,
			SpecId:        specID,
			SpecName:      specName,
			SchemaVersion: SchemaVersion,
			TestCommands:  TestCommands{},
			Dependencies:  []Dependency{},
			Tasks:         []Task{},
		},
	}
}

// IsScaffold reports whether the spec is still an empty scaffold: no
// requirements, no tests and no tasks. A scaffold is incomplete rather than
// invalid, which is what lets `spec new` write a spec before `spec generate`
// has produced its content.
func (s *Spec) IsScaffold() bool {
	return s.Requirements != nil && len(s.Requirements.Requirements) == 0 &&
		s.TestSpec != nil && len(s.TestSpec.Tests) == 0 &&
		s.Tasks != nil && len(s.Tasks.Tasks) == 0
}
