package hub

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	lines  *bufio.Scanner
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
			return newEventStream(resp.Body, cancel), nil
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

// maxEventLine is the longest line of the event stream that is read, its line break aside.
const maxEventLine = 1 << 20

// Activity is what a connection received: a comment, or a frame. Event is the event that a frame carries when its data
// is a JSON object, and nil otherwise.
type Activity struct {
	Comment bool
	Event   *StreamEvent
}

// StreamEvent is an event of hub's stream, as far as the service reads it: its id and type, the workspace when hub names
// one, and, for an event about an issue of the forge, the issue's number (0 when absent) and the label (empty when absent).
type StreamEvent struct {
	ID, Type, Workspace string
	Issue               int
	Label               string
}

// sseFrame is a frame of the stream: the fields id, event and data, the lines of data joined with a line break.
type sseFrame struct{ id, event, data string }

// newEventStream reads the body of an answer as an event stream; cancel ends the connection.
func newEventStream(body io.ReadCloser, cancel context.CancelFunc) *EventStream {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64<<10), maxEventLine+2) // a line of maxEventLine bytes and its CRLF
	return &EventStream{body: body, cancel: cancel, lines: sc}
}

// frame reads up to the next comment or the next frame, by the rules of server-sent events, which the af client's reader
// of the service's own stream follows as well: the fields id:, event: and data: up to an empty line, the lines of data
// joined with a line break, and a line that begins with a colon a comment; an empty line that ends no field is nothing. It
// returns io.EOF at the end of the stream, where a frame that no empty line ended is dropped, and any other error when the
// stream cannot be read or has a line over 1 MiB.
func (s *EventStream) frame() (f sseFrame, comment bool, err error) {
	var data []string
	fields := false
	for s.lines.Scan() {
		line := s.lines.Text()
		switch {
		case line == "" && !fields:
			continue
		case line == "":
			f.data = strings.Join(data, "\n")
			return f, false, nil
		case line[0] == ':':
			if !fields {
				return sseFrame{}, true, nil
			}
			continue
		}
		fields = true
		name, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch name {
		case "id":
			f.id = value
		case "event":
			f.event = value
		case "data":
			data = append(data, value)
		}
	}
	if err := s.lines.Err(); err != nil {
		return sseFrame{}, false, err
	}
	return sseFrame{}, false, io.EOF
}

// Next reads the next comment or frame. A frame whose data is a JSON object carries its event, decoded; any other frame,
// a frame with no data included, is activity and nothing more. Next returns io.EOF when hub ended the stream, and another
// error when the stream cannot be read, its context ended, or a line is over 1 MiB.
func (s *EventStream) Next() (Activity, error) {
	f, comment, err := s.frame()
	switch {
	case err != nil:
		return Activity{}, err
	case comment:
		return Activity{Comment: true}, nil
	}
	ev, ok := decodeEvent(f)
	if !ok {
		return Activity{}, nil
	}
	return Activity{Event: &ev}, nil
}

// decodeEvent reads the event of a frame whose data is a JSON object: hub's audit event {id, event_type, resource_type,
// resource_id, workspace, metadata: {number, label}}, and the encodings with a payload ({issue}, {issue: {number}},
// {number}, {label}, {label: {name}}) and workspace_slug as well. The frame's id: and event: stand in for an id and a type
// the object lacks.
func decodeEvent(f sseFrame) (StreamEvent, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal([]byte(f.data), &obj) != nil || obj == nil {
		return StreamEvent{}, false
	}
	ev := StreamEvent{
		ID: firstString(obj, "id"), Type: firstString(obj, "event_type"), Workspace: firstString(obj, "workspace", "workspace_slug"),
	}
	if ev.ID == "" {
		ev.ID = f.id
	}
	if ev.Type == "" {
		ev.Type = f.event
	}
	for _, key := range []string{"metadata", "payload"} {
		var inner map[string]json.RawMessage
		if json.Unmarshal(obj[key], &inner) != nil || inner == nil {
			continue
		}
		if ev.Issue == 0 {
			ev.Issue = issueNumber(inner)
		}
		if ev.Label == "" {
			ev.Label = labelName(inner["label"])
		}
	}
	if ev.Issue == 0 && firstString(obj, "resource_type") == "issue" {
		if n, err := strconv.Atoi(firstString(obj, "resource_id")); err == nil && n > 0 {
			ev.Issue = n
		}
	}
	return ev, true
}

// firstString is the first of the keys whose value is a string.
func firstString(obj map[string]json.RawMessage, keys ...string) string {
	for _, k := range keys {
		var v string
		if json.Unmarshal(obj[k], &v) == nil && v != "" {
			return v
		}
	}
	return ""
}

// issueNumber is the number of the issue that an object of an event names: {issue: N}, {issue: {number: N}} or {number: N}.
func issueNumber(obj map[string]json.RawMessage) int {
	var n int
	if json.Unmarshal(obj["issue"], &n) == nil && n > 0 {
		return n
	}
	var issue struct {
		Number int `json:"number"`
	}
	if json.Unmarshal(obj["issue"], &issue) == nil && issue.Number > 0 {
		return issue.Number
	}
	if json.Unmarshal(obj["number"], &n) == nil && n > 0 {
		return n
	}
	return 0
}

// labelName is a label given as its name, or as {name}.
func labelName(raw json.RawMessage) string {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		return name
	}
	var label struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &label) == nil {
		return label.Name
	}
	return ""
}
