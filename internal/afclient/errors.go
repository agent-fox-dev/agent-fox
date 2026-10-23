package afclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"syscall"
)

// The categories of an Error. They are the strings of the categories that the commands give
// the failures of the same names.
const (
	CategoryUsage    = "usage"
	CategoryAuth     = "auth"
	CategoryConflict = "conflict"
	CategoryHub      = "hub"
	CategoryService  = "service"
)

// The codes of the service's error object that a hint is made for.
const (
	codeNotSubscribed   = "not_subscribed"
	codeTokenUnreadable = "token_unreadable"
)

// slugPlaceholder stands for the slug in the hint of a failure on a job route, which does not
// name the workspace.
const slugPlaceholder = "<slug>"

// Hint names what would likely fix a failure, as the fix_hint of an envelope does.
type Hint struct {
	Flag    string
	Env     string
	Suggest string
}

// Error is a request that did not end in a 2xx answer.
type Error struct {
	// Status is the HTTP status of the answer, or 0 when there was none.
	Status int
	// Code is the code of the service's error object, or "".
	Code string
	// Message is what a person reads.
	Message string
	// Category is one of the Category constants.
	Category  string
	Retryable bool
	Hint      *Hint
}

func (e *Error) Error() string { return e.Message }

// answerError is the error for a non-2xx answer of the service to a request for path (the path
// of the API, as it was given to Do): its category and retryable flag follow the status, its
// message is the service's, and its hint follows the answer and the route.
func answerError(status int, body []byte, path string) *Error {
	var doc struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	// A body that is not the error object of the service is "the service answered <status>".
	_ = json.Unmarshal(body, &doc)
	e := &Error{Status: status, Code: doc.Error.Code, Message: doc.Error.Message}
	if e.Message == "" {
		e.Message = fmt.Sprintf("the service answered %d", status)
	}
	if e.Code != "" {
		e.Message += " (" + e.Code + ")"
	}
	kind, name := askedFor(path)

	switch {
	case status == http.StatusBadRequest || status == http.StatusNotFound:
		e.Category = CategoryUsage
	case status == http.StatusUnauthorized || status == http.StatusUnprocessableEntity:
		e.Category = CategoryAuth
	case status == http.StatusConflict:
		e.Category = CategoryConflict
	case status == http.StatusBadGateway:
		e.Category, e.Retryable = CategoryHub, true
	case status == http.StatusInternalServerError:
		e.Category = CategoryService
	case status >= 500:
		e.Category, e.Retryable = CategoryService, true
	default: // a 3xx, which is never followed, and any other 4xx
		e.Category = CategoryService
	}

	switch {
	case status == http.StatusUnauthorized:
		e.Hint = &Hint{Suggest: "af login --token-file FILE"}
	case status == http.StatusUnprocessableEntity:
		e.Hint = &Hint{Flag: "--hub-token-file"}
	case status == http.StatusNotFound && kind != "":
		e.Message = fmt.Sprintf("the %s %q does not exist, or hub does not let you see it: %s", kind, name, e.Message)
		if kind == "workspace" {
			e.Hint = subscribeHint(name)
		}
	case status == http.StatusConflict && (e.Code == codeNotSubscribed || e.Code == codeTokenUnreadable):
		if kind != "workspace" {
			name = slugPlaceholder
		}
		e.Hint = subscribeHint(name)
	}
	return e
}

// subscribeHint is the hint to subscribe the workspace slug.
func subscribeHint(slug string) *Hint {
	return &Hint{Suggest: "af workspace subscribe " + slug + " --hub-token-file FILE"}
}

// askedFor says what the path of the API asks for: a "job" and its id, a "workspace" and its
// slug, or nothing.
func askedFor(path string) (kind, name string) {
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(segments) < 2 || segments[1] == "" {
		return "", ""
	}
	switch segments[0] {
	case "jobs":
		kind = "job"
	case "workspaces":
		kind = "workspace"
	default:
		return "", ""
	}
	if name, err := url.PathUnescape(segments[1]); err == nil {
		return kind, name
	}
	return kind, segments[1]
}

// transportError is the error for a request that got no answer, naming the server and what
// gave it.
func transportError(server Server, err error) *Error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	cause := err.Error()
	if errors.Is(err, syscall.ECONNREFUSED) {
		cause = "nothing is listening there"
	}
	return &Error{
		Category: CategoryService, Retryable: true,
		Message: fmt.Sprintf("the service at %s (given by %s) cannot be reached: %s", server.URL, server.Source, cause),
	}
}
