package issuex

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// MaxResponseBodyBytes is the maximum number of bytes read from an HTTP response
// body by the shared transport safety utilities (8 MB = 8388608 bytes).
const MaxResponseBodyBytes = 8 << 20

// MaxResponseBytes is an alias for MaxResponseBodyBytes.
const MaxResponseBytes = MaxResponseBodyBytes

// LimitResponseBody wraps an io.Reader in an io.LimitReader bounded to MaxResponseBodyBytes (8 MB).
func LimitResponseBody(r io.Reader) io.Reader {
	return io.LimitReader(r, MaxResponseBodyBytes)
}

// ReadResponseBody reads from r bounded to MaxResponseBodyBytes (8 MB).
func ReadResponseBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	return io.ReadAll(LimitResponseBody(r))
}

// ReadResponse reads resp.Body bounded to MaxResponseBodyBytes (8 MB) and closes resp.Body.
func ReadResponse(resp *http.Response) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer resp.Body.Close()
	return ReadResponseBody(resp.Body)
}

// Transport wraps an HTTP round-tripper with rate-limiting retry backoff,
// bounded response reading safety, and error handling.
type Transport struct {
	// Base is the underlying RoundTripper. If nil, http.DefaultTransport is used.
	Base http.RoundTripper

	// Sleep is the sleep function invoked for rate-limiting backoffs.
	// If nil, time.Sleep is used.
	Sleep func(time.Duration)

	// Authenticated indicates whether the client using this transport is authenticated.
	Authenticated bool
}

// RoundTrip implements http.RoundTripper, delegating to ExecuteRequest.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.ExecuteRequest(req)
}

// ExecuteRequest executes req using the underlying transport. If the response indicates
// rate limiting (HTTP 429 or HTTP 403 with quota exhausted):
//   - If the calculated RetryAfter duration <= 120 seconds, it invokes the configured
//     sleep function and retries the request once.
//   - If the calculated RetryAfter duration > 120 seconds, it aborts immediately and returns
//     an error wrapping ErrRateLimited without retrying.
//   - If a retry attempt also encounters rate limiting, it returns an error wrapping ErrRateLimited.
func (t *Transport) ExecuteRequest(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	sleepFn := t.Sleep
	if sleepFn == nil {
		sleepFn = time.Sleep
	}

	// Preserve request body for retry if body exists and GetBody is not yet populated.
	if req.Body != nil && req.GetBody == nil {
		bodyBytes, err := ReadResponseBody(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(bodyBytes)), nil
		}
	}

	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	httpErr := HTTPErrorFromResponse(resp)
	wait, isRateLimit := httpErr.RetryAfter()
	if !isRateLimit {
		return resp, nil
	}

	// RetryAfter duration exceeds 120 seconds: abort without retrying.
	if wait > 120*time.Second {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil, fmt.Errorf("%w: rate limit backoff %v exceeds 120 seconds: %w", ErrRateLimited, wait, httpErr)
	}

	// Close the initial response body before waiting and retrying.
	if resp.Body != nil {
		_ = resp.Body.Close()
	}

	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	default:
	}

	sleepFn(wait)

	select {
	case <-req.Context().Done():
		return nil, req.Context().Err()
	default:
	}

	if req.GetBody != nil {
		newBody, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		req.Body = newBody
	}

	// Retry once.
	retryResp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	retryErr := HTTPErrorFromResponse(retryResp)
	if _, retryIsRateLimit := retryErr.RetryAfter(); retryIsRateLimit {
		if retryResp.Body != nil {
			_ = retryResp.Body.Close()
		}
		return nil, fmt.Errorf("%w: rate limit retries exhausted: %w", ErrRateLimited, retryErr)
	}

	return retryResp, nil
}

// HandleResponse inspects an HTTP response using the transport's Authenticated setting.
func (t *Transport) HandleResponse(resp *http.Response) error {
	return HandleResponse(resp, t.Authenticated)
}

// HTTPErrorFromResponse constructs an HTTPError from an http.Response.
func HTTPErrorFromResponse(resp *http.Response) *HTTPError {
	if resp == nil {
		return nil
	}
	var method, path string
	if resp.Request != nil {
		method = resp.Request.Method
		if resp.Request.URL != nil {
			path = resp.Request.URL.Path
		}
	}
	msg := resp.Status
	if msg == "" {
		msg = http.StatusText(resp.StatusCode)
	}
	return &HTTPError{
		Method:             method,
		Path:               path,
		Status:             resp.StatusCode,
		Message:            msg,
		RetryAfterHeader:   getHeader(resp.Header, "Retry-After"),
		RateLimitReset:     getHeader(resp.Header, "X-RateLimit-Reset"),
		RateLimitRemaining: getHeader(resp.Header, "X-RateLimit-Remaining"),
	}
}

// getHeader retrieves a header value by key, first via h.Get and falling back
// to case-insensitive matching for non-canonical map keys.
func getHeader(h http.Header, key string) string {
	if h == nil {
		return ""
	}
	if v := h.Get(key); v != "" {
		return v
	}
	for k, vv := range h {
		if strings.EqualFold(k, key) && len(vv) > 0 {
			return vv[0]
		}
	}
	return ""
}

// HandleResponse inspects an HTTP response and client authentication state.
// If the status indicates an error (status >= 400), it returns a corresponding typed error:
//   - 404 on unauthenticated client returns an error wrapping ErrNotFound with guidance that
//     the resource may be private and require credentials.
//   - 404 on authenticated client returns an error wrapping ErrNotFound.
//   - 409 returns an error wrapping ErrConflict.
//   - 429 or 403 (with exhausted quota) returns an error wrapping ErrRateLimited.
//   - other >= 400 statuses return the HTTPError.
//
// For successful responses (status < 400), it returns nil.
func HandleResponse(resp *http.Response, authenticated bool) error {
	if resp == nil || resp.StatusCode < 400 {
		return nil
	}
	httpErr := HTTPErrorFromResponse(resp)
	if resp.StatusCode == http.StatusNotFound {
		if !authenticated {
			return fmt.Errorf("%w: resource may be private and require credentials: %w", ErrNotFound, httpErr)
		}
		return fmt.Errorf("%w: %w", ErrNotFound, httpErr)
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("%w: %w", ErrConflict, httpErr)
	}
	if resp.StatusCode == http.StatusTooManyRequests ||
		(resp.StatusCode == http.StatusForbidden && strings.TrimSpace(getHeader(resp.Header, "X-RateLimit-Remaining")) == "0") {
		return fmt.Errorf("%w: %w", ErrRateLimited, httpErr)
	}
	return httpErr
}

// HandleResponseStatus evaluates an HTTP status code and client authentication state.
func HandleResponseStatus(statusCode int, authenticated bool) error {
	resp := &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
	}
	return HandleResponse(resp, authenticated)
}
