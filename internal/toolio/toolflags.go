package toolio

import (
	"fmt"
	"strings"
)

// notDefinedPrefix is the start of the error the flag package returns for a
// flag the flag set does not define.
const notDefinedPrefix = "flag provided but not defined: -"

// toolOrder is the order the accepting tools are named in.
var toolOrder = []string{"triage", "fix", "spec", "impl"}

// toolFlags is each of the four tools' own flags — the ones registered in its
// Flags func, beside Common's. It exists so that a flag given to a tool that
// does not use it can be reported as another tool's flag rather than as an
// unknown one. The shared flags (Common) are not listed: every tool has them.
//
// It is deliberately a static table and not derived from the tools' flag sets:
// each tool keeps registering only what its own Exec uses, and a tool's
// package does not know about the others'. A test in each cmd package keeps
// its row honest.
var toolFlags = map[string][]string{
	"triage": {"repo", "label", "overwrite"},
	"fix": {"repo", "land", "verify", "no-verify", "verify-timeout", "push-attempts",
		"allow", "draft", "pull", "branch-prefix", "no-review"},
	"spec": {"specs-dir", "name", "architecture", "no-activate", "comment"},
	"impl": {"specs-dir", "task", "branch", "repo", "land", "verify", "no-verify",
		"verify-timeout", "push-attempts", "allow", "draft", "pull", "no-survey",
		"no-test-first", "no-review", "max-func-lines", "task-attempts", "repair", "repair-attempts", "repair-model",
		"repair-model-effort"},
}

// ToolFlags returns the names of tool's own flags (those beside Common's), for
// a test that checks the table against the tool's real flag set.
func ToolFlags(tool string) []string {
	return append([]string(nil), toolFlags[tool]...)
}

// removedFlagMessages maps flag names that were removed to the message a
// caller passing them should see.
var removedFlagMessages = map[string]string{
	"events-file": "--events-file was removed; events are always written to <state>/events/",
	"events":      "--events was renamed --emit-events",
	"variant":     "--variant was removed; use --effort to set reasoning effort and --model to name a specific model",
}

// unsupportedFlagMessage rewrites a "flag provided but not defined" parse
// error into one that names the flag and the tool or tools that do accept it,
// or into a removal/rename message for --events and --events-file.
// It reports false — leaving Go's own message to stand — for any other error,
// and for a flag that none of the other tools defines either.
func unsupportedFlagMessage(tool string, err error) (string, bool) {
	if err == nil {
		return "", false
	}
	name, ok := strings.CutPrefix(err.Error(), notDefinedPrefix)
	if !ok || name == "" {
		return "", false
	}
	// Check for removed flags first.
	if msg, found := removedFlagMessages[name]; found {
		return msg, true
	}
	var accepting []string
	for _, other := range toolOrder {
		if other == tool {
			continue
		}
		for _, f := range toolFlags[other] {
			if f == name {
				accepting = append(accepting, other)
				break
			}
		}
	}
	switch len(accepting) {
	case 0:
		return "", false
	case 1:
		return fmt.Sprintf("%s does not accept --%s; it is %s %s flag",
			tool, name, article(accepting[0]), accepting[0]), true
	default:
		return fmt.Sprintf("%s does not accept --%s; it is a flag of %s",
			tool, name, strings.Join(accepting[:len(accepting)-1], ", ")+" and "+accepting[len(accepting)-1]), true
	}
}

// article is "an" before a tool name that starts with a vowel.
func article(tool string) string {
	if strings.ContainsAny(tool[:1], "aeiou") {
		return "an"
	}
	return "a"
}
