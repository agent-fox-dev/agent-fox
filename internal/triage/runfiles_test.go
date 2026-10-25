package triage

import (
	"path/filepath"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// The facts get the format version, the hash of the original and the size and
// hash of the prefix, and the three files are written whole.
func TestWriteRunFiles_WritesTheBriefTheOriginalAndTheFacts(t *testing.T) {
	dir := t.TempDir()
	b := Brief{Prefix: "prefix\n", Task: "task\n", Document: "prefix\ntask\n", Original: "the report\n"}

	facts, err := WriteRunFiles(dir, b, Facts{Input: InputFact{Kind: KindText, Fingerprint: "f"}})

	if err != nil {
		t.Fatal(err)
	}
	want := Facts{
		FormatVersion: FormatVersion, Input: InputFact{Kind: KindText, Fingerprint: "f"},
		OriginalReportSHA256: testutil.SHA256("the report\n"), Prefix: PrefixFact{Bytes: 7, SHA256: testutil.SHA256("prefix\n")},
	}
	if facts != want {
		t.Errorf("facts = %+v, want %+v", facts, want)
	}
	files := testutil.Files(t, dir)
	if len(files) != 3 || files["brief.md"] != b.Document || files["original.md"] != b.Original || testutil.Decode[Facts](t, []byte(files["triage.json"])) != want {
		t.Errorf("the run directory holds %v, want brief.md, original.md and triage.json with the facts", files)
	}
}

func TestWriteRunFiles_FailsWithoutARunDirectory(t *testing.T) {
	if _, err := WriteRunFiles(filepath.Join(t.TempDir(), "missing"), Brief{}, Facts{}); err == nil {
		t.Error("writing into a directory that is not there succeeded")
	}
}

func TestFingerprint_IsTheIssueRefOrTheHashOfTheBody(t *testing.T) {
	if got := Fingerprint(Report{Kind: KindIssue, Ref: issueRef(), Body: "ignored"}); got != "o/r#7" {
		t.Errorf("an issue: %q, want o/r#7", got)
	}
	for _, kind := range []string{KindText, KindFile, KindStdin} {
		if got := Fingerprint(Report{Kind: kind, Body: "the app crashes"}); got != testutil.SHA256("the app crashes") {
			t.Errorf("%s: %q, want the hash of the body", kind, got)
		}
	}
}
