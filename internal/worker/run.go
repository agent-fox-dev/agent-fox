package worker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// inputDirName is where the file of a job is written, in the attempt directory.
const inputDirName = "input"

// run runs the loop of the job in this process, as the af command it is, in the box of the
// lease: the forge is hub's, the repository the workspace's, the git commands the hermetic ones of
// the clone, the box providers the gateway of the lease alone, and the checks of the repository
// are measured in the box. Nothing is read from the environment of the process: the settings come
// as flags and the model in the options of the brain. The loop is not stopped by a signal, only by
// the context of the attempt, and every line of its events is passed on as it is written.
func (r *Runner) run(ctx context.Context, a *attempt) *outcome {
	a.step("run")
	args, stdin, refused := r.loopArgs(a)
	if refused != nil {
		return r.envelopeOf(a, refused)
	}
	var stdout, stderr bytes.Buffer
	r.loopApp(a).Main(ctx, args, stdin, &stdout, &stderr)
	if a.aborted != "" { // the shell recovered the death of the process that a test made: it goes on
		panic(stageAbort{a.aborted})
	}
	return r.loopOutcome(ctx, a, stdout.Bytes())
}

// loopApp is the shell the loop is run by: not stopped by a signal, with the run id of the
// session, the workspace's repository as the one every command works on, the clock and the source of
// randomness of the worker, and a sink that passes the events on.
func (r *Runner) loopApp(a *attempt) *toolio.App {
	app := afcmd.New(r.loopConfig(a))
	app.NoSignals = true
	app.RunID = a.runID
	app.Target = a.repo
	app.EventSink = a.eventSink(r.cfg.Now)
	app.LedgerSink = r.ledgerSink(a)
	app.Now, app.Rand = r.cfg.Now, r.cfg.Rand
	return app
}

// loopConfig is what the loop is made of: the seams of afcmd, from the hub and the lease.
func (r *Runner) loopConfig(a *attempt) *afcmd.Config {
	questions := make([]*toolio.Question, 0, len(a.req.Context)+len(a.req.Answers))
	for range a.req.Context {
		questions = append(questions, nil) // material from a person, which answers no question
	}
	for _, answer := range a.req.Answers {
		options := make([]string, len(answer.Options))
		for i, o := range answer.Options {
			options[i] = o.Label
		}
		questions = append(questions, &toolio.Question{Text: answer.Question, Options: options})
	}
	return &afcmd.Config{
		Version: r.cfg.Version, Commit: r.cfg.Commit, BuildTime: r.cfg.BuildTime,
		Forge:     r.cfg.Hub.ForgeFactory(a.pat, a.req.Workspace, a.repo),
		Boxes:     a.boxes,
		Brain:     brain.Options{Model: a.settings.Model, Getenv: nothing},
		Git:       a.git,
		PostPush:  r.publishBranch(a),
		Landing:   r.landingOf(a),
		Earlier:   &afcmd.Earlier{Questions: questions},
		InBox:     &afcmd.InBox{Profile: a.settings.Profile},
		BoxLabels: a.boxLabels,
	}
}

// landingOf is how the loop tells the journal about the landing of a fix.
func (r *Runner) landingOf(a *attempt) *afcmd.Landing {
	l := r.landing(a)
	return &afcmd.Landing{Plan: l.plan, Pushed: l.pushed, Published: l.published, PullRequest: l.pullRequest}
}

// nothing is the environment of the loop: it has none.
func nothing(string) (string, bool) { return "", false }

// loopArgs is the invocation of the loop: the command, the input (the URL of the issue, a "-"
// with the text on stdin, or the path of the file under input/), the clone, the gateway box, the
// settings as flags, and the context values, the strings of the request and then the answers, in
// order. The flags a test chose come last.
func (r *Runner) loopArgs(a *attempt) (args []string, stdin io.Reader, refused *refusal) {
	args = []string{"af", string(a.req.Kind)}
	stdin = strings.NewReader("")
	switch a.input.kind {
	case inputIssue:
		args = append(args, a.input.url)
	case inputText:
		args = append(args, "-")
		stdin = strings.NewReader(a.input.text)
	case inputFile:
		path, err := r.writeInput(a)
		if err != nil {
			return nil, nil, err
		}
		args = append(args, path)
	}
	args = append(args, "--dir", a.cloneDir, "--sandbox", box.GatewayName)
	args = append(args, a.settings.flags(a.req.Kind)...)
	for _, c := range a.req.Context {
		args = append(args, "--context="+c)
	}
	for _, answer := range a.req.Answers {
		args = append(args, "--context="+answer.Text)
	}
	if r.loopFlags != nil {
		args = append(args, r.loopFlags(a)...)
	}
	return args, stdin, nil
}

// writeInput writes the file of the job to input/ of the attempt directory under its base name,
// and returns its path. A name that has no base name to write under is a usage refusal.
func (r *Runner) writeInput(a *attempt) (string, *refusal) {
	name := filepath.Base(filepath.FromSlash(a.input.file.Name))
	if name == "." || name == ".." || name == string(filepath.Separator) || strings.ContainsRune(name, 0) {
		return "", usageRefusal("the file %q has no name it can be written under", a.input.file.Name)
	}
	dir := filepath.Join(a.req.Dir, inputDirName)
	if err := os.RemoveAll(dir); err != nil {
		return "", &refusal{category: toolio.CategoryIO, message: fmt.Sprintf("the directory of the input cannot be made: %v", err)}
	}
	if err := os.Mkdir(dir, dirMode); err != nil {
		return "", &refusal{category: toolio.CategoryIO, message: fmt.Sprintf("the directory of the input cannot be made: %v", err)}
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(a.input.file.Content), 0o600); err != nil {
		return "", &refusal{category: toolio.CategoryIO, message: fmt.Sprintf("the file of the input cannot be written: %v", err)}
	}
	return path, nil
}
