package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// setupV1Spec copies the checked-in version 1 fixture into a spec root as
// {NN}_{snake_case_name} and returns the spec root.
func setupV1Spec(t *testing.T, specID, specName string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), ".specs")
	dst := filepath.Join(root, specID+"_"+specName)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir("../../testdata/v1_spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("../../testdata/v1_spec", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runMigrate(t *testing.T, specRoot string, args ...string) (map[string]any, error) {
	t.Helper()
	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(append([]string{"--spec-dir", specRoot, "migrate"}, args...))
	err := cmd.Execute()

	var parsed map[string]any
	if stdout.Len() > 0 {
		if jsonErr := json.Unmarshal(stdout.Bytes(), &parsed); jsonErr != nil && err == nil {
			t.Fatalf("stdout is not valid JSON: %v\noutput: %s", jsonErr, stdout.String())
		}
	}
	return parsed, err
}

func TestMigrateConvertsASpecInPlace(t *testing.T) {
	specRoot := setupV1Spec(t, "01", "test_feature")

	parsed, err := runMigrate(t, specRoot, "01_test_feature")
	if err != nil {
		t.Fatalf("spec migrate = %v", err)
	}
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("ok = %v; want true", parsed["ok"])
	}
	if valid, _ := parsed["valid"].(bool); !valid {
		t.Errorf("the migrated spec does not validate: %v", parsed["errors"])
	}

	// The tests v1 left unowned are named in the result, because the operator
	// has to check which task each really belongs to.
	attached, _ := parsed["attached_tests"].([]any)
	if len(attached) == 0 {
		t.Error("attached_tests is empty; the v1 fixture orphans its edge-case and property tests")
	}

	// The spec on disk is now version 2 and loads.
	specPath := filepath.Join(specRoot, "01_test_feature")
	spec, loadErr := afspec.LoadSpec(specPath)
	if loadErr != nil {
		t.Fatalf("LoadSpec on the migrated spec = %v", loadErr)
	}
	if spec.SchemaVersion != afspec.SchemaVersion {
		t.Errorf("schema_version = %d; want %d", spec.SchemaVersion, afspec.SchemaVersion)
	}
	if result := spec.Validate(); !result.Valid {
		t.Errorf("the spec on disk does not validate: %v", result.Errors)
	}

	// No v1 construct survives on disk.
	for _, name := range []string{"requirements.json", "test_spec.json", "tasks.json"} {
		data, err := os.ReadFile(filepath.Join(specPath, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			`"acceptance_criteria"`, `"edge_cases"`, `"correctness_properties"`,
			`"error_handling"`, `"task_groups"`, `"traceability"`, `"coverage"`,
			`"ears_pattern"`, `"return_contract"`,
		} {
			if strings.Contains(string(data), forbidden) {
				t.Errorf("%s still contains the version 1 field %s", name, forbidden)
			}
		}
	}
}

func TestMigrateDryRunWritesNothing(t *testing.T) {
	specRoot := setupV1Spec(t, "01", "test_feature")
	specPath := filepath.Join(specRoot, "01_test_feature")

	before, err := os.ReadFile(filepath.Join(specPath, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := runMigrate(t, specRoot, "--dry-run", "01_test_feature")
	if err != nil {
		t.Fatalf("spec migrate --dry-run = %v", err)
	}
	if dry, _ := parsed["dry_run"].(bool); !dry {
		t.Errorf("dry_run = %v; want true", parsed["dry_run"])
	}
	if notes, _ := parsed["notes"].([]any); len(notes) == 0 {
		t.Error("a dry run reported no notes")
	}

	after, err := os.ReadFile(filepath.Join(specPath, "tasks.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("--dry-run rewrote tasks.json")
	}
}

func TestMigrateRefusesASpecThatIsAlreadyV2(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})

	_, err := runMigrate(t, specRoot, "08_my_spec")
	if err == nil {
		t.Fatal("spec migrate accepted a spec that is already version 2")
	}
	if !strings.Contains(err.Error(), "already declares schema_version") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMigrateRejectsAMissingSpec(t *testing.T) {
	specRoot := setupV1Spec(t, "01", "test_feature")
	if _, err := runMigrate(t, specRoot, "99_nope"); err == nil {
		t.Fatal("spec migrate exited 0 for a spec that does not exist")
	}
}

func TestMigrateIsIdempotentAcrossTwoRuns(t *testing.T) {
	specRoot := setupV1Spec(t, "01", "test_feature")

	if _, err := runMigrate(t, specRoot, "01_test_feature"); err != nil {
		t.Fatalf("first migration = %v", err)
	}
	if _, err := runMigrate(t, specRoot, "01_test_feature"); err == nil {
		t.Fatal("a second migration ran over the already-converted spec")
	}
}
