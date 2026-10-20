package triage

import (
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// TestReadBackRejectionsReadsTheFileBesideTheSubmission verifies the read-back of
// 08-REQ-4.5: the refused calls of a box are found where the kind notes them.
func TestReadBackRejectionsReadsTheFileBesideTheSubmission(t *testing.T) {
	const id = "20240101_000000_abcdef"
	if got, want := RejectionsPath(id), ".agent-fox/run/"+id+"/rejections.jsonl"; got != want {
		t.Fatalf("RejectionsPath = %q, want %q", got, want)
	}
	tree := fstest.MapFS{RejectionsPath(id): {Data: []byte("{\"a\":1}\n{\"b\":2}\n")}}

	got, err := ReadBackRejections(tree, id)

	if err != nil || string(got) != "{\"a\":1}\n{\"b\":2}\n" {
		t.Errorf("ReadBackRejections = %q, %v", got, err)
	}
}

func TestReadBackRejectionsOfATreeWithNoneIsNoBytesAndNoError(t *testing.T) {
	got, err := ReadBackRejections(fstest.MapFS{}, "20240101_000000_abcdef")

	if err != nil || len(got) != 0 {
		t.Errorf("ReadBackRejections = %q, %v, want nothing", got, err)
	}
}

func TestReadBackRejectionsReportsAFileThatCannotBeRead(t *testing.T) {
	// A directory where the file should be is a file that exists and cannot be read.
	tree := fstest.MapFS{RejectionsPath("20240101_000000_abcdef") + "/x": {Data: []byte("x")}}

	_, err := ReadBackRejections(tree, "20240101_000000_abcdef")

	if err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want a read error that is not 'not exist'", err)
	}
}

// TestStaleReasonHoldsWhileTheHostHasTheFiles verifies the host half of
// 08-REQ-4.5: the submission is checked against the host's tree once more.
func TestStaleReasonHoldsWhileTheHostHasTheFiles(t *testing.T) {
	payload := json.RawMessage(`{"title":"T","severity":"low","confidence":"confirmed","root_cause":"R",` +
		`"affected_files":[{"path":"a.go","line":3,"note":"n"}],"suggested_fix":"F","acceptance_criteria":["C"]}`)
	lines := func(p string) (int, error) { return 5, nil }

	if got := StaleReason(payload, Tree{Files: []string{"a.go"}, Lines: lines}); got != "" {
		t.Errorf("StaleReason of a tree that has the file = %q, want none", got)
	}

	for name, tree := range map[string]Tree{
		"a file the host no longer has": {Files: []string{"b.go"}, Lines: lines},
		"a file that became shorter":    {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 2, nil }},
		"a file that cannot be counted": {Files: []string{"a.go"}, Lines: func(string) (int, error) { return 0, errors.New("gone") }},
	} {
		t.Run(name, func(t *testing.T) {
			got := StaleReason(payload, tree)
			if got == "" || !strings.Contains(got, "/affected_files/0") {
				t.Errorf("StaleReason = %q, want the problem with its pointer", got)
			}
		})
	}
}

func TestStaleReasonOfAPayloadThatIsNoDiagnosisSaysSo(t *testing.T) {
	if got := StaleReason(json.RawMessage(`{"title":1}`), Tree{}); got == "" {
		t.Error("StaleReason of a payload that does not validate is empty")
	}
}

func TestStaleReasonListsEveryProblem(t *testing.T) {
	payload := json.RawMessage(`{"title":"T","severity":"low","confidence":"confirmed","root_cause":"R",` +
		`"affected_files":[{"path":"a.go","note":"n"},{"path":"b.go","note":"n"}],"suggested_fix":"F","acceptance_criteria":["C"]}`)

	got := StaleReason(payload, Tree{Files: []string{"c.go"}})

	if !strings.Contains(got, "/affected_files/0") || !strings.Contains(got, "/affected_files/1") || !strings.Contains(got, "; /affected_files/1") {
		t.Errorf("StaleReason = %q, want both problems, one after the other", got)
	}
}
