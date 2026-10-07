package issuex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// MaxResponseBodyBytes is the maximum number of bytes read from an HTTP response
// body by the shared transport safety utilities (8 MB = 8388608 bytes).
const MaxResponseBodyBytes = 8 << 20

// MaxResponseBytes is an alias for MaxResponseBodyBytes.
const MaxResponseBytes = MaxResponseBodyBytes

// maxRateLimitWait is the longest rate-limit backoff a request waits out; a
// longer one aborts the request instead.
const maxRateLimitWait = 120 * time.Second

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

// forgeHTTP is the one request loop every forge adapter runs on: the bounded
// body read, the single rate-limit retry with an injectable sleep, the status
// to sentinel mapping and the 404 annotation. What differs between forges is
// passed in: the headers, the error payload, and the statuses a forge reports
// as a conflict. An adapter builds it per request from its own fields, so a
// sleep set on the adapter later is the one used.
type forgeHTTP struct {
	baseURL string
	client  *http.Client
	// sleep waits out a rate-limit backoff. If nil, time.Sleep is used.
	sleep func(time.Duration)
	// authenticated reports whether the adapter holds a credential; an
	// unauthenticated 404 is annotated.
	authenticated bool
	// header sets the forge's headers, credential included, on a request.
	header func(*http.Request)
	// newError builds the forge's HTTPError from a response and its body,
	// reading the forge's error payload and rate-limit headers.
	newError func(method, path string, resp *http.Response, body []byte) *HTTPError
	// conflicts lists the statuses, beyond 405 and 409, the forge reports as
	// ErrConflict.
	conflicts []int
}

// do sends one request and returns the successful response, whose body has
// already been read (bounded to MaxResponseBodyBytes) and closed, with that
// body. A rate-limited response whose backoff is within 120 seconds is retried
// once after sleeping; a longer backoff, or a retry that is limited again, is
// an error wrapping ErrRateLimited. reqBody may be []byte, string, io.Reader
// or any value json.Marshal accepts; a 2xx body is decoded into respTarget,
// or written to it when it is an io.Writer.
func (h forgeHTTP) do(ctx context.Context, method, path string, reqBody, respTarget any) (*http.Response, []byte, error) {
	var bodyBytes []byte
	if reqBody != nil {
		switch v := reqBody.(type) {
		case []byte:
			if len(v) > 0 {
				bodyBytes = v
			}
		case string:
			if len(v) > 0 {
				bodyBytes = []byte(v)
			}
		case io.Reader:
			b, err := io.ReadAll(v)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %s: reading request body: %w", method, path, err)
			}
			if len(b) > 0 {
				bodyBytes = b
			}
		default:
			b, err := json.Marshal(v)
			if err != nil {
				return nil, nil, fmt.Errorf("%s %s: encoding request body: %w", method, path, err)
			}
			bodyBytes = b
		}
	}

	fullURL := path
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		p := path
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		fullURL = strings.TrimRight(h.baseURL, "/") + p
	}

	// The rate-limit wait (up to maxRateLimitWait) ends when the run is
	// cancelled. A sleep injected by a test is called as it is.
	sleep := h.sleep
	if sleep == nil {
		sleep = func(d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		var bodyReader io.Reader
		if len(bodyBytes) > 0 {
			bodyReader = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequestWithContext(ctx, method, fullURL, bodyReader)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
		}
		if h.header != nil {
			h.header(req)
		}
		if len(bodyBytes) > 0 {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := h.client.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
		}

		raw, err := ReadResponse(resp)
		if err != nil {
			return nil, nil, fmt.Errorf("%s %s: reading response: %w", method, path, err)
		}

		// 2xx response
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if respTarget != nil {
				if w, ok := respTarget.(io.Writer); ok {
					if _, err := w.Write(raw); err != nil {
						return nil, nil, fmt.Errorf("%s %s: writing response: %w", method, path, err)
					}
				} else {
					if err := json.Unmarshal(raw, respTarget); err != nil {
						return nil, nil, fmt.Errorf("%s %s: decoding response: %w", method, path, err)
					}
				}
			}
			return resp, raw, nil
		}

		httpErr := h.newError(method, path, resp, raw)

		// Rate limiting: wait out a short backoff and retry once.
		if wait, isRateLimit := httpErr.RetryAfter(); isRateLimit {
			if attempt == 0 && wait <= maxRateLimitWait {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				default:
				}
				sleep(wait)
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				default:
				}
				continue
			}
			return nil, nil, fmt.Errorf("%w: %w", ErrRateLimited, httpErr)
		}

		return nil, nil, classifyError(httpErr, h.authenticated, h.conflicts)
	}

	return nil, nil, nil
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

// classifyError maps an error response to the package's sentinel errors, the
// one mapping every adapter and HandleResponse share:
//   - 404 on an unauthenticated client wraps ErrNotFound with guidance that
//     the resource may be private and require credentials;
//   - 404 on an authenticated client wraps ErrNotFound;
//   - 405, 409 and the forge's own conflict statuses wrap ErrConflict;
//   - 429, or 403 with an exhausted quota, wraps ErrRateLimited;
//   - any other status is the HTTPError itself.
func classifyError(httpErr *HTTPError, authenticated bool, conflicts []int) error {
	switch {
	case httpErr.Status == http.StatusNotFound:
		if !authenticated {
			return fmt.Errorf("%w: resource may be private and require credentials: %w", ErrNotFound, httpErr)
		}
		return fmt.Errorf("%w: %w", ErrNotFound, httpErr)
	case httpErr.Status == http.StatusConflict || httpErr.Status == http.StatusMethodNotAllowed || slices.Contains(conflicts, httpErr.Status):
		return fmt.Errorf("%w: %w", ErrConflict, httpErr)
	case IsRateLimited(httpErr):
		return fmt.Errorf("%w: %w", ErrRateLimited, httpErr)
	}
	return httpErr
}

// HandleResponse inspects an HTTP response and client authentication state.
// If the status indicates an error (status >= 400), it returns the error
// classifyError describes; for successful responses (status < 400), it
// returns nil.
func HandleResponse(resp *http.Response, authenticated bool) error {
	if resp == nil || resp.StatusCode < 400 {
		return nil
	}
	return classifyError(HTTPErrorFromResponse(resp), authenticated, nil)
}

// HandleResponseStatus evaluates an HTTP status code and client authentication state.
func HandleResponseStatus(statusCode int, authenticated bool) error {
	resp := &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
	}
	return HandleResponse(resp, authenticated)
}
