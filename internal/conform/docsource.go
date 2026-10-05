package conform

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/project"
)

// DocSource is where one fact written into the documentation comes from: the
// code or test line that shows it, and the text quoted from that line.
// Documentation copied from the code describes the code; documentation copied
// from the PRD describes what was asked for, which is how a doc comes to
// promise an exit code the program does not return.
type DocSource struct {
	// Claim is the fact as the documentation states it.
	Claim string `json:"claim" trust:"model" description:"The fact as the documentation states it."`
	// Source is the code or test line it was copied from, as file:line.
	Source string `json:"source" trust:"model" description:"The code or test line it was copied from, as file:line."`
	// Quote is the text on that line that shows it.
	Quote string `json:"quote" trust:"model" description:"The text on that line that shows it."`
}

// docSourceSlack is how far from the cited line the quote may sit: a
// multi-line literal or a wrapped call is cited by its first line.
const docSourceSlack = 3

// VerifyDocSource checks a doc source against the repository: the file is
// code or a test, not documentation; the line exists; and the quote is on it
// or within a few lines of it.
func VerifyDocSource(root string, s DocSource) error {
	m := citationRe.FindStringSubmatch(s.Source)
	if m == nil {
		return fmt.Errorf("source %q is not a file:line", s.Source)
	}
	rel := filepath.ToSlash(filepath.Clean(strings.TrimPrefix(m[1], "./")))
	if filepath.IsAbs(rel) || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("source %q is outside the repository", s.Source)
	}
	if project.IsDocsFile(rel) {
		return fmt.Errorf("source %q is documentation; a fact in the docs is copied from the code or "+
			"a test, not from other prose", s.Source)
	}
	quote := strings.TrimSpace(s.Quote)
	if len([]rune(quote)) < 3 {
		return fmt.Errorf("the quote for %q is empty; copy the text from the line", s.Claim)
	}
	n, _ := strconv.Atoi(m[2])
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("source %q does not exist", s.Source)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	found, last := false, 0
	for i := 1; sc.Scan(); i++ {
		last = i
		if i >= n-docSourceSlack && i <= n+docSourceSlack && strings.Contains(sc.Text(), quote) {
			found = true
		}
		if i > n+docSourceSlack {
			break
		}
	}
	if last < n {
		return fmt.Errorf("source %q has no line %d", s.Source, n)
	}
	if !found {
		return fmt.Errorf("%q is not on %s or the lines around it; quote the code exactly", quote, s.Source)
	}
	return nil
}
