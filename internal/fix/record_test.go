package fix

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestRecordMeasurementNotesTheFactsAndTheRefusal guards the ledger of a measuring: the
// verdict, the gate after, the outcome of the revert check, whether it closes, the paths, and
// the refusal when there is one.
func TestRecordMeasurementNotesTheFactsAndTheRefusal(t *testing.T) {
	var kinds []string
	var data []map[string]any
	record := func(kind string, d any) {
		kinds = append(kinds, kind)
		raw, _ := json.Marshal(d)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		data = append(data, m)
	}

	RecordMeasurement(record, Measurement{Verdict: "pass", Closes: true, After: &GateRef{GateID: "g2"}, RevertCheck: &RevertRef{Outcome: "proven"}, Changed: []string{"a"}, Reverted: []string{"b"}})
	RecordMeasurement(record, Measurement{Verdict: "regressed", Refusal: &Refusal{Kind: RefusalGate}})
	RecordMeasurement(nil, Measurement{})

	if !slices.Equal(kinds, []string{"measure", "measure", "refusal"}) {
		t.Fatalf("kinds = %v", kinds)
	}
	first := data[0]
	if first["verdict"] != "pass" || first["after_gate_id"] != "g2" || first["revert_outcome"] != "proven" || first["closes"] != true || len(first["changed"].([]any)) != 1 || len(first["reverted"].([]any)) != 1 {
		t.Errorf("measure = %v", first)
	}
	if _, has := data[1]["after_gate_id"]; has || data[2]["kind"] != "gate" {
		t.Errorf("a refusal: measure %v, refusal %v", data[1], data[2])
	}
}
