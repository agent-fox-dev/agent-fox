package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// renderArtifactNames maps artifact basenames to their display key names.
var renderArtifactNames = map[string]string{
	"requirements.json": "requirements",
	"test_spec.json":    "test_spec",
	"tasks.json":        "tasks",
}

// renderArtifactOrder is the canonical order for rendering JSON artifacts.
var renderArtifactOrder = []string{"requirements.json", "test_spec.json", "tasks.json"}

// jsonArtifactKeys lists the display keys for JSON artifacts, used to
// distinguish them from markdown artifacts (prd, architecture).
var jsonArtifactKeys = []string{"requirements", "test_spec", "tasks"}

// newRenderCmd creates the "spec render" subcommand which renders spec
// artifacts as markdown or JSON.
func newRenderCmd() *cobra.Command {
	var jsonOutput bool
	var combined bool

	cmd := &cobra.Command{
		Use:   "render SPEC",
		Short: "Render spec artifacts as markdown or JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")
			specName := args[0]

			// Auto-enable --json when AF_AGENT=1 is active.
			if isAgentMode() {
				jsonOutput = true
			}

			// Resolve spec to its directory path.
			specPath, err := resolveSpec(specDir, specName)
			if err != nil {
				return err
			}

			// Read available artifacts.
			available, err := readAvailableArtifacts(specPath)
			if err != nil {
				return err
			}

			// Error if no JSON artifacts are available (prd/architecture alone
			// are not sufficient to produce a renderable output).
			jsonCount := 0
			for _, key := range jsonArtifactKeys {
				if _, ok := available[key]; ok {
					jsonCount++
				}
			}
			if jsonCount == 0 {
				return fmt.Errorf("no renderable artifacts found in %q", specPath)
			}

			w := cmd.OutOrStdout()

			if jsonOutput {
				return renderJSON(w, available, combined)
			}

			return renderMarkdown(w, available, combined)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON envelope")
	cmd.Flags().BoolVar(&combined, "combined", false, "combine all artifacts into a single document")

	return cmd
}

// readAvailableArtifacts reads artifact files from the spec directory,
// returning a map of display-key to content for each file that exists
// and is readable. The map includes:
//   - "prd": PRD body with YAML frontmatter stripped (if prd.md is present)
//   - "architecture": architecture content as-is (if architecture.md is present)
//   - "requirements", "test_spec", "tasks": JSON artifact content (if present)
//
// Returns an error if a file exists but cannot be read due to permissions.
func readAvailableArtifacts(specPath string) (map[string]string, error) {
	available := make(map[string]string)

	// Read prd.md — strip YAML frontmatter, include body only.
	prdPath := filepath.Join(specPath, "prd.md")
	if _, statErr := os.Stat(prdPath); statErr == nil {
		data, readErr := os.ReadFile(prdPath)
		if readErr != nil {
			return nil, fmt.Errorf("cannot read prd.md: %w", readErr)
		}
		available["prd"] = stripFrontmatter(string(data))
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("cannot stat prd.md: %w", statErr)
	}

	// Read architecture.md — optional, included as-is when present.
	archPath := filepath.Join(specPath, "architecture.md")
	if _, statErr := os.Stat(archPath); statErr == nil {
		data, readErr := os.ReadFile(archPath)
		if readErr != nil {
			return nil, fmt.Errorf("cannot read architecture.md: %w", readErr)
		}
		available["architecture"] = string(data)
	} else if !os.IsNotExist(statErr) {
		return nil, fmt.Errorf("cannot stat architecture.md: %w", statErr)
	}

	// Read JSON artifact files.
	for _, filename := range renderArtifactOrder {
		p := filepath.Join(specPath, filename)
		info, statErr := os.Stat(p)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, fmt.Errorf("cannot stat %s: %w", filename, statErr)
		}
		if info.Mode().Perm()&0400 == 0 {
			// File exists but is not readable — attempt read to surface the error.
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return nil, fmt.Errorf("cannot read %s: %w", filename, readErr)
			}
			key := renderArtifactNames[filename]
			available[key] = string(data)
			continue
		}

		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, fmt.Errorf("cannot read %s: %w", filename, readErr)
		}

		key := renderArtifactNames[filename]
		available[key] = string(data)
	}

	return available, nil
}

// stripFrontmatter removes YAML frontmatter from markdown content. Frontmatter
// is a block delimited by "---" on its own line at the very start of the file.
// Returns the body content with the frontmatter block removed. If the content
// does not begin with frontmatter, it is returned unchanged.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	// Skip past the opening --- line.
	nl := strings.IndexByte(content, '\n')
	if nl < 0 {
		return content
	}
	rest := content[nl+1:]
	// Find the closing --- on its own line.
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return content
	}
	body := rest[end+4:] // skip past "\n---"
	// Trim the newline that follows the closing delimiter.
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	} else if len(body) > 1 && body[0] == '\r' && body[1] == '\n' {
		body = body[2:]
	}
	return body
}

// renderJSON outputs artifacts in a JSON envelope.
func renderJSON(w interface{ Write([]byte) (int, error) }, available map[string]string, combined bool) error {
	if combined {
		// Combine all artifact content into a single markdown document.
		content := combineArtifacts(available)
		return emitOKTo(w, "format", "combined", "content", content)
	}

	// Individual format: emit only JSON artifact keys (not prd/architecture).
	jsonArtifacts := make(map[string]string)
	for _, key := range jsonArtifactKeys {
		if v, ok := available[key]; ok {
			jsonArtifacts[key] = v
		}
	}
	return emitOKTo(w, "format", "individual", "artifacts", jsonArtifacts)
}

// renderMarkdown outputs artifacts as raw markdown to the writer.
func renderMarkdown(w interface{ Write([]byte) (int, error) }, available map[string]string, combined bool) error {
	if combined {
		content := combineArtifacts(available)
		_, err := fmt.Fprint(w, content)
		return err
	}

	// Print each artifact separated by '---', with a header for each.
	first := true
	for _, filename := range renderArtifactOrder {
		key := renderArtifactNames[filename]
		content, exists := available[key]
		if !exists {
			continue
		}
		if !first {
			fmt.Fprintln(w, "\n---")
		}
		fmt.Fprintf(w, "# %s\n\n", key)
		fmt.Fprintln(w, strings.TrimRight(content, "\n"))
		first = false
	}

	return nil
}

// combineArtifacts concatenates all available artifact content into a single
// markdown document. Canonical order (§11.1): PRD body → architecture →
// requirements → test_spec → tasks.
func combineArtifacts(available map[string]string) string {
	var sb strings.Builder

	// PRD body (frontmatter already stripped by readAvailableArtifacts).
	if prd, ok := available["prd"]; ok {
		sb.WriteString("# prd\n\n")
		sb.WriteString(strings.TrimRight(prd, "\n"))
	}

	// Architecture (optional).
	if arch, ok := available["architecture"]; ok {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString("# architecture\n\n")
		sb.WriteString(strings.TrimRight(arch, "\n"))
	}

	// JSON artifacts in canonical order.
	for _, filename := range renderArtifactOrder {
		key := renderArtifactNames[filename]
		content, exists := available[key]
		if !exists {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(fmt.Sprintf("# %s\n\n", key))
		sb.WriteString(strings.TrimRight(content, "\n"))
	}

	return sb.String()
}
