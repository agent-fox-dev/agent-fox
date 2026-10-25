package labelcleanup_test

import (
	"errors"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/service/store"
)

func TestClean_RemovesWhatTheJobAccountsForPutsTheRequestLabelBackAndRepeatsAsANoOp(t *testing.T) {
	for name, tc := range map[string]struct {
		start         []string
		req           func(*jobs.CleanupRequest)
		removed, want []string
	}{
		"a blocked job":                      {[]string{"af:needs-human"}, nil, []string{"af:needs-human"}, []string{"af:fix"}},
		"in-progress with no other job":      {[]string{"af:in-progress"}, nil, []string{"af:in-progress"}, []string{"af:fix"}},
		"in-progress while another job runs": {[]string{"af:in-progress"}, func(r *jobs.CleanupRequest) { r.OtherRunning = true }, nil, []string{"af:fix", "af:in-progress"}},
		"labels of an earlier job or a person": {[]string{"af:landed", "af:ready", "af:failed", "bug"}, func(r *jobs.CleanupRequest) { r.LabelsSet = []string{"bug"} }, nil,
			[]string{"af:failed", "af:fix", "af:landed", "af:ready", "bug"}},
		"a label another open job set": {[]string{"af:needs-human", "af:failed"}, func(r *jobs.CleanupRequest) {
			r.LabelsSet, r.OthersSet = []string{"af:needs-human", "af:failed"}, []string{"af:needs-human"}
		}, []string{"af:failed"}, []string{"af:fix", "af:needs-human"}},
		"a request label a person put back": {[]string{"af:fix", "af:needs-human"}, nil, []string{"af:needs-human"}, []string{"af:fix"}},
		"a job of the API":                  {[]string{"af:needs-human"}, func(r *jobs.CleanupRequest) { r.RequestLabel = "" }, []string{"af:needs-human"}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.issue(tc.start...)
			req := fix("af:needs-human")
			if tc.req != nil {
				tc.req(&req)
			}
			res := w.cleaner.Clean(t.Context(), req)
			wantSet := []string{"af:fix"}
			if slices.Contains(tc.start, "af:fix") || req.RequestLabel == "" {
				wantSet = nil
			}
			if res.Outcome != jobs.CleanupDone || !slices.Equal(res.Removed, tc.removed) || !slices.Equal(res.Set, wantSet) || res.RequestLabelOn != (req.RequestLabel != "") {
				t.Errorf("Clean = %+v, want done, removed %v and set %v", res, tc.removed, wantSet)
			}
			if got := w.labels(); !slices.Equal(got, tc.want) {
				t.Errorf("the issue carries %v, want %v", got, tc.want)
			}
			writes := w.writes()
			if again := w.cleaner.Clean(t.Context(), req); again.Outcome != jobs.CleanupDone || again.Set != nil || again.Removed != nil || w.writes() != writes {
				t.Errorf("a repeated try came to %+v and wrote %d times, want done with nothing written", again, w.writes()-writes)
			}
		})
	}
}

func TestClean_APartialTryIsPendingAndTheNextFinishesIt(t *testing.T) {
	w := newWorld(t)
	w.issue("af:needs-human")
	w.hub.Inject("POST", labelsP, fakehub.Fault{Status: 503, Once: true})
	res := w.cleaner.Clean(t.Context(), fix("af:needs-human"))
	if res.Outcome != jobs.CleanupPending || res.Level != slog.LevelWarn || !slices.Equal(res.Removed, []string{"af:needs-human"}) || res.RequestLabelOn {
		t.Errorf("the partial try came to %+v", res)
	}
	res = w.cleaner.Clean(t.Context(), fix("af:needs-human"))
	if res.Outcome != jobs.CleanupDone || res.Removed != nil || !slices.Equal(res.Set, []string{"af:fix"}) {
		t.Errorf("the next try came to %+v", res)
	}
}

func TestClean_ATokenThatCannotBeRead(t *testing.T) {
	for name, tc := range map[string]struct {
		err     error
		outcome jobs.CleanupOutcome
		level   slog.Level
	}{
		"the workspace is no longer subscribed": {store.ErrNotSubscribed, jobs.CleanupLabelNotSet, slog.LevelInfo},
		"another failure":                       {errors.New("database is locked"), jobs.CleanupPending, slog.LevelWarn},
	} {
		w := newWorld(t)
		w.cleaner.Subscriptions = subs{tc.err}
		if res := w.cleaner.Clean(t.Context(), fix()); res.Outcome != tc.outcome || res.Level != tc.level || w.hub.Total() != 0 {
			t.Errorf("%s: Clean = %+v after %d requests, want %s at %s and no request", name, res, w.hub.Total(), tc.outcome, tc.level)
		}
	}
}

func TestClean_HowHubAnswersDecidesTheOutcome(t *testing.T) {
	forge := fault(502, "GitHub refused the app installation", "forge_forbidden")
	for name, tc := range map[string]struct {
		method, path string
		fault        fakehub.Fault
		outcome      jobs.CleanupOutcome
		level        slog.Level
		says         string
	}{
		"the record 401":           {"GET", record, fakehub.Fault{Status: 401}, jobs.CleanupLabelNotSet, slog.LevelError, "read the workspace record: hub refused the token"},
		"the record 403":           {"GET", record, fakehub.Fault{Status: 403}, jobs.CleanupLabelNotSet, slog.LevelError, "PUT /api/v1/workspaces/widgets"},
		"the record 502 forge":     {"GET", record, forge, jobs.CleanupLabelNotSet, slog.LevelError, "the forge refused hub's credential for the repository: GitHub refused"},
		"the record 404":           {"GET", record, fakehub.Fault{Status: 404}, jobs.CleanupLabelNotSet, slog.LevelError, "hub does not find the workspace"},
		"the record 400":           {"GET", record, fault(400, "the number is malformed", "bad_request"), jobs.CleanupLabelNotSet, slog.LevelError, "hub refused it: the number is malformed"},
		"the record 503":           {"GET", record, fakehub.Fault{Status: 503}, jobs.CleanupPending, slog.LevelWarn, "read the workspace record"},
		"the record 429":           {"GET", record, fakehub.Fault{Status: 429}, jobs.CleanupPending, slog.LevelWarn, "read the workspace record"},
		"the issue 401":            {"GET", issue, fakehub.Fault{Status: 401}, jobs.CleanupLabelNotSet, slog.LevelError, "read the issue: hub refused the token"},
		"the issue 403":            {"GET", issue, fakehub.Fault{Status: 403}, jobs.CleanupLabelNotSet, slog.LevelError, "read the issue: hub refused the token"},
		"the issue 502 forge":      {"GET", issue, fault(502, "no credential", "forge_unauthenticated"), jobs.CleanupLabelNotSet, slog.LevelError, "read the issue: the forge refused"},
		"the issue 404":            {"GET", issue, fakehub.Fault{Status: 404}, jobs.CleanupLabelNotSet, slog.LevelInfo, "the issue is gone"},
		"the issue 400":            {"GET", issue, fault(400, "the number is malformed", "bad_request"), jobs.CleanupLabelNotSet, slog.LevelError, "the number is malformed"},
		"the issue hangs up":       {"GET", issue, fakehub.Fault{Hangup: true}, jobs.CleanupPending, slog.LevelWarn, "read the issue"},
		"the removal 404":          {"DELETE", labelsP + "/af:needs-human", fakehub.Fault{Status: 404}, jobs.CleanupDone, 0, ""},
		"the removal 400":          {"DELETE", labelsP + "/af:needs-human", fault(400, "the label is locked", ""), jobs.CleanupLabelNotSet, slog.LevelWarn, "the label stays: remove the label af:needs-human: the label is locked"},
		"the removal 401":          {"DELETE", labelsP + "/af:needs-human", fakehub.Fault{Status: 401}, jobs.CleanupLabelNotSet, slog.LevelError, "remove the label af:needs-human: hub refused the token"},
		"the removal 500":          {"DELETE", labelsP + "/af:needs-human", fakehub.Fault{Status: 500}, jobs.CleanupPending, slog.LevelWarn, "remove the label"},
		"the add 404":              {"POST", labelsP, fault(404, "no such label in the repository", "forge_not_found"), jobs.CleanupLabelNotSet, slog.LevelError, "add the request label af:fix: hub refused it"},
		"the add 400":              {"POST", labelsP, fault(400, "no such label in the repository", ""), jobs.CleanupLabelNotSet, slog.LevelError, "add the request label af:fix: hub refused it: no such label"},
		"the add 409":              {"POST", labelsP, fault(409, "a conflict", ""), jobs.CleanupLabelNotSet, slog.LevelError, "add the request label"},
		"the add 403":              {"POST", labelsP, fakehub.Fault{Status: 403}, jobs.CleanupLabelNotSet, slog.LevelError, "add the request label af:fix: hub refused the token"},
		"the add echoes the token": {"POST", labelsP, fault(422, "the token "+pat+" may not", ""), jobs.CleanupLabelNotSet, slog.LevelError, "add the request label"},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			w.issue("af:needs-human")
			w.hub.Inject(tc.method, tc.path, tc.fault)
			res := w.cleaner.Clean(t.Context(), fix("af:needs-human"))
			if res.Outcome != tc.outcome || res.Level != tc.level || !strings.Contains(res.Message, tc.says) {
				t.Errorf("Clean = %+v, want %s at %s saying %q", res, tc.outcome, tc.level, tc.says)
			}
			if strings.Contains(res.Message, pat) {
				t.Errorf("the message holds the token: %s", res.Message)
			}
		})
	}
}

func TestClean_ARefusedRemovalLeavesTheLabelAndWritesTheRest(t *testing.T) {
	w := newWorld(t)
	w.issue("af:needs-human", "af:failed")
	w.hub.Inject("DELETE", labelsP+"/af:failed", fault(400, "the label is locked", ""))
	res := w.cleaner.Clean(t.Context(), fix("af:needs-human", "af:failed"))
	if res.Outcome != jobs.CleanupLabelNotSet || !slices.Equal(res.Removed, []string{"af:needs-human"}) || !slices.Equal(res.Set, []string{"af:fix"}) {
		t.Errorf("Clean = %+v", res)
	}
	if got := w.labels(); !slices.Equal(got, []string{"af:failed", "af:fix"}) {
		t.Errorf("the issue carries %v, want af:failed kept, needs-human removed and af:fix added", got)
	}
}

func TestClean_AForgeURLOfNoRepositoryEndsTheCleanup(t *testing.T) {
	w := newWorld(t)
	w.issue("af:needs-human")
	w.hub.UpdateWorkspace("widgets", func(ws *fakehub.Workspace) { ws.ForgeURL = "not a repository" })
	if res := w.cleaner.Clean(t.Context(), fix("af:needs-human")); res.Outcome != jobs.CleanupLabelNotSet || !strings.Contains(res.Message, "names no repository") {
		t.Errorf("Clean = %+v", res)
	}
}
