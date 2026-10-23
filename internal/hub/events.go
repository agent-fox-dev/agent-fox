package hub

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"
)

// The types of hub's audit events that the service acts on: a label put on an issue of the workspace's repository, and an
// issue closed, as hub's webhook ingress relays them from the forge.
const (
	EventIssueLabeled = "hub.forge.issue.labeled"
	EventIssueClosed  = "hub.forge.issue.closed"
)

// EventStream is an open connection to hub's event stream of a workspace. It is read until the context it was opened
// with ends, hub ends it, or Close is called.
type EventStream struct {
	body   io.ReadCloser
	cancel context.CancelFunc
}

// Close ends the connection.
func (s *EventStream) Close() error {
	s.cancel()
	return s.body.Close()
}

// Events opens hub's event stream of the workspace: GET /api/v1/events?workspace=<slug>, with the PAT, which needs the
// scope audit:read. The answer's headers are waited for for at most the client's ordinary bound; the stream is then read
// with no bound of its own, until ctx ends. A redirect is not followed. An answer that is not a stream is classed as the
// other calls class theirs: 401 refused, 403 forbidden (the PAT lacks audit:read), 404 not found, another 4xx except 429
// rejected, and 429, a 5xx, a redirect, a transport failure, a timeout or a 200 that is not text/event-stream
// unavailable. No error holds the PAT.
func (c *Client) Events(ctx context.Context, pat, slug string) (*EventStream, error) {
	sctx, cancel := context.WithCancel(ctx)
	req, err := http.NewRequestWithContext(sctx, http.MethodGet, c.BaseURL+"/api/v1/events?workspace="+url.QueryEscape(slug), nil)
	if err != nil {
		cancel()
		return nil, &UnavailableError{Err: errors.New("the call could not be made")}
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", c.UserAgent)
	// The client's bound would cut the stream short: it bounds the wait for the headers only.
	hc := *c.HTTP
	hc.Timeout = 0
	late := time.AfterFunc(c.bound(false), cancel)
	resp, err := hc.Do(req)
	if !late.Stop() {
		if err == nil {
			_ = resp.Body.Close() // what came after the bound is not read
		}
		cancel()
		return nil, &UnavailableError{Err: fmt.Errorf("hub did not answer within %v", c.bound(false))}
	}
	if err != nil {
		cancel()
		// What the transport says can quote the answer, a Location header that echoes the request included.
		return nil, &UnavailableError{Err: errors.New(scrubbed(cause(err).Error(), pat))}
	}
	if resp.StatusCode == http.StatusOK {
		if media, _, perr := mime.ParseMediaType(resp.Header.Get("Content-Type")); perr == nil && media == "text/event-stream" {
			return &EventStream{body: resp.Body, cancel: cancel}, nil
		}
	}
	defer cancel()
	defer resp.Body.Close()
	switch status := resp.StatusCode; {
	case status >= 200 && status < 300:
		return nil, &UnavailableError{Status: status, Err: errors.New("the answer is not an event stream")}
	case status == http.StatusForbidden:
		return nil, fmt.Errorf("hub forbids the token to open the event stream (%d)%s: the PAT of the subscription lacks the scope audit:read; replace the token with PUT /api/v1/workspaces/%s: %w",
			status, hubSaid(resp.Body, pat), url.PathEscape(slug), ErrForbidden)
	}
	return nil, c.classify(resp, pat, operation{name: "open the event stream", slug: slug})
}
