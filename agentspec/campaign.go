package agentspec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/goccy/go-yaml"
)

// CampaignMetadata holds the metadata stored in campaign.yaml.
type CampaignMetadata struct {
	Name        string    `yaml:"name"`
	Description string    `yaml:"description"`
	CreatedAt   time.Time `yaml:"created_at"`
	UpdatedAt   time.Time `yaml:"updated_at"`
}

// Campaign represents a campaign directory with its metadata.
type Campaign struct {
	Path     string
	Metadata CampaignMetadata
}

// CreateCampaign initializes a new campaign directory by writing
// campaign.yaml atomically using the atomic temp-file-and-rename pattern.
// Returns a CampaignError if the parent directory does not exist,
// campaign.yaml already exists, or name/description is empty.
func CreateCampaign(path, name, description string) (*Campaign, error) {
	// Validate name and description are non-empty.
	if name == "" {
		return nil, &CampaignError{Msg: "campaign name must not be empty"}
	}
	if description == "" {
		return nil, &CampaignError{Msg: "campaign description must not be empty"}
	}

	// Validate parent directory exists.
	parentDir := filepath.Dir(path)
	if _, err := os.Stat(parentDir); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("parent directory does not exist: %s", parentDir),
			Cause: err,
		}
	}

	// Check that campaign.yaml does not already exist at path.
	yamlPath := filepath.Join(path, "campaign.yaml")
	if _, err := os.Stat(yamlPath); err == nil {
		return nil, &CampaignError{
			Msg: fmt.Sprintf("campaign.yaml already exists at %s", path),
		}
	}

	// Create the campaign directory if it does not exist.
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to create campaign directory %s: %v", path, err),
			Cause: err,
		}
	}

	now := time.Now().UTC()
	meta := CampaignMetadata{
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// Marshal metadata to YAML.
	data, err := yaml.Marshal(meta)
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to marshal campaign metadata: %v", err),
			Cause: err,
		}
	}

	// Atomic write: write to temp file in same directory, then rename.
	tmpFile, err := os.CreateTemp(path, "campaign.yaml.tmp.*")
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to create temporary file for campaign.yaml: %v", err),
			Cause: err,
		}
	}
	tmpPath := tmpFile.Name()

	// Ensure cleanup of temp file on any failure.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to write campaign.yaml temp file: %v", err),
			Cause: err,
		}
	}
	if err := tmpFile.Close(); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to close campaign.yaml temp file: %v", err),
			Cause: err,
		}
	}

	// Rename temp file to final path atomically.
	if err := os.Rename(tmpPath, yamlPath); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to rename temp file to campaign.yaml: %v", err),
			Cause: err,
		}
	}

	success = true

	return &Campaign{
		Path:     path,
		Metadata: meta,
	}, nil
}

// OpenCampaign reads and parses an existing campaign.yaml from path
// to reconstruct a Campaign. Returns a CampaignError if the file is
// missing, malformed, or missing required fields.
func OpenCampaign(path string) (*Campaign, error) {
	// Validate path is non-empty.
	if path == "" {
		return nil, &CampaignError{Msg: "campaign path must not be empty"}
	}

	yamlPath := filepath.Join(path, "campaign.yaml")

	data, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to read campaign.yaml at %s: %v", path, err),
			Cause: err,
		}
	}

	var meta CampaignMetadata
	if err := yaml.Unmarshal(data, &meta); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to parse campaign.yaml at %s: %v", path, err),
			Cause: err,
		}
	}

	// Validate required fields.
	if meta.Name == "" {
		return nil, &CampaignError{
			Msg: fmt.Sprintf("campaign.yaml at %s is missing required field: name", path),
		}
	}
	if meta.CreatedAt.IsZero() {
		return nil, &CampaignError{
			Msg: fmt.Sprintf("campaign.yaml at %s is missing required field: created_at", path),
		}
	}

	return &Campaign{
		Path:     path,
		Metadata: meta,
	}, nil
}

// Specs returns the names of all subdirectories in the campaign directory
// that match the {NN}_{snake_case} spec directory naming pattern, sorted
// by numeric prefix ascending. The archive/ subdirectory is excluded.
func (c *Campaign) Specs() ([]string, error) {
	entries, err := os.ReadDir(c.Path)
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to read campaign directory %s: %v", c.Path, err),
			Cause: err,
		}
	}

	type specEntry struct {
		name   string
		prefix string
	}

	var specs []specEntry
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "archive" {
			continue
		}
		if !afspec.IsSpecDirName(name) {
			continue
		}
		prefix, _, err := afspec.ParseSpecDirName(name)
		if err != nil {
			continue
		}
		specs = append(specs, specEntry{name: name, prefix: prefix})
	}

	// Sort by numeric prefix ascending.
	sort.Slice(specs, func(i, j int) bool {
		return specs[i].prefix < specs[j].prefix
	})

	result := make([]string, len(specs))
	for i, s := range specs {
		result[i] = s.name
	}
	return result, nil
}

// specNamePattern validates that specName matches [a-z][a-z0-9_]*.
var specNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// NewSpec provisions a new spec directory within the campaign from a PRD
// file. It validates the spec name, computes the next numeric prefix,
// creates the directory, writes prd.md with YAML frontmatter, and
// initializes a SpecSession via CreateSession.
func (c *Campaign) NewSpec(specName, prdPath, mode, source string) (*SpecSession, error) {
	// Validate specName against [a-z][a-z0-9_]*.
	if !specNamePattern.MatchString(specName) {
		return nil, &CampaignError{
			Msg: fmt.Sprintf("invalid spec name %q: must match [a-z][a-z0-9_]*", specName),
		}
	}

	// Validate prdPath exists and is readable.
	prdBody, err := os.ReadFile(prdPath)
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to read PRD file %s: %v", prdPath, err),
			Cause: err,
		}
	}

	// Compute next numeric prefix by scanning both active specs and archive/.
	maxPrefix := 0

	// Scan active specs.
	entries, err := os.ReadDir(c.Path)
	if err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to read campaign directory %s: %v", c.Path, err),
			Cause: err,
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "archive" {
			continue
		}
		if afspec.IsSpecDirName(name) {
			prefixStr, _, err := afspec.ParseSpecDirName(name)
			if err == nil {
				if n, err := strconv.Atoi(prefixStr); err == nil && n > maxPrefix {
					maxPrefix = n
				}
			}
		}
	}

	// Scan archive/ subdirectory for higher prefixes.
	archiveDir := filepath.Join(c.Path, "archive")
	if archiveEntries, err := os.ReadDir(archiveDir); err == nil {
		for _, entry := range archiveEntries {
			if !entry.IsDir() {
				continue
			}
			if afspec.IsSpecDirName(entry.Name()) {
				prefixStr, _, err := afspec.ParseSpecDirName(entry.Name())
				if err == nil {
					if n, err := strconv.Atoi(prefixStr); err == nil && n > maxPrefix {
						maxPrefix = n
					}
				}
			}
		}
	}

	nextPrefix := maxPrefix + 1
	specDirName := fmt.Sprintf("%02d_%s", nextPrefix, specName)
	specDir := filepath.Join(c.Path, specDirName)

	// Create spec directory.
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to create spec directory %s: %v", specDir, err),
			Cause: err,
		}
	}

	// Build prd.md with YAML frontmatter.
	now := time.Now().UTC()
	specID := fmt.Sprintf("%02d", nextPrefix)

	var prdMD strings.Builder
	prdMD.WriteString("---\n")
	prdMD.WriteString(fmt.Sprintf("spec_id: %s\n", specID))
	prdMD.WriteString(fmt.Sprintf("spec_name: %s\n", specName))
	prdMD.WriteString(fmt.Sprintf("title: %s\n", specName))
	prdMD.WriteString("status: draft\n")
	prdMD.WriteString(fmt.Sprintf("created_at: %s\n", now.Format(time.RFC3339)))
	prdMD.WriteString(fmt.Sprintf("updated_at: %s\n", now.Format(time.RFC3339)))
	prdMD.WriteString("owner: \"\"\n")
	prdMD.WriteString(fmt.Sprintf("source: %s\n", source))
	prdMD.WriteString("schema_version: 1\n")
	prdMD.WriteString("---\n")
	prdMD.Write(prdBody)

	// Write prd.md.
	prdFilePath := filepath.Join(specDir, "prd.md")
	if err := os.WriteFile(prdFilePath, []byte(prdMD.String()), 0o644); err != nil {
		return nil, &CampaignError{
			Msg:   fmt.Sprintf("failed to write prd.md: %v", err),
			Cause: err,
		}
	}

	// Initialize session via CreateSession.
	session, err := CreateSession(specDir, mode, source)
	if err != nil {
		// Per 06-REQ-6.E1: return the error from CreateSession;
		// the spec directory and prd.md remain on disk for manual recovery.
		return nil, err
	}

	return session, nil
}
