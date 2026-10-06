package specgen

import (
	"strings"
	"testing"
)

// TS-13-22: Generation prompt includes a relevant-files block when the PRD
// supplied a non-empty list.
func TestTS_13_22_GenerationPromptIncludesRelevantFilesBlock(t *testing.T) {
	files := []RelevantFile{
		{Path: "internal/agentrun/phase.go", Why: "the phase runner"},
		{Path: "internal/toolio/envelope.go", Why: "the envelope"},
	}
	block := relevantFilesBlock(files)
	prompt := generationUserPrompt("requirements", "13", "test", "/",
		"prd body", "", "", "", "", block, "")
	if !strings.Contains(prompt, "## Files the PRD phase found relevant") {
		t.Error("prompt missing '## Files the PRD phase found relevant' heading")
	}
	if !strings.Contains(prompt, "`internal/agentrun/phase.go` — the phase runner") {
		t.Error("prompt missing entry for phase.go")
	}
	if !strings.Contains(prompt, "previous phase's notes") {
		t.Error("prompt missing introductory text about previous phase's notes")
	}
}

// TS-13-23: Generation prompt omits the relevant-files block when the PRD
// did not supply relevant_files.
func TestTS_13_23_GenerationPromptOmitsRelevantFilesBlockWhenEmpty(t *testing.T) {
	promptNil := generationUserPrompt("requirements", "13", "test", "/",
		"prd body", "", "", "", "", relevantFilesBlock(nil), "")
	promptEmpty := generationUserPrompt("requirements", "13", "test", "/",
		"prd body", "", "", "", "", relevantFilesBlock([]RelevantFile{}), "")
	promptWithout := generationUserPrompt("requirements", "13", "test", "/",
		"prd body", "", "", "", "", "", "")

	if strings.Contains(promptNil, "## Files the PRD phase found relevant") {
		t.Error("nil files produced a relevant-files block")
	}
	if strings.Contains(promptEmpty, "## Files the PRD phase found relevant") {
		t.Error("empty files produced a relevant-files block")
	}
	if promptNil != promptWithout {
		t.Error("prompt with nil files differs from prompt without the feature")
	}
	if promptEmpty != promptWithout {
		t.Error("prompt with empty files differs from prompt without the feature")
	}
}

// TS-13-24: artifactRequest carries the relevant_files list for generation
// and architecture prompts.
func TestTS_13_24_ArtifactRequestCarriesRelevantFiles(t *testing.T) {
	req := artifactRequest{
		RelevantFiles: []RelevantFile{{Path: "a.go", Why: "reason"}},
	}
	if len(req.RelevantFiles) != 1 {
		t.Fatalf("expected 1 relevant file, got %d", len(req.RelevantFiles))
	}
	if req.RelevantFiles[0].Path != "a.go" {
		t.Errorf("path = %q, want %q", req.RelevantFiles[0].Path, "a.go")
	}
}

// TS-13-25: Architecture prompt includes the relevant-files block when the
// PRD supplied a non-empty list.
func TestTS_13_25_ArchitecturePromptIncludesRelevantFilesBlock(t *testing.T) {
	files := []RelevantFile{{Path: "a.go", Why: "reason"}}
	req := architectureRequest{
		SpecID:        "01",
		SpecName:      "test",
		Root:          "/",
		PRD:           "prd body",
		RelevantFiles: files,
	}
	// Build the architecture prompt the same way WriteArchitecture does.
	prompt := fill(template("architecture_user.md"), map[string]string{
		"spec_id":              req.SpecID,
		"spec_name":            req.SpecName,
		"root":                 req.Root,
		"prd":                  strings.TrimSpace(req.PRD),
		"relevant_files_block": relevantFilesBlock(req.RelevantFiles),
		"prior_block":          "",
	})
	if !strings.Contains(prompt, "## Files the PRD phase found relevant") {
		t.Error("architecture prompt missing relevant-files heading")
	}
	if !strings.Contains(prompt, "`a.go` \u2014 reason") {
		t.Error("architecture prompt missing entry")
	}
}
