// Package triage is the logic of af triage: the diagnosis a brain submits and
// how it is validated, the issue body that carries it and the original report
// it keeps, the brief a brain is given, the checks made before any spend, and
// the landing on the forge. It knows nothing of the command shell; internal/afcmd wires
// it (docs/cli.md describes the commands).
package triage

// Diagnosis is the one object a triage brain submits. The JSON names are the
// contract: the schema in diagnosis.schema.json, the validator, the body and
// `af fix` all read them.
type Diagnosis struct {
	Title              string         `json:"title"`
	Severity           string         `json:"severity"`
	Confidence         string         `json:"confidence"`
	RootCause          string         `json:"root_cause"`
	AffectedFiles      []AffectedFile `json:"affected_files"`
	SuggestedFix       string         `json:"suggested_fix"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	Question           *Question      `json:"question,omitempty"`
}

// AffectedFile is a file the diagnosis cites, with the line when it names one.
type AffectedFile struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
	Note string `json:"note"`
}

// Question is what a diagnosis asks a person when two readings of the report
// lead to opposite fixes. Options is empty for a free-text answer.
type Question struct {
	Text    string   `json:"text"`
	Options []string `json:"options"`
}
