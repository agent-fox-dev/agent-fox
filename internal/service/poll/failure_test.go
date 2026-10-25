package poll

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/agent-fox-dev/aflib/issuex"

	"github.com/agent-fox-dev/agentfox/internal/hub"
	"github.com/agent-fox-dev/agentfox/internal/hub/fakehub"
)

func TestClassify(t *testing.T) {
	httpErr := func(status int) error {
		return fmt.Errorf("op: %w", &issuex.HTTPError{Status: status, Message: "hub said"})
	}
	for name, tc := range map[string]struct {
		err     error
		removal bool
		want    failure
	}{
		"401":                      {fmt.Errorf("x: %w", hub.ErrRefused), false, failure{row: rowDenied, status: 401}},
		"403":                      {fmt.Errorf("x: %w", hub.ErrForbidden), false, failure{row: rowDenied, status: 403}},
		"404 of hub":               {fmt.Errorf("x: %w", hub.ErrNotFound), false, failure{row: rowDenied, status: 404}},
		"401 of the forge":         {httpErr(401), true, failure{row: rowDenied, status: 401}},
		"403 of the forge":         {httpErr(403), false, failure{row: rowDenied, status: 403}},
		"422 of the forge":         {httpErr(422), false, failure{row: rowRejected, status: 422, message: "hub said"}},
		"409 of a removal":         {httpErr(409), true, failure{row: rowRemoval, status: 409, message: "hub said"}},
		"502 of a removal":         {httpErr(502), true, failure{row: rowRejected, status: 502, message: "hub said"}},
		"a rejection of hub":       {&hub.RejectedError{Status: 409, Message: "locked"}, false, failure{row: rowRejected, status: 409, message: "locked"}},
		"a conflict":               {issuex.ErrConflict, true, failure{row: rowRemoval, status: 409, message: "conflict"}},
		"not found of the forge":   {issuex.ErrNotFound, false, failure{row: rowDenied, status: 404}},
		"anything else, a 5xx say": {errors.New("connection reset"), false, failure{row: rowUnavailable}},
	} {
		got := classify("op", tc.err, tc.removal)
		if tc.want.operation, tc.want.err = "op", tc.err; got != tc.want {
			t.Errorf("%s: classify = %+v, want %+v", name, got, tc.want)
		}
	}
}

func TestCycle_AFailureOfHubEndsTheCycleWithOneRecordAcrossCycles(t *testing.T) {
	conflict := `{"error":{"code":409,"message":"conflict text of hub","error_type":"forge_conflict"}}`
	for name, tc := range map[string]struct {
		path, body    string
		status        int
		level         string
		want, notWant string
	}{
		"503 on the list":           {listPath, "", 503, "WARN", "could not be asked to list the issues of af:triage", ""},
		"403 on the list":           {listPath, "", 403, "ERROR", "list the issues of af:triage; replace the token with PUT", ""},
		"401 on the variables":      {varsPath, "", 401, "ERROR", "read the resolved variables; replace the token with PUT", ""},
		"404 on the workspace":      {wsPath, "", 404, "ERROR", "does not find the workspace when the poll asks it to read the workspace record", "PUT"},
		"409 on the list":           {listPath, conflict, 409, "ERROR", "conflict text of hub", ""},
		"409 on the workspace read": {wsPath, conflict, 409, "ERROR", "read the workspace record: conflict text of hub", ""},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.issue(42, "af:fix")
			e.Hub.Inject("GET", tc.path, fakehub.Fault{Status: tc.status, Body: tc.body})
			e.cycle()
			e.cycle()
			got := e.Log.Find(tc.level, "")
			if len(got) != 1 || !strings.Contains(got[0]["msg"].(string), tc.want) || (tc.notWant != "" && strings.Contains(got[0]["msg"].(string), tc.notWant)) {
				t.Errorf("the %s records are %v, want one that says %q", tc.level, got, tc.want)
			}
			if got[0]["workspace"] != slug || strings.Contains(e.Log.String(), pat) {
				t.Errorf("the record %v does not name the workspace, or a record holds the token", got[0])
			}
			if e.Jobs.count("Create") != 0 || len(e.removals()) != 0 {
				t.Error("the cycle went on after the failure")
			}
		})
	}
}

func TestCycle_AFailureIsLoggedAgainOnlyAfterACycleCompletedWithoutOne(t *testing.T) {
	e := newEnv(t)
	logged := func(level string) int { return len(e.Log.Find(level, "")) }
	e.Hub.FailOnce(listPath, 503)
	e.cycle()
	e.Hub.Inject("GET", varsPath, fakehub.Fault{Status: 403})
	e.cycle()
	e.cycle()
	if logged("WARN") != 1 || logged("ERROR") != 1 || len(e.Log.Find("INFO", "recovered")) != 0 {
		t.Fatalf("logs %s, want one WARN, one ERROR and no recovery", e.Log)
	}
	e.Hub.Clear()
	e.issue(42, "af:fix")
	e.Jobs.fail("OpenJob", errors.New("the database is locked"))
	e.cycle()
	if len(e.Log.Find("INFO", "recovered")) != 0 || len(e.Log.Find("WARN", "look up the open job")) != 1 {
		t.Fatalf("logs %s, want the failure of the jobs layer and no recovery", e.Log)
	}
	e.Jobs.fail("OpenJob", nil)
	e.cycle()
	e.cycle()
	if n := len(e.Log.Find("INFO", "recovered")); n != 1 {
		t.Errorf("%d records of recovery after two cycles without a failure, want 1", n)
	}
	e.Hub.Inject("GET", varsPath, fakehub.Fault{Status: 403})
	e.cycle()
	if logged("ERROR") != 2 || logged("WARN") != 2 {
		t.Errorf("%d ERROR records, want the 403 logged again after the recovery", logged("ERROR"))
	}
}
