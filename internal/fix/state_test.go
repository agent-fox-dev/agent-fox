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
)

func sampleState() State {
	return State{
		RunID:  "20261020_100000_aaaaaa",
		Input:  StateInput{Kind: "issue", Origin: "https://github.com/o/r/issues/7", Key: "https://github.com/o/r/issues/7"},
		Issue:  &StateIssue{URL: "https://github.com/o/r/issues/7", Title: "Greeting is wrong"},
		Target: StateTarget{Repo: "o/r", Host: "github.com"}, Base: "main", BaseHead: "0123456789abcdef0123456789abcdef01234567",
		Table:          project.Table{Source: "detected", Checks: []project.Check{{Name: "make check", Argv: []string{"make", "check"}, Kind: "check", Source: "Makefile:check", Available: true}}},
		BaselineGateID: "abc123", BaselinePassed: true, BaselineMS: 1200,
		PrefixSHA256: "feed", Context: []string{"A", "B"}, Rounds: 0,
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
	for _, key := range []string{"run_id", "input", "issue", "target", "base", "base_head", "table", "baseline_gate_id", "no_verify", "prefix_sha256", "context", "rounds"} {
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
	os.WriteFile(filepath.Join(bad, "fix.json"), []byte("{"), 0o644)
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
