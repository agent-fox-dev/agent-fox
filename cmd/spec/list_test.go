package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- TS-08-17: Verify that spec list scans the spec directory, reads
//     _session.json state for each spec, and emits the correct JSON
//     structure ---

// TestTS08_17_ListScansSpecDir verifies that spec list discovers spec
// subdirectories matching the NN_snake_case pattern, reads their
// _session.json state fields, and emits JSON with ok: true, spec_dir,
// and a specs array.
// Covers: TS-08-17, Requirement: 08-REQ-7.1
func TestTS08_17_ListScansSpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create spec directories with _session.json files.
	spec1Dir := filepath.Join(specDir, "08_my_spec")
	if err := os.MkdirAll(spec1Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(spec1Dir, "_session.json"),
		[]byte(`{"state": "assessing"}`),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	spec2Dir := filepath.Join(specDir, "09_other_spec")
	if err := os.MkdirAll(spec2Dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(spec2Dir, "_session.json"),
		[]byte(`{"state": "generated"}`),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	output := stdoutBuf.String()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}

	// Verify top-level fields.
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}
	if _, exists := parsed["spec_dir"]; !exists {
		t.Error("parsed missing 'spec_dir' field")
	}

	// Verify specs array.
	specsRaw, exists := parsed["specs"]
	if !exists {
		t.Fatal("parsed missing 'specs' field")
	}
	specs, ok := specsRaw.([]any)
	if !ok {
		t.Fatalf("specs is not an array: %T", specsRaw)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d; want 2", len(specs))
	}

	// Collect spec names and states.
	nameStateMap := make(map[string]string)
	for _, s := range specs {
		smap, ok := s.(map[string]any)
		if !ok {
			t.Fatalf("spec entry is not an object: %T", s)
		}
		name, _ := smap["name"].(string)
		state, _ := smap["state"].(string)
		nameStateMap[name] = state
	}

	if state, exists := nameStateMap["08_my_spec"]; !exists || state != "assessing" {
		t.Errorf("spec 08_my_spec state = %q; want %q", state, "assessing")
	}
	if state, exists := nameStateMap["09_other_spec"]; !exists || state != "generated" {
		t.Errorf("spec 09_other_spec state = %q; want %q", state, "generated")
	}
}

// --- 08-REQ-7.E1: spec directory does not exist ---

// TestTS08_17_ListMissingSpecDir verifies that spec list emits an empty
// specs array (not an error) when the spec directory does not exist.
// Covers: 08-REQ-7.E1, 08-PROP-8
func TestTS08_17_ListMissingSpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, "nonexistent_specs")

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; spec list should always exit 0", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("parsed.ok = %v; want true", parsed["ok"])
	}

	specsRaw, exists := parsed["specs"]
	if !exists {
		t.Fatal("parsed missing 'specs' field")
	}
	specs, ok := specsRaw.([]any)
	if !ok {
		t.Fatalf("specs is not an array: %T", specsRaw)
	}
	if len(specs) != 0 {
		t.Errorf("len(specs) = %d; want 0", len(specs))
	}
}

// --- 08-REQ-7.E2: malformed _session.json defaults to 'no_session' ---

// TestTS08_17_ListMalformedSessionJSON verifies that when _session.json
// contains malformed JSON, the state defaults to 'no_session' and listing
// continues for remaining specs.
// Covers: 08-REQ-7.E2
func TestTS08_17_ListMalformedSessionJSON(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Spec with valid _session.json.
	goodSpec := filepath.Join(specDir, "01_good_spec")
	if err := os.MkdirAll(goodSpec, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(goodSpec, "_session.json"),
		[]byte(`{"state": "init"}`),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	// Spec with malformed _session.json.
	badSpec := filepath.Join(specDir, "02_bad_spec")
	if err := os.MkdirAll(badSpec, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(badSpec, "_session.json"),
		[]byte(`{not valid json!!!`),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; spec list should always exit 0", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	specsRaw := parsed["specs"]
	specs, ok := specsRaw.([]any)
	if !ok {
		t.Fatalf("specs is not an array: %T", specsRaw)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d; want 2", len(specs))
	}

	nameStateMap := make(map[string]string)
	for _, s := range specs {
		smap, _ := s.(map[string]any)
		name, _ := smap["name"].(string)
		state, _ := smap["state"].(string)
		nameStateMap[name] = state
	}

	if state := nameStateMap["01_good_spec"]; state != "init" {
		t.Errorf("01_good_spec state = %q; want %q", state, "init")
	}
	if state := nameStateMap["02_bad_spec"]; state != "no_session" {
		t.Errorf("02_bad_spec state = %q; want %q (malformed JSON should default)", state, "no_session")
	}
}

// --- 08-REQ-7.E3: spec directory exists but has no matching subdirectories ---

// TestTS08_17_ListEmptySpecDir verifies that spec list emits an empty
// specs array when the spec directory exists but contains no spec subdirs.
// Covers: 08-REQ-7.E3
func TestTS08_17_ListEmptySpecDir(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Add some non-matching entries.
	os.WriteFile(filepath.Join(specDir, "readme.md"), []byte("hi"), 0644)
	os.MkdirAll(filepath.Join(specDir, "archive"), 0755)
	os.MkdirAll(filepath.Join(specDir, "not_a_spec"), 0755) // missing numeric prefix

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v; spec list should always exit 0", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	specs, ok := parsed["specs"].([]any)
	if !ok {
		t.Fatalf("specs is not an array")
	}
	if len(specs) != 0 {
		t.Errorf("len(specs) = %d; want 0 (no matching spec subdirs)", len(specs))
	}
}

// --- 08-PROP-8: spec list always exits 0 ---

// TestTS08_17_ListAlwaysExits0 verifies the invariant that spec list
// always exits with code 0 regardless of the spec directory state.
// Covers: 08-PROP-8
func TestTS08_17_ListAlwaysExits0(t *testing.T) {
	scenarios := []struct {
		name  string
		setup func(t *testing.T, specDir string)
	}{
		{
			name:  "nonexistent_dir",
			setup: func(t *testing.T, specDir string) {},
		},
		{
			name: "empty_dir",
			setup: func(t *testing.T, specDir string) {
				os.MkdirAll(specDir, 0755)
			},
		},
		{
			name: "has_specs",
			setup: func(t *testing.T, specDir string) {
				os.MkdirAll(specDir, 0755)
				specSub := filepath.Join(specDir, "01_test")
				os.MkdirAll(specSub, 0755)
				os.WriteFile(filepath.Join(specSub, "_session.json"),
					[]byte(`{"state":"init"}`), 0644)
			},
		},
		{
			name: "malformed_sessions",
			setup: func(t *testing.T, specDir string) {
				os.MkdirAll(specDir, 0755)
				specSub := filepath.Join(specDir, "01_test")
				os.MkdirAll(specSub, 0755)
				os.WriteFile(filepath.Join(specSub, "_session.json"),
					[]byte(`INVALID JSON`), 0644)
			},
		},
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			specDir := filepath.Join(tmpDir, ".specs")
			sc.setup(t, specDir)

			cmd := newRootCmd()
			stdoutBuf := new(bytes.Buffer)
			cmd.SetOut(stdoutBuf)
			cmd.SetErr(new(bytes.Buffer))
			cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

			err := cmd.Execute()
			if err != nil {
				t.Errorf("spec list returned error: %v; want always exit 0", err)
			}
		})
	}
}

// TestTS08_17_ListMissingSessionJSON verifies that spec list defaults
// the state to 'no_session' when _session.json does not exist at all.
// Covers: 08-REQ-7.1
func TestTS08_17_ListMissingSessionJSON(t *testing.T) {
	tmpDir := t.TempDir()
	specDir := filepath.Join(tmpDir, ".specs")
	if err := os.MkdirAll(specDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create spec directory with NO _session.json.
	specSub := filepath.Join(specDir, "01_no_session_spec")
	if err := os.MkdirAll(specSub, 0755); err != nil {
		t.Fatal(err)
	}

	cmd := newRootCmd()
	stdoutBuf := new(bytes.Buffer)
	cmd.SetOut(stdoutBuf)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specDir, "list"})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(stdoutBuf.String()), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}

	specs, ok := parsed["specs"].([]any)
	if !ok {
		t.Fatal("specs is not an array")
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d; want 1", len(specs))
	}

	smap, ok := specs[0].(map[string]any)
	if !ok {
		t.Fatal("spec entry is not an object")
	}
	state, _ := smap["state"].(string)
	if state != "no_session" {
		t.Errorf("state = %q; want %q", state, "no_session")
	}
}
