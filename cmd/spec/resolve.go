package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// resolveSpec resolves a spec argument (either a numeric prefix or an
// exact directory name) within the given spec directory. Returns the
// full path to the matched spec directory.
//
// If the argument is purely numeric, it matches entries whose directory
// name starts with that numeric prefix. Otherwise, it matches entries
// whose directory name equals the argument exactly.
//
// Returns an error if zero or multiple matches are found, or if the
// spec directory cannot be read.
func resolveSpec(specDir, arg string) (string, error) {
	entries, err := os.ReadDir(specDir)
	if err != nil {
		return "", fmt.Errorf("resolveSpec: cannot read spec directory %q: %w", specDir, err)
	}

	isNumeric := isPurelyNumeric(arg)
	var matches []string

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if isNumeric {
			// Match entries whose name starts with the numeric prefix followed
			// by an underscore, so "1" matches "1_foo" but not "10_bar".
			if strings.HasPrefix(name, arg+"_") {
				matches = append(matches, name)
			}
		} else {
			// Match by exact directory name.
			if name == arg {
				matches = append(matches, name)
			}
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("resolveSpec: no spec matching %q found in %q", arg, specDir)
	case 1:
		return filepath.Join(specDir, matches[0]), nil
	default:
		return "", fmt.Errorf("resolveSpec: ambiguous match for %q in %q: %s",
			arg, specDir, strings.Join(matches, ", "))
	}
}

// isPurelyNumeric returns true if all characters in s are digits and s is non-empty.
func isPurelyNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
