package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/worker/recovery"
)

// The steps of the journal, by name: the seam of the tests that make a write fail names one.
const (
	stepBegin        = "begin"
	stepSession      = "session"
	stepClone        = "clone"
	stepRun          = "run"
	stepSpend        = "spend"
	stepLease        = "lease"
	stepRelease      = "release"
	stepEnding       = "ending"
	stepLandingPlan  = "landing_plan"
	stepPushed       = "pushed"
	stepHubPush      = "hub_push"
	stepPullRequest  = "pull_request"
	stepStatusLabels = "mirror_status_labels"
	stepComment      = "mirror_comment"
	stepUsage        = "report_usage"
	stepEvents       = "report_events"
	stepSessionDone  = "report_session"
)

// WarnJournalUnwritable says a write of the journal failed, which the delivery goes on from.
const WarnJournalUnwritable = "journal_unwritable"

// journalHandle is the journal of the attempt as this delivery holds it: the document in memory, which is
// what the delivery goes on from whether it could be written or not, and the file it is written to
// each time a step is done.
type journalHandle struct {
	store *recovery.Store
	fault func(step string) error

	mu     sync.Mutex
	j      recovery.Journal
	warned bool
}

// update changes the journal in memory and then writes it. The change is kept when the write fails.
func (h *journalHandle) update(step string, change func(*recovery.Journal)) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	change(&h.j)
	if h.fault != nil {
		if err := h.fault(step); err != nil {
			return err
		}
	}
	return h.store.Write(h.j)
}

// note records a step in the journal. A write that fails is the warning journal_unwritable, once in
// the delivery, and the delivery goes on from what it holds in memory.
func (a *attempt) note(step string, change func(*recovery.Journal)) {
	if a.journal == nil {
		return
	}
	if err := a.journal.update(step, change); err != nil {
		a.journal.unwritable(a, step, err)
	}
}

// noteRequired records a step in the journal and says if it could not be written, for a step that the
// delivery does not go on without.
func (a *attempt) noteRequired(step string, change func(*recovery.Journal)) error {
	if a.journal == nil {
		return errors.New("the attempt has no journal")
	}
	return a.journal.update(step, change)
}

// unwritable raises the warning of a write that failed, the first time in the delivery.
func (h *journalHandle) unwritable(a *attempt, step string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.warned {
		return
	}
	h.warned = true
	a.warnings = append(a.warnings, toolio.Warning{Code: WarnJournalUnwritable, Message: fmt.Sprintf(
		"the journal of the attempt could not be written at the step %s, so the delivery goes on from what it holds: %v", step, err)})
}

// ending records how the delivery ended.
func (a *attempt) ending(ending string) {
	a.note(stepEnding, func(j *recovery.Journal) {
		if n := len(j.Deliveries); n > 0 {
			j.Deliveries[n-1].Ending = ending
		}
	})
}

// endingOf is how a delivery that returned the outcome ended.
func endingOf(how stop, out *outcome) string {
	switch {
	case how == stoppedByShutdown:
		return recovery.EndingShutdown
	case jobs.IsTransient(out.err):
		return recovery.EndingTransient
	}
	return recovery.EndingOutcome
}

// noteSession records the hub session of the attempt.
func (a *attempt) noteSession() {
	a.note(stepSession, func(j *recovery.Journal) { j.Session = recovery.Session{ID: a.sessionID, RunID: a.runID} })
}

// noteLease records the lease the attempt took.
func (a *attempt) noteLease() {
	l := recovery.Lease{SandboxID: a.lease.ID, ExpiresAt: a.lease.ExpiresAt}
	a.note(stepLease, func(j *recovery.Journal) { j.Leases = append(j.Leases, l) })
}

// noteRelease records that the lease was given back.
func (a *attempt) noteRelease() {
	id := a.lease.ID
	a.note(stepRelease, func(j *recovery.Journal) {
		for i := range j.Leases {
			if j.Leases[i].SandboxID == id {
				j.Leases[i].Released = true
			}
		}
	})
}

// noteRun records the run of the loop and the base the fix is made on, once the loop has written what
// its preflight and its brief found.
func (a *attempt) noteRun() {
	run := recovery.Run{ID: a.runID}
	if a.req.Kind == jobs.KindFix {
		if st, err := fix.LoadState(filepath.Join(a.cloneDir, ".agent-fox", "run", a.runID)); err == nil {
			run.Base, run.BaseHead = st.Base, st.BaseHead
		}
	}
	a.note(stepRun, func(j *recovery.Journal) { j.Run = run })
}

// landing is what the loop tells the journal about the landing of a fix.
func (r *Runner) landing(a *attempt) *landingSteps {
	return &landingSteps{r: r, a: a}
}

// landingSteps are the steps of a landing, as the loop is told them.
type landingSteps struct {
	r *Runner
	a *attempt
}

// plan records the plan of the landing. A plan that cannot be written stops the landing.
func (l *landingSteps) plan(_ context.Context, p fix.Plan, warnings []toolio.Warning) error {
	a := l.a
	plan := recovery.Plan{
		Branch: p.Branch, Commit: p.Commit, Outcome: p.Outcome, Status: string(p.Status), Verdict: p.Verdict, Closes: p.Closes,
		Title: a.secretFree(p.Title), Body: a.secretFree(p.Body), Draft: p.Draft,
	}
	for _, w := range warnings {
		plan.Warnings = append(plan.Warnings, recovery.Warning{Code: w.Code, Message: a.secretFree(w.Message)})
	}
	result, err := json.Marshal(p.Result)
	if err != nil {
		return err
	}
	plan.Result = json.RawMessage(a.secretFree(string(result)))
	if err := a.noteRequired(stepLandingPlan, func(j *recovery.Journal) { j.Landing.Plan = &plan }); err != nil {
		return err
	}
	l.r.reached(a, "plan_recorded")
	return nil
}

func (l *landingSteps) pushed(string) {
	l.a.note(stepPushed, func(j *recovery.Journal) { j.Landing.Pushed = true })
	l.r.reached(l.a, "pushed")
}

func (l *landingSteps) published(string) {
	l.a.note(stepHubPush, func(j *recovery.Journal) { j.Landing.HubPush = true })
}

func (l *landingSteps) pullRequest(ref fix.PullRequestRef) {
	l.a.note(stepPullRequest, func(j *recovery.Journal) { j.Landing.PullRequest = &recovery.Ref{Number: ref.Number, URL: ref.URL} })
	l.r.reached(l.a, "pull_request")
}

// refreshSpend records in the journal what the brain runs of the attempt have cost so far: the spend carried
// over from run directories that were removed, and the runs the ledger of the run records.
func (a *attempt) refreshSpend() {
	run, err := toolio.OpenRunDir(a.cloneDir, a.runID)
	if err != nil {
		return
	}
	ledger, err := ledgerSpend(run)
	if err != nil {
		return
	}
	a.note(stepSpend, func(j *recovery.Journal) { j.Spend = addSpend(j.Carried, ledger) })
}

// ledgerSink is told every record of the ledger of the run, which is how the worker follows what the
// loop does in its own process: the run it makes, the submission it accepts, the tree it applies.
func (r *Runner) ledgerSink(a *attempt) func(kind string, data json.RawMessage) {
	return func(kind string, _ json.RawMessage) {
		switch kind {
		case "fix.brief", "triage.brief":
			a.noteRun()
			r.reached(a, "briefed")
		case "brain_end":
			a.refreshSpend()
		case "submission_accepted":
			r.reached(a, "submission_accepted")
		case fix.LedgerApply:
			r.reached(a, "applied")
		}
	}
}

// reached is the point where a test stops a delivery as a process that died at the stage does, see
// stageAbort. The panic is the death: it unwinds past the cleanup, and the shell of the loop, which
// recovers it, is asked afterwards.
func (r *Runner) reached(a *attempt, stage string) {
	if r.abortAfter == "" {
		return
	}
	name, nth := r.abortAfter, 1 // "applied#2" is the second time the stage is reached
	if i := strings.LastIndex(name, "#"); i >= 0 {
		if n, err := strconv.Atoi(name[i+1:]); err == nil && n > 0 {
			name, nth = name[:i], n
		}
	}
	if name != stage {
		return
	}
	if a.stagesReached == nil {
		a.stagesReached = map[string]int{}
	}
	if a.stagesReached[stage]++; a.stagesReached[stage] != nth {
		return
	}
	a.aborted = stage
	panic(stageAbort{stage})
}

// credentials are what must not be written down: a URL with user information, and an Authorization
// header and what follows it on its line.
var (
	userinfoInURL = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s"']+@`)
	authorization = regexp.MustCompile(`(?i)authorization:[^\n"]*`)
)

// secretFree is the text without the PAT, the token of the lease, the user information of a URL and
// an Authorization header.
func (a *attempt) secretFree(text string) string {
	text = redact(text, a.pat)
	if a.lease != nil && a.lease.Token != "" {
		text = strings.ReplaceAll(text, a.lease.Token, "***")
	}
	text = userinfoInURL.ReplaceAllString(text, "$1")
	return authorization.ReplaceAllString(text, "authorization: ***")
}

// stripUserinfo is the URL without the user information it may carry.
func stripUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return userinfoInURL.ReplaceAllString(raw, "$1")
	}
	u.User = nil
	return u.String()
}
