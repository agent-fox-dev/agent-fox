package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

// The largest body of each route that makes a job: at least six times its largest
// valid decoded body, which is the most a JSON encoder grows a byte (\u00XX), and
// room for the syntax. A triage is 128 KiB of text or file content and 32 KiB of
// context; a fix is a 2048 byte issue and 32 KiB of context.
const (
	triageBodyLimit = 1 << 20
	fixBodyLimit    = 256 << 10
)

// jobRoutes registers the routes of the jobs.
func (s *server) jobRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/workspaces/{slug}/triage", s.inWorkspace(s.createJob(jobs.KindTriage, triageBodyLimit)))
	mux.HandleFunc("POST /api/v1/workspaces/{slug}/fix", s.inWorkspace(s.createJob(jobs.KindFix, fixBodyLimit)))
	mux.HandleFunc("POST /api/v1/jobs/{id}/cancel", s.inJob(s.cancelJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/answer", s.inJob(s.answerJob))
	mux.HandleFunc("POST /api/v1/jobs/{id}/retry", s.inJob(s.retryJob))
	mux.HandleFunc("GET /api/v1/jobs", s.authed(s.listJobs))
	mux.HandleFunc("GET /api/v1/workspaces/{slug}/jobs", s.inWorkspace(s.listWorkspaceJobs))
	mux.HandleFunc("GET /api/v1/jobs/{id}", s.inJob(s.getJob))
	mux.HandleFunc("GET /api/v1/jobs/{id}/attempts/{n}/envelope", s.inJob(s.getEnvelope))
	mux.HandleFunc("GET /api/v1/jobs/{id}/attempts/{n}/report", s.inJob(s.getReport))
	mux.HandleFunc("GET /api/v1/jobs/{id}/events", s.inJob(s.streamEvents))
}

// writeJobNotFound answers 404 for a job. One answer serves a job that does not exist and
// one on a workspace that the caller cannot see, so that nobody learns from the answer
// that a job they may not see exists.
func writeJobNotFound(w http.ResponseWriter) { writeError(w, CodeNotFound, "job not found") }

// inJob wraps a handler of a route with a {id} in the checks of the API, in this order: who
// is calling (401), and whether the job exists and its workspace is one the caller sees on
// hub (404, the same answer for both). The handler gets the id.
func (s *server) inJob(h func(w http.ResponseWriter, r *http.Request, id string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		id := r.PathValue("id")
		workspace, err := s.deps.Jobs.WorkspaceOf(r.Context(), id)
		if errors.Is(err, jobs.ErrNotFound) {
			writeJobNotFound(w)
			return
		}
		if err != nil {
			s.storeFailure(w, r, "the workspace of a job could not be read", err)
			return
		}
		seen, err := s.auth.visible(r.Context(), p.token, workspace)
		if err != nil {
			s.hubFailure(w, r, err)
			return
		}
		if !seen {
			writeJobNotFound(w)
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, p)), id)
	}
}

// noBody reads the body of an action that takes none: it is nothing, or {}. Anything else
// is refused (415, 413 or 400) and noBody reports false.
func noBody(w http.ResponseWriter, r *http.Request) bool {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		bodyError(w, err, maxBody)
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return true
	}
	if mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mediaType != "application/json" {
		writeError(w, CodeUnsupportedMedia, "the body must be application/json")
		return false
	}
	return decodeJSON(w, data, &struct{}{})
}

// cancelJob is POST /api/v1/jobs/{id}/cancel: a job that is queued, running or blocked
// reads cancelled at once, and its runner is told. 200, with the job.
func (s *server) cancelJob(w http.ResponseWriter, r *http.Request, id string) {
	if !noBody(w, r) {
		return
	}
	doc, err := s.deps.Jobs.Cancel(r.Context(), id)
	if err != nil {
		s.jobRefusal(w, r, "the job could not be cancelled", err, true)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// fixBody is the body of a fix: an issue, and context.
type fixBody struct {
	Issue   json.RawMessage `json:"issue"`
	Context []string        `json:"context"`
}

// createJob is POST /api/v1/workspaces/{slug}/triage and …/fix: the body is read,
// checked and made a queued job on the workspace, which the guard has shown to the
// caller. 202, with the job.
func (s *server) createJob(kind jobs.Kind, limit int64) func(http.ResponseWriter, *http.Request, string) {
	return func(w http.ResponseWriter, r *http.Request, slug string) {
		data, ok := readBody(w, r, limit)
		if !ok {
			return
		}
		// A body that is not UTF-8 is read with its bad bytes replaced, so the rule
		// of the fields would never see them: it is looked for in the bytes.
		if field := invalidUTF8Field(data); field != "" {
			writeError(w, CodeInvalidRequest, field+": must be valid UTF-8")
			return
		}
		var body jobs.Body
		if kind == jobs.KindFix {
			var fix fixBody
			if !decodeJSON(w, data, &fix) {
				return
			}
			body = jobs.Body{Issue: fix.Issue, Context: fix.Context}
		} else if !decodeJSON(w, data, &body) {
			return
		}

		doc, err := s.deps.Jobs.Create(r.Context(), jobs.CreateRequest{
			Workspace: slug, Kind: kind, Body: body,
			Source: jobs.Source{Kind: "api", By: principalOf(r).user},
		})
		if err != nil {
			s.jobRefusal(w, r, "the job could not be created", err, false)
			return
		}
		writeJSON(w, http.StatusAccepted, doc)
	}
}

// jobRefusal answers the error of the jobs layer: the refusals it names are the
// answer of the request, anything else is a failure of the service.
func (s *server) jobRefusal(w http.ResponseWriter, r *http.Request, what string, err error, ofAJob bool) {
	var invalid *jobs.ValidationError
	var duplicate *jobs.DuplicateError
	var state *jobs.StateError
	switch {
	case errors.As(err, &invalid):
		writeError(w, CodeInvalidRequest, invalid.Error())
	case errors.Is(err, jobs.ErrNotFound):
		writeJobNotFound(w)
	case errors.As(err, &state):
		writeError(w, CodeInvalidState, fmt.Sprintf("the job is %s", state.State))
	case errors.Is(err, jobs.ErrNotSubscribed) && ofAJob:
		// The job is one the caller sees, so a 404 would say it does not exist: this says what is wrong.
		writeError(w, CodeNotSubscribed, "the workspace is not subscribed")
	case errors.Is(err, jobs.ErrNotSubscribed):
		writeNotFound(w)
	case errors.Is(err, jobs.ErrTokenUnreadable):
		writeError(w, CodeTokenUnreadable, "the token of the subscription cannot be read with this deployment's key; subscribe the workspace again")
	case errors.As(err, &duplicate):
		writeError(w, CodeDuplicateJob, fmt.Sprintf("an open job of this kind exists for the issue: %s", duplicate.JobID))
	default:
		s.storeFailure(w, r, what, err)
	}
}

// invalidUTF8Field is the field of a JSON body that holds bytes that are not UTF-8,
// the first in the order of the names, or "" when the body is UTF-8 throughout. The
// field of a file is named file.name or file.content; a body that is not an object
// is the "body".
func invalidUTF8Field(data []byte) string {
	if utf8.Valid(data) {
		return ""
	}
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return "body"
	}
	for _, name := range slices.Sorted(maps.Keys(top)) {
		if utf8.ValidString(name) && utf8.Valid(top[name]) {
			continue
		}
		if name == "file" {
			var file map[string]json.RawMessage
			if json.Unmarshal(top[name], &file) == nil {
				for _, inner := range slices.Sorted(maps.Keys(file)) {
					if !utf8.Valid(file[inner]) {
						return "file." + inner
					}
				}
			}
		}
		if utf8.ValidString(name) {
			return name
		}
		return "body"
	}
	return "body"
}

// answerBody is the body of an answer: exactly one of the two.
type answerBody struct {
	Answer *string `json:"answer"`
	Option *string `json:"option"`
}

// answerJob is POST /api/v1/jobs/{id}/answer: the question of a blocked job is answered, in
// text or with an option, and the job is queued again. 202, with the job.
func (s *server) answerJob(w http.ResponseWriter, r *http.Request, id string) {
	data, ok := readBodyOrNothing(w, r, maxBody) // no body at all is a 400 about the body, not a 415
	if !ok {
		return
	}
	if field := invalidUTF8Field(data); field != "" {
		writeError(w, CodeInvalidRequest, field+": must be valid UTF-8")
		return
	}
	var body answerBody
	if !decodeJSON(w, data, &body) {
		return
	}
	doc, err := s.deps.Jobs.Answer(r.Context(), id, principalOf(r).user, jobs.AnswerRequest{Answer: body.Answer, Option: body.Option})
	if err != nil {
		s.jobRefusal(w, r, "the job could not be answered", err, true)
		return
	}
	writeJSON(w, http.StatusAccepted, doc)
}

// retryJob is POST /api/v1/jobs/{id}/retry: a failed job is asked for again, with no body or
// {}, and queued. 202, with the job.
func (s *server) retryJob(w http.ResponseWriter, r *http.Request, id string) {
	if !noBody(w, r) {
		return
	}
	doc, err := s.deps.Jobs.Retry(r.Context(), id, principalOf(r).user)
	if err != nil {
		s.jobRefusal(w, r, "the job could not be retried", err, true)
		return
	}
	writeJSON(w, http.StatusAccepted, doc)
}

// listFilter reads the parameters of a list from the query of the request, or answers 400
// naming the one that is wrong and reports false.
func (s *server) listFilter(w http.ResponseWriter, r *http.Request) (jobs.ListFilter, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, CodeInvalidRequest, "the query string is not valid")
		return jobs.ListFilter{}, false
	}
	filter, err := jobs.ParseList(query)
	if err != nil {
		s.jobRefusal(w, r, "the list could not be read", err, false)
		return jobs.ListFilter{}, false
	}
	return filter, true
}

// listJobs is GET /api/v1/jobs: the jobs of every workspace that has a job the filter
// matches and that the caller sees on hub, whether it is subscribed or not, newest first, a
// page at a time. Hub is asked through the cache of the API for each of those workspaces; if it
// cannot say for one, the answer is the error, and not a page that leaves the jobs of that
// workspace out.
func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	filter, ok := s.listFilter(w, r)
	if !ok {
		return
	}
	known, err := s.deps.Jobs.Workspaces(r.Context(), filter)
	if err != nil {
		s.storeFailure(w, r, "the workspaces with jobs could not be listed", err)
		return
	}
	token := principalOf(r).token
	seen := make([]string, 0, len(known))
	for _, slug := range known {
		visible, err := s.auth.visible(r.Context(), token, slug)
		if err != nil {
			s.hubFailure(w, r, err)
			return
		}
		if visible {
			seen = append(seen, slug)
		}
	}
	s.writePage(w, r, seen, filter)
}

// listWorkspaceJobs is GET /api/v1/workspaces/{slug}/jobs: the jobs of one workspace that the
// caller sees, which the guard has shown, subscribed or not.
func (s *server) listWorkspaceJobs(w http.ResponseWriter, r *http.Request, slug string) {
	filter, ok := s.listFilter(w, r)
	if !ok {
		return
	}
	s.writePage(w, r, []string{slug}, filter)
}

// writePage answers 200 with a page of the jobs of the workspaces.
func (s *server) writePage(w http.ResponseWriter, r *http.Request, workspaces []string, filter jobs.ListFilter) {
	page, err := s.deps.Jobs.List(r.Context(), workspaces, filter)
	if err != nil {
		s.storeFailure(w, r, "the jobs could not be listed", err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

// getJob is GET /api/v1/jobs/{id}: the job with its attempts.
func (s *server) getJob(w http.ResponseWriter, r *http.Request, id string) {
	doc, err := s.deps.Jobs.Get(r.Context(), id)
	if err != nil {
		s.jobRefusal(w, r, "the job could not be read", err, false)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// getEnvelope is GET /api/v1/jobs/{id}/attempts/{n}/envelope: the envelope.json that the
// attempt stored, with its bytes unchanged.
func (s *server) getEnvelope(w http.ResponseWriter, r *http.Request, id string) {
	s.serveFile(w, r, id, "application/json", s.deps.Jobs.Envelope)
}

// getReport is GET /api/v1/jobs/{id}/attempts/{n}/report: the report.md that the attempt
// stored, with its bytes unchanged.
func (s *server) getReport(w http.ResponseWriter, r *http.Request, id string) {
	s.serveFile(w, r, id, "text/markdown; charset=utf-8", s.deps.Jobs.Report)
}

// serveFile answers with a file of an attempt. A number that is not an attempt's, an attempt
// that is not there and a file that it does not have are one answer, the one for a job that is
// not there.
func (s *server) serveFile(w http.ResponseWriter, r *http.Request, id, contentType string, read func(context.Context, string, int) ([]byte, error)) {
	raw := r.PathValue("n")
	n, err := strconv.Atoi(raw)
	if err != nil || strconv.Itoa(n) != raw { // 01, +1 and 1.0 are no attempt's number
		writeJobNotFound(w)
		return
	}
	data, err := read(r.Context(), id, n)
	if err != nil {
		s.jobRefusal(w, r, "the file of the attempt could not be read", err, false)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// eventNumber is an integer of at least 0 written as a person writes one.
var eventNumber = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// startingPoint is the number the stream starts after: the since parameter of the query if it
// is there, else the Last-Event-ID header, else 0. A value that is not an integer of at least 0 is
// refused with 400, naming where it came from, and startingPoint reports false.
func startingPoint(w http.ResponseWriter, r *http.Request) (int64, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, CodeInvalidRequest, "the query string is not valid")
		return 0, false
	}
	value, name := r.Header.Get("Last-Event-ID"), "Last-Event-ID"
	if values, given := query["since"]; given {
		if len(values) > 1 {
			writeError(w, CodeInvalidRequest, "since: must be given at most once")
			return 0, false
		}
		value, name = values[0], "since"
	} else if value == "" {
		return 0, true
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if !eventNumber.MatchString(value) || err != nil {
		writeError(w, CodeInvalidRequest, name+": must be an integer of at least 0")
		return 0, false
	}
	return n, true
}

// streamEvents is GET /api/v1/jobs/{id}/events: the events of the job after the starting point
// as text/event-stream, the stored ones and then the live ones, until the job is at rest. Each
// frame is flushed as it is written. A client that goes away, falls too far behind or meets the
// shutdown of the server is let go, and follows again from the last event it has.
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request, id string) {
	since, ok := startingPoint(w, r)
	if !ok {
		return
	}
	rc := http.NewResponseController(w)
	// A stream has no length that a deadline on the write could be set to.
	_ = rc.SetWriteDeadline(time.Time{})
	header := w.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		s.log.LogAttrs(r.Context(), slog.LevelError, "an event stream cannot be flushed", slog.String("error", err.Error()))
		return
	}
	// A write that fails is the client having gone, which is no fault of the service's.
	var gone bool
	write := func(frame []byte) error {
		if _, err := w.Write(frame); err != nil {
			gone = true
			return err
		}
		if err := rc.Flush(); err != nil {
			gone = true
			return err
		}
		return nil
	}
	err := s.deps.Jobs.Stream(r.Context(), id, since,
		func(ev jobs.Event) error { return write(ev.Frame()) },
		func() error { return write([]byte(jobs.KeepaliveFrame)) })
	switch {
	case err == nil, gone, errors.Is(err, jobs.ErrStreamsClosed), errors.Is(err, context.Canceled):
	case errors.Is(err, jobs.ErrSlowSubscriber):
		s.log.LogAttrs(r.Context(), slog.LevelInfo, "an event stream was closed because its client fell behind", slog.String("job", id))
	default:
		s.log.LogAttrs(r.Context(), slog.LevelError, "an event stream ended because the events could not be read", slog.String("job", id), slog.String("error", err.Error()))
	}
}
