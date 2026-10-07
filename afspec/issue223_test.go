package afspec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Issue #223 (1): a spec name is valid exactly when NN_<name> is a valid
// directory name: no doubled or trailing underscore.
func TestIsSpecNameIsTheDirectoryRule(t *testing.T) {
	for name, want := range map[string]bool{
		"widget_cache": true, "a1": true, "issuex": true, "v2_api": true,
		"widget__cache": false, "issuex_": false, "_x": false, "1x": false, "Widget": false, "": false,
	} {
		if got := IsSpecName(name); got != want {
			t.Errorf("IsSpecName(%q) = %v, want %v", name, got, want)
		}
		if got := IsSpecDirName("07_" + name); got != want {
			t.Errorf("IsSpecDirName(07_%s) = %v, but IsSpecName says %v", name, got, want)
		}
	}
}

// Issue #223 (2): HasIntent accepts a body exactly when ComputeIntentHash
// can hash it, so a PRD that passes the check can be activated.
func TestHasIntentAgreesWithTheIntentHash(t *testing.T) {
	for _, body := range []string{
		"## Intent\n\nWhy it exists.\n",
		"## Intent  \nWhy.\n",
		"## intent\n\nWhy.\n",
		"##  Intent\n\nWhy.\n",
		"## INTENT\nWhy.\n",
		"# Intent\nWhy.\n",
		"No intent here.\n",
	} {
		_, err := ComputeIntentHash(body)
		if got := HasIntent(body); got != (err == nil) {
			t.Errorf("HasIntent(%q) = %v, but ComputeIntentHash says err=%v", body, got, err)
		}
	}
	if HasIntent("## intent\n\nWhy.\n") {
		t.Error("a lower-case heading is accepted, and activation would refuse it")
	}
}

// Issue #223 (3): a key the schema does not allow is refused when the model
// submits it, by its path, rather than silently dropped.
func TestDecodeArtifactRefusesUnknownKeys(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "testdata", "valid_spec", "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var content map[string]any
	if err := json.Unmarshal(raw, &content); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeArtifact(StepTasks, content); err != nil {
		t.Fatalf("the fixture is refused: %v", err)
	}
	content["traceability"] = map[string]any{"x": 1}
	content["tasks"].([]any)[0].(map[string]any)["done-when"] = []any{"x"}
	_, err = DecodeArtifact(StepTasks, content)
	if err == nil {
		t.Fatal("unknown keys were accepted")
	}
	for _, want := range []string{"/traceability", "/tasks/0/done-when"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
}

// Issue #223 (3): a package on disk with a key the schema does not allow
// still loads, and validation reports the key as a schema error.
func TestValidationReportsUnknownKeysOnDisk(t *testing.T) {
	dir := copyFixture(t, filepath.Join("..", "testdata", "valid_spec"))
	clean := loadFixture(t, dir).Validate()
	if slices.Contains(errorChecks(clean), "unknown_field") {
		t.Fatalf("the clean fixture reports an unknown field: %+v", clean.Errors)
	}

	path := filepath.Join(dir, "tasks.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["traceability"] = []any{}
	out, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
	res := loadFixture(t, dir).Validate()
	found := false
	for _, e := range res.Errors {
		if e.Check == "unknown_field" && e.Artifact == "tasks.json" && strings.Contains(e.Message, "/traceability") {
			found = true
		}
	}
	if !found || res.Valid {
		t.Errorf("valid=%v errors=%+v, want an unknown_field error for /traceability", res.Valid, res.Errors)
	}
}

// Issue #223 (smaller): C5 asks for a smoke test on every execution path, so
// a path verified only by another kind of test is not covered.
func TestAPathIsCoveredOnlyByASmokeTest(t *testing.T) {
	spec := loadFixture(t, copyFixture(t, filepath.Join("..", "testdata", "valid_spec")))
	covered := func() bool {
		for _, l := range spec.ComputeTraceability().Links {
			if l.Kind == "path" && l.ID == "01-PATH-1" {
				return l.Covered
			}
		}
		t.Fatal("no link for 01-PATH-1")
		return false
	}
	if !covered() {
		t.Fatal("the smoke-tested path is not covered")
	}
	for i := range spec.TestSpec.Tests {
		if spec.TestSpec.Tests[i].Kind == TestKindSmoke {
			spec.TestSpec.Tests[i].Kind = TestKindUnit
		}
	}
	if covered() {
		t.Error("a path verified only by a unit test is reported covered, beside a C5 error")
	}
}
