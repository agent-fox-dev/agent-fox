package afspec

import "fmt"

// ---------------------------------------------------------------------------
// requirements.json
// ---------------------------------------------------------------------------

// deepCopyRequirements returns a copy whose slices and maps are independent of
// the original's.
func deepCopyRequirements(req RequirementsV2Json) RequirementsV2Json {
	out := req

	if req.Glossary != nil {
		out.Glossary = make(RequirementsV2JsonGlossary, len(req.Glossary))
		for k, v := range req.Glossary {
			out.Glossary[k] = v
		}
	}

	if req.Requirements != nil {
		out.Requirements = make([]Requirement, len(req.Requirements))
		for i, r := range req.Requirements {
			out.Requirements[i] = deepCopyRequirement(r)
		}
	}

	if req.ExecutionPaths != nil {
		out.ExecutionPaths = make([]ExecutionPath, len(req.ExecutionPaths))
		for i, p := range req.ExecutionPaths {
			out.ExecutionPaths[i] = p
			out.ExecutionPaths[i].Steps = make([]PathStep, len(p.Steps))
			copy(out.ExecutionPaths[i].Steps, p.Steps)
		}
	}

	if req.ExternalApis != nil {
		out.ExternalApis = make([]ExternalApi, len(req.ExternalApis))
		for i, a := range req.ExternalApis {
			out.ExternalApis[i] = a
			out.ExternalApis[i].Symbols = make([]ExternalApiSymbol, len(a.Symbols))
			copy(out.ExternalApis[i].Symbols, a.Symbols)
		}
	}

	return out
}

func deepCopyRequirement(r Requirement) Requirement {
	out := r
	if r.Criteria != nil {
		out.Criteria = make([]Criterion, len(r.Criteria))
		copy(out.Criteria, r.Criteria)
	}
	return out
}

// AddRequirement appends a requirement and returns a new artifact. It fails
// when the ID is already in use.
func AddRequirement(req RequirementsV2Json, r Requirement) (RequirementsV2Json, error) {
	for _, existing := range req.Requirements {
		if existing.Id == r.Id {
			return req, fmt.Errorf("duplicate requirement ID %q", r.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.Requirements = append(out.Requirements, deepCopyRequirement(r))
	return out, nil
}

// GetRequirement returns a copy of the requirement with the given ID.
func GetRequirement(req RequirementsV2Json, id string) (*Requirement, bool) {
	for _, r := range req.Requirements {
		if r.Id == id {
			out := deepCopyRequirement(r)
			return &out, true
		}
	}
	return nil, false
}

// RemoveRequirement drops the requirement with the given ID and reports
// whether it was present.
func RemoveRequirement(req RequirementsV2Json, id string) (RequirementsV2Json, bool) {
	idx := -1
	for i, r := range req.Requirements {
		if r.Id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return deepCopyRequirements(req), false
	}
	out := deepCopyRequirements(req)
	out.Requirements = append(out.Requirements[:idx], out.Requirements[idx+1:]...)
	return out, true
}

// SetGlossaryEntry sets a glossary term, replacing any existing definition.
func SetGlossaryEntry(req RequirementsV2Json, term, definition string) RequirementsV2Json {
	out := deepCopyRequirements(req)
	if out.Glossary == nil {
		out.Glossary = RequirementsV2JsonGlossary{}
	}
	out.Glossary[term] = definition
	return out
}

// RemoveGlossaryEntry drops a glossary term and reports whether it was present.
func RemoveGlossaryEntry(req RequirementsV2Json, term string) (RequirementsV2Json, bool) {
	out := deepCopyRequirements(req)
	if _, ok := out.Glossary[term]; !ok {
		return out, false
	}
	delete(out.Glossary, term)
	return out, true
}

// AddExecutionPath appends an execution path. It fails on a duplicate ID.
func AddExecutionPath(req RequirementsV2Json, p ExecutionPath) (RequirementsV2Json, error) {
	for _, existing := range req.ExecutionPaths {
		if existing.Id == p.Id {
			return req, fmt.Errorf("duplicate execution path ID %q", p.Id)
		}
	}
	out := deepCopyRequirements(req)
	out.ExecutionPaths = append(out.ExecutionPaths, p)
	return out, nil
}

// AddExternalAPI appends an external API package entry.
func AddExternalAPI(req RequirementsV2Json, api ExternalApi) (RequirementsV2Json, error) {
	for _, existing := range req.ExternalApis {
		if existing.Package == api.Package {
			return req, fmt.Errorf("duplicate external API package %q", api.Package)
		}
	}
	out := deepCopyRequirements(req)
	out.ExternalApis = append(out.ExternalApis, api)
	return out, nil
}

// AddCriterion appends a criterion to a requirement. It fails on a duplicate
// ID within that requirement.
func AddCriterion(r Requirement, c Criterion) (Requirement, error) {
	for _, existing := range r.Criteria {
		if existing.Id == c.Id {
			return r, fmt.Errorf("duplicate criterion ID %q", c.Id)
		}
	}
	out := deepCopyRequirement(r)
	out.Criteria = append(out.Criteria, c)
	return out, nil
}

// GetCriterion returns a copy of the criterion with the given ID.
func GetCriterion(r Requirement, id string) (*Criterion, bool) {
	for _, c := range r.Criteria {
		if c.Id == id {
			out := c
			return &out, true
		}
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// test_spec.json
// ---------------------------------------------------------------------------

func deepCopyTestSpec(ts TestSpecV2Json) TestSpecV2Json {
	out := ts
	if ts.Tests != nil {
		out.Tests = make([]Test, len(ts.Tests))
		for i, t := range ts.Tests {
			out.Tests[i] = t
			out.Tests[i].Verifies = copyStrings(t.Verifies)
			out.Tests[i].Given = copyStrings(t.Given)
			out.Tests[i].Then = copyStrings(t.Then)
			out.Tests[i].RealComponents = copyStrings(t.RealComponents)
		}
	}
	return out
}

// AddTest appends a test to the single flat list. It fails on a duplicate ID.
func AddTest(ts TestSpecV2Json, t Test) (TestSpecV2Json, error) {
	for _, existing := range ts.Tests {
		if existing.Id == t.Id {
			return ts, fmt.Errorf("duplicate test ID %q", t.Id)
		}
	}
	out := deepCopyTestSpec(ts)
	out.Tests = append(out.Tests, t)
	return out, nil
}

// GetTest returns a copy of the test with the given ID.
func GetTest(ts TestSpecV2Json, id string) (*Test, bool) {
	for _, t := range ts.Tests {
		if t.Id == id {
			out := t
			return &out, true
		}
	}
	return nil, false
}

// TestsOfKind returns every test of the given kind, in artifact order.
func (ts *TestSpecV2Json) TestsOfKind(kind TestKind) []Test {
	var out []Test
	for _, t := range ts.Tests {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// tasks.json
// ---------------------------------------------------------------------------

func deepCopyTasks(t TasksV2Json) TasksV2Json {
	return *t.deepCopy()
}

// AddTask appends a task. It fails on a duplicate ID.
func AddTask(t TasksV2Json, task Task) (TasksV2Json, error) {
	for _, existing := range t.Tasks {
		if existing.Id == task.Id {
			return t, fmt.Errorf("duplicate task ID %d", task.Id)
		}
	}
	out := deepCopyTasks(t)
	out.Tasks = append(out.Tasks, task)
	return out, nil
}

// AddDependency appends a cross-spec dependency unconditionally.
func AddDependency(t TasksV2Json, d Dependency) TasksV2Json {
	out := deepCopyTasks(t)
	out.Dependencies = append(out.Dependencies, d)
	return out
}

// ---------------------------------------------------------------------------
// LoadDependentInterfaces — cross-spec context loading
// ---------------------------------------------------------------------------

// LoadDependentInterfaces loads interface summaries from the upstream specs
// that specID depends on: glossary entries, external API symbols and criterion
// contracts, one map per upstream spec.
//
// It degrades gracefully: any spec that cannot be discovered or loaded is
// skipped and the remaining ones are still returned. The result is never nil.
func LoadDependentInterfaces(specID string, specRoot string) []map[string]any {
	metas, err := DiscoverSpecs(specRoot)
	if err != nil {
		return []map[string]any{}
	}

	graph, err := BuildDependencyGraph(metas, specRoot)
	if err != nil && graph == nil {
		// Only bail if we got no graph at all; a partial graph with
		// dangling-reference warnings is still usable.
		return []map[string]any{}
	}

	// Dependencies() returns edges where FromSpec == specID, meaning specID
	// depends on ToSpec, so ToSpec is the upstream spec.
	deps := graph.Dependencies(specID)
	if len(deps) == 0 {
		return []map[string]any{}
	}

	metaByID := make(map[string]string, len(metas))
	for _, m := range metas {
		metaByID[m.SpecID] = m.Dir
	}

	result := []map[string]any{}
	for _, dep := range deps {
		dir, ok := metaByID[dep.ToSpec]
		if !ok {
			continue
		}
		spec, err := LoadSpec(dir)
		if err != nil {
			continue
		}
		result = append(result, extractInterfaceSummary(spec))
	}
	return result
}

// extractInterfaceSummary collects the glossary, external API symbols and
// criterion contracts a downstream spec may need from an upstream one.
func extractInterfaceSummary(spec *Spec) map[string]any {
	entry := make(map[string]any)
	if spec.Requirements == nil {
		return entry
	}

	if spec.Requirements.Glossary != nil {
		glossary := make(map[string]string, len(spec.Requirements.Glossary))
		for k, v := range spec.Requirements.Glossary {
			glossary[k] = v
		}
		entry["glossary"] = glossary
	}

	if len(spec.Requirements.ExternalApis) > 0 {
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

	var contracts []map[string]string
	for _, req := range spec.Requirements.Requirements {
		for _, c := range req.Criteria {
			if contract := c.ContractText(); contract != "" {
				contracts = append(contracts, map[string]string{
					"criterion_id": c.Id,
					"contract":     contract,
				})
			}
		}
	}
	if len(contracts) > 0 {
		entry["contracts"] = contracts
	}

	return entry
}
