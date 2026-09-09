package afspec

// CoverageReport represents the result of computing test coverage against
// requirements. Covered contains IDs that are referenced by at least one
// test entry; Uncovered contains IDs with no test coverage.
type CoverageReport struct {
	Covered   []string
	Uncovered []string
}

// ComputeCoverageStruct computes test coverage and returns a Coverage struct
// suitable for storing in TestSpecV1Json.Coverage. Unlike ComputeCoverage,
// it preserves the type separation between requirements, correctness
// properties, and execution paths, and maps each to its own field:
//
//   - RequirementsCovered: criterion IDs (acceptance criteria and edge case
//     criteria) that are referenced by at least one test case or edge case test.
//   - PropertiesCovered: correctness property IDs referenced by at least one
//     property test.
//   - PathsCovered: execution path IDs referenced by at least one smoke test.
//   - Gaps: criterion and other entity IDs from all three categories that have
//     no test coverage.
func (ts *TestSpecV1Json) ComputeCoverageStruct(req *RequirementsV1Json) Coverage {
	// Build an ordered list of all criterion IDs (acceptance criteria + edge
	// cases) and a fast-lookup set of valid criterion IDs.
	var allCriteria []string
	criterionIDs := make(map[string]bool)
	for _, r := range req.Requirements {
		for _, ac := range r.AcceptanceCriteria {
			allCriteria = append(allCriteria, ac.Id)
			criterionIDs[ac.Id] = true
		}
		for _, ec := range r.EdgeCases {
			allCriteria = append(allCriteria, ec.Id)
			criterionIDs[ec.Id] = true
		}
	}

	// Track which criterion IDs are covered.
	coveredCriteria := make(map[string]bool)
	coveredProps := make(map[string]bool)
	coveredPaths := make(map[string]bool)

	// Test cases reference criterion IDs directly.
	for _, tc := range ts.TestCases {
		if tc.RequirementId != "" && criterionIDs[tc.RequirementId] {
			coveredCriteria[tc.RequirementId] = true
		}
	}

	// Edge case tests also reference criterion IDs directly.
	for _, ec := range ts.EdgeCaseTests {
		if ec.RequirementId != "" && criterionIDs[ec.RequirementId] {
			coveredCriteria[ec.RequirementId] = true
		}
	}

	// Property tests cover correctness properties.
	for _, pt := range ts.PropertyTests {
		if pt.PropertyId != "" {
			coveredProps[pt.PropertyId] = true
		}
	}

	// Smoke tests cover execution paths.
	for _, sm := range ts.SmokeTests {
		if sm.ExecutionPathId != "" {
			coveredPaths[sm.ExecutionPathId] = true
		}
	}

	// Build the Coverage struct preserving the order from Requirements.
	var reqCovered, propCovered, pathCovered, gaps []string

	for _, id := range allCriteria {
		if coveredCriteria[id] {
			reqCovered = append(reqCovered, id)
		} else {
			gaps = append(gaps, id)
		}
	}

	for _, p := range req.CorrectnessProperties {
		if coveredProps[p.Id] {
			propCovered = append(propCovered, p.Id)
		} else {
			gaps = append(gaps, p.Id)
		}
	}

	for _, p := range req.ExecutionPaths {
		if coveredPaths[p.Id] {
			pathCovered = append(pathCovered, p.Id)
		} else {
			gaps = append(gaps, p.Id)
		}
	}

	// Ensure no nil slices so marshaling produces [] instead of null.
	if reqCovered == nil {
		reqCovered = []string{}
	}
	if propCovered == nil {
		propCovered = []string{}
	}
	if pathCovered == nil {
		pathCovered = []string{}
	}
	if gaps == nil {
		gaps = []string{}
	}

	return Coverage{
		RequirementsCovered: reqCovered,
		PropertiesCovered:   propCovered,
		PathsCovered:        pathCovered,
		Gaps:                gaps,
	}
}

// ComputeCoverage scans all test entries in the TestSpec against criterion IDs,
// correctness property IDs, and execution path IDs in the Requirements struct.
// Returns a CoverageReport indicating which entities are covered and which
// are not.
//
// Coverage is computed at three levels:
//   - Criteria: each acceptance criterion and edge case criterion ID is
//     individually covered if referenced by a test case or edge case test.
//   - Properties: a property is covered if it's referenced by a property test.
//   - Paths: an execution path is covered if it's referenced by a smoke test.
//
// If the Requirements struct has no entities to cover, the report indicates
// 100% coverage (empty Uncovered list). If the TestSpec has no test entries,
// all criterion/property/path entities appear in the Uncovered list.
func (ts *TestSpecV1Json) ComputeCoverage(req *RequirementsV1Json) CoverageReport {
	// Build an ordered list of all criterion IDs and a fast-lookup set.
	var allCriteria []string
	criterionIDs := make(map[string]bool)
	for _, r := range req.Requirements {
		for _, ac := range r.AcceptanceCriteria {
			allCriteria = append(allCriteria, ac.Id)
			criterionIDs[ac.Id] = true
		}
		for _, ec := range r.EdgeCases {
			allCriteria = append(allCriteria, ec.Id)
			criterionIDs[ec.Id] = true
		}
	}

	propIDs := make(map[string]bool)
	for _, p := range req.CorrectnessProperties {
		propIDs[p.Id] = true
	}

	pathIDs := make(map[string]bool)
	for _, p := range req.ExecutionPaths {
		pathIDs[p.Id] = true
	}

	// Track which criterion IDs are covered.
	coveredCriteria := make(map[string]bool)
	coveredProps := make(map[string]bool)
	coveredPaths := make(map[string]bool)

	// Test cases reference criterion IDs directly.
	for _, tc := range ts.TestCases {
		if tc.RequirementId != "" && criterionIDs[tc.RequirementId] {
			coveredCriteria[tc.RequirementId] = true
		}
	}

	// Edge case tests also reference criterion IDs directly.
	for _, ec := range ts.EdgeCaseTests {
		if ec.RequirementId != "" && criterionIDs[ec.RequirementId] {
			coveredCriteria[ec.RequirementId] = true
		}
	}

	// Property tests cover correctness properties.
	for _, pt := range ts.PropertyTests {
		if pt.PropertyId != "" {
			coveredProps[pt.PropertyId] = true
		}
	}

	// Smoke tests cover execution paths.
	for _, sm := range ts.SmokeTests {
		if sm.ExecutionPathId != "" {
			coveredPaths[sm.ExecutionPathId] = true
		}
	}

	// Build the coverage report preserving the order from Requirements.
	var covered, uncovered []string

	for _, id := range allCriteria {
		if coveredCriteria[id] {
			covered = append(covered, id)
		} else {
			uncovered = append(uncovered, id)
		}
	}

	for _, p := range req.CorrectnessProperties {
		if coveredProps[p.Id] {
			covered = append(covered, p.Id)
		} else {
			uncovered = append(uncovered, p.Id)
		}
	}

	for _, p := range req.ExecutionPaths {
		if coveredPaths[p.Id] {
			covered = append(covered, p.Id)
		} else {
			uncovered = append(uncovered, p.Id)
		}
	}

	if covered == nil {
		covered = []string{}
	}
	if uncovered == nil {
		uncovered = []string{}
	}

	return CoverageReport{
		Covered:   covered,
		Uncovered: uncovered,
	}
}
