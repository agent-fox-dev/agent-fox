package afspec

import "fmt"

// UbiquitousCriterion constructs a Criterion with the ubiquitous EARS pattern.
// Sets Id, EarsPattern, System, and Action. No pattern-specific optional fields.
func UbiquitousCriterion(id, system, action string) Criterion {
	return Criterion{
		Id:          id,
		EarsPattern: CriterionEarsPatternUbiquitous,
		System:      system,
		Action:      action,
	}
}

// EventDrivenCriterion constructs a Criterion with the event_driven EARS pattern.
// Sets Id, EarsPattern, Trigger, System, and Action.
func EventDrivenCriterion(id, trigger, system, action string) Criterion {
	return Criterion{
		Id:          id,
		EarsPattern: CriterionEarsPatternEventDriven,
		Trigger:     &trigger,
		System:      system,
		Action:      action,
	}
}

// ComplexEventCriterion constructs a Criterion with the complex_event EARS pattern.
// Sets Id, EarsPattern, Trigger, Condition, System, and Action.
func ComplexEventCriterion(id, trigger, condition, system, action string) Criterion {
	return Criterion{
		Id:          id,
		EarsPattern: CriterionEarsPatternComplexEvent,
		Trigger:     &trigger,
		Condition:   &condition,
		System:      system,
		Action:      action,
	}
}

// StateDrivenCriterion constructs a Criterion with the state_driven EARS pattern.
// Sets Id, EarsPattern, State, System, and Action.
func StateDrivenCriterion(id, state, system, action string) Criterion {
	return Criterion{
		Id:          id,
		EarsPattern: CriterionEarsPatternStateDriven,
		State:       &state,
		System:      system,
		Action:      action,
	}
}

// UnwantedCriterion constructs a Criterion with the unwanted EARS pattern.
// Sets Id, EarsPattern, ErrorCondition, System, and Action.
// Does not set Trigger, State, or Feature (they remain nil).
func UnwantedCriterion(id, errorCondition, system, action string) Criterion {
	return Criterion{
		Id:             id,
		EarsPattern:    CriterionEarsPatternUnwanted,
		ErrorCondition: &errorCondition,
		System:         system,
		Action:         action,
	}
}

// OptionalCriterion constructs a Criterion with the optional EARS pattern.
// Sets Id, EarsPattern, Feature, System, and Action.
// Does not set Trigger, State, or ErrorCondition (they remain nil).
func OptionalCriterion(id, feature, system, action string) Criterion {
	return Criterion{
		Id:          id,
		EarsPattern: CriterionEarsPatternOptional,
		Feature:     &feature,
		System:      system,
		Action:      action,
	}
}

// RenderEARSSentence renders a single EARS criterion as a natural-language
// sentence following the EARS pattern templates:
//
//   - ubiquitous:    "THE <System> SHALL <Action>"
//   - event_driven:  "WHEN <Trigger>, THE <System> SHALL <Action>"
//   - complex_event: "WHEN <Trigger> AND <Condition>, THE <System> SHALL <Action>"
//   - state_driven:  "WHILE <State>, THE <System> SHALL <Action>"
//   - unwanted:      "IF <ErrorCondition>, THEN THE <System> SHALL <Action>"
//   - optional:      "WHERE <Feature>, THE <System> SHALL <Action>"
func (c Criterion) RenderEARSSentence() string {
	core := fmt.Sprintf("THE %s SHALL %s", c.System, c.Action)

	switch c.EarsPattern {
	case CriterionEarsPatternUbiquitous:
		return core
	case CriterionEarsPatternEventDriven:
		trigger := ""
		if c.Trigger != nil {
			trigger = *c.Trigger
		}
		return fmt.Sprintf("WHEN %s, %s", trigger, core)
	case CriterionEarsPatternComplexEvent:
		trigger := ""
		if c.Trigger != nil {
			trigger = *c.Trigger
		}
		condition := ""
		if c.Condition != nil {
			condition = *c.Condition
		}
		return fmt.Sprintf("WHEN %s AND %s, %s", trigger, condition, core)
	case CriterionEarsPatternStateDriven:
		state := ""
		if c.State != nil {
			state = *c.State
		}
		return fmt.Sprintf("WHILE %s, %s", state, core)
	case CriterionEarsPatternUnwanted:
		errCond := ""
		if c.ErrorCondition != nil {
			errCond = *c.ErrorCondition
		}
		return fmt.Sprintf("IF %s, THEN %s", errCond, core)
	case CriterionEarsPatternOptional:
		feature := ""
		if c.Feature != nil {
			feature = *c.Feature
		}
		return fmt.Sprintf("WHERE %s, %s", feature, core)
	default:
		return core
	}
}
