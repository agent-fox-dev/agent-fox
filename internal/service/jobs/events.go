package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
)

// maxEventData is the most JSON that the data of an event of a runner may be.
const maxEventData = 64 << 10

// invalidEventType is the type an event is stored as when the runner gave it a type
// that is malformed or that is one of the layer's own.
const invalidEventType = "invalid_event_type"

// eventType is what the type of an event of a runner looks like.
var eventType = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// encode is the JSON of v without the escaping of <, > and & that makes the data of
// an event harder to read.
func encode(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// normalizeEvent is the type and the data that an event of a runner is stored with.
// Data that cannot be encoded is replaced by {"error":…}, and data of more than 64
// KiB of JSON by {"truncated":true,"bytes":N}; a type that is not a lowercase word
// of letters, digits and underscores, or is job_state, attempt_start or attempt_end,
// which are the layer's own, makes the event an invalid_event_type whose data is the
// type and the data it was given. An event never fails the run it is reported in.
func normalizeEvent(typ string, data any) (string, json.RawMessage) {
	raw, err := encode(data)
	switch {
	case err != nil:
		raw, _ = encode(map[string]string{"error": err.Error()})
	case len(raw) > maxEventData:
		raw, _ = encode(map[string]any{"truncated": true, "bytes": len(raw)})
	}
	if !eventType.MatchString(typ) || typ == EventJobState || typ == EventAttemptStart || typ == EventAttemptEnd {
		wrapped, _ := encode(struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}{typ, raw})
		return invalidEventType, wrapped
	}
	return typ, raw
}

// AppendEvent stores an event that a runner reported, as normalizeEvent has it, and
// publishes it. The error is that of the file; the caller decides what a runner that
// reports is told of it.
func (s *Store) AppendEvent(ctx context.Context, jobID string, attempt int, typ string, data any) error {
	typ, raw := normalizeEvent(typ, data)
	return s.run(ctx, jobID, func(x *txn) error { return x.emitRaw(ctx, attempt, typ, raw) })
}

// EventsAfter are the stored events of the job whose number is greater than after,
// in order, at most limit of them.
func (s *Store) EventsAfter(ctx context.Context, jobID string, after int64, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT seq, attempt, ts, type, data FROM af_events WHERE job_id = ? AND seq > ? ORDER BY seq LIMIT ?", jobID, after, limit)
	if err != nil {
		return nil, fmt.Errorf("events of %s: %w", jobID, err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		ev := Event{JobID: jobID}
		var data string
		if err := rows.Scan(&ev.Seq, &ev.Attempt, &ev.TS, &ev.Type, &data); err != nil {
			return nil, fmt.Errorf("events of %s: %w", jobID, err)
		}
		ev.Data = json.RawMessage(data)
		out = append(out, ev)
	}
	return out, rows.Err()
}

// replayBatch is how many stored events a replay reads at a time.
const replayBatch = 500

// Follower reads the events of a job after a number without a gap or a repeat: the
// stored ones first, then the live ones. It subscribes before it reads anything from
// the file, so an event is in one of the two or in both, and the numbers say which
// were seen already.
type Follower struct {
	sub   *Subscription
	store *Store
	jobID string
	last  int64
}

// Follow subscribes to the events of the job, to be followed after the event numbered since.
func (b *Broker) Follow(st *Store, jobID string, since int64) *Follower {
	return &Follower{sub: b.Subscribe(jobID), store: st, jobID: jobID, last: since}
}

// Replay sends the stored events after the last one this follower has seen, in
// order, until there are no more, and counts each as seen.
func (f *Follower) Replay(ctx context.Context, send func(Event) error) error {
	for {
		evs, err := f.store.EventsAfter(ctx, f.jobID, f.last, replayBatch)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if err := send(ev); err != nil {
				return err
			}
			f.last = ev.Seq
		}
		if len(evs) < replayBatch {
			return nil
		}
	}
}

// Live is where the live events arrive. It is closed when the follower fell behind
// (see Slow), when it is closed and when the broker is.
func (f *Follower) Live() <-chan Event { return f.sub.C }

// Take says whether a live event is to be sent, which is when it is after the last
// one seen, and counts it as seen. The live events include some that Replay sent.
func (f *Follower) Take(ev Event) bool {
	if ev.Seq <= f.last {
		return false
	}
	f.last = ev.Seq
	return true
}

// Last is the number of the last event seen.
func (f *Follower) Last() int64 { return f.last }

// Slow reports whether the follower was closed because it fell behind, which it
// recovers from by following again from Last.
func (f *Follower) Slow() bool { return f.sub.Slow() }

// Close ends the follower.
func (f *Follower) Close() { f.sub.Cancel() }
