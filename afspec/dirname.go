package afspec

import (
	"fmt"
	"regexp"
	"strings"
)

// specDirPattern matches the NN_snake_case pattern: two or more digits,
// an underscore, then a lowercase letter followed by zero or more lowercase
// letters, digits, or underscores. Double underscores, trailing underscores,
// uppercase, and hyphens are rejected by the character class rules.
// specNameExpr is the spec-name rule: lowercase words of letters and digits,
// the first starting with a letter, joined by single underscores. The
// directory rule is built from it, so the two cannot drift apart.
const specNameExpr = `[a-z][a-z0-9]*(?:_[a-z0-9]+)*`

var (
	specDirPattern  = regexp.MustCompile(`^[0-9]{2,}_` + specNameExpr + `$`)
	specNamePattern = regexp.MustCompile(`^` + specNameExpr + `$`)
)

// IsSpecDirName validates whether a directory name matches the
// NN_snake_case pattern (two-or-more-digit numeric prefix, underscore, then
// one or more snake_case segments).
func IsSpecDirName(name string) bool {
	return specDirPattern.MatchString(name)
}

// ParseSpecDirName parses a valid NN_snake_case directory name and
// returns the numeric prefix and the snake_case name portion as
// separate values.
//
// Returns an error if the name does not match the NN_snake_case pattern.
func ParseSpecDirName(name string) (string, string, error) {
	if !IsSpecDirName(name) {
		return "", "", fmt.Errorf("not a valid spec directory name: %q", name)
	}
	num, rest, _ := strings.Cut(name, "_")
	return num, rest, nil
}

// IsSpecName reports whether s is a valid spec name: exactly the part of a
// spec directory name after the numeric prefix, so a name that passes it
// always makes a valid directory.
func IsSpecName(s string) bool { return specNamePattern.MatchString(s) }
