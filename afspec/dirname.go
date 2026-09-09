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
var specDirPattern = regexp.MustCompile(`^[0-9]{2,}_[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

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
