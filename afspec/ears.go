package afspec

import "fmt"

// defaultCriterionSystem is used when a criterion omits `system`
// (format v2 §6.2.1).
const defaultCriterionSystem = "system"

func strPtr(s string) *string { return &s }

// UbiquitousCriterion constructs a criterion with the ubiquitous pattern:
// "THE {system} SHALL {action}". Correctness properties are written this way,
// with an action that starts with "for any …".
func UbiquitousCriterion(id, system, action string) Criterion {
	return Criterion{
		Id:      id,
		Pattern: CriterionPatternUbiquitous,
		System:  optionalSystem(system),
		Action:  action,
	}
}

// EventDrivenCriterion constructs a criterion with the event_driven pattern:
// "WHEN {condition}, THE {system} SHALL {action}".
func EventDrivenCriterion(id, condition, system, action string) Criterion {
	return Criterion{
		Id:        id,
		Pattern:   CriterionPatternEventDriven,
		Condition: strPtr(condition),
		System:    optionalSystem(system),
		Action:    action,
	}
}

// ComplexEventCriterion constructs a criterion with the complex_event pattern:
// "WHEN {condition} AND {guard}, THE {system} SHALL {action}".
func ComplexEventCriterion(id, condition, guard, system, action string) Criterion {
	return Criterion{
		Id:        id,
		Pattern:   CriterionPatternComplexEvent,
		Condition: strPtr(condition),
		Guard:     strPtr(guard),
		System:    optionalSystem(system),
		Action:    action,
	}
}

// StateDrivenCriterion constructs a criterion with the state_driven pattern:
// "WHILE {condition}, THE {system} SHALL {action}".
func StateDrivenCriterion(id, condition, system, action string) Criterion {
	return Criterion{
		Id:        id,
		Pattern:   CriterionPatternStateDriven,
		Condition: strPtr(condition),
		System:    optionalSystem(system),
		Action:    action,
	}
}

// UnwantedCriterion constructs a criterion with the unwanted pattern:
// "IF {condition}, THEN THE {system} SHALL {action}". The contract is
// mandatory for this pattern (§6.5.2, rule C10).
func UnwantedCriterion(id, condition, system, action, contract string) Criterion {
	return Criterion{
		Id:        id,
		Pattern:   CriterionPatternUnwanted,
		Condition: strPtr(condition),
		System:    optionalSystem(system),
		Action:    action,
		Contract:  strPtr(contract),
	}
}

// OptionalCriterion constructs a criterion with the optional pattern:
// "WHERE {condition}, THE {system} SHALL {action}".
func OptionalCriterion(id, condition, system, action string) Criterion {
	return Criterion{
		Id:        id,
		Pattern:   CriterionPatternOptional,
		Condition: strPtr(condition),
		System:    optionalSystem(system),
		Action:    action,
	}
}

// WithContract returns a copy of the criterion carrying the given contract.
// A contract is required for unwanted criteria and recommended whenever the
// result is consumed by something else.
func (c Criterion) WithContract(contract string) Criterion {
	c.Contract = strPtr(contract)
	return c
}

// optionalSystem drops an empty system so that the field is omitted rather
// than written as an empty string, which the schema rejects.
func optionalSystem(system string) *string {
	if system == "" {
		return nil
	}
	return strPtr(system)
}

// SystemName returns the acting component, defaulting to "system" when the
// criterion omits it (§6.2.1).
func (c Criterion) SystemName() string {
	if c.System == nil || *c.System == "" {
		return defaultCriterionSystem
	}
	return *c.System
}

// ConditionText returns the condition clause, or the empty string when absent.
func (c Criterion) ConditionText() string {
	if c.Condition == nil {
		return ""
	}
	return *c.Condition
}

// GuardText returns the guard clause, or the empty string when absent.
func (c Criterion) GuardText() string {
	if c.Guard == nil {
		return ""
	}
	return *c.Guard
}

// ContractText returns the contract, or the empty string when absent.
func (c Criterion) ContractText() string {
	if c.Contract == nil {
		return ""
	}
	return *c.Contract
}

// RenderEARSSentence renders the criterion as its EARS sentence (§6.2.1):
//
//   - ubiquitous:    "THE {system} SHALL {action}"
//   - event_driven:  "WHEN {condition}, THE {system} SHALL {action}"
//   - complex_event: "WHEN {condition} AND {guard}, THE {system} SHALL {action}"
//   - state_driven:  "WHILE {condition}, THE {system} SHALL {action}"
//   - unwanted:      "IF {condition}, THEN THE {system} SHALL {action}"
//   - optional:      "WHERE {condition}, THE {system} SHALL {action}"
//
// The contract is not part of the sentence; renderers append it on a second
// line as "→ {contract}" (§11.2). Use RenderEARS for both at once.
func (c Criterion) RenderEARSSentence() string {
	core := fmt.Sprintf("THE %s SHALL %s", c.SystemName(), c.Action)

	switch c.Pattern {
	case CriterionPatternUbiquitous:
		return core
	case CriterionPatternEventDriven:
		return fmt.Sprintf("WHEN %s, %s", c.ConditionText(), core)
	case CriterionPatternComplexEvent:
		return fmt.Sprintf("WHEN %s AND %s, %s", c.ConditionText(), c.GuardText(), core)
	case CriterionPatternStateDriven:
		return fmt.Sprintf("WHILE %s, %s", c.ConditionText(), core)
	case CriterionPatternUnwanted:
		return fmt.Sprintf("IF %s, THEN %s", c.ConditionText(), core)
	case CriterionPatternOptional:
		return fmt.Sprintf("WHERE %s, %s", c.ConditionText(), core)
	default:
		return core
	}
}

// RenderEARS renders the criterion as its EARS sentence followed, when a
// contract is present, by a second line "→ {contract}" (§11.2). A contract the
// coder cannot see does not exist, so every renderer goes through this.
func (c Criterion) RenderEARS() string {
	sentence := c.RenderEARSSentence()
	if contract := c.ContractText(); contract != "" {
		return sentence + "\n   → " + contract
	}
	return sentence
}
