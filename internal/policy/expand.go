package policy

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

// maxExpansion bounds what one word may expand to: its brace expansion makes
// at most this many words, and this many bytes in all. A line that asks for
// more is not read; it is refused, as the shell would run out of time on it.
const (
	maxExpansionWords = 4096
	maxExpansionBytes = 1 << 20
)

// wordBuf is a word being read: its text as the shell passes it, and for each
// byte whether it is an unquoted {, } or , that brace expansion acts on.
type wordBuf struct {
	text    []byte
	active  []bool
	touched bool // anything was written, an empty quoted string included
}

// put writes s. A quoted string, and anything an expansion wrote, is data.
func (w *wordBuf) put(s string, quoted bool) {
	w.touched = true
	for i := 0; i < len(s); i++ {
		w.text = append(w.text, s[i])
		w.active = append(w.active, !quoted && (s[i] == '{' || s[i] == '}' || s[i] == ','))
	}
}

// expand is the words the buffer stands for: itself, or what its brace
// expansion makes of it when braces is set. It reports false when that is more
// than can be judged.
func (w *wordBuf) expand(braces bool) ([]string, bool) {
	if !braces {
		return []string{string(w.text)}, true
	}
	wordsLeft, bytesLeft := maxExpansionWords, maxExpansionBytes
	return expandBraces(braceWord{string(w.text), w.active}, &wordsLeft, &bytesLeft)
}

// braceWord is text, and which of its bytes are braces or commas that expand.
type braceWord struct {
	s      string
	active []bool
}

func (w braceWord) slice(from, to int) braceWord {
	return braceWord{w.s[from:to], w.active[from:to]}
}

// expandBraces is bash's brace expansion of w: {a,b} and the sequences {1..3}
// and {a..c}, with the preamble and the postscript they come between. A brace
// without a comma or a sequence stays as it is.
func expandBraces(w braceWord, words, size *int) ([]string, bool) {
	for open := 0; open < len(w.s); open++ {
		if !w.active[open] || w.s[open] != '{' {
			continue
		}
		closing, commas := matchingBrace(w, open)
		if closing < 0 {
			continue
		}
		var alternatives []string
		pre, post := w.s[:open], w.slice(closing+1, len(w.s))
		if len(commas) > 0 {
			start := open + 1
			for _, c := range append(commas, closing) {
				inner, ok := expandBraces(w.slice(start, c), words, size)
				if !ok {
					return nil, false
				}
				alternatives = append(alternatives, inner...)
				start = c + 1
			}
		} else {
			seq, status := sequence(w.s[open+1 : closing])
			switch {
			case status == notSequence:
				continue
			case status == tooLong || len(seq) > *words:
				return nil, false
			}
			alternatives = seq
		}
		tails, ok := expandBraces(post, words, size)
		if !ok {
			return nil, false
		}
		var out []string
		for _, a := range alternatives {
			for _, tail := range tails {
				word := pre + a + tail
				*words--
				*size -= len(word) + 1
				if *words < 0 || *size < 0 {
					return nil, false
				}
				out = append(out, word)
			}
		}
		return out, true
	}
	return []string{w.s}, true
}

// matchingBrace finds the } that closes the { at open, and the commas at its
// own depth. It returns -1 when there is none.
func matchingBrace(w braceWord, open int) (closing int, commas []int) {
	depth := 0
	for i := open; i < len(w.s); i++ {
		if !w.active[i] {
			continue
		}
		switch w.s[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, commas
			}
		case ',':
			if depth == 1 {
				commas = append(commas, i)
			}
		}
	}
	return -1, nil
}

// sequenceStatus says what the inside of a brace is.
type sequenceStatus int

const (
	notSequence sequenceStatus = iota
	isSequence
	tooLong
)

// sequence is the words of {x..y} or {x..y..step}: integers, or letters.
func sequence(inner string) ([]string, sequenceStatus) {
	parts := strings.Split(inner, "..")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, notSequence
	}
	step := 1
	if len(parts) == 3 {
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			return nil, notSequence
		}
		if n < 0 {
			n = -n
		}
		if n != 0 {
			step = n
		}
	}
	from, errFrom := strconv.Atoi(parts[0])
	to, errTo := strconv.Atoi(parts[1])
	if errFrom == nil && errTo == nil {
		width := 0
		if padded(parts[0]) || padded(parts[1]) {
			width = max(len(parts[0]), len(parts[1]))
		}
		return count(from, to, step, func(n int) string {
			s := strconv.Itoa(n)
			if len(s) < width {
				neg := ""
				if n < 0 {
					neg, s = "-", s[1:]
				}
				s = neg + strings.Repeat("0", width-len(neg)-len(s)) + s
			}
			return s
		})
	}
	if len(parts[0]) == 1 && len(parts[1]) == 1 && isLetter(parts[0][0]) && isLetter(parts[1][0]) {
		return count(int(parts[0][0]), int(parts[1][0]), step, func(n int) string { return string(rune(n)) })
	}
	return nil, notSequence
}

// padded reports whether an integer is written with a leading zero.
func padded(s string) bool {
	s = strings.TrimPrefix(s, "-")
	return len(s) > 1 && s[0] == '0'
}

func isLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// count lists from to to, either way, by step; it gives up on a list longer
// than a word may expand to.
func count(from, to, step int, format func(int) string) ([]string, sequenceStatus) {
	span := to - from
	if span < 0 {
		span = -span
	}
	if span/step >= maxExpansionWords {
		return nil, tooLong
	}
	dir := 1
	if to < from {
		dir = -1
	}
	var out []string
	for n := from; (dir > 0 && n <= to) || (dir < 0 && n >= to); n += dir * step {
		out = append(out, format(n))
	}
	return out, isSequence
}

// ansiC decodes the body of a $'...' string: the escapes that bash reads.
// It returns the text, and the number of bytes of s that it took, up to and
// including the closing quote; the count is -1 when the string is not
// closed. s starts after the opening quote.
func ansiC(s string) (string, int) {
	var out []byte
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			if nul := bytes.IndexByte(out, 0); nul >= 0 {
				out = out[:nul]
			}
			return string(out), i + 1
		case c != '\\':
			out = append(out, c)
			i++
		case i+1 >= len(s):
			return "", -1
		default:
			var n int
			out, n = appendEscape(out, s[i+1:])
			i += 1 + n
		}
	}
	return "", -1
}

// appendEscape decodes the escape that starts s, which follows a backslash,
// and returns the bytes it takes of s.
func appendEscape(out []byte, s string) ([]byte, int) {
	switch e := s[0]; e {
	case 'a':
		return append(out, 7), 1
	case 'b':
		return append(out, 8), 1
	case 'e', 'E':
		return append(out, 27), 1
	case 'f':
		return append(out, 12), 1
	case 'n':
		return append(out, 10), 1
	case 'r':
		return append(out, 13), 1
	case 't':
		return append(out, 9), 1
	case 'v':
		return append(out, 11), 1
	case '\\', '\'', '"', '?':
		return append(out, e), 1
	case '0', '1', '2', '3', '4', '5', '6', '7':
		n := 1
		for n < len(s) && n < 3 && s[n] >= '0' && s[n] <= '7' {
			n++
		}
		v, _ := strconv.ParseUint(s[:n], 8, 16)
		return append(out, byte(v)), n
	case 'x':
		return hexEscape(out, s, 2, false)
	case 'u':
		return hexEscape(out, s, 4, true)
	case 'U':
		return hexEscape(out, s, 8, true)
	case 'c':
		if len(s) > 1 {
			return append(out, s[1]&0x1f), 2
		}
	}
	return append(out, '\\', s[0]), 1
}

// hexEscape decodes up to limit hex digits after the letter that starts s, as
// a byte or as a character. With no digit the escape is not one and stays as
// it is.
func hexEscape(out []byte, s string, limit int, character bool) ([]byte, int) {
	n := 1
	for n < len(s) && n <= limit && isHex(s[n]) {
		n++
	}
	if n == 1 {
		return append(out, '\\', s[0]), 1
	}
	v, _ := strconv.ParseUint(s[1:n], 16, 32)
	if character {
		return utf8.AppendRune(out, rune(v)), n
	}
	return append(out, byte(v)), n
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// putByte is put for one byte.
func (w *wordBuf) putByte(c byte, quoted bool) {
	w.touched = true
	w.text = append(w.text, c)
	w.active = append(w.active, !quoted && (c == '{' || c == '}' || c == ','))
}
