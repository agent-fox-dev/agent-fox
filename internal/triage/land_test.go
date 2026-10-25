package triage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// landing is a run ready to land, and what its callbacks saw.
type landing struct {
	t         *testing.T
	forge     *fakeforge.Forge
	token     string
	params    LandParams
	warnings  []string
	artifacts []Artifact
	effects   []SideEffect
	ledger    []string
}

// newLanding is a run on issue 7 of o/r on a fake forge of the kind, whose body
// is the report the brief had, with the submission of the payload (none when
// it is nil).
func newLanding(t *testing.T, kind, body string, payload []byte) *landing {
	t.Helper()
	noToken(t) // a client with no token of its own must not find one in the environment
	l := &landing{t: t, forge: fakeforge.New(t, kind, "o/r"), token: "test-token"}
	l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: body})
	dir := t.TempDir()
	if payload != nil {
		testutil.WriteFile(t, dir, "submission.json", fmt.Sprintf(`{"kind":"triage","run_id":%q,"attempt":1,"payload":%s}`, testRun, payload))
	}
	testutil.WriteFile(t, dir, "original.md", body)
	host := map[string]string{fakeforge.GitHub: "github.com", fakeforge.GitLab: "gitlab.com"}[kind]
	l.params = LandParams{
		RunDir: dir, RunID: testRun, Mode: LandIssue, Tree: testTree(),
		Facts: Facts{
			Input:                InputFact{Kind: KindIssue, Origin: l.forge.IssueURL(7), Fingerprint: "o/r#7"},
			Target:               RepoFact{Repo: "o/r", Host: host},
			Issue:                &IssueFact{Repo: "o/r", Host: host, Number: 7, URL: l.forge.IssueURL(7)},
			OriginalReportSHA256: testutil.SHA256(body),
		},
		Warn:       func(code, _ string) { l.warnings = append(l.warnings, code) },
		Artifact:   func(a Artifact) { l.artifacts = append(l.artifacts, a) },
		SideEffect: func(s SideEffect) { l.effects = append(l.effects, s) },
		Record: func(kind string, data any) error {
			l.ledger = append(l.ledger, kind+" "+string(testutil.JSON(t, data)))
			return nil
		},
	}
	return l
}

// asText makes the run one of a text report, which lands as a new issue.
func (l *landing) asText() *landing {
	l.params.Facts.Input, l.params.Facts.Issue = InputFact{Kind: KindText}, nil
	return l
}

// land lands with a client for the forge that has the token of the landing.
func (l *landing) land() (LandResult, error) {
	l.t.Helper()
	if l.params.Forge == nil {
		l.params.Forge = client(l.t, l.forge, l.token)
	}
	return Land(context.Background(), l.params)
}

func (l *landing) diagnosisMD() (string, bool) {
	data, err := os.ReadFile(filepath.Join(l.params.RunDir, "diagnosis.md"))
	return string(data), err == nil
}

func asRefusal(t *testing.T, err error) *Refusal {
	t.Helper()
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("error = %v (%T), want a *Refusal", err, err)
	}
	return r
}

func kinds(effects []SideEffect) []string {
	var out []string
	for _, e := range effects {
		out = append(out, e.Kind+" "+e.State)
	}
	return out
}

// A run with no submission, or one that no longer validates against the tree,
// has no result, and nothing is written.
func TestLand_WithNoValidSubmissionWritesNothing(t *testing.T) {
	stale := payloadOf(t, with(oneFile(map[string]any{"path": "deleted.go", "note": "gone since"})))
	for name, payload := range map[string][]byte{"no submission": nil, "a stale submission": stale, "no diagnosis": []byte(`"text"`), "no payload": []byte(`null`)} {
		l := newLanding(t, fakeforge.GitHub, "the report", payload)

		res, err := l.land()

		if r := asRefusal(t, err); r.Category != CategoryNoResult || res.Submitted {
			t.Errorf("%s: %+v, refusal %+v, want no_result", name, res, r)
		}
		if _, ok := l.diagnosisMD(); ok || len(l.forge.Requests()) != 0 || len(l.artifacts) != 0 {
			t.Errorf("%s: diagnosis.md %v, %d requests, artifacts %+v, want nothing", name, ok, len(l.forge.Requests()), l.artifacts)
		}
	}
	l := newLanding(t, fakeforge.GitHub, "the report", nil)
	testutil.WriteFile(t, l.params.RunDir, "submission.json", "{oops")
	if _, err := l.land(); asRefusal(t, err).Category != CategoryNoResult {
		t.Errorf("a submission that is no JSON: %v, want no_result", err)
	}
}

// orderedForge fails the test when a write comes before diagnosis.md is
// written and listed.
type orderedForge struct {
	issuex.Client
	l *landing
}

func (o orderedForge) check(op string) {
	if _, ok := o.l.diagnosisMD(); !ok || !slices.ContainsFunc(o.l.artifacts, func(a Artifact) bool { return a.Kind == "diagnosis" }) {
		o.l.t.Errorf("%s was sent before diagnosis.md was written and listed", op)
	}
}
func (o orderedForge) UpdateIssue(ctx context.Context, ref issuex.IssueRef, req issuex.UpdateIssueRequest) (issuex.Issue, error) {
	o.check("UpdateIssue")
	return o.Client.UpdateIssue(ctx, ref, req)
}
func (o orderedForge) AddLabels(ctx context.Context, ref issuex.IssueRef, labels []string) error {
	o.check("AddLabels")
	return o.Client.AddLabels(ctx, ref, labels)
}

// diagnosis.md holds the rendered body and is listed before the first write;
// each write is a side effect, the issue an artifact, and the end of the
// landing a ledger record.
func TestLand_OnAnIssueWritesDiagnosisMDFirstAndRecordsEachWrite(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
	l.params.Forge = orderedForge{Client: client(t, l.forge, l.token), l: l}
	l.params.Labels = []string{"triaged"}

	res, err := l.land()

	if err != nil || res.Action != ActionReplacedBody || !res.Submitted {
		t.Fatalf("Land = %+v, %v", res, err)
	}
	path := filepath.Join(l.params.RunDir, "diagnosis.md")
	if md, _ := l.diagnosisMD(); md != Render(res.Diagnosis, testRun, "the report") || res.DiagnosisPath != path || l.forge.Issue(7).Body != md {
		t.Errorf("diagnosis.md = %q, want the rendered body, which the issue now has", md)
	}
	if want := []Artifact{{Kind: "diagnosis", Path: path}, {Kind: "issue", URL: l.forge.IssueURL(7), Number: 7}}; !slices.Equal(l.artifacts, want) {
		t.Errorf("artifacts = %+v, want %+v", l.artifacts, want)
	}
	if want := []SideEffect{{"update_issue", "o/r#7", StateDone}, {"add_labels", "o/r#7", StateDone}}; !slices.Equal(l.effects, want) {
		t.Errorf("side effects = %+v, want %+v", l.effects, want)
	}
	if want := `triage.land {"action":"replaced_body","issue":{"repo":"o/r","host":"github.com","number":7,"url":"` + l.forge.IssueURL(7) + `"},"labels":["triaged"]}`; !slices.Equal(l.ledger, []string{want}) {
		t.Errorf("ledger = %v, want %s", l.ledger, want)
	}
	if res.Issue == nil || res.Issue.Number != 7 || !slices.Equal(res.Labels, []string{"triaged"}) || res.OriginalSHA256 != testutil.SHA256("the report") {
		t.Errorf("result = %+v", res)
	}
}

// With --land none the landing stops at diagnosis.md: no credential is needed
// and no request is sent.
func TestLand_NoneWritesOnlyDiagnosisMD(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
	l.token, l.params.Mode = "", LandNone

	res, err := l.land()

	if err != nil || res.Action != ActionNone || res.Issue != nil || !res.Submitted {
		t.Fatalf("Land = %+v, %v, want action none", res, err)
	}
	if _, ok := l.diagnosisMD(); !ok || len(l.forge.Requests()) != 0 || len(l.effects) != 0 {
		t.Errorf("diagnosis.md %v, %d requests, side effects %+v, want only diagnosis.md", ok, len(l.forge.Requests()), l.effects)
	}
	if want := `triage.land {"action":"none","issue":null,"labels":[]}`; !slices.Equal(l.ledger, []string{want}) {
		t.Errorf("ledger = %v, want %s", l.ledger, want)
	}
}

// The body is replaced with one write of the body alone, around the report as
// the issue has it now, with a warning when it changed since the brief; the
// title is left as it is.
func TestLand_OnAnIssueReplacesTheBodyAroundTheCurrentReport(t *testing.T) {
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		for name, current := range map[string]string{
			"as it was":                "the report as the brief had it",
			"edited while the run was": "the report, edited while the brain ran",
		} {
			l := newLanding(t, kind, "the report as the brief had it", payloadOf(t, valid()))
			l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: current})

			res, err := l.land()

			if err != nil || res.Action != ActionReplacedBody {
				t.Fatalf("%s %s: Land = %+v, %v", kind, name, res, err)
			}
			writes := l.forge.Writes()
			method := map[string]string{fakeforge.GitHub: "PATCH", fakeforge.GitLab: "PUT"}[kind]
			if len(writes) != 1 || writes[0].Op != fakeforge.OpBody || len(writes[0].JSON) != 1 || writes[0].Method != method {
				t.Errorf("%s %s: writes = %+v, want one %s of the body alone", kind, name, writes, method)
			}
			now := l.forge.Issue(7)
			if original, _, _ := Extract(now.Body); original != current || now.Title != "Login fails" {
				t.Errorf("%s %s: the issue is %q with the report %q, want the title untouched and the current report", kind, name, now.Title, original)
			}
			if changed := slices.Contains(l.warnings, WarnReportChanged); changed != (current != "the report as the brief had it") {
				t.Errorf("%s %s: warnings = %v", kind, name, l.warnings)
			}
		}
	}
}

func TestLand_ABodyOfOursThatCannotBeReadIsKeptWholeAsTheReport(t *testing.T) {
	broken := "<!-- agent-fox:triage v1 run=x -->\nthe markers are gone"
	l := newLanding(t, fakeforge.GitHub, broken, payloadOf(t, valid()))

	if _, err := l.land(); err != nil || !slices.Equal(l.warnings, []string{WarnOriginalUnparsed}) {
		t.Fatalf("Land: %v, warnings %v, want %s alone", err, l.warnings, WarnOriginalUnparsed)
	}
	if original, _, _ := Extract(l.forge.Issue(7).Body); original != broken {
		t.Errorf("the report is now %q, want the whole body as it was", original)
	}
}

// A body that would exceed the limit of the forge is left alone, and the
// diagnosis, without the original report, is posted as one comment, once
// however often the run lands; a comment the forge kept with other line
// endings is the one that was posted, and someone else's is not.
func TestLand_ABodyOverTheLimitIsPostedOnceAsAComment(t *testing.T) {
	huge := repeat("x", issuex.GitHubMaxBodyLength)
	d, _ := Validate(payloadOf(t, valid()), testTree())
	normalised := strings.TrimSpace(strings.ReplaceAll(RenderDiagnosisOnly(d, testRun), "\n", "\r\n"))
	for name, tc := range map[string]struct {
		before   []fakeforge.Comment
		landings int
		posted   int
	}{
		"a first landing":                  {nil, 1, 1},
		"three landings":                   {[]fakeforge.Comment{{Author: "bob", Body: "Works for me."}}, 3, 1},
		"a comment the forge normalised":   {[]fakeforge.Comment{{Author: "agent-fox", Body: normalised}}, 1, 0},
		"someone else's diagnosis comment": {[]fakeforge.Comment{{Author: "bob", Body: "## Diagnosis\n\nmine"}}, 1, 1},
	} {
		l := newLanding(t, fakeforge.GitHub, huge, payloadOf(t, valid()))
		l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: huge, Comments: tc.before})
		l.params.Labels = []string{"triaged"}

		for i := range tc.landings {
			if res, err := l.land(); err != nil || res.Action != ActionCommented || res.Issue == nil || res.Issue.Number != 7 {
				t.Fatalf("%s: landing %d = %+v, %v, want commented on issue 7", name, i+1, res, err)
			}
		}

		now := l.forge.Issue(7)
		if n := l.forge.Count(fakeforge.OpComment); n != tc.posted || l.forge.Count(fakeforge.OpBody) != 0 || now.Body != huge {
			t.Errorf("%s: %d comments posted and %d body writes, want %d and none", name, n, l.forge.Count(fakeforge.OpBody), tc.posted)
		}
		if tc.posted == 1 {
			if last := now.Comments[len(now.Comments)-1].Body; last != RenderDiagnosisOnly(d, testRun) {
				t.Errorf("%s: the comment is %q, want the diagnosis alone", name, last)
			}
		}
		if !slices.Contains(l.warnings, WarnBodyTooLong) || l.forge.Count(fakeforge.OpLabels) != tc.landings {
			t.Errorf("%s: warnings %v, %d label writes, want body_too_long and the labels", name, l.warnings, l.forge.Count(fakeforge.OpLabels))
		}
	}
}

// A report that is no issue creates one issue in the repository of the run,
// with the title of the diagnosis, the body and the labels.
func TestLand_ATextReportCreatesOneIssue(t *testing.T) {
	for name, host := range map[string]string{"a recorded host": "github.com", "the host of the issue's URL": ""} {
		l := newLanding(t, fakeforge.GitHub, "the app crashes", payloadOf(t, valid())).asText()
		l.params.Facts.Target.Host = host
		l.params.Labels = []string{"triaged", "bug"}

		res, err := l.land()

		if err != nil || res.Action != ActionCreatedIssue || l.forge.Count(fakeforge.OpCreate) != 1 || l.forge.Count(fakeforge.OpBody) != 0 {
			t.Fatalf("%s: Land = %+v, %v, want one issue created", name, res, err)
		}
		issue := l.forge.Issue(100)
		if original, _, _ := Extract(issue.Body); issue.Title != "Login fails on submit" || original != "the app crashes" || !slices.Equal(issue.Labels, []string{"triaged", "bug"}) {
			t.Errorf("%s: the created issue = %+v", name, issue)
		}
		wantHost := host
		if host == "" {
			wantHost = strings.TrimPrefix(l.forge.URL, "http://") // the fake forge's issues live on its own server
		}
		if res.Issue == nil || res.Issue.Number != 100 || res.Issue.Repo != "o/r" || res.Issue.Host != wantHost || res.Issue.URL == "" || !slices.Equal(res.Labels, l.params.Labels) {
			t.Errorf("%s: issue %+v, labels %v, want issue 100 of o/r on %s", name, res.Issue, res.Labels, wantHost)
		}
	}
}

func TestLand_ATextReportTooLongForAnIssueIsRefused(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, repeat("x", issuex.GitHubMaxBodyLength), payloadOf(t, valid())).asText()

	_, err := l.land()

	if r := asRefusal(t, err); r.Category != CategoryUsage || !strings.Contains(r.Message, "cannot land whole") || len(l.forge.Writes()) != 0 {
		t.Errorf("refusal = %+v, %d writes, want usage and nothing created", r, len(l.forge.Writes()))
	}
	if _, ok := l.diagnosisMD(); !ok {
		t.Error("diagnosis.md was not written")
	}
}

// A failed write is auth for no credential, forge retryable for a rate limit or
// a lost connection, and forge for anything else the forge answered; it ends
// the landing, with diagnosis.md kept (a failed read of the issue comes before
// it) and no ledger record.
func TestLand_AFailedWriteIsRefusedByItsCategory(t *testing.T) {
	for name, tc := range map[string]struct {
		token     string
		op        string
		fail      fakeforge.Failure
		category  string
		retryable bool
	}{
		"no credential":           {"", "", fakeforge.Failure{}, CategoryAuth, false},
		"a rate limit":            {"test-token", fakeforge.OpBody, fakeforge.Failure{Status: 429, Message: "slow"}, CategoryForge, true},
		"a lost connection":       {"test-token", fakeforge.OpBody, fakeforge.Failure{Hangup: true}, CategoryForge, true},
		"a server error":          {"test-token", fakeforge.OpBody, fakeforge.Failure{Status: 500, Message: "boom"}, CategoryForge, false},
		"a refusal":               {"test-token", fakeforge.OpBody, fakeforge.Failure{Status: 422, Message: "no"}, CategoryForge, false},
		"an issue that is gone":   {"test-token", fakeforge.OpRead, fakeforge.Failure{Status: 404, Message: "Not Found"}, CategoryForge, false},
		"an issue not created":    {"test-token", fakeforge.OpCreate, fakeforge.Failure{Status: 403, Message: "no"}, CategoryAuth, false},
		"a lost create":           {"test-token", fakeforge.OpCreate, fakeforge.Failure{Hangup: true}, CategoryForge, true},
		"a refused issue comment": {"test-token", fakeforge.OpComment, fakeforge.Failure{Status: 422, Message: "no"}, CategoryForge, false},
	} {
		body := "the report"
		if tc.op == fakeforge.OpComment {
			body = repeat("x", issuex.GitHubMaxBodyLength)
		}
		l := newLanding(t, fakeforge.GitHub, body, payloadOf(t, valid()))
		if tc.op == fakeforge.OpCreate {
			l.asText()
		}
		l.token, l.params.Labels = tc.token, []string{"triaged"}
		if tc.op != "" {
			l.forge.Fail(tc.op, tc.fail)
		}

		res, err := l.land()

		r := asRefusal(t, err)
		if r.Category != tc.category || r.Retryable != tc.retryable || (r.Category == CategoryAuth) != (r.FixHintEnv == "GITHUB_TOKEN") {
			t.Errorf("%s: refusal = %+v, want %s, retryable %v", name, r, tc.category, tc.retryable)
		}
		if _, ok := l.diagnosisMD(); ok != (tc.op != fakeforge.OpRead) || !res.Submitted || res.Action != ActionNone || l.forge.Count(fakeforge.OpLabels) != 0 || len(l.ledger) != 0 {
			t.Errorf("%s: result %+v, %d label writes, ledger %v, want diagnosis.md kept and nothing more", name, res, l.forge.Count(fakeforge.OpLabels), l.ledger)
		}
	}
}

// A label that cannot be added is a warning: the body is the deliverable.
func TestLand_ALabelThatCannotBeAddedIsAWarning(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
	l.params.Labels = []string{"triaged"}
	l.forge.Fail(fakeforge.OpLabels, fakeforge.Failure{Status: 500, Message: "boom"})

	res, err := l.land()

	if err != nil || res.Action != ActionReplacedBody || len(res.Labels) != 0 || !slices.Equal(l.warnings, []string{WarnLabelsFailed}) {
		t.Fatalf("Land = %+v, %v, warnings %v, want the landing to stand with labels_failed", res, err, l.warnings)
	}
	if got := kinds(l.effects); !slices.Equal(got, []string{"update_issue done", "add_labels failed"}) {
		t.Errorf("side effects = %v", got)
	}
}

var errDry = errors.New("dry run")

// dryForge refuses every write the way a dry run does, which records its own
// plan.
type dryForge struct {
	issuex.Client
	refused []string
}

func (d *dryForge) refuse(op string) error {
	d.refused = append(d.refused, op)
	return fmt.Errorf("%w: %s", errDry, op)
}
func (d *dryForge) UpdateIssue(context.Context, issuex.IssueRef, issuex.UpdateIssueRequest) (issuex.Issue, error) {
	return issuex.Issue{}, d.refuse("update_issue")
}
func (d *dryForge) AddLabels(context.Context, issuex.IssueRef, []string) error {
	return d.refuse("add_labels")
}
func (d *dryForge) CreateIssue(context.Context, issuex.Repo, issuex.CreateIssueRequest) (issuex.Issue, error) {
	return issuex.Issue{}, d.refuse("create_issue")
}

// A write that a dry run refuses is planned, not failed, and the landing goes
// on to its labels.
func TestLand_ADryRunIsPlannedNotFailed(t *testing.T) {
	for name, tc := range map[string]struct {
		text    bool
		refused []string
	}{
		"an issue":      {false, []string{"update_issue", "add_labels"}},
		"a text report": {true, []string{"create_issue"}},
	} {
		l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
		if tc.text {
			l.asText()
		}
		dry := &dryForge{Client: client(t, l.forge, l.token)}
		l.params.Forge, l.params.Labels = dry, []string{"triaged"}
		l.params.IsDryRun = func(err error) bool { return errors.Is(err, errDry) }

		res, err := l.land()

		if err != nil || res.Action != ActionPlanned || len(res.Labels) != 0 || !slices.Equal(dry.refused, tc.refused) {
			t.Errorf("%s: Land = %+v, %v, refused %v, want planned after %v", name, res, err, dry.refused, tc.refused)
		}
		if _, ok := l.diagnosisMD(); !ok || len(l.forge.Writes()) != 0 || len(l.effects) != 0 {
			t.Errorf("%s: diagnosis.md %v, %d writes, side effects %+v, want diagnosis.md and nothing on the forge", name, ok, len(l.forge.Writes()), l.effects)
		}
	}
}

// A submission that asks a question lands nothing, not even a read, and hands
// the question back.
func TestLand_ASubmissionWithAQuestionLandsNothing(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, with(suspecting(question("Which session?", "old", "new")))))

	res, err := l.land()

	if err != nil || res.Question == nil || res.Question.Text != "Which session?" || len(res.Question.Options) != 2 || res.Action != ActionNone || !res.Submitted {
		t.Fatalf("Land = %+v, %v, want the question and action none", res, err)
	}
	if _, ok := l.diagnosisMD(); ok || len(l.forge.Requests()) != 0 {
		t.Errorf("diagnosis.md %v, %d requests, want nothing", ok, len(l.forge.Requests()))
	}
}

// A run directory that cannot be read or written is an io refusal.
func TestLand_ARunDirectoryThatFailsIsAnIORefusal(t *testing.T) {
	noOriginal := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid())).asText()
	if err := os.Remove(filepath.Join(noOriginal.params.RunDir, "original.md")); err != nil {
		t.Fatal(err)
	}
	blocked := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
	if err := os.Mkdir(filepath.Join(blocked.params.RunDir, "diagnosis.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, l := range map[string]*landing{"no original.md": noOriginal, "diagnosis.md is a directory": blocked} {
		if _, err := l.land(); asRefusal(t, err).Category != CategoryIO || len(l.forge.Writes()) != 0 {
			t.Errorf("%s: %v, %d writes, want an io refusal and no write", name, err, len(l.forge.Writes()))
		}
	}
}

// The next step of a done run is to fix the problem when the diagnosis is
// confirmed or probable and there is an issue to point at.
func TestFixSuggested_ForAConfirmedOrProbableDiagnosisOnAnIssue(t *testing.T) {
	issue := &IssueInfo{Repo: "o/r", Number: 7}
	for confidence, want := range map[string]bool{"confirmed": true, "probable": true, "suspected": false, "not_reproducible": false} {
		if got := FixSuggested(Diagnosis{Confidence: confidence}, issue); got != want || FixSuggested(Diagnosis{Confidence: confidence}, nil) {
			t.Errorf("FixSuggested(%s) = %v on an issue, want %v, and false without one", confidence, got, want)
		}
	}
}

// The callbacks of a landing may be nil.
func TestLand_NeedsNoCallbacks(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", payloadOf(t, valid()))
	l.params.Record, l.params.Warn, l.params.Artifact, l.params.SideEffect = nil, nil, nil, nil
	l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: "edited"})

	if res, err := l.land(); err != nil || res.Action != ActionReplacedBody {
		t.Errorf("Land = %+v, %v, want the body replaced", res, err)
	}
}

// A second triage of an issue replaces the diagnosis in the body and keeps the
// person's original report byte for byte, and the body is still the only field
// written.
func TestLand_ASecondTriageReplacesTheDiagnosisAndKeepsTheReport(t *testing.T) {
	const report = "The login button does nothing.\r\n\n  ```\ntrailing space \n"
	l := newLanding(t, fakeforge.GitHub, report, payloadOf(t, valid()))
	if _, err := l.land(); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, l.params.RunDir, "submission.json", fmt.Sprintf(`{"payload":%s}`, payloadOf(t, with(func(m map[string]any) {
		m["root_cause"] = "The session store drops the session."
	}))))

	if res, err := l.land(); err != nil || res.Action != ActionReplacedBody || slices.Contains(l.warnings, WarnReportChanged) {
		t.Fatalf("the second landing = %+v, %v, warnings %v, want the body replaced around the same report", res, err, l.warnings)
	}

	now := l.forge.Issue(7)
	original, previous, warning := Extract(now.Body)
	if original != report || warning != "" || strings.Count(now.Body, BeginMarker) != 1 {
		t.Errorf("the report is now %q (%s), want %q kept once", original, warning, report)
	}
	if !strings.Contains(previous, "The session store drops the session.") || strings.Contains(previous, "dereferences a nil session") {
		t.Errorf("the diagnosis is %q, want the second one alone", previous)
	}
	for _, w := range l.forge.Writes() {
		if len(w.JSON) != 1 || w.JSON["body"] == nil {
			t.Errorf("a write sent %v, want the body alone", w.JSON)
		}
	}
	if now.Title != "Login fails" || len(now.Labels) != 0 {
		t.Errorf("the issue is %+v, want its title and labels untouched", now)
	}
}
