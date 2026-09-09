package legacy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Spec holds the three JSON artifacts of a format version 1.3 spec package.
// The PRD is not part of it: prd.md is format-agnostic apart from its
// schema_version field, so callers parse it with the v2 loader.
type Spec struct {
	Requirements *RequirementsV1Json
	TestSpec     *TestSpecV1Json
	Tasks        *TasksV1Json
}

// LoadSpec reads requirements.json, test_spec.json and tasks.json from dir
// and decodes them as version 1.3 artifacts. The generated UnmarshalJSON
// methods reject missing required fields, unknown enum values and malformed
// IDs, so a successful load means the input is structurally a v1 spec.
func LoadSpec(dir string) (*Spec, error) {
	var req RequirementsV1Json
	if err := loadArtifact(filepath.Join(dir, "requirements.json"), &req); err != nil {
		return nil, err
	}
	var ts TestSpecV1Json
	if err := loadArtifact(filepath.Join(dir, "test_spec.json"), &ts); err != nil {
		return nil, err
	}
	var tasks TasksV1Json
	if err := loadArtifact(filepath.Join(dir, "tasks.json"), &tasks); err != nil {
		return nil, err
	}
	return &Spec{Requirements: &req, TestSpec: &ts, Tasks: &tasks}, nil
}

func loadArtifact(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("cannot parse %s as a version 1 artifact: %w", filepath.Base(path), err)
	}
	return nil
}
