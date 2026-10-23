package afclient

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// ExistingFile is the credential file as it stands before af login writes it.
type ExistingFile struct {
	// Path is the credential file.
	Path string
	// Exists is false when there is no file yet.
	Exists bool
	// Content is what the file holds.
	Content []byte
	// Loose is true when, on Unix, a group or other permission bit of the file is set.
	Loose bool

	// start and end bound the quoted value on the token line of Content.
	start, end int
}

// tokenKey matches the start of a token line: the bare key, the equals sign and the space after it.
var tokenKeyLine = regexp.MustCompile(`^token[ \t]*=[ \t]*`)

// ReadExisting reads the credential file that af login is about to write, where home gives the
// home directory (os.UserHomeDir for nil). A file that is not there is not an error. One that
// cannot be read is a CredentialIO error, and one that does not parse or whose layout is not
// the one af edits is a CredentialConfig error: it must have, before any table header, a
// token on a line of its own, whose value is a one-line basic or literal string followed by
// nothing but a comment. Nothing is changed.
func ReadExisting(home func() (string, error)) (*ExistingFile, error) {
	path, err := credentialPath(home)
	if err != nil {
		return nil, err
	}
	data, rerr := os.ReadFile(path)
	switch {
	case errors.Is(rerr, fs.ErrNotExist):
		return &ExistingFile{Path: path}, nil
	case rerr != nil:
		return nil, &CredentialError{Kind: CredentialIO, Path: path, Message: fmt.Sprintf("cannot read the credential file %s: %v", path, rerr)}
	}
	var doc map[string]any
	if _, derr := toml.Decode(string(data), &doc); derr != nil {
		return nil, parseError(path, derr)
	}
	f := &ExistingFile{Path: path, Exists: true, Content: data, Loose: isLoose(path)}
	if start, end, ok := locateToken(data); ok {
		// The line that was found has to be the token the parser sees.
		var v struct {
			Value string `toml:"v"`
		}
		_, verr := toml.Decode("v = "+string(data[start:end]), &v)
		if token, isString := doc[tokenKey].(string); verr == nil && isString && token == v.Value {
			f.start, f.end = start, end
			return f, nil
		}
	}
	return nil, &CredentialError{Kind: CredentialConfig, Path: path, Message: fmt.Sprintf(
		"the layout of the credential file %s is not the one af expects (a `token = \"...\"` line of its own, before any table); run `afc login` or edit the file to give it that line", path)}
}

// With returns the content of the credential file once credential is stored: the file as it is
// with the value on its token line replaced by credential as a TOML basic string and every other
// byte kept, or, for a file that is not there, the single line that holds it.
func (f *ExistingFile) With(credential string) ([]byte, error) {
	quoted := quoteBasic(credential)
	if !f.Exists {
		return []byte("token = " + quoted + "\n"), nil
	}
	out := make([]byte, 0, len(f.Content)+len(quoted))
	out = append(out, f.Content[:f.start]...)
	out = append(out, quoted...)
	out = append(out, f.Content[f.end:]...)
	var doc map[string]any
	if _, err := toml.Decode(string(out), &doc); err != nil || doc[tokenKey] != credential {
		return nil, errors.New("the credential file with the new token does not read back as the token")
	}
	return out, nil
}

// quoteBasic is s as a TOML basic string.
func quoteBasic(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// locateToken finds the quoted value of the token line of a TOML document that parses: the
// span in data from the opening to the closing quote. There is none, and ok is false, if the
// first table header comes before a token line, if the token line is not `token = "…"` or
// `token = '…'` on one line followed at most by a comment, or if there is no token line.
func locateToken(data []byte) (start, end int, ok bool) {
	var multiline string // the delimiter of the multi-line string the scan is inside of
	for pos := 0; pos < len(data); {
		lineEnd, next := len(data), len(data)
		if i := bytes.IndexByte(data[pos:], '\n'); i >= 0 {
			lineEnd, next = pos+i, pos+i+1
		}
		line := bytes.TrimSuffix(data[pos:lineEnd], []byte("\r"))
		if multiline != "" {
			if closesString(line, multiline) {
				multiline = ""
			}
			pos = next
			continue
		}
		trimmed := bytes.TrimLeft(line, " \t")
		offset := pos + len(line) - len(trimmed)
		switch {
		case len(trimmed) == 0 || trimmed[0] == '#':
		case trimmed[0] == '[':
			return 0, 0, false
		case tokenKeyLine.Match(trimmed):
			prefix := len(tokenKeyLine.Find(trimmed))
			s, e, valid := stringValue(trimmed[prefix:])
			if !valid {
				return 0, 0, false
			}
			return offset + prefix + s, offset + prefix + e, true
		default:
			multiline = opensMultiline(trimmed)
		}
		pos = next
	}
	return 0, 0, false
}

// stringValue reads a one-line basic or literal string at the start of rest, which may be
// followed by spaces and a comment and by nothing else. It gives the span of the quoted string.
func stringValue(rest []byte) (start, end int, ok bool) {
	if len(rest) == 0 || bytes.HasPrefix(rest, []byte(`"""`)) || bytes.HasPrefix(rest, []byte(`'''`)) {
		return 0, 0, false
	}
	quote := rest[0]
	if quote != '"' && quote != '\'' {
		return 0, 0, false
	}
	end = -1
	for i := 1; i < len(rest); i++ {
		if quote == '"' && rest[i] == '\\' {
			i++
			continue
		}
		if rest[i] == quote {
			end = i + 1
			break
		}
	}
	if end < 0 {
		return 0, 0, false
	}
	after := bytes.TrimLeft(rest[end:], " \t")
	if len(after) > 0 && after[0] != '#' {
		return 0, 0, false
	}
	return 0, end, true
}

// opensMultiline says which multi-line string, if any, the key and value on line open and do not
// close: `"""`, `”'` or "".
func opensMultiline(line []byte) string {
	_, value, found := bytes.Cut(line, []byte("="))
	if !found {
		return ""
	}
	value = bytes.TrimLeft(value, " \t")
	for _, delim := range []string{`"""`, `'''`} {
		if bytes.HasPrefix(value, []byte(delim)) && !closesString(value[3:], delim) {
			return delim
		}
	}
	return ""
}

// closesString reports whether line holds the delimiter that ends a multi-line string. A basic
// string has escapes: a backslash makes the next character part of the string.
func closesString(line []byte, delim string) bool {
	for i := 0; i < len(line); i++ {
		if delim == `"""` && line[i] == '\\' {
			i++
			continue
		}
		if bytes.HasPrefix(line[i:], []byte(delim)) {
			return true
		}
	}
	return false
}
