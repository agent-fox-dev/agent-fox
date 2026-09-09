package afspec

import (
	"fmt"
	"strings"

	"github.com/goccy/go-yaml"
)

// prdFrontmatter is the internal representation used for YAML parsing.
// It uses go-yaml struct tags for parsing and maps to the Spec struct fields.
type prdFrontmatter struct {
	SpecID        string   `yaml:"spec_id"`
	SpecName      string   `yaml:"spec_name"`
	Title         string   `yaml:"title"`
	Status        string   `yaml:"status"`
	CreatedAt     string   `yaml:"created_at"`
	UpdatedAt     string   `yaml:"updated_at"`
	Owner         string   `yaml:"owner"`
	Source        string   `yaml:"source"`
	Supersedes    []string `yaml:"supersedes"`
	Tags          []string `yaml:"tags"`
	IntentHash    *string  `yaml:"intent_hash"`
	SchemaVersion int      `yaml:"schema_version"`
}

// parsePRD splits a prd.md file into its YAML frontmatter and Markdown body.
// The frontmatter is parsed into the prdFrontmatter struct using go-yaml.
// Returns a LoadError wrapping SpecError on any parsing failure.
func parsePRD(data []byte, filePath string) (*prdFrontmatter, string, error) {
	content := string(data)

	// Must start with ---
	if !strings.HasPrefix(content, "---") {
		return nil, "", &LoadError{
			Msg:  "prd.md missing opening --- delimiter",
			File: filePath,
			Err:  &SpecError{Msg: "prd.md missing opening --- delimiter"},
		}
	}

	// Find closing --- delimiter
	// Skip the first line (which is "---")
	rest := content[3:]
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}

	closingIdx := strings.Index(rest, "\n---\n")
	var frontmatterStr string
	var body string

	if closingIdx < 0 {
		// Check for closing --- at end of file (no trailing newline)
		if strings.HasSuffix(rest, "\n---") {
			closingIdx = len(rest) - 4
			frontmatterStr = rest[:closingIdx]
			body = ""
		} else {
			// Missing closing delimiter — match Python behavior: treat rest as frontmatter
			return nil, "", &LoadError{
				Msg:  "prd.md missing closing --- delimiter",
				File: filePath,
				Err:  &SpecError{Msg: "prd.md missing closing --- delimiter"},
			}
		}
	} else {
		frontmatterStr = rest[:closingIdx]
		body = rest[closingIdx+5:] // skip "\n---\n"
	}

	// Parse YAML frontmatter
	var fm prdFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatterStr), &fm); err != nil {
		return nil, "", &LoadError{
			Msg:  fmt.Sprintf("failed to parse YAML frontmatter: %s", err),
			File: filePath,
			Err:  &SpecError{Msg: fmt.Sprintf("failed to parse YAML frontmatter: %s", err)},
		}
	}

	// Validate required fields
	if fm.SpecID == "" {
		return nil, "", &LoadError{
			Msg:  "prd.md frontmatter missing required field: spec_id",
			File: filePath,
			Err:  &SpecError{Msg: "prd.md frontmatter missing required field: spec_id"},
		}
	}
	if fm.SpecName == "" {
		return nil, "", &LoadError{
			Msg:  "prd.md frontmatter missing required field: spec_name",
			File: filePath,
			Err:  &SpecError{Msg: "prd.md frontmatter missing required field: spec_name"},
		}
	}
	if fm.Status == "" {
		return nil, "", &LoadError{
			Msg:  "prd.md frontmatter missing required field: status",
			File: filePath,
			Err:  &SpecError{Msg: "prd.md frontmatter missing required field: status"},
		}
	}

	// Ensure slices are non-nil (match Python behavior where empty lists are [])
	if fm.Supersedes == nil {
		fm.Supersedes = []string{}
	}
	if fm.Tags == nil {
		fm.Tags = []string{}
	}

	return &fm, body, nil
}

// renderPRD renders a Spec back to prd.md format using a hand-written
// renderer with fixed field order matching Python's _render_prd().
// This function does NOT use yaml.Marshal to ensure byte-for-byte
// fidelity with the Python library's output.
func renderPRD(s *Spec) []byte {
	var b strings.Builder

	b.WriteString("---\n")

	// Fixed field order matching Python's _FRONTMATTER_FIELDS
	b.WriteString("spec_id: ")
	b.WriteString(renderYAMLString(s.SpecID))
	b.WriteByte('\n')

	b.WriteString("spec_name: ")
	b.WriteString(renderYAMLString(s.SpecName))
	b.WriteByte('\n')

	b.WriteString("title: ")
	b.WriteString(renderYAMLString(s.Title))
	b.WriteByte('\n')

	b.WriteString("status: ")
	b.WriteString(renderYAMLString(s.Status))
	b.WriteByte('\n')

	b.WriteString("created_at: ")
	b.WriteString(renderYAMLString(s.CreatedAt))
	b.WriteByte('\n')

	b.WriteString("updated_at: ")
	b.WriteString(renderYAMLString(s.UpdatedAt))
	b.WriteByte('\n')

	b.WriteString("owner: ")
	b.WriteString(renderYAMLString(s.Owner))
	b.WriteByte('\n')

	b.WriteString("source: ")
	b.WriteString(renderYAMLString(s.Source))
	b.WriteByte('\n')

	b.WriteString("supersedes: ")
	b.WriteString(renderYAMLList(s.Supersedes))
	b.WriteByte('\n')

	b.WriteString("tags: ")
	b.WriteString(renderYAMLList(s.Tags))
	b.WriteByte('\n')

	b.WriteString("intent_hash: ")
	b.WriteString(renderYAMLNullableString(s.IntentHash))
	b.WriteByte('\n')

	b.WriteString("schema_version: ")
	b.WriteString(fmt.Sprintf("%d", s.SchemaVersion))
	b.WriteByte('\n')

	b.WriteString("---\n")
	b.WriteString(s.PRDBody)

	return []byte(b.String())
}

// renderYAMLString renders a Go string as a double-quoted YAML value.
func renderYAMLString(s string) string {
	return fmt.Sprintf("%q", s)
}

// renderYAMLList renders a Go string slice as an inline YAML list.
// Empty slices are rendered as []. Non-empty slices are rendered as
// ["item1", "item2"].
func renderYAMLList(items []string) string {
	if len(items) == 0 {
		return "[]"
	}
	quoted := make([]string, len(items))
	for i, item := range items {
		quoted[i] = fmt.Sprintf("%q", item)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

// renderYAMLNullableString renders a nullable string. nil is rendered as "null",
// non-nil is rendered as a double-quoted string.
func renderYAMLNullableString(s *string) string {
	if s == nil {
		return "null"
	}
	return fmt.Sprintf("%q", *s)
}
