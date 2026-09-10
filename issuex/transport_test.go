package issuex_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestTransport is a mock transport for testing ExecuteRequest behavior.
type TestTransport struct {
	Sleep        func(time.Duration)
	Responses    []*http.Response
	RequestCount int
	Requests     []*http.Request
}

func (tt *TestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tt.RequestCount++
	tt.Requests = append(tt.Requests, req)
	if len(tt.Responses) == 0 {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    req,
		}, nil
	}
	resp := tt.Responses[0]
	tt.Responses = tt.Responses[1:]
	if resp.Header == nil {
		resp.Header = make(http.Header)
	}
	if resp.Body == nil {
		resp.Body = io.NopCloser(strings.NewReader(""))
	}
	if resp.Request == nil {
		resp.Request = req
	}
	return resp, nil
}

func (tt *TestTransport) ExecuteRequest(req *http.Request) (*http.Response, error) {
	t := &issuex.Transport{
		Base:  tt,
		Sleep: tt.Sleep,
	}
	return t.ExecuteRequest(req)
}

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

// TestSharedTransportRetryWithin120s_TS_01_24 verifies TS-01-24:
// Shared transport invokes injectable sleep and retries once for rate-limits within 120s.
// Verifies: 01-REQ-6.2, 01-REQ-6.5
func TestSharedTransportRetryWithin120s_TS_01_24(t *testing.T) {
	var slept time.Duration
	transport := &TestTransport{
		Sleep: func(d time.Duration) { slept = d },
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"10"}}},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))},
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.ExecuteRequest(req)
	if err != nil {
		t.Fatalf("expected nil error on retry success, got: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected status code 200, got: %d", resp.StatusCode)
	}
	if slept != 11*time.Second {
		t.Fatalf("expected slept == 11s (10s + 1s buffer), got: %v", slept)
	}
	if transport.RequestCount != 2 {
		t.Fatalf("expected 2 requests (1 original + 1 retry), got: %d", transport.RequestCount)
	}

	// Test boundary condition: Retry-After: 119s -> backoff is 120s <= 120s -> retried
	slept = 0
	boundaryTransport := &TestTransport{
		Sleep: func(d time.Duration) { slept = d },
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"119"}}},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))},
		},
	}
	boundaryReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	boundaryResp, err := boundaryTransport.ExecuteRequest(boundaryReq)
	if err != nil || boundaryResp.StatusCode != 200 {
		t.Fatalf("expected success on 120s backoff boundary, got resp=%v, err=%v", boundaryResp, err)
	}
	if slept != 120*time.Second {
		t.Fatalf("expected slept == 120s, got: %v", slept)
	}
	if boundaryTransport.RequestCount != 2 {
		t.Fatalf("expected 2 requests for 120s boundary, got: %d", boundaryTransport.RequestCount)
	}

	// Test 403 quota exhaustion with X-RateLimit-Reset within 120s
	slept = 0
	futureSec := time.Now().Add(5 * time.Second).Unix()
	quotaTransport := &TestTransport{
		Sleep: func(d time.Duration) { slept = d },
		Responses: []*http.Response{
			{
				StatusCode: 403,
				Header: http.Header{
					"X-RateLimit-Remaining": []string{"0"},
					"X-RateLimit-Reset":     []string{fmt.Sprint(futureSec)},
				},
			},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))},
		},
	}
	quotaReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	quotaResp, err := quotaTransport.ExecuteRequest(quotaReq)
	if err != nil || quotaResp.StatusCode != 200 {
		t.Fatalf("expected success on 403 quota retry, got resp=%v, err=%v", quotaResp, err)
	}
	if slept <= 0 {
		t.Fatalf("expected positive sleep for 403 quota exhaustion, got: %v", slept)
	}
	if quotaTransport.RequestCount != 2 {
		t.Fatalf("expected 2 requests for 403 quota exhaustion, got: %d", quotaTransport.RequestCount)
	}

	// Test request body preservation on retry
	reqBody := "{\"title\":\"sample\"}"
	bodyTransport := &TestTransport{
		Sleep: func(d time.Duration) {},
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"1"}}},
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))},
		},
	}
	bodyReq, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://api.example.com/test", strings.NewReader(reqBody))
	bodyResp, err := bodyTransport.ExecuteRequest(bodyReq)
	if err != nil || bodyResp.StatusCode != 200 {
		t.Fatalf("expected success on request with body, got err=%v", err)
	}
	if bodyTransport.RequestCount != 2 {
		t.Fatalf("expected 2 requests with body, got: %d", bodyTransport.RequestCount)
	}
	// Verify the retried request still had body readable
	retriedReqBody, err := io.ReadAll(bodyTransport.Requests[1].Body)
	if err != nil {
		t.Fatalf("failed reading body on retried request: %v", err)
	}
	if string(retriedReqBody) != reqBody {
		t.Fatalf("expected retried body %q, got %q", reqBody, string(retriedReqBody))
	}
}

// TestSharedTransportAbortsExceeding120s_TS_01_25 verifies TS-01-25:
// Shared transport aborts and returns ErrRateLimited when retry-after exceeds 120s.
// Verifies: 01-REQ-6.3
func TestSharedTransportAbortsExceeding120s_TS_01_25(t *testing.T) {
	var slept time.Duration
	transport := &TestTransport{
		Sleep: func(d time.Duration) { slept = d },
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"300"}}},
		},
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := transport.ExecuteRequest(req)
	if resp != nil {
		t.Fatalf("expected nil response on abort, got: %v", resp)
	}
	if err == nil {
		t.Fatal("expected error when retry-after > 120s, got nil")
	}
	if !errors.Is(err, issuex.ErrRateLimited) {
		t.Fatalf("expected errors.Is(err, ErrRateLimited) == true, got: %v", err)
	}
	if transport.RequestCount != 1 {
		t.Fatalf("expected exactly 1 request without retry, got: %d", transport.RequestCount)
	}
	if slept != 0 {
		t.Fatalf("expected no sleep on abort, slept: %v", slept)
	}

	// Boundary condition: Retry-After: 120s -> backoff is 121s > 120s -> aborts without retry
	boundaryTransport := &TestTransport{
		Sleep: func(d time.Duration) { slept = d },
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"120"}}},
		},
	}
	boundaryReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	_, err = boundaryTransport.ExecuteRequest(boundaryReq)
	if err == nil || !errors.Is(err, issuex.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited on 121s backoff, got: %v", err)
	}
	if boundaryTransport.RequestCount != 1 {
		t.Fatalf("expected exactly 1 request for 121s backoff, got: %d", boundaryTransport.RequestCount)
	}

	// Retries exhausted: first request 429 with 5s, retry also returns 429
	exhaustedTransport := &TestTransport{
		Sleep: func(d time.Duration) {},
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"5"}}},
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"5"}}},
		},
	}
	exhaustedReq, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.com/test", nil)
	_, err = exhaustedTransport.ExecuteRequest(exhaustedReq)
	if err == nil || !errors.Is(err, issuex.ErrRateLimited) {
		t.Fatalf("expected ErrRateLimited when retries exhausted, got: %v", err)
	}
	if exhaustedTransport.RequestCount != 2 {
		t.Fatalf("expected exactly 2 requests before exhausting retries, got: %d", exhaustedTransport.RequestCount)
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

	// Verify 429 returns IsRateLimited
	resp429 := &http.Response{StatusCode: 429, Header: make(http.Header)}
	errRate := issuex.HandleResponse(resp429, true)
	if !issuex.IsRateLimited(errRate) {
		t.Fatalf("expected IsRateLimited == true for 429, got: %v", errRate)
	}
}

// TestTransportRoundTripper verifies that Transport implements http.RoundTripper.
func TestTransportRoundTripper(t *testing.T) {
	mock := &TestTransport{
		Responses: []*http.Response{
			{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`ok`))},
		},
	}
	transport := &issuex.Transport{
		Base: mock,
	}
	client := &http.Client{Transport: transport}
	resp, err := client.Get("https://api.example.com/ping")
	if err != nil {
		t.Fatalf("unexpected error via http.Client: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
	if mock.RequestCount != 1 {
		t.Fatalf("expected 1 request, got %d", mock.RequestCount)
	}
}

// TestTransportContextCancellation verifies that canceled context aborts sleep.
func TestTransportContextCancellation(t *testing.T) {
	transport := &TestTransport{
		Sleep: func(d time.Duration) {},
		Responses: []*http.Response{
			{StatusCode: 429, Header: http.Header{"Retry-After": []string{"10"}}},
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.example.com/test", nil)
	_, err := transport.ExecuteRequest(req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}
