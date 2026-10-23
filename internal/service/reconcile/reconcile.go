// Package reconcile gives back what an attempt of the worker left held when it could not: the sandboxes it leased
// and did not release, and the hub sessions it opened and did not complete. A pass looks at every subscribed
// workspace, and runs when the service starts and every 15 minutes after that.
package reconcile

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// DefaultInterval is how often a pass is made.
const DefaultInterval = 15 * time.Minute

// Subscriptions are the subscribed workspaces and the tokens that open them.
type Subscriptions interface {
	List(ctx context.Context) ([]store.Subscription, error)
	Token(ctx context.Context, slug string) (string, error)
}

// Hub is what a pass asks of hub, with the token of the workspace.
type Hub interface {
	ListSandboxes(ctx context.Context, pat, slug string) ([]hub.LeaseInfo, error)
	ReleaseLease(ctx context.Context, pat, slug, id string) error
	PostUsage(ctx context.Context, pat, slug, sessionID string, u hub.Usage) error
	CompleteSession(ctx context.Context, pat, slug, sessionID, status, message string) error
}

// Jobs is what a pass asks of the jobs layer: whether it has a job, whether an attempt is in a runner of this
// process, which attempts have ended, and where the files of an attempt are.
type Jobs interface {
	Known(ctx context.Context, jobID string) (bool, error)
	Running(jobID string, attempt int) bool
	EndedAttempts(ctx context.Context) ([]jobs.AttemptRef, error)
	AttemptDir(jobID string, attempt int) string
}

// Config is what a Reconciler is made of.
type Config struct {
	Subscriptions Subscriptions
	Hub           Hub
	Jobs          Jobs
	Logger        *slog.Logger
	// Interval is the time between two passes; DefaultInterval when 0. Tests make it short.
	Interval time.Duration
	// JournalFault, when it is set, is asked before each write of a journal, which fails with the error it returns. It
	// is for the tests of a journal that cannot be written.
	JournalFault func() error
}

// Reconciler makes the passes.
type Reconciler struct {
	cfg Config
	mu  sync.Mutex
	// posted and completed are the sessions whose usage this process posted and that it completed, which a journal that
	// could not be written does not say: what a pass has done is not done again by the next.
	posted, completed map[string]bool
}

// New makes the Reconciler.
func New(cfg Config) *Reconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Reconciler{cfg: cfg, posted: map[string]bool{}, completed: map[string]bool{}}
}

// Run makes a pass now, and another every interval, until ctx is done. A pass is made when the one before it is
// over: a tick that comes while a pass runs makes none of its own.
func (r *Reconciler) Run(ctx context.Context) {
	ticker := time.NewTicker(r.cfg.Interval)
	defer ticker.Stop()
	for {
		r.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Pass makes one pass, unless one is running: the leases of every subscribed workspace that the attempts left held
// are released, and then the sessions that the attempts which ended left open are completed. A workspace that cannot
// be asked is logged and left to the next pass.
func (r *Reconciler) Pass(ctx context.Context) {
	if !r.mu.TryLock() {
		return
	}
	defer r.mu.Unlock()
	subs, err := r.cfg.Subscriptions.List(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.cfg.Logger.Warn("the subscriptions cannot be listed, so nothing is reconciled until the next pass", "error", err.Error())
		}
		return
	}
	workspaces := map[string]string{} // the workspaces that can be asked, by their tokens
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		pat, err := r.cfg.Subscriptions.Token(ctx, sub.Slug)
		if err != nil {
			r.cfg.Logger.Warn("a workspace is left to the next pass: its token cannot be read", "workspace", sub.Slug, "error", err.Error())
			continue
		}
		if r.leases(ctx, sub.Slug, pat) {
			workspaces[sub.Slug] = pat
		}
	}
	r.sessions(ctx, workspaces)
}

// leases releases the leases of the workspace that no attempt carries out any more, and says whether the workspace
// could be asked.
func (r *Reconciler) leases(ctx context.Context, slug, pat string) bool {
	held, err := r.cfg.Hub.ListSandboxes(ctx, pat, slug)
	if err != nil {
		r.failed(ctx, slug, pat, "the sandboxes cannot be listed", err)
		return false
	}
	for _, l := range held {
		job, attempt, ok := r.orphan(ctx, l)
		if !ok {
			continue
		}
		if err := r.cfg.Hub.ReleaseLease(ctx, pat, slug, l.ID); err != nil {
			r.failed(ctx, slug, pat, "an orphan sandbox "+l.ID+" cannot be released", err)
			continue
		}
		r.cfg.Logger.Info("released a sandbox that no attempt carries out", "workspace", slug, "sandbox_id", l.ID, "job", job, "attempt", attempt)
	}
	return true
}

// orphan says whether the lease is held by an attempt that nothing carries out: its labels name a job of the
// database and an attempt that is in no runner of this process. A lease that names none, a job that the database
// does not have, or an attempt that is not a number is not an orphan, for it may be someone else's.
func (r *Reconciler) orphan(ctx context.Context, l hub.LeaseInfo) (job string, attempt int, ok bool) {
	job = l.Labels["job"]
	n, err := strconv.Atoi(l.Labels["attempt"])
	if job == "" || err != nil || n < 1 {
		return "", 0, false
	}
	known, err := r.cfg.Jobs.Known(ctx, job)
	if err != nil || !known || r.cfg.Jobs.Running(job, n) {
		return "", 0, false
	}
	return job, n, true
}

// sessions completes the sessions that the attempts which ended left open.
func (r *Reconciler) sessions(ctx context.Context, workspaces map[string]string) {
	ended, err := r.cfg.Jobs.EndedAttempts(ctx)
	if err != nil {
		if ctx.Err() == nil {
			r.cfg.Logger.Warn("the attempts that ended cannot be listed, so no session is completed until the next pass", "error", err.Error())
		}
		return
	}
	failed := map[string]bool{}
	for _, a := range ended {
		pat, ok := workspaces[a.Workspace]
		if !ok || failed[a.Workspace] || ctx.Err() != nil || r.cfg.Jobs.Running(a.JobID, a.Attempt) {
			continue
		}
		store := recovery.NewStore(r.cfg.Jobs.AttemptDir(a.JobID, a.Attempt))
		j, err := store.Load()
		if err != nil || j.Job != a.JobID || j.Attempt != a.Attempt || j.Session.ID == "" || j.Session.Completed {
			continue // no journal, one that does not read, or a session that is closed: nothing to finish
		}
		if !r.complete(ctx, store, j, a, pat) {
			failed[a.Workspace] = true
		}
	}
}

// complete posts the usage that the journal records as spent and not posted, and completes the session with the status of
// the outcome that was stored, or failed when there is none; each step is recorded in the journal when it is done. A
// step that is done and could not be recorded is not done again by a later pass of this process. It says whether hub
// could be asked.
func (r *Reconciler) complete(ctx context.Context, store *recovery.Store, j recovery.Journal, a jobs.AttemptRef, pat string) bool {
	slug, id := a.Workspace, j.Session.ID
	if !j.Report.UsagePosted {
		if u := j.Spend; !r.posted[id] && (u.CostUSD > 0 || u.Turns > 0 || u.InputTokens > 0 || u.OutputTokens > 0) {
			err := r.cfg.Hub.PostUsage(ctx, pat, slug, id, hub.Usage{
				Model: j.Settings.Model, Brain: j.Settings.Brain, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
				CostUSD: u.CostUSD, CostSource: u.CostSource, Turns: u.Turns,
			})
			if err != nil {
				r.failed(ctx, slug, pat, "the usage of the session "+id+" cannot be posted", err)
				return false
			}
		}
		r.posted[id] = true
		j.Report.UsagePosted = true
		r.record(store, j, a)
	}
	if !r.completed[id] {
		status, message := hub.SessionFailed, "the attempt ended without a delivery that finished its report"
		if o, err := store.LoadOutcome(); err == nil && o.Job == a.JobID && o.Attempt == a.Attempt {
			status, message = o.SessionEnd()
		}
		if err := r.cfg.Hub.CompleteSession(ctx, pat, slug, id, status, message); err != nil {
			r.failed(ctx, slug, pat, "the session "+id+" cannot be completed", err)
			return false
		}
		r.completed[id] = true
		r.cfg.Logger.Info("completed the session of an attempt that ended without finishing its report", "workspace", slug, "session_id", id,
			"job", a.JobID, "attempt", a.Attempt, "status", status)
	}
	j.Session.Completed, j.Report.SessionCompleted = true, true
	if r.record(store, j, a) { // the journal says it all: the memory of this process is not needed any more
		delete(r.posted, id)
		delete(r.completed, id)
	}
	return true
}

// record writes the journal, and logs a journal that cannot be written.
func (r *Reconciler) record(store *recovery.Store, j recovery.Journal, a jobs.AttemptRef) bool {
	var err error
	if r.cfg.JournalFault != nil {
		err = r.cfg.JournalFault()
	}
	if err == nil {
		err = store.Write(j)
	}
	if err != nil {
		r.cfg.Logger.Warn("the journal of an attempt cannot be written", "workspace", a.Workspace, "job", a.JobID, "attempt", a.Attempt, "error", err.Error())
		return false
	}
	return true
}

// failed logs a call to hub that failed, unless the pass was stopped, with the workspace and without the token.
func (r *Reconciler) failed(ctx context.Context, slug, pat, what string, err error) {
	if ctx.Err() != nil {
		return
	}
	r.cfg.Logger.Warn("a workspace is left to the next pass: "+what, "workspace", slug, "error", strings.ReplaceAll(err.Error(), pat, "***"))
}
