package afspec

// Schema URIs for each artifact type.
const (
	requirementsSchemaURI = "https://agent-fox.dev/schemas/requirements.v1.json"
	testSpecSchemaURI     = "https://agent-fox.dev/schemas/test_spec.v1.json"
	tasksSchemaURI        = "https://agent-fox.dev/schemas/tasks.v1.json"
)

// CreateSpec creates a new Spec with status set to "draft", all sub-artifacts
// initialized with valid $schema, spec_id, spec_name, and schema_version
// fields so that Save + LoadSpec round-trips succeed without error.
// Validation of specID and specName is deferred to spec.Validate.
func CreateSpec(specID, specName string) *Spec {
	return &Spec{
		SpecID:        specID,
		SpecName:      specName,
		Status:        "draft",
		SchemaVersion: 1,
		Requirements: &RequirementsV1Json{
			Schema:                requirementsSchemaURI,
			SpecId:                specID,
			SpecName:              specName,
			SchemaVersion:         1,
			Glossary:              RequirementsV1JsonGlossary{},
			Requirements:          []Requirement{},
			CorrectnessProperties: []CorrectnessProperty{},
			ExecutionPaths:        []ExecutionPath{},
			ErrorHandling:         []ErrorHandlingEntry{},
		},
		TestSpec: &TestSpecV1Json{
			Schema:        testSpecSchemaURI,
			SpecId:        specID,
			SpecName:      specName,
			SchemaVersion: 1,
			TestCases:     []TestCase{},
			PropertyTests: []PropertyTest{},
			EdgeCaseTests: []EdgeCaseTest{},
			SmokeTests:    []SmokeTest{},
		},
		Tasks: &TasksV1Json{
			Schema:        tasksSchemaURI,
			SpecId:        specID,
			SpecName:      specName,
			SchemaVersion: 1,
			TestCommands:  TestCommands{},
			Dependencies:  []TaskDependency{},
			// task_groups requires minItems:1 per Section 8.3; scaffold with
			// the mandatory first ("tests") and last ("wiring_verification") groups.
			TaskGroups: []TaskGroup{
				{
					Id:       1,
					Kind:     TaskGroupKindTests,
					Title:    "Write Tests",
					Subtasks: []Subtask{},
					Verification: VerificationSubtask{
						Id:     "1.V",
						Checks: []string{"all tests pass"},
					},
				},
				{
					Id:       2,
					Kind:     TaskGroupKindWiringVerification,
					Title:    "Wiring Verification",
					Subtasks: []Subtask{},
					Verification: VerificationSubtask{
						Id:     "2.V",
						Checks: []string{"no stubs remain"},
					},
				},
			},
			Traceability: []TraceabilityEntry{},
		},
	}
}
