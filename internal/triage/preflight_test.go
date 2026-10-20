package triage

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"
)

// forgeStub is a forge that answers the repository, and counts what it was
// asked.
type forgeStub struct {
	issuex.Client
	authenticated bool
	limit         int
	repo          issuex.Repository
	err           error
	asked         atomic.Int32
}

func newForgeStub() *forgeStub {
	return &forgeStub{Client: issuex.NewNoOp(), authenticated: true, limit: issuex.GitHubMaxBodyLength}
}

func (f *forgeStub) Authenticated() bool { return f.authenticated }
func (f *forgeStub) MaxBodyLength() int  { return f.limit }
func (f *forgeStub) GetRepository(context.Context, issuex.Repo) (issuex.Repository, error) {
	f.asked.Add(1)
	return f.repo, f.err
}

// noToken clears the environment a client takes its token from.
func noToken(t *testing.T) {
	t.Helper()
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GITLAB_TOKEN", "GITHUB_API_URL", "GITLAB_API_URL"} {
		t.Setenv(name, "")
	}
}

func okFlags() Flags { return Flags{Budget: 2, MaxTurns: 100, Land: LandIssue} }

var target = issuex.Repo{Owner: "o", Name: "r"}

// TestFlagsAreRefusedAsUsage_TS_08_9 verifies TS-08-9:
// a bad label, budget or turn bound, and --label with --land none, are usage
// refusals that name what is wrong; ten labels and exact duplicates pass.
// Verifies: 08-REQ-2.2
func TestFlagsAreRefusedAsUsage_TS_08_9(t *testing.T) {
	long := strings.Repeat("l", 51)
	eleven := make([]string, 11)
	for i := range eleven {
		eleven[i] = fmt.Sprintf("label-%d", i)
	}
	for name, tc := range map[string]struct {
		change  func(*Flags)
		mention string
	}{
		"an empty label":            {func(f *Flags) { f.Labels = []string{""} }, "--label"},
		"a label of 51 characters":  {func(f *Flags) { f.Labels = []string{long} }, long},
		"a label with a comma":      {func(f *Flags) { f.Labels = []string{"a,b"} }, "a,b"},
		"a leading space":           {func(f *Flags) { f.Labels = []string{" a"} }, `" a"`},
		"a trailing space":          {func(f *Flags) { f.Labels = []string{"a "} }, `"a "`},
		"eleven distinct labels":    {func(f *Flags) { f.Labels = eleven }, "10"},
		"--label with --land none":  {func(f *Flags) { f.Labels = []string{"x"}; f.Land = LandNone }, "--land none"},
		"a budget of 0":             {func(f *Flags) { f.Budget = 0 }, "--budget"},
		"a negative budget":         {func(f *Flags) { f.Budget = -1 }, "--budget"},
		"a budget of NaN":           {func(f *Flags) { f.Budget = math.NaN() }, "--budget"},
		"an infinite budget":        {func(f *Flags) { f.Budget = math.Inf(1) }, "--budget"},
		"no turns":                  {func(f *Flags) { f.MaxTurns = 0 }, "--max-turns"},
		"a landing that is no mode": {func(f *Flags) { f.Land = "pr" }, "--land"},
	} {
		t.Run(name, func(t *testing.T) {
			f := okFlags()
			tc.change(&f)

			labels, refusal := CheckFlags(f)

			if refusal == nil || refusal.Category != CategoryUsage || refusal.Retryable {
				t.Fatalf("refusal = %+v, want a usage refusal", refusal)
			}
			if !strings.Contains(refusal.Message, tc.mention) {
				t.Errorf("message = %q, want it to mention %q", refusal.Message, tc.mention)
			}
			if labels != nil {
				t.Errorf("labels = %v from a refused run", labels)
			}
		})
	}

	exactly := strings.Repeat("é", 50)
	f := okFlags()
	f.Labels = []string{exactly}
	if labels, refusal := CheckFlags(f); refusal != nil || len(labels) != 1 {
		t.Errorf("a label of exactly 50 characters (100 bytes): %v, %+v, want it accepted", labels, refusal)
	}
	ten := eleven[:10]
	f = okFlags()
	f.Labels = ten
	if labels, refusal := CheckFlags(f); refusal != nil || len(labels) != 10 {
		t.Errorf("ten labels: %v, %+v", labels, refusal)
	}
	f.Labels = []string{"x", "y", "x", "x", "z", "y"}
	if labels, refusal := CheckFlags(f); refusal != nil || strings.Join(labels, ",") != "x,y,z" {
		t.Errorf("duplicates: %v, %+v, want x,y,z collapsed in first-seen order", labels, refusal)
	}
	f.Labels = append(append([]string{}, ten...), ten...)
	if _, refusal := CheckFlags(f); refusal != nil {
		t.Errorf("ten distinct labels given twice: %+v, want them collapsed and accepted", refusal)
	}
	f = okFlags()
	f.Land = LandNone
	if labels, refusal := CheckFlags(f); refusal != nil || len(labels) != 0 {
		t.Errorf("--land none without labels: %v, %+v", labels, refusal)
	}
	f.Land = ""
	if _, refusal := CheckFlags(f); refusal != nil {
		t.Errorf("no --land: %+v, want the default, issue", refusal)
	}
}

// TestAPullOrMergeRequestIsRefused_TS_08_10 verifies TS-08-10:
// an issue ref that is a pull or merge request is a usage refusal.
// Verifies: 08-REQ-2.3
func TestAPullOrMergeRequestIsRefused_TS_08_10(t *testing.T) {
	for name, host := range map[string]string{"GitHub": "github.com", "GitLab": "gitlab.com"} {
		t.Run(name, func(t *testing.T) {
			ref := issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: host}, Number: 3, IsPullRequest: true}

			_, refusal := CheckInput(Report{Kind: KindIssue, Origin: ref.URL(), Ref: ref})

			if refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, "pull or merge request") {
				t.Errorf("refusal = %+v, want usage that says triage takes an issue and not a pull or merge request", refusal)
			}
		})
	}
}

// TestAClosedIssueRaisesAWarningAndContinues_TS_08_11 verifies TS-08-11:
// a closed issue is the warning issue_closed and no refusal; an open one, and a
// report that is no issue, none.
// Verifies: 08-REQ-2.4
func TestAClosedIssueRaisesAWarningAndContinues_TS_08_11(t *testing.T) {
	closed := issueReport("body")
	closed.Thread.Issue.State = "closed"
	warnings, refusal := CheckInput(closed)
	if refusal != nil || len(warnings) != 1 || warnings[0].Code != WarnIssueClosed {
		t.Errorf("a closed issue: %+v, %+v, want the warning issue_closed", warnings, refusal)
	}
	if warnings, refusal := CheckInput(issueReport("body")); refusal != nil || len(warnings) != 0 {
		t.Errorf("an open issue: %+v, %+v", warnings, refusal)
	}
	if warnings, refusal := CheckInput(Report{Kind: KindText, Body: "text"}); refusal != nil || len(warnings) != 0 {
		t.Errorf("a text report: %+v, %+v", warnings, refusal)
	}
}

// TestANonIssueReportWithNoTargetIsRefused_TS_08_12 verifies TS-08-12:
// a text, file or stdin report with no repository to write to is a usage
// refusal that names --repo and --land none, unless nothing is to be written.
// Verifies: 08-REQ-2.5
func TestANonIssueReportWithNoTargetIsRefused_TS_08_12(t *testing.T) {
	for _, kind := range []string{KindText, KindFile, KindStdin} {
		p := PreflightParams{Flags: okFlags(), Report: Report{Kind: kind, Body: "some text"}, LandCheck: LandCheck{Forge: newForgeStub()}}

		_, refusal := Preflight(context.Background(), p)

		if refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, "--repo") || !strings.Contains(refusal.Message, "--land none") {
			t.Errorf("%s: refusal = %+v, want usage naming --repo and --land none", kind, refusal)
		}
		p.Flags.Land = LandNone
		if _, refusal := Preflight(context.Background(), p); refusal != nil {
			t.Errorf("%s with --land none: %+v, want it accepted", kind, refusal)
		}
	}
}

// TestAnUnauthenticatedForgeIsRefusedWithTheCredentialMessage_TS_08_13 verifies TS-08-13:
// without a credential, a run that writes is refused as auth with the message and
// the variable to set; a dry run and --land none are not.
// Verifies: 08-REQ-2.6
func TestAnUnauthenticatedForgeIsRefusedWithTheCredentialMessage_TS_08_13(t *testing.T) {
	noToken(t)
	// The clients are never asked anything: whether they hold a credential is
	// known when they are made.
	const nowhere = "http://127.0.0.1:1"
	github, err := issuex.NewGitHub(issuex.Options{BaseURL: nowhere})
	if err != nil {
		t.Fatal(err)
	}
	gitlab, err := issuex.NewGitLab(issuex.Options{BaseURL: nowhere})
	if err != nil {
		t.Fatal(err)
	}
	if github.Authenticated() || gitlab.Authenticated() {
		t.Fatal("a test client is authenticated")
	}

	for name, tc := range map[string]struct {
		forge issuex.Client
		repo  issuex.Repo
		env   string
	}{
		"GitHub":                       {github, issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, "GITHUB_TOKEN"},
		"GitLab":                       {gitlab, issuex.Repo{Owner: "o", Name: "r", Host: "gitlab.com"}, "GITLAB_TOKEN"},
		"GitLab with no host recorded": {gitlab, target, "GITLAB_TOKEN"},
		"GitHub with no host recorded": {github, target, "GITHUB_TOKEN"},
	} {
		t.Run(name, func(t *testing.T) {
			c := LandCheck{Forge: tc.forge, Target: tc.repo}

			refusal := CheckLandTarget(context.Background(), c)

			want := fmt.Sprintf("creating or updating an issue in %s needs a credential: set GITHUB_TOKEN, GH_TOKEN, or GITLAB_TOKEN, or pass --dry-run or --land none", tc.repo)
			if refusal == nil || refusal.Category != CategoryAuth || refusal.Message != want || refusal.FixHintEnv != tc.env || refusal.Retryable {
				t.Errorf("refusal = %+v, want auth with %q and the hint %s", refusal, want, tc.env)
			}
			c.DryRun = true
			if refusal := CheckLandTarget(context.Background(), c); refusal != nil {
				t.Errorf("a dry run: %+v, want it accepted without a credential", refusal)
			}
		})
	}
}

// TestRepositoryFailuresAreRefusedWithTheMappedCategory_TS_08_14 verifies TS-08-14:
// a repository that cannot be read, or is archived, is refused before anything is
// spent, as auth, as forge not retryable, or as forge retryable.
// Verifies: 08-REQ-2.7
func TestRepositoryFailuresAreRefusedWithTheMappedCategory_TS_08_14(t *testing.T) {
	for name, tc := range map[string]struct {
		err       error
		archived  bool
		category  string
		retryable bool
		message   string
	}{
		"no token":          {fmt.Errorf("repository: %w", issuex.ErrNoToken), false, CategoryAuth, false, ""},
		"not found":         {fmt.Errorf("%w: gone", issuex.ErrNotFound), false, CategoryForge, false, "o/r"},
		"a rate limit":      {fmt.Errorf("%w: slow down", issuex.ErrRateLimited), false, CategoryForge, true, ""},
		"a transport error": {fmt.Errorf("Get: connection reset"), false, CategoryForge, true, ""},
		"an HTTP 500":       {&issuex.HTTPError{Status: 500, Message: "boom"}, false, CategoryForge, true, ""},
		"an HTTP 422":       {&issuex.HTTPError{Status: 422, Message: "no"}, false, CategoryForge, false, ""},
		"archived":          {nil, true, CategoryForge, false, "repository is archived"},
	} {
		t.Run(name, func(t *testing.T) {
			forge := newForgeStub()
			forge.err, forge.repo = tc.err, issuex.Repository{FullName: "o/r", Archived: tc.archived}

			refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target})

			if refusal == nil || refusal.Category != tc.category || refusal.Retryable != tc.retryable || !strings.Contains(refusal.Message, tc.message) {
				t.Errorf("refusal = %+v, want %s, retryable %v, mentioning %q", refusal, tc.category, tc.retryable, tc.message)
			}
			if tc.category == CategoryAuth && refusal.FixHintEnv == "" {
				t.Error("an auth refusal names no variable to set")
			}
			if forge.asked.Load() != 1 {
				t.Errorf("the repository was asked for %d times, want once", forge.asked.Load())
			}
		})
	}

	forge := newForgeStub()
	forge.repo = issuex.Repository{FullName: "o/r"}
	if refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target}); refusal != nil {
		t.Errorf("a repository that is fine: %+v", refusal)
	}
	forge = newForgeStub()
	forge.err = issuex.ErrNotFound
	if refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target, DryRun: true}); refusal != nil || forge.asked.Load() != 0 {
		t.Errorf("a dry run: %+v, asked %d times, want no request and no refusal", refusal, forge.asked.Load())
	}
}

// TestATruncatedOrOversizedReportIsRefusedWithBothNumbers_TS_08_15 verifies TS-08-15:
// a text, file or stdin report that was cut, or would not fit the body limit of
// the forge with 16896 bytes of diagnosis and framing, is a usage refusal that
// gives both numbers and suggests --land none.
// Verifies: 08-REQ-2.8
func TestATruncatedOrOversizedReportIsRefusedWithBothNumbers_TS_08_15(t *testing.T) {
	forge := func(limit int) *forgeStub {
		f := newForgeStub()
		f.limit = limit
		f.repo = issuex.Repository{FullName: "o/r"}
		return f
	}
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
		"a short report":                  {10, false, github, false},
	} {
		for _, kind := range []string{KindText, KindFile, KindStdin} {
			t.Run(name+" as "+kind, func(t *testing.T) {
				p := PreflightParams{
					Flags: okFlags(), Report: Report{Kind: kind, Body: strings.Repeat("x", tc.length)}, Truncated: tc.truncated,
					LandCheck: LandCheck{Forge: forge(tc.limit), Target: target},
				}

				_, refusal := Preflight(context.Background(), p)

				if !tc.refused {
					if refusal != nil {
						t.Fatalf("refusal = %+v, want the report accepted", refusal)
					}
					return
				}
				if refusal == nil || refusal.Category != CategoryUsage || !strings.Contains(refusal.Message, "--land none") {
					t.Fatalf("refusal = %+v, want usage that suggests --land none", refusal)
				}
				if !tc.truncated && !(strings.Contains(refusal.Message, fmt.Sprint(tc.length)) && strings.Contains(refusal.Message, fmt.Sprint(tc.limit))) {
					t.Errorf("message = %q, want it to give the length %d and the limit %d", refusal.Message, tc.length, tc.limit)
				}
			})
		}
	}

	// A report that is not to land whole has no size to hold to, and an issue's
	// body is measured when it lands.
	f := forge(issuex.GitHubMaxBodyLength)
	p := PreflightParams{Flags: okFlags(), Report: Report{Kind: KindText, Body: strings.Repeat("x", 70000)}, Truncated: true, LandCheck: LandCheck{Forge: f, Target: target}}
	p.Flags.Land = LandNone
	if _, refusal := Preflight(context.Background(), p); refusal != nil {
		t.Errorf("--land none: %+v, want no size check", refusal)
	}
	issue := PreflightParams{Flags: okFlags(), Report: issueReport(strings.Repeat("x", 70000)), LandCheck: LandCheck{Forge: f, Target: target}}
	if _, refusal := Preflight(context.Background(), issue); refusal != nil {
		t.Errorf("an issue with a long body: %+v, want it accepted: the landing falls back to a comment", refusal)
	}
}

// TestPreflightRunsTheChecksInOrder guards 08-REQ-2.1: the flags before the
// input before the landing, and the forge is not asked when an earlier check
// refuses.
func TestPreflightRunsTheChecksInOrder(t *testing.T) {
	pull := issuex.IssueRef{Repo: issuex.Repo{Owner: "o", Name: "r", Host: "github.com"}, Number: 3, IsPullRequest: true}
	forge := newForgeStub()
	forge.err = issuex.ErrNotFound
	p := PreflightParams{
		Flags:     Flags{Budget: 2, MaxTurns: 100, Land: LandIssue, Labels: []string{"a,b"}},
		Report:    Report{Kind: KindIssue, Origin: pull.URL(), Ref: pull},
		LandCheck: LandCheck{Forge: forge, Target: pull.Repo},
	}

	_, refusal := Preflight(context.Background(), p)
	if refusal == nil || !strings.Contains(refusal.Message, "a,b") {
		t.Errorf("flags and input both wrong: %+v, want the flags reported first", refusal)
	}
	p.Flags.Labels = nil
	_, refusal = Preflight(context.Background(), p)
	if refusal == nil || !strings.Contains(refusal.Message, "pull or merge request") {
		t.Errorf("input and landing both wrong: %+v, want the input reported first", refusal)
	}
	if forge.asked.Load() != 0 {
		t.Errorf("the forge was asked %d times, want none before the earlier checks pass", forge.asked.Load())
	}

	ok := PreflightParams{
		Flags: Flags{Budget: 2, MaxTurns: 100, Land: LandIssue, Labels: []string{"b", "a", "b"}}, Report: issueReport("body"),
		LandCheck: LandCheck{Forge: func() *forgeStub { f := newForgeStub(); f.repo = issuex.Repository{FullName: "o/r"}; return f }(), Target: issueRef().Repo},
	}
	ok.Report.Thread.Issue.State = "closed"
	res, refusal := Preflight(context.Background(), ok)
	if refusal != nil || strings.Join(res.Labels, ",") != "b,a" || len(res.Warnings) != 1 || res.Warnings[0].Code != WarnIssueClosed {
		t.Errorf("Preflight = %+v, %+v, want the labels b,a and the warning issue_closed", res, refusal)
	}
}

// TestRepositoryFailuresThroughRealClients guards 08-REQ-2.7 with the clients
// of both forges over httptest servers, as the failures arrive from them.
func TestRepositoryFailuresThroughRealClients(t *testing.T) {
	noToken(t)
	type answer struct {
		status int
		body   string
	}
	for name, tc := range map[string]struct {
		answer    answer
		category  string
		retryable bool
		message   string
	}{
		"found":                 {answer{200, `{"full_name":"o/r","path_with_namespace":"o/r","archived":false}`}, "", false, ""},
		"archived":              {answer{200, `{"full_name":"o/r","path_with_namespace":"o/r","archived":true}`}, CategoryForge, false, "repository is archived"},
		"not found":             {answer{404, `{"message":"Not Found"}`}, CategoryForge, false, "was not found"},
		"a rate limit":          {answer{429, `{"message":"slow down"}`}, CategoryForge, true, ""},
		"a server error":        {answer{500, `{"message":"boom"}`}, CategoryForge, true, ""},
		"a rejected credential": {answer{401, `{"message":"Bad credentials"}`}, CategoryAuth, false, ""},
	} {
		for forgeName, build := range map[string]func(string) (issuex.Client, error){
			"GitHub": func(url string) (issuex.Client, error) {
				return issuex.NewGitHub(issuex.Options{BaseURL: url, Token: "test-token"})
			},
			"GitLab": func(url string) (issuex.Client, error) {
				return issuex.NewGitLab(issuex.Options{BaseURL: url, Token: "test-token"})
			},
		} {
			t.Run(name+" on "+forgeName, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.answer.status)
					fmt.Fprint(w, tc.answer.body)
				}))
				defer srv.Close()
				forge, err := build(srv.URL)
				if err != nil {
					t.Fatal(err)
				}
				defer forge.Close()

				refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target})

				if tc.category == "" {
					if refusal != nil {
						t.Errorf("refusal = %+v, want none", refusal)
					}
					return
				}
				if refusal == nil || refusal.Category != tc.category || refusal.Retryable != tc.retryable || !strings.Contains(refusal.Message, tc.message) {
					t.Errorf("refusal = %+v, want %s, retryable %v, mentioning %q", refusal, tc.category, tc.retryable, tc.message)
				}
			})
		}
	}

	t.Run("a lost connection", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		forge, err := issuex.NewGitHub(issuex.Options{BaseURL: srv.URL, Token: "test-token"})
		if err != nil {
			t.Fatal(err)
		}
		srv.Close()

		refusal := CheckLandTarget(context.Background(), LandCheck{Forge: forge, Target: target})

		if refusal == nil || refusal.Category != CategoryForge || !refusal.Retryable {
			t.Errorf("refusal = %+v, want forge, retryable", refusal)
		}
	})
}
