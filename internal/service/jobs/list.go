package jobs

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The size of a page: 50 jobs when the request names none, and at most 200.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// ListFilter is what a list asks for: the jobs of a kind, in a state, for an issue, and
// how many of them, from where. A field left at its zero value asks for no restriction.
type ListFilter struct {
	Kind  Kind
	State State
	// Issue is an issue number, or 0 for any.
	Issue int64
	// Limit is the most jobs a page holds, from 1 to 200; 0 means 50.
	Limit int
	// after is the position the page begins after: the job that ended the page before it.
	after *position
}

// Page is a page of a list: the jobs, newest first, and the cursor of the page after it, or
// null when this is the last.
type Page struct {
	Jobs       []Doc   `json:"jobs"`
	NextCursor *string `json:"next_cursor"`
}

// position is where in the order a page ends: the job it ends with. The jobs are ordered by
// created_at and then id, newest first, so the next page holds the jobs that come after it.
type position struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

var (
	jobIDPattern = regexp.MustCompile(`^job_[0-9a-f]{20}$`)
	// number is a positive integer written the way a person writes one: no sign, no leading zero.
	number = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// cursor is the opaque form of a position: URL-safe base64, without padding, of its JSON.
func (p position) cursor() string {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(err) // two strings
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// parsePosition reads a cursor that cursor made: anything else, a cursor edited by hand
// included, is not one.
func parsePosition(cursor string) (*position, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var p position
	if err := dec.Decode(&p); err != nil {
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	at, err := time.Parse(timeLayout, p.CreatedAt)
	if err != nil || formatTime(at) != p.CreatedAt || !jobIDPattern.MatchString(p.ID) {
		return nil, false
	}
	return &p, true
}

// ParseList reads the parameters of a list request: kind, state, issue, limit and cursor,
// each at most once. A parameter that is there must be valid, so that an empty one is refused
// like any other; one that is not named here is left alone. The first that breaks its rule, in
// that order, is a *ValidationError that names it.
func ParseList(query url.Values) (ListFilter, error) {
	var f ListFilter
	one := func(name string) (string, bool, error) {
		values, given := query[name]
		if len(values) > 1 {
			return "", given, invalid(name, "must be given at most once")
		}
		if !given {
			return "", false, nil
		}
		return values[0], true, nil
	}

	if v, given, err := one("kind"); err != nil {
		return f, err
	} else if given {
		if f.Kind = Kind(v); f.Kind != KindTriage && f.Kind != KindFix {
			return f, invalid("kind", "must be %s or %s", KindTriage, KindFix)
		}
	}
	if v, given, err := one("state"); err != nil {
		return f, err
	} else if given {
		f.State = State(v)
		switch f.State {
		case StateQueued, StateRunning, StateBlocked, StateDone, StateFailed, StateCancelled:
		default:
			return f, invalid("state", "must be one of queued, running, blocked, done, failed, cancelled")
		}
	}
	if v, given, err := one("issue"); err != nil {
		return f, err
	} else if given {
		n, err := strconv.ParseInt(v, 10, 64)
		if !number.MatchString(v) || err != nil {
			return f, invalid("issue", "must be a positive integer")
		}
		f.Issue = n
	}
	if v, given, err := one("limit"); err != nil {
		return f, err
	} else if given {
		n, err := strconv.Atoi(v)
		if !number.MatchString(v) || err != nil || n > maxLimit {
			return f, invalid("limit", "must be an integer from 1 to %d", maxLimit)
		}
		f.Limit = n
	}
	if v, given, err := one("cursor"); err != nil {
		return f, err
	} else if given {
		after, ok := parsePosition(v)
		if !ok {
			return f, invalid("cursor", "is not a cursor this service gave")
		}
		f.after = after
	}
	return f, nil
}

// where is the conditions of a filter on the jobs, without the workspaces or the page.
func (f ListFilter) where() (conditions []string, args []any) {
	if f.Kind != "" {
		conditions, args = append(conditions, "kind = ?"), append(args, string(f.Kind))
	}
	if f.State != "" {
		conditions, args = append(conditions, "state = ?"), append(args, string(f.State))
	}
	if f.Issue != 0 {
		conditions, args = append(conditions, "issue = ?"), append(args, f.Issue)
	}
	return conditions, args
}

// placeholders is n question marks for an IN list.
func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

// Workspaces are the workspaces that have a job the filter matches, whether they are
// subscribed or not. The page and the cursor of the filter play no part: a client
// is asked about the same workspaces on every page of one list.
func (s *Store) Workspaces(ctx context.Context, f ListFilter) ([]string, error) {
	conditions, args := f.where()
	query := "SELECT DISTINCT workspace FROM af_jobs"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	rows, err := s.db.QueryContext(ctx, query+"", args...)
	if err != nil {
		return nil, fmt.Errorf("workspaces with jobs: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, fmt.Errorf("workspaces with jobs: %w", err)
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}

// List is a page of the jobs of the workspaces that the filter matches, newest first by
// created_at and then by id. One more job than the page holds is read, to know whether there
// is a page after it; the cursor is the position of the last job of this one.
func (s *Store) List(ctx context.Context, workspaces []string, f ListFilter) (Page, error) {
	page := Page{Jobs: []Doc{}}
	limit := f.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	conditions, args := f.where()
	in := make([]any, len(workspaces))
	for i, slug := range workspaces {
		in[i] = slug
	}
	conditions = append([]string{"workspace IN (" + placeholders(len(workspaces)) + ")"}, conditions...)
	args = append(in, args...)
	if f.after != nil {
		conditions = append(conditions, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, f.after.CreatedAt, f.after.CreatedAt, f.after.ID)
	}
	args = append(args, limit+1)

	// The jobs and their attempts are read from one snapshot, so that no entry shows a job in
	// a state that its attempts do not bear out.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return page, fmt.Errorf("list of jobs: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx,
		"SELECT "+jobColumns+" FROM af_jobs WHERE "+strings.Join(conditions, " AND ")+" ORDER BY created_at DESC, id DESC LIMIT ?", args...)
	if err != nil {
		return page, fmt.Errorf("list of jobs: %w", err)
	}
	var found []jobRow
	for rows.Next() {
		var j jobRow
		if err := j.scan(rows); err != nil {
			_ = rows.Close()
			return page, fmt.Errorf("list of jobs: %w", err)
		}
		found = append(found, j)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return page, fmt.Errorf("list of jobs: %w", err)
	}
	if len(found) > limit {
		found = found[:limit]
		last := found[limit-1]
		next := position{CreatedAt: last.CreatedAt, ID: last.ID}.cursor()
		page.NextCursor = &next
	}
	attempts, err := attemptsOf(ctx, tx, found)
	if err != nil {
		return page, err
	}
	for _, j := range found {
		doc, err := buildDoc(j, attempts[j.ID], false)
		if err != nil {
			return page, err
		}
		page.Jobs = append(page.Jobs, doc)
	}
	return page, nil
}

// attemptsOf reads the attempts of the jobs, in the order of their numbers, by job.
func attemptsOf(ctx context.Context, q querier, found []jobRow) (map[string][]attemptRow, error) {
	out := make(map[string][]attemptRow, len(found))
	ids := make([]any, len(found))
	for i, j := range found {
		ids[i] = j.ID
	}
	rows, err := q.QueryContext(ctx,
		"SELECT "+attemptColumns+" FROM af_attempts WHERE job_id IN ("+placeholders(len(ids))+") ORDER BY job_id, n", ids...)
	if err != nil {
		return nil, fmt.Errorf("attempts of the jobs listed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var a attemptRow
		if err := a.scan(rows); err != nil {
			return nil, fmt.Errorf("attempts of the jobs listed: %w", err)
		}
		out[a.JobID] = append(out[a.JobID], a)
	}
	return out, rows.Err()
}

// Workspaces are the workspaces that have a job the filter matches: the ones whose visibility
// a list across workspaces has to settle.
func (s *Service) Workspaces(ctx context.Context, f ListFilter) ([]string, error) {
	return s.store.Workspaces(ctx, f)
}

// List is a page of the jobs of the workspaces, as the routes show them: one document for
// each job, without its attempts. Whether the caller sees the workspaces is for the caller
// to have asked.
func (s *Service) List(ctx context.Context, workspaces []string, f ListFilter) (Page, error) {
	return s.store.List(ctx, workspaces, f)
}
