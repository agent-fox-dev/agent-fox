package afspec

import "fmt"

// ---------------------------------------------------------------------------
// Requirements-level collection mutation functions (05-REQ-1)
// ---------------------------------------------------------------------------

// deepCopyRequirements returns a deep copy of a RequirementsV1Json, ensuring
// that all slice and map fields are independently allocated. Callers can
// safely mutate the returned struct without affecting the original.
func deepCopyRequirements(req RequirementsV1Json) RequirementsV1Json {
	out := req // shallow copy of scalar/struct fields

	// Deep copy Requirements slice (and nested slices within each Requirement).
	out.Requirements = make([]Requirement, len(req.Requirements))
	for i, r := range req.Requirements {
		out.Requirements[i] = r
		out.Requirements[i].AcceptanceCriteria = make([]Criterion, len(r.AcceptanceCriteria))
		copy(out.Requirements[i].AcceptanceCriteria, r.AcceptanceCriteria)
		out.Requirements[i].EdgeCases = make([]Criterion, len(r.EdgeCases))
		copy(out.Requirements[i].EdgeCases, r.EdgeCases)
	}

	// Deep copy Glossary map.
	out.Glossary = make(RequirementsV1JsonGlossary, len(req.Glossary))
	for k, v := range req.Glossary {
		out.Glossary[k] = v
	}

	// Deep copy CorrectnessProperties slice (and nested Validates slices).
	out.CorrectnessProperties = make([]CorrectnessProperty, len(req.CorrectnessProperties))
	for i, cp := range req.CorrectnessProperties {
		out.CorrectnessProperties[i] = cp
		out.CorrectnessProperties[i].Validates = make([]string, len(cp.Validates))
		copy(out.CorrectnessProperties[i].Validates, cp.Validates)
	}

	// Deep copy ExecutionPaths slice (and nested Steps slices).
	out.ExecutionPaths = make([]ExecutionPath, len(req.ExecutionPaths))
	for i, ep := range req.ExecutionPaths {
		out.ExecutionPaths[i] = ep
		out.ExecutionPaths[i].Steps = make([]PathStep, len(ep.Steps))
		copy(out.ExecutionPaths[i].Steps, ep.Steps)
	}

	// Deep copy ErrorHandling slice.
	out.ErrorHandling = make([]ErrorHandlingEntry, len(req.ErrorHandling))
	copy(out.ErrorHandling, req.ErrorHandling)

	// Deep copy ExternalApis slice (and nested Symbols slices).
	if req.ExternalApis != nil {
		out.ExternalApis = make([]ExternalApi, len(req.ExternalApis))
		for i, api := range req.ExternalApis {
			out.ExternalApis[i] = api
			out.ExternalApis[i].Symbols = make([]ExternalApiSymbol, len(api.Symbols))
			copy(out.ExternalApis[i].Symbols, api.Symbols)
		}
	}

	return out
}

// AddRequirement returns a new RequirementsV1Json with the given Requirement
// appended. Returns an error if a Requirement with the same ID already exists.
// The original is not modified.
func AddRequirement(req RequirementsV1Json, r Requirement) (RequirementsV1Json, error) {
	for _, existing := range req.Requirements {
		if existing.Id == r.Id {
			return req, fmt.Errorf("duplicate requirement ID %q", r.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.Requirements = append(out.Requirements, r)
	return out, nil
}

// GetRequirement looks up a Requirement by ID. Returns a pointer to the
// matching Requirement and true if found, or nil and false otherwise.
func GetRequirement(req RequirementsV1Json, id string) (*Requirement, bool) {
	for i := range req.Requirements {
		if req.Requirements[i].Id == id {
			return &req.Requirements[i], true
		}
	}
	return nil, false
}

// RemoveRequirement returns a new RequirementsV1Json with the Requirement
// having the given ID removed. Returns the new copy and true if found, or
// the original and false if the ID does not exist.
func RemoveRequirement(req RequirementsV1Json, id string) (RequirementsV1Json, bool) {
	idx := -1
	for i, r := range req.Requirements {
		if r.Id == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		return req, false
	}
	out := deepCopyRequirements(req)
	out.Requirements = append(out.Requirements[:idx], out.Requirements[idx+1:]...)
	return out, true
}

// SetGlossaryEntry returns a new RequirementsV1Json with the glossary term
// set to the given definition. Inserts a new entry or overwrites an existing
// one. The original is not modified.
func SetGlossaryEntry(req RequirementsV1Json, term, definition string) RequirementsV1Json {
	out := deepCopyRequirements(req)
	if out.Glossary == nil {
		out.Glossary = make(RequirementsV1JsonGlossary)
	}
	out.Glossary[term] = definition
	return out
}

// RemoveGlossaryEntry returns a new RequirementsV1Json with the given term
// removed from the glossary. Returns the new copy and true if the term
// existed, or the original and false otherwise.
func RemoveGlossaryEntry(req RequirementsV1Json, term string) (RequirementsV1Json, bool) {
	if _, exists := req.Glossary[term]; !exists {
		return req, false
	}
	out := deepCopyRequirements(req)
	delete(out.Glossary, term)
	return out, true
}

// AddCorrectnessProperty returns a new RequirementsV1Json with the given
// CorrectnessProperty appended. Returns an error if a property with the same
// ID already exists. The original is not modified.
func AddCorrectnessProperty(req RequirementsV1Json, p CorrectnessProperty) (RequirementsV1Json, error) {
	for _, existing := range req.CorrectnessProperties {
		if existing.Id == p.Id {
			return req, fmt.Errorf("duplicate correctness property ID %q", p.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.CorrectnessProperties = append(out.CorrectnessProperties, p)
	return out, nil
}

// AddExecutionPath returns a new RequirementsV1Json with the given
// ExecutionPath appended. Returns an error if a path with the same ID already
// exists. The original is not modified.
func AddExecutionPath(req RequirementsV1Json, p ExecutionPath) (RequirementsV1Json, error) {
	for _, existing := range req.ExecutionPaths {
		if existing.Id == p.Id {
			return req, fmt.Errorf("duplicate execution path ID %q", p.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.ExecutionPaths = append(out.ExecutionPaths, p)
	return out, nil
}

// AddErrorHandling returns a new RequirementsV1Json with the given
// ErrorHandlingEntry appended. Returns an error if an entry with the same ID
// already exists. The original is not modified.
func AddErrorHandling(req RequirementsV1Json, e ErrorHandlingEntry) (RequirementsV1Json, error) {
	for _, existing := range req.ErrorHandling {
		if existing.Id == e.Id {
			return req, fmt.Errorf("duplicate error handling ID %q", e.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.ErrorHandling = append(out.ErrorHandling, e)
	return out, nil
}

// ---------------------------------------------------------------------------
// Criterion-level mutation functions (05-REQ-2)
// ---------------------------------------------------------------------------

// AddCriterion returns a new Requirement with the given Criterion appended to
// acceptance_criteria. Returns an error if a Criterion with the same ID
// already exists in acceptance_criteria. The original is not modified.
func AddCriterion(r Requirement, c Criterion) (Requirement, error) {
	for _, existing := range r.AcceptanceCriteria {
		if existing.Id == c.Id {
			return r, fmt.Errorf("duplicate criterion ID %q", c.Id)
		}
	}
	out := r // shallow copy
	// Copy acceptance_criteria slice so append doesn't mutate the original.
	out.AcceptanceCriteria = make([]Criterion, len(r.AcceptanceCriteria), len(r.AcceptanceCriteria)+1)
	copy(out.AcceptanceCriteria, r.AcceptanceCriteria)
	out.AcceptanceCriteria = append(out.AcceptanceCriteria, c)
	// Copy edge_cases to isolate from original.
	if r.EdgeCases != nil {
		out.EdgeCases = make([]Criterion, len(r.EdgeCases))
		copy(out.EdgeCases, r.EdgeCases)
	}
	return out, nil
}

// AddEdgeCase returns a new Requirement with the given Criterion appended to
// edge_cases. Returns an error if a Criterion with the same ID already exists
// in edge_cases. The original is not modified.
func AddEdgeCase(r Requirement, c Criterion) (Requirement, error) {
	for _, existing := range r.EdgeCases {
		if existing.Id == c.Id {
			return r, fmt.Errorf("duplicate edge case ID %q", c.Id)
		}
	}
	out := r // shallow copy
	// Copy edge_cases slice so append doesn't mutate the original.
	out.EdgeCases = make([]Criterion, len(r.EdgeCases), len(r.EdgeCases)+1)
	copy(out.EdgeCases, r.EdgeCases)
	out.EdgeCases = append(out.EdgeCases, c)
	// Copy acceptance_criteria to isolate from original.
	if r.AcceptanceCriteria != nil {
		out.AcceptanceCriteria = make([]Criterion, len(r.AcceptanceCriteria))
		copy(out.AcceptanceCriteria, r.AcceptanceCriteria)
	}
	return out, nil
}

// GetCriterion searches both acceptance_criteria and edge_cases for a Criterion
// with the given ID. Returns a pointer to the first match and true, or nil and
// false if not found in either slice.
func GetCriterion(r Requirement, id string) (*Criterion, bool) {
	for i := range r.AcceptanceCriteria {
		if r.AcceptanceCriteria[i].Id == id {
			return &r.AcceptanceCriteria[i], true
		}
	}
	for i := range r.EdgeCases {
		if r.EdgeCases[i].Id == id {
			return &r.EdgeCases[i], true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// TestSpec collection mutation functions (05-REQ-3)
// ---------------------------------------------------------------------------

// deepCopyTestSpec returns a deep copy of a TestSpecV1Json, ensuring that all
// slice fields are independently allocated. Callers can safely mutate the
// returned struct without affecting the original.
func deepCopyTestSpec(ts TestSpecV1Json) TestSpecV1Json {
	out := ts // shallow copy of scalar/struct fields

	// Deep copy TestCases.
	if ts.TestCases != nil {
		out.TestCases = make([]TestCase, len(ts.TestCases))
		copy(out.TestCases, ts.TestCases)
	}
	// Deep copy PropertyTests (and nested Validates slices).
	if ts.PropertyTests != nil {
		out.PropertyTests = make([]PropertyTest, len(ts.PropertyTests))
		for i, pt := range ts.PropertyTests {
			out.PropertyTests[i] = pt
			if pt.Validates != nil {
				out.PropertyTests[i].Validates = make([]string, len(pt.Validates))
				copy(out.PropertyTests[i].Validates, pt.Validates)
			}
		}
	}
	// Deep copy EdgeCaseTests.
	if ts.EdgeCaseTests != nil {
		out.EdgeCaseTests = make([]EdgeCaseTest, len(ts.EdgeCaseTests))
		copy(out.EdgeCaseTests, ts.EdgeCaseTests)
	}
	// Deep copy SmokeTests (and nested slice fields).
	if ts.SmokeTests != nil {
		out.SmokeTests = make([]SmokeTest, len(ts.SmokeTests))
		for i, st := range ts.SmokeTests {
			out.SmokeTests[i] = st
			if st.ExpectedEffects != nil {
				out.SmokeTests[i].ExpectedEffects = make([]string, len(st.ExpectedEffects))
				copy(out.SmokeTests[i].ExpectedEffects, st.ExpectedEffects)
			}
			if st.RealComponents != nil {
				out.SmokeTests[i].RealComponents = make([]string, len(st.RealComponents))
				copy(out.SmokeTests[i].RealComponents, st.RealComponents)
			}
			if st.Mockable != nil {
				out.SmokeTests[i].Mockable = make([]string, len(st.Mockable))
				copy(out.SmokeTests[i].Mockable, st.Mockable)
			}
		}
	}
	return out
}

// AddTestCase returns a new TestSpecV1Json with the given TestCase appended.
// Returns an error if a TestCase with the same ID already exists.
// The original is not modified.
func AddTestCase(ts TestSpecV1Json, tc TestCase) (TestSpecV1Json, error) {
	for _, existing := range ts.TestCases {
		if existing.Id == tc.Id {
			return ts, fmt.Errorf("duplicate test case ID %q", tc.Id)
		}
	}
	out := deepCopyTestSpec(ts)
	if out.TestCases == nil {
		out.TestCases = make([]TestCase, 0, 1)
	}
	out.TestCases = append(out.TestCases, tc)
	return out, nil
}

// AddPropertyTest returns a new TestSpecV1Json with the given PropertyTest
// appended. Returns an error if a PropertyTest with the same ID already exists.
// The original is not modified.
func AddPropertyTest(ts TestSpecV1Json, pt PropertyTest) (TestSpecV1Json, error) {
	for _, existing := range ts.PropertyTests {
		if existing.Id == pt.Id {
			return ts, fmt.Errorf("duplicate property test ID %q", pt.Id)
		}
	}
	out := deepCopyTestSpec(ts)
	if out.PropertyTests == nil {
		out.PropertyTests = make([]PropertyTest, 0, 1)
	}
	out.PropertyTests = append(out.PropertyTests, pt)
	return out, nil
}

// AddEdgeCaseTest returns a new TestSpecV1Json with the given EdgeCaseTest
// appended. Returns an error if an EdgeCaseTest with the same ID already
// exists. The original is not modified.
func AddEdgeCaseTest(ts TestSpecV1Json, et EdgeCaseTest) (TestSpecV1Json, error) {
	for _, existing := range ts.EdgeCaseTests {
		if existing.Id == et.Id {
			return ts, fmt.Errorf("duplicate edge case test ID %q", et.Id)
		}
	}
	out := deepCopyTestSpec(ts)
	if out.EdgeCaseTests == nil {
		out.EdgeCaseTests = make([]EdgeCaseTest, 0, 1)
	}
	out.EdgeCaseTests = append(out.EdgeCaseTests, et)
	return out, nil
}

// AddSmokeTest returns a new TestSpecV1Json with the given SmokeTest appended.
// Returns an error if a SmokeTest with the same ID already exists.
// The original is not modified.
func AddSmokeTest(ts TestSpecV1Json, st SmokeTest) (TestSpecV1Json, error) {
	for _, existing := range ts.SmokeTests {
		if existing.Id == st.Id {
			return ts, fmt.Errorf("duplicate smoke test ID %q", st.Id)
		}
	}
	out := deepCopyTestSpec(ts)
	if out.SmokeTests == nil {
		out.SmokeTests = make([]SmokeTest, 0, 1)
	}
	out.SmokeTests = append(out.SmokeTests, st)
	return out, nil
}

// ---------------------------------------------------------------------------
// Tasks collection mutation functions (05-REQ-4)
// ---------------------------------------------------------------------------

// deepCopyTasks returns a deep copy of a TasksV1Json, ensuring that all slice
// fields are independently allocated. Callers can safely mutate the returned
// struct without affecting the original.
func deepCopyTasks(t TasksV1Json) TasksV1Json {
	out := t // shallow copy of scalar/struct fields

	// Deep copy TaskGroups (and nested Subtasks within each group).
	out.TaskGroups = make([]TaskGroup, len(t.TaskGroups))
	for i, g := range t.TaskGroups {
		out.TaskGroups[i] = g
		out.TaskGroups[i].Subtasks = make([]Subtask, len(g.Subtasks))
		for j, s := range g.Subtasks {
			out.TaskGroups[i].Subtasks[j] = s
			// Deep copy nested slices within Subtask.
			out.TaskGroups[i].Subtasks[j].Details = make([]string, len(s.Details))
			copy(out.TaskGroups[i].Subtasks[j].Details, s.Details)
			out.TaskGroups[i].Subtasks[j].RequirementRefs = make([]string, len(s.RequirementRefs))
			copy(out.TaskGroups[i].Subtasks[j].RequirementRefs, s.RequirementRefs)
			out.TaskGroups[i].Subtasks[j].TestSpecRefs = make([]string, len(s.TestSpecRefs))
			copy(out.TaskGroups[i].Subtasks[j].TestSpecRefs, s.TestSpecRefs)
		}
		// Deep copy Verification checks slice.
		out.TaskGroups[i].Verification.Checks = make([]string, len(g.Verification.Checks))
		copy(out.TaskGroups[i].Verification.Checks, g.Verification.Checks)
	}

	// Deep copy Dependencies slice.
	out.Dependencies = make([]TaskDependency, len(t.Dependencies))
	copy(out.Dependencies, t.Dependencies)

	// Deep copy Traceability slice.
	out.Traceability = make([]TraceabilityEntry, len(t.Traceability))
	copy(out.Traceability, t.Traceability)

	return out
}

// AddTaskGroup returns a new TasksV1Json with the given TaskGroup appended.
// Returns an error if a TaskGroup with the same ID already exists.
// The original is not modified.
func AddTaskGroup(t TasksV1Json, g TaskGroup) (TasksV1Json, error) {
	for _, existing := range t.TaskGroups {
		if existing.Id == g.Id {
			return t, fmt.Errorf("duplicate task group ID %d", g.Id)
		}
	}
	out := deepCopyTasks(t)
	out.TaskGroups = append(out.TaskGroups, g)
	return out, nil
}

// AddSubtask returns a new TaskGroup with the given Subtask appended.
// Returns an error if a Subtask with the same ID already exists.
// The original is not modified.
func AddSubtask(g TaskGroup, s Subtask) (TaskGroup, error) {
	for _, existing := range g.Subtasks {
		if existing.Id == s.Id {
			return g, fmt.Errorf("duplicate subtask ID %q", s.Id)
		}
	}
	out := g // shallow copy
	// Copy subtasks slice so append doesn't mutate the original.
	out.Subtasks = make([]Subtask, len(g.Subtasks), len(g.Subtasks)+1)
	copy(out.Subtasks, g.Subtasks)
	out.Subtasks = append(out.Subtasks, s)
	// Copy verification checks to isolate from original.
	out.Verification.Checks = make([]string, len(g.Verification.Checks))
	copy(out.Verification.Checks, g.Verification.Checks)
	return out, nil
}

// AddTraceabilityEntry returns a new TasksV1Json with the given
// TraceabilityEntry appended. Returns an error if an entry with the same
// (requirement_id, test_spec_id) pair already exists.
// The original is not modified.
func AddTraceabilityEntry(t TasksV1Json, e TraceabilityEntry) (TasksV1Json, error) {
	for _, existing := range t.Traceability {
		if existing.RequirementId == e.RequirementId && existing.TestSpecId == e.TestSpecId {
			return t, fmt.Errorf("duplicate traceability entry (requirement_id=%q, test_spec_id=%q)", e.RequirementId, e.TestSpecId)
		}
	}
	out := deepCopyTasks(t)
	out.Traceability = append(out.Traceability, e)
	return out, nil
}

// AddDependency returns a new TasksV1Json with the given TaskDependency
// appended unconditionally. The original is not modified.
func AddDependency(t TasksV1Json, d TaskDependency) TasksV1Json {
	out := deepCopyTasks(t)
	out.Dependencies = append(out.Dependencies, d)
	return out
}

// ---------------------------------------------------------------------------
// LoadDependentInterfaces — cross-spec context loading (05-REQ-10)
// ---------------------------------------------------------------------------

// LoadDependentInterfaces loads interface summaries from upstream dependency
// specs for the given specID. It calls DiscoverSpecs, BuildDependencyGraph,
// and LoadSpec for each upstream dependency, extracting glossary entries,
// external API symbols, and criterion return contracts into a map[string]any
// per upstream spec.
//
// Returns an empty slice on any error (never returns an error value, never
// panics in production).
func LoadDependentInterfaces(specID string, specRoot string) []map[string]any {
	// Discover all specs under specRoot.
	metas, err := DiscoverSpecs(specRoot)
	if err != nil {
		// Graceful degradation: return empty slice without propagating.
		return []map[string]any{}
	}

	// Build the dependency graph.
	graph, err := BuildDependencyGraph(metas, specRoot)
	if err != nil && graph == nil {
		// Only bail if we got no graph at all; a partial graph with
		// dangling-reference warnings is still usable.
		return []map[string]any{}
	}

	// Find upstream dependencies for specID.
	// Dependencies() returns edges where FromSpec == specID,
	// meaning specID depends on ToSpec (ToSpec is upstream).
	deps := graph.Dependencies(specID)
	if len(deps) == 0 {
		return []map[string]any{}
	}

	// Build a lookup from spec ID to directory path.
	metaByID := make(map[string]string, len(metas))
	for _, m := range metas {
		metaByID[m.SpecID] = m.Dir
	}

	var result []map[string]any
	for _, dep := range deps {
		upstreamID := dep.ToSpec
		dir, ok := metaByID[upstreamID]
		if !ok {
			// Unknown upstream spec — skip.
			continue
		}

		spec, err := LoadSpec(dir)
		if err != nil {
			// Graceful degradation: skip this upstream, continue with others.
			continue
		}

		entry := extractInterfaceSummary(spec)
		result = append(result, entry)
	}

	if result == nil {
		result = []map[string]any{}
	}
	return result
}

// extractInterfaceSummary builds a map[string]any containing the glossary
// entries, external API symbols, and criterion return contracts from a Spec.
func extractInterfaceSummary(spec *Spec) map[string]any {
	entry := make(map[string]any)

	// Extract glossary entries.
	if spec.Requirements != nil && spec.Requirements.Glossary != nil {
		glossary := make(map[string]string, len(spec.Requirements.Glossary))
		for k, v := range spec.Requirements.Glossary {
			glossary[k] = v
		}
		entry["glossary"] = glossary
	}

	// Extract external API symbols.
	if spec.Requirements != nil && len(spec.Requirements.ExternalApis) > 0 {
		var symbols []map[string]string
		for _, api := range spec.Requirements.ExternalApis {
			for _, sym := range api.Symbols {
				symbols = append(symbols, map[string]string{
					"name":        sym.Name,
					"import_path": sym.ImportPath,
					"signature":   sym.Signature,
				})
			}
		}
		entry["external_apis"] = symbols
	}

	// Extract criterion return contracts from acceptance criteria.
	if spec.Requirements != nil {
		var contracts []map[string]string
		for _, req := range spec.Requirements.Requirements {
			for _, ac := range req.AcceptanceCriteria {
				if ac.ReturnContract != nil && *ac.ReturnContract != "" {
					contracts = append(contracts, map[string]string{
						"criterion_id":    ac.Id,
						"return_contract": *ac.ReturnContract,
					})
				}
			}
		}
		if len(contracts) > 0 {
			entry["return_contracts"] = contracts
		}
	}

	return entry
}
