package triage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteRunFilesFillsInTheHashesAndLeavesNoTemporaryFile guards 08-REQ-1.3:
// the facts get the format version, the hash of the original and the length and
// hash of the prefix, and the three files are there whole.
func TestWriteRunFilesFillsInTheHashesAndLeavesNoTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	b := Brief{Prefix: "prefix\n", Task: "task\n", Document: "prefix\ntask\n", Original: "the report\n"}

	facts, err := WriteRunFiles(dir, b, Facts{Input: InputFact{Kind: KindText, Fingerprint: "f"}})

	if err != nil {
		t.Fatal(err)
	}
	if facts.FormatVersion != 1 || facts.Prefix.Bytes != len("prefix\n") || facts.Prefix.SHA256 != sha("prefix\n") || facts.OriginalReportSHA256 != sha("the report\n") {
		t.Errorf("facts = %+v", facts)
	}
	for name, want := range map[string]string{"brief.md": "prefix\ntask\n", "original.md": "the report\n"} {
		if got, _ := os.ReadFile(filepath.Join(dir, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	var onDisk Facts
	data, _ := os.ReadFile(filepath.Join(dir, "triage.json"))
	if err := json.Unmarshal(data, &onDisk); err != nil || onDisk != facts {
		t.Errorf("triage.json = %+v (%v), want the returned facts %+v", onDisk, err, facts)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp") {
			t.Errorf("a temporary file is left: %s", e.Name())
		}
	}
	if _, err := WriteRunFiles(filepath.Join(dir, "missing"), b, Facts{}); err == nil {
		t.Error("writing into a directory that is not there succeeded")
	}
}

// TestFingerprintIsTheIssueRefOrTheHashOfTheBody guards 08-REQ-3.6.
func TestFingerprintIsTheIssueRefOrTheHashOfTheBody(t *testing.T) {
	if got := Fingerprint(Report{Kind: KindIssue, Ref: issueRef()}); got != "o/r#7" {
		t.Errorf("an issue: %q, want o/r#7", got)
	}
	for _, kind := range []string{KindText, KindFile, KindStdin} {
		if got := Fingerprint(Report{Kind: kind, Body: "the app crashes"}); got != sha("the app crashes") {
			t.Errorf("%s: %q, want the hash of the body", kind, got)
		}
	}
}

// TestFindPreviousQuestionTakesTheNewestRunOfTheInputThatAsked guards 08-REQ-3.6.
func TestFindPreviousQuestionTakesTheNewestRunOfTheInputThatAsked(t *testing.T) {
	dir := t.TempDir()
	run := func(id, fingerprint, ledger string) {
		t.Helper()
		path := filepath.Join(dir, ".agent-fox", "run", id)
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
		facts, _ := json.Marshal(Facts{Input: InputFact{Fingerprint: fingerprint}})
		for name, content := range map[string]string{"triage.json": string(facts), "ledger.jsonl": ledger} {
			if err := os.WriteFile(filepath.Join(path, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	question := func(text string) string {
		return `{"seq":1,"ts":"t","kind":"triage.question","data":{"text":"` + text + `","options":["x","y"]}}` + "\n"
	}
	run("20261019_100000_aaaaaa", "f", question("older"))
	run("20261019_110000_bbbbbb", "f", question("first of the newest")+question("last of the newest"))
	run("20261019_120000_cccccc", "other", question("another input's"))
	run("20261019_130000_dddddd", "f", "{\"seq\":1,\"kind\":\"triage.input\",\"data\":{}}\n")
	run("20261019_140000_eeeeee", "f", question("the run being made"))
	run("20261019_150000_ffffff", "f", "not json\n")
	if err := os.MkdirAll(filepath.Join(dir, ".agent-fox", "run", "not-a-run"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := FindPreviousQuestion(dir, "f", "20261019_140000_eeeeee")

	if got == nil || got.Text != "last of the newest" || len(got.Options) != 2 {
		t.Errorf("FindPreviousQuestion = %+v, want the last question of the newest run that asked, the run being made skipped", got)
	}
	if got := FindPreviousQuestion(dir, "never triaged", ""); got != nil {
		t.Errorf("an input that was never triaged: %+v", got)
	}
	if got := FindPreviousQuestion(t.TempDir(), "f", ""); got != nil {
		t.Errorf("no run directory: %+v", got)
	}
}
