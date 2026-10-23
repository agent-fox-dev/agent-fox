package afclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	// IdleCut is how long a stream may send no byte before it is cut.
	IdleCut = 60 * time.Second

	// maxFailedTries is how many failed tries in a row end the following.
	maxFailedTries = 5

	// afterEvent is the pause after a try that delivered an event.
	afterEvent = time.Second
)

// afterFailure is the pause after the first, second, third and fourth failed try in a row.
var afterFailure = [maxFailedTries - 1]time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second}

// The reasons a try can end without the job at rest, besides an *Error of the service.
var (
	errIdle         = errors.New("the stream sent no byte for 60 seconds")
	errStreamEnded  = errors.New("the event stream ended with no new event and the job is not at rest")
	errBadFrame     = errors.New("the stream sent a frame that is not an event")
	errStreamBroken = errors.New("the stream broke off")
)

// Event is an event of a job as the stream delivers it.
type Event struct {
	Seq  int64
	Type string
	// Document is the event as the service sent it: {seq, ts, job_id, attempt, type, data}.
	Document json.RawMessage
}

// Rest is a job that is at rest.
type Rest struct {
	// Job is the document of the job with its attempts, as the service answered.
	Job json.RawMessage
	// LastSeq is the number of the last event that was delivered, or the number followed from.
	LastSeq int64
}

// GaveUp is the end of a following that made five failed tries in a row. It unwraps to the
// *Error of the last failure.
type GaveUp struct {
	// Err is the failure: the category of the last failed try, retryable, saying that the job may still run.
	Err     *Error
	JobID   string
	LastSeq int64
}

func (g *GaveUp) Error() string { return g.Err.Error() }

func (g *GaveUp) Unwrap() error { return g.Err }

// jobView is what the follower reads of the document of a job.
type jobView struct {
	State    string `json:"state"`
	Attempts []struct {
		N     int    `json:"n"`
		State string `json:"state"`
	} `json:"attempts"`
}

// atRest says whether the job is blocked, done, failed or cancelled and has no attempt that is running.
func (v jobView) atRest() bool {
	switch v.State {
	case "blocked", "done", "failed", "cancelled":
	default:
		return false
	}
	for _, a := range v.Attempts {
		if a.State == "running" {
			return false
		}
	}
	return true
}

// Follow follows the job id until it is at rest, delivering each of its events above since to
// onEvent, in order and once.
//
// It works in tries. A try asks for the stream of events from the last one delivered and reads it
// until it ends: at its end, on a dropped connection, on a frame that is not an event, or when it
// has sent no byte for IdleCut. Then it reads the job, and the following is over if the job is at
// rest. A try that ends otherwise fails when it delivered no new event, whatever ended it, a 5xx or
// a transport error on either request among the causes. The pause before the next try is one second
// after a try that delivered an event, which also clears the count of failed tries, and 1, 2, 4 and 8
// seconds after the first four failed tries in a row. The fifth ends the following with a *GaveUp.
//
// A 401, a 404, a 3xx or any other 4xx on either request ends it at once with the *Error of the
// answer, as does a read of the job that is not the JSON expected. A call stopped by ctx returns its error.
func (c *Client) Follow(ctx context.Context, id string, since int64, onEvent func(Event)) (*Rest, error) {
	last, failed := since, 0
	for {
		delivered, rest, cause, err := c.try(ctx, id, &last, onEvent)
		if err != nil {
			return nil, err
		}
		if rest != nil {
			rest.LastSeq = last
			return rest, nil
		}
		var pause time.Duration
		if delivered > 0 {
			failed, pause = 0, afterEvent
		} else {
			failed++
			if failed == maxFailedTries {
				return nil, giveUp(id, last, cause)
			}
			pause = afterFailure[failed-1]
		}
		select {
		case <-c.clock().After(pause):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// try makes one try: it reads the stream from *last, then the job. It returns how many events it
// delivered, the job if it is at rest, why the try did not reach rest (nil for a stream that
// ended cleanly), or the error that ends the following.
func (c *Client) try(ctx context.Context, id string, last *int64, onEvent func(Event)) (int, *Rest, error, error) {
	delivered, cause, opened, err := c.readStream(ctx, id, last, onEvent)
	if err != nil {
		return delivered, nil, nil, err
	}
	if !opened { // the request for the stream failed: there is no stream to end, and the job is not read
		return delivered, nil, cause, nil
	}
	resp, jerr := c.Do(ctx, "GET", "/jobs/"+url.PathEscape(id), nil, nil)
	if jerr != nil {
		if err := endsFollowing(ctx, jerr); err != nil {
			return delivered, nil, nil, err
		}
		return delivered, nil, jerr, nil
	}
	var view jobView
	if derr := resp.Decode(&view); derr != nil {
		return delivered, nil, nil, derr
	}
	if view.atRest() {
		return delivered, &Rest{Job: resp.Body}, nil, nil
	}
	return delivered, nil, cause, nil
}

// endsFollowing is the error that a failed request ends the following with, or nil when the try
// merely fails: a request that ctx stopped, an answer that is a 3xx or a 4xx, or an error that is
// not an answer of the service end it; a 5xx and a transport error do not.
func endsFollowing(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var ae *Error
	if !errors.As(err, &ae) {
		return err
	}
	if ae.Status == 0 || ae.Status >= 500 {
		return nil
	}
	return err
}

// readStream reads the stream of the events above *last until it ends, delivering them. It
// returns how many it delivered, why the stream ended if that was not its end (nil for its end),
// whether a stream was opened, or the error that ends the following. A request that gets no stream
// because of a 5xx or a transport error is not opened, and its error is the cause.
func (c *Client) readStream(ctx context.Context, id string, last *int64, onEvent func(Event)) (delivered int, cause error, opened bool, err error) {
	sctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	watch := &idleWatch{clock: c.clock(), cancel: cancel}
	watch.touch()
	done := make(chan struct{})
	defer close(done)
	go watch.run(done)

	// ended is why a stream that stopped did so, when it did not come to its end: the idle cut
	// or the break, or the error of ctx, which ends the following.
	ended := func(broken error) (why, stop error) {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(context.Cause(sctx), errIdle):
			return errIdle, nil
		}
		return broken, nil
	}

	resp, serr := c.stream(sctx, "/jobs/"+url.PathEscape(id)+"/events", url.Values{"since": {strconv.FormatInt(*last, 10)}})
	if serr != nil {
		if why, stop := ended(nil); stop != nil || why != nil { // ctx stopped it, or no byte came in time
			return 0, why, why != nil, stop
		}
		if stop := endsFollowing(ctx, serr); stop != nil {
			return 0, nil, false, stop
		}
		return 0, serr, false, nil
	}
	defer resp.Body.Close()

	frames := newSSEReader(&touchReader{r: resp.Body, watch: watch})
	for {
		fr, ferr := frames.next()
		if ferr != nil {
			broken := errStreamBroken
			if ferr == io.EOF {
				broken = nil
			}
			why, stop := ended(broken)
			return delivered, why, true, stop
		}
		var doc struct {
			Seq  int64  `json:"seq"`
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(fr.data), &doc) != nil || doc.Seq <= 0 {
			return delivered, errBadFrame, true, nil
		}
		if doc.Seq <= *last {
			continue
		}
		*last = doc.Seq
		delivered++
		onEvent(Event{Seq: doc.Seq, Type: doc.Type, Document: json.RawMessage(fr.data)})
	}
}

// giveUp is the end of a following whose fifth try in a row failed for cause.
func giveUp(id string, last int64, cause error) error {
	e := &Error{Category: CategoryService, Retryable: true}
	var ae *Error
	why := errStreamEnded.Error()
	if errors.As(cause, &ae) {
		e.Category = ae.Category
		why = ae.Message
	} else if cause != nil {
		why = cause.Error()
	}
	e.Message = fmt.Sprintf("stopped following the job %s after %d failed tries in a row; the job may still be running on the service: %s", id, maxFailedTries, why)
	if e.Category != CategoryHub {
		e.Category = CategoryService
	}
	return &GaveUp{Err: e, JobID: id, LastSeq: last}
}

// idleWatch cuts a stream that sends no byte for IdleCut.
type idleWatch struct {
	clock  Clock
	cancel context.CancelCauseFunc
	last   atomic.Int64 // when the last byte came, in nanoseconds on the clock
}

func (w *idleWatch) touch() { w.last.Store(w.clock.Now().UnixNano()) }

// run waits until a whole IdleCut has passed since the last byte, and cuts the stream then, or
// until done is closed.
func (w *idleWatch) run(done <-chan struct{}) {
	for {
		idle := time.Duration(w.clock.Now().UnixNano() - w.last.Load())
		if idle >= IdleCut {
			w.cancel(errIdle)
			return
		}
		select {
		case <-w.clock.After(IdleCut - idle):
		case <-done:
			return
		}
	}
}

// touchReader tells the watch of every byte that is read.
type touchReader struct {
	r     io.Reader
	watch *idleWatch
}

func (t *touchReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.watch.touch()
	}
	return n, err
}
