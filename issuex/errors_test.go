package issuex_test

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/issuex"
)

// TestSentinelErrorsAndHTTPErrorFormatting_TS_01_20 verifies TS-01-20:
// Sentinel errors and HTTPError string formatting conform to specification.
// Verifies: 01-REQ-5.1, 01-REQ-5.2
func TestSentinelErrorsAndHTTPErrorFormatting_TS_01_20(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
	}{
		{"ErrNoToken", issuex.ErrNoToken},
		{"ErrNotFound", issuex.ErrNotFound},
		{"ErrConflict", issuex.ErrConflict},
		{"ErrRateLimited", issuex.ErrRateLimited},
		{"ErrUnsupportedForge", issuex.ErrUnsupportedForge},
		{"ErrAmbiguousForge", issuex.ErrAmbiguousForge},
	}

	for _, s := range sentinels {
		if s.err == nil {
			t.Errorf("sentinel %s is nil", s.name)
		}
	}

	// Verify all six sentinel errors are distinct
	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i].err, sentinels[j].err) {
				t.Errorf("sentinels %s and %s must be distinct", sentinels[i].name, sentinels[j].name)
			}
		}
	}

	// Verify HTTPError formatting matches fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
	httpErr := &issuex.HTTPError{
		Method:  "GET",
		Path:    "/repos",
		Status:  404,
		Message: "Not Found",
	}
	expected := "GET /repos: 404 Not Found"
	if got := httpErr.Error(); got != expected {
		t.Errorf("HTTPError.Error() = %q, want %q", got, expected)
	}

	// Verify fields exist on HTTPError
	httpErrFull := &issuex.HTTPError{
		Method:             "POST",
		Path:               "/api/v4/projects",
		Status:             429,
		Message:            "Too Many Requests",
		RetryAfterHeader:   "30",
		RateLimitReset:     "1700000000",
		RateLimitRemaining: "0",
	}
	if httpErrFull.RetryAfterHeader != "30" || httpErrFull.RateLimitReset != "1700000000" || httpErrFull.RateLimitRemaining != "0" {
		t.Errorf("HTTPError fields not assigned properly: %+v", httpErrFull)
	}
}

// TestHTTPErrorRetryAfter_TS_01_21 verifies TS-01-21:
// HTTPError RetryAfter calculates backoff with 1s buffer and returns zero false otherwise.
// Verifies: 01-REQ-5.3, 01-REQ-5.4
func TestHTTPErrorRetryAfter_TS_01_21(t *testing.T) {
	// Status 429 with Retry-After header: backoff = 30s + 1s buffer = 31s
	err429 := &issuex.HTTPError{
		Status:           http.StatusTooManyRequests,
		RetryAfterHeader: "30",
	}
	d, ok := err429.RetryAfter()
	if !ok {
		t.Fatalf("expected ok == true for 429 with Retry-After")
	}
	if d != 31*time.Second {
		t.Errorf("expected 31s backoff, got %v", d)
	}

	// Status 403 with RateLimitRemaining == "0" and RateLimitReset epoch in the future
	resetTime := time.Now().Add(10 * time.Second)
	err403 := &issuex.HTTPError{
		Status:             http.StatusForbidden,
		RateLimitRemaining: "0",
		RateLimitReset:     strconv.FormatInt(resetTime.Unix(), 10),
	}
	d2, ok2 := err403.RetryAfter()
	if !ok2 {
		t.Fatalf("expected ok == true for 403 with remaining 0 and reset header")
	}
	// Allow small timing margin: between 9s and 12s
	if d2 < 9*time.Second || d2 > 12*time.Second {
		t.Errorf("expected backoff ~11s, got %v", d2)
	}

	// Status 403 with RateLimitRemaining == "0" and RateLimitReset in the past: minimum 1s buffer
	pastReset := time.Now().Add(-10 * time.Second)
	errPast := &issuex.HTTPError{
		Status:             http.StatusForbidden,
		RateLimitRemaining: "0",
		RateLimitReset:     strconv.FormatInt(pastReset.Unix(), 10),
	}
	dPast, okPast := errPast.RetryAfter()
	if !okPast {
		t.Fatalf("expected ok == true for past reset with remaining 0")
	}
	if dPast != 1*time.Second {
		t.Errorf("expected 1s buffer for past reset, got %v", dPast)
	}

	// Status 429 with RateLimitReset header when RetryAfterHeader is empty
	err429Reset := &issuex.HTTPError{
		Status:         http.StatusTooManyRequests,
		RateLimitReset: strconv.FormatInt(resetTime.Unix(), 10),
	}
	d3, ok3 := err429Reset.RetryAfter()
	if !ok3 || d3 < 9*time.Second {
		t.Errorf("expected ok == true and positive backoff for 429 with RateLimitReset, got ok=%v, d=%v", ok3, d3)
	}

	// Status 403 with RateLimitRemaining == "0" and RetryAfterHeader
	err403RetryAfter := &issuex.HTTPError{
		Status:             http.StatusForbidden,
		RateLimitRemaining: "0",
		RetryAfterHeader:   "15",
	}
	d403, ok403 := err403RetryAfter.RetryAfter()
	if !ok403 || d403 != 16*time.Second {
		t.Errorf("expected ok == true and 16s backoff for 403 with RetryAfterHeader, got ok=%v, d=%v", ok403, d403)
	}

	// Non-rate-limit status (e.g. 500)
	err500 := &issuex.HTTPError{
		Status:           http.StatusInternalServerError,
		RetryAfterHeader: "30",
	}
	d4, ok4 := err500.RetryAfter()
	if ok4 || d4 != 0 {
		t.Errorf("expected ok == false and d == 0 for status 500, got ok=%v, d=%v", ok4, d4)
	}

	// Status 403 with RateLimitRemaining != "0" (e.g. "5" or "")
	err403NotZero := &issuex.HTTPError{
		Status:             http.StatusForbidden,
		RateLimitRemaining: "5",
		RetryAfterHeader:   "30",
	}
	d5, ok5 := err403NotZero.RetryAfter()
	if ok5 || d5 != 0 {
		t.Errorf("expected ok == false and d == 0 for 403 with remaining > 0, got ok=%v, d=%v", ok5, d5)
	}

	// Status 429 without any rate limit headers
	err429NoHeaders := &issuex.HTTPError{
		Status: http.StatusTooManyRequests,
	}
	d6, ok6 := err429NoHeaders.RetryAfter()
	if ok6 || d6 != 0 {
		t.Errorf("expected ok == false and d == 0 for 429 with no headers, got ok=%v, d=%v", ok6, d6)
	}

	// Status 429 with invalid RetryAfterHeader
	errInvalid := &issuex.HTTPError{
		Status:           http.StatusTooManyRequests,
		RetryAfterHeader: "not-a-number",
	}
	d7, ok7 := errInvalid.RetryAfter()
	if ok7 || d7 != 0 {
		t.Errorf("expected ok == false and d == 0 for invalid header, got ok=%v, d=%v", ok7, d7)
	}

	// Nil HTTPError receiver
	var nilErr *issuex.HTTPError
	dNil, okNil := nilErr.RetryAfter()
	if okNil || dNil != 0 {
		t.Errorf("expected ok == false and d == 0 for nil receiver, got ok=%v, d=%v", okNil, dNil)
	}
}

// TestErrorClassifiers_TS_01_22 verifies TS-01-22:
// Error classifiers correctly identify not-found, conflict, rate-limit, and no-token conditions.
// Verifies: 01-REQ-5.5, 01-REQ-5.6, 01-REQ-5.7, 01-REQ-5.8
func TestErrorClassifiers_TS_01_22(t *testing.T) {
	// IsNotFound
	if !issuex.IsNotFound(issuex.ErrNotFound) {
		t.Errorf("IsNotFound(ErrNotFound) = false, want true")
	}
	wrappedNotFound := fmt.Errorf("wrapped: %w", issuex.ErrNotFound)
	if !issuex.IsNotFound(wrappedNotFound) {
		t.Errorf("IsNotFound(wrappedNotFound) = false, want true")
	}
	if !issuex.IsNotFound(&issuex.HTTPError{Status: 404}) {
		t.Errorf("IsNotFound(HTTPError{Status: 404}) = false, want true")
	}
	wrappedHTTP404 := fmt.Errorf("api error: %w", &issuex.HTTPError{Status: 404})
	if !issuex.IsNotFound(wrappedHTTP404) {
		t.Errorf("IsNotFound(wrappedHTTP404) = false, want true")
	}
	if issuex.IsNotFound(errors.New("some other error")) {
		t.Errorf("IsNotFound(other) = true, want false")
	}
	if issuex.IsNotFound(nil) {
		t.Errorf("IsNotFound(nil) = true, want false")
	}
	if issuex.IsNotFound(&issuex.HTTPError{Status: 500}) {
		t.Errorf("IsNotFound(HTTPError{Status: 500}) = true, want false")
	}

	// IsConflict
	if !issuex.IsConflict(issuex.ErrConflict) {
		t.Errorf("IsConflict(ErrConflict) = false, want true")
	}
	wrappedConflict := fmt.Errorf("wrapped: %w", issuex.ErrConflict)
	if !issuex.IsConflict(wrappedConflict) {
		t.Errorf("IsConflict(wrappedConflict) = false, want true")
	}
	if !issuex.IsConflict(&issuex.HTTPError{Status: 409}) {
		t.Errorf("IsConflict(HTTPError{Status: 409}) = false, want true")
	}
	wrappedHTTP409 := fmt.Errorf("api error: %w", &issuex.HTTPError{Status: 409})
	if !issuex.IsConflict(wrappedHTTP409) {
		t.Errorf("IsConflict(wrappedHTTP409) = false, want true")
	}
	if issuex.IsConflict(errors.New("other")) {
		t.Errorf("IsConflict(other) = true, want false")
	}
	if issuex.IsConflict(nil) {
		t.Errorf("IsConflict(nil) = true, want false")
	}

	// IsRateLimited
	if !issuex.IsRateLimited(issuex.ErrRateLimited) {
		t.Errorf("IsRateLimited(ErrRateLimited) = false, want true")
	}
	wrappedRateLimited := fmt.Errorf("wrapped: %w", issuex.ErrRateLimited)
	if !issuex.IsRateLimited(wrappedRateLimited) {
		t.Errorf("IsRateLimited(wrappedRateLimited) = false, want true")
	}
	if !issuex.IsRateLimited(&issuex.HTTPError{Status: 429}) {
		t.Errorf("IsRateLimited(HTTPError{Status: 429}) = false, want true")
	}
	if !issuex.IsRateLimited(&issuex.HTTPError{Status: 403, RateLimitRemaining: "0"}) {
		t.Errorf("IsRateLimited(HTTPError{Status: 403, RateLimitRemaining: 0}) = false, want true")
	}
	if issuex.IsRateLimited(&issuex.HTTPError{Status: 403, RateLimitRemaining: "1"}) {
		t.Errorf("IsRateLimited(HTTPError{Status: 403, RateLimitRemaining: 1}) = true, want false")
	}
	if issuex.IsRateLimited(&issuex.HTTPError{Status: 403}) {
		t.Errorf("IsRateLimited(HTTPError{Status: 403}) = true, want false")
	}
	if issuex.IsRateLimited(errors.New("other")) {
		t.Errorf("IsRateLimited(other) = true, want false")
	}
	if issuex.IsRateLimited(nil) {
		t.Errorf("IsRateLimited(nil) = true, want false")
	}

	// IsNoToken
	if !issuex.IsNoToken(issuex.ErrNoToken) {
		t.Errorf("IsNoToken(ErrNoToken) = false, want true")
	}
	wrappedNoToken := fmt.Errorf("wrapped: %w", issuex.ErrNoToken)
	if !issuex.IsNoToken(wrappedNoToken) {
		t.Errorf("IsNoToken(wrappedNoToken) = false, want true")
	}
	if issuex.IsNoToken(errors.New("other")) {
		t.Errorf("IsNoToken(other) = true, want false")
	}
	if issuex.IsNoToken(nil) {
		t.Errorf("IsNoToken(nil) = true, want false")
	}
}
