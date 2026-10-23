package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The calls an attempt of the worker makes of hub, with the PAT of the subscription as the
// bearer token. None of them reads or fills a cache of the core, and every one answers in
// the same five ways, which a caller tells apart with errors.Is and errors.As:
//
//   - hub refuses the token, 401: ErrRefused;
//   - hub forbids the call, 403: ErrForbidden, with a message that names the operation, says
//     that the PAT lacks the scope for it and points to PUT /api/v1/workspaces/{slug}, where
//     the token is replaced. This is so for the record of a workspace too, where the core's
//     Workspace reads 403 as not found;
//   - hub does not find what the call names, 404: ErrNotFound;
//   - hub rejects the request, any other 4xx: *RejectedError, with the message of hub;
//   - the answer says nothing about the request, 429, 5xx, a redirect, a transport failure, a
//     timeout, or a body that is not what hub sends: *UnavailableError.
//
// No error holds the PAT, the URL of the call or a part of the answer but hub's own message.
// Only this package knows the names of the fields of hub's answers.

var (
	// ErrForbidden is what a call wraps when hub answers 403.
	ErrForbidden = errors.New("hub forbids the operation to the token")
	// ErrRejected is what a call wraps when hub answers a 4xx that is none of the above.
	ErrRejected = errors.New("hub rejected the request")
)

// RejectedError is hub's refusal of a request: a 4xx other than 401, 403 and 404.
type RejectedError struct {
	// Op is what was asked, Status the status hub answered with, and Code and Message what
	// the error object of its answer says.
	Op            string
	Status        int
	Code, Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("hub rejected %s (%d): %s", e.Op, e.Status, e.Message)
}

func (e *RejectedError) Is(target error) bool { return target == ErrRejected }

const (
	// longTimeout bounds the lease of a sandbox and the push of a branch, where hub does
	// slow work before it answers: it creates a box from an image, it pushes to the forge.
	longTimeout = 5 * time.Minute
	// maxErrorBody is how much of the body of an error is read for its message, and
	// maxMessage how much of the message is kept.
	maxErrorBody = 64 << 10
	maxMessage   = 1024
)

// bound is how long a call may take, unless its context is shorter.
func (c *Client) bound(long bool) time.Duration {
	if long {
		return c.longBound()
	}
	if c.HTTP != nil && c.HTTP.Timeout > 0 {
		return c.HTTP.Timeout
	}
	return timeout
}

// longBound is the bound of the lease and of the push of a branch.
func (c *Client) longBound() time.Duration {
	if c.LongTimeout > 0 {
		return c.LongTimeout
	}
	return longTimeout
}

// operation is one call: what it does, for the messages, and what it sends.
type operation struct {
	name   string // "read the workspace record"
	slug   string // names where the token is replaced, in the message of a 403
	method string
	path   string // escaped
	body   any
	long   bool
	// into receives the decoded answer of a 2xx; nil leaves the answer unread.
	into any
	// missingIsDone makes a 404 a success.
	missingIsDone bool
	// header, when it is set, receives the headers of the answer.
	header *http.Header
	// maxBody, when it is set, is the longest answer that is read, in place of the usual 1 MiB.
	maxBody int
	// status, when it is set, receives the status of the answer.
	status *int
}

// send makes the call with the PAT and classifies the answer.
func (c *Client) send(ctx context.Context, pat string, op operation) error {
	ctx, cancel := context.WithTimeout(ctx, c.bound(op.long))
	defer cancel()
	var body io.Reader
	if op.body != nil {
		data, err := json.Marshal(op.body)
		if err != nil {
			return &UnavailableError{Err: errors.New("the request cannot be encoded")}
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, op.method, c.BaseURL+op.path, body)
	if err != nil {
		return &UnavailableError{Err: errors.New("the call could not be made")}
	}
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if op.body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The bound is the context's: a client with a timeout of its own would cut the slow calls
	// short.
	hc := *c.HTTP
	hc.Timeout = 0
	resp, err := hc.Do(req)
	if err != nil {
		return &UnavailableError{Err: cause(err)}
	}
	defer resp.Body.Close()
	if op.header != nil {
		*op.header = resp.Header.Clone()
	}
	if op.status != nil {
		*op.status = resp.StatusCode
	}
	return c.classify(resp, pat, op)
}

// classify turns the answer into the result of the call.
func (c *Client) classify(resp *http.Response, pat string, op operation) error {
	status := resp.StatusCode
	switch {
	case status >= 200 && status < 300:
		limit := maxBody
		if op.maxBody > 0 {
			limit = op.maxBody
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
		switch {
		case err != nil:
			return &UnavailableError{Status: status, Err: cause(err)}
		case len(data) > limit:
			return &UnavailableError{Status: status, Err: fmt.Errorf("the answer is longer than %d MiB", limit>>20)}
		case op.into == nil:
			return nil
		}
		if err := json.Unmarshal(data, op.into); err != nil {
			return &UnavailableError{Status: status, Err: errors.New("the answer is not what hub sends")}
		}
		return nil
	case status == http.StatusUnauthorized:
		return fmt.Errorf("hub refused the token to %s (%d): %w", op.name, status, ErrRefused)
	case status == http.StatusForbidden:
		return fmt.Errorf("hub forbids %s (%d): the PAT of the subscription lacks the scope for it; replace the token with PUT /api/v1/workspaces/%s: %w",
			op.name, status, url.PathEscape(op.slug), ErrForbidden)
	case status == http.StatusNotFound:
		if op.missingIsDone {
			return nil
		}
		return fmt.Errorf("hub does not find what %s names (%d): %w", op.name, status, ErrNotFound)
	case status >= 400 && status < 500 && status != http.StatusTooManyRequests:
		code, message := errorObject(resp.Body, pat)
		return &RejectedError{Op: op.name, Status: status, Code: code, Message: message}
	case status == http.StatusBadGateway:
		// The forge refused the credential that hub holds for it: a retry cannot cure it, the
		// person who keeps the workspace has to. Any other failure of the forge may pass.
		if code, message := errorObject(resp.Body, pat); code == "forge_unauthenticated" || code == "forge_forbidden" {
			return &RejectedError{Op: op.name, Status: status, Code: code, Message: message}
		}
	}
	// 429, 5xx, and a redirect, which is an answer and not something to follow.
	return &UnavailableError{Status: status}
}

// errorObject reads hub's error object {error: {code, message, error_type}}, whose code is the
// integer status repeated in the body and whose error_type, when the handler sets one, is the
// machine-readable kind. The code it returns is that kind, or the status when there is none; the
// message is cut short and has no token in it. A body that is none says what the status says.
func errorObject(body io.Reader, pat string) (code, message string) {
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorBody))
	var answer struct {
		Error struct {
			Code      json.RawMessage `json:"code"`
			Message   string          `json:"message"`
			ErrorType string          `json:"error_type"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &answer) != nil || answer.Error.Message == "" {
		return "", "hub gave no reason"
	}
	code = answer.Error.ErrorType
	if code == "" {
		code = strings.Trim(string(answer.Error.Code), `"`)
	}
	return scrubbed(code, pat), scrubbed(answer.Error.Message, pat)
}

// scrubbed is what hub said without the token, cut short.
func scrubbed(s, pat string) string {
	if pat != "" {
		s = strings.ReplaceAll(s, pat, "[token]")
	}
	if len(s) > maxMessage {
		s = strings.ToValidUTF8(s[:maxMessage], "")
	}
	return s
}

// workspacePath is the path of a route of the workspace, with its slug escaped.
func workspacePath(slug string, rest ...string) string {
	return "/api/v1/workspaces/" + url.PathEscape(slug) + strings.Join(rest, "")
}

// Record is what a workspace is: where to clone it from on hub's git server, the branch to
// work on, whether it is a standard workspace or carries patches, and the URL of the
// repository of the forge, whose issues and pull requests the worker works on.
type Record struct {
	CloneURL, Branch, Mode, ForgeURL string
}

// The modes of a workspace.
const (
	ModeStandard   = "standard"
	ModeCarryPatch = "carry_patch"
)

// WorkspaceRecord reads the record of the workspace: GET /api/v1/workspaces/{slug}. A record
// without a clone URL, a branch or a forge URL is not one the worker can work from, and is
// unavailable.
func (c *Client) WorkspaceRecord(ctx context.Context, pat, slug string) (Record, error) {
	var answer struct {
		HubURL        *string `json:"hub_url"`
		Branch        *string `json:"branch"`
		GitURL        string  `json:"git_url"`
		WorkspaceMode string  `json:"workspace_mode"`
		Mode          string  `json:"mode"`
	}
	err := c.send(ctx, pat, operation{name: "read the workspace record", slug: slug, method: http.MethodGet, path: workspacePath(slug), into: &answer})
	if err != nil {
		return Record{}, err
	}
	rec := Record{ForgeURL: answer.GitURL, Mode: answer.WorkspaceMode}
	if answer.HubURL != nil {
		rec.CloneURL = *answer.HubURL
	}
	if answer.Branch != nil {
		rec.Branch = *answer.Branch
	}
	if rec.Mode == "" {
		rec.Mode = answer.Mode
	}
	if rec.Mode == "" {
		rec.Mode = ModeStandard
	}
	if rec.CloneURL == "" || rec.Branch == "" || rec.ForgeURL == "" {
		return Record{}, &UnavailableError{Status: http.StatusOK, Err: errors.New("the record of the workspace has no clone URL, branch or forge URL")}
	}
	return rec, nil
}

// ResolvedVars reads the variables of the workspace after the tiers are merged: GET
// /api/v1/workspaces/{slug}/vars/resolved. Hub answers a list of {key, value, origin}; an
// object {"vars": {name: value}} and a list of {name, value} are read as well.
func (c *Client) ResolvedVars(ctx context.Context, pat, slug string) (map[string]string, error) {
	var raw json.RawMessage
	err := c.send(ctx, pat, operation{name: "read the resolved variables", slug: slug, method: http.MethodGet, path: workspacePath(slug, "/vars/resolved"), into: &raw})
	if err != nil {
		return nil, err
	}
	vars, ok := decodeVars(raw)
	if !ok {
		return nil, &UnavailableError{Status: http.StatusOK, Err: errors.New("the variables are not what hub sends")}
	}
	return vars, nil
}

// decodeVars reads the variables in either of their encodings.
func decodeVars(raw json.RawMessage) (map[string]string, bool) {
	var list []struct {
		Key   string `json:"key"`
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &list) == nil && list != nil {
		vars := make(map[string]string, len(list))
		for _, v := range list {
			name := v.Key
			if name == "" {
				name = v.Name
			}
			if name == "" {
				return nil, false
			}
			vars[name] = v.Value
		}
		return vars, true
	}
	var object struct {
		Vars map[string]string `json:"vars"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Vars != nil {
		return object.Vars, true
	}
	return nil, false
}

// SessionRequest is what a session is opened with.
type SessionRequest struct {
	WorkspaceSlug, Archetype, Model, RunID string
}

// OpenSession opens a session of the run: POST /api/v1/sessions. It returns its id.
func (c *Client) OpenSession(ctx context.Context, pat string, req SessionRequest) (string, error) {
	var answer struct {
		ID string `json:"id"`
	}
	err := c.send(ctx, pat, operation{
		name: "open a session", slug: req.WorkspaceSlug, method: http.MethodPost, path: "/api/v1/sessions", into: &answer,
		body: map[string]string{"workspace_slug": req.WorkspaceSlug, "archetype": req.Archetype, "model": req.Model, "run_id": req.RunID},
	})
	if err != nil {
		return "", err
	}
	if answer.ID == "" {
		return "", &UnavailableError{Status: http.StatusOK, Err: errors.New("the session has no id")}
	}
	return answer.ID, nil
}

// Usage is what an attempt cost: hub keeps the model and the tokens, and takes the rest
// along.
type Usage struct {
	Model, Brain              string
	InputTokens, OutputTokens int64
	CostUSD                   float64
	CostSource                string
	Turns                     int
}

// PostUsage posts the usage of the session: POST /api/v1/sessions/{id}/usage. The slug is
// where a refusal says the token is replaced.
func (c *Client) PostUsage(ctx context.Context, pat, slug, sessionID string, u Usage) error {
	return c.send(ctx, pat, operation{
		name: "post the usage of the session", slug: slug, method: http.MethodPost, path: "/api/v1/sessions/" + url.PathEscape(sessionID) + "/usage",
		body: map[string]any{
			"model": u.Model, "input_tokens": u.InputTokens, "output_tokens": u.OutputTokens,
			"cost_usd": u.CostUSD, "cost_source": u.CostSource, "brain": u.Brain, "turns": u.Turns,
		},
	})
}

// The statuses a session is completed with, which are the ones hub takes.
const (
	SessionCompleted = "completed"
	SessionFailed    = "failed"
	SessionTimeout   = "timeout"
)

// CompleteSession completes the session: POST /api/v1/sessions/{id}/complete.
func (c *Client) CompleteSession(ctx context.Context, pat, slug, sessionID, status, message string) error {
	body := map[string]string{"status": status}
	if message != "" {
		body["error_message"] = message
	}
	return c.send(ctx, pat, operation{
		name: "complete the session", slug: slug, method: http.MethodPost, path: "/api/v1/sessions/" + url.PathEscape(sessionID) + "/complete", body: body,
	})
}

// Event is an event of a run, as the loops write it.
type Event struct {
	Seq      int64
	TS, Type string
	Data     json.RawMessage
}

// PostEvents posts a batch of events of the run, at most 1000: POST
// /api/v1/workspaces/{slug}/runs/{run id}/events/batch. Hub takes an event once for its id,
// which is the run and the number of the event, so that a batch posted again adds nothing. An
// event that hub does not take is a *RejectedError, though the others were taken.
func (c *Client) PostEvents(ctx context.Context, pat, slug, runID string, events []Event) error {
	items := make([]map[string]any, len(events))
	for i, e := range events {
		items[i] = map[string]any{
			"id": fmt.Sprintf("%s-%d", runID, e.Seq), "run_id": runID, "event_type": e.Type, "timestamp": e.TS, "payload": payloadOf(e.Data),
		}
	}
	var answer struct {
		Errors []struct {
			Index   int    `json:"index"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	err := c.send(ctx, pat, operation{
		name: "post the events of the run", slug: slug, method: http.MethodPost, path: workspacePath(slug, "/runs/", url.PathEscape(runID), "/events/batch"),
		body: items, into: &answer,
	})
	if err != nil {
		return err
	}
	if len(answer.Errors) > 0 {
		return &RejectedError{Op: "post the events of the run", Status: http.StatusOK, Code: "partial", Message: fmt.Sprintf("hub did not take %d of %d events: %s", len(answer.Errors), len(events), scrubbed(answer.Errors[0].Message, pat))}
	}
	return nil
}

// payloadOf is the data of an event as the object that hub takes for a payload.
func payloadOf(data json.RawMessage) any {
	trimmed := bytes.TrimSpace(data)
	switch {
	case len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")):
		return map[string]any{}
	case trimmed[0] == '{':
		return data
	}
	return map[string]json.RawMessage{"data": data}
}

// LeaseRequest is what a sandbox is leased for: the profile (hub's default when empty), the
// purpose and the labels that name what the box is for.
type LeaseRequest struct {
	Profile, Purpose string
	Labels           map[string]string
}

// Lease is a sandbox that hub granted: where its gateway is, the token that opens it, and
// when it ends.
type Lease struct {
	ID, GatewayURL, Token string
	ExpiresAt             time.Time
}

// Lease leases a sandbox: POST /api/v1/workspaces/{slug}/sandboxes. Hub creates the box from
// the image of the profile before it answers, so the call has the long bound.
func (c *Client) Lease(ctx context.Context, pat, slug string, req LeaseRequest) (Lease, error) {
	body := map[string]any{"purpose": req.Purpose, "labels": req.Labels}
	if req.Labels == nil {
		body["labels"] = map[string]string{}
	}
	if req.Profile != "" {
		body["profile"] = req.Profile
	}
	var answer struct {
		ID         string `json:"id"`
		GatewayURL string `json:"gateway_url"`
		Token      string `json:"token"`
		ExpiresAt  string `json:"expires_at"`
	}
	err := c.send(ctx, pat, operation{name: "lease a sandbox", slug: slug, method: http.MethodPost, path: workspacePath(slug, "/sandboxes"), body: body, long: true, into: &answer})
	if err != nil {
		return Lease{}, err
	}
	expires, perr := time.Parse(time.RFC3339, answer.ExpiresAt)
	if answer.ID == "" || answer.GatewayURL == "" || answer.Token == "" || perr != nil {
		return Lease{}, &UnavailableError{Status: http.StatusOK, Err: errors.New("the lease has no id, gateway URL, token or end")}
	}
	return Lease{ID: answer.ID, GatewayURL: answer.GatewayURL, Token: answer.Token, ExpiresAt: expires}, nil
}

// ReleaseLease releases the lease: DELETE /api/v1/workspaces/{slug}/sandboxes/{id}. Hub
// destroys the box. A lease that hub does not find is released.
func (c *Client) ReleaseLease(ctx context.Context, pat, slug, id string) error {
	return c.send(ctx, pat, operation{
		name: "release the sandbox", slug: slug, method: http.MethodDelete, path: workspacePath(slug, "/sandboxes/", url.PathEscape(id)), missingIsDone: true,
	})
}

// PushBranch asks hub to push the branch of its clone to the forge: POST
// /api/v1/workspaces/{slug}/branches/{branch}/push. Hub pushes before it answers, so the call
// has the long bound.
func (c *Client) PushBranch(ctx context.Context, pat, slug, branch string) error {
	return c.send(ctx, pat, operation{
		name: "publish the branch", slug: slug, method: http.MethodPost, path: workspacePath(slug, "/branches/", url.PathEscape(branch), "/push"),
		body: map[string]any{}, long: true,
	})
}

// LeaseInfo is a lease as hub lists it: the labels the holder gave it and when it ends. It carries
// no token.
type LeaseInfo struct {
	ID, Purpose, State string
	Labels             map[string]string
	ExpiresAt          time.Time
}

// ListSandboxes lists the leases of the workspace that hub holds: GET
// /api/v1/workspaces/{slug}/sandboxes, a bare array of at most 200 leases that are leased or
// active, newest first. Hub has no pages for it.
func (c *Client) ListSandboxes(ctx context.Context, pat, slug string) ([]LeaseInfo, error) {
	var answer []struct {
		ID        string            `json:"id"`
		Purpose   string            `json:"purpose"`
		State     string            `json:"state"`
		Labels    map[string]string `json:"labels"`
		ExpiresAt string            `json:"expires_at"`
	}
	err := c.send(ctx, pat, operation{name: "list the sandboxes", slug: slug, method: http.MethodGet, path: workspacePath(slug, "/sandboxes"), into: &answer})
	if err != nil {
		return nil, err
	}
	out := make([]LeaseInfo, 0, len(answer))
	for _, l := range answer {
		expires, perr := time.Parse(time.RFC3339, l.ExpiresAt)
		if l.ID == "" || perr != nil {
			return nil, &UnavailableError{Status: http.StatusOK, Err: errors.New("a lease in the list has no id or no end")}
		}
		out = append(out, LeaseInfo{ID: l.ID, Purpose: l.Purpose, State: l.State, Labels: l.Labels, ExpiresAt: expires})
	}
	return out, nil
}

// CreateLabel makes sure the workspace's repository has the label: POST /api/v1/workspaces/{slug}/labels with
// {name, color, description}, the color six hex digits without '#'. It reports whether hub created the label (201) or found
// it there (200), and an existing label's colour and description are left as they are. Hub, or the forge behind it, may
// also refuse a label that exists: a 409, or any other 4xx whose error_type is already_exists or whose message says
// "already exists" in any case, is the label being there as well. Every other answer is classed as the other calls class
// it.
func (c *Client) CreateLabel(ctx context.Context, pat, slug, name, color, description string) (created bool, err error) {
	var answer map[string]json.RawMessage
	var status int
	err = c.send(ctx, pat, operation{
		name: "create a label", slug: slug, method: http.MethodPost, path: workspacePath(slug, "/labels"),
		body: map[string]string{"name": name, "color": color, "description": description}, into: &answer, status: &status,
	})
	if rejected, ok := errors.AsType[*RejectedError](err); ok && alreadyThere(rejected) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if answer == nil {
		return false, &UnavailableError{Status: status, Err: errors.New("the label is not what hub sends")}
	}
	return status == http.StatusCreated, nil
}

// alreadyThere says that a refusal of a label creation reports the label as existing.
func alreadyThere(r *RejectedError) bool {
	return r.Status >= 400 && r.Status < 500 &&
		(r.Status == http.StatusConflict || r.Code == "already_exists" || strings.Contains(strings.ToLower(r.Message), "already exists"))
}
