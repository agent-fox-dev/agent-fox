package afspec

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSpecPopulatesEveryArtifact(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)

	if spec.SpecID != "01" || spec.SpecName != "test_feature" {
		t.Errorf("identity = %q/%q; want 01/test_feature", spec.SpecID, spec.SpecName)
	}
	if spec.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d; want %d", spec.SchemaVersion, SchemaVersion)
	}
	if spec.Requirements == nil || spec.TestSpec == nil || spec.Tasks == nil {
		t.Fatal("one of the three JSON artifacts is nil")
	}
	if got := len(spec.Requirements.Requirements); got != 2 {
		t.Errorf("requirements = %d; want 2", got)
	}
	if got := len(spec.TestSpec.Tests); got != 5 {
		t.Errorf("tests = %d; want 5", got)
	}
	if got := len(spec.Tasks.Tasks); got != 3 {
		t.Errorf("tasks = %d; want 3", got)
	}
	if spec.Architecture != "" {
		t.Errorf("architecture = %q; want empty for a fixture with no architecture.md", spec.Architecture)
	}
	if spec.Dir != fixtureValidSpec {
		t.Errorf("Dir = %q; want %q", spec.Dir, fixtureValidSpec)
	}
}

func TestLoadSpecReadsOptionalArchitecture(t *testing.T) {
	spec := loadFixture(t, "../testdata/valid_spec_with_arch")
	if spec.Architecture == "" {
		t.Error("architecture.md was present but Architecture is empty")
	}
}

func TestLoadSpecFailures(t *testing.T) {
	cases := []struct {
		name string
		dir  string
	}{
		{"missing artifact", "../testdata/missing_req"},
		{"malformed JSON", "../testdata/malformed_json"},
		{"malformed YAML frontmatter", "../testdata/malformed_yaml"},
		{"directory does not exist", "../testdata/does_not_exist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := LoadSpec(tc.dir)
			if err == nil {
				t.Fatalf("LoadSpec(%q) = nil error; want a failure", tc.dir)
			}
			if spec != nil {
				t.Error("a failed load returned a non-nil Spec")
			}
			var loadErr *LoadError
			if !errors.As(err, &loadErr) {
				t.Errorf("error is %T; want it to unwrap to *LoadError", err)
			}
		})
	}
}

func TestLoadSpecOnAFileIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSpec(path); err == nil {
		t.Fatal("LoadSpec on a regular file returned no error")
	}
}

// TestSaveRoundTripIsByteIdentical is the round-trip fidelity contract: a
// fixture loaded and saved without modification produces the same bytes. It
// covers the PRD renderer, the deterministic marshaller and the schema field
// ordering at once.
func TestSaveRoundTripIsByteIdentical(t *testing.T) {
	for _, fixture := range []string{
		fixtureValidSpec,
		fixtureV2Example,
		"../testdata/draft_spec",
		"../testdata/alpha_prefix_spec",
		"../testdata/valid_spec_with_arch",
	} {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			spec := loadFixture(t, fixture)
			dst := t.TempDir()
			if err := spec.Save(dst); err != nil {
				t.Fatalf("Save = %v", err)
			}

			names := []string{"prd.md", "requirements.json", "test_spec.json", "tasks.json"}
			if spec.Architecture != "" {
				names = append(names, "architecture.md")
			}
			for _, name := range names {
				want, err := os.ReadFile(filepath.Join(fixture, name))
				if err != nil {
					t.Fatalf("ReadFile(%s) = %v", name, err)
				}
				got, err := os.ReadFile(filepath.Join(dst, name))
				if err != nil {
					t.Fatalf("ReadFile(saved %s) = %v", name, err)
				}
				if string(got) != string(want) {
					t.Errorf("%s is not byte-identical after a save round trip:\n%s",
						name, firstDifference(string(want), string(got)))
				}
			}
		})
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	dst := t.TempDir()
	if err := spec.Save(dst); err != nil {
		t.Fatalf("Save = %v", err)
	}
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if contains(e.Name(), ".tmp.") {
			t.Errorf("temp file %q survived a successful save", e.Name())
		}
	}
}

func TestSaveRejectsTerminalStates(t *testing.T) {
	for _, status := range []string{"sealed", "superseded", "archived"} {
		t.Run(status, func(t *testing.T) {
			spec := loadFixture(t, fixtureValidSpec)
			spec.Status = status
			err := spec.Save(t.TempDir())
			if err == nil {
				t.Fatalf("Save on a %s spec returned no error", status)
			}
			var lifecycleErr *LifecycleError
			if !errors.As(err, &lifecycleErr) {
				t.Errorf("error is %T; want *LifecycleError", err)
			}
		})
	}
}

func TestSaveRejectsMutatedIdentityOnActiveSpec(t *testing.T) {
	dir := copyFixture(t, fixtureValidSpec)
	spec := loadFixture(t, dir)
	spec.Status = "active"
	hash, err := ComputeIntentHash(spec.PRDBody)
	if err != nil {
		t.Fatal(err)
	}
	spec.IntentHash = &hash
	if err := spec.Save(dir); err != nil {
		t.Fatalf("Save of the activated spec = %v", err)
	}

	reloaded := loadFixture(t, dir)
	reloaded.SpecName = "renamed"
	if err := reloaded.Save(dir); err == nil {
		t.Fatal("Save accepted a renamed spec_name on an active spec")
	}
}

func TestSaveDetectsIntentDrift(t *testing.T) {
	dir := copyFixture(t, fixtureValidSpec)
	spec := loadFixture(t, dir)
	spec.Status = "active"
	hash, err := ComputeIntentHash(spec.PRDBody)
	if err != nil {
		t.Fatal(err)
	}
	spec.IntentHash = &hash
	if err := spec.Save(dir); err != nil {
		t.Fatal(err)
	}

	drifted := loadFixture(t, dir)
	drifted.PRDBody = "# Test Feature\n\n## Intent\n\nSomething else entirely.\n"
	err = drifted.Save(dir)
	if err == nil {
		t.Fatal("Save accepted an edited Intent section on an active spec")
	}
	var intentErr *IntentError
	if !errors.As(err, &intentErr) {
		t.Errorf("error is %T; want *IntentError", err)
	}
}

// TestSaveDoesNotStoreDerivedData guards format v2 §7.1 and §8.5: coverage and
// traceability are computed, so nothing resembling them may reach disk.
func TestSaveDoesNotStoreDerivedData(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	dst := t.TempDir()
	if err := spec.Save(dst); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"test_spec.json", "tasks.json"} {
		data, err := os.ReadFile(filepath.Join(dst, name))
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{`"coverage"`, `"traceability"`} {
			if contains(string(data), forbidden) {
				t.Errorf("%s contains %s; derived data must never be stored", name, forbidden)
			}
		}
	}
}
