package poll

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub"
)

// row is the row of the table of failures that a failure falls in: two failures of a workspace are the same when they
// fall in the same row and concern the same operation.
type row string

const (
	// rowUnavailable: hub cannot be asked, or answers nothing about the request: 429, 5xx, a transport error, a timeout,
	// or a body that is not what hub sends. The cycle ends with a WARN.
	rowUnavailable row = "unavailable"
	// rowDenied: hub refuses the token (401) or forbids the operation (403) on any call, or does not find the workspace
	// (404) when it is asked for its record, its variables or a list. The cycle ends with an ERROR.
	rowDenied row = "denied"
	// rowRejected: hub rejects a read, or a list, with another 4xx, a 409 included. The cycle ends with an ERROR.
	rowRejected row = "rejected"
	// rowRemoval: hub rejects the removal of a label with another 4xx, a 409 included. The removal stays owed and the
	// cycle goes on with the next issue.
	rowRemoval row = "removal"
	// rowToken: the token of the subscription does not open. No request is made of hub.
	rowToken row = "token"
	// rowJobs: the jobs layer refuses to make a job for a reason other than that the workspace is not subscribed or the
	// issue has a job.
	rowJobs row = "jobs"
	// rowStore: the store cannot be read.
	rowStore row = "store"
)

// failure is what went wrong in a cycle.
type failure struct {
	row row
	// operation names what was asked: "read the workspace record", "list the issues of af:fix", "remove the label af:fix
	// from the issue 42".
	operation string
	err       error
	// status is the status hub answered with, 0 when it did not.
	status int
	// message is what hub said, when it rejected the request.
	message string
}

// classify turns the error of a call of hub, of the hub client or of the forge client behind it, into the failure it is.
// removal says that the call was the removal of a label, which hub's 404 does not fail.
func classify(operation string, err error, removal bool) failure {
	f := failure{row: rowUnavailable, operation: operation, err: err}
	var (
		httpErr  *issuex.HTTPError
		rejected *hub.RejectedError
	)
	switch {
	case errors.Is(err, hub.ErrRefused):
		f.row, f.status = rowDenied, http.StatusUnauthorized
	case errors.Is(err, hub.ErrForbidden):
		f.row, f.status = rowDenied, http.StatusForbidden
	case errors.Is(err, hub.ErrNotFound):
		f.row, f.status = rowDenied, http.StatusNotFound
	case errors.As(err, &httpErr) && (httpErr.Status == http.StatusUnauthorized || httpErr.Status == http.StatusForbidden):
		f.row, f.status = rowDenied, httpErr.Status
	case errors.As(err, &httpErr):
		// The forge client gives an *issuex.HTTPError for what hub rejected; hub being unavailable is none.
		f.row, f.status, f.message = rowRejected, httpErr.Status, httpErr.Message
	case errors.As(err, &rejected):
		f.row, f.status, f.message = rowRejected, rejected.Status, rejected.Message
	case errors.Is(err, issuex.ErrConflict):
		f.row, f.status, f.message = rowRejected, http.StatusConflict, err.Error()
	case issuex.IsNotFound(err):
		f.row, f.status = rowDenied, http.StatusNotFound
	}
	// A rejection of a removal leaves it owed and goes on with the next issue, since an issue of its own cannot make the
	// others fail; hub's 502 for a forge that refuses its credential (a rejection with a 5xx status) would fail them all.
	if removal && f.row == rowRejected && f.status < http.StatusInternalServerError {
		f.row = rowRemoval
	}
	return f
}

// level is the level of the record of a failure: a WARN for what may pass, and an ERROR for what a person must cure.
func (f failure) level() slog.Level {
	if f.row == rowUnavailable || f.row == rowJobs {
		return slog.LevelWarn
	}
	return slog.LevelError
}

// text is the message of the record of a failure: it names the operation, points to the route that replaces the token when
// that is the cure, and carries what hub said when it rejected the request.
func (f failure) text(slug string) string {
	switch f.row {
	case rowDenied:
		if f.status == http.StatusNotFound {
			return fmt.Sprintf("hub does not find the workspace when the poll asks it to %s", f.operation)
		}
		return fmt.Sprintf("hub does not accept the token of the workspace to %s; replace the token with PUT /api/v1/workspaces/%s", f.operation, slug)
	case rowRejected, rowRemoval:
		return fmt.Sprintf("hub rejected the request to %s: %s", f.operation, f.message)
	case rowToken:
		return "the token of the subscription does not open with the key, so no request is made of hub for the workspace"
	case rowUnavailable:
		return fmt.Sprintf("hub could not be asked to %s, so the cycle ends and the next runs one interval later", f.operation)
	}
	return fmt.Sprintf("the poll could not %s", f.operation)
}

// failureKey is what makes two failures of a workspace the same.
type failureKey struct {
	row       row
	operation string
}

// failures is what the poll remembers of the failures of a workspace: which have been logged since a cycle last
// completed without failure, and whether the cycle that is running has failed.
type failures struct {
	logged map[failureKey]bool
	failed bool
}

// begin starts a cycle.
func (f *failures) begin() { f.failed = false }

// report logs the failure, unless it is the same as one that was logged since a cycle last completed without failure, and
// says that the cycle failed. A cycle that was cancelled fails nothing: it says why itself.
func (s *Supervisor) report(ctx context.Context, slug string, st *wsState, f failure) {
	if ctx.Err() != nil {
		return
	}
	st.failures.failed = true
	key := failureKey{f.row, f.operation}
	if st.failures.logged[key] {
		return
	}
	if st.failures.logged == nil {
		st.failures.logged = map[failureKey]bool{}
	}
	st.failures.logged[key] = true
	attrs := []any{"workspace", slug, "operation", f.operation}
	if f.status != 0 {
		attrs = append(attrs, "status", f.status)
	}
	if f.err != nil {
		attrs = append(attrs, "error", f.err.Error())
	}
	s.cfg.Logger.Log(ctx, f.level(), f.text(slug), attrs...)
}

// completed notes that a cycle ended without a failure; if failures were logged before it, it says that the poll of the
// workspace has recovered, and the failures are logged again if they come back.
func (s *Supervisor) completed(slug string, st *wsState) {
	if st.failures.failed || len(st.failures.logged) == 0 {
		return
	}
	st.failures.logged = nil
	s.cfg.Logger.Info("the poll of the workspace has recovered: a cycle completed without a failure", "workspace", slug)
}
