package codeimpl

import (
	"encoding/json"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-43 (unit): an input_looks_like_path warning does not change what
// impl's summary view keeps for verification.
func TestTS06_43_SummaryViewUnaffectedByPathWarning(t *testing.T) {
	build := func(verdict checks.Verdict, warn bool) map[string]any {
		run := toolio.NewRun("impl", "test")
		if warn {
			run.Warn(toolio.WarnInputLooksLikePath, "high", "%q looks like a path", "widget/report.txt")
		}
		r := &Result{
			Stage:        "landed",
			Gate:         []string{"go test ./..."},
			Verdict:      string(verdict),
			Verification: GateResult{Checks: []checks.Result{{Output: "tail output"}}},
		}
		b, err := json.Marshal(r.SummaryView())
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	for _, v := range []checks.Verdict{checks.VerdictPass, checks.VerdictRegressed} {
		with, without := build(v, true), build(v, false)
		_, hasWith := with["verification"]
		_, hasWithout := without["verification"]
		if hasWith != hasWithout {
			t.Errorf("verdict %s: verification present with warning=%v, without=%v", v, hasWith, hasWithout)
		}
		if hasWith == landable(string(v), false) {
			t.Errorf("verdict %s: verification present=%v, want %v", v, hasWith, !landable(string(v), false))
		}
	}
}
