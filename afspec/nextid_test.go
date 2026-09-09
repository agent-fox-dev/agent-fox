package afspec

import "testing"

func TestNextIDsUseTheHighestSuffixInUse(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	if got := NextRequirementID(*spec.Requirements); got != "01-REQ-3" {
		t.Errorf("NextRequirementID = %q; want 01-REQ-3", got)
	}
	if got := NextCriterionID(spec.Requirements.Requirements[0]); got != "01-REQ-1.4" {
		t.Errorf("NextCriterionID = %q; want 01-REQ-1.4", got)
	}
	if got := NextExecutionPathID(*spec.Requirements); got != "01-PATH-2" {
		t.Errorf("NextExecutionPathID = %q; want 01-PATH-2", got)
	}
	if got := NextTestID(*spec.TestSpec); got != "TS-01-6" {
		t.Errorf("NextTestID = %q; want TS-01-6", got)
	}
	if got := NextTaskID(*spec.Tasks); got != 4 {
		t.Errorf("NextTaskID = %d; want 4", got)
	}
}

func TestNextIDsStartAtOneWhenEmpty(t *testing.T) {
	spec := CreateSpec("05", "empty")

	if got := NextRequirementID(*spec.Requirements); got != "05-REQ-1" {
		t.Errorf("NextRequirementID = %q; want 05-REQ-1", got)
	}
	if got := NextExecutionPathID(*spec.Requirements); got != "05-PATH-1" {
		t.Errorf("NextExecutionPathID = %q; want 05-PATH-1", got)
	}
	if got := NextTestID(*spec.TestSpec); got != "TS-05-1" {
		t.Errorf("NextTestID = %q; want TS-05-1", got)
	}
	if got := NextTaskID(*spec.Tasks); got != 1 {
		t.Errorf("NextTaskID = %d; want 1", got)
	}
	if got := NextCriterionID(Requirement{Id: "05-REQ-1"}); got != "05-REQ-1.1" {
		t.Errorf("NextCriterionID = %q; want 05-REQ-1.1", got)
	}
}

// TestNextTestIDUsesOneSeriesForEveryKind guards the v2 change that collapsed
// the four v1 test-ID families (TS-N, TS-EN, TS-PN, TS-SMOKE-N) into one.
func TestNextTestIDUsesOneSeriesForEveryKind(t *testing.T) {
	ts := TestSpecV2Json{
		SpecId: "05",
		Tests: []Test{
			{Id: "TS-05-1", Kind: TestKindUnit},
			{Id: "TS-05-2", Kind: TestKindProperty},
			{Id: "TS-05-7", Kind: TestKindSmoke},
			{Id: "TS-05-4", Kind: TestKindIntegration},
		},
	}
	if got := NextTestID(ts); got != "TS-05-8" {
		t.Errorf("NextTestID = %q; want TS-05-8 — every kind shares one series", got)
	}
}

func TestNextIDsIgnoreMalformedIDs(t *testing.T) {
	req := RequirementsV2Json{
		SpecId: "05",
		Requirements: []Requirement{
			{Id: "05-REQ-2"},
			{Id: "garbage"},
			{Id: "05-REQ-notanumber"},
		},
	}
	if got := NextRequirementID(req); got != "05-REQ-3" {
		t.Errorf("NextRequirementID = %q; want 05-REQ-3", got)
	}
}

func TestNextIDsHandleAlphaPrefixes(t *testing.T) {
	spec := loadFixture(t, "../testdata/alpha_prefix_spec")
	if got := NextRequirementID(*spec.Requirements); got != "abc-REQ-3" {
		t.Errorf("NextRequirementID = %q; want abc-REQ-3", got)
	}
	if got := NextTestID(*spec.TestSpec); got != "TS-abc-6" {
		t.Errorf("NextTestID = %q; want TS-abc-6", got)
	}
}
