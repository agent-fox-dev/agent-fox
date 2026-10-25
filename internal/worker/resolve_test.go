package worker

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

// Either way nothing is written, and nothing holds the token.
func TestResolve_RefusesWhatCannotRunAndIsTransientWhileHubIsUnavailable(t *testing.T) {
	const replace = "PUT /api/v1/workspaces/acme"
	fault := func(method, path string, f fakehub.Fault) func(*env) {
		return func(e *env) { e.hub.Inject(method, path, f) }
	}
	for _, c := range []struct {
		name      string
		setup     func(e *env)
		input     string
		category  string // of the envelope, or of the transient error
		transient bool
		message   string
	}{
		{"a subscription that is gone", func(e *env) { delete(e.subs.tokens, slug) }, "42", "auth", false, "not subscribed any more"},
		{"a token that does not open", func(e *env) { e.subs.tokenErr[slug] = store.ErrUnreadable }, "42", "auth", false, "cannot be read with this key"},
		{"a token that cannot be had", func(e *env) { e.subs.tokenErr[slug] = errors.New("disk") }, "42", "auth", false, "cannot be had"},
		{"a record that hub refuses", fault("GET", recordPath, fakehub.Fault{Status: 401}), "42", "auth", false, replace},
		{"a record that hub forbids", fault("GET", recordPath, fakehub.Fault{Status: 403}), "42", "auth", false, replace},
		{"a record that is not there", fault("GET", recordPath, fakehub.Fault{Status: 404}), "42", "config", false, "does not find the workspace"},
		{"a record that hub rejects", fault("GET", recordPath, fakehub.Fault{Status: 422, Body: `{"error":{"code":"x","message":"the workspace is archived"}}`}), "42", CategoryHub, false, "the workspace is archived"},
		{"a record that hub is unavailable for", fault("GET", recordPath, fakehub.Fault{Status: 503}), "42", CategoryHub, true, "unavailable"},
		{"a forge that agent-fox does not know", fault("GET", recordPath, fakehub.Fault{Body: `{"slug":"acme","branch":"main","hub_url":"http://127.0.0.1:1/git/acme.git","git_url":"ftp://x"}`}), "42", "config", false, "none that agent-fox knows"},
		{"variables that hub forbids", fault("GET", varsPath, fakehub.Fault{Status: 403}), "42", "auth", false, replace},
		{"variables that hub is unavailable for", fault("GET", varsPath, fakehub.Fault{Hangup: true}), "42", CategoryHub, true, ""},
		{"an issue that hub forbids", fault("GET", issuePath, fakehub.Fault{Status: 403}), "42", "auth", false, replace},
		{"an issue that is not there", nil, "99", "usage", false, "does not exist"},
		{"an issue that is a pull request", func(e *env) { e.hub.AddIssue(slug, fakehub.Issue{Number: 43, Title: "PR", IsPR: true}) }, "43", "usage", false, "pull request"},
		{"an issue elsewhere", nil, `"other/repo#3"`, "usage", false, "not on the workspace's repository"},
		{"an issue that hub is unavailable for", fault("GET", issuePath, fakehub.Fault{Status: 502}), "42", CategoryHub, true, "unavailable"},
		{"an issue that hub rate limits", fault("GET", issuePath, fakehub.Fault{Status: 429}), "42", CategoryHub, true, "unavailable"},
		{"a fix on a carry-patch workspace", fault("GET", recordPath, fakehub.Fault{Body: `{"slug":"acme","branch":"main","workspace_mode":"carry_patch","hub_url":"http://127.0.0.1:1/git/acme.git","git_url":"` + forgeRepo + `"}`}),
			"42", "unsupported", false, "carry-patch landing comes in a later release"},
		{"a fix that lands as a patch", fault("GET", varsPath, fakehub.Fault{Body: `[{"key":"AF_FIX_LAND","value":"patch","origin":"workspace"}]`}), "42", "unsupported", false, "carry-patch landing"},
		{"an issue that hub rejects", fault("GET", issuePath, fakehub.Fault{Status: 422, Body: `{"error":{"code":"x","message":"the forge said no"}}`}), "42", CategoryHub, false, "the forge said no"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, fakehub.Workspace{})
			if c.setup != nil {
				c.setup(e)
			}

			res, err := e.run(e.request(jobs.KindFix, issueInput(c.input)))

			if c.transient {
				if f := transient(err); f == nil || f.Category != c.category || !f.Retryable || !strings.Contains(f.Message, c.message) || len(res.Envelope) != 0 {
					t.Fatalf("result %s, error %v, want a transient %s error that says %q", res.Envelope, err, c.category, c.message)
				}
			} else {
				env := envelope(t, res)
				category, message, retryable, _ := errorOf(env)
				if err != nil || env["tool"] != "fix" || category != c.category || retryable || !strings.Contains(message, c.message) {
					t.Fatalf("error %v, envelope %s, want a %s refusal that says %q", err, res.Envelope, c.category, c.message)
				}
				for _, field := range []string{"warnings", "artifacts", "side_effects", "next"} {
					if list, ok := env[field].([]any); !ok || len(list) != 0 {
						t.Errorf("%s = %v, want []", field, env[field])
					}
				}
				if res.MaxAttempts != defaultMaxAttempts {
					t.Errorf("MaxAttempts = %d, want the default", res.MaxAttempts)
				}
			}
			if strings.Contains(string(res.Envelope), pat) || (err != nil && strings.Contains(err.Error(), pat)) {
				t.Error("the outcome holds the token")
			}
			if w := e.writes(); len(w) != 0 {
				t.Errorf("writes to hub: %v", w)
			}
		})
	}
}

func TestResolve_AVariableOutOfRangeIsAConfigFailureNamingIt(t *testing.T) {
	for _, tc := range []struct {
		vars        map[string]string
		bad         string
		maxAttempts int
	}{
		{map[string]string{"AF_TIMEOUT": "never"}, "AF_TIMEOUT", 2},
		{map[string]string{"AF_MAX_ATTEMPTS": "11"}, "AF_MAX_ATTEMPTS", 2},
		{map[string]string{"AF_MAX_ATTEMPTS": "5", "AF_FIX_LAND": "weird"}, "AF_FIX_LAND", 5},
	} {
		e := varsEnv(t, tc.vars)

		res, err := e.run(e.fix())

		env := envelope(t, res)
		category, message, retryable, hint := errorOf(env)
		if err != nil || env["status"] != "failed" || category != "config" || retryable || hint["var"] != tc.bad || !strings.Contains(message, tc.bad) {
			t.Errorf("%v: error %v, envelope %s, want a config failure whose hint is %s", tc.vars, err, res.Envelope, tc.bad)
		}
		if res.MaxAttempts != tc.maxAttempts {
			t.Errorf("%v: MaxAttempts = %d, want %d", tc.vars, res.MaxAttempts, tc.maxAttempts)
		}
		if w := e.writes(); len(w) != 0 {
			t.Errorf("%v: writes to hub: %v", tc.vars, w)
		}
	}
}

func TestOpenSession_OpensItWithTheModelAndARunIDAfterTheReads(t *testing.T) {
	runID := regexp.MustCompile(`^[0-9]{8}_[0-9]{6}_[0-9a-f]{6}$`)
	for _, tc := range []struct {
		vars  map[string]string
		model string
		kind  jobs.Kind
	}{
		{nil, catalog.Default().DefaultModelID("anthropic"), jobs.KindFix},
		{map[string]string{"AF_MODEL": "m1"}, "m1", jobs.KindTriage},
	} {
		e := varsEnv(t, tc.vars)

		a := e.through(t, "session", e.request(tc.kind, issueInput("42")))

		s := e.session()
		if s.Model != tc.model || s.Archetype != string(tc.kind) || s.Slug != slug || !runID.MatchString(s.RunID) {
			t.Errorf("session = %+v, want the model %q, the archetype %s and a run id", s, tc.model, tc.kind)
		}
		if a.runID != s.RunID || a.sessionID != s.ID || e.sink.lastHub() != [3]string{s.ID, s.RunID, ""} {
			t.Errorf("the attempt holds %q %q and the recorder was told %v, want the session %s %s", a.sessionID, a.runID, e.sink.lastHub(), s.ID, s.RunID)
		}
		if read := e.seqOf("GET", issuePath, ""); read == 0 || read > s.Seq || len(e.writes()) != 1 {
			t.Errorf("the issue was read at %d, the session opened at %d, writes %v: want the reads first and the session alone", read, s.Seq, e.writes())
		}
	}
}

func TestOpenSession_ASessionHubDoesNotOpenStopsTheAttemptBeforeAnyWrite(t *testing.T) {
	for _, status := range []int{503, 401, 403} {
		e := newEnv(t, fakehub.Workspace{})
		e.hub.Inject("POST", sessionsPath, fakehub.Fault{Status: status})

		res, err := e.run(e.fix())

		if status == 503 {
			if f := transient(err); f == nil || f.Category != CategoryHub || len(res.Envelope) != 0 {
				t.Errorf("503: result %s, error %v, want a transient hub error", res.Envelope, err)
			}
		} else if category, message, _, _ := errorOf(envelope(t, res)); err != nil || category != "auth" || !strings.Contains(message, "PUT /api/v1/workspaces/acme") {
			t.Errorf("%d: error %v, envelope %s, want an auth failure", status, err, res.Envelope)
		}
		if w := e.writes(); len(w) != 1 || len(e.hub.GitRequests()) != 0 || e.sink.lastHub() != [3]string{} {
			t.Errorf("%d: writes %v, %d git requests, recorder told %v: want the session alone", status, w, len(e.hub.GitRequests()), e.sink.lastHub())
		}
	}

	t.Run("a run id that cannot be drawn", func(t *testing.T) {
		e := newEnv(t, fakehub.Workspace{})
		e.runner.cfg.Rand = failingReader{}

		res, err := e.run(e.fix())

		if err == nil || jobs.IsTransient(err) || len(res.Envelope) != 0 || len(e.writes()) != 0 {
			t.Errorf("result %s, error %v, writes %v, want the error and nothing written", res.Envelope, err, e.writes())
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("no randomness") }
