package afspec

import "testing"

func TestCriterionBuildersSetTheRightFields(t *testing.T) {
	t.Run("ubiquitous carries neither condition nor guard", func(t *testing.T) {
		c := UbiquitousCriterion("01-REQ-1.1", "loader", "return a Spec")
		if c.Condition != nil || c.Guard != nil {
			t.Errorf("condition=%v guard=%v; both must be absent", c.Condition, c.Guard)
		}
	})
	t.Run("complex_event carries both", func(t *testing.T) {
		c := ComplexEventCriterion("01-REQ-1.1", "a file is read", "the file is empty", "loader", "return nil")
		if c.ConditionText() != "a file is read" || c.GuardText() != "the file is empty" {
			t.Errorf("condition=%q guard=%q", c.ConditionText(), c.GuardText())
		}
	})
	t.Run("unwanted requires a contract", func(t *testing.T) {
		c := UnwantedCriterion("01-REQ-1.1", "the file is missing", "loader", "return an error", "a *LoadError")
		if c.ContractText() != "a *LoadError" {
			t.Errorf("contract = %q", c.ContractText())
		}
	})
	t.Run("an empty system is omitted rather than written empty", func(t *testing.T) {
		c := UbiquitousCriterion("01-REQ-1.1", "", "do a thing")
		if c.System != nil {
			t.Errorf("System = %v; want nil so the schema's minLength is not violated", c.System)
		}
		if c.SystemName() != "system" {
			t.Errorf("SystemName() = %q; want the default %q", c.SystemName(), "system")
		}
	})
}

func TestRenderEARSSentence(t *testing.T) {
	cases := []struct {
		name string
		c    Criterion
		want string
	}{
		{
			"ubiquitous",
			UbiquitousCriterion("01-REQ-1.1", "loader", "return a Spec"),
			"THE loader SHALL return a Spec",
		},
		{
			"event_driven",
			EventDrivenCriterion("01-REQ-1.1", "a spec is loaded", "loader", "return a Spec"),
			"WHEN a spec is loaded, THE loader SHALL return a Spec",
		},
		{
			"complex_event",
			ComplexEventCriterion("01-REQ-1.1", "a spec is loaded", "it is sealed", "loader", "reject the write"),
			"WHEN a spec is loaded AND it is sealed, THE loader SHALL reject the write",
		},
		{
			"state_driven",
			StateDrivenCriterion("01-REQ-1.1", "the spec is a draft", "loader", "allow edits"),
			"WHILE the spec is a draft, THE loader SHALL allow edits",
		},
		{
			"unwanted",
			UnwantedCriterion("01-REQ-1.1", "the file is missing", "loader", "return an error", "a *LoadError"),
			"IF the file is missing, THEN THE loader SHALL return an error",
		},
		{
			"optional",
			OptionalCriterion("01-REQ-1.1", "architecture.md is present", "loader", "read it"),
			"WHERE architecture.md is present, THE loader SHALL read it",
		},
		{
			"missing system falls back to the default",
			Criterion{Id: "01-REQ-1.1", Pattern: CriterionPatternUbiquitous, Action: "do a thing"},
			"THE system SHALL do a thing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.RenderEARSSentence(); got != tc.want {
				t.Errorf("RenderEARSSentence() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}

// TestRenderEARSAlwaysShowsTheContract guards §11.2: a contract the coder
// cannot see does not exist. Under v1 return_contract was never rendered.
func TestRenderEARSAlwaysShowsTheContract(t *testing.T) {
	c := UnwantedCriterion("01-REQ-1.1", "the file is missing", "loader", "return an error", "a *LoadError naming the file")
	got := c.RenderEARS()
	want := "IF the file is missing, THEN THE loader SHALL return an error\n   → a *LoadError naming the file"
	if got != want {
		t.Errorf("RenderEARS() =\n%q\nwant\n%q", got, want)
	}

	plain := UbiquitousCriterion("01-REQ-1.2", "loader", "do a thing")
	if plain.RenderEARS() != plain.RenderEARSSentence() {
		t.Error("a criterion with no contract gained a contract line")
	}
}

func TestWithContract(t *testing.T) {
	base := UbiquitousCriterion("01-REQ-1.1", "loader", "do a thing")
	withContract := base.WithContract("a nil error")
	if base.Contract != nil {
		t.Error("WithContract mutated the receiver")
	}
	if withContract.ContractText() != "a nil error" {
		t.Errorf("contract = %q", withContract.ContractText())
	}
}
