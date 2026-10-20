package fix

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/project"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func sampleState() State {
	return State{
		RunID:  "20261020_100000_aaaaaa",
		Input:  StateInput{Kind: "issue", Origin: "https://github.com/o/r/issues/7", Key: "https://github.com/o/r/issues/7"},
		Issue:  &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong"},
		Target: StateTarget{Repo: "o/r", Host: "github.com"}, Base: "main", BaseHead: "0123456789abcdef0123456789abcdef01234567",
		Table:          project.Table{Source: "detected", Checks: []project.Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: "check", Source: "Makefile:check", Available: true}}},
		BaselineGateID: "abc123", BaselinePassed: true, BaselineMS: 1200,
		PrefixSHA256: "feed", PrefixBytes: 1234, Suite: [][]string{{"make", "check"}}, Programs: []string{"make", "sh"}, Context: []string{"A", "B"}, Rounds: 0,
	}
}

// TestFixJSONHoldsEveryFieldAndIsWrittenAtomically_TS_09_17 verifies TS-09-17 at the
// function: every field the later steps need, written as a whole or not at all,
// with no temporary file left, and read back as it was.
// Verifies: 09-REQ-2.8
func TestFixJSONHoldsEveryFieldAndIsWrittenAtomically_TS_09_17(t *testing.T) {
	dir := t.TempDir()
	s := sampleState()

	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}

	var raw map[string]json.RawMessage
	data, err := os.ReadFile(filepath.Join(dir, "fix.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"run_id", "input", "issue", "target", "base", "base_head", "table", "baseline_gate_id", "no_verify", "prefix_sha256", "prefix_bytes", "suite", "programs", "context", "rounds"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("fix.json has no %s: %s", key, data)
		}
	}
	var input map[string]string
	if err := json.Unmarshal(raw["input"], &input); err != nil || input["kind"] != "issue" || input["origin"] == "" || input["key"] == "" {
		t.Errorf("input = %v (%v), want kind, origin and key", input, err)
	}
	got, err := LoadState(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := json.Marshal(got); string(again) != string(mustJSON(t, s)) {
		t.Errorf("LoadState gave %s, want %s", again, mustJSON(t, s))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the run directory holds %d entries, want fix.json alone: %v", len(entries), entries)
	}
	if _, err := LoadState(t.TempDir()); err == nil {
		t.Error("LoadState of a directory with no fix.json succeeded")
	}
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, "fix.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(bad); err == nil {
		t.Error("LoadState of a file that does not parse succeeded")
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestAReaderNeverSeesAPartialFixJSON guards the same atomicity under a reader: while
// the file is written over and over, every read finds a whole document.
func TestAReaderNeverSeesAPartialFixJSON(t *testing.T) {
	dir := t.TempDir()
	s := sampleState()
	if err := s.Write(dir); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var partial int
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := LoadState(dir); err != nil {
				mu.Lock()
				partial++
				mu.Unlock()
			}
		}
	}()
	for i := range 300 {
		s.Rounds = i
		s.Context = append(s.Context[:0], make([]string, i%7)...)
		if err := s.Write(dir); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if partial != 0 {
		t.Errorf("%d reads saw a document that did not parse", partial)
	}
}

// TestTheInputKeyIdentifiesTheInputAcrossRuns guards 09-REQ-2.8: the URL of an
// issue, the absolute path of a file, the SHA-256 of a text.
func TestTheInputKeyIdentifiesTheInputAcrossRuns(t *testing.T) {
	sum := sha256.Sum256([]byte("the greeting says helo"))
	for _, tc := range []struct{ kind, origin, body, want string }{
		{"issue", "https://github.com/o/r/issues/7", "thread text", "https://github.com/o/r/issues/7"},
		{"file", "/work/bug.md", "the contents", "/work/bug.md"},
		{"text", "", "the greeting says helo", hex.EncodeToString(sum[:])},
		{"stdin", "", "the greeting says helo", hex.EncodeToString(sum[:])},
	} {
		if got := InputKey(tc.kind, tc.origin, tc.body); got != tc.want {
			t.Errorf("InputKey(%s) = %q, want %q", tc.kind, got, tc.want)
		}
	}
	if InputKey("text", "", "a") == InputKey("text", "", "b") {
		t.Error("two texts have the same key")
	}
}

// TestSplitBriefSplitsWhereTheBriefWasRenderedAndChecksThePrefix guards what a round reads
// of a brief: the prefix is the bytes fix.json says it is, with the hash it holds, and a
// brief that was changed is refused.
func TestSplitBriefSplitsWhereTheBriefWasRenderedAndChecksThePrefix(t *testing.T) {
	const prefix, task = "# Fix mandate\n\n## Report\n\n## Task\n(in the report)\n\n", "## Task\n\nThis is run x.\n"
	sum, emptySum := sha256.Sum256([]byte(prefix)), sha256.Sum256(nil)
	s := State{PrefixBytes: len(prefix), PrefixSHA256: hex.EncodeToString(sum[:])}

	gotPrefix, gotTask, err := SplitBrief(prefix+task, s)

	if err != nil || gotPrefix != prefix || gotTask != task {
		t.Errorf("SplitBrief = %q, %q, %v, want the prefix and the task block where fix.json says", gotPrefix, gotTask, err)
	}
	for name, tc := range map[string]struct {
		document string
		state    State
	}{
		"a prefix that was edited":       {"# Fix mandate!\n\n## Report\n\n## Task\n(in the report)\n\n" + task, s},
		"a brief that is shorter":        {prefix[:10], s},
		"no length":                      {prefix + task, State{PrefixSHA256: s.PrefixSHA256}},
		"no length, the hash of nothing": {prefix + task, State{PrefixSHA256: hex.EncodeToString(emptySum[:])}},
		"a hash of another":              {prefix + task, State{PrefixBytes: len(prefix), PrefixSHA256: "feed"}},
	} {
		if _, _, err := SplitBrief(tc.document, tc.state); err == nil {
			t.Errorf("%s: SplitBrief took it", name)
		}
	}
	if _, gotTask, err := SplitBrief(prefix, s); err != nil || gotTask != "" {
		t.Errorf("a brief that is only its prefix: %q, %v", gotTask, err)
	}
}

// TestUsageAddsUpAndKeepsTheBestSourceOfTheCost guards what the rounds of a run have used:
// the costs are added, the last brain is the brain, and the source is the best any round had.
func TestUsageAddsUpAndKeepsTheBestSourceOfTheCost(t *testing.T) {
	var s State
	for _, step := range []struct {
		u      toolio.Usage
		cost   float64
		source string
	}{
		{toolio.Usage{Brain: "scripted", CostUSD: 0, CostSource: toolio.CostNone}, 0, toolio.CostNone},
		{toolio.Usage{Brain: "agentkit", CostUSD: 0.5, CostSource: toolio.CostReported}, 0.5, toolio.CostReported},
		{toolio.Usage{Brain: "agentkit", CostUSD: 0.25, CostSource: toolio.CostMeasured}, 0.75, toolio.CostMeasured},
		{toolio.Usage{Brain: "agentkit", CostUSD: 1, CostSource: toolio.CostNone}, 1.75, toolio.CostMeasured},
		{toolio.Usage{Brain: "agentkit", CostUSD: 0, CostSource: toolio.CostReported}, 1.75, toolio.CostMeasured},
	} {
		s.AddUsage(step.u)
		if s.CostUSD != step.cost || s.CostSource != step.source || s.Brain != step.u.Brain {
			t.Fatalf("after %+v: brain %q, cost %v, source %q, want %v and %q", step.u, s.Brain, s.CostUSD, s.CostSource, step.cost, step.source)
		}
	}
}
