package afspec

import "testing"

func TestComputeCoverageOnACompleteSpec(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	report := spec.TestSpec.ComputeCoverage(spec.Requirements)

	if len(report.Uncovered) != 0 {
		t.Errorf("uncovered = %v; the canonical fixture covers everything", report.Uncovered)
	}
	// Four criteria plus one path.
	if len(report.Covered) != 5 {
		t.Errorf("covered = %v; want the four criteria and the one path", report.Covered)
	}
}

func TestComputeCoverageReportsGaps(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	spec.TestSpec.Tests = spec.TestSpec.Tests[:1] // keep only TS-01-1

	report := spec.TestSpec.ComputeCoverage(spec.Requirements)
	if len(report.Covered) != 1 || report.Covered[0] != "01-REQ-1.1" {
		t.Errorf("covered = %v; want just 01-REQ-1.1", report.Covered)
	}
	want := []string{"01-REQ-1.2", "01-REQ-1.3", "01-REQ-2.1", "01-PATH-1"}
	if len(report.Uncovered) != len(want) {
		t.Fatalf("uncovered = %v; want %v", report.Uncovered, want)
	}
	for i, id := range want {
		if report.Uncovered[i] != id {
			t.Errorf("uncovered[%d] = %q; want %q (artifact order is preserved)", i, report.Uncovered[i], id)
		}
	}
}

// TestComputeTraceabilityIsDerived guards §8.5: the matrix comes from
// test.verifies and task.tests, so it cannot drift from the artifacts.
func TestComputeTraceabilityIsDerived(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	matrix := spec.ComputeTraceability()

	if len(matrix.Links) != 5 {
		t.Fatalf("links = %d; want four criteria plus one path", len(matrix.Links))
	}

	byID := map[string]TraceLink{}
	for _, l := range matrix.Links {
		byID[l.ID] = l
	}

	link := byID["01-REQ-1.1"]
	if link.Kind != "criterion" {
		t.Errorf("kind = %q; want criterion", link.Kind)
	}
	if len(link.Tests) != 2 || link.Tests[0] != "TS-01-1" || link.Tests[1] != "TS-01-5" {
		t.Errorf("tests = %v; want TS-01-1 and the smoke test that also verifies it", link.Tests)
	}
	if len(link.Tasks) != 2 || link.Tasks[0] != 1 || link.Tasks[1] != 3 {
		t.Errorf("tasks = %v; want the owning implement task and the integration task", link.Tasks)
	}
	if !link.Covered || !link.Owned {
		t.Errorf("covered=%v owned=%v; both should hold", link.Covered, link.Owned)
	}

	path := byID["01-PATH-1"]
	if path.Kind != "path" || len(path.Tests) != 1 || path.Tests[0] != "TS-01-5" {
		t.Errorf("path link = %+v; want the smoke test", path)
	}
	if len(path.Tasks) != 1 || path.Tasks[0] != 3 {
		t.Errorf("path tasks = %v; want the integration task", path.Tasks)
	}
}

func TestComputeTraceabilityMarksOrphans(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	// Orphan the property test: the criterion stays covered but unowned.
	spec.Tasks.Tasks[0].Tests = []string{"TS-01-1", "TS-01-2"}

	for _, link := range spec.ComputeTraceability().Links {
		if link.ID != "01-REQ-1.3" {
			continue
		}
		if !link.Covered {
			t.Error("the criterion is still verified by TS-01-3, so it is covered")
		}
		if link.Owned {
			t.Error("no task owns TS-01-3 any more, so the criterion is not owned")
		}
		return
	}
	t.Fatal("01-REQ-1.3 is missing from the matrix")
}

func TestComputeTraceabilityOnAnEmptySpec(t *testing.T) {
	spec := CreateSpec("05", "empty")
	if got := len(spec.ComputeTraceability().Links); got != 0 {
		t.Errorf("links = %d; want none", got)
	}

	var nilReqs Spec
	if got := len(nilReqs.ComputeTraceability().Links); got != 0 {
		t.Errorf("links = %d on a spec with no requirements; want none", got)
	}
}
