package worker

import (
	"encoding/json"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
)

// stamp is a time as the events carry it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// keep keeps an event of the attempt for the report to hub, with the next number of the
// attempt: the events of the loop and the steps of the worker are one sequence, in the order they
// happened, so that hub takes each once for its id, which is the run and the number.
func (a *attempt) keep(typ, at string, data json.RawMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, hub.Event{Seq: int64(len(a.events) + 1), TS: at, Type: typ, Data: data})
}

// eventSink is the function the loop calls with every line of its events as it is written: the
// line is passed on to the recorder as the event it is, with its type and its data, and kept
// for the report to hub. The slice it is given is its own only for the call.
func (a *attempt) eventSink(now func() time.Time) func(line []byte) {
	return func(line []byte) {
		var event struct {
			TS   string          `json:"ts"`
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(line, &event) != nil || event.Type == "" {
			return
		}
		if event.TS == "" {
			event.TS = stamp(now())
		}
		a.keep(event.Type, event.TS, append(json.RawMessage(nil), event.Data...))
		a.report.Emit(event.Type, event.Data)
	}
}

// eventsOf are the events of the attempt so far.
func (a *attempt) eventsOf() []hub.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]hub.Event(nil), a.events...)
}
