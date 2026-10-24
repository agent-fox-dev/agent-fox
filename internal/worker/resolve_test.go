package worker

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// TestVariablesApplyWithTheirDefaultsAndRangesAndReachTheLoopAsFlags_TS_13_27 verifies TS-13-27:
// variables that are unset or empty take the defaults, the boundary values of the ranges are
// accepted, unknown variables (AF_BRAIN among them) are ignored, and the loop is given the
// resolved values as flags.
// Verifies: 13-REQ-4.1
func TestVariablesApplyWithTheirDefaultsAndRangesAndReachTheLoopAsFlags_TS_13_27(t *testing.T) {
	model := catalog.Default().DefaultModelID("anthropic")
	if model == "" {
		t.Fatal("the catalog has no default Anthropic model")
	}
	cases := []struct {
		name    string
		kind    jobs.Kind
		vars    map[string]string
		want    Settings
		options afcmd.LoopOptions
	}{
		{"triage defaults", jobs.KindTriage, nil,
			Settings{Brain: "agentkit", Model: model, Budget: 2, MaxTurns: 100, Timeout: 15 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 2, MaxTurns: 100}},
		{"fix defaults", jobs.KindFix, map[string]string{},
			Settings{Brain: "agentkit", Model: model, Budget: 10, MaxTurns: 150, Timeout: 45 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"empty values are unset", jobs.KindFix,
			map[string]string{"AF_MODEL": "", "AF_MAX_BUDGET_USD": "", "AF_MAX_TURNS": "", "AF_TIMEOUT": "", "AF_MAX_ATTEMPTS": "", "AF_SANDBOX_PROFILE": "", "AF_FIX_LAND": ""},
			Settings{Brain: "agentkit", Model: model, Budget: 10, MaxTurns: 150, Timeout: 45 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"the lower boundaries", jobs.KindTriage,
			map[string]string{"AF_TIMEOUT": "1m", "AF_MAX_ATTEMPTS": "1", "AF_MAX_TURNS": "1", "AF_MAX_BUDGET_USD": "0.01", "AF_MODEL": "m-1", "AF_SANDBOX_PROFILE": "py"},
			Settings{Brain: "agentkit", Model: "m-1", Budget: 0.01, MaxTurns: 1, Timeout: time.Minute, MaxAttempts: 1, Profile: "py", Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 0.01, MaxTurns: 1}},
		{"the upper boundaries", jobs.KindFix,
			map[string]string{"AF_TIMEOUT": "24h", "AF_MAX_ATTEMPTS": "10", "AF_FIX_LAND": "branch"},
			Settings{Brain: "agentkit", Model: model, Budget: 10, MaxTurns: 150, Timeout: 24 * time.Hour, MaxAttempts: 10, Land: "branch", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "branch"}},
		{"fractions of a dollar", jobs.KindTriage, map[string]string{"AF_MAX_BUDGET_USD": "12.375"},
			Settings{Brain: "agentkit", Model: model, Budget: 12.375, MaxTurns: 100, Timeout: 15 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 12.375, MaxTurns: 100}},
		{"very small and very large budgets", jobs.KindFix, map[string]string{"AF_MAX_BUDGET_USD": "0.00001"},
			Settings{Brain: "agentkit", Model: model, Budget: 0.00001, MaxTurns: 150, Timeout: 45 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 0.00001, MaxTurns: 150, Land: "pr"}},
		{"unknown variables are ignored", jobs.KindFix, map[string]string{"AF_UNKNOWN": "x", "PATH": "/nowhere", "AF_LABEL_PREFIX": "z:", "AF_SANDBOX": "none", "AF_BRAIN": "claude", "AF_BRAIN_FIX": "scripted"},
			Settings{Brain: "agentkit", Model: model, Budget: 10, MaxTurns: 150, Timeout: 45 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"a variable of the other kind is not looked at", jobs.KindTriage, map[string]string{"AF_FIX_LAND": "weird"},
			Settings{Brain: "agentkit", Model: model, Budget: 2, MaxTurns: 100, Timeout: 15 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true},
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 2, MaxTurns: 100}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, bad := resolveSettings(tc.kind, tc.vars)
			if bad != nil {
				t.Fatalf("resolveSettings refused %v: %v", tc.vars, bad)
			}
			if got != tc.want {
				t.Errorf("settings = %+v\nwant     %+v", got, tc.want)
			}
			if options := got.options(tc.kind); options != tc.options {
				t.Errorf("options = %+v, want %+v", options, tc.options)
			}
		})
	}
	if got, _ := resolveSettings(jobs.KindFix, map[string]string{"AF_FIX_LAND": "none"}); got.Land != "none" {
		t.Errorf("AF_FIX_LAND none = %q", got.Land)
	}
}

// badVariables are values out of range, with the variable each is the fault of.
var badVariables = []struct {
	kind  jobs.Kind
	name  string
	value string
}{
	{jobs.KindFix, "AF_MAX_BUDGET_USD", "0"},
	{jobs.KindFix, "AF_MAX_BUDGET_USD", "NaN"},
	{jobs.KindFix, "AF_MAX_BUDGET_USD", "-3"},
	{jobs.KindTriage, "AF_MAX_BUDGET_USD", "lots"},
	{jobs.KindTriage, "AF_MAX_BUDGET_USD", "Inf"},
	{jobs.KindFix, "AF_MAX_TURNS", "0"},
	{jobs.KindFix, "AF_MAX_TURNS", "1.5"},
	{jobs.KindTriage, "AF_MAX_TURNS", "many"},
	{jobs.KindFix, "AF_TIMEOUT", "30s"},
	{jobs.KindFix, "AF_TIMEOUT", "25h"},
	{jobs.KindTriage, "AF_TIMEOUT", "soon"},
	{jobs.KindTriage, "AF_TIMEOUT", "59s"},
	{jobs.KindFix, "AF_MAX_ATTEMPTS", "11"},
	{jobs.KindFix, "AF_MAX_ATTEMPTS", "0"},
	{jobs.KindFix, "AF_MAX_ATTEMPTS", "two"},
	{jobs.KindFix, "AF_FIX_LAND", "weird"},
	{jobs.KindFix, "AF_FIX_LAND", "PR"},
}

// TestAVariableOutOfRangeFailsTheAttemptAsConfigBeforeASessionOpens_TS_13_28 verifies TS-13-28:
// a failed envelope of category config that is not retryable, whose fix_hint.var names the
// variable and whose message names the variable and its value, and no request for a session.
// Verifies: 13-REQ-4.2
func TestAVariableOutOfRangeFailsTheAttemptAsConfigBeforeASessionOpens_TS_13_28(t *testing.T) {
	for _, bad := range badVariables {
		name := fmt.Sprintf("%s %s=%q", bad.kind, bad.name, bad.value)
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{Vars: map[string]string{bad.name: bad.value}})

			res, err := e.run(e.request(bad.kind, issueInput("42")))

			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}
			env := envelope(t, res)
			category, message, retryable, hint := errorOf(env)
			if env["status"] != "failed" || category != "config" || retryable {
				t.Fatalf("status %v, category %q, retryable %v, message %q; want failed, config, not retryable", env["status"], category, retryable, message)
			}
			if hint["var"] != bad.name {
				t.Errorf("fix_hint = %v, want var %s", hint, bad.name)
			}
			if !strings.Contains(message, bad.name) || !strings.Contains(message, bad.value) {
				t.Errorf("the message %q does not name %s and %q", message, bad.name, bad.value)
			}
			if env["tool"] != string(bad.kind) {
				t.Errorf("tool = %v, want %s", env["tool"], bad.kind)
			}
			if got := e.hub.RequestsTo("POST", "/api/v1/sessions"); len(got) != 0 {
				t.Errorf("%d requests for a session, want none", len(got))
			}
			if len(e.writes()) != 0 {
				t.Errorf("writes to hub: %v", e.writes())
			}
			if res.MaxAttempts != 2 && res.MaxAttempts != 0 {
				t.Errorf("MaxAttempts = %d", res.MaxAttempts)
			}
		})
	}
}

// TestAFixOnACarryPatchWorkspaceOrWithPatchLandingIsRefusedAsUnsupported_TS_13_31 verifies
// TS-13-31.
// Verifies: 13-REQ-4.6
func TestAFixOnACarryPatchWorkspaceOrWithPatchLandingIsRefusedAsUnsupported_TS_13_31(t *testing.T) {
	for name, w := range map[string]fakehub.Workspace{
		"a carry_patch workspace": {Mode: "carry_patch", Files: map[string]string{"a": "b"}},
		"AF_FIX_LAND=patch":       {Vars: map[string]string{"AF_FIX_LAND": "patch"}, Files: map[string]string{"a": "b"}},
		"both":                    {Mode: "carry_patch", Vars: map[string]string{"AF_FIX_LAND": "patch"}, Files: map[string]string{"a": "b"}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, w)
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T", Labels: []string{"af:fix"}})

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			if err != nil {
				t.Fatalf("Run returned an error: %v", err)
			}
			env := envelope(t, res)
			category, message, retryable, _ := errorOf(env)
			if env["status"] != "failed" || category != "unsupported" || retryable || !strings.Contains(message, "carry-patch landing comes in a later release") {
				t.Errorf("status %v, category %q, retryable %v, message %q", env["status"], category, retryable, message)
			}
			if got := e.hub.RequestsTo("POST", "/api/v1/sessions"); len(got) != 0 {
				t.Errorf("a session was requested")
			}
			if got := e.hub.GitRequests(); len(got) != 0 {
				t.Errorf("the git server saw %d requests", len(got))
			}
			if len(e.hub.Leases()) != 0 {
				t.Errorf("a lease was requested")
			}
			if issue, _ := e.hub.IssueOf(slug, 42); len(issue.Comments) != 0 || !slices.Equal(issue.Labels, []string{"af:fix"}) {
				t.Errorf("the issue was written to: %+v", issue)
			}
			for _, r := range e.writes() {
				t.Errorf("a write: %s %s", r.Method, r.Path)
			}
		})
	}
}

// TestIssueFormsBecomeTheIssueURLAndRefusalsAreUsageEnvelopesWithNoWrite_TS_13_61 verifies
// TS-13-61.
// Verifies: 13-REQ-8.5
func TestIssueFormsBecomeTheIssueURLAndRefusalsAreUsageEnvelopesWithNoWrite_TS_13_61(t *testing.T) {
	type tc struct {
		name, raw string
		want      string // the issue URL, or "" for a refusal
		refusal   string
	}
	github := []tc{
		{"a number", `42`, "https://github.example.com/Org/Repo/issues/42", ""},
		{"owner/repo#N", `"org/repo#42"`, "https://github.example.com/Org/Repo/issues/42", ""},
		{"owner/repo#N in the case of the workspace", `"Org/Repo#42"`, "https://github.example.com/Org/Repo/issues/42", ""},
		{"a URL on the repository", `"https://github.example.com/Org/Repo/issues/42"`, "https://github.example.com/Org/Repo/issues/42", ""},
		{"a URL in another case", `"https://github.example.com/org/REPO/issues/42"`, "https://github.example.com/Org/Repo/issues/42", ""},
		{"another path", `"other/repo#42"`, "", "not on the workspace's repository"},
		{"another owner with the name of the repository", `"https://github.example.com/Other/Repo/issues/42"`, "", "not on the workspace's repository"},
		{"another owner in a path", `"Other/Repo#42"`, "", "not on the workspace's repository"},
		{"a URL on another repository", `"https://github.example.com/Org/Other/issues/42"`, "", "not on the workspace's repository"},
		{"a URL on another host", `"https://github.com/Org/Repo/issues/42"`, "", "not on the workspace's repository"},
		{"a pull request", `43`, "", "pull request"},
		{"an issue that is not there", `99`, "", "does not exist"},
	}
	gitlab := []tc{
		{"a number", `7`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
		{"the full namespace", `"Group/Sub/Project#7"`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
		{"a URL", `"https://gitlab.example.com/group/sub/project/-/issues/7"`, "https://gitlab.example.com/group/sub/project/-/issues/7", ""},
		{"the project alone", `"project#7"`, "", "not on the workspace's repository"},
		{"a namespace that is a part", `"sub/project#7"`, "", "not on the workspace's repository"},
		{"another group", `"other/sub/project#7"`, "", "not on the workspace's repository"},
	}
	for forge, cases := range map[string][]tc{"https://github.example.com/Org/Repo": github, "https://gitlab.example.com/group/sub/project": gitlab} {
		for _, c := range cases {
			t.Run(forge+" "+c.name, func(t *testing.T) {
				e := newEnv(t, fakehub.Workspace{ForgeURL: forge})
				e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T"})
				e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "A pull request", IsPR: true})
				e.hub.AddIssue(slug, fakehub.Issue{Number: 7, Title: "T"})
				ctx := context.Background()
				a := &attempt{req: e.request(jobs.KindFix, issueInput(c.raw)), report: e.sink, started: time.Now()}

				out := e.runner.resolve(ctx, a)

				if c.want != "" {
					if out != nil {
						t.Fatalf("resolve refused: %+v", out)
					}
					if a.input.url != c.want || a.input.kind != "issue" {
						t.Errorf("the issue is %q (%s), want %q", a.input.url, a.input.kind, c.want)
					}
					return
				}
				if out == nil || out.err != nil {
					t.Fatalf("resolve = %+v, want a refusal", out)
				}
				env := envelope(t, out.result)
				category, message, retryable, _ := errorOf(env)
				if env["status"] != "usage" || category != "usage" || retryable || !strings.Contains(message, c.refusal) {
					t.Errorf("status %v, category %q, retryable %v, message %q; want a usage refusal that says %q", env["status"], category, retryable, message, c.refusal)
				}
				if len(e.writes()) != 0 {
					t.Errorf("writes to hub: %v", e.writes())
				}
			})
		}
	}

	t.Run("text and a file", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		for name, in := range map[string]jobs.Input{
			"text":               {Text: "the login fails", Title: "Login"},
			"a file":             {File: &jobs.File{Name: "report.txt", Content: "body"}},
			"text with no title": {Text: "just text"},
		} {
			a := &attempt{req: e.request(jobs.KindTriage, in), report: e.sink, started: time.Now()}
			if out := e.runner.resolve(context.Background(), a); out != nil {
				t.Fatalf("%s: resolve refused: %+v", name, out)
			}
			if a.input.kind == "issue" || a.input.url != "" {
				t.Errorf("%s: input = %+v", name, a.input)
			}
		}
		for name, tc := range map[string]struct {
			in   jobs.Input
			kind string
			text string
		}{
			"text with a title":  {jobs.Input{Text: "the login fails", Title: "Login"}, "text", "# Login\n\nthe login fails"},
			"text with no title": {jobs.Input{Text: "just text"}, "text", "just text"},
			"a file":             {jobs.Input{File: &jobs.File{Name: "report.txt", Content: "body"}}, "file", ""},
		} {
			a := &attempt{req: e.request(jobs.KindTriage, tc.in), report: e.sink, started: time.Now()}
			if out := e.runner.resolve(context.Background(), a); out != nil {
				t.Fatalf("%s: resolve refused: %+v", name, out)
			}
			if a.input.kind != tc.kind || a.input.text != tc.text || (tc.kind == "file") != (a.input.file != nil) {
				t.Errorf("%s: input = %+v, want kind %s and text %q", name, a.input, tc.kind, tc.text)
			}
		}
	})
}

// TestTheSessionModelAndRunIDAreTheOnesTheAttemptUses_TS_13_74 verifies TS-13-74 as far as the
// session: with AF_MODEL unset the model is the catalog's default Anthropic one, and with
// AF_MODEL=m1 it is m1; the run id is drawn in the form of a run id and is the one the recorder
// is told with the session; the session is opened with the archetype of the job.
// Verifies: 13-REQ-10.1
func TestTheSessionModelAndRunIDAreTheOnesTheAttemptUses_TS_13_74(t *testing.T) {
	runIDForm := regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)
	for _, tc := range []struct {
		name  string
		vars  map[string]string
		model string
		kind  jobs.Kind
	}{
		{"unset", nil, catalog.Default().DefaultModelID("anthropic"), jobs.KindFix},
		{"AF_MODEL=m1", map[string]string{"AF_MODEL": "m1"}, "m1", jobs.KindTriage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{Vars: tc.vars})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T"})
			a := &attempt{req: e.request(tc.kind, issueInput("42")), report: e.sink, started: time.Now()}
			ctx := context.Background()

			if out := e.runner.resolve(ctx, a); out != nil {
				t.Fatalf("resolve: %+v", out)
			}
			if a.settings.Model != tc.model {
				t.Errorf("the model of the settings is %q, want %q", a.settings.Model, tc.model)
			}
			if out := e.runner.openSession(ctx, a); out != nil {
				t.Fatalf("session: %+v", out)
			}

			sessions := e.hub.Sessions()
			if len(sessions) != 1 {
				t.Fatalf("%d sessions, want 1", len(sessions))
			}
			s := sessions[0]
			if s.Model != tc.model || s.Archetype != string(tc.kind) || s.Slug != slug || !runIDForm.MatchString(s.RunID) {
				t.Errorf("session = %+v, want model %q, archetype %s and a run id in the form of one", s, tc.model, tc.kind)
			}
			if a.runID != s.RunID || a.sessionID != s.ID || a.sessionID == "" {
				t.Errorf("the attempt holds session %q and run %q, the hub %q and %q", a.sessionID, a.runID, s.ID, s.RunID)
			}
			if got := e.sink.lastHub(); got != [3]string{s.ID, s.RunID, ""} {
				t.Errorf("the recorder was told %v, want the session and the run, and no sandbox yet", got)
			}
			// The hub saw the PAT, and the issue was read before the session was opened.
			var reads, open int
			for _, r := range e.hub.Requests() {
				switch {
				case r.Method == "GET" && strings.HasSuffix(r.Path, "/issues/42"):
					reads = r.Seq
				case r.Method == "POST" && r.Path == "/api/v1/sessions":
					open = r.Seq
				}
				if r.TokenHash != fakehub.TokenHash(pat) {
					t.Errorf("request %s %s was not made with the PAT", r.Method, r.Path)
				}
			}
			if reads == 0 || open == 0 || reads > open {
				t.Errorf("the issue was read at %d and the session opened at %d, want the read first", reads, open)
			}
			if len(e.writes()) != 1 {
				t.Errorf("writes to hub: %v, want the session alone", e.writes())
			}
		})
	}
}

// TestASessionThatCannotOpenStopsTheAttemptWithNoForgeWriteAndNoLease_TS_13_75 verifies
// TS-13-75.
// Verifies: 13-REQ-10.2
func TestASessionThatCannotOpenStopsTheAttemptWithNoForgeWriteAndNoLease_TS_13_75(t *testing.T) {
	for _, status := range []int{503, 401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T"})
			e.hub.Inject("POST", "/api/v1/sessions", fakehub.Fault{Status: status})

			res, err := e.run(e.request(jobs.KindFix, issueInput("42")))

			switch status {
			case 503:
				failure := transient(err)
				if failure == nil || failure.Category != CategoryHub || !failure.Retryable || len(res.Envelope) != 0 {
					t.Errorf("Run = %+v, %v; want a transient error of category hub, retryable, and no envelope", res, err)
				}
			default:
				if err != nil {
					t.Fatalf("Run returned an error: %v", err)
				}
				env := envelope(t, res)
				category, message, retryable, _ := errorOf(env)
				if env["status"] != "failed" || category != "auth" || retryable || !strings.Contains(message, "PUT /api/v1/workspaces/acme") {
					t.Errorf("status %v, category %q, retryable %v, message %q; want a failed auth envelope that points to PUT /api/v1/workspaces/acme", env["status"], category, retryable, message)
				}
			}
			for _, r := range e.writes() {
				if r.Path != "/api/v1/sessions" {
					t.Errorf("a write besides the session: %s %s", r.Method, r.Path)
				}
			}
			if len(e.hub.Leases()) != 0 || len(e.hub.GitRequests()) != 0 {
				t.Errorf("a lease or a clone was requested")
			}
			if issue, _ := e.hub.IssueOf(slug, 42); len(issue.Labels) != 0 || len(issue.Comments) != 0 {
				t.Errorf("the issue was written to: %+v", issue)
			}
			if got := e.sink.lastHub(); got != [3]string{} {
				t.Errorf("the recorder was told %v", got)
			}
		})
	}
}

// TestResolveRefusesWhatHubAndTheStoreRefuseAndMakesTransientWhatIsUnavailable covers the steps
// of resolve that are not a variable or an input: the token that cannot be read, the record and
// the variables and the issue that hub does not give.
func TestResolveRefusesWhatHubAndTheStoreRefuseAndMakesTransientWhatIsUnavailable(t *testing.T) {
	type tc struct {
		name      string
		setup     func(e *env)
		category  string // of the envelope, or of the transient error
		transient bool
		message   string
	}
	cases := []tc{
		{"a token that is gone", func(e *env) { delete(e.subs.tokens, slug) }, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"a token that does not open", func(e *env) { e.subs.tokenErr[slug] = store.ErrUnreadable }, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"a subscription that is gone", func(e *env) { e.subs.tokenErr[slug] = store.ErrNotSubscribed }, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"a record that hub refuses", func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 401}) }, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"a record that hub forbids", func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 403}) }, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"a record that is not there", func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 404}) }, "config", false, "does not find the workspace"},
		{"a record that hub rejects", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 422, Body: `{"error":{"code":"x","message":"the workspace is archived"}}`})
		}, CategoryHub, false, "the workspace is archived"},
		{"a record that hub is unavailable for", func(e *env) { e.hub.Inject("GET", "/api/v1/workspaces/acme", fakehub.Fault{Status: 503}) }, CategoryHub, true, ""},
		{"variables that hub forbids", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Status: 403})
		}, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"variables that hub is unavailable for", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/vars/resolved", fakehub.Fault{Hangup: true})
		}, CategoryHub, true, ""},
		{"an issue that hub forbids", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/issues/42", fakehub.Fault{Status: 403})
		}, "auth", false, "PUT /api/v1/workspaces/acme"},
		{"an issue that hub is unavailable for", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/issues/42", fakehub.Fault{Status: 502})
		}, CategoryHub, true, ""},
		{"an issue that hub rate limits", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/issues/42", fakehub.Fault{Status: 429})
		}, CategoryHub, true, ""},
		{"an issue that hub rejects", func(e *env) {
			e.hub.Inject("GET", "/api/v1/workspaces/acme/issues/42", fakehub.Fault{Status: 422, Body: `{"error":{"code":"x","message":"the forge said no"}}`})
		}, CategoryHub, false, "the forge said no"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T"})
			c.setup(e)

			res, err := e.run(e.request(jobs.KindTriage, issueInput("42")))

			if c.transient {
				failure := transient(err)
				if failure == nil || failure.Category != c.category || !failure.Retryable || len(res.Envelope) != 0 {
					t.Fatalf("Run = %+v, %v; want a transient error of category %s and no envelope", res, err, c.category)
				}
				if strings.Contains(err.Error(), pat) {
					t.Errorf("the error holds the PAT: %v", err)
				}
			} else {
				if err != nil {
					t.Fatalf("Run returned an error: %v", err)
				}
				env := envelope(t, res)
				category, message, retryable, _ := errorOf(env)
				if category != c.category || retryable || !strings.Contains(message, c.message) {
					t.Errorf("category %q, retryable %v, message %q; want %s, not retryable, containing %q", category, retryable, message, c.category, c.message)
				}
				if strings.Contains(string(res.Envelope), pat) {
					t.Errorf("the envelope holds the PAT")
				}
			}
			if len(e.writes()) != 0 {
				t.Errorf("writes to hub: %v", e.writes())
			}
		})
	}

	t.Run("a triage on a carry_patch workspace is not refused", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{Mode: "carry_patch", Vars: map[string]string{"AF_FIX_LAND": "patch"}})
		a := &attempt{req: e.request(jobs.KindTriage, jobs.Input{Text: "t"}), report: e.sink, started: time.Now()}
		if out := e.runner.resolve(context.Background(), a); out != nil {
			t.Errorf("a triage on a carry_patch workspace was refused: %+v", out)
		}
	})
	t.Run("the token is read once, at the start", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.AddIssue(slug, fakehub.Issue{Number: 42, Title: "T"})
		_, _ = e.run(e.request(jobs.KindTriage, issueInput("42")))
		if e.subs.reads != 1 {
			t.Errorf("the token was read %d times, want once", e.subs.reads)
		}
	})
}

// TestTheWorkerReadsAFPollIntervalAsThePollDoesAndNeverFailsOnIt_TS_19_35 verifies TS-19-35.
// Verifies: 19-REQ-6.4
func TestTheWorkerReadsAFPollIntervalAsThePollDoesAndNeverFailsOnIt_TS_19_35(t *testing.T) {
	cases := map[string]bool{"": true, "0": false, "0s": false, " 0m ": false, "30s": true, "5m": true, "1h": true, "banana": true, "-1m": true, "48h": true}
	for value, on := range cases {
		for _, kind := range []jobs.Kind{jobs.KindTriage, jobs.KindFix} {
			vars := map[string]string{"AF_POLL_INTERVAL": value}
			s, bad := resolveSettings(kind, vars)
			if bad != nil {
				t.Errorf("%s with AF_POLL_INTERVAL %q failed: %v", kind, value, bad)
			}
			if s.TriggersOn != on {
				t.Errorf("%s with AF_POLL_INTERVAL %q: the label triggers on %v, want %v", kind, value, s.TriggersOn, on)
			}
		}
	}
	if s, _ := resolveSettings(jobs.KindFix, nil); !s.TriggersOn {
		t.Error("an unset AF_POLL_INTERVAL turns the label triggers off")
	}
}
