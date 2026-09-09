package afspec

import "sort"

// CoverageReport lists the criteria and paths that are verified by at least
// one test (Covered) and those that are not (Uncovered), in artifact order.
//
// Format v2 §7.1: coverage is computed, never stored. Nothing here is written
// back to test_spec.json.
type CoverageReport struct {
	Covered   []string
	Uncovered []string
}

// ComputeCoverage reports which criteria and execution paths of req are
// verified by at least one test of ts, via each test's `verifies` list.
//
// A criterion is covered by any test that lists it. A path is covered by any
// test that lists it; rule C5 additionally requires that test to be a smoke
// test, which is a validation concern rather than a coverage one.
func (ts *TestSpecV2Json) ComputeCoverage(req *RequirementsV2Json) CoverageReport {
	verified := make(map[string]bool)
	for _, t := range ts.Tests {
		for _, id := range t.Verifies {
			verified[id] = true
		}
	}

	covered := []string{}
	uncovered := []string{}
	appendID := func(id string) {
		if verified[id] {
			covered = append(covered, id)
		} else {
			uncovered = append(uncovered, id)
		}
	}

	for _, r := range req.Requirements {
		for _, c := range r.Criteria {
			appendID(c.Id)
		}
	}
	for _, p := range req.ExecutionPaths {
		appendID(p.Id)
	}

	return CoverageReport{Covered: covered, Uncovered: uncovered}
}

// TraceLink is one row of the derived traceability matrix (§8.5):
//
//	criterion → tests (from test.verifies) → tasks (from task.tests)
//	path      → smoke tests               → integration task
//
// Kind is "criterion" or "path".
type TraceLink struct {
	Kind    string
	ID      string
	Tests   []string
	Tasks   []int
	Covered bool
	Owned   bool
}

// TraceabilityMatrix is the full derived matrix, in artifact order.
type TraceabilityMatrix struct {
	Links []TraceLink
}

// ComputeTraceability derives the traceability matrix from the three
// artifacts. Nothing is read from disk and nothing is stored, so the matrix
// cannot drift from the artifacts it describes (§8.5).
//
// Covered reports whether at least one test verifies the entity; Owned
// reports whether at least one of those tests is listed by a task.
func (s *Spec) ComputeTraceability() TraceabilityMatrix {
	matrix := TraceabilityMatrix{Links: []TraceLink{}}
	if s.Requirements == nil {
		return matrix
	}

	// entity ID → verifying test IDs, in test order.
	testsFor := map[string][]string{}
	if s.TestSpec != nil {
		for _, t := range s.TestSpec.Tests {
			for _, id := range t.Verifies {
				testsFor[id] = append(testsFor[id], t.Id)
			}
		}
	}

	// test ID → owning task IDs, in task order.
	tasksFor := map[string][]int{}
	if s.Tasks != nil {
		for _, task := range s.Tasks.Tasks {
			for _, testID := range task.Tests {
				tasksFor[testID] = append(tasksFor[testID], task.Id)
			}
		}
	}

	link := func(kind, id string) TraceLink {
		tests := testsFor[id]
		seen := map[int]bool{}
		var tasks []int
		for _, testID := range tests {
			for _, taskID := range tasksFor[testID] {
				if !seen[taskID] {
					seen[taskID] = true
					tasks = append(tasks, taskID)
				}
			}
		}
		sort.Ints(tasks)
		return TraceLink{
			Kind:    kind,
			ID:      id,
			Tests:   tests,
			Tasks:   tasks,
			Covered: len(tests) > 0,
			Owned:   len(tasks) > 0,
		}
	}

	for _, r := range s.Requirements.Requirements {
		for _, c := range r.Criteria {
			matrix.Links = append(matrix.Links, link("criterion", c.Id))
		}
	}
	for _, p := range s.Requirements.ExecutionPaths {
		matrix.Links = append(matrix.Links, link("path", p.Id))
	}

	return matrix
}
