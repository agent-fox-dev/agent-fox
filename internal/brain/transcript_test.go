package brain

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestTranscript_HasALineForEachCallInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr, err := NewTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("☃", 3000) // 9000 bytes

	seqs := []int{
		tr.Add("write_file", `{"path":"<a>&b"}`, DecisionAllow, "", true, "written"),
		tr.Add("read_file", big, DecisionDeny, "no", false, "bad \xff byte"),
		tr.Skipped(4, "tool "+big),
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}

	if seqs[0] != 1 || seqs[1] != 2 || seqs[2] != 3 {
		t.Errorf("seqs = %v, want 1, 2, 3", seqs)
	}
	lines := testutil.JSONLines(t, path)
	if len(lines) != 3 {
		t.Fatalf("%d lines, want 3", len(lines))
	}
	first := lines[0]
	if first["seq"] != 1.0 || first["tool"] != "write_file" || first["args"] != `{"path":"<a>&b"}` || first["decision"] != DecisionAllow || first["ok"] != true || first["result"] != "written" {
		t.Errorf("line 1 = %v, want the call as it was made", first)
	}
	if !strings.Contains(testutil.ReadFile(t, path), `"<a>&b`) {
		t.Error("the transcript escapes HTML, which makes the arguments harder to read")
	}
	if lines[1]["result"] != "bad � byte" {
		t.Errorf("result = %q, want the byte that is not UTF-8 replaced", lines[1]["result"])
	}
	if lines[2]["skipped"] != true || lines[2]["step"] != 4.0 {
		t.Errorf("line 3 = %v, want step 4 skipped", lines[2])
	}
	for _, s := range []string{lines[1]["args"].(string), lines[2]["what"].(string)} {
		if len(s) > maxTranscriptField || len(s) < maxTranscriptField-4 || !strings.HasSuffix(s, "…") || !utf8.ValidString(s) {
			t.Errorf("a field of %d bytes is cut to %d bytes (%.20q), want 4096 at most, valid, with the mark", len(big), len(s), s)
		}
	}
}

func TestTruncate_CutsAtTheEndOfACharacterAndMarksTheCut(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("a", 5000), strings.Repeat("é", 5000), strings.Repeat("🙂", 5000), strings.Repeat("a", 4096)} {
		got := truncate(s, 4096)
		if len(s) <= 4096 && got != s {
			t.Errorf("truncate of %d bytes = %d bytes, want it whole", len(s), len(got))
		}
		if len(s) > 4096 && (len(got) > 4096 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…")) {
			t.Errorf("truncate of %d bytes = %d bytes, valid %v, want at most 4096, valid, with the mark", len(s), len(got), utf8.ValidString(got))
		}
	}
}

func TestTranscript_ThatCannotBeWrittenIsAWarning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	tr, err := NewTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = tr.f.Close() // as a file that was lost under the brain
	tr.Add("read_file", "{}", DecisionAllow, "", true, "")
	tr.Add("read_file", "{}", DecisionAllow, "", true, "")
	rec := &recorder{}

	closeTranscript(tr, Brief{TranscriptPath: path, Warn: rec.Warn})
	closeTranscript(nil, Brief{Warn: rec.Warn})

	if got := rec.warned("transcript_unwritable"); len(got) != 1 || !strings.Contains(got[0], path) {
		t.Errorf("warnings = %+v, want one transcript_unwritable that names the file", rec.warnings)
	}
	if tr, err := openTranscript(Brief{}); tr != nil || err != nil {
		t.Errorf("a brief with no path: %v, %v, want no transcript", tr, err)
	}
	if _, err := openTranscript(Brief{TranscriptPath: filepath.Join(path, "below", "t.jsonl")}); err == nil || !strings.Contains(err.Error(), "cannot be written") {
		t.Errorf("a path that cannot be made: %v, want an error that says so", err)
	}
}
