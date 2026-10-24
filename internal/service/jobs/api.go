package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// The refusals of a request to create a job.
var (
	// ErrNotSubscribed: the workspace is not subscribed, or is being unsubscribed.
	ErrNotSubscribed = errors.New("workspace is not subscribed")
	// ErrTokenUnreadable: the token of the subscription does not open with the key.
	ErrTokenUnreadable = errors.New("the token of the subscription does not open with this key")
)

// DuplicateError is the refusal of a second open job of a kind for an issue; it
// holds the job that is open. It is an ErrDuplicate.
type DuplicateError struct{ JobID string }

func (e *DuplicateError) Error() string {
	return "an open job of this kind exists for the issue: " + e.JobID
}

// Is makes a DuplicateError an ErrDuplicate.
func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicate }

// OpDuplicate is the operation that the fault seam of the store is asked for when a creator has lost an issue to an open
// job, after its insert was refused and before it looks the job up.
const OpDuplicate = "duplicate"

// CategoryUnsupported is the category of the failure of work that the service cannot
// do yet.
const CategoryUnsupported = "unsupported"

// The kinds of source a job can have: the API, and the label poll.
var sources = map[string]bool{"api": true, "label": true}

// CreateRequest is a request to create a job: what the routes take from a caller,
// and what a trigger passes. A trigger that consumed a request label names it; a job
// of the API never has one.
type CreateRequest struct {
	Workspace    string
	Kind         Kind
	Body         Body
	Source       Source
	RequestLabel string
}

// Create creates a job with attempt 1, queued, and returns it as it was made. It
// applies the validation and the refusals of the routes: a *ValidationError for a
// body that breaks a rule, ErrNotSubscribed for a workspace that is not subscribed
// or is being unsubscribed, ErrTokenUnreadable when the token of its subscription
// does not open, and a *DuplicateError (an ErrDuplicate) when the issue is the input
// of an open job of the same kind. Whether the caller sees the workspace on hub is
// for the caller to have asked.
//
// The attempt is queued after the job is stored. If that fails, the job is still
// made, a warning names it, and the sweep queues the attempt.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Doc, error) {
	if req.Kind != KindTriage && req.Kind != KindFix {
		return Doc{}, fmt.Errorf("jobs: unknown kind %q", req.Kind)
	}
	if !sources[req.Source.Kind] || strings.TrimSpace(req.Source.By) == "" {
		return Doc{}, fmt.Errorf("jobs: source %+v is not a kind of source with someone behind it", req.Source)
	}
	if req.RequestLabel != "" && req.Source.Kind == "api" {
		return Doc{}, errors.New("jobs: a job of the API consumed no request label")
	}
	parsed, err := req.Body.Validate(req.Kind)
	if err != nil {
		return Doc{}, err
	}

	id, err := s.insert(ctx, req, parsed)
	if err != nil {
		return Doc{}, err
	}
	// The document is read before the attempt is queued: once it is, a worker may
	// claim it at once, and the job that is answered is the one that was made.
	doc, err := s.store.Doc(ctx, id, false)
	if err != nil {
		return Doc{}, err
	}
	if err := s.enqueue(context.WithoutCancel(ctx), id, 1); err != nil {
		s.log.Warn("the job is stored but its attempt is not in the queue; the sweep will queue it",
			"job", id, "attempt", 1, "error", err.Error())
	}
	return doc, nil
}

// duplicateTries is how many times a creator that has lost an issue to a job that ended before it could look the job
// up tries again to make its own.
const duplicateTries = 3

// insert checks that the workspace can take the job and stores it, holding the gate
// so that an unsubscribe that starts meanwhile finds the job and cancels it.
//
// The check that no other job of the kind is open for the issue is the insert's own: the unique index of the open
// jobs refuses a second one, however the creators race, and the one that loses is told which job won. If that job
// ended between the refusal and the lookup, there is no job to name and the issue is free again, so the creator
// tries once more.
func (s *Service) insert(ctx context.Context, req CreateRequest, parsed Parsed) (string, error) {
	s.gate.RLock()
	defer s.gate.RUnlock()
	if err := s.subscribed(ctx, req.Workspace); err != nil {
		return "", err
	}
	for range duplicateTries {
		id, err := s.store.CreateJob(ctx, NewJob{
			Workspace: req.Workspace, Kind: req.Kind, Input: parsed.Input, Context: parsed.Context,
			Issue: parsed.Issue, Source: req.Source, RequestLabel: req.RequestLabel,
		})
		if !errors.Is(err, ErrDuplicate) || parsed.Issue == nil {
			return id, err
		}
		if err := s.store.fails(OpDuplicate); err != nil {
			return "", err
		}
		// The refusal names the job that holds the issue by the lookup that the label poll uses.
		open, lookup := s.OpenJob(ctx, req.Workspace, req.Kind, *parsed.Issue)
		switch {
		case lookup == nil:
			return "", &DuplicateError{JobID: open.ID}
		case !errors.Is(lookup, ErrNotFound):
			return "", lookup
		}
	}
	return "", ErrDuplicate
}

// LatestJob is the job of the kind for the issue on the workspace that was made last, whatever its state, or ErrNotFound.
func (s *Service) LatestJob(ctx context.Context, workspace string, kind Kind, issue int64) (Doc, error) {
	var id string
	err := s.store.db.QueryRowContext(ctx,
		"SELECT id FROM af_jobs WHERE workspace = ? AND kind = ? AND issue = ? ORDER BY created_at DESC, rowid DESC LIMIT 1",
		workspace, string(kind), issue).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Doc{}, ErrNotFound
	}
	if err != nil {
		return Doc{}, fmt.Errorf("the latest %s job of %s#%d: %w", kind, workspace, issue, err)
	}
	return s.store.Doc(ctx, id, false)
}

// OpenJob is the open job (queued, running or blocked) of a kind for an issue number
// on a workspace, or ErrNotFound.
func (s *Service) OpenJob(ctx context.Context, workspace string, kind Kind, issue int64) (Doc, error) {
	id, err := s.store.OpenJobID(ctx, workspace, kind, issue)
	if err != nil {
		return Doc{}, err
	}
	return s.store.Doc(ctx, id, false)
}
