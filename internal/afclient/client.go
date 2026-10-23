package afclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	// apiPrefix is where the API of the service is, under the server's URL.
	apiPrefix = "/api/v1"

	// RequestBound is how long a request other than the event stream may last.
	RequestBound = 2 * time.Minute

	// MaxBody is how much of the body of an answer is read.
	MaxBody = 8 << 20
)

// errBound is the cause of a request that was cut at RequestBound.
var errBound = errors.New("the request ran past its bound")

// Client sends requests to one agent-fox service with one credential.
type Client struct {
	// Clock is where the bound of a request takes its timer from; nil is the system's.
	Clock Clock

	server    Server
	token     string
	userAgent string
	http      *http.Client
}

// Clock is the time the client waits by. Production uses the system's; the tests move their own.
type Clock interface {
	// Now is the time.
	Now() time.Time
	// After returns a channel that gets the time once d has passed.
	After(d time.Duration) <-chan time.Time
}

// systemClock is the clock of the system.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// New makes the client of server, which sends token as the bearer credential and userAgent as
// the User-Agent. transport is the transport of the requests; nil is the default one. A
// redirect is never followed, so a credential cannot reach a host that server does not name.
func New(server Server, token, userAgent string, transport http.RoundTripper) *Client {
	return &Client{
		server: server, token: token, userAgent: userAgent,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

// Server is the service the client talks to.
func (c *Client) Server() Server { return c.server }

func (c *Client) clock() Clock {
	if c.Clock != nil {
		return c.Clock
	}
	return systemClock{}
}

// Response is the answer of the service to a request that it answered with a 2xx status.
type Response struct {
	Status int
	Header http.Header
	Body   []byte

	server Server
}

// Decode reads the JSON document of the answer into v. A body that is not the JSON that v
// describes is an error of the service that is not retryable.
func (r *Response) Decode(v any) error {
	if err := json.Unmarshal(r.Body, v); err != nil {
		return &Error{
			Status: r.Status, Category: CategoryService,
			Message: fmt.Sprintf("the service at %s (given by %s) answered with a document that is not the JSON expected", r.server.URL, r.server.Source),
		}
	}
	return nil
}

// newRequest makes a request to target with the headers every request carries: the credential,
// the User-Agent, the Accept header and, when it has a body, the JSON content type.
func (c *Client) newRequest(ctx context.Context, method, target string, body io.Reader, accept string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, fmt.Errorf("the request cannot be made: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", accept)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// stream opens the event stream at the API path: a GET that accepts text/event-stream and has no
// bound of time, since the stream lasts as long as the job. The caller reads and closes the body of
// the answer, which is a 2xx; any other answer is an *Error, and a request that ctx stopped
// returns the error of ctx.
func (c *Client) stream(ctx context.Context, path string, query url.Values) (*http.Response, error) {
	target := c.server.URL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := c.newRequest(ctx, http.MethodGet, target, nil, "text/event-stream")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, transportError(c.server, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, MaxBody))
		return nil, answerError(resp.StatusCode, data, path)
	}
	return resp, nil
}

// Do sends method to the API path (which starts with "/", like "/workspaces"), with the query
// and with body encoded as JSON when it is not nil, and returns the answer when its status is
// 2xx. The request carries the credential, the User-Agent and the Accept header, and goes to
// the server's URL and to no other.
//
// The request lasts at most RequestBound, which the clock measures, and the shorter time that
// ctx allows. At most MaxBody bytes of the answer are read. A request that ctx stopped returns
// the error of ctx, which the command leaves to its shell; any other failure is an *Error,
// whose category, message and hint follow the answer.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any) (*Response, error) {
	target := c.server.URL + apiPrefix + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("the request body cannot be encoded: %w", err)
		}
		payload = bytes.NewReader(data)
	}

	reqCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	bound := c.clock().After(RequestBound)
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-bound:
			cancel(errBound)
		case <-done:
		}
	}()

	// fail is the error of a request that went wrong on its way, whichever way that was.
	fail := func(err error) (*Response, error) {
		switch {
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(context.Cause(reqCtx), errBound):
			return nil, &Error{
				Category: CategoryService, Retryable: true,
				Message: fmt.Sprintf("the service at %s (given by %s) did not answer within %s", c.server.URL, c.server.Source, RequestBound),
			}
		}
		return nil, transportError(c.server, err)
	}

	req, err := c.newRequest(reqCtx, method, target, payload, "application/json")
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return fail(err)
	}
	tooLarge := len(data) > MaxBody
	if tooLarge {
		data = data[:MaxBody]
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, answerError(resp.StatusCode, data, path)
	}
	if tooLarge {
		return nil, &Error{
			Status: resp.StatusCode, Category: CategoryService,
			Message: fmt.Sprintf("the answer of the service at %s (given by %s) is longer than %d MiB", c.server.URL, c.server.Source, MaxBody>>20),
		}
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data, server: c.server}, nil
}
