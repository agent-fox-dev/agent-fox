package afspec

import "fmt"

// ValidationError represents a single validation error from BootstrapSpec.Finalize.
// Rule identifies the type of check that failed (e.g. "bootstrap" for missing
// artifacts, or a schema/cross-file rule name). Message describes the failure.
type ValidationError struct {
	Rule    string
	Message string
}

// BootstrapSpec supports incremental population of spec artifacts with
// deferred cross-file validation. Use NewBootstrapSpec to create one, set
// artifacts on it, then call Finalize to assemble and validate the Spec.
type BootstrapSpec struct {
	SpecID       string
	SpecName     string
	Requirements *RequirementsV1Json
	TestSpec     *TestSpecV1Json
	Tasks        *TasksV1Json
	PRDBody      string
	Architecture string
}

// NewBootstrapSpec creates a BootstrapSpec with the given specID and specName.
// All artifact fields start as nil/zero values.
func NewBootstrapSpec(specID, specName string) *BootstrapSpec {
	return &BootstrapSpec{
		SpecID:   specID,
		SpecName: specName,
	}
}

// Finalize checks that all required artifacts (Requirements, TestSpec, Tasks,
// PRDBody) are set, assembles a Spec from them, and runs full schema and
// cross-file validation. Returns (*Spec, nil) on success.
//
// If any required artifacts are missing, returns (nil, []ValidationError)
// where each missing artifact produces a ValidationError with Rule="bootstrap"
// and Message="Missing artifact: {name}".
//
// If all artifacts are present but validation fails, returns
// (nil, []ValidationError) with validation error details.
//
// The BootstrapSpec is not mutated by Finalize; calling it multiple times
// produces independent results.
func (b *BootstrapSpec) Finalize() (*Spec, []ValidationError) {
	var errs []ValidationError

	// Check for missing required artifacts.
	if b.Requirements == nil {
		errs = append(errs, ValidationError{Rule: "bootstrap", Message: "Missing artifact: requirements"})
	}
	if b.TestSpec == nil {
		errs = append(errs, ValidationError{Rule: "bootstrap", Message: "Missing artifact: test_spec"})
	}
	if b.Tasks == nil {
		errs = append(errs, ValidationError{Rule: "bootstrap", Message: "Missing artifact: tasks"})
	}
	if b.PRDBody == "" {
		errs = append(errs, ValidationError{Rule: "bootstrap", Message: "Missing artifact: prd"})
	}

	if len(errs) > 0 {
		return nil, errs
	}

	// Assemble a new Spec from all artifacts.
	spec := &Spec{
		SpecID:        b.SpecID,
		SpecName:      b.SpecName,
		Status:        "draft",
		SchemaVersion: 1,
		Supersedes:    []string{},
		PRDBody:       b.PRDBody,
		Requirements:  b.Requirements,
		TestSpec:      b.TestSpec,
		Tasks:         b.Tasks,
		Architecture:  b.Architecture,
	}

	// Cross-file validation: check spec_id consistency across artifacts.
	if b.Requirements.SpecId != b.SpecID {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("requirements.spec_id %q does not match spec ID %q", b.Requirements.SpecId, b.SpecID),
		})
	}
	if b.TestSpec.SpecId != b.SpecID {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("test_spec.spec_id %q does not match spec ID %q", b.TestSpec.SpecId, b.SpecID),
		})
	}
	if b.Tasks.SpecId != b.SpecID {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("tasks.spec_id %q does not match spec ID %q", b.Tasks.SpecId, b.SpecID),
		})
	}

	// Cross-file validation: check spec_name consistency across artifacts.
	if b.Requirements.SpecName != b.SpecName {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("requirements.spec_name %q does not match spec name %q", b.Requirements.SpecName, b.SpecName),
		})
	}
	if b.TestSpec.SpecName != b.SpecName {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("test_spec.spec_name %q does not match spec name %q", b.TestSpec.SpecName, b.SpecName),
		})
	}
	if b.Tasks.SpecName != b.SpecName {
		errs = append(errs, ValidationError{
			Rule:    "cross_file",
			Message: fmt.Sprintf("tasks.spec_name %q does not match spec name %q", b.Tasks.SpecName, b.SpecName),
		})
	}

	if len(errs) > 0 {
		return nil, errs
	}

	// Run full validation (schema + cross-file) on the assembled spec.
	result := spec.Validate()
	if !result.Valid {
		for _, e := range result.Errors {
			rule := e.Category
			if e.Check != "" {
				rule = e.Check
			}
			errs = append(errs, ValidationError{
				Rule:    rule,
				Message: e.Message,
			})
		}
		return nil, errs
	}

	return spec, nil
}
