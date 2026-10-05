package conform

import (
	"path"
	"strings"
)

// Scope is the set of paths a spec allows a change to touch.
type Scope struct {
	// Allow are the patterns: an exact path, a directory (ending in "/"), or
	// a path.Match glob.
	Allow []string
	// Exempt admits a path whatever the patterns say.
	Exempt func(string) bool
}

// Restricts reports whether the scope admits less than everything.
func (s Scope) Restricts() bool { return len(s.Allow) > 0 }

// Admits reports whether p may change.
func (s Scope) Admits(p string) bool {
	if !s.Restricts() || (s.Exempt != nil && s.Exempt(p)) {
		return true
	}
	for _, a := range s.Allow {
		a = strings.TrimPrefix(strings.TrimSpace(a), "./")
		switch {
		case a == "":
			continue
		case a == p:
			return true
		case strings.HasSuffix(a, "/") && strings.HasPrefix(p, a):
			return true
		case strings.HasSuffix(a, "/**") && strings.HasPrefix(p, strings.TrimSuffix(a, "**")):
			return true
		}
		if ok, err := path.Match(a, p); err == nil && ok {
			return true
		}
	}
	return false
}

// Outside lists the paths of changed the scope does not admit, in order.
func (s Scope) Outside(changed []string) []string {
	var out []string
	for _, p := range changed {
		if !s.Admits(p) {
			out = append(out, p)
		}
	}
	return out
}
