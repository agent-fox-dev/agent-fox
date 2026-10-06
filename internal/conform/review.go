package conform

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/schema"

	"github.com/agent-fox-dev/agentfox/internal/agentrun"
)

// The review is the independent half of "the spec's contract holds": a phase
// with a fresh context that sees the spec, the diff and the repository, and
// never the reports of the phases that wrote the change. The author of a
// change grading its own work is how a pull request comes to say "N of N
// tasks landed" over a requirement it knew it had not met.

// What a requirement row can say about the code.
const (
	StatusImplemented = "implemented"
	StatusPartial     = "partial"
	StatusMissing     = "missing"
	StatusDifferent   = "different"
)

// RequirementStatuses is the enum the schema declares.
var RequirementStatuses = []string{StatusImplemented, StatusPartial, StatusMissing, StatusDifferent}

// What a test row can say about a test.
const (
	AssessAssertsContract = "asserts_contract"
	AssessWeaker          = "weaker"
	AssessTautological    = "tautological"
	AssessNoAssertions    = "no_assertions"
	AssessMissing         = "missing"
)

// TestAssessments is the enum the schema declares.
var TestAssessments = []string{AssessAssertsContract, AssessWeaker, AssessTautological, AssessNoAssertions, AssessMissing}

// What a decision row can say.
const (
	DecisionFollowed    = "followed"
	DecisionNotFollowed = "not_followed"
)

// DecisionStatuses is the enum the schema declares.
var DecisionStatuses = []string{DecisionFollowed, DecisionNotFollowed}

// RequirementRow is the reviewer's answer for one requirement id.
type RequirementRow struct {
	ID       string `json:"id" trust:"fact" description:"The requirement or criterion id."`
	Status   string `json:"status" trust:"model" description:"implemented, partial, missing or different."`
	Evidence string `json:"evidence" trust:"model" description:"The file:line that shows it, and what is there."`
}

// TestRow is the reviewer's answer for one test id.
type TestRow struct {
	ID         string `json:"id" trust:"fact" description:"The test id from the test spec."`
	Test       string `json:"test,omitempty" trust:"model" description:"The test function that implements it, as file:line."`
	Assessment string `json:"assessment" trust:"model" description:"asserts_contract, weaker, tautological, no_assertions or missing."`
	Evidence   string `json:"evidence" trust:"model" description:"What the test asserts, against what the contract names."`
}

// Decision is a choice recorded before the work started — a survey's
// resolution — that the change was bound to follow.
type Decision struct {
	ID   string `json:"id" trust:"fact" description:"D-n, the decision's position."`
	Text string `json:"text" trust:"model" description:"The decision as it was recorded."`
}

// DecisionRow is the reviewer's answer for one decision.
type DecisionRow struct {
	ID       string `json:"id" trust:"fact" description:"D-n, the decision answered."`
	Status   string `json:"status" trust:"model" description:"followed or not_followed."`
	Evidence string `json:"evidence" trust:"model" description:"Where the code follows the decision, or how it departs from it."`
}

// DocFinding is a statement in the documentation the code contradicts.
type DocFinding struct {
	Doc       string `json:"doc" trust:"model" description:"The documentation line, as file:line."`
	Statement string `json:"statement" trust:"model" description:"What the documentation says."`
	Code      string `json:"code" trust:"model" description:"The code line that contradicts it, as file:line."`
	Actual    string `json:"actual" trust:"model" description:"What the code actually does."`
}

// Review is the reviewer's whole answer.
type Review struct {
	Summary      string           `json:"summary" trust:"model" description:"The reviewer's overall reading of the change."`
	Requirements []RequirementRow `json:"requirements,omitempty" description:"One row per requirement id in scope."`
	Tests        []TestRow        `json:"tests,omitempty" description:"One row per test id in scope."`
	Decisions    []DecisionRow    `json:"decisions,omitempty" description:"One row per recorded decision."`
	Docs         []DocFinding     `json:"docs,omitempty" description:"Documentation statements the code contradicts."`
}

// Blocker is one finding that keeps a change from being presented as done:
// a requirement missing or implemented differently, a test that proves
// nothing, a decision not carried out, a document that says what the code
// does not do, or checks that fail in a clean environment.
type Blocker struct {
	// Key identifies the finding across reviews: the requirement or test id,
	// D-n, doc:path:line, scope:path, or "hermetic".
	Key         string `json:"key" trust:"fact" description:"What the finding is about: a requirement or test id, D-n, doc:path:line, scope:path or hermetic."`
	Requirement string `json:"requirement,omitempty" trust:"fact" description:"The requirement id concerned, when there is one."`
	Test        string `json:"test,omitempty" trust:"fact" description:"The test id concerned, when there is one."`
	What        string `json:"what" trust:"model" description:"What is wrong."`
	// Declarable is false for what can only be fixed, never declared: a
	// document that contradicts the code is corrected, not explained.
	Declarable bool `json:"declarable" description:"False when the finding can only be fixed, not declared as a known deviation."`
}

// Blockers lists what in the review keeps the change from being done.
func (r Review) Blockers() []Blocker {
	var out []Blocker
	for _, row := range r.Requirements {
		if row.Status == StatusMissing || row.Status == StatusDifferent {
			out = append(out, Blocker{Key: row.ID, Requirement: row.ID, Declarable: true,
				What: fmt.Sprintf("requirement %s is %s: %s", row.ID, row.Status, strings.TrimSpace(row.Evidence))})
		}
	}
	for _, row := range r.Tests {
		switch row.Assessment {
		case AssessTautological, AssessNoAssertions, AssessMissing:
			out = append(out, Blocker{Key: row.ID, Test: row.ID, Declarable: true,
				What: fmt.Sprintf("test %s is %s: %s", row.ID, strings.ReplaceAll(row.Assessment, "_", " "),
					strings.TrimSpace(row.Evidence))})
		}
	}
	for _, row := range r.Decisions {
		if row.Status == DecisionNotFollowed {
			out = append(out, Blocker{Key: row.ID, Declarable: true,
				What: fmt.Sprintf("decision %s was not followed: %s", row.ID, strings.TrimSpace(row.Evidence))})
		}
	}
	for _, d := range r.Docs {
		out = append(out, Blocker{Key: "doc:" + d.Doc, Declarable: false,
			What: fmt.Sprintf("%s says %q; the code does otherwise (%s: %s)", d.Doc,
				strings.TrimSpace(d.Statement), d.Code, strings.TrimSpace(d.Actual))})
	}
	return out
}

// Shortfalls lists what the review found short of the contract without
// keeping the change from landing: a requirement met in part, a test weaker
// than its contract. Each is an unmet item the pull request carries.
func (r Review) Shortfalls() []Unmet {
	var out []Unmet
	for _, row := range r.Requirements {
		if row.Status == StatusPartial {
			out = append(out, Unmet{Source: SourceReview, Requirement: row.ID,
				What: "partially implemented: " + strings.TrimSpace(row.Evidence)})
		}
	}
	for _, row := range r.Tests {
		if row.Assessment == AssessWeaker {
			out = append(out, Unmet{Source: SourceReview, Test: row.ID,
				What: "the test is weaker than its contract: " + strings.TrimSpace(row.Evidence)})
		}
	}
	return out
}

// ReviewScope is what a review must answer for, every id of it.
type ReviewScope struct {
	Requirements []string
	Tests        []string
	Decisions    []Decision
}

// ReviewInput is what the reviewer is given — and, by omission, what it is
// not: no task transcript, no task report, no model's account of the work.
type ReviewInput struct {
	// Root is the repository; Base the commit the change is measured from.
	Root string
	Base string
	// Spec is the rendered specification.
	Spec string
	// Scope is what must be answered.
	Scope ReviewScope
	// ChangedFiles and DiffStat are git's account of the change.
	ChangedFiles []string
	DiffStat     string
	// TestCommands are the checks the project runs.
	TestCommands []string
	// Context is any further material, labelled by the caller: the issue a
	// fix answers, for instance.
	Context string
	// CodeSearch grants the review phase code_search, for a run that has an
	// index (16-REQ-2.1).
	CodeSearch bool
}

// The phase's name and terminator.
const (
	PhaseReview      = "review"
	ToolSubmitReview = "submit_review"
)

// minEvidenceRunes refuses a one-word answer; it cannot tell a true one.
const minEvidenceRunes = 16

// RunReview runs the review phase: read-only, on a fresh context.
func RunReview(ctx context.Context, runner *agentrun.Runner, in ReviewInput) (Review, agentrun.Result, error) {
	var out reviewSink
	res, err := runner.Run(ctx, agentrun.Phase{
		Name:         PhaseReview,
		System:       ReviewSystemPrompt,
		User:         ReviewPrompt(in),
		Terminator:   ToolSubmitReview,
		Custom:       []core.Tool{SubmitReviewTool(in.Root, in.Scope, &out)},
		BuiltinTools: agentrun.WithCodeSearch(append(append([]string(nil), agentrun.ReadOnlyFileTools...), "execute"), in.CodeSearch),
		ReadOnly:     true,
		Programs:     append([]string(nil), agentrun.ReadOnlyPrograms...),
		Temperature:  0.1,
	})
	if err != nil {
		return Review{}, res, err
	}
	got, ok := out.get()
	if !ok {
		return Review{}, res, agentrun.NoResultError(PhaseReview, ToolSubmitReview, res)
	}
	return got, res, nil
}

type reviewSink struct {
	mu   sync.Mutex
	val  Review
	done bool
}

func (s *reviewSink) set(v Review) {
	s.mu.Lock()
	s.val, s.done = v, true
	s.mu.Unlock()
}

func (s *reviewSink) get() (Review, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.val, s.done
}

func reviewSchema() *schema.Schema {
	req := schema.Object(
		schema.Prop("id", schema.String("The requirement or criterion id, exactly as listed")),
		schema.Prop("status", schema.Enum(
			"implemented: the code does what it says, observably. partial: some of it. missing: nothing "+
				"in the change does it. different: the code does something else — another exit code, "+
				"another shape, another rule.", RequirementStatuses...)),
		schema.Prop("evidence", schema.String(
			"The file:line where the code does it (or does otherwise), and what is there. Required "+
				"for every status except missing, and checked: the file and line must exist.")),
	)
	test := schema.Object(
		schema.Prop("id", schema.String("The test id, exactly as listed (TS-...)")),
		schema.Opt("test", schema.String("The test function that implements it, as file:line")),
		schema.Prop("assessment", schema.Enum(
			"asserts_contract: it drives the real component the test spec names and asserts the "+
				"observable outcome the contract names. weaker: it asserts something real but less "+
				"than the contract (an internal value instead of the process exit code, a sentinel "+
				"compared instead of the classifier driven, wiring re-implemented instead of the "+
				"production wiring run). tautological: it cannot fail whatever the code does (an "+
				"assertion in a branch that never runs, a value compared with itself). no_assertions: "+
				"nothing in it can fail. missing: no test implements the id.", TestAssessments...)),
		schema.Prop("evidence", schema.String(
			"What the test asserts, quoted, against what the contract names; file:line is checked")),
	)
	decision := schema.Object(
		schema.Prop("id", schema.String("D-n, exactly as listed")),
		schema.Prop("status", schema.Enum(
			"followed: the code does what the decision says. not_followed: it does not — including "+
				"a note that reinterprets the decision into something else.", DecisionStatuses...)),
		schema.Prop("evidence", schema.String("Where the code follows it, or how it departs, with file:line")),
	)
	doc := schema.Object(
		schema.Prop("doc", schema.String("The documentation line, as file:line")),
		schema.Prop("statement", schema.String("What it says, quoted")),
		schema.Prop("code", schema.String("The code line that contradicts it, as file:line")),
		schema.Prop("actual", schema.String("What the code actually does")),
	)
	return schema.Object(
		schema.Prop("summary", schema.String("3-6 sentences: how far the change meets the spec, and where it does not")),
		schema.Opt("requirements", schema.Array(req, "One row per requirement id in scope — every one")),
		schema.Opt("tests", schema.Array(test, "One row per test id in scope — every one")),
		schema.Opt("decisions", schema.Array(decision, "One row per decision listed — every one")),
		schema.Opt("docs", schema.Array(doc,
			"Every statement the change's documentation makes that the code contradicts: a value, a "+
				"field name, an exit code, a JSON shape, a rule. Empty when there is none.")),
	)
}

// SubmitReviewTool ends the review. It refuses a review that skips an id in
// scope, answers one twice, invents one, or cites a file:line that does not
// exist — the review is checked for completeness and for evidence that can
// be looked up, which is all a program can check of it.
func SubmitReviewTool(root string, scope ReviewScope, dest *reviewSink) core.Tool {
	decisionIDs := make([]string, len(scope.Decisions))
	for i, d := range scope.Decisions {
		decisionIDs[i] = d.ID
	}
	return core.Tool{
		Name: ToolSubmitReview,
		Description: "Submit the conformance review and end this phase. Call it once, after you have " +
			"read the change against every requirement, test and decision in scope.",
		InputSchema: reviewSchema(),
		ConstrainedSampling: &core.ConstrainedSampling{
			Type: core.ConstrainJSONSchema, Strict: core.StrictPrefer,
		},
		PromptGuidelines: []string{
			"Report the review by calling " + ToolSubmitReview + "; do not write it as prose.",
			"Every id in scope gets exactly one row; the submission is refused until it does.",
		},
		Execute: func(_ context.Context, in json.RawMessage) core.ToolResult {
			var r Review
			if err := json.Unmarshal(in, &r); err != nil {
				return core.ErrResult("invalid_arguments", err.Error())
			}
			if strings.TrimSpace(r.Summary) == "" {
				return core.ErrResult("missing_summary", "summary is empty")
			}
			var reqRows, testRows, decRows []row
			for _, x := range r.Requirements {
				reqRows = append(reqRows, row{x.ID, x.Status, x.Evidence, x.Status != StatusMissing})
			}
			for _, x := range r.Tests {
				testRows = append(testRows, row{x.ID, x.Assessment, x.Evidence + " " + x.Test, x.Assessment != AssessMissing})
			}
			for _, x := range r.Decisions {
				decRows = append(decRows, row{x.ID, x.Status, x.Evidence, false})
			}
			for _, c := range []struct {
				field string
				ids   []string
				rows  []row
				enum  []string
			}{
				{"requirements", scope.Requirements, reqRows, RequirementStatuses},
				{"tests", scope.Tests, testRows, TestAssessments},
				{"decisions", decisionIDs, decRows, DecisionStatuses},
			} {
				if err := checkRows(root, c.field, c.ids, c.rows, c.enum); err != nil {
					return core.ErrResult("incomplete_review", err.Error())
				}
			}
			for i, d := range r.Docs {
				if !Cites(root, d.Doc) || !Cites(root, d.Code) {
					return core.ErrResult("unresolved_citation", fmt.Sprintf(
						"docs[%d] cites %q and %q; both must be file:line references that exist in the "+
							"repository", i, d.Doc, d.Code))
				}
			}
			r.Requirements = normalize(r.Requirements, scope.Requirements, func(x RequirementRow) string { return x.ID },
				func(x *RequirementRow, id string) { x.ID = id })
			r.Tests = normalize(r.Tests, scope.Tests, func(x TestRow) string { return x.ID },
				func(x *TestRow, id string) { x.ID = id })
			r.Decisions = normalize(r.Decisions, decisionIDs, func(x DecisionRow) string { return x.ID },
				func(x *DecisionRow, id string) { x.ID = id })
			dest.set(r)
			res := core.OKResult(map[string]any{"accepted": true})
			res.Terminate = true
			return res
		},
	}
}

type row struct {
	id, verdict, evidence string
	needsCitation         bool
}

func checkRows(root, field string, ids []string, rows []row, enum []string) error {
	known := map[string]bool{}
	for _, id := range ids {
		known[strings.ToUpper(id)] = true
	}
	seen := map[string]bool{}
	for _, r := range rows {
		id := strings.ToUpper(strings.TrimSpace(r.id))
		switch {
		case !known[id]:
			return fmt.Errorf("%s names %q, which is not in scope (%s)", field, r.id, strings.Join(ids, ", "))
		case seen[id]:
			return fmt.Errorf("%s answers %s twice; give exactly one row", field, r.id)
		case !slices.Contains(enum, r.verdict):
			return fmt.Errorf("%s: %s is %q; it must be one of %s", field, r.id, r.verdict, strings.Join(enum, ", "))
		case len([]rune(strings.TrimSpace(r.evidence))) < minEvidenceRunes:
			return fmt.Errorf("%s: the evidence for %s says only %q; say what is there", field, r.id,
				strings.TrimSpace(r.evidence))
		case r.needsCitation && !Cites(root, r.evidence):
			return fmt.Errorf("%s: the evidence for %s cites no file:line that exists in the repository; "+
				"name the line you read", field, r.id)
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
		return fmt.Errorf("%s is missing %s; every id in scope needs a row", field, strings.Join(missing, ", "))
	}
	return nil
}

// normalize puts rows in scope order with scope's spelling of each id.
func normalize[T any](rows []T, ids []string, idOf func(T) string, setID func(*T, string)) []T {
	if len(ids) == 0 {
		return nil
	}
	byID := map[string]T{}
	for _, r := range rows {
		byID[strings.ToUpper(strings.TrimSpace(idOf(r)))] = r
	}
	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if r, ok := byID[strings.ToUpper(id)]; ok {
			setID(&r, id)
			out = append(out, r)
		}
	}
	return out
}

// citationRe is a file:line reference: a path with an extension (or a
// Makefile), a colon, a line number.
var citationRe = regexp.MustCompile(`([A-Za-z0-9_./-]*(?:\.[A-Za-z0-9]+|Makefile)):(\d+)`)

// Cites reports whether text names at least one file:line inside root that
// exists.
func Cites(root, text string) bool {
	for _, m := range citationRe.FindAllStringSubmatch(text, -1) {
		p := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(m[1], "./")))
		if p == "." || filepath.IsAbs(p) || strings.HasPrefix(p, "..") {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		if fileHasLine(filepath.Join(root, p), n) {
			return true
		}
	}
	return false
}
