package afspec

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 4.3: EARS Criterion Builders and RenderEARSSentence
// ---------------------------------------------------------------------------

// TestRenderEARSSentence_UnwantedIncludesTHEN verifies that the unwanted EARS
// pattern renders with "THEN" between the comma and "THE", matching the spec.
// Requirement: NS-REQ-1, NS-REQ-2, Test Spec: TS-NS-1, TS-NS-2
func TestRenderEARSSentence_UnwantedIncludesTHEN(t *testing.T) {
	defer requireImplemented(t)

	c := UnwantedCriterion("id", "disk full", "StorageService", "returns error")
	sentence := c.RenderEARSSentence()

	const want = "IF disk full, THEN THE StorageService SHALL returns error"
	if sentence != want {
		t.Errorf("RenderEARSSentence() = %q, want %q", sentence, want)
	}
}

// TestRenderEARSSentence_UnwantedNilErrorConditionIncludesTHEN verifies that
// a nil ErrorCondition still produces THEN in the rendered sentence.
// Requirement: NS-REQ-3, Test Spec: TS-NS-3
func TestRenderEARSSentence_UnwantedNilErrorConditionIncludesTHEN(t *testing.T) {
	defer requireImplemented(t)

	c := Criterion{
		Id:             "id",
		EarsPattern:    CriterionEarsPatternUnwanted,
		ErrorCondition: nil,
		System:         "StorageService",
		Action:         "returns error",
	}
	sentence := c.RenderEARSSentence()

	if !strings.Contains(sentence, ", THEN THE") {
		t.Errorf("expected sentence to contain \", THEN THE\", got %q", sentence)
	}
}

// TestRenderEARSSentence_ComplexEventUsesAND verifies that the complex_event
// EARS pattern renders with "AND" between trigger and condition, not "IF".
// Requirement: NS-REQ-1, Test Spec: TS-NS-1
func TestRenderEARSSentence_ComplexEventUsesAND(t *testing.T) {
	defer requireImplemented(t)

	c := ComplexEventCriterion("id", "event fires", "condition met", "the system", "handles")
	sentence := c.RenderEARSSentence()

	if !strings.Contains(sentence, "WHEN event fires AND condition met") {
		t.Errorf("expected sentence to contain 'WHEN event fires AND condition met', got %q", sentence)
	}
	if strings.Contains(sentence, "IF condition met") {
		t.Errorf("expected sentence NOT to contain 'IF condition met', got %q", sentence)
	}
}

// TestEARSBuilders_EventDriven verifies that EventDrivenCriterion returns a
// Criterion with the correct pattern and all provided arguments mapped.
// Test Spec: TS-01-40, Requirement: 01-REQ-20.1
func TestEARSBuilders_EventDriven(t *testing.T) {
	defer requireImplemented(t)

	c := EventDrivenCriterion("01-REQ-1.1", "user submits form", "AuthService", "validates credentials")

	if c.Id != "01-REQ-1.1" {
		t.Errorf("Id = %q, want %q", c.Id, "01-REQ-1.1")
	}
	if c.EarsPattern != CriterionEarsPatternEventDriven {
		t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternEventDriven)
	}
	if c.Trigger == nil || *c.Trigger != "user submits form" {
		t.Errorf("Trigger = %v, want %q", c.Trigger, "user submits form")
	}
	if c.System != "AuthService" {
		t.Errorf("System = %q, want %q", c.System, "AuthService")
	}
	if c.Action != "validates credentials" {
		t.Errorf("Action = %q, want %q", c.Action, "validates credentials")
	}
}

// TestEARSBuilders_AllPatterns verifies that each EARS criterion builder
// returns a Criterion with the correct ears_pattern and relevant fields.
// Test Spec: TS-01-40, Requirement: 01-REQ-20.1
func TestEARSBuilders_AllPatterns(t *testing.T) {
	defer requireImplemented(t)

	t.Run("Ubiquitous", func(t *testing.T) {
		defer requireImplemented(t)
		c := UbiquitousCriterion("id-1", "SystemA", "does something")
		if c.EarsPattern != CriterionEarsPatternUbiquitous {
			t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternUbiquitous)
		}
		if c.Id != "id-1" {
			t.Errorf("Id = %q, want %q", c.Id, "id-1")
		}
		if c.System != "SystemA" {
			t.Errorf("System = %q, want %q", c.System, "SystemA")
		}
		if c.Action != "does something" {
			t.Errorf("Action = %q, want %q", c.Action, "does something")
		}
	})

	t.Run("ComplexEvent", func(t *testing.T) {
		defer requireImplemented(t)
		c := ComplexEventCriterion("id-2", "event fires", "condition met", "SystemB", "handles event")
		if c.EarsPattern != CriterionEarsPatternComplexEvent {
			t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternComplexEvent)
		}
		if c.Trigger == nil || *c.Trigger != "event fires" {
			t.Errorf("Trigger = %v, want %q", c.Trigger, "event fires")
		}
		if c.Condition == nil || *c.Condition != "condition met" {
			t.Errorf("Condition = %v, want %q", c.Condition, "condition met")
		}
		if c.System != "SystemB" {
			t.Errorf("System = %q, want %q", c.System, "SystemB")
		}
		if c.Action != "handles event" {
			t.Errorf("Action = %q, want %q", c.Action, "handles event")
		}
	})

	t.Run("StateDriven", func(t *testing.T) {
		defer requireImplemented(t)
		c := StateDrivenCriterion("id-3", "system is idle", "SystemC", "monitors")
		if c.EarsPattern != CriterionEarsPatternStateDriven {
			t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternStateDriven)
		}
		if c.State == nil || *c.State != "system is idle" {
			t.Errorf("State = %v, want %q", c.State, "system is idle")
		}
		if c.System != "SystemC" {
			t.Errorf("System = %q, want %q", c.System, "SystemC")
		}
		if c.Action != "monitors" {
			t.Errorf("Action = %q, want %q", c.Action, "monitors")
		}
	})

	t.Run("Unwanted", func(t *testing.T) {
		defer requireImplemented(t)
		c := UnwantedCriterion("id-4", "disk full", "StorageService", "returns error")
		if c.EarsPattern != CriterionEarsPatternUnwanted {
			t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternUnwanted)
		}
		if c.ErrorCondition == nil || *c.ErrorCondition != "disk full" {
			t.Errorf("ErrorCondition = %v, want %q", c.ErrorCondition, "disk full")
		}
		if c.System != "StorageService" {
			t.Errorf("System = %q, want %q", c.System, "StorageService")
		}
		if c.Action != "returns error" {
			t.Errorf("Action = %q, want %q", c.Action, "returns error")
		}
	})

	t.Run("Optional", func(t *testing.T) {
		defer requireImplemented(t)
		c := OptionalCriterion("id-5", "dark mode", "UIService", "renders dark theme")
		if c.EarsPattern != CriterionEarsPatternOptional {
			t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternOptional)
		}
		if c.Feature == nil || *c.Feature != "dark mode" {
			t.Errorf("Feature = %v, want %q", c.Feature, "dark mode")
		}
		if c.System != "UIService" {
			t.Errorf("System = %q, want %q", c.System, "UIService")
		}
		if c.Action != "renders dark theme" {
			t.Errorf("Action = %q, want %q", c.Action, "renders dark theme")
		}
	})
}

// TestEARSBuilders_FieldIsolation verifies that EARS criterion builders set
// only the fields relevant to each pattern, leaving irrelevant fields as
// zero values (nil for *string).
// Test Spec: TS-01-41, Requirement: 01-REQ-20.2
func TestEARSBuilders_FieldIsolation(t *testing.T) {
	defer requireImplemented(t)

	t.Run("UnwantedCriterion", func(t *testing.T) {
		defer requireImplemented(t)
		u := UnwantedCriterion("id", "disk full", "StorageService", "returns error")

		// ErrorCondition should be set
		if u.ErrorCondition == nil || *u.ErrorCondition != "disk full" {
			t.Errorf("ErrorCondition = %v, want %q", u.ErrorCondition, "disk full")
		}

		// Trigger, State, Feature should be nil (zero values)
		if u.Trigger != nil {
			t.Errorf("Trigger = %v, want nil", u.Trigger)
		}
		if u.State != nil {
			t.Errorf("State = %v, want nil", u.State)
		}
		if u.Feature != nil {
			t.Errorf("Feature = %v, want nil", u.Feature)
		}
	})

	t.Run("OptionalCriterion", func(t *testing.T) {
		defer requireImplemented(t)
		o := OptionalCriterion("id", "dark mode", "UIService", "renders dark theme")

		// Feature should be set
		if o.Feature == nil || *o.Feature != "dark mode" {
			t.Errorf("Feature = %v, want %q", o.Feature, "dark mode")
		}

		// Trigger, State, ErrorCondition should be nil (zero values)
		if o.Trigger != nil {
			t.Errorf("Trigger = %v, want nil", o.Trigger)
		}
		if o.State != nil {
			t.Errorf("State = %v, want nil", o.State)
		}
		if o.ErrorCondition != nil {
			t.Errorf("ErrorCondition = %v, want nil", o.ErrorCondition)
		}
	})

	t.Run("UbiquitousCriterion", func(t *testing.T) {
		defer requireImplemented(t)
		u := UbiquitousCriterion("id", "SystemA", "does something")

		// All pattern-specific optional fields should be nil
		if u.Trigger != nil {
			t.Errorf("Trigger = %v, want nil", u.Trigger)
		}
		if u.State != nil {
			t.Errorf("State = %v, want nil", u.State)
		}
		if u.Feature != nil {
			t.Errorf("Feature = %v, want nil", u.Feature)
		}
		if u.ErrorCondition != nil {
			t.Errorf("ErrorCondition = %v, want nil", u.ErrorCondition)
		}
		if u.Condition != nil {
			t.Errorf("Condition = %v, want nil", u.Condition)
		}
	})

	t.Run("EventDrivenCriterion", func(t *testing.T) {
		defer requireImplemented(t)
		e := EventDrivenCriterion("id", "trigger", "SystemA", "action")

		// Trigger should be set
		if e.Trigger == nil || *e.Trigger != "trigger" {
			t.Errorf("Trigger = %v, want %q", e.Trigger, "trigger")
		}
		// Other optional fields should be nil
		if e.State != nil {
			t.Errorf("State = %v, want nil", e.State)
		}
		if e.Feature != nil {
			t.Errorf("Feature = %v, want nil", e.Feature)
		}
		if e.ErrorCondition != nil {
			t.Errorf("ErrorCondition = %v, want nil", e.ErrorCondition)
		}
		if e.Condition != nil {
			t.Errorf("Condition = %v, want nil", e.Condition)
		}
	})

	t.Run("StateDrivenCriterion", func(t *testing.T) {
		defer requireImplemented(t)
		s := StateDrivenCriterion("id", "idle", "SystemA", "action")

		// State should be set
		if s.State == nil || *s.State != "idle" {
			t.Errorf("State = %v, want %q", s.State, "idle")
		}
		// Other optional fields should be nil
		if s.Trigger != nil {
			t.Errorf("Trigger = %v, want nil", s.Trigger)
		}
		if s.Feature != nil {
			t.Errorf("Feature = %v, want nil", s.Feature)
		}
		if s.ErrorCondition != nil {
			t.Errorf("ErrorCondition = %v, want nil", s.ErrorCondition)
		}
	})
}

// TestEARSBuilders_EmptyID verifies that EARS builders accept an empty ID
// string; validation is deferred to spec.Validate.
// Requirement: 01-REQ-20.E1
func TestEARSBuilders_EmptyID(t *testing.T) {
	defer requireImplemented(t)

	c := UbiquitousCriterion("", "SystemA", "does something")
	if c.Id != "" {
		t.Errorf("Id = %q, want empty string", c.Id)
	}
	if c.EarsPattern != CriterionEarsPatternUbiquitous {
		t.Errorf("EarsPattern = %q, want %q", c.EarsPattern, CriterionEarsPatternUbiquitous)
	}
}

// TestRenderEARSSentence verifies that RenderEARSSentence produces the
// expected natural-language sentence for each EARS pattern.
// Requirement: 01-REQ-20.1
func TestRenderEARSSentence(t *testing.T) {
	defer requireImplemented(t)

	tests := []struct {
		name     string
		crit     Criterion
		contains string
	}{
		{
			name: "Ubiquitous",
			crit: Criterion{
				Id:          "id",
				EarsPattern: CriterionEarsPatternUbiquitous,
				System:      "the system",
				Action:      "does something",
			},
			contains: "THE the system SHALL does something",
		},
		{
			name: "EventDriven",
			crit: Criterion{
				Id:          "id",
				EarsPattern: CriterionEarsPatternEventDriven,
				Trigger:     strPtr("user clicks"),
				System:      "the system",
				Action:      "responds",
			},
			contains: "WHEN user clicks",
		},
		{
			name: "ComplexEvent",
			crit: Criterion{
				Id:          "id",
				EarsPattern: CriterionEarsPatternComplexEvent,
				Trigger:     strPtr("event fires"),
				Condition:   strPtr("condition met"),
				System:      "the system",
				Action:      "handles",
			},
			contains: "WHEN event fires AND condition met",
		},
		{
			name: "StateDriven",
			crit: Criterion{
				Id:          "id",
				EarsPattern: CriterionEarsPatternStateDriven,
				State:       strPtr("system is idle"),
				System:      "the system",
				Action:      "monitors",
			},
			contains: "WHILE system is idle",
		},
		{
			name: "Unwanted",
			crit: Criterion{
				Id:             "id",
				EarsPattern:    CriterionEarsPatternUnwanted,
				ErrorCondition: strPtr("disk full"),
				System:         "the system",
				Action:         "returns error",
			},
			contains: "IF disk full, THEN THE",
		},
		{
			name: "Optional",
			crit: Criterion{
				Id:          "id",
				EarsPattern: CriterionEarsPatternOptional,
				Feature:     strPtr("dark mode"),
				System:      "the system",
				Action:      "renders dark",
			},
			contains: "WHERE dark mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer requireImplemented(t)
			sentence := tt.crit.RenderEARSSentence()
			if sentence == "" {
				t.Fatal("RenderEARSSentence returned empty string")
			}
			if !strings.Contains(sentence, tt.contains) {
				t.Errorf("expected sentence to contain %q, got %q", tt.contains, sentence)
			}
			// All sentences should contain SHALL
			if !strings.Contains(sentence, "SHALL") {
				t.Errorf("expected sentence to contain 'SHALL', got %q", sentence)
			}
		})
	}
}
