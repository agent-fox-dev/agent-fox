package afclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func TestFinishedAttempt_IsTheHighestAttemptThatIsNotQueuedOrRunning(t *testing.T) {
	for doc, want := range map[string]int{
		`{"attempts":[{"n":1,"state":"failed"},{"n":3,"state":"done"},{"n":2,"state":"failed"}]}`: 3,
		`{"attempts":[{"n":1,"state":"failed"},{"n":2,"state":"running"}]}`:                       1,
		`{"attempts":[{"n":1,"state":"queued"}]}`:                                                 0,
		`{"attempts":[]}`:  0,
		`{"state":"done"}`: 0,
		`not json`:         0,
	} {
		if got := FinishedAttempt(json.RawMessage(doc)); got != want {
			t.Errorf("FinishedAttempt(%s) = %d, want %d", doc, got, want)
		}
	}
}

func TestEnvelope_IsTheEnvelopeOfTheLastFinishedAttemptOrSaysWhyNot(t *testing.T) {
	const stored = `{"status":"done","result":{"fixed":true},"warnings":[]}`
	twoDone := json.RawMessage(`{"attempts":[{"n":1,"state":"failed"},{"n":2,"state":"done"}]}`)
	for _, c := range []struct {
		name        string
		job         json.RawMessage
		answer      step
		envelope    string
		unavailable string // part of it; "" for none
	}{
		{"stored", twoDone, answers(200, stored), stored, ""},
		{"no attempt has finished", json.RawMessage(`{"attempts":[{"n":1,"state":"running"}]}`), nil, "", ""},
		{"the service has none", twoDone, answers(404, `{"error":{"code":"not_found","message":"no envelope"}}`), "", ""},
		{"the service fails", twoDone, answers(503, ``), "", "the envelope of attempt 2 of the job j could not be fetched: the service answered 503"},
		{"not JSON", twoDone, answers(200, `{oops`), "", "the envelope of attempt 2 of the job j is not JSON"},
	} {
		asked := make(chan string, 2)
		c2 := serve(t, func(w http.ResponseWriter, r *http.Request) {
			asked <- r.URL.Path
			c.answer(w, r)
		}, nil)

		envelope, unavailable, err := c2.Envelope(context.Background(), "j", c.job)

		if err != nil || string(envelope) != c.envelope || (c.unavailable == "") != (unavailable == "") || !strings.Contains(unavailable, c.unavailable) {
			t.Errorf("%s: Envelope = %s, %q, %v, want %s and %q", c.name, envelope, unavailable, err, c.envelope, c.unavailable)
		}
		close(asked)
		var got []string
		for path := range asked {
			got = append(got, path)
		}
		if c.answer == nil && len(got) != 0 || c.answer != nil && !slices.Equal(got, []string{"/api/v1/jobs/j/attempts/2/envelope"}) {
			t.Errorf("%s: asked for %v", c.name, got)
		}
	}
}

func TestEnvelope_ReturnsTheErrorOfAStoppedContext(t *testing.T) {
	c := serve(t, answers(200, `{}`), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := c.Envelope(ctx, "j", json.RawMessage(`{"attempts":[{"n":1,"state":"done"}]}`))

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the error of the context", err)
	}
}
