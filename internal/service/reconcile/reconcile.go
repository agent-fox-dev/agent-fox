// Package reconcile gives back the sandboxes that an attempt of the worker leased and could not release, because the
// process died. A pass looks at every subscribed workspace, and runs when the service starts and every 15 minutes after
// that. (A hub session that an attempt left open is completed by the next delivery of the attempt, which goes on in it.)
package reconcile

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
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
}

// Jobs is what a pass asks of the jobs layer: whether it has a job, and whether an attempt is in a runner of this
// process.
type Jobs interface {
	Known(ctx context.Context, jobID string) (bool, error)
	Running(jobID string, attempt int) bool
}

// Config is what a Reconciler is made of.
type Config struct {
	Subscriptions Subscriptions
	Hub           Hub
	Jobs          Jobs
	Logger        *slog.Logger
	// Interval is the time between two passes; DefaultInterval when 0. Tests make it short.
	Interval time.Duration
}

// Reconciler makes the passes.
type Reconciler struct {
	cfg Config
	mu  sync.Mutex
}

// New makes the Reconciler.
func New(cfg Config) *Reconciler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Reconciler{cfg: cfg}
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
// are released. A workspace that cannot be asked is logged and left to the next pass.
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
	for _, sub := range subs {
		if ctx.Err() != nil {
			return
		}
		pat, err := r.cfg.Subscriptions.Token(ctx, sub.Slug)
		if err != nil {
			r.cfg.Logger.Warn("a workspace is left to the next pass: its token cannot be read", "workspace", sub.Slug, "error", err.Error())
			continue
		}
		r.leases(ctx, sub.Slug, pat)
	}
}

// leases releases the leases of the workspace that no attempt carries out any more.
func (r *Reconciler) leases(ctx context.Context, slug, pat string) {
	held, err := r.cfg.Hub.ListSandboxes(ctx, pat, slug)
	if err != nil {
		r.failed(ctx, slug, pat, "the sandboxes cannot be listed", err)
		return
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

// failed logs a call to hub that failed, unless the pass was stopped, with the workspace and without the token.
func (r *Reconciler) failed(ctx context.Context, slug, pat, what string, err error) {
	if ctx.Err() != nil {
		return
	}
	r.cfg.Logger.Warn("a workspace is left to the next pass: "+what, "workspace", slug, "error", strings.ReplaceAll(err.Error(), pat, "***"))
}
