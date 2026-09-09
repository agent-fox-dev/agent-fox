package afspec

import "testing"

// ---------------------------------------------------------------------------
// Helpers for Next*ID tests
// ---------------------------------------------------------------------------

// makeCriterionWithID creates a minimal Criterion with just the required
// fields set, suitable for use in acceptance_criteria / edge_cases slices.
func makeCriterionWithID(id string) Criterion {
	return Criterion{
		Id:             id,
		EarsPattern:    CriterionEarsPatternUbiquitous,
		Action:         "test action",
		System:         "test system",
		ReturnContract: nil,
	}
}

// ---------------------------------------------------------------------------
// TS-05-19: NextRequirementID scans existing requirement IDs, finds the
// maximum numeric suffix, and returns {spec_id}-REQ-{max+1}.
// Requirement: 05-REQ-5.1
// ---------------------------------------------------------------------------

func TestNextRequirementID_NonContiguousIDs(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(
		makeRequirement("05-REQ-1"),
		makeRequirement("05-REQ-3"),
		makeRequirement("05-REQ-5"),
	)

	got := NextRequirementID(req)
	want := "05-REQ-6"
	if got != want {
		t.Errorf("NextRequirementID() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-20: NextCriterionID scans acceptance_criteria IDs, finds the maximum
// numeric suffix, and returns {requirement_id}.{max+1}.
// Requirement: 05-REQ-5.2
// ---------------------------------------------------------------------------

func TestNextCriterionID_NonContiguousIDs(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:    "05-REQ-2",
		Title: "Test Req",
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{
			makeCriterionWithID("05-REQ-2.1"),
			makeCriterionWithID("05-REQ-2.3"),
		},
		EdgeCases: []Criterion{},
	}

	got := NextCriterionID(r)
	want := "05-REQ-2.4"
	if got != want {
		t.Errorf("NextCriterionID() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-21: NextEdgeCaseID scans edge_cases IDs, extracts numeric suffix
// after 'E', finds maximum, and returns {requirement_id}.E{max+1}.
// Requirement: 05-REQ-5.3
// ---------------------------------------------------------------------------

func TestNextEdgeCaseID_NonContiguousIDs(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:    "05-REQ-2",
		Title: "Test Req",
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{},
		EdgeCases: []Criterion{
			makeCriterionWithID("05-REQ-2.E1"),
			makeCriterionWithID("05-REQ-2.E4"),
		},
	}

	got := NextEdgeCaseID(r)
	want := "05-REQ-2.E5"
	if got != want {
		t.Errorf("NextEdgeCaseID() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-22: NextCorrectnessPropertyID, NextExecutionPathID, and
// NextErrorHandlingID each return the correct next ID.
// Requirement: 05-REQ-5.4
// ---------------------------------------------------------------------------

func TestNextCorrectnessPropertyID(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.CorrectnessProperties = []CorrectnessProperty{
		{Id: "05-PROP-2", Title: "P", ForAny: "a", Invariant: "i", Validates: []string{"r"}},
	}

	got := NextCorrectnessPropertyID(req)
	want := "05-PROP-3"
	if got != want {
		t.Errorf("NextCorrectnessPropertyID() = %q, want %q", got, want)
	}
}

func TestNextExecutionPathID(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.ExecutionPaths = []ExecutionPath{
		{Id: "05-PATH-1", Title: "P1", Steps: []PathStep{{Actor: "a", Action: "b"}, {Actor: "c", Action: "d"}}},
		{Id: "05-PATH-3", Title: "P3", Steps: []PathStep{{Actor: "a", Action: "b"}, {Actor: "c", Action: "d"}}},
	}

	got := NextExecutionPathID(req)
	want := "05-PATH-4"
	if got != want {
		t.Errorf("NextExecutionPathID() = %q, want %q", got, want)
	}
}

func TestNextErrorHandlingID(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.ErrorHandling = []ErrorHandlingEntry{
		{Id: "05-ERR-1", Condition: "c", Behavior: "b", RequirementId: "r"},
	}

	got := NextErrorHandlingID(req)
	want := "05-ERR-2"
	if got != want {
		t.Errorf("NextErrorHandlingID() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-23: NextTestCaseID, NextPropertyTestID, NextEdgeCaseTestID, and
// NextSmokeTestID each return the correct next ID.
// Requirement: 05-REQ-5.5
// ---------------------------------------------------------------------------

func TestNextTestCaseID(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.TestCases = []TestCase{
		makeTestCase("TS-05-2"),
	}

	got := NextTestCaseID(ts)
	want := "TS-05-3"
	if got != want {
		t.Errorf("NextTestCaseID() = %q, want %q", got, want)
	}
}

func TestNextPropertyTestID(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.PropertyTests = []PropertyTest{
		makePropertyTest("TS-05-P1"),
	}

	got := NextPropertyTestID(ts)
	want := "TS-05-P2"
	if got != want {
		t.Errorf("NextPropertyTestID() = %q, want %q", got, want)
	}
}

func TestNextEdgeCaseTestID(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.EdgeCaseTests = []EdgeCaseTest{
		makeEdgeCaseTest("TS-05-E3"),
	}

	got := NextEdgeCaseTestID(ts)
	want := "TS-05-E4"
	if got != want {
		t.Errorf("NextEdgeCaseTestID() = %q, want %q", got, want)
	}
}

func TestNextSmokeTestID(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.SmokeTests = []SmokeTest{
		makeSmokeTest("TS-05-SMOKE-1"),
		makeSmokeTest("TS-05-SMOKE-2"),
	}

	got := NextSmokeTestID(ts)
	want := "TS-05-SMOKE-3"
	if got != want {
		t.Errorf("NextSmokeTestID() = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-49: Any Next*ID function called on an empty collection returns the
// ID with numeric suffix 1.
// Requirement: 05-REQ-5.E1
// ---------------------------------------------------------------------------

func TestNextIDs_EmptyCollections(t *testing.T) {
	defer requireImplemented(t)

	emptyReq := makeRequirementsV1()
	emptyTs := makeTestSpecV1()
	emptyR := Requirement{
		Id:    "05-REQ-1",
		Title: "Test",
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{},
		EdgeCases:          []Criterion{},
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"NextRequirementID", NextRequirementID(emptyReq), "05-REQ-1"},
		{"NextCriterionID", NextCriterionID(emptyR), "05-REQ-1.1"},
		{"NextEdgeCaseID", NextEdgeCaseID(emptyR), "05-REQ-1.E1"},
		{"NextCorrectnessPropertyID", NextCorrectnessPropertyID(emptyReq), "05-PROP-1"},
		{"NextExecutionPathID", NextExecutionPathID(emptyReq), "05-PATH-1"},
		{"NextErrorHandlingID", NextErrorHandlingID(emptyReq), "05-ERR-1"},
		{"NextTestCaseID", NextTestCaseID(emptyTs), "TS-05-1"},
		{"NextPropertyTestID", NextPropertyTestID(emptyTs), "TS-05-P1"},
		{"NextEdgeCaseTestID", NextEdgeCaseTestID(emptyTs), "TS-05-E1"},
		{"NextSmokeTestID", NextSmokeTestID(emptyTs), "TS-05-SMOKE-1"},
	}

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s on empty collection = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// TS-05-50: Any Next*ID function called on a non-contiguous collection
// returns max+1 without filling gaps.
// Requirement: 05-REQ-5.E2
// ---------------------------------------------------------------------------

func TestNextRequirementID_NonContiguousGaps(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(
		makeRequirement("05-REQ-1"),
		makeRequirement("05-REQ-3"),
		makeRequirement("05-REQ-7"),
	)

	got := NextRequirementID(req)
	want := "05-REQ-8"
	if got != want {
		t.Errorf("NextRequirementID() with gaps = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// TS-05-51: Next*ID functions skip IDs that do not match the expected format
// pattern and do not panic or return an incorrect result.
// Requirement: 05-REQ-5.E3
// ---------------------------------------------------------------------------

func TestNextRequirementID_MalformedIDsSkipped(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(
		makeRequirement("05-REQ-2"),
		makeRequirement("MALFORMED-ID"),
		makeRequirement("05-REQ-5"),
	)

	got := NextRequirementID(req)
	want := "05-REQ-6"
	if got != want {
		t.Errorf("NextRequirementID() with malformed IDs = %q, want %q", got, want)
	}
}

func TestNextCriterionID_MalformedIDsSkipped(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:    "05-REQ-1",
		Title: "Test",
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{
			makeCriterionWithID("05-REQ-1.2"),
			makeCriterionWithID("MALFORMED"),
			makeCriterionWithID("05-REQ-1.5"),
		},
		EdgeCases: []Criterion{},
	}

	got := NextCriterionID(r)
	want := "05-REQ-1.6"
	if got != want {
		t.Errorf("NextCriterionID() with malformed IDs = %q, want %q", got, want)
	}
}

func TestNextEdgeCaseID_MalformedIDsSkipped(t *testing.T) {
	defer requireImplemented(t)

	r := Requirement{
		Id:    "05-REQ-1",
		Title: "Test",
		UserStory: UserStory{
			Role:    "developer",
			Goal:    "test",
			Benefit: "coverage",
		},
		AcceptanceCriteria: []Criterion{},
		EdgeCases: []Criterion{
			makeCriterionWithID("05-REQ-1.E3"),
			makeCriterionWithID("BAD-EDGE"),
		},
	}

	got := NextEdgeCaseID(r)
	want := "05-REQ-1.E4"
	if got != want {
		t.Errorf("NextEdgeCaseID() with malformed IDs = %q, want %q", got, want)
	}
}

func TestNextCorrectnessPropertyID_MalformedIDsSkipped(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1()
	req.CorrectnessProperties = []CorrectnessProperty{
		{Id: "05-PROP-3", Title: "P", ForAny: "a", Invariant: "i", Validates: []string{"r"}},
		{Id: "NOT-A-PROP", Title: "P", ForAny: "a", Invariant: "i", Validates: []string{"r"}},
	}

	got := NextCorrectnessPropertyID(req)
	want := "05-PROP-4"
	if got != want {
		t.Errorf("NextCorrectnessPropertyID() with malformed IDs = %q, want %q", got, want)
	}
}

func TestNextTestCaseID_MalformedIDsSkipped(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.TestCases = []TestCase{
		makeTestCase("TS-05-4"),
		makeTestCase("INVALID-TC"),
	}

	got := NextTestCaseID(ts)
	want := "TS-05-5"
	if got != want {
		t.Errorf("NextTestCaseID() with malformed IDs = %q, want %q", got, want)
	}
}

func TestNextIDs_AllMalformedReturnsSuffix1(t *testing.T) {
	defer requireImplemented(t)

	// When all IDs are malformed, should return suffix 1 as if empty.
	req := makeRequirementsV1(
		makeRequirement("MALFORMED-A"),
		makeRequirement("MALFORMED-B"),
	)

	got := NextRequirementID(req)
	want := "05-REQ-1"
	if got != want {
		t.Errorf("NextRequirementID() with all malformed IDs = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Additional coverage: multi-digit suffixes
// ---------------------------------------------------------------------------

func TestNextRequirementID_MultiDigitSuffix(t *testing.T) {
	defer requireImplemented(t)

	req := makeRequirementsV1(
		makeRequirement("05-REQ-10"),
		makeRequirement("05-REQ-9"),
		makeRequirement("05-REQ-100"),
	)

	got := NextRequirementID(req)
	want := "05-REQ-101"
	if got != want {
		t.Errorf("NextRequirementID() with multi-digit suffixes = %q, want %q", got, want)
	}
}

func TestNextSmokeTestID_MultiDigitSuffix(t *testing.T) {
	defer requireImplemented(t)

	ts := makeTestSpecV1()
	ts.SmokeTests = []SmokeTest{
		makeSmokeTest("TS-05-SMOKE-10"),
		makeSmokeTest("TS-05-SMOKE-3"),
	}

	got := NextSmokeTestID(ts)
	want := "TS-05-SMOKE-11"
	if got != want {
		t.Errorf("NextSmokeTestID() with multi-digit suffixes = %q, want %q", got, want)
	}
}
