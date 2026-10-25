package policy

import (
	"testing"
)

// dir is the checkout the tests decide in; it need not exist.
const dir = "/work/repo"

// defaultPolicy is the policy of a Go project with a Makefile: its suite, the
// default protected paths and no allowlist of programs.
func defaultPolicy() Policy {
	return Policy{
		Suite:          [][]string{{"make", "check"}, {"make", "test"}, {"go", "test", "./..."}, {"go", "vet", "./..."}},
		ProtectedPaths: DefaultProtectedPaths(),
	}
}

// bash decides a shell call of command in dir.
func bash(p Policy, command string) Decision {
	return Decide(p, dir, "execute", map[string]any{"command": command})
}

// write decides a write_file call of path in dir.
func write(p Policy, path string) Decision {
	return Decide(p, dir, "write_file", map[string]any{"path": path, "content": "x"})
}

// words are the words of every command parse finds in src, in order.
func words(t *testing.T, src string) [][]string {
	t.Helper()
	cmds, err := parse(src)
	if err != nil {
		t.Fatalf("parse(%q): %v", src, err)
	}
	out := [][]string{}
	for _, c := range cmds {
		out = append(out, c.words)
	}
	return out
}

// expectation is how a command line is to be decided.
type expectation struct {
	command  string
	decision string
	rule     string
	segment  string // "" when the test does not look at it
}

// denied and allowed are the expectations of a denial by a rule and of an allowance.
func denied(command, rule string) expectation { return expectation{command, Deny, rule, ""} }
func allowed(command string) expectation      { return expectation{command, Allow, "", ""} }

// requireDecisions decides each command under p, compares, and checks the
// shape of the decision: a denial has a rule, a segment and a reason, an
// allowance none of them.
func requireDecisions(t *testing.T, p Policy, cases []expectation) {
	t.Helper()
	for _, tc := range cases {
		d := bash(p, tc.command)
		if d.Decision != tc.decision || d.Rule != tc.rule || (tc.segment != "" && d.Segment != tc.segment) {
			t.Errorf("%q: %s by %q on %q (%s), want %s by %q on %q", tc.command, d.Decision, d.Rule, d.Segment, d.Reason, tc.decision, tc.rule, tc.segment)
		}
		requireShape(t, tc.command, d)
	}
}

// requireShape fails the test unless d is a denial with a rule, a segment and
// a reason, or an allowance with none of them.
func requireShape(t *testing.T, what string, d Decision) {
	t.Helper()
	switch {
	case d.Decision == Deny && (d.Rule == "" || d.Segment == "" || d.Reason == ""):
		t.Errorf("%q: a denial without a rule, a segment or a reason: %+v", what, d)
	case d.Decision == Allow && d != (Decision{Decision: Allow}):
		t.Errorf("%q: an allowance that carries %+v", what, d)
	case d.Decision != Allow && d.Decision != Deny:
		t.Errorf("%q: decision %q", what, d.Decision)
	}
}

// stubHands puts f in the place of the hands' guard for the test.
func stubHands(t *testing.T, f func(string) (string, bool)) {
	t.Helper()
	old := handsGuard
	handsGuard = f
	t.Cleanup(func() { handsGuard = old })
}
