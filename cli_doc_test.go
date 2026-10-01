package agentfox

import (
	"regexp"
	"strings"
	"testing"
)

// preflightExample returns the body of the "### `<tool> --preflight`"
// subsection of the Preflight section, up to the next heading.
func preflightExample(t *testing.T, section, tool string) string {
	t.Helper()
	heading := "### `" + tool + " --preflight`"
	lines := strings.Split(section, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, heading) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("Preflight section has no example headed %q", heading)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "#") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// TS-11-47 (unit): docs/cli.md contains a --preflight row in the shared-flags table
// Verifies: 11-REQ-8.1
func TestCLIDocTS11_47_SharedFlagsRow(t *testing.T) {
	doc := readDoc(t, "cli.md")
	shared := docSection(t, doc, "Shared flags")
	if !regexp.MustCompile("(?m)^\\|\\s*`--preflight`\\s*\\|").MatchString(shared) {
		t.Fatalf("shared-flags table has no --preflight row")
	}
	row, _ := tableRow(shared, "| `--preflight`")
	for _, want := range []string{"false", "check"} {
		if !strings.Contains(row, want) {
			t.Errorf("--preflight row does not mention %q: %s", want, row)
		}
	}
}

// TS-11-48 (unit): docs/cli.md contains a Preflight section naming what it
// runs, what it never does, and the unsuppressed behaviours
// Verifies: 11-REQ-8.2
func TestCLIDocTS11_48_PreflightSection(t *testing.T) {
	doc := readDoc(t, "cli.md")
	section := docSection(t, doc, "Preflight (`--preflight`)")
	for _, want := range []string{
		"no branch", "no commit", "no push", "no comment", "no issue",
		"baseline", "gate", "fast-forward", "continuation",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("Preflight section does not mention %q", want)
		}
	}
}

// TS-11-49 (unit): docs/cli.md contains one worked example per tool showing
// result.preflight and result.estimate
// Verifies: 11-REQ-8.3
func TestCLIDocTS11_49_WorkedExamples(t *testing.T) {
	doc := readDoc(t, "cli.md")
	section := docSection(t, doc, "Preflight (`--preflight`)")
	for _, tool := range []string{"fix", "impl", "spec", "issue"} {
		ex := preflightExample(t, section, tool)
		for _, want := range []string{`"preflight"`, `"estimate"`} {
			if !strings.Contains(ex, want) {
				t.Errorf("%s example does not show %s", tool, want)
			}
		}
	}
}
