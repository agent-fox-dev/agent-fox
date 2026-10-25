package api

import (
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/labels"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// tableLabels are the labels of the label table, as hub keeps them.
func tableLabels() []fakehub.Label {
	var out []fakehub.Label
	for _, l := range labels.Table() {
		out = append(out, fakehub.Label{Name: labels.Name(l.Suffix), Color: l.Color, Description: l.Description})
	}
	return out
}

func TestSubscribe_ANewWorkspaceIs201WithTheView(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	e.Hub.AddUser("pat-9", "usr_9", []string{"widgets"})

	r := e.put(t, "widgets", "caller", "pat-9")
	if r.Status != 201 || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("PUT = %d %s, want 201 application/json", r.Status, r.Body)
	}
	view, now := r.Object(t), e.Clock.Now().Format(time.RFC3339)
	if view["slug"] != "widgets" || view["token_user"] != "usr_9" || view["subscribed_by"] != "usr_1" || view["token_state"] != "ok" ||
		view["created_at"] != now || view["updated_at"] != now {
		t.Errorf("view = %s, want widgets, usr_9, usr_1, ok and both times %s", r.Body, now)
	}
	if strings.Contains(r.Body, "pat-9") {
		t.Errorf("the answer holds the token: %s", r.Body)
	}
	if token, err := e.Store.Token(bg, "widgets"); err != nil || token != "pat-9" {
		t.Errorf("the stored token = %q, %v; want pat-9", token, err)
	}
}

func TestSubscribe_AgainIs200AndReplacesTheSubscription(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("c1", "usr_1", []string{"widgets"})
	e.Hub.AddUser("c2", "usr_2", []string{"widgets"})
	e.Hub.AddUser("pat-1", "usr_9", []string{"widgets"})
	e.Hub.AddWorkspaceToken("pat-2", []string{"widgets"}) // /user refuses it
	first := e.put(t, "widgets", "c1", "pat-1").Object(t)
	e.Clock.Advance(2 * time.Hour)

	r := e.put(t, "widgets", "c2", "pat-2")
	view := r.Object(t)
	if r.Status != 200 || view["token_user"] != nil || view["subscribed_by"] != "usr_2" || view["created_at"] != first["created_at"] ||
		view["updated_at"] != e.Clock.Now().Format(time.RFC3339) {
		t.Errorf("the replacement = %d %s, want 200, no token user, usr_2, the first created_at and a new updated_at", r.Status, r.Body)
	}
	if token, err := e.Store.Token(bg, "widgets"); err != nil || token != "pat-2" || e.rows(t) != 1 {
		t.Errorf("the stored token = %q, %v in %d rows; want pat-2 in one", token, err, e.rows(t))
	}
}

func TestSubscribe_ABadBodyIsRefusedBeforeHubIsAsked(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	e.Hub.AddUser("pat", "usr_9", []string{"widgets"})
	for name, tc := range map[string]struct {
		body        any
		contentType string
		status      int
		code        Code
	}{
		"no hub_token":               {`{}`, "application/json", 400, CodeInvalidRequest},
		"an empty hub_token":         {`{"hub_token":""}`, "application/json", 400, CodeInvalidRequest},
		"a null hub_token":           {`{"hub_token":null}`, "application/json", 400, CodeInvalidRequest},
		"a hub_token with a newline": {map[string]string{"hub_token": "pat\nx"}, "", 400, CodeInvalidRequest},
		"a hub_token with a NUL":     {map[string]string{"hub_token": "pat\x00"}, "", 400, CodeInvalidRequest},
		"a field that is not known":  {`{"hub_token":"pat","label_prefix":"af:"}`, "application/json", 400, CodeInvalidRequest},
		"text/plain":                 {`{"hub_token":"pat"}`, "text/plain", 415, CodeUnsupportedMedia},
		"too large":                  {`{"hub_token":"` + strings.Repeat("x", maxBody) + `"}`, "application/json", 413, CodePayloadTooLarge},
	} {
		var headers [][]string
		if tc.contentType != "" {
			headers = append(headers, []string{"Content-Type", tc.contentType})
		}
		failed(t, name, e.call(t, "PUT", "/api/v1/workspaces/widgets", tc.body, "caller", headers...), tc.status, tc.code)
	}
	if n := e.Hub.CountFor(fakehub.TokenHash("pat")); n != 0 || e.rows(t) != 0 {
		t.Errorf("hub saw the token %d times and the store holds %d rows, want neither", n, e.rows(t))
	}
}

func TestSubscribe_OnlyACallerWhoSeesTheWorkspace(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("blind", "usr_2", nil)
	e.Hub.AddUser("pat", "usr_9", []string{"widgets"})
	failed(t, "no caller", e.put(t, "widgets", "", "pat"), 401, CodeUnauthenticated)
	failed(t, "a caller hub refuses", e.put(t, "widgets", "nobody", "pat"), 401, CodeUnauthenticated)
	failed(t, "an invalid slug", e.put(t, "-x", "blind", "pat"), 400, CodeInvalidRequest)
	hidden, missing := e.put(t, "widgets", "blind", "pat"), e.put(t, "nothing", "blind", "pat")
	failed(t, "a caller who does not see it", hidden, 404, CodeNotFound)
	if hidden.Body != missing.Body {
		t.Errorf("the 404 depends on the workspace: %q and %q", hidden.Body, missing.Body)
	}
	if n := e.Hub.CountFor(fakehub.TokenHash("pat")); n != 0 || e.rows(t) != 0 {
		t.Errorf("hub saw the token %d times and the store holds %d rows, want neither", n, e.rows(t))
	}
}

func TestSubscribe_TheTokenIsPutToHubFreshEveryTime(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	e.Hub.AddWorkspaceToken("pat", []string{"widgets"}) // it sees widgets, and /user refuses it
	for range 2 {
		if r := e.put(t, "widgets", "caller", "pat"); r.Status/100 != 2 || r.Object(t)["token_user"] != nil {
			t.Fatalf("PUT = %d %s, want a success without a token user", r.Status, r.Body)
		}
	}
	var paths []string
	for _, req := range e.Hub.RequestsFor(fakehub.TokenHash("pat")) {
		paths = append(paths, req.Path)
	}
	once := []string{"/api/v1/workspaces/widgets", userPath}
	for range labels.Table() {
		once = append(once, "/api/v1/workspaces/widgets/labels")
	}
	if want := append(slices.Clone(once), once...); !slices.Equal(paths, want) {
		t.Errorf("hub saw with the token %v, want the workspace, the user and the labels, each time: %v", paths, want)
	}
	if n := e.userCalls("caller"); n != 1 {
		t.Errorf("hub was asked about the caller %d times, want once", n)
	}
	if identity, visibility := e.cached("pat", "widgets"); identity || visibility {
		t.Errorf("the caches keep the identity (%v) or the visibility (%v) of the token of a subscription", identity, visibility)
	}
}

func TestSubscribe_TheCallersOwnTokenLeavesTheCallersCachesAlone(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("t", "usr_1", []string{"widgets"})
	e.put(t, "widgets", "t", "t")
	if r := e.put(t, "widgets", "t", "t"); r.Status != 200 || e.workspaceCalls("t", "widgets") != 3 || e.userCalls("t") != 3 {
		t.Errorf("PUT = %d, with %d workspace and %d user calls, want 200 with 3 each: the caller once, the token every time",
			r.Status, e.workspaceCalls("t", "widgets"), e.userCalls("t"))
	}
	// Hub now refuses the token on the workspace call: as a token it is a 422, not the 401 of a
	// caller, and nothing kept about the caller is forgotten.
	e.Hub.FailFor("/api/v1/workspaces/widgets", "t", 401)
	failed(t, "the token refused", e.put(t, "widgets", "t", "t"), 422, CodeInvalidHubToken)
	if identity, visibility := e.cached("t", "widgets"); !identity || !visibility {
		t.Errorf("the refusal of the token forgot the identity (%v) or the visibility (%v) of the caller", identity, visibility)
	}
}

func TestSubscribe_ATokenThatDoesNotFindTheWorkspaceIs422(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	e.Hub.AddUser("p403", "usr_8", []string{"widgets"})
	e.Hub.AddUser("p404", "usr_9", []string{"widgets"})
	e.Hub.AddUser("elsewhere", "usr_7", []string{"gadgets"})
	e.Hub.FailFor("/api/v1/workspaces/widgets", "p403", 403)
	e.Hub.FailFor("/api/v1/workspaces/widgets", "p404", 404)
	for pat, says := range map[string]string{"unknown": "refused", "p403": "widgets", "p404": "widgets", "elsewhere": "widgets"} {
		r := e.put(t, "widgets", "caller", pat)
		failed(t, pat, r, 422, CodeInvalidHubToken)
		if !strings.Contains(message(t, r), says) {
			t.Errorf("%s: the message %q does not say %q", pat, message(t, r), says)
		}
		if identity, visibility := e.cached(pat, "widgets"); identity || visibility {
			t.Errorf("%s: the caches keep an answer about the token of a subscription", pat)
		}
	}
	if n := e.Hub.Count(userPath, fakehub.TokenHash("unknown")); n != 0 || e.rows(t) != 0 {
		t.Errorf("%d user calls after the refusal and %d rows, want neither", n, e.rows(t))
	}
	if identity, visibility := e.cached("caller", "widgets"); !identity || !visibility {
		t.Error("the caller's answers were forgotten")
	}
}

func TestSubscribe_HubUnavailableForTheTokenIs502(t *testing.T) {
	for _, path := range []string{"/api/v1/workspaces/widgets", userPath} {
		t.Run(path, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
			e.Hub.AddUser("pat", "usr_9", []string{"widgets"})
			e.Hub.FailFor(path, "pat", 500)
			failed(t, "PUT", e.put(t, "widgets", "caller", "pat"), 502, CodeHubUnavailable)
			if e.rows(t) != 0 || e.labelCalls("widgets") != 0 {
				t.Errorf("%d rows and %d label creations, want neither", e.rows(t), e.labelCalls("widgets"))
			}
		})
	}
}

func TestSubscribe_TheLabelsAreInPlaceBeforeTheSubscriptionIsStored(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	const pat = "pat-recognisable-0123456789"
	e.Hub.AddUser(pat, "usr_9", []string{"widgets"})
	want := tableLabels()
	changed := fakehub.Label{Name: want[1].Name, Color: "000000", Description: "a person changed it"}
	e.Hub.PutLabel("widgets", want[0])
	e.Hub.PutLabel("widgets", changed)
	e.Hub.PutLabel("widgets", want[2])
	var storedAtLabels atomic.Int64
	e.Hub.OnRequest(func(r fakehub.Request) {
		if strings.HasSuffix(r.Path, "/labels") {
			storedAtLabels.Add(int64(e.rows(t)))
		}
	})

	if r := e.put(t, "widgets", "caller", pat); r.Status != 201 {
		t.Fatalf("PUT = %d %s", r.Status, r.Body)
	}
	e.Hub.OnRequest(nil)
	for i, req := range e.Hub.RequestsTo("POST", "/api/v1/workspaces/widgets/labels") {
		body := testutil.Decode[map[string]string](t, req.Body)
		if l := want[i]; body["name"] != l.Name || body["color"] != l.Color || body["description"] != l.Description || req.TokenHash != fakehub.TokenHash(pat) {
			t.Errorf("creation %d = %v, want %+v made with the token", i+1, body, l)
		}
	}
	if n := storedAtLabels.Load(); n != 0 || e.labelCalls("widgets") != len(want) {
		t.Errorf("the store held %d rows in all at the %d label creations, want none at each of %d", n, e.labelCalls("widgets"), len(want))
	}
	want[1] = changed
	if got := e.Hub.Labels("widgets"); !slices.Equal(got, want) {
		t.Errorf("hub holds %v, want the labels that were there left as they were and the others made: %v", got, want)
	}
	recs := e.Log.Find("INFO", "the labels of the workspace are in place")
	if len(recs) != 1 || recs[0]["workspace"] != "widgets" || recs[0]["created"] != float64(len(want)-3) || recs[0]["existing"] != float64(3) {
		t.Errorf("the records of the labels are %v, want one with %d created and 3 there already", recs, len(want)-3)
	}
	if strings.Contains(e.Log.String(), pat) {
		t.Error("the log holds the token")
	}
}

func TestSubscribe_AnswersALabelThatHubDidNotTake(t *testing.T) {
	const pat = "pat-recognisable-9876543210"
	for name, tc := range map[string]struct {
		fault  fakehub.Fault
		status int
		code   Code
		says   []string
	}{
		"401": {fakehub.Fault{Status: 401}, 422, CodeInvalidHubToken, []string{"hub refused the token"}},
		"403": {fakehub.Fault{Status: 403}, 422, CodeForgeForbidden, []string{"hub forbids the token to create labels", "scope", "forge API"}},
		"502 from the forge": {fakehub.Fault{Status: 502, Body: `{"error":{"code":502,"message":"GitHub said no to ` + pat + `","error_type":"forge_forbidden"}}`},
			422, CodeForgeForbidden, []string{"the forge refused hub's credential for the repository", "GitHub said no"}},
		"502 forge_unauthenticated": {fakehub.Fault{Status: 502, Body: `{"error":{"code":502,"message":"bad credential","error_type":"forge_unauthenticated"}}`},
			422, CodeForgeForbidden, []string{"bad credential"}},
		"404":                    {fakehub.Fault{Status: 404}, 502, CodeHubUnavailable, []string{"hub does not serve label creation for the workspace", "widgets"}},
		"405":                    {fakehub.Fault{Status: 405}, 502, CodeHubUnavailable, []string{"hub does not serve label creation"}},
		"400":                    {fakehub.Fault{Status: 400, Body: `{"error":{"code":400,"message":"invalid label name"}}`}, 422, CodeForgeForbidden, []string{"invalid label name"}},
		"422 validation_failed":  {fakehub.Fault{Status: 422, Body: `{"error":{"code":422,"message":"name is too long","error_type":"validation_failed"}}`}, 422, CodeForgeForbidden, []string{"name is too long"}},
		"429":                    {fakehub.Fault{Status: 429}, 502, CodeHubUnavailable, nil},
		"500":                    {fakehub.Fault{Status: 500}, 502, CodeHubUnavailable, nil},
		"a redirect":             {fakehub.Fault{Redirect: "/elsewhere"}, 502, CodeHubUnavailable, nil},
		"a hang-up":              {fakehub.Fault{Hangup: true}, 502, CodeHubUnavailable, nil},
		"a delay past the bound": {fakehub.Fault{Delay: 2 * time.Second}, 502, CodeHubUnavailable, nil},
		"not hub's body":         {fakehub.Fault{Status: 201, Body: "<html>"}, 502, CodeHubUnavailable, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
			e.Hub.AddUser(pat, "usr_9", []string{"widgets"})
			e.srv.deps.Hub.HTTP.Timeout = 200 * time.Millisecond
			tc.fault.Skip = 2
			e.Hub.Inject("POST", "/api/v1/workspaces/widgets/labels", tc.fault)
			r := e.put(t, "widgets", "caller", pat)
			failed(t, "PUT", r, tc.status, tc.code)
			for _, part := range tc.says {
				if !strings.Contains(message(t, r), part) {
					t.Errorf("the message %q does not say %q", message(t, r), part)
				}
			}
			if strings.Contains(r.Body, pat) || strings.Contains(e.Log.String(), pat) {
				t.Error("the answer or the log holds the token")
			}
			if e.labelCalls("widgets") != 3 || e.rows(t) != 0 {
				t.Errorf("%d label creations and %d rows, want the third the last and nothing stored", e.labelCalls("widgets"), e.rows(t))
			}
		})
	}
}

func TestSubscribe_ALabelHubSaysIsThereIsInPlace(t *testing.T) {
	for name, f := range map[string]fakehub.Fault{
		"409":                        {Status: 409},
		"422 already_exists":         {Status: 422, Body: `{"error":{"code":422,"message":"Validation Failed","error_type":"already_exists"}}`},
		"400 'Label Already Exists'": {Status: 400, Body: `{"error":{"code":400,"message":"Label Already Exists"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
			e.Hub.AddUser("pat", "usr_9", []string{"widgets"})
			f.Skip, f.Once = 2, true
			e.Hub.Inject("POST", "/api/v1/workspaces/widgets/labels", f)
			if r := e.put(t, "widgets", "caller", "pat"); r.Status != 201 || e.labelCalls("widgets") != len(labels.Table()) {
				t.Errorf("PUT = %d %s after %d creations, want 201 after all of them", r.Status, r.Body, e.labelCalls("widgets"))
			}
		})
	}
}

func TestSubscribe_AFailedReplacementLeavesTheSubscriptionAsItWas(t *testing.T) {
	e := newEnv(t)
	e.Hub.AddUser("caller", "usr_1", []string{"widgets"})
	e.Hub.AddUser("old", "usr_8", []string{"widgets"})
	e.Hub.AddUser("new", "usr_9", []string{"widgets"})
	e.put(t, "widgets", "caller", "old")
	row := func() []string {
		return testutil.Column(t, e.Store.DB(), "SELECT hex(sealed_token) || token_user || subscribed_by || created_at || updated_at || revision FROM af_subscriptions")
	}
	before := row()
	e.Clock.Advance(time.Hour)
	e.Hub.Inject("POST", "/api/v1/workspaces/widgets/labels", fakehub.Fault{Status: 403})
	failed(t, "PUT", e.put(t, "widgets", "caller", "new"), 422, CodeForgeForbidden)
	if after := row(); !slices.Equal(after, before) {
		t.Errorf("the stored row changed:\n%v\nwant\n%v", after, before)
	}
}
