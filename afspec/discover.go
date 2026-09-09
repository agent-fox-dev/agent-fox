package afspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SpecMeta is a lightweight struct containing metadata parsed from a spec's
// PRD frontmatter, used for discovery and landscape views.
type SpecMeta struct {
	SpecID   string
	SpecName string
	Status   string
	Dir      string
}

// DiscoverSpecs scans the root directory for subdirectories matching the
// NN_snake_case pattern, parses the PRD frontmatter of each, and returns
// a slice of SpecMeta entries with SpecID, SpecName, Status, and Dir
// fields populated.
//
// If a subdirectory matches the pattern but its prd.md is missing or
// malformed, the entry is skipped and the error is collected. If the
// root directory does not exist or is not readable, a LoadError is returned.
func DiscoverSpecs(root string) ([]SpecMeta, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, &LoadError{
			Msg:  fmt.Sprintf("cannot read root directory: %s", err),
			File: root,
			Err:  &SpecError{Msg: fmt.Sprintf("cannot read root directory: %s", err)},
		}
	}

	var metas []SpecMeta
	var errs []error

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsSpecDirName(name) {
			continue
		}

		dir := filepath.Join(root, name)
		meta, parseErr := parseSpecMeta(dir)
		if parseErr != nil {
			errs = append(errs, parseErr)
			continue
		}
		metas = append(metas, meta)
	}

	if metas == nil {
		metas = []SpecMeta{}
	}

	return metas, errors.Join(errs...)
}

// parseSpecMeta reads and parses the PRD frontmatter from a spec directory
// to extract lightweight metadata.
func parseSpecMeta(dir string) (SpecMeta, error) {
	prdPath := filepath.Join(dir, "prd.md")
	prdData, err := os.ReadFile(prdPath)
	if err != nil {
		return SpecMeta{}, fmt.Errorf("cannot read prd.md in %s: %w", dir, err)
	}

	fm, _, err := parsePRD(prdData, prdPath)
	if err != nil {
		return SpecMeta{}, err
	}

	return SpecMeta{
		SpecID:   fm.SpecID,
		SpecName: fm.SpecName,
		Status:   fm.Status,
		Dir:      dir,
	}, nil
}

// LoadSpecLandscape collects metadata for all specs for landscape views.
// It scans spec directories, optionally including the archive subdirectory,
// and excludes the currentSpecID entry from results.
//
// When includeArchive is true, archived specs from {root}/archive/ are
// included. When false, only non-archived spec directories are returned.
func LoadSpecLandscape(root string, includeArchive bool, currentSpecID string) ([]SpecMeta, error) {
	// Scan the root directory; ignore errors from malformed PRDs.
	metas, _ := DiscoverSpecs(root)

	if includeArchive {
		archiveDir := filepath.Join(root, "archive")
		archiveMetas, _ := DiscoverSpecs(archiveDir)
		metas = append(metas, archiveMetas...)
	}

	// Filter out currentSpecID
	if currentSpecID != "" {
		filtered := make([]SpecMeta, 0, len(metas))
		for _, m := range metas {
			if m.SpecID != currentSpecID {
				filtered = append(filtered, m)
			}
		}
		metas = filtered
	}

	if metas == nil {
		metas = []SpecMeta{}
	}

	return metas, nil
}
