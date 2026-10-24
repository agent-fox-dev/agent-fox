package triage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

func sha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// landing is a run ready to land, and what its callbacks saw.
type landing struct {
	t          *testing.T
	forge      *fakeforge.Forge
	token      string
	params     LandParams
	warnings   []Warning
	artifacts  []Artifact
	effects    []SideEffect
	ledger     []string
	ledgerData []any
}

// diagnosisJSON is a diagnosis that is valid against landTree.
func diagnosisJSON(t *testing.T, mutate func(map[string]any)) []byte {
	t.Helper()
	m := map[string]any{
		"title": "Login fails on submit", "severity": "high", "confidence": "probable",
		"root_cause":          "The handler dereferences a nil session.",
		"affected_files":      []any{map[string]any{"path": "main.go", "line": 3, "note": "the handler"}},
		"suggested_fix":       "Create the session first.",
		"acceptance_criteria": []any{"The login form returns 200"},
	}
	if mutate != nil {
		mutate(m)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func landTree() Tree {
	lines := map[string]int{"main.go": 10, "a/b.go": 20}
	var files []string
	for f := range lines {
		files = append(files, f)
	}
	slices.Sort(files)
	return Tree{Roots: []string{"/work/repo"}, Files: files, Lines: func(p string) (int, error) {
		n, ok := lines[p]
		if !ok {
			return 0, errors.New("not a file")
		}
		return n, nil
	}}
}

// newLanding sets up a run for an issue 7 of o/r on a fake forge of the kind,
// with the submission the diagnosis says, and the issue body as it is.
func newLanding(t *testing.T, kind, body string, diagnosis []byte) *landing {
	t.Helper()
	noToken(t) // a client with no token of its own must not find one in the environment
	l := &landing{t: t, forge: fakeforge.New(t, kind, "o/r"), token: "test-token"}
	l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: body})
	dir := t.TempDir()
	if diagnosis != nil {
		file := fmt.Sprintf(`{"kind":"triage","run_id":%q,"attempt":1,"nonce":"n","submitted_at":"2026-10-20T08:00:00Z","payload_sha256":"x","payload":%s}`, testRun, diagnosis)
		if err := os.WriteFile(filepath.Join(dir, "submission.json"), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "original.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	host := "github.com"
	if kind == fakeforge.GitLab {
		host = "gitlab.com"
	}
	l.params = LandParams{
		RunDir: dir, RunID: testRun, Mode: LandIssue, Tree: landTree(),
		Facts: Facts{
			FormatVersion:        1,
			Input:                InputFact{Kind: KindIssue, Origin: l.forge.IssueURL(7), Fingerprint: "o/r#7"},
			Target:               RepoFact{Repo: "o/r", Host: host},
			Issue:                &IssueFact{Repo: "o/r", Host: host, Number: 7, URL: l.forge.IssueURL(7)},
			OriginalReportSHA256: sha(body),
		},
		Warn:       func(code, msg string) { l.warnings = append(l.warnings, Warning{code, msg}) },
		Artifact:   func(a Artifact) { l.artifacts = append(l.artifacts, a) },
		SideEffect: func(s SideEffect) { l.effects = append(l.effects, s) },
		Record: func(kind string, data any) error {
			l.ledger = append(l.ledger, kind)
			l.ledgerData = append(l.ledgerData, data)
			return nil
		},
	}
	return l
}

// land lands with a client for the forge, which has the token of the landing.
func (l *landing) land() (LandResult, error) {
	l.t.Helper()
	if l.params.Forge == nil {
		client, err := l.forge.NewForge(l.token)(issuex.Options{})
		if err != nil {
			l.t.Fatal(err)
		}
		l.t.Cleanup(func() { _ = client.Close() })
		l.params.Forge = client
	}
	return Land(context.Background(), l.params)
}

func (l *landing) warned(code string) bool {
	for _, w := range l.warnings {
		if w.Code == code {
			return true
		}
	}
	return false
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

// betweenMarkers is the original report in a body.
func betweenMarkers(t *testing.T, body string) string {
	t.Helper()
	original, _, warning := Extract(body)
	if warning != "" {
		t.Fatalf("the body does not pair: %s", warning)
	}
	return original
}

// TestLandingWithNoValidSubmissionWritesNothing_TS_08_49 verifies TS-08-49:
// a run with no submission, or one that fails validation against the tree again,
// fails as no_result with no diagnosis.md and no request to the forge.
// Verifies: 08-REQ-8.1
func TestLandingWithNoValidSubmissionWritesNothing_TS_08_49(t *testing.T) {
	stale := diagnosisJSON(t, func(m map[string]any) {
		m["affected_files"] = []any{map[string]any{"path": "deleted.go", "note": "gone since"}}
	})
	for name, diagnosis := range map[string][]byte{"no submission": nil, "a stale submission": stale, "garbage": []byte(`"not a diagnosis"`)} {
		t.Run(name, func(t *testing.T) {
			l := newLanding(t, fakeforge.GitHub, "the report", diagnosis)

			res, err := l.land()

			if r := asRefusal(t, err); r.Category != CategoryNoResult {
				t.Errorf("refusal = %+v, want no_result", r)
			}
			if res.Submitted {
				t.Error("Submitted is set")
			}
			if _, ok := l.diagnosisMD(); ok {
				t.Error("diagnosis.md was written")
			}
			if n := len(l.forge.Requests()); n != 0 {
				t.Errorf("%d requests to the forge, want none", n)
			}
			if len(l.artifacts) != 0 {
				t.Errorf("artifacts = %+v, want none", l.artifacts)
			}
		})
	}

	t.Run("a submission that is not JSON", func(t *testing.T) {
		l := newLanding(t, fakeforge.GitHub, "the report", nil)
		if err := os.WriteFile(filepath.Join(l.params.RunDir, "submission.json"), []byte("{oops"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := l.land(); asRefusal(t, err).Category != CategoryNoResult {
			t.Errorf("err = %v, want no_result", err)
		}
	})
}

// orderedForge fails when a write comes before diagnosis.md is there.
type orderedForge struct {
	issuex.Client
	t   *testing.T
	dir string
	got *[]Artifact
}

func (o orderedForge) check(op string) {
	o.t.Helper()
	if _, err := os.Stat(filepath.Join(o.dir, "diagnosis.md")); err != nil {
		o.t.Errorf("%s was sent before diagnosis.md was written", op)
	}
	if !slices.ContainsFunc(*o.got, func(a Artifact) bool { return a.Kind == "diagnosis" }) {
		o.t.Errorf("%s was sent before the diagnosis was listed as an artifact", op)
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

// TestDiagnosisMDIsWrittenAndListedBeforeAnyWrite_TS_08_50 verifies TS-08-50:
// diagnosis.md is written and listed before the first forge write, and --land
// none stops there, needing no credential and sending no request.
// Verifies: 08-REQ-8.2
func TestDiagnosisMDIsWrittenAndListedBeforeAnyWrite_TS_08_50(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
	client, err := l.forge.NewForge("test-token")(issuex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	l.params.Forge = orderedForge{Client: client, t: t, dir: l.params.RunDir, got: &l.artifacts}
	l.params.Labels = []string{"triaged"}

	res, err := l.land()

	if err != nil || res.Action != ActionReplacedBody {
		t.Fatalf("Land = %+v, %v", res, err)
	}
	md, ok := l.diagnosisMD()
	if !ok || md != Render(res.Diagnosis, testRun, "the report") {
		t.Errorf("diagnosis.md = %q, want the rendered body", md)
	}
	if want := (Artifact{Kind: "diagnosis", Path: filepath.Join(l.params.RunDir, "diagnosis.md")}); !slices.Contains(l.artifacts, want) {
		t.Errorf("artifacts = %+v, want %+v", l.artifacts, want)
	}

	t.Run("--land none", func(t *testing.T) {
		l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
		l.token = "" // no credential
		l.params.Mode = LandNone

		res, err := l.land()

		if err != nil || res.Action != ActionNone || res.Issue != nil || !res.Submitted {
			t.Fatalf("Land = %+v, %v, want action none", res, err)
		}
		if _, ok := l.diagnosisMD(); !ok {
			t.Error("diagnosis.md was not written")
		}
		if n := len(l.forge.Requests()); n != 0 {
			t.Errorf("%d requests to the forge, want none", n)
		}
		if len(l.effects) != 0 || l.ledger != nil && len(l.ledger) != 1 {
			t.Errorf("side effects %+v, ledger %v", l.effects, l.ledger)
		}
	})
}

// TestLandingOnAnIssueSendsOneBodyOnlyWriteAndKeepsTheCurrentOriginal_TS_08_51 verifies TS-08-51:
// one body write with no title, the original between the markers as it is now
// (with a warning when it changed since the brief), then the labels.
// Verifies: 08-REQ-8.3
func TestLandingOnAnIssueSendsOneBodyOnlyWriteAndKeepsTheCurrentOriginal_TS_08_51(t *testing.T) {
	for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
		t.Run(kind+" with the report changed", func(t *testing.T) {
			l := newLanding(t, kind, "the report as the brief had it", diagnosisJSON(t, nil))
			l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: "the report, edited while the brain ran"})
			l.params.Labels = []string{"triaged"}

			res, err := l.land()

			if err != nil || res.Action != ActionReplacedBody {
				t.Fatalf("Land = %+v, %v", res, err)
			}
			writes := l.forge.Writes()
			var bodies []fakeforge.Request
			for _, w := range writes {
				if w.Op == fakeforge.OpBody {
					bodies = append(bodies, w)
				}
			}
			if len(bodies) != 1 {
				t.Fatalf("%d body writes of %d writes, want 1", len(bodies), len(writes))
			}
			if _, hasTitle := bodies[0].JSON["title"]; hasTitle || len(bodies[0].JSON) != 1 {
				t.Errorf("the body write sent %v, want the body and nothing else", bodies[0].JSON)
			}
			if (kind == fakeforge.GitHub && bodies[0].Method != "PATCH") || (kind == fakeforge.GitLab && bodies[0].Method != "PUT") {
				t.Errorf("the body was written with %s", bodies[0].Method)
			}
			now := l.forge.Issue(7)
			if got := betweenMarkers(t, now.Body); got != "the report, edited while the brain ran" {
				t.Errorf("the original between the markers is %q, want the current one", got)
			}
			if !l.warned(WarnReportChanged) {
				t.Errorf("warnings = %+v, want report_changed_during_triage", l.warnings)
			}
			if got := l.forge.Count(fakeforge.OpLabels); got != 1 || !slices.Contains(now.Labels, "triaged") {
				t.Errorf("%d label writes, labels %v, want one that adds triaged", got, now.Labels)
			}
			if title := now.Title; title != "Login fails" {
				t.Errorf("the title is %q, want it untouched", title)
			}
			if !strings.Contains(l.forge.Issue(7).Body, "Diagnosed by agent-fox") {
				t.Error("the body has no diagnosis")
			}
		})

		t.Run(kind+" with the report as it was", func(t *testing.T) {
			l := newLanding(t, kind, "the report", diagnosisJSON(t, nil))

			res, err := l.land()

			if err != nil || res.Action != ActionReplacedBody || l.warned(WarnReportChanged) {
				t.Errorf("Land = %+v, %v, warnings %+v; want no warning", res, err, l.warnings)
			}
			if got := l.forge.Count(fakeforge.OpLabels); got != 0 {
				t.Errorf("%d label writes with no labels given", got)
			}
		})
	}
}

// TestABodyOverTheLimitFallsBackToOneComment_TS_08_52 verifies TS-08-52:
// a body that would exceed the limit of the forge leaves the body alone and
// posts the diagnosis, without the original section, as one comment.
// Verifies: 08-REQ-8.4
func TestABodyOverTheLimitFallsBackToOneComment_TS_08_52(t *testing.T) {
	huge := strings.Repeat("x", issuex.GitHubMaxBodyLength)
	l := newLanding(t, fakeforge.GitHub, huge, diagnosisJSON(t, nil))
	l.params.Labels = []string{"triaged"}

	res, err := l.land()

	if err != nil || res.Action != ActionCommented {
		t.Fatalf("Land = %+v, %v, want commented", res, err)
	}
	if n := l.forge.Count(fakeforge.OpBody); n != 0 {
		t.Errorf("%d body writes, want none", n)
	}
	if l.forge.Issue(7).Body != huge {
		t.Error("the body of the issue was changed")
	}
	comments := l.forge.Issue(7).Comments
	if len(comments) != 1 || strings.Contains(comments[0].Body, "## Original report") || !strings.Contains(comments[0].Body, "## Diagnosis") {
		t.Fatalf("comments = %+v, want one with the diagnosis and no original section", comments)
	}
	if !l.warned(WarnBodyTooLong) {
		t.Errorf("warnings = %+v, want body_too_long", l.warnings)
	}
	if n := l.forge.Count(fakeforge.OpLabels); n != 1 {
		t.Errorf("%d label writes, want 1", n)
	}
	if kinds := effectKinds(l.effects); !slices.Equal(kinds, []string{"add_comment", "add_labels"}) {
		t.Errorf("side effects = %v, want add_comment then add_labels", kinds)
	}
}

func effectKinds(effects []SideEffect) []string {
	var out []string
	for _, e := range effects {
		out = append(out, e.Kind)
	}
	return out
}

// TestATextReportCreatesOneIssue_TS_08_53 verifies TS-08-53 as it stands: a report that
// is no issue creates one issue with the title, the body and the labels.
// Verifies: 08-REQ-8.5
func TestATextReportCreatesOneIssue_TS_08_53(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the app crashes", diagnosisJSON(t, nil))
	l.params.Facts.Input = InputFact{Kind: KindText, Origin: "", Fingerprint: sha("the app crashes")}
	l.params.Facts.Issue = nil
	l.params.Labels = []string{"triaged", "bug"}

	res, err := l.land()

	if err != nil {
		t.Fatal(err)
	}
	if got := l.forge.Count(fakeforge.OpCreate); got != 1 {
		t.Fatalf("%d create requests, want exactly 1", got)
	}
	if res.Action != ActionCreatedIssue || res.Issue == nil || res.Issue.Number != 100 {
		t.Errorf("the landing = %+v, want created_issue on issue 100", res)
	}
	issue := l.forge.Issue(100)
	if issue.Title != "Login fails on submit" || betweenMarkers(t, issue.Body) != "the app crashes" || !slices.Equal(issue.Labels, []string{"triaged", "bug"}) && len(issue.Labels) < 2 {
		t.Errorf("the created issue = %+v, want the title of the submission, the input between the markers and the labels", issue)
	}
	if got := l.forge.Count(fakeforge.OpBody); got != 0 {
		t.Errorf("%d body writes, want none", got)
	}
	if res.Issue.URL == "" || res.Issue.Repo != "o/r" {
		t.Errorf("issue info = %+v", res.Issue)
	}
}

// TestEachWriteIsASideEffectAndTheIssueAnArtifact_TS_08_54 verifies TS-08-54:
// side effects for the writes, the artifact of the landed issue and the ledger
// record of the landing.
// Verifies: 08-REQ-8.6
func TestEachWriteIsASideEffectAndTheIssueAnArtifact_TS_08_54(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
	l.params.Labels = []string{"triaged"}

	res, err := l.land()

	if err != nil {
		t.Fatal(err)
	}
	want := []SideEffect{{"update_issue", "o/r#7", StateDone}, {"add_labels", "o/r#7", StateDone}}
	if !slices.Equal(l.effects, want) {
		t.Errorf("side effects = %+v, want %+v", l.effects, want)
	}
	issueArtifact := Artifact{Kind: "issue", URL: l.forge.IssueURL(7), Number: 7}
	if !slices.Contains(l.artifacts, issueArtifact) {
		t.Errorf("artifacts = %+v, want %+v", l.artifacts, issueArtifact)
	}
	if len(l.ledger) != 1 || l.ledger[0] != "triage.land" {
		t.Fatalf("ledger = %v, want one triage.land", l.ledger)
	}
	if data, _ := json.Marshal(l.ledgerData[0]); !strings.Contains(string(data), `"action":"replaced_body"`) {
		t.Errorf("triage.land = %s, want the action", data)
	}
	if res.Issue == nil || res.Issue.Number != 7 || res.Issue.Repo != "o/r" || res.Issue.Host != "github.com" || !slices.Equal(res.Labels, []string{"triaged"}) {
		t.Errorf("result = %+v", res)
	}
	if res.OriginalSHA256 != sha("the report") {
		t.Errorf("OriginalSHA256 = %s", res.OriginalSHA256)
	}
}

// TestLandingWriteFailuresMapToCategories_TS_08_55 verifies TS-08-55:
// a failed body write is auth (no credential), forge retryable (a rate limit, a
// lost connection) or forge not retryable (anything else), and stops the
// landing; a failed label write only warns.
// Verifies: 08-REQ-8.7
func TestLandingWriteFailuresMapToCategories_TS_08_55(t *testing.T) {
	for name, tc := range map[string]struct {
		token     string
		fail      *fakeforge.Failure
		category  string
		retryable bool
	}{
		"no credential":     {"", nil, CategoryAuth, false},
		"a rate limit":      {"test-token", &fakeforge.Failure{Status: 429, Message: "slow"}, CategoryForge, true},
		"a lost connection": {"test-token", &fakeforge.Failure{Hangup: true}, CategoryForge, true},
		"a server error":    {"test-token", &fakeforge.Failure{Status: 500, Message: "boom"}, CategoryForge, false},
		"a refusal":         {"test-token", &fakeforge.Failure{Status: 422, Message: "no"}, CategoryForge, false},
	} {
		t.Run(name, func(t *testing.T) {
			l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
			l.token = tc.token
			l.params.Labels = []string{"triaged"}
			if tc.fail != nil {
				l.forge.Fail(fakeforge.OpBody, *tc.fail)
			}

			res, err := l.land()

			r := asRefusal(t, err)
			if r.Category != tc.category || r.Retryable != tc.retryable {
				t.Errorf("refusal = %+v, want %s, retryable %v", r, tc.category, tc.retryable)
			}
			if tc.category == CategoryAuth && r.FixHintEnv != "GITHUB_TOKEN" {
				t.Errorf("fix hint env = %q, want GITHUB_TOKEN", r.FixHintEnv)
			}
			if n := l.forge.Count(fakeforge.OpLabels); n != 0 {
				t.Errorf("%d label writes after a failed body write, want none", n)
			}
			if !res.Submitted || res.DiagnosisPath == "" {
				t.Errorf("result = %+v, want it to say there was a valid submission and a diagnosis.md", res)
			}
			if _, ok := l.diagnosisMD(); !ok {
				t.Error("diagnosis.md is gone")
			}
			if len(l.ledger) != 0 {
				t.Errorf("ledger = %v, want no triage.land for a landing that failed", l.ledger)
			}
		})
	}

	t.Run("a label failure", func(t *testing.T) {
		l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
		l.params.Labels = []string{"triaged"}
		l.forge.Fail(fakeforge.OpLabels, fakeforge.Failure{Status: 500, Message: "boom"})

		res, err := l.land()

		if err != nil || res.Action != ActionReplacedBody {
			t.Fatalf("Land = %+v, %v, want the landing to stand", res, err)
		}
		if !l.warned(WarnLabelsFailed) {
			t.Errorf("warnings = %+v, want labels_failed", l.warnings)
		}
		want := []SideEffect{{"update_issue", "o/r#7", StateDone}, {"add_labels", "o/r#7", StateFailed}}
		if !slices.Equal(l.effects, want) {
			t.Errorf("side effects = %+v, want %+v", l.effects, want)
		}
		if len(res.Labels) != 0 {
			t.Errorf("labels = %v, want none added", res.Labels)
		}
	})
}

var errDry = errors.New("dry run")

// dryForge refuses every write the way a dry run does.
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

// TestADryRunIsPlannedNotAFailure guards 08-REQ-8.8: an error that stands for a
// refused write is a planned action, and the landing goes on to its labels.
func TestADryRunIsPlannedNotAFailure(t *testing.T) {
	for name, issue := range map[string]bool{"an issue": true, "a text report": false} {
		t.Run(name, func(t *testing.T) {
			l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, nil))
			client, _ := l.forge.NewForge("test-token")(issuex.Options{})
			dry := &dryForge{Client: client}
			l.params.Forge = dry
			l.params.IsDryRun = func(err error) bool { return errors.Is(err, errDry) }
			l.params.Labels = []string{"triaged"}
			if !issue {
				l.params.Facts.Input, l.params.Facts.Issue = InputFact{Kind: KindText}, nil
			}

			res, err := l.land()

			if err != nil || res.Action != ActionPlanned {
				t.Fatalf("Land = %+v, %v, want planned", res, err)
			}
			if len(res.Labels) != 0 {
				t.Errorf("labels = %v: a dry run adds none, and the plan is in the side effects", res.Labels)
			}
			if n := len(l.forge.Writes()); n != 0 {
				t.Errorf("%d writes reached the forge", n)
			}
			wantRefused := []string{"update_issue", "add_labels"}
			if !issue {
				wantRefused = []string{"create_issue"}
			}
			if !slices.Equal(dry.refused, wantRefused) {
				t.Errorf("the dry run refused %v, want %v", dry.refused, wantRefused)
			}
			if len(l.effects) != 0 {
				t.Errorf("side effects = %+v: a dry run records its own, as planned, and the landing adds none", l.effects)
			}
			if _, ok := l.diagnosisMD(); !ok {
				t.Error("a dry run writes diagnosis.md")
			}
		})
	}
}

// TestASubmissionWithAQuestionLandsNothing guards 08-REQ-9.1 for the landing:
// no diagnosis.md and no forge request, and the question is handed back.
func TestASubmissionWithAQuestionLandsNothing(t *testing.T) {
	l := newLanding(t, fakeforge.GitHub, "the report", diagnosisJSON(t, func(m map[string]any) {
		m["confidence"] = "suspected"
		m["question"] = map[string]any{"text": "Which session?", "options": []any{"old", "new"}}
	}))

	res, err := l.land()

	if err != nil || res.Question == nil || res.Question.Text != "Which session?" || len(res.Question.Options) != 2 || res.Action != ActionNone || !res.Submitted {
		t.Fatalf("Land = %+v, %v, want the question and action none", res, err)
	}
	if _, ok := l.diagnosisMD(); ok {
		t.Error("diagnosis.md was written")
	}
	if n := len(l.forge.Requests()); n != 0 {
		t.Errorf("%d requests to the forge, want none: not even a read", n)
	}
}

// TestTheCommentFallbackIsPostedOnceWhateverTheNumberOfLandings guards "landing is
// repeatable" (DD19) for a body that is too long: the comment is there already,
// and a second landing does not post another. A comment that someone else wrote
// does not stand in for it.
func TestTheCommentFallbackIsPostedOnceWhateverTheNumberOfLandings(t *testing.T) {
	huge := strings.Repeat("x", issuex.GitHubMaxBodyLength)
	l := newLanding(t, fakeforge.GitHub, huge, diagnosisJSON(t, nil))
	l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: huge, Comments: []fakeforge.Comment{{Author: "bob", Body: "Works for me."}}})

	var results []LandResult
	for i := range 3 {
		res, err := l.land()
		if err != nil {
			t.Fatalf("landing %d: %v", i+1, err)
		}
		results = append(results, res)
	}

	comments := l.forge.Issue(7).Comments
	if len(comments) != 2 || comments[0].Body != "Works for me." || !strings.Contains(comments[1].Body, "## Diagnosis") {
		t.Fatalf("comments = %+v, want bob's and one diagnosis", comments)
	}
	if n := l.forge.Count(fakeforge.OpComment); n != 1 {
		t.Errorf("%d comment writes, want 1", n)
	}
	for i, r := range results {
		if r.Action != ActionCommented || r.Issue == nil || r.Issue.Number != 7 {
			t.Errorf("landing %d = %+v, want commented on issue 7", i+1, r)
		}
	}
	if kinds := effectKinds(l.effects); !slices.Equal(kinds, []string{"add_comment"}) {
		t.Errorf("side effects = %v, want the one write", kinds)
	}
}

// TestACommentTheForgeNormalisedIsTheCommentThatWasPosted guards the same: a forge
// keeps a comment with other line endings and without its last newline, and it is
// still the diagnosis of the run.
func TestACommentTheForgeNormalisedIsTheCommentThatWasPosted(t *testing.T) {
	huge := strings.Repeat("x", issuex.GitHubMaxBodyLength)
	l := newLanding(t, fakeforge.GitHub, huge, diagnosisJSON(t, nil))
	d, problems := Validate(diagnosisJSON(t, nil), l.params.Tree)
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	posted := strings.TrimSpace(strings.ReplaceAll(RenderDiagnosisOnly(d, testRun), "\n", "\r\n"))
	l.forge.AddIssue(fakeforge.Issue{Number: 7, Title: "Login fails", Body: huge, Comments: []fakeforge.Comment{{Author: "agent-fox", Body: posted}}})

	res, err := l.land()

	if err != nil || res.Action != ActionCommented {
		t.Fatalf("Land = %+v, %v", res, err)
	}
	if n := l.forge.Count(fakeforge.OpComment); n != 0 {
		t.Errorf("%d comment writes, want none: the comment is there", n)
	}
	if sameText("a\nb", "a\nc") {
		t.Error("two different texts are the same")
	}
}
