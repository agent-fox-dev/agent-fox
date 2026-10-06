package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-17-20 (unit): the documentation says what the system prompt tells the
// model about its tools: the rendered guidelines, the built-in file tools for
// reading and searching, and the shell for git and building and testing.
//
// Verifies: 17-REQ-8.1
func TestTS17_20_DocsDescribeTheToolGuidance(t *testing.T) {
	root := findWorkspaceRoot(t)
	cli, err := os.ReadFile(filepath.Join(root, "docs", "cli.md"))
	if err != nil {
		t.Fatal(err)
	}
	usage, err := os.ReadFile(filepath.Join(root, "docs", "model-usage.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"docs/cli.md (What the model may and may not do)": subsection(t, string(cli), "### What the model may and may not do"),
		"docs/model-usage.md":                             string(usage),
	} {
		for _, want := range []string{"Tool guidelines", "search_files", "only for git"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s lacks %q", name, want)
			}
		}
		if !strings.Contains(text, "building") || !strings.Contains(text, "testing") {
			t.Errorf("%s does not say the shell is for building and testing", name)
		}
	}
}

// subsection is the first section of doc whose heading line is heading, up to
// the next heading of the same or a higher level.
func subsection(t *testing.T, doc, heading string) string {
	t.Helper()
	level := strings.Index(heading, " ")
	lines := strings.Split(doc, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) != heading {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if k := strings.Index(lines[j], " "); k > 0 && k <= level && strings.Trim(lines[j][:k], "#") == "" {
				end = j
				break
			}
		}
		return strings.Join(lines[i:end], "\n")
	}
	t.Fatalf("no section %q", heading)
	return ""
}
