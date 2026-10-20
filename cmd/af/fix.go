package main

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// fixRegistry is where the fix commands find the brain they were told to use. A
// test puts a brain of its own in it.
var fixRegistry = brain.Default

// fixBrainOptions are what the brain of a run is made of: the model from AF_MODEL,
// and the provider and the credentials the brain finds for itself. A test
// replaces it with a brain on the faux provider.
var fixBrainOptions = func() brain.Options { return brain.Options{Model: os.Getenv("AF_MODEL")} }

// fixGate runs the checks of the baseline; a test counts the runs with its own.
var fixGate = checks.Run

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
func registerFix(app *toolio.App) {
	var o fixOptions
	app.Register(toolio.Command{
		Path:    "fix",
		Summary: "Fix a reported problem: a verified change on a branch, and a pull request",
		Usage:   "af fix <issue|file|text|-> [--dir DIR] [--base BRANCH] [--context ANSWER]... [--repo owner/name] [--land pr|branch|none] [--attempts N] [--no-verify] [--dry-run] [--brain NAME] [--sandbox NAME] [--budget USD] [--max-turns N] [--timeout 45m]",
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
		Run:            func(r *toolio.Run) (toolio.Outcome, error) { return fixLoop(r, o) },
	})
}

// The ledger record of the question a run ended on.
const ledgerFixQuestion = "fix.question"

// fixLoop is a fix run from the report to the pull request.
func fixLoop(r *toolio.Run, o fixOptions) (toolio.Outcome, error) {
	if err := refusePullRequest(r); err != nil {
		return toolio.Outcome{}, err
	}
	if err := o.settings.Validate(); err != nil {
		return toolio.Outcome{}, err
	}
	entry, err := fixRegistry.Lookup(o.settings.Brain)
	if err != nil {
		return toolio.Outcome{}, err
	}
	boxEntry, err := box.Default.Lookup(o.settings.Sandbox)
	if err != nil {
		return toolio.Outcome{}, err
	}

	opts := fixBrainOptions()
	opts.Script = o.settings.Script
	pre, err := fix.Preflight(r.Ctx, fix.PreflightParams{
		Dir: r.Dir, Base: o.base, Land: string(o.land), NoVerify: o.noVerify, DryRun: r.DryRun,
		Forge: r.Forge, Target: r.Target, Warn: r.Warn, Gate: fixGate,
		NewBrain: func() (brain.Brain, error) { return entry.New(opts) },
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	warnIssueClosed(r)
	start, err := startRun(r, pre, o.noVerify)
	if err != nil {
		return toolio.Outcome{}, err
	}

	state := start.state
	var ended fix.LoopResult
	var info fix.BoxInfo
	err = box.With(r.Ctx, box.Request{
		Provider: o.settings.Sandbox, HostDir: r.Dir, RunID: r.RunDir.ID, Tool: "fix",
		Env: brain.BuildBoxEnv(entry, opts.Getenv), Record: r.RunDir.Record, Warn: r.Warn,
	}, func(sess *box.Session) error {
		info = fix.NewBoxInfo(boxEntry, sess.Spec())
		var err error
		ended, err = fix.RunLoop(r.Ctx, fix.LoopParams{
			Dir: r.Dir, Git: pre.Git, Session: sess, Name: o.settings.Brain, Brain: pre.Brain, Tally: &brain.Tally{}, RunDir: r.RunDir, Report: r, Reporter: r,
			State: &state, Prefix: start.prefix.Text, Attempts: int(o.attempts), Budget: o.settings.Budget, MaxTurns: o.settings.MaxTurns, Gate: fixGate,
		})
		return err
	})
	if err != nil {
		return toolio.Outcome{}, err
	}
	return endFixLoop(r, o, state, ended, info)
}

// endFixLoop turns what the rounds came to into the outcome of the run. A question is the
// end of the run, with nothing applied to the checkout.
func endFixLoop(r *toolio.Run, o fixOptions, state fix.State, ended fix.LoopResult, info fix.BoxInfo) (toolio.Outcome, error) {
	if q := ended.Question; q != nil {
		record(r, ledgerFixQuestion, q)
		result := fix.NewResult(fix.ResultInput{
			State: state, Attempts: ended.Attempts, Rounds: ended.Rounds, AttemptsMax: int(o.attempts), Outcome: fix.OutcomeNeedsHuman, Land: string(o.land), Box: &info,
		})
		return toolio.Outcome{
			Status: toolio.StatusNeedsHuman, Summary: "the brain asks a question, and nothing was applied to the checkout",
			Result: result, NeedsHuman: &toolio.Question{Text: q.Text, Options: q.Options},
		}, nil
	}
	return toolio.Outcome{}, &toolio.Failure{Category: toolio.CategoryInternal, Message: "the endings of the fix loop are not wired"}
}
