package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The follow-ups of the forge: a triage job that ends ready on a workspace with AF_AUTO_FIX asks, through the worker, for
// a fix of its issue. The follow-up is recorded before the worker adds the fix label, so that the trigger that sees the
// label knows it for the triage's request and makes the linked fix job, with the triage as its parent. A triage job has
// at most one follow-up, and an issue at most one pending one.

// The states of a follow-up.
const (
	FollowUpPending   = "pending"
	FollowUpMade      = "made"
	FollowUpAbsorbed  = "absorbed"
	FollowUpWithdrawn = "withdrawn"
)

// OpFollowUp is the operation that the fault seam of the store is asked for when a linked fix job is made, after the job
// is stored and before its follow-up is marked made, in the same transaction.
const OpFollowUp = "follow_up"

// errFollowUpGone is the refusal of a linked fix job whose follow-up is no longer pending.
var errFollowUpGone = errors.New("the follow-up is no longer pending")

// FollowUp is the fix a triage job asked for on an issue, as the job document shows it: the label, the issue, the state,
// and the fix job that it was made into or absorbed by.
type FollowUp struct {
	// TriageJobID is the triage job that asked; the document of that job does not repeat it.
	TriageJobID string  `json:"-"`
	Label       string  `json:"label"`
	Issue       int64   `json:"issue"`
	State       string  `json:"state"`
	JobID       *string `json:"job_id"`
}

// followUpColumns are the columns of af_followups that make a FollowUp.
const followUpColumns = "triage_job_id, label, issue, state, job_id"

func scanFollowUp(r interface{ Scan(...any) error }) (FollowUp, error) {
	var f FollowUp
	var job sql.NullString
	if err := r.Scan(&f.TriageJobID, &f.Label, &f.Issue, &f.State, &job); err != nil {
		return FollowUp{}, err
	}
	f.JobID = nullable(job)
	return f, nil
}

// RecordFollowUp records that the triage job asks for a fix of the issue with the label, and returns the follow-up as it
// is recorded. A triage job that asked already gets its follow-up as it stands. Otherwise an open fix job of the issue
// absorbs it, naming that job; and when there is none it is pending, and a pending follow-up of an earlier triage job of
// the issue is withdrawn in the same transaction, so that the fix is linked to the latest triage. ErrNotFound is a triage
// job that does not exist.
func (s *Service) RecordFollowUp(ctx context.Context, triage string, issue int64, label string) (FollowUp, error) {
	return s.store.RecordFollowUp(ctx, triage, issue, label)
}

// RecordFollowUp is Service.RecordFollowUp, which the recorder of a triage attempt calls for the worker.
func (st *Store) RecordFollowUp(ctx context.Context, triage string, issue int64, label string) (FollowUp, error) {
	var out FollowUp
	err := st.run(ctx, triage, func(x *txn) error {
		// The transaction writes first, so that it holds the write lock from its start (see Store.run).
		if _, err := x.ExecContext(ctx, "UPDATE af_followups SET state = state WHERE triage_job_id = ?", triage); err != nil {
			return err
		}
		f, err := scanFollowUp(x.QueryRowContext(ctx, "SELECT "+followUpColumns+" FROM af_followups WHERE triage_job_id = ?", triage))
		if err == nil {
			out = f
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var workspace string
		err = x.QueryRowContext(ctx, "SELECT workspace FROM af_jobs WHERE id = ? AND kind = 'triage'", triage).Scan(&workspace)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out = FollowUp{TriageJobID: triage, Label: label, Issue: issue, State: FollowUpPending}
		var open string
		err = x.QueryRowContext(ctx, "SELECT id FROM af_jobs WHERE workspace = ? AND kind = 'fix' AND issue = ? AND state IN ('queued', 'running', 'blocked')",
			workspace, issue).Scan(&open)
		switch {
		case err == nil:
			out.State, out.JobID = FollowUpAbsorbed, &open
		case !errors.Is(err, sql.ErrNoRows):
			return err
		default:
			if _, err := x.ExecContext(ctx, "UPDATE af_followups SET state = 'withdrawn' WHERE workspace = ? AND issue = ? AND state = 'pending'", workspace, issue); err != nil {
				return err
			}
		}
		_, err = x.ExecContext(ctx,
			"INSERT INTO af_followups (triage_job_id, workspace, issue, label, state, job_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
			triage, workspace, issue, label, out.State, out.JobID, formatTime(st.now()))
		return err
	})
	if err != nil {
		return FollowUp{}, fmt.Errorf("the follow-up of %s: %w", triage, err)
	}
	return out, nil
}

// WithdrawFollowUp withdraws the pending follow-up of the triage job: the worker could not add its label.
func (s *Service) WithdrawFollowUp(ctx context.Context, triage string) error {
	return s.store.settleFollowUp(ctx, triage, FollowUpWithdrawn, nil)
}

// AbsorbFollowUp marks the pending follow-up of the triage job absorbed by the open fix job: the trigger saw its label on
// an issue whose fix job is open.
func (s *Service) AbsorbFollowUp(ctx context.Context, triage, job string) error {
	var named *string
	if job != "" {
		named = &job
	}
	return s.store.settleFollowUp(ctx, triage, FollowUpAbsorbed, named)
}

// settleFollowUp moves the follow-up of the triage job from pending to the state; one that is not pending stays as it is.
func (st *Store) settleFollowUp(ctx context.Context, triage, state string, job *string) error {
	if _, err := st.db.ExecContext(ctx, "UPDATE af_followups SET state = ?, job_id = ? WHERE triage_job_id = ? AND state = 'pending'", state, job, triage); err != nil {
		return fmt.Errorf("the follow-up of %s: %w", triage, err)
	}
	return nil
}

// PendingFollowUp is the pending follow-up of the issue on the workspace, or ErrNotFound.
func (s *Service) PendingFollowUp(ctx context.Context, workspace string, issue int64) (FollowUp, error) {
	f, err := scanFollowUp(s.store.db.QueryRowContext(ctx,
		"SELECT "+followUpColumns+" FROM af_followups WHERE workspace = ? AND issue = ? AND state = 'pending'", workspace, issue))
	if errors.Is(err, sql.ErrNoRows) {
		return FollowUp{}, ErrNotFound
	}
	if err != nil {
		return FollowUp{}, fmt.Errorf("the pending follow-up of %s#%d: %w", workspace, issue, err)
	}
	return f, nil
}

// CreateLinked makes the fix job that the pending follow-up of the triage job asks for, as Create makes a job, and marks
// the follow-up made with it in the transaction that stores the job: no reader sees one without the other, and a failure
// leaves neither. A follow-up that is no longer pending makes no job, and a duplicate refusal leaves it pending.
func (s *Service) CreateLinked(ctx context.Context, triage string, req CreateRequest) (Doc, error) {
	return s.create(ctx, req, triage)
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

// checkParent refuses a parent that a job may not have: a job has a parent only when its source is a triage, and the
// parent is that triage job, of the same workspace.
func (s *Service) checkParent(ctx context.Context, req CreateRequest) error {
	if req.ParentJobID == "" {
		return nil
	}
	if req.Source.Kind != "triage" || req.Source.By != req.ParentJobID {
		return invalid("parent_job_id", "a job has a parent only when its source is the triage job that is its parent")
	}
	parent, err := loadJob(ctx, s.store.db, req.ParentJobID)
	if errors.Is(err, ErrNotFound) {
		return invalid("parent_job_id", "the job %s does not exist", req.ParentJobID)
	}
	if err != nil {
		return err
	}
	if parent.Kind != string(KindTriage) || parent.Workspace != req.Workspace {
		return invalid("parent_job_id", "the parent must be a triage job of the workspace %s", req.Workspace)
	}
	return nil
}

// linkFollowUp marks the follow-up of the triage job made with the job, in the transaction that stores the job.
func (x *txn) linkFollowUp(ctx context.Context, triage string) error {
	if err := x.s.fails(OpFollowUp); err != nil {
		return err
	}
	res, err := x.ExecContext(ctx, "UPDATE af_followups SET state = 'made', job_id = ? WHERE triage_job_id = ? AND state = 'pending'", x.jobID, triage)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return errFollowUpGone
	}
	return nil
}
