package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// makeTestSpecV1 creates a minimal TestSpecV1Json with optional pre-populated
// collections (all empty by default).
func makeTestSpecV1() TestSpecV1Json {
	return TestSpecV1Json{
		SpecId:        "05",
		SpecName:      "test_spec",
		SchemaVersion: 1,
		TestCases:     []TestCase{},
		PropertyTests: []PropertyTest{},
		EdgeCaseTests: []EdgeCaseTest{},
		SmokeTests:    []SmokeTest{},
		Coverage:      Coverage{},
	}
}

func makeTestCase(id string) TestCase {
	return TestCase{
		Id:                  id,
		Description:         "test case " + id,
		Kind:                TestCaseKindUnit,
		RequirementId:       "05-REQ-1",
		Preconditions:       []string{"precondition"},
		Expected:            "expected",
		AssertionPseudocode: "assert true",
	}
}

func makePropertyTest(id string) PropertyTest {
	return PropertyTest{
		Id:             id,
		Description:    "property " + id,
		PropertyId:     "05-PROP-1",
		ForAnyStrategy: "random",
		InvariantCheck: "check",
		Validates:      []string{"05-REQ-1"},
	}
}

func makeEdgeCaseTest(id string) EdgeCaseTest {
	return EdgeCaseTest{
		Id:                  id,
		Description:         "edge case " + id,
		Kind:                EdgeCaseTestKindUnit,
		RequirementId:       "05-REQ-1",
		Preconditions:       []string{},
		Expected:            "expected",
		AssertionPseudocode: "assert true",
	}
}

func makeSmokeTest(id string) SmokeTest {
	return SmokeTest{
		Id:              id,
		Description:     "smoke test " + id,
		ExecutionPathId: "05-PATH-1",
		Trigger:         "trigger",
		ExpectedEffects: []string{"effect"},
		RealComponents:  []string{"component"},
		Mockable:        []string{"mock"},
	}
}

// ---------------------------------------------------------------------------
// TS-05-11: AddTestCase with a non-duplicate ID
// Requirement: 05-REQ-3.1
// ---------------------------------------------------------------------------

func TestMutateAddTestCase(t *testing.T) {
	defer requireImplemented(t)

	original := makeTestSpecV1()
	original.TestCases = []TestCase{makeTestCase("TS-05-1")}

	newTs, err := AddTestCase(original, makeTestCase("TS-05-2"))

	if err != nil {
		t.Fatalf("AddTestCase returned unexpected error: %v", err)
	}
	if len(newTs.TestCases) != 2 {
		t.Errorf("new TestSpecV1Json has %d test cases, want 2", len(newTs.TestCases))
	}
	if newTs.TestCases[1].Id != "TS-05-2" {
		t.Errorf("appended test case ID = %q, want %q", newTs.TestCases[1].Id, "TS-05-2")
	}
	// Original must be unchanged.
	if len(original.TestCases) != 1 {
		t.Errorf("original has %d test cases, want 1 (immutability violated)", len(original.TestCases))
	}
}

// ---------------------------------------------------------------------------
// TS-05-12: AddPropertyTest with a non-duplicate ID
// Requirement: 05-REQ-3.2
// ---------------------------------------------------------------------------

func TestMutateAddPropertyTest(t *testing.T) {
	defer requireImplemented(t)

	original := makeTestSpecV1()
	original.PropertyTests = []PropertyTest{makePropertyTest("TS-05-P1")}

	newTs, err := AddPropertyTest(original, makePropertyTest("TS-05-P2"))

	if err != nil {
		t.Fatalf("AddPropertyTest returned unexpected error: %v", err)
	}
	if len(newTs.PropertyTests) != 2 {
		t.Errorf("new TestSpecV1Json has %d property tests, want 2", len(newTs.PropertyTests))
	}
	if newTs.PropertyTests[1].Id != "TS-05-P2" {
		t.Errorf("appended property test ID = %q, want %q", newTs.PropertyTests[1].Id, "TS-05-P2")
	}
	// Original must be unchanged.
	if len(original.PropertyTests) != 1 {
		t.Errorf("original has %d property tests, want 1 (immutability violated)", len(original.PropertyTests))
	}
}

// ---------------------------------------------------------------------------
// TS-05-13: AddEdgeCaseTest with a non-duplicate ID
// Requirement: 05-REQ-3.3
// ---------------------------------------------------------------------------

func TestMutateAddEdgeCaseTest(t *testing.T) {
	defer requireImplemented(t)

	original := makeTestSpecV1()
	original.EdgeCaseTests = []EdgeCaseTest{makeEdgeCaseTest("TS-05-E1")}

	newTs, err := AddEdgeCaseTest(original, makeEdgeCaseTest("TS-05-E2"))

	if err != nil {
		t.Fatalf("AddEdgeCaseTest returned unexpected error: %v", err)
	}
	if len(newTs.EdgeCaseTests) != 2 {
		t.Errorf("new TestSpecV1Json has %d edge case tests, want 2", len(newTs.EdgeCaseTests))
	}
	if newTs.EdgeCaseTests[1].Id != "TS-05-E2" {
		t.Errorf("appended edge case test ID = %q, want %q", newTs.EdgeCaseTests[1].Id, "TS-05-E2")
	}
	// Original must be unchanged.
	if len(original.EdgeCaseTests) != 1 {
		t.Errorf("original has %d edge case tests, want 1 (immutability violated)", len(original.EdgeCaseTests))
	}
}

// ---------------------------------------------------------------------------
// TS-05-14: AddSmokeTest with a non-duplicate ID
// Requirement: 05-REQ-3.4
// ---------------------------------------------------------------------------

func TestMutateAddSmokeTest(t *testing.T) {
	defer requireImplemented(t)

	original := makeTestSpecV1()
	original.SmokeTests = []SmokeTest{makeSmokeTest("TS-05-SMOKE-1")}

	newTs, err := AddSmokeTest(original, makeSmokeTest("TS-05-SMOKE-2"))

	if err != nil {
		t.Fatalf("AddSmokeTest returned unexpected error: %v", err)
	}
	if len(newTs.SmokeTests) != 2 {
		t.Errorf("new TestSpecV1Json has %d smoke tests, want 2", len(newTs.SmokeTests))
	}
	if newTs.SmokeTests[1].Id != "TS-05-SMOKE-2" {
		t.Errorf("appended smoke test ID = %q, want %q", newTs.SmokeTests[1].Id, "TS-05-SMOKE-2")
	}
	// Original must be unchanged.
	if len(original.SmokeTests) != 1 {
		t.Errorf("original has %d smoke tests, want 1 (immutability violated)", len(original.SmokeTests))
	}
}

// ---------------------------------------------------------------------------
// TS-05-44: AddTestCase, AddPropertyTest, AddEdgeCaseTest, AddSmokeTest
//           with duplicate IDs
// Requirement: 05-REQ-3.E1
// ---------------------------------------------------------------------------

func TestMutateTestSpec_AddDuplicates(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.TestCases = []TestCase{makeTestCase("TS-05-1")}
	ts.PropertyTests = []PropertyTest{makePropertyTest("TS-05-P1")}
	ts.EdgeCaseTests = []EdgeCaseTest{makeEdgeCaseTest("TS-05-E1")}
	ts.SmokeTests = []SmokeTest{makeSmokeTest("TS-05-SMOKE-1")}

	// AddTestCase duplicate
	r1, e1 := AddTestCase(ts, makeTestCase("TS-05-1"))
	if e1 == nil {
		t.Error("AddTestCase with duplicate ID returned nil error")
	} else if !strings.Contains(e1.Error(), "TS-05-1") {
		t.Errorf("AddTestCase error %q does not contain %q", e1.Error(), "TS-05-1")
	}
	if len(r1.TestCases) != 1 {
		t.Errorf("TestCases count = %d, want 1", len(r1.TestCases))
	}

	// AddPropertyTest duplicate
	r2, e2 := AddPropertyTest(ts, makePropertyTest("TS-05-P1"))
	if e2 == nil {
		t.Error("AddPropertyTest with duplicate ID returned nil error")
	} else if !strings.Contains(e2.Error(), "TS-05-P1") {
		t.Errorf("AddPropertyTest error %q does not contain %q", e2.Error(), "TS-05-P1")
	}
	if len(r2.PropertyTests) != 1 {
		t.Errorf("PropertyTests count = %d, want 1", len(r2.PropertyTests))
	}

	// AddEdgeCaseTest duplicate
	r3, e3 := AddEdgeCaseTest(ts, makeEdgeCaseTest("TS-05-E1"))
	if e3 == nil {
		t.Error("AddEdgeCaseTest with duplicate ID returned nil error")
	} else if !strings.Contains(e3.Error(), "TS-05-E1") {
		t.Errorf("AddEdgeCaseTest error %q does not contain %q", e3.Error(), "TS-05-E1")
	}
	if len(r3.EdgeCaseTests) != 1 {
		t.Errorf("EdgeCaseTests count = %d, want 1", len(r3.EdgeCaseTests))
	}

	// AddSmokeTest duplicate
	r4, e4 := AddSmokeTest(ts, makeSmokeTest("TS-05-SMOKE-1"))
	if e4 == nil {
		t.Error("AddSmokeTest with duplicate ID returned nil error")
	} else if !strings.Contains(e4.Error(), "TS-05-SMOKE-1") {
		t.Errorf("AddSmokeTest error %q does not contain %q", e4.Error(), "TS-05-SMOKE-1")
	}
	if len(r4.SmokeTests) != 1 {
		t.Errorf("SmokeTests count = %d, want 1", len(r4.SmokeTests))
	}
}

// ---------------------------------------------------------------------------
// TS-05-45: AddTestCase, AddPropertyTest, AddEdgeCaseTest, AddSmokeTest
//           on nil target slices
// Requirement: 05-REQ-3.E2
// ---------------------------------------------------------------------------

func TestMutateTestSpec_AddToNilSlices(t *testing.T) {
	defer requireImplemented(t)

	// Construct with all collection fields at zero value (nil slices).
	ts := TestSpecV1Json{
		SpecId:        "05",
		SpecName:      "test_spec",
		SchemaVersion: 1,
		Coverage:      Coverage{},
	}

	r1, e1 := AddTestCase(ts, makeTestCase("TS-05-1"))
	if e1 != nil {
		t.Fatalf("AddTestCase on nil slice returned error: %v", e1)
	}
	if r1.TestCases == nil {
		t.Fatal("AddTestCase returned nil TestCases, want non-nil")
	}
	if len(r1.TestCases) != 1 {
		t.Errorf("TestCases length = %d, want 1", len(r1.TestCases))
	}

	r2, e2 := AddPropertyTest(ts, makePropertyTest("TS-05-P1"))
	if e2 != nil {
		t.Fatalf("AddPropertyTest on nil slice returned error: %v", e2)
	}
	if r2.PropertyTests == nil {
		t.Fatal("AddPropertyTest returned nil PropertyTests, want non-nil")
	}
	if len(r2.PropertyTests) != 1 {
		t.Errorf("PropertyTests length = %d, want 1", len(r2.PropertyTests))
	}

	r3, e3 := AddEdgeCaseTest(ts, makeEdgeCaseTest("TS-05-E1"))
	if e3 != nil {
		t.Fatalf("AddEdgeCaseTest on nil slice returned error: %v", e3)
	}
	if r3.EdgeCaseTests == nil {
		t.Fatal("AddEdgeCaseTest returned nil EdgeCaseTests, want non-nil")
	}
	if len(r3.EdgeCaseTests) != 1 {
		t.Errorf("EdgeCaseTests length = %d, want 1", len(r3.EdgeCaseTests))
	}

	r4, e4 := AddSmokeTest(ts, makeSmokeTest("TS-05-SMOKE-1"))
	if e4 != nil {
		t.Fatalf("AddSmokeTest on nil slice returned error: %v", e4)
	}
	if r4.SmokeTests == nil {
		t.Fatal("AddSmokeTest returned nil SmokeTests, want non-nil")
	}
	if len(r4.SmokeTests) != 1 {
		t.Errorf("SmokeTests length = %d, want 1", len(r4.SmokeTests))
	}
}
