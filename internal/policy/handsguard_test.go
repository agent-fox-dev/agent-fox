package policy

import (
	"strings"
	"testing"
)

// TestHandsGuard_RefusesWhatOnlyItKnows: the real adapter refuses a variable
// that changes which program runs, an unreadable line and a line that runs no
// program, and lets the rest through to the rules.
func TestHandsGuard_RefusesWhatOnlyItKnows(t *testing.T) {
	for _, command := range []string{"ls -l", "FOO=1 ls", "go test ./... -run X", "echo $(date)", "cat <<EOF\nbody\nEOF", "./run.sh --flag", "a && b || c"} {
		if reason, refused := handsGuard(command); refused {
			t.Errorf("the hands' guard refuses %q: %s", command, reason)
		}
	}
	for command, mention := range map[string]string{
		"PATH=/tmp/evil ls":          "PATH",
		"FOO=1 LD_PRELOAD=x ls":      "LD_PRELOAD",
		"HOME=/tmp ls":               "HOME",
		"BASH_ENV=/tmp/x bash -c ls": "BASH_ENV",
		`echo "unterminated`:         "",
		"":                           "no program",
	} {
		if reason, refused := handsGuard(command); !refused || reason == "" || !strings.Contains(reason, mention) {
			t.Errorf("handsGuard(%q) = %q, %v; want a refusal that mentions %q", command, reason, refused, mention)
		}
	}
	// Through Decide, an assignment on its own runs nothing.
	requireDecisions(t, defaultPolicy(), []expectation{{"FOO=1", Deny, RuleHandsGuard, "FOO=1"}, denied("PATH=/x ls", RuleHandsGuard)})
}

// TestHandsGuard_IsAFloorThatOnlyDenies: its refusal is the denial, with its
// reason, after the line is read and before the rules; what it allows the
// rules still judge.
func TestHandsGuard_IsAFloorThatOnlyDenies(t *testing.T) {
	const reason = "guard.Restricted: the reason of the hands"
	stubHands(t, func(command string) (string, bool) {
		return reason, strings.Contains(command, "rm -rf /")
	})

	if d := bash(defaultPolicy(), "rm -rf /"); d.Decision != Deny || d.Rule != RuleHandsGuard || d.Reason != reason || d.Segment != "rm -rf /" {
		t.Errorf("rm -rf /: %+v, want a denial by hands_guard with exactly the hands' reason", d)
	}
	requireDecisions(t, defaultPolicy(), []expectation{
		denied("git push", RuleGitMutation),
		denied("make check", RuleSuiteCommand),
		allowed("ls"),
		denied("git push; rm -rf /", RuleHandsGuard),      // before the rules
		denied(`rm -rf / "unterminated`, RuleUnparseable), // after the reading
	})
}
