package agentfox

import (
	"strings"
	"testing"
)

// oneLine joins a wrapped passage into one line, so a phrase is found wherever
// the wrap fell.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// docs/cli.md says what `resumable` means for each tool the way the code
// computes it: a result at stage "preflight" is never resumable (11-REQ-6.4),
// including an ordinary run that refused there, because it wrote nothing.
func TestCLIDocResumableStatesThePreflightRule(t *testing.T) {
	doc := readDoc(t, "cli.md")
	i := strings.Index(doc, "`resumable` says whether re-running the *same input* continues")
	if i < 0 {
		t.Fatal("docs/cli.md has no sentence defining `resumable`")
	}
	sentence := oneLine(doc[i : i+strings.Index(doc[i:], "`fix_hint`")])
	for _, want := range []string{"preflight", "wrote nothing"} {
		if !strings.Contains(sentence, want) {
			t.Errorf("the `resumable` sentence does not mention %q:\n%s", want, sentence)
		}
	}
	if strings.Contains(sentence, "whenever a branch was named,") && !strings.Contains(sentence, "except") {
		t.Errorf("the `resumable` sentence states the rule without its preflight exception:\n%s", sentence)
	}
}

// The --context bound is described in the order the code checks it: before
// anything is fetched for text given as the argument, after the read for a
// file, an issue URL or stdin (internal/toolio/app.go).
func TestCLIDocContextBoundOrderMatchesTheCode(t *testing.T) {
	doc := readDoc(t, "cli.md")
	i := strings.Index(doc, "`--context` does not change `input.bytes`")
	if i < 0 {
		t.Fatal("docs/cli.md has no paragraph on --context and input.bytes")
	}
	para := oneLine(doc[i : i+strings.Index(doc[i:], "\n\n")])
	for _, want := range []string{"before anything is fetched", "a file, an issue URL or stdin", "after", "truncated"} {
		if !strings.Contains(para, want) {
			t.Errorf("the --context paragraph does not mention %q:\n%s", want, para)
		}
	}
}
