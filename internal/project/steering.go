package project

import (
	"os"
	"path/filepath"
	"strings"
)

// steeringPlaceholder marks a steering file that has nothing to say yet.
const steeringPlaceholder = "<!-- steering:placeholder -->"

// maxSteeringBytes bounds the steering file. A document that size is not a
// preamble, and inlining it would crowd out the code a phase has to read.
const maxSteeringBytes = 24 << 10

// Steering reads <specsDir>/steering.md, the project-level directives to every
// agent working on the repository. A file that is missing, a symlink, empty,
// over the size bound, or that holds only the placeholder or the bare
// heading is nothing, and the result is "".
func Steering(specsDir string) string {
	path := filepath.Join(specsDir, "steering.md")
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 || len(b) > maxSteeringBytes {
		return ""
	}
	s := string(b)
	if strings.Contains(s, steeringPlaceholder) {
		return ""
	}
	if strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "# Steering")) == "" {
		return ""
	}
	return s
}
