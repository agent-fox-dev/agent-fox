package afspec

import (
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec/legacy"
)

const fixtureV1Spec = "../testdata/v1_spec"

func migrateFixture(t *testing.T) (*RequirementsV2Json, *TestSpecV2Json, *TasksV2Json, *MigrationReport) {
	t.Helper()
	src, err := legacy.LoadSpec(fixtureV1Spec)
	if err != nil {
		t.Fatalf("legacy.LoadSpec = %v", err)
	}
	req, ts, tasks, report, err := Migrate(src, "01", "test_feature")
	if err != nil {
		t.Fatalf("Migrate = %v", err)
	}
	return req, ts, tasks, report
}

// TestMigrateProducesAValidV2Spec is the contract: whatever a v1 spec looked
// like, the converted spec passes schema validation and every rule C1 to C11.
func TestMigrateProducesAValidV2Spec(t *testing.T) {
	req, ts, tasks, _ := migrateFixture(t)

	spec := &Spec{
		SpecID: "01", SpecName: "test_feature", Title: "Test Feature", Status: "draft",
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		SchemaVersion: SchemaVersion,
		Requirements:  req, TestSpec: ts, Tasks: tasks,
	}

	result := spec.Validate()
	if !result.Valid {
		t.Fatalf("the migrated spec does not validate: %v", result.Errors)
	}
}

// TestMigrateFoldsEdgeCasesIntoCriteria covers §13: an edge case is a
// criterion, and its .E ID becomes an ordinary .N one.
func TestMigrateFoldsEdgeCasesIntoCriteria(t *testing.T) {
	req, _, _, _ := migrateFixture(t)

	if len(req.Requirements) < 1 {
		t.Fatal("no requirements survived the migration")
	}
	first := req.Requirements[0]
	if len(first.Criteria) != 2 {
		t.Fatalf("requirement %s has %d criteria; want the acceptance criterion plus the edge case",
			first.Id, len(first.Criteria))
	}
	if first.Criteria[0].Id != "01-REQ-1.1" || first.Criteria[1].Id != "01-REQ-1.2" {
		t.Errorf("criterion IDs = %q, %q; want a single .N series",
			first.Criteria[0].Id, first.Criteria[1].Id)
	}
	if first.Criteria[1].Pattern != CriterionPatternUnwanted {
		t.Errorf("the migrated edge case has pattern %q; want unwanted", first.Criteria[1].Pattern)
	}
	if first.Rationale == nil || !strings.Contains(*first.Rationale, "As a developer") {
		t.Errorf("the v1 user_story did not become a rationale: %v", first.Rationale)
	}
}

func TestMigrateTurnsPropertiesIntoUbiquitousCriteria(t *testing.T) {
	req, ts, _, _ := migrateFixture(t)

	var propCriterion *Criterion
	for _, r := range req.Requirements {
		for i, c := range r.Criteria {
			if strings.HasPrefix(c.Action, "for any ") {
				propCriterion = &r.Criteria[i]
			}
		}
	}
	if propCriterion == nil {
		t.Fatal("the v1 correctness property did not become a ubiquitous criterion")
	}
	if propCriterion.Pattern != CriterionPatternUbiquitous {
		t.Errorf("pattern = %q; want ubiquitous", propCriterion.Pattern)
	}

	found := false
	for _, test := range ts.Tests {
		if test.Kind == TestKindProperty {
			found = true
		}
	}
	if !found {
		t.Error("no test of kind property survived the migration")
	}
}

// TestMigrateCollapsesTheTestIDFamilies covers the v1 → v2 ID change: four
// families (TS-N, TS-EN, TS-PN, TS-SMOKE-N) become one series.
func TestMigrateCollapsesTheTestIDFamilies(t *testing.T) {
	_, ts, _, _ := migrateFixture(t)

	if len(ts.Tests) != 4 {
		t.Fatalf("tests = %d; want the unit, edge-case, property and smoke tests", len(ts.Tests))
	}
	for i, test := range ts.Tests {
		want := "TS-01-" + string(rune('1'+i))
		if test.Id != want {
			t.Errorf("test %d has ID %q; want %q", i, test.Id, want)
		}
		if strings.Contains(test.Id, "-E") || strings.Contains(test.Id, "-P") || strings.Contains(test.Id, "SMOKE") {
			t.Errorf("test ID %q still carries a v1 family marker", test.Id)
		}
	}

	kinds := map[TestKind]int{}
	for _, test := range ts.Tests {
		kinds[test.Kind]++
	}
	if kinds[TestKindSmoke] != 1 || kinds[TestKindProperty] != 1 || kinds[TestKindUnit] != 2 {
		t.Errorf("kinds = %v; want two unit, one property and one smoke test", kinds)
	}
}

// TestMigrateAttachesTheOrphanedTests is the part v1 never had, and the reason
// the converter cannot be purely mechanical: no archived v1 spec owned a
// single edge-case or property test.
func TestMigrateAttachesTheOrphanedTests(t *testing.T) {
	_, ts, tasks, report := migrateFixture(t)

	owned := map[string]bool{}
	for _, task := range tasks.Tasks {
		for _, id := range task.Tests {
			owned[id] = true
		}
	}
	for _, test := range ts.Tests {
		if !owned[test.Id] {
			t.Errorf("test %s is still owned by no task after migration", test.Id)
		}
	}

	if len(report.Attached) == 0 {
		t.Error("the report claims nothing was attached, but the v1 fixture orphans its edge-case and property tests")
	}
	joined := strings.Join(report.Attached, ", ")
	if !strings.Contains(joined, "TS-01-2") || !strings.Contains(joined, "TS-01-3") {
		t.Errorf("the report does not name the attached tests: %v", report.Attached)
	}
}

// TestMigrateConvertsWiringGroupIntoTheIntegrationTask covers rule C9.
func TestMigrateConvertsWiringGroupIntoTheIntegrationTask(t *testing.T) {
	_, ts, tasks, _ := migrateFixture(t)

	if len(tasks.Tasks) < 2 {
		t.Fatalf("tasks = %d; want at least one implement task and the integration task", len(tasks.Tasks))
	}
	last := tasks.Tasks[len(tasks.Tasks)-1]
	if last.Kind != TaskKindIntegration {
		t.Fatalf("the last task has kind %q; want integration", last.Kind)
	}

	for _, task := range tasks.Tasks[:len(tasks.Tasks)-1] {
		if task.Kind != TaskKindImplement {
			t.Errorf("task %d has kind %q; only the last task may be integration", task.Id, task.Kind)
		}
	}

	for _, test := range ts.Tests {
		if test.Kind != TestKindSmoke {
			continue
		}
		if !containsString(last.Tests, test.Id) {
			t.Errorf("the integration task does not own smoke test %s", test.Id)
		}
	}

	if len(last.Steps) == 0 {
		t.Error("the integration task has no steps")
	}
}

func TestMigrateFoldsAwayTheTestsGroup(t *testing.T) {
	_, _, tasks, report := migrateFixture(t)

	for _, task := range tasks.Tasks {
		if strings.Contains(strings.ToLower(task.Title), "write failing spec tests") {
			t.Errorf("the v1 \"tests\" group survived as task %d; v2 writes tests inside the task that owns them", task.Id)
		}
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), "folded") {
		t.Errorf("the report does not mention folding the tests group: %v", report.Notes)
	}
}

func TestMigrateDropsErrorHandlingAndReportsIt(t *testing.T) {
	_, _, _, report := migrateFixture(t)
	if !strings.Contains(strings.Join(report.Notes, "\n"), "error_handling") {
		t.Errorf("the report does not mention the dropped error_handling entries: %v", report.Notes)
	}
}

func TestMigrateGivesEveryUnwantedCriterionAContract(t *testing.T) {
	req, _, _, _ := migrateFixture(t)
	for _, r := range req.Requirements {
		for _, c := range r.Criteria {
			if c.Pattern == CriterionPatternUnwanted && c.ContractText() == "" {
				t.Errorf("unwanted criterion %s has no contract; rule C10 would reject the migrated spec", c.Id)
			}
		}
	}
}

func TestMigrateRejectsAnIncompleteSource(t *testing.T) {
	if _, _, _, _, err := Migrate(nil, "01", "x"); err == nil {
		t.Error("Migrate accepted a nil source")
	}
	if _, _, _, _, err := Migrate(&legacy.Spec{}, "01", "x"); err == nil {
		t.Error("Migrate accepted a source with no artifacts")
	}
}

func TestLegacyLoadSpecRejectsANonV1Directory(t *testing.T) {
	if _, err := legacy.LoadSpec(fixtureValidSpec); err == nil {
		t.Error("legacy.LoadSpec accepted a version 2 spec")
	}
	if _, err := legacy.LoadSpec("../testdata/does_not_exist"); err == nil {
		t.Error("legacy.LoadSpec accepted a directory that does not exist")
	}
}
