package issuetriage

import (
	"reflect"
	"testing"

	"github.com/agent-fox-dev/agentfox/codeimpl"
)

func trustTag(t *testing.T, typ reflect.Type, name string) string {
	t.Helper()
	f, ok := typ.FieldByName(name)
	if !ok {
		t.Fatalf("%s has no field %s", typ, name)
	}
	return f.Tag.Get("trust")
}

// TS-10-6 (unit): issuetriage.FileRef.Path is fact, but codeimpl.Location.Path
// is model, reflecting the citation-check asymmetry.
//
// Verifies: 10-REQ-1.6
func TestTS10_6_FileRefPathFactLocationPathModel(t *testing.T) {
	if got := trustTag(t, reflect.TypeOf(FileRef{}), "Path"); got != "fact" {
		t.Errorf("FileRef.Path trust = %q, want fact", got)
	}
	if got := trustTag(t, reflect.TypeOf(codeimpl.Location{}), "Path"); got != "model" {
		t.Errorf("codeimpl.Location.Path trust = %q, want model", got)
	}
	if got := trustTag(t, reflect.TypeOf(FileRef{}), "Role"); got != "model" {
		t.Errorf("FileRef.Role trust = %q, want model", got)
	}
	fix := reflect.TypeOf(Fix{})
	for _, n := range []string{"Approach", "Risks"} {
		if got := trustTag(t, fix, n); got != "model" {
			t.Errorf("Fix.%s trust = %q, want model", n, got)
		}
	}
	if got := trustTag(t, fix, "Files"); got != "" {
		t.Errorf("Fix.Files trust = %q, want none", got)
	}
}

// TS-10-8 (unit): issuetriage.Result's model-authored fields and its
// forge-established facts are classified as the table requires.
//
// Verifies: 10-REQ-1.8
func TestTS10_8_ResultClassification(t *testing.T) {
	typ := reflect.TypeOf(Result{})
	for _, n := range []string{"Title", "Body", "Severity", "SeverityRationale", "Confidence",
		"Problem", "Reproduction", "RootCause", "RelatedInstances", "AcceptanceCriteria", "RejectedPaths"} {
		if got := trustTag(t, typ, n); got != "model" {
			t.Errorf("Result.%s trust = %q, want model", n, got)
		}
	}
	for _, n := range []string{"Action", "Repo", "URL", "UpstreamURL", "Labels", "Detail"} {
		if got := trustTag(t, typ, n); got != "fact" {
			t.Errorf("Result.%s trust = %q, want fact", n, got)
		}
	}
	for _, n := range []string{"Number", "RejectedPathCalls", "AffectedFiles", "SuggestedFix"} {
		if got := trustTag(t, typ, n); got != "" {
			t.Errorf("Result.%s trust = %q, want none", n, got)
		}
	}
}
