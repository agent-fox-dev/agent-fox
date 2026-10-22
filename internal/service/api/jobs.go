package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
			s.jobRefusal(w, r, "the job could not be created", err)
			return
		}
		writeJSON(w, http.StatusAccepted, doc)
	}
}

// jobRefusal answers the error of the jobs layer: the refusals it names are the
// answer of the request, anything else is a failure of the service.
func (s *server) jobRefusal(w http.ResponseWriter, r *http.Request, what string, err error) {
	var invalid *jobs.ValidationError
	var duplicate *jobs.DuplicateError
	switch {
	case errors.As(err, &invalid):
		writeError(w, CodeInvalidRequest, invalid.Error())
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
