package afclient

import (
	"reflect"
	"strings"
	"testing"
)

func TestAnswerError_TheStatusGivesTheCategoryAndWhetherToRetry(t *testing.T) {
	for _, c := range []struct {
		status    int
		category  string
		retryable bool
	}{
		{400, CategoryUsage, false}, {404, CategoryUsage, false},
		{401, CategoryAuth, false}, {422, CategoryAuth, false},
		{409, CategoryConflict, false},
		{502, CategoryHub, true},
		{500, CategoryService, false},
		{503, CategoryService, true}, {504, CategoryService, true}, {599, CategoryService, true},
		{405, CategoryService, false}, {413, CategoryService, false}, {418, CategoryService, false},
		{301, CategoryService, false}, {302, CategoryService, false}, {308, CategoryService, false},
	} {
		e := answerError(c.status, nil, "/workspaces")
		if e.Status != c.status || e.Category != c.category || e.Retryable != c.retryable {
			t.Errorf("%d: category %q, retryable %v, want %q and %v", c.status, e.Category, e.Retryable, c.category, c.retryable)
		}
	}
}

func TestAnswerError_TheMessageIsTheServicesWithItsCode(t *testing.T) {
	for body, want := range map[string]string{
		`{"error":{"code":"invalid_request","message":"bad"}}`: "bad (invalid_request)",
		`{"error":{"message":"just text"}}`:                    "just text",
		`{"error":{"code":"internal"}}`:                        "the service answered 500 (internal)",
		``:                                                     "the service answered 500",
		`oops`:                                                 "the service answered 500",
		`{"x":1}`:                                              "the service answered 500",
		`{"error":{}}`:                                         "the service answered 500",
	} {
		if e := answerError(500, []byte(body), "/workspaces"); e.Message != want {
			t.Errorf("%s: message %q, want %q", body, e.Message, want)
		}
	}
}

func TestAnswerError_ANotFoundNamesWhatWasAskedFor(t *testing.T) {
	body := []byte(`{"error":{"code":"not_found","message":"no such thing"}}`)
	for path, want := range map[string]string{
		"/jobs/job_abc/events":    `the job "job_abc" does not exist, or hub does not let you see it: no such thing (not_found)`,
		"/workspaces/a%20b%2Fc":   `the workspace "a b/c" does not exist`,
		"/workspaces/bad%zz/jobs": `the workspace "bad%zz" does not exist`,
		"/workspaces":             `no such thing (not_found)`,
		"/workspaces/":            `no such thing (not_found)`,
		"/nothing/here":           `no such thing (not_found)`,
	} {
		if e := answerError(404, body, path); !strings.HasPrefix(e.Message, want) {
			t.Errorf("404 on %s: %q, want it to start %q", path, e.Message, want)
		}
	}
}

func TestAnswerError_TheHintFollowsTheAnswerAndTheRoute(t *testing.T) {
	subscribe := func(slug string) *Hint {
		return &Hint{Suggest: "af workspace subscribe " + slug + " --hub-token-file FILE"}
	}
	for _, c := range []struct {
		path   string
		status int
		code   string
		want   *Hint
	}{
		{"/workspaces", 401, "unauthenticated", &Hint{Suggest: CredentialSuggestion}},
		{"/workspaces/w", 422, "invalid_hub_token", &Hint{Flag: "--hub-token-file"}},
		{"/workspaces/w/jobs", 404, "not_found", subscribe("w")},
		{"/workspaces/a%20b/fix", 404, "not_found", subscribe("a b")},
		{"/jobs/j", 404, "not_found", nil},
		{"/nothing", 404, "not_found", nil},
		{"/workspaces/w/fix", 409, "not_subscribed", subscribe("w")},
		{"/workspaces/w/triage", 409, "token_unreadable", subscribe("w")},
		{"/jobs/j/cancel", 409, "not_subscribed", subscribe("<slug>")},
		{"/jobs/j/retry", 409, "token_unreadable", subscribe("<slug>")},
		{"/jobs/j/cancel", 409, "invalid_state", nil},
		{"/workspaces/w/fix", 409, "duplicate_job", nil},
		{"/workspaces", 500, "internal", nil},
	} {
		e := answerError(c.status, []byte(`{"error":{"code":"`+c.code+`","message":"m"}}`), c.path)
		if !reflect.DeepEqual(e.Hint, c.want) {
			t.Errorf("%d %s on %s: hint = %+v, want %+v", c.status, c.code, c.path, e.Hint, c.want)
		}
	}
}
