// Package skills embeds the skills that af itself uses: the af-* entries of
// this directory, each a Markdown file with YAML frontmatter. The Makefile's
// install-skills copies the same files to ~/.claude/skills for a coding agent;
// the binary reads them from here, so there is one source and no copy to drift.
package skills

import (
	"embed"
	"strings"
)

// FS holds the af-* skills.
//
//go:embed af-*
var FS embed.FS

// Triage is the body of the af-triage skill without its frontmatter, which the
// brief of a triage run carries.
func Triage() string {
	data, err := FS.ReadFile("af-triage")
	if err != nil {
		// The file is embedded at build time; a missing one is a mistake in the
		// build, which the tests find.
		return ""
	}
	return body(string(data))
}

// body is a skill without its frontmatter: the lines between the first two
// lines of three dashes, and the blank line after them.
func body(skill string) string {
	rest, ok := strings.CutPrefix(skill, "---\n")
	if !ok {
		return skill
	}
	_, after, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return skill
	}
	return after
}
