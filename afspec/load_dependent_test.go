package afspec

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// writeDepSpecDir creates a complete spec directory with prd.md,
// requirements.json (with glossary and external_apis), test_spec.json,
// and tasks.json (with optional dependency declarations).
// deps is a slice of spec IDs that this spec depends on.
func writeDepSpecDir(t *testing.T, dir, specID, specName string, deps []string) {
	t.Helper()
	mustMkdir(t, dir)

	prd := fmt.Sprintf("---\nspec_id: %q\nspec_name: %q\ntitle: %q\nstatus: \"draft\"\n"+
		"created_at: \"2026-01-01T00:00:00Z\"\nupdated_at: \"2026-01-01T00:00:00Z\"\n"+
		"owner: \"test\"\nsource: \"test\"\nsupersedes: []\nintent_hash: null\nschema_version: 1\n---\n# %s\n",
		specID, specName, specName, specName)
	mustWriteFile(t, filepath.Join(dir, "prd.md"), prd)

	// Requirements with glossary entries and an external API symbol.
	req := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/requirements.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,"introduction":"Test",`+
			`"glossary":{"term_%s":"definition for %s"},`+
			`"requirements":[{"id":"%s-REQ-1","title":"R1",`+
			`"user_story":{"role":"developer","goal":"test","benefit":"verify"},`+
			`"acceptance_criteria":[{"id":"%s-REQ-1.1",`+
			`"action":"return bool","system":"test","ears_pattern":"event_driven",`+
			`"return_contract":"returns bool"}],`+
			`"edge_cases":[]}],`+
			`"correctness_properties":[],"execution_paths":[],"error_handling":[],`+
			`"external_apis":[{"package":"pkg_%s","version":"v1",`+
			`"symbols":[{"name":"Func%s","import_path":"pkg_%s","signature":"func()"}]}]}`,
		specID, specName,
		specID, specID,
		specID,
		specID,
		specID, specID, specID)
	mustWriteFile(t, filepath.Join(dir, "requirements.json"), req)

	ts := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/test_spec.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`,
		specID, specName)
	mustWriteFile(t, filepath.Join(dir, "test_spec.json"), ts)

	// Build dependency entries.
	var depEntries []string
	for _, depID := range deps {
		depEntries = append(depEntries, fmt.Sprintf(
			`{"depends_on_spec":%q,"from_group":1,"to_group":1,"relationship":"blocks","sentinel":false}`,
			depID))
	}
	depsJSON := "[" + strings.Join(depEntries, ",") + "]"

	tasks := fmt.Sprintf(
		`{"$schema":"https://agent-fox.dev/schemas/tasks.v1.json",`+
			`"spec_id":%q,"spec_name":%q,"schema_version":1,`+
			`"test_commands":{"spec_tests":"go test","all_tests":"go test","linter":"go vet"},`+
			`"dependencies":%s,`+
			`"task_groups":[{"id":1,"kind":"tests","title":"Tests","subtasks":[],"verification":{"id":"1.V","checks":["ok"]}}],`+
			`"traceability":[]}`,
		specID, specName, depsJSON)
	mustWriteFile(t, filepath.Join(dir, "tasks.json"), tasks)
}

// ---------------------------------------------------------------------------
// TS-05-35: LoadDependentInterfaces with a valid specID and specRoot calls
// DiscoverSpecs, BuildDependencyGraph, and LoadSpec for each upstream
// dependency, returning a slice of interface maps.
// Requirement: 05-REQ-10.1
// ---------------------------------------------------------------------------

func TestLoadDependentInterfaces_WithUpstream(t *testing.T) {
	defer requireImplemented(t)

	specRoot := t.TempDir()
	// 04-upstream_spec: the upstream dependency.
	writeDepSpecDir(t, filepath.Join(specRoot, "04_upstream_spec"), "04", "upstream_spec", nil)
	// 05-my_spec: depends on 04.
	writeDepSpecDir(t, filepath.Join(specRoot, "05_my_spec"), "05", "my_spec", []string{"04"})

	result := LoadDependentInterfaces("05", specRoot)

	if result == nil {
		t.Fatal("LoadDependentInterfaces returned nil, want non-nil slice")
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 upstream interface map, got %d", len(result))
	}

	entry := result[0]

	// Check that glossary entries from upstream were extracted.
	glossary, ok := entry["glossary"]
	if !ok {
		t.Error("expected 'glossary' key in interface map, not found")
	}
	if glossary == nil {
		t.Error("expected non-nil glossary value")
	}
}

// ---------------------------------------------------------------------------
// LoadDependentInterfaces with a specID that has no upstream dependencies
// returns an empty slice.
// Requirement: 05-REQ-10.1
// ---------------------------------------------------------------------------

func TestLoadDependentInterfaces_NoUpstream(t *testing.T) {
	defer requireImplemented(t)

	specRoot := t.TempDir()
	// 05-my_spec: no dependencies declared.
	writeDepSpecDir(t, filepath.Join(specRoot, "05_standalone"), "05", "standalone", nil)

	result := LoadDependentInterfaces("05", specRoot)

	if result == nil {
		t.Fatal("LoadDependentInterfaces returned nil, want empty slice")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 upstream interface maps for spec with no deps, got %d", len(result))
	}
}

// ---------------------------------------------------------------------------
// TS-05-61: LoadDependentInterfaces returns an empty slice without
// propagating the error when DiscoverSpecs or BuildDependencyGraph returns
// an error.
// Requirement: 05-REQ-10.E1
// ---------------------------------------------------------------------------

func TestLoadDependentInterfaces_NonexistentRoot(t *testing.T) {
	defer requireImplemented(t)

	result := LoadDependentInterfaces("05", "/nonexistent/specroot")

	if result == nil {
		t.Fatal("LoadDependentInterfaces returned nil, want empty slice")
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice for nonexistent specRoot, got %d entries", len(result))
	}
}

// ---------------------------------------------------------------------------
// TS-05-62: LoadDependentInterfaces skips a failing upstream dependency and
// returns entries for all successfully loaded upstream specs.
// Requirement: 05-REQ-10.E2
// ---------------------------------------------------------------------------

func TestLoadDependentInterfaces_SkipBadUpstream(t *testing.T) {
	defer requireImplemented(t)

	specRoot := t.TempDir()

	// 03-upstream_ok: valid upstream spec.
	writeDepSpecDir(t, filepath.Join(specRoot, "03_upstream_ok"), "03", "upstream_ok", nil)

	// 04-upstream_bad: has prd.md but malformed requirements.json.
	dir04 := filepath.Join(specRoot, "04_upstream_bad")
	writeMinimalPRD(t, dir04, "04", "upstream_bad", "draft")
	mustWriteFile(t, filepath.Join(dir04, "requirements.json"), "{{not valid json}}")
	mustWriteFile(t, filepath.Join(dir04, "test_spec.json"),
		`{"spec_id":"04","spec_name":"upstream_bad","schema_version":1,`+
			`"test_cases":[],"property_tests":[],"edge_case_tests":[],`+
			`"smoke_tests":[],"coverage":{}}`)
	mustWriteFile(t, filepath.Join(dir04, "tasks.json"),
		`{"spec_id":"04","spec_name":"upstream_bad","schema_version":1,`+
			`"test_commands":{"spec_tests":"t","all_tests":"t","linter":"t"},`+
			`"dependencies":[],"task_groups":[],"traceability":[]}`)

	// 05-my_spec: depends on both 03 and 04.
	writeDepSpecDir(t, filepath.Join(specRoot, "05_my_spec"), "05", "my_spec", []string{"03", "04"})

	result := LoadDependentInterfaces("05", specRoot)

	if result == nil {
		t.Fatal("LoadDependentInterfaces returned nil, want non-nil slice")
	}
	// The bad upstream should be skipped; only the good upstream should be included.
	if len(result) < 1 {
		t.Error("expected at least 1 upstream interface map (from 03_upstream_ok), got 0")
	}
}

// ---------------------------------------------------------------------------
// TS-05-63: LoadDependentInterfaces returns an empty slice without panicking
// when specRoot does not exist or is not accessible.
// Requirement: 05-REQ-10.E3
// ---------------------------------------------------------------------------

func TestLoadDependentInterfaces_InaccessibleRoot(t *testing.T) {
	defer requireImplemented(t)

	result := LoadDependentInterfaces("05", "/inaccessible/path")

	if result == nil {
		t.Fatal("LoadDependentInterfaces returned nil, want empty slice")
	}
	if len(result) != 0 {
		t.Errorf("expected empty slice for inaccessible specRoot, got %d entries", len(result))
	}
}
