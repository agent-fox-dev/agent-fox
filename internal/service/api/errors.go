// Package api is the HTTP surface of the agent-fox service: the probes, the
// error envelope, the reading of request bodies, the recovery from panics and
// the log of requests, and (in the files that follow) the authentication of
// callers against hub and the subscription of workspaces.
//
// Every error is application/json with the body {"error":{"code":…,"message":…}},
// the envelope of apikit. Its optional error_type is never sent in this release.
package api

import (
	"encoding/json"
	"net/http"
)

// Code is the machine-readable code of an error answer.
type Code string

// The codes of the error envelope, and, in statuses, the status of each.
const (
	CodeInvalidRequest   Code = "invalid_request"
	CodeUnauthenticated  Code = "unauthenticated"
	CodeNotFound         Code = "not_found"
	CodeMethodNotAllowed Code = "method_not_allowed"
	CodePayloadTooLarge  Code = "payload_too_large"
	CodeUnsupportedMedia Code = "unsupported_media_type"
	CodeInvalidHubToken  Code = "invalid_hub_token"
	CodeInvalidState     Code = "invalid_state"
	CodeDuplicateJob     Code = "duplicate_job"
	CodeNotSubscribed    Code = "not_subscribed"
	CodeTokenUnreadable  Code = "token_unreadable"
	CodeInternal         Code = "internal"
	CodeHubUnavailable   Code = "hub_unavailable"
	CodeNotReady         Code = "not_ready"
)

var statuses = map[Code]int{
	CodeInvalidRequest:   http.StatusBadRequest,
	CodeUnauthenticated:  http.StatusUnauthorized,
	CodeNotFound:         http.StatusNotFound,
	CodeMethodNotAllowed: http.StatusMethodNotAllowed,
	CodePayloadTooLarge:  http.StatusRequestEntityTooLarge,
	CodeUnsupportedMedia: http.StatusUnsupportedMediaType,
	CodeInvalidHubToken:  http.StatusUnprocessableEntity,
	CodeInvalidState:     http.StatusConflict,
	CodeDuplicateJob:     http.StatusConflict,
	CodeNotSubscribed:    http.StatusConflict,
	CodeTokenUnreadable:  http.StatusConflict,
	CodeInternal:         http.StatusInternalServerError,
	CodeHubUnavailable:   http.StatusBadGateway,
	CodeNotReady:         http.StatusServiceUnavailable,
}

// Status is the HTTP status of the code; a code that is not one of the table is
// an internal error.
func (c Code) Status() int {
	if status, ok := statuses[c]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// errorBody is the envelope. It has no error_type.
type errorBody struct {
	Error struct {
		Code    Code   `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// writeError answers with the error envelope and the status of the code. The
// message says what is wrong, for the person who made the request; it never
// holds a token, a ciphertext or the text of an internal error.
func writeError(w http.ResponseWriter, code Code, message string) {
	var body errorBody
	body.Error.Code, body.Error.Message = code, message
	writeJSON(w, code.Status(), body)
}

// writeJSON answers with a JSON document.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
