// Package hub is the client of hub's API: the one place that knows hub's paths
// and how to read hub's answers, including the field names of its user object.
//
// A call ends in one of four ways, and the caller tells them apart with
// errors.Is and errors.As, never by the text of a message:
//
//   - it succeeds;
//   - hub refuses the token (ErrRefused);
//   - hub does not find the workspace for the token (ErrNotFound);
//   - the answer says nothing about the token (*UnavailableError): hub is down,
//     slow, redirecting, rate limiting or sending what is not understood.
//
// No error holds the token, nor the URL of a call, nor any part of an answer.
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

const (
	// timeout bounds a call, unless the context of the call is shorter.
	timeout = 10 * time.Second
	// maxBody is how much of an answer is read.
	maxBody = 1 << 20
)

// User is the hub user a token belongs to.
type User struct {
	ID string
	// Login is hub's name for the user. It is optional, and nothing relies on it.
	Login string
}

var (
	// ErrRefused is what a call wraps when hub refuses the token.
	ErrRefused = errors.New("hub refused the token")
	// ErrNotFound is what a call wraps when hub does not find the workspace for
	// the token, whether it does not exist or the token may not see it.
	ErrNotFound = errors.New("hub does not find the workspace")
)

// UnavailableError is the failure of a call whose answer says nothing about the
// token: a status that is none of the ones the call understands, a transport
// failure, a timeout, or a body that is not what hub sends.
type UnavailableError struct {
	// Status is the status hub answered with, or 0 when it did not answer.
	Status int
	// Err is the cause, without the URL of the call.
	Err error
}

func (e *UnavailableError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("hub is unavailable: it answered %d", e.Status)
	}
	return "hub is unavailable: " + e.Err.Error()
}

func (e *UnavailableError) Unwrap() error { return e.Err }

// Client calls hub.
type Client struct {
	// BaseURL is hub's URL, with the path prefix that hub is served under, if any.
	BaseURL string
	// HTTP makes the calls. New gives it a timeout of 10 seconds and no redirects.
	HTTP *http.Client
	// UserAgent is sent with every call.
	UserAgent string
}

// New makes a client of the hub at baseURL, for the agent-fox of the version.
func New(baseURL, version string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP: &http.Client{
			Timeout: timeout,
			// A redirect is an answer, not something to follow: the token must
			// never reach a host that the configuration did not name.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		UserAgent: "agent-fox/" + version,
	}
}

// User asks hub whom the token belongs to: GET <base>/api/v1/user. Hub's 401 and
// 403 are ErrRefused; a 200 with an object that has a non-empty id is the user;
// every other answer is an *UnavailableError.
func (c *Client) User(ctx context.Context, token string) (User, error) {
	status, body, err := c.get(ctx, token, c.BaseURL+"/api/v1/user")
	if err != nil {
		return User{}, err
	}
	switch status {
	case http.StatusOK:
		return readUser(body)
	case http.StatusUnauthorized, http.StatusForbidden:
		return User{}, fmt.Errorf("hub refused the token on the user call (%d): %w", status, ErrRefused)
	}
	return User{}, &UnavailableError{Status: status}
}

// Workspace asks hub whether the token sees the workspace: GET
// <base>/api/v1/workspaces/<slug>, the slug path-escaped. Hub's 200 with an
// object is nil (found), 401 is ErrRefused, and 403 and 404 are ErrNotFound;
// every other answer is an *UnavailableError.
func (c *Client) Workspace(ctx context.Context, token, slug string) error {
	// PathEscape keeps a "/" or a "?" in a slug inside its segment, and the URL
	// that is parsed from the string keeps that escaped form on the wire.
	status, body, err := c.get(ctx, token, c.BaseURL+"/api/v1/workspaces/"+url.PathEscape(slug))
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
		if _, err := readObject(body); err != nil {
			return err
		}
		return nil
	case http.StatusUnauthorized:
		return fmt.Errorf("hub refused the token on the workspace call (%d): %w", status, ErrRefused)
	case http.StatusForbidden, http.StatusNotFound:
		return fmt.Errorf("hub answered %d for the workspace: %w", status, ErrNotFound)
	}
	return &UnavailableError{Status: status}
}

// get makes a GET call and reads at most maxBody bytes of the answer. A transport
// failure, and an answer that is longer, are *UnavailableError.
func (c *Client) get(ctx context.Context, token, target string) (status int, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, nil, &UnavailableError{Err: errors.New("the call could not be made")}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, &UnavailableError{Err: cause(err)}
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return 0, nil, &UnavailableError{Status: resp.StatusCode, Err: cause(err)}
	}
	if len(body) > maxBody {
		return 0, nil, &UnavailableError{Err: errors.New("the answer is longer than 1 MiB")}
	}
	return resp.StatusCode, body, nil
}

// cause is err without the URL of the call, which net/http puts in a *url.Error.
func cause(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return ue.Err
	}
	return err
}

// readObject decodes the body as a JSON object. Anything else, null included, is
// an answer that hub does not send.
func readObject(body []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(body, &object); err != nil || object == nil {
		return nil, &UnavailableError{Status: http.StatusOK, Err: errors.New("the answer is not a JSON object")}
	}
	return object, nil
}

// readUser reads hub's user object. The id is the field "id", a string or a
// number that is kept as written, and it must not be empty. The login is
// "login", else hub's "username", and is left empty when neither is a string.
func readUser(body []byte) (User, error) {
	object, err := readObject(body)
	if err != nil {
		return User{}, err
	}
	var u User
	if u.ID = identifier(object["id"]); u.ID == "" {
		return User{}, &UnavailableError{Status: http.StatusOK, Err: errors.New("the user has no id")}
	}
	for _, field := range []string{"login", "username"} {
		var login string
		if json.Unmarshal(object[field], &login) == nil && login != "" {
			u.Login = login
			break
		}
	}
	return u, nil
}

// identifier is a JSON string, or a JSON number as it was written, and empty for
// anything else.
func identifier(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return ""
		}
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) != nil {
		return ""
	}
	return n.String()
}
