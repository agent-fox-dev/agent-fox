package worker

import (
	"strings"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/catalog"

	"github.com/agent-fox-dev/agentfox/internal/afcmd"
	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
)

func TestResolveSettings_TakesDefaultsAndValuesInRange(t *testing.T) {
	model := catalog.Default().DefaultModelID("anthropic")
	triage := Settings{Brain: "agentkit", Model: model, Budget: 2, MaxTurns: 100, Timeout: 15 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true}
	fix := Settings{Brain: "agentkit", Model: model, Budget: 10, MaxTurns: 150, Timeout: 45 * time.Minute, MaxAttempts: 2, Land: "pr", TriggersOn: true}
	with := func(s Settings, change func(*Settings)) Settings { change(&s); return s }
	for _, tc := range []struct {
		name    string
		kind    jobs.Kind
		vars    map[string]string
		want    Settings
		options afcmd.LoopOptions
	}{
		{"the defaults of a triage", jobs.KindTriage, nil, triage, afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 2, MaxTurns: 100}},
		{"the defaults of a fix", jobs.KindFix, nil, fix, afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"empty values are unset", jobs.KindFix, map[string]string{"AF_MODEL": "", "AF_MAX_BUDGET_USD": "", "AF_MAX_TURNS": "", "AF_TIMEOUT": "", "AF_MAX_ATTEMPTS": "", "AF_SANDBOX_PROFILE": "", "AF_FIX_LAND": ""},
			fix, afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"the lower bounds", jobs.KindTriage, map[string]string{"AF_TIMEOUT": "1m", "AF_MAX_ATTEMPTS": "1", "AF_MAX_TURNS": "1", "AF_MAX_BUDGET_USD": "0.01", "AF_MODEL": "m-1", "AF_SANDBOX_PROFILE": "py"},
			with(triage, func(s *Settings) {
				s.Model, s.Budget, s.MaxTurns, s.Timeout, s.MaxAttempts, s.Profile = "m-1", 0.01, 1, time.Minute, 1, "py"
			}), afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 0.01, MaxTurns: 1}},
		{"the upper bounds", jobs.KindFix, map[string]string{"AF_TIMEOUT": "24h", "AF_MAX_ATTEMPTS": "10", "AF_FIX_LAND": "branch"},
			with(fix, func(s *Settings) { s.Timeout, s.MaxAttempts, s.Land = 24*time.Hour, 10, "branch" }),
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "branch"}},
		{"a landing of none", jobs.KindFix, map[string]string{"AF_FIX_LAND": "none"}, with(fix, func(s *Settings) { s.Land = "none" }),
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "none"}},
		{"unknown variables are ignored", jobs.KindFix, map[string]string{"AF_UNKNOWN": "x", "PATH": "/nowhere", "AF_SANDBOX": "none", "AF_BRAIN": "claude", "AF_BRAIN_FIX": "scripted"},
			fix, afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 10, MaxTurns: 150, Land: "pr"}},
		{"a triage does not look at the landing", jobs.KindTriage, map[string]string{"AF_FIX_LAND": "weird"}, triage,
			afcmd.LoopOptions{Brain: "agentkit", Sandbox: "gateway", Budget: 2, MaxTurns: 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, bad := resolveSettings(tc.kind, tc.vars)
			if bad != nil {
				t.Fatalf("refused: %v", bad)
			}
			if got != tc.want {
				t.Errorf("settings = %+v\nwant       %+v", got, tc.want)
			}
			if o := got.options(tc.kind); o != tc.options {
				t.Errorf("options = %+v, want %+v", o, tc.options)
			}
		})
	}
}

func TestResolveSettings_RefusesAValueOutOfItsRange(t *testing.T) {
	for _, bad := range []struct {
		kind        jobs.Kind
		name, value string
	}{
		{jobs.KindFix, "AF_MAX_BUDGET_USD", "0"}, {jobs.KindFix, "AF_MAX_BUDGET_USD", "NaN"}, {jobs.KindFix, "AF_MAX_BUDGET_USD", "-3"},
		{jobs.KindTriage, "AF_MAX_BUDGET_USD", "lots"}, {jobs.KindTriage, "AF_MAX_BUDGET_USD", "Inf"},
		{jobs.KindFix, "AF_MAX_TURNS", "0"}, {jobs.KindFix, "AF_MAX_TURNS", "1.5"}, {jobs.KindTriage, "AF_MAX_TURNS", "many"},
		{jobs.KindFix, "AF_TIMEOUT", "30s"}, {jobs.KindFix, "AF_TIMEOUT", "25h"}, {jobs.KindTriage, "AF_TIMEOUT", "soon"}, {jobs.KindTriage, "AF_TIMEOUT", "59s"},
		{jobs.KindFix, "AF_MAX_ATTEMPTS", "11"}, {jobs.KindFix, "AF_MAX_ATTEMPTS", "0"}, {jobs.KindFix, "AF_MAX_ATTEMPTS", "two"},
		{jobs.KindFix, "AF_FIX_LAND", "weird"}, {jobs.KindFix, "AF_FIX_LAND", "PR"},
	} {
		_, err := resolveSettings(bad.kind, map[string]string{bad.name: bad.value})
		if err == nil || err.Name != bad.name || !strings.Contains(err.Error(), bad.name) || !strings.Contains(err.Error(), `"`+bad.value+`"`) {
			t.Errorf("%s %s=%q: %v, want the fault of the variable, named with its value", bad.kind, bad.name, bad.value, err)
		}
	}
}

// AF_POLL_INTERVAL is read as the label poll reads it, and no value of it fails an attempt.
func TestResolveSettings_AZeroPollIntervalTurnsTheLabelTriggersOff(t *testing.T) {
	for value, on := range map[string]bool{"": true, "0": false, "0s": false, " 0m ": false, "30s": true, "1h": true, "banana": true, "-1m": true} {
		for _, kind := range []jobs.Kind{jobs.KindTriage, jobs.KindFix} {
			s, bad := resolveSettings(kind, map[string]string{"AF_POLL_INTERVAL": value})
			if bad != nil || s.TriggersOn != on {
				t.Errorf("%s with AF_POLL_INTERVAL %q: triggers on %v (%v), want %v", kind, value, s.TriggersOn, bad, on)
			}
		}
	}
}
