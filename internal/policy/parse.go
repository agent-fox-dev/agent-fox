package policy

import (
	"fmt"
	"strings"

	"github.com/agent-fox-dev/agentkit-go/guard"
)

// maxDepth is how deeply the scripts of shells, eval and trap may nest. A line
// that nests further is not read; it is refused.
const maxDepth = 64

// redirect is one redirection of a simple command.
type redirect struct {
	fd     string // the descriptor written in front of the operator, or ""
	op     string // <, >, >>, >|, <>, <&, >&, &>, &>>, <<, <<- or <<<
	target string // the word after the operator, unquoted
}

// command is one simple command of a shell line.
type command struct {
	text      string   // as written, trimmed
	words     []string // the words with quotes and escapes removed, assignments included
	redirects []redirect
}

// parse reads a shell command line into every simple command it runs, with the
// hands' reader, guard.Commands: the ones separated by &&, ||, ;, |, & and
// newlines, the ones in groups and in command and process substitutions, and
// the ones in the bodies of heredocs that expand them. A line it cannot read
// with certainty is an error.
//
// The hands' reader leaves an expansion as it was written. parse expands the
// ones that can make a plain word of it, as the shell would: $'…' and $"…"
// quotes, an unquoted $IFS, which splits a word, and braces. It cannot tell
// which of them were quoted, so it expands them wherever they are: a word may
// be read as more than the shell makes of it, which can only deny more. An
// assignment in front of a program is its name and an =; its value is not read.
func parse(src string) ([]command, error) {
	cmds, err := guard.Commands(src)
	if err != nil {
		return nil, err
	}
	out := make([]command, 0, len(cmds))
	for _, c := range cmds {
		cmd := command{text: c.Text}
		for _, name := range c.Assign {
			cmd.words = append(cmd.words, name+"=")
		}
		for _, w := range c.Argv {
			words, err := shellWords(w)
			if err != nil {
				return nil, err
			}
			cmd.words = append(cmd.words, words...)
		}
		for _, r := range c.Redirects {
			targets := []string{r.Target}
			if r.Op != "<<" && r.Op != "<<-" {
				// A brace expansion that makes several words is an ambiguous
				// redirect to the shell; every one of them is judged.
				if targets, err = shellWords(r.Target); err != nil {
					return nil, err
				}
			}
			for _, t := range targets {
				cmd.redirects = append(cmd.redirects, redirect{fd: r.FD, op: r.Op, target: t})
			}
		}
		out = append(out, cmd)
	}
	return out, nil
}

// shellWords is the words the shell makes of a word as the hands' reader gives
// it: split at $IFS, with $'…' and $"…" read, and braces expanded. A word that
// is only $IFS is no word.
func shellWords(w string) ([]string, error) {
	var out []string
	for _, piece := range splitIFS(w) {
		bw, err := readQuotes(piece)
		if err != nil {
			return nil, err
		}
		wordsLeft, bytesLeft := maxExpansionWords, maxExpansionBytes
		words, ok := expandBraces(bw, &wordsLeft, &bytesLeft)
		if !ok {
			return nil, fmt.Errorf("a brace expansion that makes too many words in %q", w)
		}
		out = append(out, words...)
	}
	return out, nil
}

// splitIFS splits a word at each $IFS and ${IFS}: the shell splits there, so
// git${IFS}push is git and push. A word with none is itself.
func splitIFS(w string) []string {
	var out []string
	start, split := 0, false
	for i := 0; i < len(w); {
		if n := ifsLength(w[i:]); n > 0 {
			if i > start {
				out = append(out, w[start:i])
			}
			i += n
			start, split = i, true
			continue
		}
		i++
	}
	if start < len(w) || !split {
		out = append(out, w[start:])
	}
	return out
}

// ifsLength is the length of $IFS or ${IFS} at the start of s, or 0. A $IFS
// that a name goes on from is taken as one too: the hands' reader has removed
// the quotes of git$IFS'push', and $IFSx is an empty variable in front of x.
func ifsLength(s string) int {
	switch {
	case strings.HasPrefix(s, "${IFS}"):
		return len("${IFS}")
	case strings.HasPrefix(s, "$IFS"):
		return len("$IFS")
	}
	return 0
}

// readQuotes reads the $'…' and $"…" strings of a word into what they stand
// for, and marks the braces and commas that brace expansion acts on: those
// outside the strings and outside the expansions ${…}, $(…) and `…`.
func readQuotes(w string) (braceWord, error) {
	var text []byte
	var active []bool
	put := func(s string, expands bool) {
		for i := 0; i < len(s); i++ {
			text = append(text, s[i])
			active = append(active, expands && (s[i] == '{' || s[i] == '}' || s[i] == ','))
		}
	}
	for i := 0; i < len(w); {
		switch {
		case strings.HasPrefix(w[i:], "$'"):
			text, n := ansiC(w[i+2:])
			if n < 0 {
				return braceWord{}, fmt.Errorf("unterminated $' quote in %q", w)
			}
			put(text, false)
			i += 2 + n
		case strings.HasPrefix(w[i:], `$"`):
			end := closing(w, i+2, '"')
			put(w[i+2:end], false)
			i = min(end+1, len(w))
		case strings.HasPrefix(w[i:], "${"):
			end := closing(w, i+2, '}')
			put(w[i:min(end+1, len(w))], false)
			i = min(end+1, len(w))
		case strings.HasPrefix(w[i:], "$("):
			end := closing(w, i+2, ')')
			put(w[i:min(end+1, len(w))], false)
			i = min(end+1, len(w))
		case w[i] == '`':
			end := closing(w, i+1, '`')
			put(w[i:min(end+1, len(w))], false)
			i = min(end+1, len(w))
		default:
			put(w[i:i+1], true)
			i++
		}
	}
	return braceWord{string(text), active}, nil
}

// closing is the index of the end that closes what opened before from, with
// the brackets of the same kind nested in it counted, or len(w) when there is
// none.
func closing(w string, from int, end byte) int {
	open := map[byte]byte{'}': '{', ')': '('}[end]
	depth := 0
	for i := from; i < len(w); i++ {
		switch c := w[i]; {
		case c == '\\':
			i++
		case open != 0 && c == open:
			depth++
		case c == end && depth == 0:
			return i
		case c == end:
			depth--
		}
	}
	return len(w)
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
