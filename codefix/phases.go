package codefix

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// The two terminating tools.
const (
	ToolSubmitAnalysis       = "submit_analysis"
	ToolSubmitImplementation = "submit_implementation"
)

// constrainedJSON asks the wire to constrain sampling to the tool's schema
// where it can. StrictPrefer, not StrictRequire: it is honoured on the OpenAI
// wires and ignored on Anthropic's, so it is a helpful narrowing and never
// the thing keeping the arguments well formed — the handler validates either
// way.
var constrainedJSON = &core.ConstrainedSampling{
	Type: core.ConstrainJSONSchema, Strict: core.StrictPrefer,
}

// readOnlyPrograms is the analysis phase's shell allowlist: programs that
// report and do not change anything.
//
// `find` is not here on purpose — its -exec and -delete make it a write tool,
// and find_files covers the reading half. The guard refuses those flags
// anyway, for a phase that adds find back through --allow.
var readOnlyPrograms = []string{"git", "ls", "cat", "head", "tail", "wc", "rg", "grep", "file"}

// buildPrograms is what an implementation phase needs on top of that: the
// toolchains that compile, format and test. The verification command's own
// program is appended at run time, because a phase that cannot run the suite
// it will be judged by is a phase set up to fail.
var buildPrograms = []string{
	"go", "gofmt", "goimports", "make", "npm", "npx", "node", "yarn", "pnpm",
	"python", "python3", "pytest", "uv", "pip", "cargo", "rustfmt",
	"mkdir", "cp", "mv", "sed", "awk", "diff", "sort", "uniq", "touch",
}

// analysisSystemPrompt is the diagnosis mandate.
//
// It is short because most of af-fix's step 4 is control flow — classify the
// issue, check for a dirty tree, derive a branch name — which is Go here. The
// two paragraphs that survive are the ones a model actually needs: fix the
// cause rather than the symptom, and do not stop for anything but a genuine
// contradiction.
const analysisSystemPrompt = `You are a senior engineer diagnosing a problem against a codebase before any code is written.

This phase is read-only. You can read files, search, and run reporting
commands; you cannot write, and you do not need to — a second phase implements
what you decide here.

Method:

1. Read the report and extract its signals: stack frames, error strings, named
   functions and files, and behavioural claims ("X happens when Y").
2. Locate each signal in the code. Read the file, then its callers and callees,
   until you can state the path from trigger to fault.
3. Read the tests for the affected code. What they assert is what the code was
   believed to do, and the gap between that and the report is usually the bug.
4. Separate the symptom from the cause and fix the cause. A band-aid over the
   symptom passes the reporter's test case and leaves the defect in place.
5. Decide the smallest correct change. Name the files it touches and what
   changes in each. If the same bug class appears elsewhere in the code, say so
   — the implementation phase decides whether fixing it belongs in scope.
6. Plan the test that proves it. For a defect, a test that fails now and passes
   after; for a feature, a test that pins the new behaviour.

On ambiguity: use your judgement. Record the small decisions as assumptions and
proceed. Set the ambiguity field ONLY when the report has two or more readings
that lead to fundamentally different changes AND the codebase cannot settle
which was meant — that stops the run and asks a person, which is expensive and
is the right answer perhaps one time in twenty.

When the diagnosis is complete, call submit_analysis exactly once.`

// implementSystemPrompt is the implementation mandate.
//
// The two sentences about git and about the verification command are the ones
// that matter: the model's shell refuses mutating git subcommands, and being
// told that up front saves it a turn discovering it.
const implementSystemPrompt = `You are a senior engineer implementing a fix that has already been diagnosed.

You have the diagnosis and the plan. Implement it.

Method:

1. Write the test first. For a defect, write the test that reproduces it and
   confirm it fails; for a feature, write the test that pins the new behaviour.
   Use the project's existing test framework and conventions — read a
   neighbouring test file rather than inventing a style.
2. Make the minimal, correct change. Follow the conventions already in the
   files you are editing.
3. Introduce nothing unrelated. A "while I was here" cleanup makes the change
   harder to review and harder to revert.
4. Run the project's checks yourself and fix what they report. The command is
   named in your task; you are judged by it afterwards, so there is no benefit
   in leaving it failing.
5. Update the documentation the change makes wrong: a README, a doc comment, a
   configuration reference.

Constraints that are mechanical, not advisory:

- git is limited to its read-only subcommands. The branch already exists and
  you are on it; the commit, the push and the pull request are made by the
  program after this phase, from what you submit. Do not try to commit.
- gh is not available. Every comment on the issue is written by the program.
- Your file tools cannot reach outside the repository.

When the work is done and the checks pass, call submit_implementation exactly
once with an honest report of what you changed. Do not claim a test you did not
write or a check you did not run — both are verified afterwards.`

// analysisSchema is the first phase's contract.
func analysisSchema() *schema.Schema {
	fileChange := schema.Object(
		schema.Prop("path", schema.String("Repository-relative path")),
		schema.Prop("change", schema.String("One line: what changes in this file and why")),
	)
	return schema.Object(
		schema.Prop("classification", schema.Enum(
			"bug: something is broken. feature: something is missing. "+
				"refactor: structure changes, behaviour does not. "+
				"performance: behaviour is right and too slow.", Classifications...)),
		schema.Prop("title", schema.String(
			"Under 70 characters, imperative, naming the defect or the capability. "+
				"It becomes the branch name and the commit subject, so write it as one.")),
		schema.Prop("summary", schema.String("1-3 sentences: what is wrong and what you will do")),
		schema.Prop("root_cause", schema.String(
			"For a defect: why it happens, citing files and functions you read. "+
				"For a feature or refactor: the gap in the code as it stands.")),
		schema.Prop("approach", schema.String(
			"What to change, where, and why this addresses the cause rather than the symptom")),
		schema.Prop("files", schema.Array(fileChange,
			"Every file the change touches, including the test files").MinItemsN(1)),
		schema.Opt("assumptions", schema.Array(schema.String(),
			"Decisions you made where the report was unclear, and why. Omit if none.")),
		schema.Opt("ambiguity", schema.Object(
			schema.Prop("question", schema.String("The one question that must be answered")),
			schema.Prop("interpretation_a", schema.String("The first reading, and the change it implies")),
			schema.Prop("interpretation_b", schema.String("The second reading, and the change it implies")),
		).Describe("Set ONLY for two readings that lead to fundamentally different changes "+
			"and that the codebase cannot settle. Setting it stops the run.")),
	)
}

// implementationSchema is the second phase's contract.
func implementationSchema() *schema.Schema {
	fileChange := schema.Object(
		schema.Prop("path", schema.String("Repository-relative path you changed")),
		schema.Prop("change", schema.String("One line: what you changed in it")),
	)
	verdict := schema.Object(
		schema.Prop("id", schema.String(
			"The criterion's id, exactly as the task listed it (for example AC-1)")),
		schema.Prop("verdict", schema.Enum(
			"pass: the change satisfies this criterion. fail: it does not, or you could "+
				"not establish that it does.", CriterionVerdicts...)),
		schema.Prop("evidence", schema.String(
			"Why the verdict is what it is, in specifics: the file and symbol that implement "+
				"the criterion, the test that covers it, and the result you observed when you "+
				"ran that test. A verdict without a test that exercises the criterion says so.")),
	)
	return schema.Object(
		schema.Prop("summary", schema.String("1-3 sentences: what you did")),
		schema.Prop("commit_subject", schema.String(
			"A conventional-commit subject line under 72 characters, WITHOUT the type "+
				"prefix and without the issue reference — both are added by the program. "+
				"Example: 'skip the expiry check on cached credentials'")),
		schema.Prop("changes", schema.Array(fileChange,
			"Every file you changed").MinItemsN(1)),
		schema.Opt("tests", schema.Array(schema.String(),
			"The tests you added or changed, as 'file: what it asserts'")),
		schema.Opt("notes", schema.String(
			"Anything a reviewer should know: a trade-off, something left undone and why")),
		schema.Opt("criteria_verdicts", schema.Array(verdict,
			"One entry per acceptance criterion listed in your task, by id — all of them, "+
				"including any the change does not meet. Omit only when the task listed none.")),
	)
}

// brain is the model-driven half: the two steps where judgment is required
// and nothing else.
//
// Everything on the other side of it is deterministic Go — input
// classification, git, the verification run, the commit, the push. Keeping
// that split as an interface is what makes it checkable: a test swaps in a
// scripted provider and the rest of the pipeline does not notice.
type brain interface {
	Analyze(context.Context, analysisInput) (Analysis, agentrun.Result, error)
	Implement(context.Context, implementInput) (Implementation, agentrun.Result, error)
}

type analysisInput struct {
	Input         toolio.Input
	Baseline      checks.Result
	VerifyCommand string
	Root          string
	// Criteria is what the report defined as done, extracted from its own
	// text. Empty for the common case of a report that defined nothing.
	Criteria []Criterion
}

type implementInput struct {
	Input         toolio.Input
	Analysis      Analysis
	Baseline      checks.Result
	VerifyCommand string
	Branch        string
	Root          string
	// Criteria is what this phase must answer for, one verdict each. The
	// submit tool is built from it, so the requirement is enforced at the
	// tool boundary rather than asked for in the prompt.
	Criteria []Criterion
	// Instructions is the project's AGENTS.md or CLAUDE.md when one exists
	// and is small enough to inline. It is rendered into the task prompt for
	// the phase that writes code, because an AgentKit agent has no implicit
	// behaviour that picks such a file up.
	Instructions string
}

// agentBrain runs both phases against the configured model.
type agentBrain struct {
	runner *agentrun.Runner
	// extraPrograms widens the IMPLEMENTATION phase's shell allowlist: the
	// verification command's own program, plus whatever --allow adds. It is
	// not offered to the analysis phase, because that phase is read-only and
	// the programs in question — go, make, npm — compile and write.
	extraPrograms []string
}

func (b *agentBrain) Analyze(ctx context.Context, in analysisInput) (Analysis, agentrun.Result, error) {
	var out analysis
	programs := append([]string(nil), readOnlyPrograms...)

	res, err := b.runner.Run(ctx, agentrun.Phase{
		Name:               "analyse",
		System:             analysisSystemPrompt,
		User:               analysisPrompt(in),
		Terminator:         ToolSubmitAnalysis,
		Custom:             []core.Tool{submitAnalysisTool(&out)},
		BuiltinTools:       append(append([]string(nil), agentrun.ReadOnlyFileTools...), "execute"),
		ReadOnly:           true,
		Programs:           programs,
		Temperature:        0.2,
		LoadProjectContext: true,
	})
	if err != nil {
		return Analysis{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return Analysis{}, res, agentrun.NoResultError("analyse", ToolSubmitAnalysis, res)
	}
	return got, res, nil
}

func (b *agentBrain) Implement(ctx context.Context, in implementInput) (Implementation, agentrun.Result, error) {
	var out implementation
	programs := append(append([]string(nil), readOnlyPrograms...), buildPrograms...)
	programs = append(programs, b.extraPrograms...)

	tools := append(append([]string(nil), agentrun.ReadOnlyFileTools...), agentrun.WriteFileTools...)
	tools = append(tools, "execute")

	res, err := b.runner.Run(ctx, agentrun.Phase{
		Name:               "implement",
		System:             implementSystemPrompt,
		User:               implementPrompt(in),
		Terminator:         ToolSubmitImplementation,
		Custom:             []core.Tool{submitImplementationTool(&out, in.Criteria)},
		BuiltinTools:       tools,
		ReadOnly:           false,
		Programs:           programs,
		Temperature:        0.2,
		LoadProjectContext: true,
	})
	if err != nil {
		return Implementation{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return Implementation{}, res, agentrun.NoResultError("implement", ToolSubmitImplementation, res)
	}
	return got, res, nil
}

// analysis and implementation are the guarded destinations the tool handlers
// write into. The loop executes tool calls on its own goroutines, so the
// mutex is not decoration.
type analysis struct {
	mu   sync.Mutex
	val  Analysis
	done bool
}

func (a *analysis) set(v Analysis) {
	a.mu.Lock()
	a.val, a.done = v, true
	a.mu.Unlock()
}

func (a *analysis) get() (Analysis, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.val, a.done
}

type implementation struct {
	mu   sync.Mutex
	val  Implementation
	done bool
}

func (i *implementation) set(v Implementation) {
	i.mu.Lock()
	i.val, i.done = v, true
	i.mu.Unlock()
}

func (i *implementation) get() (Implementation, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.val, i.done
}

// submitAnalysisTool ends the analysis phase and hands back a validated
// struct.
//
// The alternative — asking for JSON in the final message and unmarshalling it
// — fails in a way this cannot: a model that wraps the object in a code
// fence, adds a sentence before it, or renames a field produces text that
// parses into nothing, and the failure surfaces as an empty analysis rather
// than as a correction the model can act on.
func submitAnalysisTool(dest *analysis) core.Tool {
	return core.Tool{
		Name: ToolSubmitAnalysis,
		Description: "Submit the diagnosis and the plan, and end this phase. " +
			"Call this once, after you have read enough code to name the cause.",
		InputSchema:         analysisSchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Report the diagnosis by calling " + ToolSubmitAnalysis + "; do not write it as prose.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var a Analysis
			if err := json.Unmarshal(in, &a); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(a.Title) == "" {
				return core.ErrResult("missing_title",
					"title is empty; it becomes the branch name and the commit subject")
			}
			if !a.Classification.Valid() {
				return core.ErrResult("invalid_classification", fmt.Sprintf(
					"classification %q is not one of %s", a.Classification,
					strings.Join(Classifications, ", ")))
			}
			if a.Ambiguity != nil && strings.TrimSpace(a.Ambiguity.Question) == "" {
				return core.ErrResult("empty_ambiguity",
					"ambiguity was set with no question. Either state the question and the two "+
						"interpretations, or omit ambiguity and record your decision as an assumption.")
			}
			dest.set(a)
			res := core.OKResult(map[string]any{"accepted": true, "title": a.Title})
			// Terminate is a vote, ANDed across the batch: the phase ends when
			// this is the last thing the model asked for, and is just another
			// result when it was emitted alongside reads it still wants.
			res.Terminate = true
			return res
		},
	}
}

// submitImplementationTool ends the implementation phase.
//
// It takes the acceptance criteria because they are what the phase is
// answerable for: a report that skips one, invents one, or answers with a
// word is refused here, with the ids named, and the model gets to correct it.
// The requirement is therefore a property of the tool rather than a sentence
// in a prompt — the phase cannot end without an answer for every criterion.
func submitImplementationTool(dest *implementation, criteria []Criterion) core.Tool {
	return core.Tool{
		Name: ToolSubmitImplementation,
		Description: "Submit a report of the work and end this phase. Call this once, " +
			"after the change is written and the project's checks pass.",
		InputSchema:         implementationSchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: append([]string{
			"Report the work by calling " + ToolSubmitImplementation + "; do not write it as prose.",
			"The commit, the push and the pull request are made by the program from what you submit.",
		}, criteriaGuideline(criteria)...),
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var impl Implementation
			if err := json.Unmarshal(in, &impl); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(impl.CommitSubject) == "" {
				return core.ErrResult("missing_commit_subject",
					"commit_subject is empty; it is the subject line of the commit this run makes")
			}
			if err := checkVerdicts(criteria, impl.CriteriaVerdicts); err != nil {
				return core.ErrResult("incomplete_criteria_verdicts", err.Error())
			}
			impl.CriteriaVerdicts = normalizeVerdicts(criteria, impl.CriteriaVerdicts)
			dest.set(impl)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

// criteriaGuideline reminds the phase of the one rule the submit tool
// enforces, and says nothing at all when there are no criteria to enforce it
// over.
func criteriaGuideline(criteria []Criterion) []string {
	if len(criteria) == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"criteria_verdicts must answer every acceptance criterion (%s), each with a verdict "+
			"and the evidence for it; the submission is refused until it does.",
		strings.Join(criteriaIDs(criteria), ", "))}
}
