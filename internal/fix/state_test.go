package fix

import (
	"os"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestState_WriteThenLoadGivesItBack(t *testing.T) {
	dir := t.TempDir()
	s := sampleState()

	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(dir)

	if err != nil || !reflect.DeepEqual(got, s) {
		t.Errorf("LoadState = %+v, %v, want %+v", got, err, s)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || entries[0].Name() != StateFile {
		t.Errorf("the run directory holds %v, want fix.json alone", entries)
	}
	if _, err := LoadState(t.TempDir()); err == nil {
		t.Error("LoadState of a directory with no fix.json succeeded")
	}
	bad := t.TempDir()
	testutil.WriteFile(t, bad, StateFile, "{")
	if _, err := LoadState(bad); err == nil {
		t.Error("LoadState of a file that does not parse succeeded")
	}
}

func TestState_AReaderNeverSeesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	s := sampleState()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	var partial atomic.Int32
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := LoadState(dir); err != nil {
				partial.Add(1)
			}
		}
	}()
	for i := range 200 {
		s.Rounds, s.Context = i, make([]string, i%7)
		if err := s.Write(dir); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	<-done
	if n := partial.Load(); n != 0 {
		t.Errorf("%d reads saw a document that did not parse", n)
	}
}

func TestInputKey_IsTheURLThePathOrTheHashOfTheText(t *testing.T) {
	for _, tc := range []struct{ kind, origin, body, want string }{
		{"issue", "https://github.com/o/r/issues/7", "thread text", "https://github.com/o/r/issues/7"},
		{"file", "/work/bug.md", "the contents", "/work/bug.md"},
		{"text", "", "the greeting says helo", testutil.SHA256("the greeting says helo")},
		{"stdin", "", "the greeting says helo", testutil.SHA256("the greeting says helo")},
	} {
		if got := InputKey(tc.kind, tc.origin, tc.body); got != tc.want {
			t.Errorf("InputKey(%s) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestAddUsage_AddsTheCostsAndKeepsTheBestSource(t *testing.T) {
	var s State
	for _, step := range []struct {
		u      toolio.Usage
		cost   float64
		source string
	}{
		{toolio.Usage{Brain: "scripted", CostSource: toolio.CostNone}, 0, toolio.CostNone},
		{toolio.Usage{Brain: "agentkit", CostUSD: 0.5, CostSource: toolio.CostReported}, 0.5, toolio.CostReported},
		{toolio.Usage{Brain: "agentkit", CostUSD: 0.25, CostSource: toolio.CostMeasured}, 0.75, toolio.CostMeasured},
		{toolio.Usage{Brain: "agentkit", CostUSD: 1, CostSource: toolio.CostNone}, 1.75, toolio.CostMeasured},
		{toolio.Usage{Brain: "agentkit", CostSource: toolio.CostReported}, 1.75, toolio.CostMeasured},
	} {
		s.AddUsage(step.u)
		if s.CostUSD != step.cost || s.CostSource != step.source || s.Brain != step.u.Brain {
			t.Fatalf("after %+v: brain %q, cost %v, source %q, want %v and %q", step.u, s.Brain, s.CostUSD, s.CostSource, step.cost, step.source)
		}
	}
}

func TestGateMS_AddsTheDurationsOfTheChecks(t *testing.T) {
	res := &checks.Result{Checks: []checks.CheckResult{{DurationMS: 1200}, {DurationMS: 34}}}
	if got := GateMS(res); got != 1234 {
		t.Errorf("GateMS = %d, want 1234", got)
	}
	if got := GateMS(nil); got != 0 {
		t.Errorf("GateMS(nil) = %d, want 0", got)
	}
}
