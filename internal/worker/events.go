package worker

import (
	"encoding/json"
	"slices"
)

// eventSink is the function the loop calls with every line of its events as it is written: the
// line is passed on to the recorder as the event it is, with its type and its data, and kept
// for the report to hub. The slice it is given is its own only for the call.
func (a *attempt) eventSink() func(line []byte) {
	return func(line []byte) {
		a.mu.Lock()
		a.events = append(a.events, slices.Clone(line))
		a.mu.Unlock()
		var event struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &event) == nil && event.Type != "" {
			a.report.Emit(event.Type, event.Data)
		}
	}
}
