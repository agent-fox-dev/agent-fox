package afcmd

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/conform"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The defaults of the fix commands.
const (
	fixDefaultBudget   = 10
	fixDefaultMaxTurns = 150
	fixDefaultAttempts = 2
	fixMaxAttempts     = 5
	fixDefaultTimeout  = 45 * time.Minute
	// fixGrace is how long a run that is told to stop has to put the tree back and
	// park what it holds: the parking itself is bounded to 10 seconds.
	fixGrace = 30 * time.Second
)

// attemptsFlag is --attempts, which has to be from 1 to 5: a value out of range
// is refused when the flags are parsed, before anything is read or asked.
type attemptsFlag int

func (a *attemptsFlag) String() string { return strconv.Itoa(int(*a)) }

func (a *attemptsFlag) Set(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > fixMaxAttempts {
		return fmt.Errorf("--attempts must be a whole number from 1 to %d, not %q", fixMaxAttempts, v)
	}
	*a = attemptsFlag(n)
	return nil
}

// landFlag is --land: pr, branch or none.
type landFlag string

func (l *landFlag) String() string { return string(*l) }

func (l *landFlag) Set(v string) error {
	if !slices.Contains([]string{fix.LandPR, fix.LandBranch, fix.LandNone}, v) {
		return fmt.Errorf("--land must be pr, branch or none, not %q", v)
	}
	*l = landFlag(v)
	return nil
}

// fixOptions are the flags of af fix that are its own.
type fixOptions struct {
	settings *brain.Settings
	base     string
	attempts attemptsFlag
	land     landFlag
	noVerify bool
}

// registerFix registers af fix.
func registerFix(app *toolio.App, c *Config) {
	var o fixOptions
	app.Register(toolio.Command{
		Path:    "fix",
		Summary: "Fix a reported problem: a verified change on a branch, and a pull request",
		Usage:   "af fix <issue|file|text|-> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name] [--land pr|branch|none] [--attempts N] [--no-verify] [--dry-run] [--brain NAME] [--sandbox NAME] [--borrowed-hands] [--budget USD] [--max-turns N] [--timeout 45m]",
		Flags: func(fs *flag.FlagSet) {
			o = fixOptions{attempts: fixDefaultAttempts, land: fix.LandPR}
			o.settings = brain.Flags(fs, brain.Defaults{Budget: fixDefaultBudget, MaxTurns: fixDefaultMaxTurns})
			fs.StringVar(&o.base, "base", "", baseUsage)
			fs.Var(&o.attempts, "attempts", "up to `n` attempts, each with a budget of its own; from 1 to 5")
			fs.Var(&o.land, "land", "where the change goes, as a `mode`: pr (push the branch and open a pull request), branch (push it), or none (keep it on a local branch)")
			fs.BoolVar(&o.noVerify, "no-verify", false, "land without the gate: no baseline, no check after, a draft that says Refs")
		},
		Input:          toolio.InputRequired,
		UsesForge:      true,
		UsesRunDir:     true,
		Context:        true,
		Repo:           true,
		DryRun:         true,
		Timeout:        true,
		DefaultTimeout: fixDefaultTimeout,
		Grace:          fixGrace,
		ResultSchema:   resultSchema("fix"),
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixLoop(c, r, o) },
	})
}

// The ledger record of the question a run ended on.
const ledgerFixQuestion = "fix.question"

// fixLoop is a fix run from the report to the pull request.
func fixLoop(c *Config, r *toolio.Run, o fixOptions) (toolio.Outcome, error) {
	if err := refusePullRequest(r); err != nil {
		return toolio.Outcome{}, err
	}
	if err := o.settings.ValidateIn(c.getenv(), c.boxes()); err != nil {
		return toolio.Outcome{}, err
	}
	entry, err := c.brains().Lookup(o.settings.Brain)
	if err != nil {
		return toolio.Outcome{}, err
	}
	boxEntry, err := c.boxes().Lookup(o.settings.Sandbox)
	if err != nil {
		return toolio.Outcome{}, err
	}

	opts := c.brainOptions()
	opts.Script, opts.Provider, opts.BorrowedHands = o.settings.Script, o.settings.Sandbox, o.settings.BorrowedHands
	a := &fixAttempt{c: c, r: r, o: o, entry: entry, boxEntry: boxEntry, opts: opts}
	if c.Resume != nil && c.Resume.Submitted {
		if err := a.askAgain(); err != nil {
			return toolio.Outcome{}, err
		}
		return a.finish()
	}
	if c.InBox == nil {
		// On the host: the checks of the repository run here, before the box is opened.
		if err := a.begin(measuring{gate: c.gate(), conform: c.conform()}); err != nil {
			return toolio.Outcome{}, err
		}
	}
	err = box.With(r.Ctx, fixBoxRequest(c, r, r.RunDir, entry, boxEntry, o.settings, opts), func(sess *box.Session) error {
		a.info = fix.NewBoxInfo(sess)
		m := measuring{gate: c.gate(), conform: c.conform()}
		if c.InBox != nil {
			// In the box: it is opened before the detection, serves the whole attempt, and
			// is where the checks of the repository run.
			measurer := a.measurer(sess)
			m = measuring{detect: measurer.Detect, gate: measurer.Gate, conform: measurer.RevertCheck}
			if a.prepareErr = a.begin(m); a.prepareErr != nil {
				return nil
			}
		}
		if c.Resume != nil && c.Resume.Applied {
			a.landed, a.landedErr = a.landApplied(m)
			return nil
		}
		a.loop(sess, m)
		return nil
	})
	if err != nil { // the box could not be opened: nothing was done
		return toolio.Outcome{}, err
	}
	if a.prepareErr != nil {
		return toolio.Outcome{}, a.prepareErr
	}
	if c.Resume != nil && c.Resume.Applied {
		return a.landed, a.landedErr
	}
	return a.finish()
}

// finish turns what the loop came to, or the submission a delivery carried on from, into the end of the run.
func (a *fixAttempt) finish() (toolio.Outcome, error) {
	c, r, o := a.c, a.r, a.o
	pre, state, ended, loopErr, info := a.pre, a.state, a.ended, a.loopErr, a.info
	used := &info
	if c.Resume != nil && c.Resume.Submitted {
		used = nil // no box was opened to ask the question
	}

	fin, err := fix.Finish(r.Ctx, fix.FinishParams{
		Git: pre.Git, State: state, Input: fix.Input{Kind: string(r.Input.Kind), Origin: r.Input.Origin, Body: r.Input.Body},
		Mode: string(o.land), DryRun: r.DryRun, Forge: r.Forge, Target: repoOfState(state.Target), Ended: ended, LoopErr: loopErr,
		AttemptsMax: int(o.attempts), Box: used, Reporter: r, WriteReport: r.RunDir.WriteReport,
		PostPush: c.PostPush, Steps: c.landSteps(r),
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	out := toolio.Outcome{Status: fin.Status, Summary: fin.Summary, Result: fin.Result}
	if q := fin.Question; q != nil {
		record(r, ledgerFixQuestion, q)
		out.NeedsHuman = &toolio.Question{Text: q.Text, Options: q.Options}
	}
	return out, nil
}

// measuring is what the checks of a fix run are made with: the detection of the table (the
// host's when nil), the gate, and the revert check.
type measuring struct {
	detect  func(ctx context.Context, dir string, files []string) (project.Detection, error)
	gate    func(ctx context.Context, dir string, o checks.Options) (checks.Result, error)
	conform func(ctx context.Context, dir, since string, o conform.Options) (conform.Result, error)
}

// fixAttempt is what a fix run holds between its steps: the preflight, the start of the run
// and, once the loop has run, how it ended.
type fixAttempt struct {
	c        *Config
	r        *toolio.Run
	o        fixOptions
	entry    brain.Entry
	boxEntry box.Entry
	opts     brain.Options

	pre        fix.Preflighted
	start      startedRun
	state      fix.State
	ended      fix.LoopResult
	loopErr    error
	info       fix.BoxInfo
	prepareErr error
	// landed is how a run that carries on from the tree it applied ended.
	landed    toolio.Outcome
	landedErr error
}

// landApplied measures the change that an earlier delivery applied to the checkout, once, and lands it, or
// parks it when the measuring refuses it: the brain does not run.
func (a *fixAttempt) landApplied(m measuring) (toolio.Outcome, error) {
	r, state := a.r, a.state
	a.c.seededTally(r, a.o.settings.Brain) // the usage of the run is the attempt's, though no brain runs
	got, err := fix.LandRun(r.Ctx, fix.LandRunParams{
		Dir: r.Dir, Git: a.pre.Git, RunID: r.RunDir.ID, RunPath: r.RunDir.Path, State: state, Mode: string(a.o.land),
		Forge: r.Forge, Target: repoOfState(state.Target), Reporter: r,
		Record: func(kind string, data any) { record(r, kind, data) },
		WriteReport: func(text string) error {
			if err := r.RunDir.WriteReport(text); err != nil {
				return err
			}
			r.Artifact(toolio.Artifact{Kind: "report", Path: filepath.Join(r.RunDir.Path, "report.md")})
			return nil
		},
		Gate: m.gate, Conform: m.conform, PostPush: a.c.PostPush, Steps: a.c.landSteps(r),
	})
	if errors.Is(err, fix.ErrNoChange) {
		// The change that was applied is nothing once the protected paths are put back: there is nothing to
		// land or to park, and nothing a person is asked for.
		return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryNoResult, Message: "the change that was applied to the checkout is nothing once the protected paths are put back: there is nothing to land or to park"}
	}
	if err != nil {
		return toolio.Outcome{}, err
	}
	return toolio.Outcome{Status: got.Status, Summary: got.Summary, Result: got.Result}, nil
}

// askAgain ends a run on the question that an earlier delivery was asked, which the host accepted: no box
// is opened and no brain runs.
func (a *fixAttempt) askAgain() error {
	if err := a.resume(); err != nil {
		return err
	}
	a.c.seededTally(a.r, a.o.settings.Brain) // the usage of the run is the attempt's, though no brain runs
	data, err := os.ReadFile(filepath.Join(a.r.RunDir.Path, "submission.json"))
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the run %s cannot be carried on: %v", a.r.RunDir.ID, err)}
	}
	var file submission.File
	var sub fix.Submission
	if err = json.Unmarshal(data, &file); err == nil {
		sub, err = fix.Decode(file.Payload)
	}
	if err != nil || sub.Question == nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the run %s cannot be carried on: its submission is not a question", a.r.RunDir.ID)}
	}
	// The round that asked is counted in fix.json when it ended, and is not when the process died in it; how many
	// attempts there were is not known, and the one that asked is the least.
	a.ended = fix.LoopResult{End: fix.EndQuestion, Question: sub.Question, Attempts: 1, Rounds: max(a.state.Rounds, 1)}
	return nil
}

// begin is the start of the run: the preflight and the brief, or, for a run that is carried on, what
// they left.
func (a *fixAttempt) begin(m measuring) error {
	if a.c.Resume == nil {
		return a.prepare(m)
	}
	if err := a.resume(); err != nil {
		return err
	}
	return a.ensureBaseline(m)
}

// ensureBaseline makes sure that the checkout holds the result of the baseline that fix.json names, which
// every measuring compares a change with. A result is named by the tree, the table and the mode it was run
// with, so the baseline that is run again, on the tree of the base with the pinned table, is the one that
// was lost; the change of the run is set aside for it and put back afterwards. fix.json names what was run.
func (a *fixAttempt) ensureBaseline(m measuring) error {
	r, state := a.r, a.state
	if state.BaselineGateID == "" || checks.Stored(r.Dir, state.BaselineGateID) {
		return nil
	}
	ctx, git := r.Ctx, a.pre.Git
	if head, err := git.RevParse(ctx, "HEAD"); err != nil || head != state.BaseHead {
		return nil // the measuring says where the checkout is, and measures nothing
	}
	stashed, err := git.Stash(ctx)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot set the change of the run aside to run the baseline again: %v", err)}
	}
	res, runErr := m.gate(ctx, r.Dir, checks.Options{Table: state.Table})
	if stashed {
		if err := git.StashPop(context.WithoutCancel(ctx)); err != nil {
			return &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("cannot put the change of the run back after the baseline ran again: %v", err)}
		}
	}
	if runErr != nil {
		return fix.GateError(ctx, "the baseline gate, run again", runErr)
	}
	state.BaselineGateID, state.BaselinePassed = res.ID, res.Passed
	if err := state.Write(r.RunDir.Path); err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot keep the baseline that ran again in fix.json: %v", err)}
	}
	a.state = state
	return nil
}

// resume takes up a run that was begun: its pinned file says the base, the table and the baseline, and
// its brief says the prefix that every round shares. The preflight and the brief are not made again.
func (a *fixAttempt) resume() error {
	r := a.r
	state, err := fix.LoadState(r.RunDir.Path)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the run %s cannot be carried on: %v", r.RunDir.ID, err)}
	}
	document, err := os.ReadFile(filepath.Join(r.RunDir.Path, "brief.md"))
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the run %s cannot be carried on: %v", r.RunDir.ID, err)}
	}
	prefix, _, err := fix.SplitBrief(string(document), state)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryInternal, Message: fmt.Sprintf("the run %s cannot be carried on: %v", r.RunDir.ID, err)}
	}
	b, err := a.entry.New(a.opts)
	if err != nil {
		return err
	}
	a.pre = fix.Preflighted{Git: gitx.New(r.Dir, a.c.git()), Base: state.Base, BaseHead: state.BaseHead, Brain: b}
	a.start = startedRun{state: state, prefix: fix.Prefix{Text: prefix}}
	a.state = state
	return nil
}

// prepare makes the checks that need no model and starts the run: the preflight, with the
// detection and the baseline made by m, and the brief.
func (a *fixAttempt) prepare(m measuring) error {
	c, r, o := a.c, a.r, a.o
	pre, err := fix.Preflight(r.Ctx, fix.PreflightParams{
		Dir: r.Dir, Base: o.base, Land: string(o.land), NoVerify: o.noVerify, DryRun: r.DryRun,
		Forge: r.Forge, Target: r.Target, Warn: r.Warn, Gate: m.gate, Detect: m.detect, GitRunner: c.git(),
		NewBrain: func() (brain.Brain, error) { return a.entry.New(a.opts) },
	})
	if err != nil {
		return err
	}
	warnIssueClosed(r)
	start, err := startRun(c, r, pre, o.noVerify)
	if err != nil {
		return err
	}
	a.pre, a.start, a.state = pre, start, start.state
	return nil
}

// loop runs the rounds in the session, measuring with m.
func (a *fixAttempt) loop(sess *box.Session, m measuring) {
	placement := brain.PlacementOf(a.entry, a.boxEntry, a.o.settings.BorrowedHands)
	tally := a.c.seededTally(a.r, a.o.settings.Brain)
	a.ended, a.loopErr = fix.RunLoop(a.r.Ctx, fix.LoopParams{
		Dir: a.r.Dir, Git: a.pre.Git, Session: sess, Name: a.o.settings.Brain, Brain: a.pre.Brain, Tally: tally, RunDir: a.r.RunDir, Report: a.r, Reporter: a.r,
		State: &a.state, Prefix: a.start.prefix.Text, Attempts: int(a.o.attempts), Budget: a.o.settings.Budget, MaxTurns: a.o.settings.MaxTurns,
		Gate: m.gate, Conform: m.conform, Earlier: a.c.suppliedEarlier(a.state.Context),
		DriverInBox: placement == brain.PlacementBox && !a.entry.OwnHands, CommandOnly: a.entry.CommandRoute,
	})
}

// measurer is the measuring of the run in the box of the session.
func (a *fixAttempt) measurer(sess *box.Session) *fix.BoxMeasurer {
	return &fix.BoxMeasurer{
		Session: sess, Process: brain.NewProcess(a.opts.ProcessConfig()), Profile: a.c.InBox.Profile,
		RunID: a.r.RunDir.ID, Record: a.r.RunDir.Record, Warn: a.r.Warn, Artifact: a.r.Artifact,
	}
}

// fixBoxRequest is the request for the box of a fix run of the brain: what the brain, the
// provider, borrowed hands and the tools of a fix brief (all of the hands') come to.
func fixBoxRequest(c *Config, r *toolio.Run, run *toolio.RunDir, entry brain.Entry, provider box.Entry, s *brain.Settings, opts brain.Options) box.Request {
	deadline, _ := r.Ctx.Deadline()
	return brain.BoxRequest(brain.BoxParams{
		Brain: entry, Provider: provider, BorrowedHands: s.BorrowedHands, Getenv: opts.Getenv, Deadline: deadline,
		Defaulted: s.SandboxSource != brain.SandboxFlag, AFPath: opts.AFPath,
		HostDir: r.Dir, RunID: run.ID, Tool: "fix", Record: run.Record, Warn: r.Warn, Registry: c.boxes(), Labels: c.BoxLabels,
	})
}
