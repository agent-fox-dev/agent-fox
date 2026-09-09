package afspec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// immutableSnapshot captures the values of fields that must not change once a
// spec becomes active: spec_id, spec_name, and created_at (Section 9 of the
// spec format: "Immutable after creation").
type immutableSnapshot struct {
	SpecID    string
	SpecName  string
	CreatedAt string
}

// Spec represents a complete specification package with all artifacts.
// The PRD frontmatter fields are stored directly on the struct, while
// the PRD Markdown body is stored in PRDBody. The three JSON artifacts
// are stored as pointers to the auto-generated types.
type Spec struct {
	// PRD frontmatter fields
	SpecID        string
	SpecName      string
	Title         string
	Status        string
	CreatedAt     string
	UpdatedAt     string
	Owner         string
	Source        string
	Supersedes    []string
	Tags          []string
	IntentHash    *string
	SchemaVersion int

	// PRD Markdown body (everything after the closing --- delimiter)
	PRDBody string

	// Dir is the directory path from which this spec was loaded. It is
	// set by LoadSpec and used by ValidateCrossFile to check that the
	// folder name's numeric prefix and snake_case suffix match SpecID
	// and SpecName. Empty when the spec was not loaded from disk.
	Dir string

	// JSON artifacts
	Requirements *RequirementsV1Json
	TestSpec     *TestSpecV1Json
	Tasks        *TasksV1Json

	// Optional architecture document
	Architecture string

	// loaded holds an immutable snapshot of identity fields captured at load
	// time (or at activation via Transition). Non-nil when the spec was loaded
	// in an active state or transitioned to active, enabling Save to reject
	// mutations to spec_id, spec_name, and created_at.
	loaded *immutableSnapshot
}

// LoadSpec reads all spec artifacts from a directory and returns a
// populated Spec struct. It reads prd.md, requirements.json,
// test_spec.json, tasks.json, and optionally architecture.md.
func LoadSpec(dir string) (*Spec, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, &LoadError{
			Msg:  fmt.Sprintf("cannot access spec directory: %s", err),
			File: dir,
			Err:  &SpecError{Msg: fmt.Sprintf("cannot access spec directory: %s", err)},
		}
	}
	if !info.IsDir() {
		return nil, &LoadError{
			Msg:  fmt.Sprintf("not a directory: %s", dir),
			File: dir,
			Err:  &SpecError{Msg: fmt.Sprintf("not a directory: %s", dir)},
		}
	}

	// Read and parse prd.md
	prdPath := filepath.Join(dir, "prd.md")
	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		return nil, &LoadError{
			Msg:  fmt.Sprintf("cannot read prd.md: %s", err),
			File: prdPath,
			Err:  &SpecError{Msg: fmt.Sprintf("cannot read prd.md: %s", err)},
		}
	}

	fm, body, err := parsePRD(prdData, prdPath)
	if err != nil {
		return nil, err // parsePRD already returns LoadError
	}

	// Read and parse requirements.json
	reqPath := filepath.Join(dir, "requirements.json")
	var req RequirementsV1Json
	if err := loadJSONArtifact(reqPath, &req); err != nil {
		return nil, err
	}

	// Read and parse test_spec.json
	tsPath := filepath.Join(dir, "test_spec.json")
	var ts TestSpecV1Json
	if err := loadJSONArtifact(tsPath, &ts); err != nil {
		return nil, err
	}

	// Read and parse tasks.json
	tasksPath := filepath.Join(dir, "tasks.json")
	var tasks TasksV1Json
	if err := loadJSONArtifact(tasksPath, &tasks); err != nil {
		return nil, err
	}

	// Read architecture.md (optional)
	var architecture string
	archPath := filepath.Join(dir, "architecture.md")
	archData, err := os.ReadFile(archPath)
	if err == nil {
		architecture = string(archData)
	}
	// If architecture.md doesn't exist, that's fine — leave empty

	spec := &Spec{
		SpecID:        fm.SpecID,
		SpecName:      fm.SpecName,
		Title:         fm.Title,
		Status:        fm.Status,
		CreatedAt:     fm.CreatedAt,
		UpdatedAt:     fm.UpdatedAt,
		Owner:         fm.Owner,
		Source:        fm.Source,
		Supersedes:    fm.Supersedes,
		Tags:          fm.Tags,
		IntentHash:    fm.IntentHash,
		SchemaVersion: fm.SchemaVersion,
		PRDBody:       body,
		Requirements:  &req,
		TestSpec:      &ts,
		Tasks:         &tasks,
		Architecture:  architecture,
		Dir:           dir,
	}

	// Capture immutable snapshot for active specs so Save can detect mutations
	// to spec_id, spec_name, and created_at (Section 9: immutable after creation).
	if fm.Status == "active" {
		spec.loaded = &immutableSnapshot{
			SpecID:    fm.SpecID,
			SpecName:  fm.SpecName,
			CreatedAt: fm.CreatedAt,
		}
	}

	return spec, nil
}

// loadJSONArtifact reads and unmarshals a JSON artifact file.
// Returns a LoadError wrapping SpecError on failure.
func loadJSONArtifact(path string, target interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return &LoadError{
			Msg:  fmt.Sprintf("cannot read %s: %s", filepath.Base(path), err),
			File: path,
			Err:  &SpecError{Msg: fmt.Sprintf("cannot read %s: %s", filepath.Base(path), err)},
		}
	}

	if err := json.Unmarshal(data, target); err != nil {
		return &LoadError{
			Msg:  fmt.Sprintf("cannot parse %s: %s", filepath.Base(path), err),
			File: path,
			Err:  &SpecError{Msg: fmt.Sprintf("cannot parse %s: %s", filepath.Base(path), err)},
		}
	}

	return nil
}

// Save atomically writes all spec artifacts to the given directory.
// Each artifact is written to a temporary file first, then renamed
// to its final name. Returns a LifecycleError if the spec is sealed,
// superseded, or archived.
//
// For active specs with a stored IntentHash, Save recomputes the hash from
// the current PRD body and returns an IntentError if the ## Intent section
// has changed since activation (intent drift detection).
func (s *Spec) Save(dir string) error {
	if s.Status == "sealed" || s.Status == "superseded" || s.Status == "archived" {
		return &LifecycleError{
			Msg: fmt.Sprintf("cannot save spec in %s state", s.Status),
			Err: &SpecError{Msg: fmt.Sprintf("cannot save spec in %s state", s.Status)},
		}
	}

	// For active specs with a stored immutable snapshot, reject mutations to
	// spec_id, spec_name, and created_at (Section 9: immutable after creation).
	if s.Status == "active" && s.loaded != nil {
		if s.SpecID != s.loaded.SpecID {
			msg := fmt.Sprintf("cannot mutate immutable field spec_id on active spec (was %q, now %q)", s.loaded.SpecID, s.SpecID)
			return &LifecycleError{
				Msg: msg,
				Err: &SpecError{Msg: msg},
			}
		}
		if s.SpecName != s.loaded.SpecName {
			msg := fmt.Sprintf("cannot mutate immutable field spec_name on active spec (was %q, now %q)", s.loaded.SpecName, s.SpecName)
			return &LifecycleError{
				Msg: msg,
				Err: &SpecError{Msg: msg},
			}
		}
		if s.CreatedAt != s.loaded.CreatedAt {
			msg := fmt.Sprintf("cannot mutate immutable field created_at on active spec (was %q, now %q)", s.loaded.CreatedAt, s.CreatedAt)
			return &LifecycleError{
				Msg: msg,
				Err: &SpecError{Msg: msg},
			}
		}
	}

	// For active specs with a stored intent hash, detect drift by recomputing
	// the hash from the current PRD body and comparing against the stored value.
	if s.Status == "active" && s.IntentHash != nil {
		current, err := ComputeIntentHash(s.PRDBody)
		if err != nil {
			return err
		}
		if current != *s.IntentHash {
			msg := "intent drift detected: ## Intent section has changed since activation"
			return &IntentError{
				Msg: msg,
				Err: &SpecError{Msg: msg},
			}
		}
	}

	// Spec section 7.6: coverage is computed on every save, not authored.
	if s.TestSpec != nil && s.Requirements != nil {
		s.TestSpec.Coverage = s.TestSpec.ComputeCoverageStruct(s.Requirements)
	}

	return s.saveToDisk(dir)
}

// saveToDisk is the internal save method that bypasses lifecycle checks.
// It is used by Transition, Supersede, and MoveToArchive which need to
// persist specs in sealed, superseded, or archived states.
//
// Two-phase write strategy:
//
//	Phase 1 — all artifact data is written to temporary files (*.tmp.*) in
//	           dir and each file is fsynced to disk. If any write fails at
//	           this stage, all temporary files are removed and no on-disk
//	           artifact file is modified.
//	Phase 2 — all temporary files are renamed to their final names. Because
//	           renames are fast filesystem metadata operations on the same
//	           device, the window for inconsistency across artifacts is
//	           minimal compared to the previous interleaved write-then-rename
//	           approach.
//
// On return (success or failure) no *.tmp.* files remain in dir.
func (s *Spec) saveToDisk(dir string) error {
	// Verify directory exists
	if _, err := os.Stat(dir); err != nil {
		return &SaveError{
			Msg: fmt.Sprintf("cannot access directory: %s", err),
			Err: &SpecError{Msg: fmt.Sprintf("cannot access directory: %s", err)},
		}
	}

	// Render PRD
	prdContent := renderPRD(s)

	// Marshal JSON artifacts
	reqData, err := MarshalJSON(s.Requirements)
	if err != nil {
		return &SaveError{
			Msg: fmt.Sprintf("cannot marshal requirements.json: %s", err),
			Err: &SpecError{Msg: fmt.Sprintf("cannot marshal requirements.json: %s", err)},
		}
	}

	tsData, err := MarshalJSON(s.TestSpec)
	if err != nil {
		return &SaveError{
			Msg: fmt.Sprintf("cannot marshal test_spec.json: %s", err),
			Err: &SpecError{Msg: fmt.Sprintf("cannot marshal test_spec.json: %s", err)},
		}
	}

	tasksData, err := MarshalJSON(s.Tasks)
	if err != nil {
		return &SaveError{
			Msg: fmt.Sprintf("cannot marshal tasks.json: %s", err),
			Err: &SpecError{Msg: fmt.Sprintf("cannot marshal tasks.json: %s", err)},
		}
	}

	// Build the list of (name, data) pairs to persist.
	type artifact struct {
		name    string
		data    []byte
		tmpPath string
	}

	artifacts := []artifact{
		{name: "prd.md", data: prdContent},
		{name: "requirements.json", data: reqData},
		{name: "test_spec.json", data: tsData},
		{name: "tasks.json", data: tasksData},
	}

	if s.Architecture != "" {
		artifacts = append(artifacts, artifact{name: "architecture.md", data: []byte(s.Architecture)})
	}

	// tmpPaths accumulates paths of temp files that need cleanup on failure.
	// The deferred function removes any paths still in the slice, so callers
	// must clear the slice before returning nil to suppress cleanup on success.
	var tmpPaths []string
	defer func() {
		for _, p := range tmpPaths {
			os.Remove(p) //nolint:errcheck // best-effort cleanup
		}
	}()

	// Phase 1: write all artifact data to temp files, fsyncing each before
	// closing. If any write fails, the deferred cleanup removes all temp files
	// and no final artifact file has been modified.
	for i := range artifacts {
		tmpPath, err := writeTempFile(dir, artifacts[i].name, artifacts[i].data)
		if err != nil {
			return &SaveError{
				Msg: fmt.Sprintf("cannot write temp file for %s: %s", artifacts[i].name, err),
				Err: &SpecError{Msg: fmt.Sprintf("cannot write temp file for %s: %s", artifacts[i].name, err)},
			}
		}
		artifacts[i].tmpPath = tmpPath
		tmpPaths = append(tmpPaths, tmpPath)
	}

	// Phase 2: rename all temp files to their final names. Renames on the
	// same filesystem are atomic metadata operations; sequencing them all
	// after a fully-successful write phase minimises the inconsistency window
	// to near-zero.
	for _, a := range artifacts {
		finalPath := filepath.Join(dir, a.name)
		if err := os.Rename(a.tmpPath, finalPath); err != nil {
			return &SaveError{
				Msg: fmt.Sprintf("cannot rename temp file to %s: %s", a.name, err),
				Err: &SpecError{Msg: fmt.Sprintf("cannot rename temp file to %s: %s", a.name, err)},
			}
		}
	}

	// All renames succeeded; clear the cleanup list — temp paths no longer exist.
	tmpPaths = tmpPaths[:0]
	return nil
}

// writeTempFile writes data to a new temporary file inside dir (named
// "<name>.tmp.<random>"), fsyncs it, and returns the path. The caller is
// responsible for either renaming the file to its final destination or
// removing it on failure.
func writeTempFile(dir, name string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, name+".tmp.*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath) //nolint:errcheck
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpPath) //nolint:errcheck
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath) //nolint:errcheck
		return "", err
	}
	return tmpPath, nil
}
