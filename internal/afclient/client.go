package afclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// apiPrefix is where the API of the service is, under the server's URL.
const apiPrefix = "/api/v1"

// Client sends requests to one agent-fox service with one credential.
type Client struct {
	server    Server
	token     string
	userAgent string
	http      *http.Client
}

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

// Response is the answer of the service to a request that it answered with a 2xx status.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Error is a request that did not end in a 2xx answer.
type Error struct {
	// Status is the HTTP status of the answer, or 0 when there was none.
	Status  int
	Message string
}

func (e *Error) Error() string { return e.Message }

// Do sends method to the API path (which starts with "/", like "/workspaces"), with the query
// and with body encoded as JSON when it is not nil, and returns the answer when its status is
// 2xx. The request carries the credential, the User-Agent and the Accept header, and goes to
// the server's URL and to no other.
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
	req, err := http.NewRequestWithContext(ctx, method, target, payload)
	if err != nil {
		return nil, fmt.Errorf("the request cannot be made: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Message: fmt.Sprintf("the service at %s (%s) cannot be reached: %v", c.server.URL, c.server.Source, err)}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("the answer of the service at %s (%s) cannot be read: %v", c.server.URL, c.server.Source, err)}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &Error{Status: resp.StatusCode, Message: fmt.Sprintf("the service answered %d", resp.StatusCode)}
	}
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: data}, nil
}
