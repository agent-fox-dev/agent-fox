package project

import "strings"

// makefileNames are the names make looks for, in the order it prefers them.
var makefileNames = []string{"GNUmakefile", "makefile", "Makefile"}

// makefile is the Makefile at the root of a repository.
type makefile struct {
	file    string // its name; empty when there is none
	targets map[string]bool
}

// has reports whether the Makefile has a rule for target.
func (m makefile) has(target string) bool { return m.targets[target] }

// gateChecks are the checks the Makefile gives the gate: make check when it
// has that target, otherwise make lint (when it has that) and make test.
func (m makefile) gateChecks() []Check {
	rule := func(target, kind string) Check {
		return Check{
			Name:   "make " + target,
			Argv:   []string{"make", target},
			Kind:   kind,
			Source: m.file + ":" + target,
		}
	}
	switch {
	case m.has("check"):
		return []Check{rule("check", KindCheck)}
	case m.has("test"):
		var checks []Check
		if m.has("lint") {
			checks = append(checks, rule("lint", KindLint))
		}
		return append(checks, rule("test", KindTest))
	}
	return nil
}

// readMakefile reads the Makefile among the root files of r.
func readMakefile(r repo) makefile {
	for _, name := range makefileNames {
		if r.root[name] {
			return makefile{file: name, targets: parseTargets(r.read(name))}
		}
	}
	return makefile{}
}

// parseTargets lists the targets that the rules of a Makefile name. It reads
// what is needed to find a rule and no more: assignments, recipes, special
// targets (.PHONY), pattern rules and the bodies of define blocks are not
// rules, and make is never run.
func parseTargets(text string) map[string]bool {
	targets := map[string]bool{}
	continued := false
	defines := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		wasContinued := continued
		continued = strings.HasSuffix(line, `\`)
		trimmed := strings.TrimSpace(line)
		switch {
		case wasContinued:
			continue
		case defines > 0:
			if isDirective(trimmed, "define") {
				defines++
			} else if isDirective(trimmed, "endef") {
				defines--
			}
			continue
		case isDirective(trimmed, "define"):
			defines++
			continue
		case strings.HasPrefix(line, "\t"), trimmed == "", strings.HasPrefix(trimmed, "#"):
			continue
		}

		colon := strings.IndexByte(trimmed, ':')
		if colon < 0 || isAssignment(trimmed, colon) {
			continue
		}
		for _, name := range strings.Fields(trimmed[:colon]) {
			if !strings.HasPrefix(name, ".") && !strings.ContainsAny(name, "%$()") {
				targets[name] = true
			}
		}
	}
	return targets
}

// isDirective reports whether line starts with the directive word.
func isDirective(line, word string) bool {
	return line == word || strings.HasPrefix(line, word+" ") || strings.HasPrefix(line, word+"\t")
}

// isAssignment reports whether the line, whose first colon is at colon, sets
// a variable rather than states a rule: VAR = x:y, VAR := x, VAR ::= x. A rule
// that sets a variable for one target (check: FLAGS=-v) is still a rule.
func isAssignment(line string, colon int) bool {
	if eq := strings.IndexByte(line, '='); eq >= 0 && eq < colon {
		return true
	}
	return strings.HasPrefix(strings.TrimLeft(line[colon:], ":"), "=")
}
