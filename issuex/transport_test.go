package issuex_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

func handleResponse(statusCode int, authenticated bool) error {
	return issuex.HandleResponseStatus(statusCode, authenticated)
}

// TestSharedTransportBoundsResponseBody_TS_01_23 verifies TS-01-23:
// Shared transport bounds response body reads to a maximum of 8 MB (8388608 bytes).
// Verifies: 01-REQ-6.1
func TestSharedTransportBoundsResponseBody_TS_01_23(t *testing.T) {
	largeBody := make([]byte, 10<<20) // 10 MB
	for i := range largeBody {
		largeBody[i] = byte(i % 256)
	}

	readBytes, err := issuex.ReadResponseBody(bytes.NewReader(largeBody))
	if err != nil {
		t.Fatalf("unexpected error reading response body: %v", err)
	}
	if len(readBytes) != 8388608 {
		t.Fatalf("expected response body capped at 8388608 bytes, got %d", len(readBytes))
	}

	// Verify LimitResponseBody directly
	r := issuex.LimitResponseBody(bytes.NewReader(largeBody))
	limitRead, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error from LimitResponseBody: %v", err)
	}
	if len(limitRead) != 8388608 {
		t.Fatalf("expected LimitResponseBody capped at 8388608 bytes, got %d", len(limitRead))
	}

	// Small body reads completely
	smallBody := []byte("hello world")
	smallRead, err := issuex.ReadResponseBody(bytes.NewReader(smallBody))
	if err != nil {
		t.Fatalf("unexpected error reading small body: %v", err)
	}
	if string(smallRead) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", string(smallRead))
	}

	// Nil reader returns nil, nil
	nilRead, err := issuex.ReadResponseBody(nil)
	if err != nil || nilRead != nil {
		t.Fatalf("expected nil, nil for nil reader, got %v, %v", nilRead, err)
	}

	// ReadResponse closes body
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(smallBody)),
	}
	respRead, err := issuex.ReadResponse(resp)
	if err != nil {
		t.Fatalf("unexpected error reading response: %v", err)
	}
	if string(respRead) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", string(respRead))
	}
}

// TestSharedTransportAnnotates404Unauthenticated_TS_01_26 verifies TS-01-26:
// Shared transport annotates 404 on unauthenticated client with private resource guidance.
// Verifies: 01-REQ-6.4
func TestSharedTransportAnnotates404Unauthenticated_TS_01_26(t *testing.T) {
	err := handleResponse(404, false)
	if !issuex.IsNotFound(err) {
		t.Fatalf("expected IsNotFound(err) == true, got false for unauthenticated 404")
	}
	if !strings.Contains(err.Error(), "resource may be private") {
		t.Fatalf("expected error message to contain 'resource may be private', got: %q", err.Error())
	}

	// Authenticated 404 should satisfy IsNotFound but not contain "resource may be private"
	errAuth := handleResponse(404, true)
	if !issuex.IsNotFound(errAuth) {
		t.Fatalf("expected IsNotFound(errAuth) == true, got false for authenticated 404")
	}
	if strings.Contains(errAuth.Error(), "resource may be private") {
		t.Fatalf("expected authenticated 404 not to contain 'resource may be private', got: %q", errAuth.Error())
	}

	// Verify HandleResponse with http.Response objects
	resp404Unauth := &http.Response{
		StatusCode: http.StatusNotFound,
		Header:     make(http.Header),
	}
	errResp := issuex.HandleResponse(resp404Unauth, false)
	if !issuex.IsNotFound(errResp) || !strings.Contains(errResp.Error(), "resource may be private") {
		t.Fatalf("expected unauthenticated 404 guidance from HandleResponse, got: %v", errResp)
	}

	// Verify 200 returns nil
	resp200 := &http.Response{StatusCode: 200}
	if err := issuex.HandleResponse(resp200, false); err != nil {
		t.Fatalf("expected nil for 200 response, got: %v", err)
	}

	// Verify 409 returns IsConflict
	resp409 := &http.Response{StatusCode: 409, Header: make(http.Header)}
	errConflict := issuex.HandleResponse(resp409, true)
	if !issuex.IsConflict(errConflict) {
		t.Fatalf("expected IsConflict == true for 409, got: %v", errConflict)
	}

	// Verify 405 returns ErrConflict, as the adapters report it
	resp405 := &http.Response{StatusCode: 405, Header: make(http.Header)}
	if err405 := issuex.HandleResponse(resp405, true); !errors.Is(err405, issuex.ErrConflict) {
		t.Fatalf("expected ErrConflict for 405, got: %v", err405)
	}

	// Verify 429 returns IsRateLimited
	resp429 := &http.Response{StatusCode: 429, Header: make(http.Header)}
	errRate := issuex.HandleResponse(resp429, true)
	if !issuex.IsRateLimited(errRate) {
		t.Fatalf("expected IsRateLimited == true for 429, got: %v", errRate)
	}
}
