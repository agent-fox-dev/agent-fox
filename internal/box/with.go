package box

import (
	"context"
	"fmt"
)

// With opens a session, calls fn and closes the session however fn ends: a
// normal return, an error, a panic (which goes on after the close, with the
// value it had) and a context that is cancelled. The close runs on a context
// that is not cancelled with ctx and is bounded to 30 seconds. An error from
// it is the warning box_close_failed, and never changes what With returns.
func With(ctx context.Context, req Request, fn func(*Session) error) error {
	s, err := Open(ctx, req)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := s.closeWith(ctx); cerr != nil && req.Warn != nil {
			req.Warn("box_close_failed", fmt.Sprintf("the %s box could not be closed: %v", s.provider, cerr))
		}
	}()
	return fn(s)
}
