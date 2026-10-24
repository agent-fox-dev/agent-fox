// Package afcmd holds the commands of af: the triage and fix loops, the ring-1 commands
// that measure a tree, and the submit and land commands, with the schemas of their
// results. Every command speaks the contract that internal/toolio implements. The package
// was in cmd/af; it is importable so that agent-fox can run the loops in its own process,
// with the forge, the boxes, the brains, the git runner and the earlier questions of its
// own, and without reading the process environment on behalf of an attempt.
package afcmd

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"os"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/fix"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage"
)

// Config is what a caller of New supplies for the App it makes: the identity of the
// program, and the values that the commands would otherwise take from package-level
// variables and from the process environment. The zero value is the af binary: the
// forge of the environment, box.Default, brain.Default, the process environment, git as
// the process runs it, and the earlier questions found in the run directories.
//
// An App serves one invocation at a time: its commands keep the flags of the invocation
// in closures. A caller that runs invocations at the same time makes an App and a Config
// for each, and the Apps share nothing but what the Configs hold.
type Config struct {
	// Version, Commit and BuildTime identify the build of the program.
	Version, Commit, BuildTime string
	// Forge builds the forge client of a command that uses one; nil is
	// issuex.NewWithOptions.
	Forge func(issuex.Options) (issuex.Client, error)
	// Boxes is where the providers of a box are found; nil is box.Default. It may hold
	// providers that af does not offer.
	Boxes *box.Registry
	// Brains is where a brain is found; nil is brain.Default.
	Brains *brain.Registry
	// Brain is what every brain is made from. Its Getenv is the environment that the
	// brains, the box and the settings read, and nil is the process's. Its Model is
	// AF_MODEL when empty, read through Getenv; its AFVersion and AFCommit are the
	// identity of the program when empty.
	Brain brain.Options
	// Git runs the git commands of the fix pipeline, the push among them; nil is
	// gitx.ExecRunner.
	Git gitx.Runner
	// PostPush, when it is set, is called with the branch of a fix once the pipeline has pushed
	// it and before any pull request is asked for. An error from it fails the push: the
	// pipeline asks for no pull request, and the run fails with the branch kept where it is.
	PostPush func(ctx context.Context, branch string) error
	// Earlier says which questions the --context values answer; nil is to search the run
	// directories for them, as af does.
	Earlier *Earlier
	// SandboxProfile names the sandbox profile of a leased box, whose image has to provide the
	// af that a fix measures with, for the failure that says it does not; empty is hub's
	// default profile.
	SandboxProfile string
	// BoxLabels are put on the box a command opens, beside af.run_id and af.tool.
	BoxLabels map[string]string
}

// Earlier is what a caller knows of the questions that the --context values answer, in
// place of the run directories that af searches for them: a clone that was just made has
// none.
type Earlier struct {
	// Questions[i] is the question that the i-th --context value answers, with its
	// options, or nil when that value answers none: it is material from a person, and
	// not an answer to a question that could not be found. A value past the end is
	// material too.
	Questions []*toolio.Question
}

// question is the question the i-th --context value answers, or nil.
func (e *Earlier) question(i int) *toolio.Question {
	if e == nil || i >= len(e.Questions) {
		return nil
	}
	return e.Questions[i]
}

// New returns the shell with the commands registered, for the program and the values of
// cfg. The commands read cfg when they run, as they read the variables of the process in
// af, so a caller changes it between two invocations and never during one; the shell's
// own fields (Version, Commit, BuildTime and NewForge) are copied from it now.
func New(cfg *Config) *toolio.App {
	app := &toolio.App{Version: cfg.Version, Commit: cfg.Commit, BuildTime: cfg.BuildTime, NewForge: cfg.Forge}
	register(app, cfg)
	return app
}

// getenv is the environment that the brains and the settings of the invocation read.
func (c *Config) getenv() brain.Getenv {
	if c.Brain.Getenv != nil {
		return c.Brain.Getenv
	}
	return os.LookupEnv
}

// boxes is the registry that box providers are found in.
func (c *Config) boxes() *box.Registry {
	if c.Boxes != nil {
		return c.Boxes
	}
	return box.Default
}

// brains is the registry that brains are found in.
func (c *Config) brains() *brain.Registry {
	if c.Brains != nil {
		return c.Brains
	}
	return brain.Default
}

// git is the runner of the git commands of the fix pipeline.
func (c *Config) git() gitx.Runner {
	if c.Git != nil {
		return c.Git
	}
	return gitx.ExecRunner
}

// earlierOf is the question that each of the answers (the --context values) answers: the
// ones the caller supplied, or the ones the run directories of dir hold.
func (c *Config) earlierOf(dir, key string, answers []string, exclude string) []fix.Earlier {
	if c.Earlier == nil {
		return fix.FindEarlier(dir, key, answers, exclude)
	}
	earlier := make([]fix.Earlier, len(answers))
	for i, answer := range answers {
		earlier[i] = fix.Earlier{Answer: answer, Material: true}
		if q := c.Earlier.question(i); q != nil {
			earlier[i].Question, earlier[i].Options, earlier[i].Found, earlier[i].Material = q.Text, q.Options, true, false
		}
	}
	return earlier
}

// suppliedEarlier is earlierOf for the loop, which searches the run directories itself
// when it is given none: nil when the caller supplied no questions.
func (c *Config) suppliedEarlier(answers []string) []fix.Earlier {
	if c.Earlier == nil {
		return nil
	}
	return c.earlierOf("", "", answers, "")
}

// previousQuestion is the question that the brief of a triage resumes: the question of the
// last of the --context values (n of them) that the caller supplied one for, or the newest
// the run directories of dir hold for the fingerprint.
func (c *Config) previousQuestion(dir, fingerprint, exclude string, n int) *triage.Question {
	if c.Earlier == nil {
		return triage.FindPreviousQuestion(dir, fingerprint, exclude)
	}
	for i := n - 1; i >= 0; i-- {
		if q := c.Earlier.question(i); q != nil {
			return &triage.Question{Text: q.Text, Options: q.Options}
		}
	}
	return nil
}

// materialFlags says, for each of the n --context values, whether the caller supplied no
// question for it, so that the brief frames it as material from a person and not as an answer.
// Without supplied questions af frames every value as an answer, and none is marked.
func (c *Config) materialFlags(n int) []bool {
	if c.Earlier == nil {
		return nil
	}
	flags := make([]bool, n)
	for i := range flags {
		flags[i] = c.Earlier.question(i) == nil
	}
	return flags
}

// brainOptions are the options every brain is made from: the model of AF_MODEL when
// the options name none, and which af this is, so that the af of an image that is
// another can be told.
func (c *Config) brainOptions() brain.Options {
	opts := c.Brain
	opts.Getenv = c.getenv()
	if opts.Boxes == nil {
		opts.Boxes = c.boxes()
	}
	if opts.Model == "" {
		opts.Model, _ = opts.Getenv(modelVar)
	}
	if opts.AFVersion == "" && opts.AFCommit == "" {
		opts.AFVersion, opts.AFCommit = (&toolio.App{Version: c.Version, Commit: c.Commit, BuildTime: c.BuildTime}).Identity()
	}
	return opts
}

// modelVar is the variable that names the model of the brains.
const modelVar = "AF_MODEL"

// schemaFiles are the JSON Schemas of the commands' results, embedded in the
// binary so that af --schema prints them.
//
//go:embed schemas/*.json
var schemaFiles embed.FS

// resultSchema is the embedded schema schemas/<name>.json. A missing file is
// a mistake in the build, which a test finds, and not a condition to handle.
func resultSchema(name string) json.RawMessage {
	data, err := schemaFiles.ReadFile("schemas/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("the result schema %q is not embedded: %v", name, err))
	}
	return data
}

// register registers the commands of the program on app.
func register(app *toolio.App, c *Config) {
	registerLang(app)
	registerGate(app)
	registerRevertCheck(app)
	registerTriage(app, c)
	registerFix(app, c)
	registerFixRun(app, c)
}
