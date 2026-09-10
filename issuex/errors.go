package issuex

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Sentinel errors defined by the issuex package.
var (
	// ErrNoToken indicates that an operation requiring authentication was attempted without credentials.
	ErrNoToken = errors.New("no authentication token configured")

	// ErrNotFound indicates that the requested repository, issue, or pull request was not found.
	ErrNotFound = errors.New("resource not found")

	// ErrConflict indicates a state conflict, such as a merge conflict or duplicate label.
	ErrConflict = errors.New("conflict")

	// ErrRateLimited indicates that the forge rate limit has been exceeded and retries were exhausted or backoff was too long.
	ErrRateLimited = errors.New("rate limited")

	// ErrUnsupportedForge indicates that the requested or detected forge type is not supported.
	ErrUnsupportedForge = errors.New("unsupported forge")

	// ErrAmbiguousForge indicates that the forge type could not be determined from configuration or environment.
	ErrAmbiguousForge = errors.New("ambiguous forge")
)

// HTTPError represents an HTTP error response returned by a forge API.
type HTTPError struct {
	Method             string
	Path               string
	Status             int
	Message            string
	RetryAfterHeader   string
	RateLimitReset     string
	RateLimitRemaining string
}

// Error formats the HTTPError as "<Method> <Path>: <Status> <Message>".
func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.Method, e.Path, e.Status, e.Message)
}

// RetryAfter calculates the backoff duration when status is 429 or status is 403
// with RateLimitRemaining "0". It inspects numeric seconds in Retry-After header
// or epoch timestamp in X-RateLimit-Reset header, adds a 1-second buffer, and
// returns the duration with true. If not rate limited or headers are missing,
// it returns (0, false).
func (e *HTTPError) RetryAfter() (time.Duration, bool) {
	if e == nil {
		return 0, false
	}
	if e.Status != http.StatusTooManyRequests &&
		!(e.Status == http.StatusForbidden && strings.TrimSpace(e.RateLimitRemaining) == "0") {
		return 0, false
	}

	if s := strings.TrimSpace(e.RetryAfterHeader); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n >= 0 {
			return time.Duration(n)*time.Second + time.Second, true
		}
	}

	if s := strings.TrimSpace(e.RateLimitReset); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			d := time.Until(time.Unix(n, 0))
			if d > 0 {
				return d + time.Second, true
			}
			return time.Second, true
		}
	}

	return 0, false
}

// IsNotFound returns true if the error wraps ErrNotFound or is an HTTPError with status 404,
// and false otherwise.
func IsNotFound(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		return true
	}
	return false
}

// IsConflict returns true if the error wraps ErrConflict or is an HTTPError with status 409,
// and false otherwise.
func IsConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrConflict) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) && he.Status == http.StatusConflict {
		return true
	}
	return false
}

// IsRateLimited returns true if the error wraps ErrRateLimited or is an HTTPError with status 429
// or status 403 with RateLimitRemaining "0", and false otherwise.
func IsRateLimited(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRateLimited) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		if he.Status == http.StatusTooManyRequests {
			return true
		}
		if he.Status == http.StatusForbidden && strings.TrimSpace(he.RateLimitRemaining) == "0" {
			return true
		}
	}
	return false
}

// IsNoToken returns true if the error wraps ErrNoToken, and false otherwise.
func IsNoToken(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, ErrNoToken)
}
