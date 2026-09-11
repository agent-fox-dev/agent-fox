package codeimpl

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/agentrun"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// The three terminating tools.
const (
	ToolSubmitSurvey = "submit_survey"
	ToolSubmitRepair = "submit_repair"
	ToolSubmitTask   = "submit_task"
)

// The three phase names. They are stable across tasks and runs on purpose:
// the phase name is the provider's cache key, and every task's system prompt
// and tool schema are the same — the task itself is in the user prompt.
const (
	PhaseSurvey    = "survey"
	PhaseRepair    = "repair"
	PhaseImplement = "implement"
)

// constrainedJSON asks the wire to constrain sampling to the tool's schema
// where it can. StrictPrefer, not StrictRequire: honoured on the OpenAI
// wires and ignored on Anthropic's, so it is a helpful narrowing and never
// the thing keeping the arguments well formed — the handler validates.
var constrainedJSON = &core.ConstrainedSampling{
	Type: core.ConstrainJSONSchema, Strict: core.StrictPrefer,
}

// minEvidenceRunes is the floor under a piece of evidence. It is not a
// quality bar — nothing here can tell whether evidence is true — but it
// refuses the one-word claim that carries no information at all.
const minEvidenceRunes = 24

// surveyInput is what the read-only phase is given.
type surveyInput struct {
	Spec     *afspec.Spec
	Root     string
	Profile  project.Profile
	Gate     []string
	Baseline GateResult
	// Pending are the tasks this run will implement, in order.
	Pending []afspec.Task
	// Repair says the red baseline will be repaired before the first task.
	Repair bool
}

// repairInput is what one repair phase is given.
type repairInput struct {
	Spec   *afspec.Spec
	Root   string
	Branch string
	Gate   []string
	// Failing is the gate as it stands: the one the phase has to make green.
	Failing GateResult
	Survey  *Survey
	// Task is set when the repair follows the integration task: the task
	// whose work is at HEAD, provisionally committed. Nil means the repair
	// is of the baseline, before any task.
	Task *afspec.Task
	// Prior are the reports of the tasks landed in this run, oldest first.
	Prior []priorTask
	// Attempt is 1 for the first attempt; Previous describes the attempt
	// before it when there was one.
	Attempt  int
	Attempts int
	Previous *attemptFailure
	// Instructions and Steering are the repository's own files, rendered as
	// labelled material.
	Instructions string
	Steering     string
	Profile      project.Profile
}

// taskInput is what one implementation phase is given.
type taskInput struct {
	Spec     *afspec.Spec
	Task     afspec.Task
	Root     string
	Branch   string
	Gate     []string
	Baseline GateResult
	Survey   *Survey
	// Prior are the reports of the tasks landed before this one in this
	// run, oldest first.
	Prior []priorTask
	// Attempt is 1 for the first attempt; Previous describes the attempt
	// before it when there was one.
	Attempt  int
	Attempts int
	Previous *attemptFailure
	// Instructions and Steering are the repository's own files, rendered as
	// labelled material.
	Instructions string
	Steering     string
	Profile      project.Profile
}

// priorTask is what an earlier task leaves for the ones after it.
type priorTask struct {
	ID      int
	Title   string
	Summary string
	Gotchas []string
	Files   []string
}

// attemptFailure is why the previous attempt at the same task did not land.
type attemptFailure struct {
	// Reason is one sentence.
	Reason string
	// Gate is the gate that failed, when one ran.
	Gate *GateResult
	// Verdicts are the fail verdicts the model itself reported.
	Verdicts []Verdict
	// DiffStat is what the attempt had changed before it was discarded.
	DiffStat string
}

// agentBrain runs the phases against the configured model.
type agentBrain struct {
	runner *agentrun.Runner
	// repairRunner drives the repair phase. It is the same runner unless the
	// operator asked for the repair to run on another model.
	repairRunner *agentrun.Runner
	// extraPrograms widens the implementation phase's shell allowlist: the
	// gate's programs, plus whatever --allow adds. The survey phase does not
	// get them, because it is read-only and those programs write.
	extraPrograms []string
	// protected is the spec package's directory.
	protected string
}

func (b *agentBrain) Survey(ctx context.Context, in surveyInput) (Survey, agentrun.Result, error) {
	var out sink[Survey]
	res, err := b.runner.Run(ctx, agentrun.Phase{
		Name:               PhaseSurvey,
		System:             surveySystemPrompt,
		User:               surveyPrompt(in),
		Terminator:         ToolSubmitSurvey,
		Custom:             []core.Tool{submitSurveyTool(&out)},
		BuiltinTools:       append(append([]string(nil), agentrun.ReadOnlyFileTools...), "execute"),
		ReadOnly:           true,
		Programs:           append([]string(nil), agentrun.ReadOnlyPrograms...),
		Temperature:        0.2,
		LoadProjectContext: true,
	})
	if err != nil {
		return Survey{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return Survey{}, res, agentrun.NoResultError(PhaseSurvey, ToolSubmitSurvey, res)
	}
	return got, res, nil
}

// writingPhase is the tool grant the two phases that edit code share: the
// file tools, and a shell that can build and run the gate's programs, with
// the spec package refused.
func (b *agentBrain) writingPhase() (programs, tools []string) {
	programs = append(append([]string(nil), agentrun.ReadOnlyPrograms...), agentrun.BuildPrograms...)
	programs = append(programs, b.extraPrograms...)
	tools = append(append([]string(nil), agentrun.ReadOnlyFileTools...), agentrun.WriteFileTools...)
	tools = append(tools, "execute")
	return programs, tools
}

func (b *agentBrain) Repair(ctx context.Context, in repairInput) (RepairSubmission, agentrun.Result, error) {
	var out sink[RepairSubmission]
	programs, tools := b.writingPhase()
	runner := b.repairRunner
	if runner == nil {
		runner = b.runner
	}
	res, err := runner.Run(ctx, agentrun.Phase{
		Name:               PhaseRepair,
		System:             repairSystemPrompt,
		User:               repairPrompt(in),
		Terminator:         ToolSubmitRepair,
		Custom:             []core.Tool{submitRepairTool(&out)},
		BuiltinTools:       tools,
		ReadOnly:           false,
		Programs:           programs,
		ProtectedPaths:     []string{b.protected},
		Temperature:        0.2,
		LoadProjectContext: true,
	})
	if err != nil {
		return RepairSubmission{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return RepairSubmission{}, res, agentrun.NoResultError(PhaseRepair, ToolSubmitRepair, res)
	}
	return got, res, nil
}

// RepairModel names the model the repair phase runs on.
func (b *agentBrain) RepairModel() string {
	if b.repairRunner != nil && b.repairRunner.Model() != nil {
		return b.repairRunner.Model().ID
	}
	return ""
}

func (b *agentBrain) Implement(ctx context.Context, in taskInput) (Submission, agentrun.Result, error) {
	var out sink[Submission]
	programs, tools := b.writingPhase()

	res, err := b.runner.Run(ctx, agentrun.Phase{
		Name:               PhaseImplement,
		System:             implementSystemPrompt,
		User:               taskPrompt(in),
		Terminator:         ToolSubmitTask,
		Custom:             []core.Tool{submitTaskTool(&out, in.Task)},
		BuiltinTools:       tools,
		ReadOnly:           false,
		Programs:           programs,
		ProtectedPaths:     []string{b.protected},
		Temperature:        0.2,
		LoadProjectContext: true,
	})
	if err != nil {
		return Submission{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return Submission{}, res, agentrun.NoResultError(PhaseImplement, ToolSubmitTask, res)
	}
	return got, res, nil
}

// sink is the guarded destination a tool handler writes into. The loop
// executes tool calls on its own goroutines, so the mutex is not decoration.
type sink[T any] struct {
	mu   sync.Mutex
	val  T
	done bool
}

func (s *sink[T]) set(v T) {
	s.mu.Lock()
	s.val, s.done = v, true
	s.mu.Unlock()
}

func (s *sink[T]) get() (T, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.val, s.done
}

func blockerSchema() *schema.Schema {
	return schema.Object(
		schema.Prop("reason", schema.String(
			"Why the spec cannot be implemented as written: the module it names and what the "+
				"code does instead, or the invariant it contradicts. Cite the files you read.")),
		schema.Prop("needed", schema.String(
			"What a person has to decide or change before the work can proceed")),
	).Describe("Set ONLY when the spec cannot be implemented as written and reading the code " +
		"cannot settle how to proceed. Setting it stops the run and asks a person. " +
		"Small doubts are resolved and recorded, not raised.")
}

func surveySchema() *schema.Schema {
	location := schema.Object(
		schema.Prop("name", schema.String("The thing the spec names: a module, a type, a command, a file")),
		schema.Prop("path", schema.String("Where it lives in this repository, or where it should be created")),
		schema.Opt("note", schema.String("What a coder should know before touching it")),
	)
	drift := schema.Object(
		schema.Prop("spec_ref", schema.String("The requirement, criterion, path, test or task id concerned")),
		schema.Prop("finding", schema.String("What the spec assumes and what the code actually does")),
		schema.Prop("resolution", schema.String(
			"How the tasks should proceed: follow the spec, follow the code, or the specific adaptation")),
	)
	return schema.Object(
		schema.Prop("summary", schema.String(
			"3-8 sentences: how this spec maps onto this repository, and what the tasks are up against")),
		schema.Opt("conventions", schema.Array(schema.String(),
			"The conventions a coder must follow here: test framework and layout, error handling, "+
				"naming, how packages are wired, what the linter enforces")),
		schema.Opt("locations", schema.Array(location,
			"Where the things the spec names actually live")),
		schema.Opt("drift", schema.Array(drift,
			"Every place the spec's assumptions and the code disagree, each with a resolution")),
		schema.Opt("blocker", blockerSchema()),
	)
}

func repairSchema() *schema.Schema {
	fileChange := schema.Object(
		schema.Prop("path", schema.String("Repository-relative path you changed")),
		schema.Prop("change", schema.String("One line: what you changed in it")),
	)
	return schema.Object(
		schema.Prop("cause", schema.String(
			"1-3 sentences: what was actually wrong before your change — the failing test or "+
				"lint finding and its root cause, not its symptom")),
		schema.Prop("summary", schema.String("1-3 sentences: what you changed to fix it")),
		schema.Prop("commit_subject", schema.String(
			"A conventional-commit subject line under 72 characters, WITHOUT the type prefix — "+
				"it is added by the program. Example: 'update the fixture the parser test reads'")),
		schema.Prop("changes", schema.Array(fileChange, "Every file you changed").MinItemsN(1)),
		schema.Opt("notes", schema.String(
			"Anything a reviewer should know: a test you judged obsolete and why, a fix that "+
				"papers over something deeper, a failure you could not reproduce")),
		schema.Opt("blocker", schema.Object(
			schema.Prop("reason", schema.String(
				"Why the checks cannot be made to pass by changing code: the credential, service, "+
					"tool or environment they need and how you established that")),
			schema.Prop("needed", schema.String("What a person has to provide or change first")),
		).Describe("Set ONLY when the failure is not in the code — a missing tool, credential or "+
			"service — so no change to the repository could fix it. Setting it stops the run and "+
			"asks a person.")),
	)
}

func submissionSchema(task afspec.Task) *schema.Schema {
	fileChange := schema.Object(
		schema.Prop("path", schema.String("Repository-relative path you changed")),
		schema.Prop("change", schema.String("One line: what you changed in it")),
	)
	testVerdict := schema.Object(
		schema.Prop("id", schema.String("The test's id, exactly as the task lists it (for example "+
			firstOr(task.Tests, "TS-01-1")+")")),
		schema.Prop("verdict", schema.Enum(
			"pass: the test exists, runs, and passes. fail: it does not exist, does not run, "+
				"or fails.", Verdicts...)),
		schema.Prop("evidence", schema.String(
			"The file and the test function that implement it, and the result you observed "+
				"when you ran it. A verdict without a run says so.")),
	)
	doneWhen := schema.Object(
		schema.Prop("id", schema.String("DW-n, the entry's position in done_when")),
		schema.Prop("verdict", schema.Enum("pass: it holds. fail: it does not, or you could not "+
			"establish that it does.", Verdicts...)),
		schema.Prop("evidence", schema.String("What you did to establish it and what you observed")),
	)
	s := schema.Object(
		schema.Prop("summary", schema.String("1-3 sentences: what you did")),
		schema.Prop("commit_subject", schema.String(
			"A conventional-commit subject line under 72 characters, WITHOUT the type prefix — "+
				"it is added by the program. Example: 'route progress output to stderr'")),
		schema.Prop("changes", schema.Array(fileChange, "Every file you changed").MinItemsN(1)),
		schema.Opt("test_verdicts", schema.Array(testVerdict,
			"One entry per test the task owns, by id — all of them, including any that "+
				"does not pass. The submission is refused until every id has an answer.")),
		schema.Opt("done_when_verdicts", schema.Array(doneWhen,
			"One entry per done_when item of the task, as DW-1, DW-2, … Omit when the task has none.")),
		schema.Opt("notes", schema.String(
			"Anything a reviewer or the next task should know: a trade-off, a divergence from "+
				"the spec and why, something left undone")),
		schema.Opt("gotchas", schema.Array(schema.String(),
			"The surprises: an API that does not behave as the spec assumes, a fragile test, "+
				"an assumption a later task depends on. They are carried into the next task's prompt.")),
		schema.Opt("blocker", blockerSchema()),
	)
	return s
}

func firstOr(ss []string, fallback string) string {
	if len(ss) > 0 {
		return ss[0]
	}
	return fallback
}

// submitSurveyTool ends the survey phase and hands back a validated brief.
func submitSurveyTool(dest *sink[Survey]) core.Tool {
	return core.Tool{
		Name: ToolSubmitSurvey,
		Description: "Submit the survey brief and end this phase. Call it once, after you " +
			"have located what the spec names and compared its assumptions with the code.",
		InputSchema:         surveySchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Report the survey by calling " + ToolSubmitSurvey + "; do not write it as prose.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var s Survey
			if err := json.Unmarshal(in, &s); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(s.Summary) == "" {
				return core.ErrResult("missing_summary", "summary is empty; it is what every task prompt opens with")
			}
			if s.Blocker != nil && strings.TrimSpace(s.Blocker.Reason) == "" {
				return core.ErrResult("empty_blocker",
					"blocker was set with no reason. Either say what cannot be implemented and "+
						"what a person has to decide, or omit blocker and record the decision as drift.")
			}
			dest.set(s)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

// submitRepairTool ends a repair phase. It asks less than submit_task —
// there is no test list to answer for — and the report it accepts is judged
// afterwards by the only evidence that counts, the gate running green.
func submitRepairTool(dest *sink[RepairSubmission]) core.Tool {
	return core.Tool{
		Name: ToolSubmitRepair,
		Description: "Submit a report of the repair and end this phase. Call it once, after " +
			"the checks pass when you run them.",
		InputSchema:         repairSchema(),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Report the repair by calling " + ToolSubmitRepair + "; do not write it as prose.",
			"The commit is made by the program from what you submit, after it has run the checks itself.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var s RepairSubmission
			if err := json.Unmarshal(in, &s); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if s.Blocker != nil {
				if strings.TrimSpace(s.Blocker.Reason) == "" {
					return core.ErrResult("empty_blocker",
						"blocker was set with no reason. Either say what the checks need that the code "+
							"cannot give them and what a person has to provide, or omit blocker.")
				}
				dest.set(s)
				res := core.OKResult(map[string]any{"accepted": true, "blocked": true})
				res.Terminate = true
				return res
			}
			if strings.TrimSpace(s.Cause) == "" {
				return core.ErrResult("missing_cause",
					"cause is empty; say what was wrong before the change, so a reviewer can judge the fix")
			}
			if strings.TrimSpace(s.CommitSubject) == "" {
				return core.ErrResult("missing_commit_subject",
					"commit_subject is empty; it is the subject line of the commit this repair becomes")
			}
			if len(s.Changes) == 0 {
				return core.ErrResult("missing_changes",
					"changes is empty; a repair that changed nothing has nothing to submit — if the "+
						"checks pass without a change, say so in notes and list what you verified")
			}
			dest.set(s)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

// submitTaskTool ends an implementation phase.
//
// It takes the task because the task is what the phase is answerable for: a
// report that skips one of the task's tests, invents one, or answers with a
// word is refused here, with the ids named, and the model corrects it. The
// requirement is a property of the tool rather than a sentence in a prompt —
// the phase cannot end without an answer for every test it owns.
func submitTaskTool(dest *sink[Submission], task afspec.Task) core.Tool {
	doneWhenIDs := doneWhenIDs(task)
	return core.Tool{
		Name: ToolSubmitTask,
		Description: "Submit a report of the task's work and end this phase. Call it once, " +
			"after the tests are written, the steps are implemented, and the checks pass.",
		InputSchema:         submissionSchema(task),
		ConstrainedSampling: constrainedJSON,
		PromptGuidelines: []string{
			"Report the work by calling " + ToolSubmitTask + "; do not write it as prose.",
			"The task's state, the commit and the push are made by the program from what you submit.",
			fmt.Sprintf("test_verdicts must answer every test the task owns (%s), each with a "+
				"verdict and the evidence for it; the submission is refused until it does.",
				strings.Join(task.Tests, ", ")),
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var s Submission
			if err := json.Unmarshal(in, &s); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if s.Blocker != nil {
				if strings.TrimSpace(s.Blocker.Reason) == "" {
					return core.ErrResult("empty_blocker",
						"blocker was set with no reason. Either say what cannot be implemented and "+
							"what a person has to decide, or omit blocker and record the decision in notes.")
				}
				dest.set(s)
				res := core.OKResult(map[string]any{"accepted": true, "blocked": true})
				res.Terminate = true
				return res
			}
			if strings.TrimSpace(s.CommitSubject) == "" {
				return core.ErrResult("missing_commit_subject",
					"commit_subject is empty; it is the subject line of the commit this task becomes")
			}
			if err := checkVerdicts("test_verdicts", task.Tests, s.TestVerdicts); err != nil {
				return core.ErrResult("incomplete_test_verdicts", err.Error())
			}
			if err := checkVerdicts("done_when_verdicts", doneWhenIDs, s.DoneWhenVerdicts); err != nil {
				return core.ErrResult("incomplete_done_when_verdicts", err.Error())
			}
			s.TestVerdicts = normalizeVerdicts(task.Tests, s.TestVerdicts)
			s.DoneWhenVerdicts = normalizeVerdicts(doneWhenIDs, s.DoneWhenVerdicts)
			dest.set(s)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

// doneWhenIDs names the task's done_when entries by position.
func doneWhenIDs(task afspec.Task) []string {
	ids := make([]string, 0, len(task.DoneWhen))
	for i := range task.DoneWhen {
		ids = append(ids, fmt.Sprintf("DW-%d", i+1))
	}
	return ids
}

// checkVerdicts is the completeness check the submit tool applies: one
// verdict per id, no unknown id, no bare word for evidence.
func checkVerdicts(field string, ids []string, got []Verdict) error {
	if len(ids) == 0 {
		return nil
	}
	known := map[string]bool{}
	for _, id := range ids {
		known[strings.ToUpper(id)] = true
	}
	seen := map[string]bool{}
	for _, v := range got {
		id := strings.ToUpper(strings.TrimSpace(v.ID))
		switch {
		case !known[id]:
			return fmt.Errorf("%s names %q, which is not one of the ids the task owns (%s)",
				field, v.ID, strings.Join(ids, ", "))
		case seen[id]:
			return fmt.Errorf("%s has two verdicts for %s; give exactly one", field, id)
		case !strings.EqualFold(v.Verdict, VerdictPass) && !strings.EqualFold(v.Verdict, VerdictFail):
			return fmt.Errorf("the verdict for %s is %q; it must be one of %s", id, v.Verdict,
				strings.Join(Verdicts, ", "))
		case len([]rune(strings.TrimSpace(v.Evidence))) < minEvidenceRunes:
			return fmt.Errorf("the evidence for %s says only %q; name the file and the test "+
				"function, and the result you observed when you ran it", id, strings.TrimSpace(v.Evidence))
		}
		seen[id] = true
	}
	var missing []string
	for _, id := range ids {
		if !seen[strings.ToUpper(id)] {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s is missing %s; every id needs a verdict, including any that fails",
			field, strings.Join(missing, ", "))
	}
	return nil
}

// normalizeVerdicts canonicalises a submission once it has passed the check,
// in the task's own order and spelling.
func normalizeVerdicts(ids []string, got []Verdict) []Verdict {
	if len(ids) == 0 || len(got) == 0 {
		return nil
	}
	byID := make(map[string]Verdict, len(got))
	for _, v := range got {
		byID[strings.ToUpper(strings.TrimSpace(v.ID))] = v
	}
	out := make([]Verdict, 0, len(ids))
	for _, id := range ids {
		v, ok := byID[strings.ToUpper(id)]
		if !ok {
			continue
		}
		out = append(out, Verdict{
			ID:       id,
			Verdict:  strings.ToLower(strings.TrimSpace(v.Verdict)),
			Evidence: strings.TrimSpace(v.Evidence),
		})
	}
	return out
}

// verdictsOutcome is the one-word summary of a verdict list: "pass" when
// every id was answered pass, "fail" when any was not, "" when there were
// no ids.
func verdictsOutcome(ids []string, verdicts []Verdict) string {
	if len(ids) == 0 {
		return ""
	}
	byID := make(map[string]Verdict, len(verdicts))
	for _, v := range verdicts {
		byID[strings.ToUpper(strings.TrimSpace(v.ID))] = v
	}
	for _, id := range ids {
		v, ok := byID[strings.ToUpper(id)]
		if !ok || !v.Passed() {
			return VerdictFail
		}
	}
	return VerdictPass
}

// failedVerdicts lists the verdicts that were not a pass.
func failedVerdicts(verdicts []Verdict) []Verdict {
	var out []Verdict
	for _, v := range verdicts {
		if !v.Passed() {
			out = append(out, v)
		}
	}
	return out
}
