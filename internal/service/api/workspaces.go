package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// storeFailure answers a failure of the store with 500. The error is logged; it
// names the operation and the cause of the database, never a token.
func (s *server) storeFailure(w http.ResponseWriter, r *http.Request, what string, err error) {
	s.log.LogAttrs(r.Context(), slog.LevelError, what, slog.String("error", err.Error()))
	writeError(w, CodeInternal, "internal error")
}

// listWorkspaces is GET /api/v1/workspaces: every subscription whose slug the
// caller sees on hub, sorted by slug, as {"workspaces":[…]}. Hub is asked through
// the same cache as for any route, so a list costs a call to hub only for a slug
// that has not been asked about within the TTL. When hub cannot say for one of
// them, the answer is the error and not a list that leaves it out.
func (s *server) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	subs, err := s.deps.Store.List(r.Context())
	if err != nil {
		s.storeFailure(w, r, "the subscriptions could not be listed", err)
		return
	}
	token := principalOf(r).token
	views := []subscriptionView{}
	for _, sub := range subs {
		seen, err := s.auth.visible(r.Context(), token, sub.Slug)
		if err != nil {
			s.hubFailure(w, r, err)
			return
		}
		if seen {
			views = append(views, viewOf(sub))
		}
	}
	writeJSON(w, http.StatusOK, map[string][]subscriptionView{"workspaces": views})
}

// getWorkspace is GET /api/v1/workspaces/{slug}: the subscription, or the 404 that
// a workspace gets that the caller cannot see (the guard has answered that one
// already) and that is not subscribed, which is the same answer.
func (s *server) getWorkspace(w http.ResponseWriter, r *http.Request, slug string) {
	sub, err := s.deps.Store.Get(r.Context(), slug)
	switch {
	case errors.Is(err, store.ErrNotSubscribed):
		writeNotFound(w)
	case err != nil:
		s.storeFailure(w, r, "the subscription could not be read", err)
	default:
		writeJSON(w, http.StatusOK, viewOf(sub))
	}
}

// deleteWorkspace is DELETE /api/v1/workspaces/{slug}: the jobs of the workspace that are
// queued, running or blocked are cancelled, and the runners of the running ones are given
// time to return (see jobs.Service.Unsubscribe); then the row is removed, its sealed token
// with it, and the answer is 204. A workspace that is not subscribed is the same 404 as for
// GET. A subscription whose token does not open is removed like any other. A removal that
// fails is a 500, and leaves the subscription, and the workspace takes jobs again.
func (s *server) deleteWorkspace(w http.ResponseWriter, r *http.Request, slug string) {
	if _, err := s.deps.Store.Get(r.Context(), slug); errors.Is(err, store.ErrNotSubscribed) {
		writeNotFound(w)
		return
	} else if err != nil {
		s.storeFailure(w, r, "the subscription could not be read", err)
		return
	}
	existed := true
	remove := func() error {
		var err error
		existed, err = s.deps.Store.Delete(r.Context(), slug)
		return err
	}
	var err error
	if s.deps.Jobs != nil {
		err = s.deps.Jobs.Unsubscribe(r.Context(), slug, remove)
	} else {
		err = remove()
	}
	switch {
	case err != nil:
		s.storeFailure(w, r, "the subscription could not be removed", err)
	case !existed:
		writeNotFound(w)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
