package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/spf13/cobra"
)

// renderArtifactOrder is the canonical order the individual render walks
// (§11.1): the PRD body, architecture when present, then the three artifacts.
var renderArtifactOrder = []string{"prd", "architecture", "requirements", "test_spec", "tasks"}

// jsonArtifactKeys names the three generated artifacts, as opposed to the two
// Markdown documents.
var jsonArtifactKeys = []string{"requirements", "test_spec", "tasks"}

// newRenderCmd creates the "spec render" subcommand, which renders a spec as
// Markdown through the library's renderer.
func newRenderCmd() *cobra.Command {
	var jsonOutput bool
	var combined bool
	var task int
	var maxTokens int

	cmd := &cobra.Command{
		Use:   "render SPEC",
		Short: "Render spec artifacts as Markdown or JSON",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			specDir, _ := cmd.Flags().GetString("spec-dir")

			// Auto-enable --json when AF_AGENT=1 is active.
			if isAgentMode() {
				jsonOutput = true
			}

			specPath, err := resolveSpec(specDir, args[0])
			if err != nil {
				return err
			}

			w := cmd.OutOrStdout()

			spec, loadErr := afspec.LoadSpec(specPath)
			if loadErr != nil {
				// A spec too incomplete to load is still worth showing: during
				// generation only some artifacts exist. Fall back to printing
				// the files that are there.
				return renderFallback(w, specPath, jsonOutput, combined)
			}
			if spec.IsScaffold() {
				return fmt.Errorf("spec %s has no generated artifacts yet; run `spec generate` first", args[0])
			}

			var opts []afspec.RenderOption
			if maxTokens > 0 {
				opts = append(opts, afspec.WithMaxTokens(maxTokens))
			}

			if combined {
				content := spec.RenderCombined(opts...)
				if jsonOutput {
					return emitOKTo(w, "format", "combined", "content", content)
				}
				_, err := fmt.Fprint(w, content)
				return err
			}

			var artifacts map[string]string
			if task > 0 {
				artifacts = spec.RenderIndividualScoped(task, opts...)
			} else {
				artifacts = spec.RenderIndividual(opts...)
			}

			if jsonOutput {
				return emitOKTo(w, "format", "individual", "artifacts", artifacts)
			}
			return writeIndividualMarkdown(w, artifacts)
		},
	}

	cmd.Flags().BoolVar(&jsonOutput, "json", false, "output as JSON envelope")
	cmd.Flags().BoolVar(&combined, "combined", false, "combine all artifacts into a single document")
	cmd.Flags().IntVar(&task, "task", 0, "scope the render to one task: its requirements and tests in full, the rest as one line each")
	cmd.Flags().IntVar(&maxTokens, "max-tokens", 0, "cap the estimated size of the output, truncating progressively")

	return cmd
}

// writeIndividualMarkdown prints each artifact under its own heading,
// separated by a rule.
func writeIndividualMarkdown(w interface{ Write([]byte) (int, error) }, artifacts map[string]string) error {
	first := true
	for _, key := range renderArtifactOrder {
		content, exists := artifacts[key]
		if !exists || strings.TrimSpace(content) == "" {
			continue
		}
		if !first {
			if _, err := fmt.Fprintln(w, "\n---"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "# %s\n\n", key); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w, strings.TrimRight(content, "\n")); err != nil {
			return err
		}
		first = false
	}
	return nil
}

// renderFallback prints whatever artifact files exist in a spec directory the
// library could not load. The content is the raw file, not a Markdown render:
// the renderer needs a decoded spec, and this path exists precisely because
// there is not one.
func renderFallback(w interface{ Write([]byte) (int, error) }, specPath string, jsonOutput, combined bool) error {
	available, err := readAvailableArtifacts(specPath)
	if err != nil {
		return err
	}

	present := 0
	for _, key := range jsonArtifactKeys {
		if _, ok := available[key]; ok {
			present++
		}
	}
	if present == 0 {
		return fmt.Errorf("no renderable artifacts found in %q", specPath)
	}

	if combined {
		var sb strings.Builder
		for _, key := range renderArtifactOrder {
			content, ok := available[key]
			if !ok {
				continue
			}
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString("# " + key + "\n\n")
			sb.WriteString(strings.TrimRight(content, "\n"))
		}
		if jsonOutput {
			return emitOKTo(w, "format", "combined", "content", sb.String())
		}
		_, err := fmt.Fprint(w, sb.String())
		return err
	}

	if jsonOutput {
		return emitOKTo(w, "format", "individual", "artifacts", available)
	}
	return writeIndividualMarkdown(w, available)
}

// readAvailableArtifacts reads the artifact files that are present in a spec
// directory, keyed by display name. It is used by the fallback above, which
// must work on a spec too broken for LoadSpec to read.
func readAvailableArtifacts(specPath string) (map[string]string, error) {
	available := make(map[string]string)

	files := map[string]string{
		"prd.md":            "prd",
		"architecture.md":   "architecture",
		"requirements.json": "requirements",
		"test_spec.json":    "test_spec",
		"tasks.json":        "tasks",
	}
	order := []string{"prd.md", "architecture.md", "requirements.json", "test_spec.json", "tasks.json"}

	for _, filename := range order {
		p := filepath.Join(specPath, filename)
		if _, statErr := os.Stat(p); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return nil, fmt.Errorf("cannot stat %s: %w", filename, statErr)
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return nil, fmt.Errorf("cannot read %s: %w", filename, readErr)
		}
		content := string(data)
		if filename == "prd.md" {
			content = stripFrontmatter(content)
		}
		available[files[filename]] = content
	}

	return available, nil
}

// stripFrontmatter removes YAML frontmatter from Markdown content. Frontmatter
// is a block delimited by "---" on its own line at the very start of the file.
// Content that does not begin with frontmatter is returned unchanged.
func stripFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	nl := strings.IndexByte(content, '\n')
	if nl < 0 {
		return content
	}
	rest := content[nl+1:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return content
	}
	body := rest[end+4:] // skip past "\n---"
	if len(body) > 0 && body[0] == '\n' {
		body = body[1:]
	} else if len(body) > 1 && body[0] == '\r' && body[1] == '\n' {
		body = body[2:]
	}
	return body
}
