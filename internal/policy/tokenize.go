package policy

import (
	"fmt"
	"strings"
)

// maxDepth is how deeply substitutions, groups and the scripts of shells may
// nest. A line that nests further is not read; it is refused.
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

// parseError is a line that cannot be read with certainty.
type parseError struct {
	msg string
	pos int
}

func (e *parseError) Error() string { return fmt.Sprintf("%s (at byte %d)", e.msg, e.pos) }

// parseAt reads a shell command line into every simple command it runs: the
// ones separated by &&, ||, ;, |, & and newlines, the ones in groups and in
// command and process substitutions, and the ones in the bodies of heredocs
// that expand them. A command's words are those the shell would pass, with
// quotes and escapes removed; an expansion is left as it was written, so that
// a word the shell computes is never spelled like a plain program name.
//
// It reads the grammar and does not run it: aliases, functions and variables
// are not resolved, and the keywords of compound commands are words like any
// other. A line it cannot read with certainty is an error. The line is nested
// depth levels deep in scripts.
func parseAt(src string, depth int) ([]command, error) {
	p := &parser{src: src, depth: depth}
	cmds, err := p.list(0)
	if err != nil {
		return nil, err
	}
	return cmds, nil
}

// heredoc is a heredoc whose body starts at the next newline.
type heredoc struct {
	delim  string
	strip  bool // <<-: the tabs in front of a line are not part of it
	quoted bool // the delimiter was quoted: the body is data
}

type parser struct {
	src     string
	pos     int
	depth   int
	pending []heredoc
}

// builder is the simple command being read.
type builder struct {
	cmd     command
	nested  []command // what the substitutions in it run
	start   int       // where it starts in the source; -1 before its first word
	lastEnd int       // where its last word ended
}

func (p *parser) fail(msg string) error {
	return &parseError{msg: msg, pos: p.pos}
}

func (p *parser) peek(n int) byte {
	if p.pos+n < len(p.src) {
		return p.src[p.pos+n]
	}
	return 0
}

// list reads simple commands until the closer (')'), which it consumes, or
// until the end of the source when closer is 0.
func (p *parser) list(closer byte) ([]command, error) {
	var out []command
	b := builder{start: -1}
	flush := func(end int) {
		if len(b.cmd.words) > 0 || len(b.cmd.redirects) > 0 {
			b.cmd.text = strings.TrimSpace(p.src[b.start:end])
			out = append(out, b.cmd)
		}
		out = append(out, b.nested...)
		b = builder{start: -1}
	}

	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			p.pos++
		case c == '\\' && p.peek(1) == '\n':
			p.pos += 2
		case c == '\n':
			flush(p.pos)
			p.pos++
			bodies, err := p.heredocBodies()
			if err != nil {
				return nil, err
			}
			out = append(out, bodies...)
		case c == '#':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case closer != 0 && c == closer:
			flush(p.pos)
			p.pos++
			return out, nil
		case c == ')':
			// Not ours: the end of a pattern of a case, or a mistake the shell
			// would refuse. It ends a command.
			flush(p.pos)
			p.pos++
		case c == '(' && p.peek(1) == '(' && (b.start < 0 || len(b.cmd.words) == 1 && b.cmd.words[0] == "for") && p.arithmetic(p.pos, &b):
			// ((…)) is arithmetic and not a heredoc: the shell runs only what
			// the substitutions in it run.
		case c == '(':
			flush(p.pos)
			p.pos++
			group, err := p.child(')')
			if err != nil {
				return nil, err
			}
			out = append(out, group...)
		case c == ';' || c == '|' || (c == '&' && p.peek(1) != '>'):
			flush(p.pos)
			p.pos += operatorLength(p.src[p.pos:])
		default:
			if b.start < 0 {
				b.start = p.pos
			}
			if err := p.item(closer, &b); err != nil {
				return nil, err
			}
		}
	}
	if closer != 0 {
		return nil, p.fail("unterminated ( or $(")
	}
	flush(len(p.src))
	return out, nil
}

// operatorLength is how many bytes the control operator at the start of s is.
func operatorLength(s string) int {
	for _, op := range []string{"&&", "||", "|&", ";;", ";&"} {
		if strings.HasPrefix(s, op) {
			return 2
		}
	}
	return 1
}

// child reads a nested list: a group or a substitution.
func (p *parser) child(closer byte) ([]command, error) {
	if p.depth+1 > maxDepth {
		return nil, p.fail("nested too deeply")
	}
	p.depth++
	defer func() { p.depth-- }()
	return p.list(closer)
}

// item reads the next word, redirection or process substitution of a command.
func (p *parser) item(closer byte, b *builder) error {
	c := p.src[p.pos]
	switch {
	case (c == '<' || c == '>') && p.peek(1) == '(':
		start := p.pos
		p.pos += 2
		nested, err := p.child(')')
		if err != nil {
			return err
		}
		b.nested = append(b.nested, nested...)
		b.cmd.words = append(b.cmd.words, p.src[start:p.pos])
		b.lastEnd = p.pos
	case c == '<' || c == '>' || c == '&':
		return p.redirect(closer, b)
	default:
		ws, err := p.word(closer, b, true)
		if err != nil {
			return err
		}
		b.cmd.words = append(b.cmd.words, ws...)
		b.lastEnd = p.pos
	}
	return nil
}

// isMeta reports whether c ends a word.
func isMeta(c byte) bool {
	switch c {
	case ' ', '\t', '\r', '\n', ';', '&', '|', '(', ')', '<', '>':
		return true
	}
	return false
}

// word reads a word: unquoted text, quotes, escapes and expansions up to the
// next character that ends a word. Whatever its expansions run is added to b.
// A word that is only quotes is the empty word. The words it makes are the
// word itself, or what its brace expansion (when braces is set) and an
// unquoted $IFS make of it: the shell splits there, so git${IFS}push is git
// and push.
func (p *parser) word(closer byte, b *builder, braces bool) ([]string, error) {
	var out []string
	var w wordBuf
	split := false
	finish := func() error {
		words, ok := w.expand(braces)
		if !ok {
			return p.fail("a brace expansion that makes too many words")
		}
		out = append(out, words...)
		w = wordBuf{}
		return nil
	}
	begin := p.pos
loop:
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case isMeta(c) || c == closer && closer != 0:
			break loop
		case c == '\\':
			if p.peek(1) == '\n' {
				p.pos += 2
			} else if p.pos+1 < len(p.src) {
				w.put(p.src[p.pos+1:p.pos+2], true)
				p.pos += 2
			} else {
				w.putByte(c, true)
				p.pos++
			}
		case c == '\'':
			end := strings.IndexByte(p.src[p.pos+1:], '\'')
			if end < 0 {
				return nil, p.fail("unterminated single quote")
			}
			w.put(p.src[p.pos+1:p.pos+1+end], true)
			p.pos += end + 2
		case c == '"':
			if err := p.doubleQuoted(&w, b, '"'); err != nil {
				return nil, err
			}
		case c == '$' && p.peek(1) == '"':
			p.pos++ // $"…" is "…" in the locale of the shell
		case c == '$' && p.peek(1) == '\'':
			if err := p.ansiC(&w); err != nil {
				return nil, err
			}
		case c == '$' && ifsLength(p.src[p.pos:]) > 0:
			p.pos += ifsLength(p.src[p.pos:])
			split = true
			if w.touched {
				if err := finish(); err != nil {
					return nil, err
				}
			}
		case c == '`' || c == '$':
			if err := p.expansion(&w, b); err != nil {
				return nil, err
			}
		default:
			w.putByte(c, false)
			p.pos++
		}
	}
	if p.pos == begin {
		p.pos++ // never stand still
	}
	if w.touched || !split {
		if err := finish(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ifsLength is the length of $IFS or ${IFS} at the start of s, or 0.
func ifsLength(s string) int {
	if strings.HasPrefix(s, "${IFS}") {
		return len("${IFS}")
	}
	if strings.HasPrefix(s, "$IFS") && (len(s) == len("$IFS") || !isNameByte(s[len("$IFS")])) {
		return len("$IFS")
	}
	return 0
}

func isNameByte(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// ansiC reads $'…' at the current position, and writes what the string
// stands for to w: it is quoted text with escapes, and \' does not end it.
func (p *parser) ansiC(w *wordBuf) error {
	text, n := ansiC(p.src[p.pos+2:])
	if n < 0 {
		return p.fail("unterminated $' quote")
	}
	w.put(text, true)
	p.pos += 2 + n
	return nil
}

// doubleQuoted reads the inside of a double-quoted string, from the opening
// quote to the closing one (end is '"'), or the whole of the rest of the
// source when end is 0, which is how a heredoc body is read. Substitutions in
// it run, and their commands are added to b; everything else is data.
func (p *parser) doubleQuoted(w *wordBuf, b *builder, end byte) error {
	if end != 0 {
		p.pos++ // the opening quote
	}
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case end != 0 && c == end:
			p.pos++
			return nil
		case c == '\\':
			next := p.peek(1)
			switch next {
			case '$', '`', '"', '\\':
				w.putByte(next, true)
				p.pos += 2
			case '\n':
				p.pos += 2
			default:
				w.putByte(c, true)
				p.pos++
			}
		case c == '`' || c == '$':
			if err := p.expansion(w, b); err != nil {
				return err
			}
		default:
			w.putByte(c, true)
			p.pos++
		}
	}
	if end != 0 {
		return p.fail("unterminated double quote")
	}
	return nil
}

// expansion reads a $(...), $((...)), $[...], ${...} or backtick expansion at
// the current position, or a lone $, and writes it to w as it was written. The
// commands it runs are added to b.
func (p *parser) expansion(w *wordBuf, b *builder) error {
	start := p.pos
	switch {
	case p.src[p.pos] == '`':
		if err := p.backtick(b); err != nil {
			return err
		}
	case p.peek(1) == '(' && p.peek(2) == '(' && p.arithmetic(p.pos+1, b):
	case p.peek(1) == '(':
		p.pos += 2
		nested, err := p.child(')')
		if err != nil {
			return err
		}
		b.nested = append(b.nested, nested...)
	case p.peek(1) == '[':
		if !p.bracketed(b) {
			return p.fail("unterminated $[")
		}
	case p.peek(1) == '{':
		if err := p.braced(b); err != nil {
			return err
		}
	default:
		p.pos++
	}
	w.put(p.src[start:p.pos], true)
	return nil
}

// backtick reads a `...` command substitution at the current position. The
// shell takes the text up to the next backtick that is not escaped, removes
// the backslash from \`, \$ and \\, and reads what is left as commands.
func (p *parser) backtick(b *builder) error {
	var body []byte
	i := p.pos + 1
	for ; i < len(p.src) && p.src[i] != '`'; i++ {
		if c := p.src[i]; c == '\\' && i+1 < len(p.src) && (p.src[i+1] == '`' || p.src[i+1] == '$' || p.src[i+1] == '\\') {
			i++
		}
		body = append(body, p.src[i])
	}
	if i >= len(p.src) {
		return p.fail("unterminated backtick")
	}
	if p.depth+1 > maxDepth {
		return p.fail("nested too deeply")
	}
	nested, err := parseAt(string(body), p.depth+1)
	if err != nil {
		return err
	}
	b.nested = append(b.nested, nested...)
	p.pos = i + 1
	return nil
}

// arithmetic reads ((...)) whose first ( is at open, as $((...)) or as a
// command, if that is what it is, adding what the substitutions inside it run
// to b, and reports whether it was. The shell takes it for arithmetic when the
// ) that closes the second ( is followed at once by another: ((a); (b)) and
// $((a) ) are subshells, which run commands, and (( 1 << 2 )) is a number.
func (p *parser) arithmetic(open int, b *builder) bool {
	depth := 0
	for i := open + 1; i < len(p.src); i++ {
		switch p.src[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth > 0 {
				continue
			}
			if i+1 >= len(p.src) || p.src[i+1] != ')' {
				return false
			}
			nested, err := p.dataCommands(p.src[open+2 : i])
			if err != nil {
				return false
			}
			b.nested = append(b.nested, nested...)
			p.pos = i + 2
			return true
		}
	}
	return false
}

// bracketed reads $[...], the old form of arithmetic, at the current
// position if it is closed, adding what the substitutions in it run to b.
func (p *parser) bracketed(b *builder) bool {
	depth := 0
	for i := p.pos + 1; i < len(p.src); i++ {
		switch p.src[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				nested, err := p.dataCommands(p.src[p.pos+2 : i])
				if err != nil {
					return false
				}
				b.nested = append(b.nested, nested...)
				p.pos = i + 1
				return true
			}
		}
	}
	return false
}

// braced reads ${...} at the current position, adding what the substitutions
// in it run to b.
func (p *parser) braced(b *builder) error {
	var skipped wordBuf
	p.pos += 2
	depth := 1
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == '\\' && p.pos+1 < len(p.src):
			p.pos += 2
		case c == '\'':
			end := strings.IndexByte(p.src[p.pos+1:], '\'')
			if end < 0 {
				return p.fail("unterminated single quote")
			}
			p.pos += end + 2
		case c == '$' && p.peek(1) == '\'':
			if err := p.ansiC(&skipped); err != nil {
				return err
			}
		case c == '"':
			if err := p.doubleQuoted(&skipped, b, '"'); err != nil {
				return err
			}
		case c == '`' || c == '$' && (p.peek(1) == '(' || p.peek(1) == '['):
			if err := p.expansion(&skipped, b); err != nil {
				return err
			}
		case c == '$' && p.peek(1) == '{':
			depth++
			p.pos += 2
		case c == '}':
			depth--
			p.pos++
			if depth == 0 {
				return nil
			}
		default:
			p.pos++
		}
	}
	return p.fail("unterminated ${")
}

// dataCommands reads text as the inside of a double-quoted string, and returns
// the commands its substitutions run.
func (p *parser) dataCommands(text string) ([]command, error) {
	if p.depth+1 > maxDepth {
		return nil, p.fail("nested too deeply")
	}
	q := &parser{src: text, depth: p.depth + 1}
	b := builder{start: -1}
	var w wordBuf
	if err := q.doubleQuoted(&w, &b, 0); err != nil {
		return nil, err
	}
	return b.nested, nil
}

// redirect reads a redirection operator and its target.
func (p *parser) redirect(closer byte, b *builder) error {
	opStart := p.pos
	op := redirectOperator(p.src[p.pos:])
	if op == "" {
		return p.fail("a & that is not an operator") // cannot happen: the caller checked
	}
	p.pos += len(op)

	fd := ""
	if n := len(b.cmd.words); n > 0 && b.lastEnd == opStart && isDigits(b.cmd.words[n-1]) {
		fd = b.cmd.words[n-1]
		b.cmd.words = b.cmd.words[:n-1]
	}
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
	targetStart := p.pos
	if p.pos >= len(p.src) || isMeta(p.src[p.pos]) && p.src[p.pos] != '<' && p.src[p.pos] != '>' || closer != 0 && p.src[p.pos] == closer {
		return p.fail(fmt.Sprintf("the redirection %s has no target", op))
	}
	heredocOp := op == "<<" || op == "<<-"
	targets, err := p.word(closer, b, !heredocOp)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return p.fail(fmt.Sprintf("the redirection %s has no target", op))
	}
	// A brace expansion that makes several words is an ambiguous redirect to
	// the shell; every one of them is judged.
	for _, target := range targets {
		b.cmd.redirects = append(b.cmd.redirects, redirect{fd: fd, op: op, target: target})
	}
	b.lastEnd = p.pos

	if heredocOp {
		p.pending = append(p.pending, heredoc{
			delim:  targets[0],
			strip:  op == "<<-",
			quoted: strings.ContainsAny(p.src[targetStart:p.pos], `'"\`),
		})
	}
	return nil
}

// redirectOperator is the redirection operator at the start of s, or "".
func redirectOperator(s string) string {
	for _, op := range []string{"<<<", "<<-", "&>>", ">>", ">|", ">&", "&>", "<<", "<&", "<>", ">", "<"} {
		if strings.HasPrefix(s, op) {
			return op
		}
	}
	return ""
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

// heredocBodies reads the bodies of the heredocs that were waiting for a
// newline, which has just been read. The body of one whose delimiter was not
// quoted expands substitutions; those run, and their commands are returned.
// A body that is not ended is taken to run to the end of the source, as shells
// do.
func (p *parser) heredocBodies() ([]command, error) {
	var out []command
	pending := p.pending
	p.pending = nil
	for _, h := range pending {
		var body strings.Builder
		for p.pos < len(p.src) {
			end := strings.IndexByte(p.src[p.pos:], '\n')
			line := p.src[p.pos:]
			if end >= 0 {
				line = p.src[p.pos : p.pos+end]
				p.pos += end + 1
			} else {
				p.pos = len(p.src)
			}
			compare := strings.TrimSuffix(line, "\r")
			if h.strip {
				compare = strings.TrimLeft(compare, "\t")
			}
			if compare == h.delim {
				break
			}
			body.WriteString(line)
			body.WriteByte('\n')
		}
		if h.quoted {
			continue
		}
		nested, err := p.dataCommands(body.String())
		if err != nil {
			return nil, err
		}
		out = append(out, nested...)
	}
	return out, nil
}
