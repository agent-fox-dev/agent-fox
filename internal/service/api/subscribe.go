package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
)

// subscribeRequest is the body of PUT /api/v1/workspaces/{slug}.
type subscribeRequest struct {
	HubToken string `json:"hub_token"`
}

// validate checks the fields and returns what is wrong, if anything. A hub_token with a control character cannot be sent to hub as a header,
// and would be reported as hub being unavailable; it is refused here instead.
func (req subscribeRequest) validate() string {
	if req.HubToken == "" {
		return "hub_token is required"
	}
	if strings.IndexFunc(req.HubToken, unicode.IsControl) >= 0 {
		return "hub_token must not contain a control character"
	}
	return ""
}

// createLabels creates the seven labels of the table, with the token, and stops at the first that
// fails. It says how many hub created and how many were there already.
func (s *server) createLabels(ctx context.Context, token, slug string) (created, existing int, err error) {
	for _, l := range labels.Table() {
		made, err := s.deps.Hub.CreateLabel(ctx, token, slug, labels.Name(l.Suffix), l.Color, l.Description)
		if err != nil {
			return created, existing, err
		}
		if made {
			created++
		} else {
			existing++
		}
	}
	return created, existing, nil
}

// labelFailure answers a label creation that hub did not take, by what hub answered: a token hub refuses is an invalid
// hub token; a token hub forbids, or a credential the forge refuses, or any other refusal, is forge_forbidden, which no
// retry cures; a hub that does not serve the route is unavailable, since another token would not cure it either; and a
// hub that could not say is unavailable. No message holds the token: hub's own message is scrubbed of it.
func (s *server) labelFailure(w http.ResponseWriter, r *http.Request, slug string, err error) {
	rejected, isRejected := errors.AsType[*hub.RejectedError](err)
	switch {
	case errors.Is(err, hub.ErrRefused):
		writeError(w, CodeInvalidHubToken, "hub refused the token")
	case errors.Is(err, hub.ErrForbidden):
		writeError(w, CodeForgeForbidden, fmt.Sprintf("hub forbids the token to create labels on the workspace %q: it lacks the scope for hub's forge API (forge:write)", slug))
	case errors.Is(err, hub.ErrNotFound), isRejected && rejected.Status == http.StatusMethodNotAllowed:
		writeError(w, CodeHubUnavailable, fmt.Sprintf("hub does not serve label creation for the workspace %q", slug))
	case isRejected && rejected.Status == http.StatusBadGateway:
		writeError(w, CodeForgeForbidden, fmt.Sprintf("the forge refused hub's credential for the repository of the workspace %q: %s", slug, rejected.Message))
	case isRejected:
		writeError(w, CodeForgeForbidden, fmt.Sprintf("hub refused to create a label on the workspace %q: %s", slug, rejected.Message))
	default:
		s.hubFailure(w, r, err)
	}
}

// subscribe is PUT /api/v1/workspaces/{slug}: the caller, who sees the workspace
// on hub, hands over a token for it, and the service keeps it, sealed. It is a
// full replace: 201 when the workspace was not subscribed, 200 when it was.
//
// Nothing is written to the store before every check has passed. After the caller and
// the body, two fresh calls to hub are made with the token: whether it finds the
// workspace (the gate), and then whose token it is (best effort). They go to the hub
// client directly: a call made with the token of a subscription never reads, fills or
// evicts the caches of the caller's answers, even when the token is the caller's own,
// so that a refusal of the token here is a 422 and never the 401 of a caller.
//
// Then the seven labels of the label table are created through hub with the token, one
// at a time and in table order; a label that hub
// reports as there already is in place, and is left as it is. Only when all seven are
// in place is the subscription stored. A label created before a failure stays on the
// forge, where the next PUT finds it.
func (s *server) subscribe(w http.ResponseWriter, r *http.Request, slug string) {
	var req subscribeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if problem := req.validate(); problem != "" {
		writeError(w, CodeInvalidRequest, problem)
		return
	}

	if err := s.deps.Hub.Workspace(r.Context(), req.HubToken, slug); err != nil {
		switch {
		case errors.Is(err, hub.ErrRefused):
			writeError(w, CodeInvalidHubToken, "hub refused the token")
		case errors.Is(err, hub.ErrNotFound):
			writeError(w, CodeInvalidHubToken, fmt.Sprintf("hub does not show the workspace %q to the token", slug))
		default:
			s.hubFailure(w, r, err)
		}
		return
	}
	tokenUser := ""
	switch user, err := s.deps.Hub.User(r.Context(), req.HubToken); {
	case err == nil:
		tokenUser = user.ID
	case errors.Is(err, hub.ErrRefused):
		// A credential that is bound to a workspace sees it but not /user. The
		// subscription is stored without a user.
	default:
		s.hubFailure(w, r, err)
		return
	}

	created, existing, err := s.createLabels(r.Context(), req.HubToken, slug)
	if err != nil {
		s.labelFailure(w, r, slug, err)
		return
	}
	s.log.LogAttrs(r.Context(), slog.LevelInfo, "the labels of the workspace are in place",
		slog.String("workspace", slug), slog.Int("created", created), slog.Int("existing", existing))

	sub, stored, err := s.deps.Store.Upsert(r.Context(), slug, req.HubToken, tokenUser, principalOf(r).user, s.now())
	if err != nil {
		s.storeFailure(w, r, "the subscription could not be stored", err)
		return
	}
	status := http.StatusOK
	if stored {
		status = http.StatusCreated
	}
	writeJSON(w, status, viewOf(sub))
}
