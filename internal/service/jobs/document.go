package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// answerWith is where a blocked job says how it is answered.
func answerWith(id string) string { return "POST /api/v1/jobs/" + id + "/answer" }

// jobRow is a row of af_jobs.
type jobRow struct {
	ID, Workspace, Kind                     string
	Input                                   string
	Issue                                   sql.NullInt64
	SourceKind, SourceBy                    string
	RequestLabel                            sql.NullString
	State                                   State
	LabelsSet, LabelsRemoved, LabelsRestore sql.NullString
	CreatedAt                               string
	StartedAt, FinishedAt                   sql.NullString
}

const jobColumns = `id, workspace, kind, input, issue, source_kind, source_by, request_label, state,
	labels_set, labels_removed, labels_restore, created_at, started_at, finished_at`

func (j *jobRow) scan(r interface{ Scan(...any) error }) error {
	return r.Scan(&j.ID, &j.Workspace, &j.Kind, &j.Input, &j.Issue, &j.SourceKind, &j.SourceBy, &j.RequestLabel, &j.State,
		&j.LabelsSet, &j.LabelsRemoved, &j.LabelsRestore, &j.CreatedAt, &j.StartedAt, &j.FinishedAt)
}

// attemptRow is a row of af_attempts.
type attemptRow struct {
	JobID                          string
	N                              int
	State                          AttemptState
	QueueSeq                       int64
	TriggerKind, TriggerBy         string
	Answer                         sql.NullString
	Deliveries, TransientFailures  int
	MaxAttempts                    int
	HubSession, HubRun, HubSandbox sql.NullString
	Question, Options              sql.NullString
	Status                         sql.NullString
	ExitCode                       sql.NullInt64
	Summary, Error                 sql.NullString
	Artifacts, SideEffects, Usage  sql.NullString
	HasEnvelope, HasReport         bool
	StartedAt, FinishedAt          sql.NullString
}

const attemptColumns = `job_id, n, state, queue_seq, trigger_kind, trigger_by, answer, deliveries, transient_failures, max_attempts,
	hub_session, hub_run, hub_sandbox, question, options, status, exit_code, summary, error, artifacts, side_effects, usage,
	has_envelope, has_report, started_at, finished_at`

func (a *attemptRow) scan(r interface{ Scan(...any) error }) error {
	return r.Scan(&a.JobID, &a.N, &a.State, &a.QueueSeq, &a.TriggerKind, &a.TriggerBy, &a.Answer, &a.Deliveries, &a.TransientFailures, &a.MaxAttempts,
		&a.HubSession, &a.HubRun, &a.HubSandbox, &a.Question, &a.Options, &a.Status, &a.ExitCode, &a.Summary, &a.Error, &a.Artifacts, &a.SideEffects, &a.Usage,
		&a.HasEnvelope, &a.HasReport, &a.StartedAt, &a.FinishedAt)
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// loadJob reads a job, or reports ErrNotFound.
func loadJob(ctx context.Context, q querier, id string) (jobRow, error) {
	var j jobRow
	err := j.scan(q.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM af_jobs WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return jobRow{}, ErrNotFound
	}
	if err != nil {
		return jobRow{}, fmt.Errorf("job %s: %w", id, err)
	}
	return j, nil
}

// loadAttempts reads the attempts of a job, in the order of their numbers.
func loadAttempts(ctx context.Context, q querier, id string) ([]attemptRow, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+attemptColumns+" FROM af_attempts WHERE job_id = ? ORDER BY n", id)
	if err != nil {
		return nil, fmt.Errorf("attempts of %s: %w", id, err)
	}
	defer rows.Close()
	var out []attemptRow
	for rows.Next() {
		var a attemptRow
		if err := a.scan(rows); err != nil {
			return nil, fmt.Errorf("attempts of %s: %w", id, err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Doc is the document of a job, with its attempts when attempts is true.
func (s *Store) Doc(ctx context.Context, id string, attempts bool) (Doc, error) {
	job, err := loadJob(ctx, s.db, id)
	if err != nil {
		return Doc{}, err
	}
	rows, err := loadAttempts(ctx, s.db, id)
	if err != nil {
		return Doc{}, err
	}
	return buildDoc(job, rows, attempts)
}

// nullable is a string that may be null, as the pointer a document holds.
func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// decode reads the JSON of a column into v, if the column is not null.
func decode(column string, s sql.NullString, v any) error {
	if !s.Valid {
		return nil
	}
	if err := json.Unmarshal([]byte(s.String), v); err != nil {
		return fmt.Errorf("%s: %w", column, err)
	}
	return nil
}

// ended reports whether an attempt has ended, which is when what it came to counts.
func (a attemptRow) ended() bool { return a.State != AttemptQueued && a.State != AttemptRunning }

// buildDoc makes the document of a job from its rows.
func buildDoc(j jobRow, attempts []attemptRow, withAttempts bool) (Doc, error) {
	doc := Doc{
		ID:           j.ID,
		Workspace:    j.Workspace,
		Kind:         Kind(j.Kind),
		Source:       Source{Kind: j.SourceKind, By: j.SourceBy},
		RequestLabel: nullable(j.RequestLabel),
		State:        j.State,
		Artifacts:    []toolio.Artifact{},
		SideEffects:  []toolio.SideEffect{},
		AttemptCount: len(attempts),
		CreatedAt:    j.CreatedAt,
		StartedAt:    nullable(j.StartedAt),
		FinishedAt:   nullable(j.FinishedAt),
	}
	if err := json.Unmarshal([]byte(j.Input), &doc.Input); err != nil {
		return Doc{}, fmt.Errorf("job %s: input: %w", j.ID, err)
	}
	if j.Issue.Valid {
		doc.Issue = &j.Issue.Int64
	}

	var current *attemptRow
	var latest *attemptRow // the latest attempt that came to something
	for i := range attempts {
		a := &attempts[i]
		current = a
		if a.Status.Valid {
			latest = a
		}
		if a.ended() && a.Usage.Valid {
			var u toolio.Usage
			if err := decode("usage", a.Usage, &u); err != nil {
				return Doc{}, fmt.Errorf("attempt %d of %s: %w", a.N, j.ID, err)
			}
			doc.Usage.CostUSD += u.CostUSD
			doc.Usage.InputTokens += u.InputTokens
			doc.Usage.OutputTokens += u.OutputTokens
			doc.Usage.Turns += u.Turns
		}
	}
	if current != nil {
		doc.CurrentAttempt = current.N
		if current.State == AttemptRunning && (current.HubSession.Valid || current.HubRun.Valid || current.HubSandbox.Valid) {
			doc.Hub = &HubDoc{SessionID: nullable(current.HubSession), RunID: nullable(current.HubRun), SandboxID: nullable(current.HubSandbox)}
		}
	}

	if latest != nil {
		result := &ResultDoc{Status: toolio.Status(latest.Status.String), Summary: latest.Summary.String}
		result.ExitCode = toolio.ExitCode(result.Status)
		if err := decode("error", latest.Error, &result.Error); err != nil {
			return Doc{}, fmt.Errorf("attempt %d of %s: %w", latest.N, j.ID, err)
		}
		doc.Result = result
		if err := decode("artifacts", latest.Artifacts, &doc.Artifacts); err != nil {
			return Doc{}, fmt.Errorf("attempt %d of %s: %w", latest.N, j.ID, err)
		}
		if err := decode("side_effects", latest.SideEffects, &doc.SideEffects); err != nil {
			return Doc{}, fmt.Errorf("attempt %d of %s: %w", latest.N, j.ID, err)
		}
		if doc.Artifacts == nil {
			doc.Artifacts = []toolio.Artifact{}
		}
		if doc.SideEffects == nil {
			doc.SideEffects = []toolio.SideEffect{}
		}
	}

	if j.State == StateBlocked && current != nil && current.Question.Valid {
		blocked := &Blocked{Reason: "needs_human", Question: current.Question.String, Options: []Option{}, AnswerWith: answerWith(j.ID)}
		if err := decode("options", current.Options, &blocked.Options); err != nil {
			return Doc{}, fmt.Errorf("attempt %d of %s: %w", current.N, j.ID, err)
		}
		doc.Blocked = blocked
	}

	labels := LabelsDoc{Set: []string{}, Removed: []string{}, Restore: nullable(j.LabelsRestore)}
	if err := decode("labels_set", j.LabelsSet, &labels.Set); err != nil {
		return Doc{}, fmt.Errorf("job %s: %w", j.ID, err)
	}
	if err := decode("labels_removed", j.LabelsRemoved, &labels.Removed); err != nil {
		return Doc{}, fmt.Errorf("job %s: %w", j.ID, err)
	}
	if len(labels.Set) > 0 || len(labels.Removed) > 0 || labels.Restore != nil {
		doc.Labels = &labels
	}

	if withAttempts {
		doc.Attempts = make([]AttemptDoc, 0, len(attempts))
		for _, a := range attempts {
			ad, err := attemptDoc(a)
			if err != nil {
				return Doc{}, fmt.Errorf("attempt %d of %s: %w", a.N, j.ID, err)
			}
			doc.Attempts = append(doc.Attempts, ad)
		}
	}
	return doc, nil
}

// attemptDoc is the document of an attempt.
func attemptDoc(a attemptRow) (AttemptDoc, error) {
	ad := AttemptDoc{
		N:                 a.N,
		State:             a.State,
		Trigger:           Trigger{Kind: TriggerKind(a.TriggerKind), By: a.TriggerBy},
		Deliveries:        a.Deliveries,
		TransientFailures: a.TransientFailures,
		Summary:           nullable(a.Summary),
		StartedAt:         nullable(a.StartedAt),
		FinishedAt:        nullable(a.FinishedAt),
	}
	if a.Status.Valid {
		status := toolio.Status(a.Status.String)
		code := toolio.ExitCode(status)
		ad.Status, ad.ExitCode = &status, &code
	}
	if a.Answer.Valid {
		var answer Answer
		if err := decode("answer", a.Answer, &answer); err != nil {
			return AttemptDoc{}, err
		}
		ad.Answer = &AnswerDoc{Question: answer.Question, Option: answer.Option, Text: answer.Text}
	}
	if err := decode("error", a.Error, &ad.Error); err != nil {
		return AttemptDoc{}, err
	}
	if a.ended() {
		if err := decode("usage", a.Usage, &ad.Usage); err != nil {
			return AttemptDoc{}, err
		}
	}
	return ad, nil
}
