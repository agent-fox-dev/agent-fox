package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// KeepaliveEvery is how often a stream that is open sends a comment, so that a link that
// would otherwise be idle is not taken for dead.
const KeepaliveEvery = 15 * time.Second

// KeepaliveFrame is the comment that a stream sends every KeepaliveEvery.
const KeepaliveFrame = ": keepalive\n\n"

// The ways a stream ends other than at the rest of the job. A client that sees its stream end
// without a job_state at rest in it follows the job again from the last event it has.
var (
	// ErrSlowSubscriber: the client fell more than SubscriberBuffer events behind.
	ErrSlowSubscriber = errors.New("the client of the stream fell too far behind")
	// ErrStreamsClosed: the service or the server is shutting down.
	ErrStreamsClosed = errors.New("the streams are closed")
)

// Frame is the event as a stream sends it: its number, its type and its data on one line,
// {seq, ts, job_id, attempt, type, data}, and an empty line.
func (e Event) Frame() []byte {
	line, err := encode(e)
	if err != nil {
		// Every field is a number, a string, or JSON that was valid when it was stored.
		panic(fmt.Sprintf("jobs: the event %s/%d cannot be encoded: %v", e.JobID, e.Seq, err))
	}
	return fmt.Appendf(nil, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, line)
}

// atRest reports whether the event is a job_state that says the job is at rest.
func (e Event) atRest() bool {
	if e.Type != EventJobState {
		return false
	}
	var data struct {
		AtRest bool `json:"at_rest"`
	}
	return json.Unmarshal(e.Data, &data) == nil && data.AtRest
}

// LatestState is the number of the latest job_state event of the job and whether it says the
// job is at rest; the number is 0 if the job has none.
func (s *Store) LatestState(ctx context.Context, jobID string) (seq int64, atRest bool, err error) {
	var data string
	err = s.db.QueryRowContext(ctx,
		"SELECT seq, data FROM af_events WHERE job_id = ? AND type = ? ORDER BY seq DESC LIMIT 1", jobID, EventJobState).Scan(&seq, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("latest state of %s: %w", jobID, err)
	}
	return seq, Event{Type: EventJobState, Data: json.RawMessage(data)}.atRest(), nil
}

// Stream follows the events of a job after the one numbered since: it hands each to send, in
// order, the stored ones first and then the live ones, with none missing and none twice; it
// calls keepalive every KeepaliveEvery while it waits. It returns nil when the job is at rest
// and everything up to its rest has been sent: at the end of the replay if it is at rest by
// then, and otherwise after the first live job_state that says it is. It returns
// ErrSlowSubscriber if the caller fell more than SubscriberBuffer events behind,
// ErrStreamsClosed if the service is closing, and the error of send, of keepalive or of the
// context if one stops it.
func (s *Service) Stream(ctx context.Context, jobID string, since int64, send func(Event) error, keepalive func() error) error {
	// Subscribed before anything is read from the store: an event is in the store, in the
	// subscription or in both, and its number says which were sent already.
	f := s.broker.Follow(s.store, jobID, since)
	defer f.Close()

	for {
		if err := f.Replay(ctx, send); err != nil {
			return err
		}
		seq, atRest, err := s.store.LatestState(ctx, jobID)
		if err != nil {
			return err
		}
		if seq > f.Last() {
			continue // it was stored after the replay read the file: read again
		}
		if atRest {
			return nil
		}
		break
	}

	tick := s.clock.NewTicker(KeepaliveEvery)
	defer tick.Stop()
	for {
		select {
		case ev, ok := <-f.Live():
			switch {
			case !ok && f.Slow():
				return ErrSlowSubscriber
			case !ok:
				return ErrStreamsClosed
			case !f.Take(ev):
				continue // it was in the replay
			}
			if err := send(ev); err != nil {
				return err
			}
			if ev.atRest() {
				return nil
			}
		case <-tick.C():
			if err := keepalive(); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// CloseStreams ends every stream that is open, and every stream that is begun afterwards
// ends once its replay is sent. The jobs go on; it is what the server does when it shuts down.
func (s *Service) CloseStreams() { s.broker.Close() }
