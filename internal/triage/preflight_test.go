package triage

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

func okFlags() Flags { return Flags{Budget: 2, MaxTurns: 100, Land: LandIssue} }

var target = issuex.Repo{Owner: "o", Name: "r"}

// client is a client of the forge with the token, or none.
func client(t *testing.T, forge *fakeforge.Forge, token string) issuex.Client {
	t.Helper()
	c, err := forge.NewForge(token)(issuex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// Each bad flag is a usage refusal that names the flag or the label.
func TestCheckFlags_RefusesABadFlagAsUsage(t *testing.T) {
	labels := make([]string, 11)
	for i := range labels {
		labels[i] = fmt.Sprintf("label-%d", i)
	}
	for name, tc := range map[string]struct {
		change  func(*Flags)
		mention string
	}{
		"an empty label":            {func(f *Flags) { f.Labels = []string{""} }, "--label"},
		"a label of 51 characters":  {func(f *Flags) { f.Labels = []string{repeat("l", 51)} }, repeat("l", 51)},
		"a label with a comma":      {func(f *Flags) { f.Labels = []string{"a,b"} }, "a,b"},
		"a leading space":           {func(f *Flags) { f.Labels = []string{" a"} }, `" a"`},
		"a trailing space":          {func(f *Flags) { f.Labels = []string{"a "} }, `"a "`},
		"eleven distinct labels":    {func(f *Flags) { f.Labels = labels }, "at most 10"},
		"--label with --land none":  {func(f *Flags) { f.Labels, f.Land = []string{"x"}, LandNone }, "--land none"},
		"a budget of 0":             {func(f *Flags) { f.Budget = 0 }, "--budget"},
		"a negative budget":         {func(f *Flags) { f.Budget = -1 }, "--budget"},
		"a budget of NaN":           {func(f *Flags) { f.Budget = math.NaN() }, "--budget"},
		"an infinite budget":        {func(f *Flags) { f.Budget = math.Inf(1) }, "--budget"},
		"no turns":                  {func(f *Flags) { f.MaxTurns = 0 }, "--max-turns"},
		"a landing that is no mode": {func(f *Flags) { f.Land = "pr" }, "--land"},
	} {
		f := okFlags()
		tc.change(&f)

		got, refusal := CheckFlags(f)

		if refusal == nil || refusal.Category != CategoryUsage || refusal.Retryable || got != nil || !strings.Contains(refusal.Error(), tc.mention) {
			t.Errorf("%s: %v, %+v, want a usage refusal that mentions %q", name, got, refusal, tc.mention)
		}
	}
}

// Good labels pass, with exact duplicates collapsed in first-seen order.
func TestCheckFlags_ReturnsTheLabelsWithoutDuplicates(t *testing.T) {
	ten := []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}
	for name, tc := range map[string]struct {
		land         string
		labels, want []string
	}{
		"a label of 50 characters in 100 bytes": {LandIssue, []string{repeat("é", 50)}, []string{repeat("é", 50)}},
		"ten labels":                            {LandIssue, ten, ten},
		"ten labels given twice":                {LandIssue, append(append([]string{}, ten...), ten...), ten},
		"duplicates":                            {"", []string{"x", "y", "x", "x", "z", "y"}, []string{"x", "y", "z"}},
		"--land none without labels":            {LandNone, nil, nil},
	} {
		f := okFlags()
		f.Land, f.Labels = tc.land, tc.labels
		if got, refusal := CheckFlags(f); refusal != nil || strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: %v, %+v, want %v", name, got, refusal, tc.want)
		}
	}
}

// Triage takes an issue, not a pull or merge request, and text that is UTF-8; a
// closed issue is a warning and the run goes on.
func TestCheckInput_RefusesWhatTriageDoesNotTake(t *testing.T) {
	closed := issueReport("body")
	closed.Thread.Issue.State = "closed"
	pull := func(host string) Report {
		ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: host}, Number: 3, IsPullRequest: true}
		return Report{Kind: KindIssue, Origin: ref.URL(), Ref: ref}
	}
	for name, tc := range map[string]struct {
		report  Report
		refused string
		warning string
	}{
		"a pull request":        {pull("github.com"), "pull or merge request", ""},
		"a merge request":       {pull("gitlab.com"), "pull or merge request", ""},
		"text that is no UTF-8": {Report{Kind: KindText, Body: "caf\xe9 crashes"}, "UTF-8", ""},
		"a file of no UTF-8":    {Report{Kind: KindFile, Body: "\xff"}, "UTF-8", ""},
		"a closed issue":        {closed, "", WarnIssueClosed},
		"an open issue":         {issueReport("body"), "", ""},
		"text":                  {Report{Kind: KindStdin, Body: "café crashes"}, "", ""},
	} {
		warnings, refusal := CheckInput(tc.report)

		if tc.refused != "" {
			if refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, tc.refused) {
				t.Errorf("%s: refusal = %+v, want usage that mentions %q", name, refusal, tc.refused)
			}
			continue
		}
		if refusal != nil || (tc.warning == "") != (len(warnings) == 0) || (tc.warning != "" && (len(warnings) != 1 || warnings[0].Code != tc.warning)) {
			t.Errorf("%s: %+v, %+v, want no refusal and the warning %q", name, warnings, refusal, tc.warning)
		}
	}
}

// Without a credential a landing is refused as auth, with the variable of the
// forge to set; a dry run needs none.
func TestCheckLandTarget_RefusesAForgeWithNoCredential(t *testing.T) {
	noToken(t)
	for name, tc := range map[string]struct {
		kind string
		repo issuex.Repo
		env  string
	}{
		"GitHub":                       {fakeforge.GitHub, issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, "GITHUB_TOKEN"},
		"GitLab":                       {fakeforge.GitLab, issuex.Repo{Owner: "o", Name: "r", Host: "gitlab.com"}, "GITLAB_TOKEN"},
		"GitLab with no host recorded": {fakeforge.GitLab, target, "GITLAB_TOKEN"},
		"GitHub with no host recorded": {fakeforge.GitHub, target, "GITHUB_TOKEN"},
	} {
		forge := fakeforge.New(t, tc.kind, "o/r")
		c := LandCheck{Forge: client(t, forge, ""), Target: tc.repo}

		refusal := CheckLandTarget(context.Background(), c)

		want := fmt.Sprintf("creating or updating an issue in %s needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run or --land none", tc.repo)
		if refusal == nil || refusal.Category != CategoryAuth || refusal.Message != want || refusal.FixHintEnv != tc.env || refusal.Retryable {
			t.Errorf("%s: refusal = %+v, want auth with %q and the hint %s", name, refusal, want, tc.env)
		}
		c.DryRun = true
		if refusal := CheckLandTarget(context.Background(), c); refusal != nil || len(forge.Requests()) != 0 {
			t.Errorf("%s: a dry run: %+v, %d requests, want it accepted without asking the forge", name, refusal, len(forge.Requests()))
		}
	}
}

// A repository that cannot be read or is archived is refused before anything
// is spent: auth for a credential the forge refused, forge retryable for what
// may pass later, forge not retryable for the rest.
func TestCheckLandTarget_RefusesARepositoryThatCannotTakeTheDiagnosis(t *testing.T) {
	type want struct {
		category  string
		retryable bool
		message   string
	}
	for name, tc := range map[string]struct {
		fail     *fakeforge.Failure
		archived bool
		want     want
	}{
		"found":                 {nil, false, want{}},
		"archived":              {nil, true, want{CategoryForge, false, "repository is archived"}},
		"not found":             {&fakeforge.Failure{Status: 404, Message: "Not Found"}, false, want{CategoryForge, false, "was not found"}},
		"a rate limit":          {&fakeforge.Failure{Status: 429, Message: "slow down"}, false, want{CategoryForge, true, ""}},
		"a server error":        {&fakeforge.Failure{Status: 500, Message: "boom"}, false, want{CategoryForge, true, ""}},
		"a refusal":             {&fakeforge.Failure{Status: 422, Message: "no"}, false, want{CategoryForge, false, ""}},
		"a rejected credential": {&fakeforge.Failure{Status: 401, Message: "Bad credentials"}, false, want{CategoryAuth, false, ""}},
		"a lost connection":     {&fakeforge.Failure{Hangup: true}, false, want{CategoryForge, true, ""}},
	} {
		for _, kind := range []string{fakeforge.GitHub, fakeforge.GitLab} {
			forge := fakeforge.New(t, kind, "o/r")
			forge.Archived = tc.archived
			if tc.fail != nil {
				forge.Fail(fakeforge.OpRepo, *tc.fail)
			}

			refusal := CheckLandTarget(context.Background(), LandCheck{Forge: client(t, forge, "test-token"), Target: target})

			var got want
			if refusal != nil {
				got = want{refusal.Category, refusal.Retryable, tc.want.message}
			}
			if got != tc.want || (refusal != nil && (!strings.Contains(refusal.Message, tc.want.message) || (refusal.Category == CategoryAuth) != (refusal.FixHintEnv != ""))) {
				t.Errorf("%s on %s: refusal = %+v, want %+v, and a hint for auth alone", name, kind, refusal, tc.want)
			}
		}
	}
}

// What only a client's own errors say: no credential of its own, a timeout,
// and a forge that refuses a request.
func TestCheckLandTarget_MapsTheErrorsOfTheClient(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		category  string
		retryable bool
	}{
		"no token":          {fmt.Errorf("repository: %w", issuex.ErrNoToken), CategoryAuth, false},
		"a transport error": {errors.New("Get: connection reset"), CategoryForge, true},
		"a timeout":         {&issuex.HTTPError{Status: 408, Message: "late"}, CategoryForge, true},
		"a forbidden one":   {&issuex.HTTPError{Status: 403, Message: "no"}, CategoryAuth, false},
	} {
		forge := newForgeStub()
		forge.err = tc.err

		refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target})

		if refusal == nil || refusal.Category != tc.category || refusal.Retryable != tc.retryable || forge.asked.Load() != 1 {
			t.Errorf("%s: refusal = %+v (asked %d times), want %s, retryable %v", name, refusal, forge.asked.Load(), tc.category, tc.retryable)
		}
	}
}

// A report that is no issue lands as a new issue: it needs a repository, and
// must fit the body limit of the forge whole, with room for the diagnosis.
func TestPreflight_AReportThatIsNoIssueMustFitANewIssue(t *testing.T) {
	forge := func(limit int) *forgeStub { f := newForgeStub(); f.limit = limit; return f }
	github, gitlab := issuex.GitHubMaxBodyLength, issuex.GitLabMaxBodyLength
	for name, tc := range map[string]struct {
		length    int
		truncated bool
		limit     int
		refused   bool
	}{
		"a cut report":                    {100, true, github, true},
		"48641 bytes on GitHub":           {48641, false, github, true},
		"48640 bytes on GitHub":           {48640, false, github, false},
		"48641 bytes on GitLab":           {48641, false, gitlab, false},
		"the limit of GitLab, less 16896": {gitlab - 16896, false, gitlab, false},
		"one more than that on GitLab":    {gitlab - 16895, false, gitlab, true},
	} {
		for _, kind := range []string{KindText, KindFile, KindStdin} {
			p := PreflightParams{
				Flags: okFlags(), Report: Report{Kind: kind, Body: repeat("x", tc.length)}, Truncated: tc.truncated,
				LandCheck: LandCheck{Forge: forge(tc.limit), Target: target},
			}

			_, refusal := Preflight(context.Background(), p)

			if !tc.refused {
				if refusal != nil {
					t.Errorf("%s as %s: refusal = %+v, want the report accepted", name, kind, refusal)
				}
				continue
			}
			if refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, "--land none") ||
				!strings.Contains(refusal.Message, fmt.Sprintf("it is %d bytes", tc.length)) || !strings.Contains(refusal.Message, fmt.Sprintf("takes %d", tc.limit)) ||
				strings.Contains(refusal.Message, "after it was cut") != tc.truncated {
				t.Errorf("%s as %s: refusal = %+v, want usage with both numbers that suggests --land none", name, kind, refusal)
			}
		}
	}

	noRepo := PreflightParams{Flags: okFlags(), Report: Report{Kind: KindText, Body: "some text"}, LandCheck: LandCheck{Forge: newForgeStub()}}
	if _, refusal := Preflight(context.Background(), noRepo); refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, "--repo") || !strings.Contains(refusal.Message, "--land none") {
		t.Errorf("no repository: refusal = %+v, want usage naming --repo and --land none", refusal)
	}
	noRepo.Flags.Land, noRepo.Truncated, noRepo.Report.Body = LandNone, true, repeat("x", 70000)
	if _, refusal := Preflight(context.Background(), noRepo); refusal != nil {
		t.Errorf("--land none: %+v, want no repository and no size to hold to", refusal)
	}
	issue := PreflightParams{Flags: okFlags(), Report: issueReport(repeat("x", 70000)), LandCheck: LandCheck{Forge: forge(github)}}
	if _, refusal := Preflight(context.Background(), issue); refusal != nil {
		t.Errorf("an issue with a long body: %+v, want it accepted: the landing falls back to a comment", refusal)
	}
}

// The flags are checked before the input, the input before the landing, and
// the forge is not asked while an earlier check refuses.
func TestPreflight_RunsTheChecksInOrder(t *testing.T) {
	pull := issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, Number: 3, IsPullRequest: true}
	forge := newForgeStub()
	forge.err = issuex.ErrNotFound
	p := PreflightParams{
		Flags:     Flags{Budget: 2, MaxTurns: 100, Land: LandIssue, Labels: []string{"a,b"}},
		Report:    Report{Kind: KindIssue, Origin: pull.URL(), Ref: pull},
		LandCheck: LandCheck{Forge: forge},
	}
	for _, want := range []string{"a,b", "pull or merge request"} {
		if _, refusal := Preflight(context.Background(), p); refusal == nil || !strings.Contains(refusal.Message, want) {
			t.Errorf("refusal = %+v, want the earlier check, which mentions %q", refusal, want)
		}
		p.Flags.Labels = nil
	}
	if forge.asked.Load() != 0 {
		t.Errorf("the forge was asked %d times, want none before the earlier checks pass", forge.asked.Load())
	}
	p.Report = issueReport("body")
	if _, refusal := Preflight(context.Background(), p); refusal == nil || !strings.Contains(refusal.Message, "o/r") || forge.asked.Load() != 1 {
		t.Errorf("refusal = %+v, want the repository of the issue asked for and not found", refusal)
	}

	ok := PreflightParams{Flags: Flags{Budget: 2, MaxTurns: 100, Land: LandIssue, Labels: []string{"b", "a", "b"}}, Report: issueReport("body"), LandCheck: LandCheck{Forge: newForgeStub()}}
	ok.Report.Thread.Issue.State = "closed"
	if res, refusal := Preflight(context.Background(), ok); refusal != nil || strings.Join(res.Labels, ",") != "b,a" || len(res.Warnings) != 1 || res.Warnings[0].Code != WarnIssueClosed {
		t.Errorf("Preflight = %+v, %+v, want the labels b,a and the warning issue_closed", res, refusal)
	}
}
