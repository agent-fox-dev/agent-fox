package afclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

// existing writes content as the credential file of a new home and reads it.
func existing(t *testing.T, content string) (*ExistingFile, error) {
	t.Helper()
	home, _ := homeWith(t, content)
	return ReadExisting(home)
}

// TestWithReplacesOnlyTheValueOfTheTokenLineAndKeepsEveryOtherByte_TS_16_35 verifies TS-16-35
// for the editing: the value on the token line is replaced by a basic string, whatever it was
// (basic or literal, with escapes, followed by a comment), and nothing else changes.
// Verifies: 16-REQ-5.7
func TestWithReplacesOnlyTheValueOfTheTokenLineAndKeepsEveryOtherByte_TS_16_35(t *testing.T) {
	const cred = `n"e\w`
	const quoted = `"n\"e\\w"`
	for name, c := range map[string]struct{ before, after string }{
		"plain":                 {"token = \"old\"\n", "token = " + quoted + "\n"},
		"no spaces":             {"token=\"old\"\n", "token=" + quoted + "\n"},
		"indented, a comment":   {"  token =\t'old'   # keep me\n", "  token =\t" + quoted + "   # keep me\n"},
		"escapes in the value":  {"token = \"a\\\"b\\\\c\"\n", "token = " + quoted + "\n"},
		"a literal with quote":  {"token = 'a\"b'\n", "token = " + quoted + "\n"},
		"CRLF":                  {"# c\r\nk = 1\r\ntoken = \"old\"\r\n[t]\r\ntoken = \"x\"\r\n", "# c\r\nk = 1\r\ntoken = " + quoted + "\r\n[t]\r\ntoken = \"x\"\r\n"},
		"no final newline":      {"k = 1\ntoken = \"old\"", "k = 1\ntoken = " + quoted},
		"after a decoy":         {"notes = \"\"\"\ntoken = \"decoy\"\n\"\"\"\ntoken = \"old\"\n", "notes = \"\"\"\ntoken = \"decoy\"\n\"\"\"\ntoken = " + quoted + "\n"},
		"after a literal decoy": {"notes = '''\ntoken = \"decoy\"\n'''\ntoken = \"old\"\n", "notes = '''\ntoken = \"decoy\"\n'''\ntoken = " + quoted + "\n"},
		"after an array":        {"list = [\n  \"a\",\n]\ntoken = \"old\"\n", "list = [\n  \"a\",\n]\ntoken = " + quoted + "\n"},
		"an empty value":        {"token = \"\"\n", "token = " + quoted + "\n"},
	} {
		f, err := existing(t, c.before)
		if err != nil {
			t.Errorf("%s: ReadExisting: %v", name, err)
			continue
		}
		got, err := f.With(cred)
		if err != nil || string(got) != c.after {
			t.Errorf("%s: With = %q, %v, want %q", name, got, err, c.after)
			continue
		}
		var doc map[string]any
		if _, err := toml.Decode(string(got), &doc); err != nil || doc["token"] != cred {
			t.Errorf("%s: the result parses to %v (%v), want the token", name, doc["token"], err)
		}
	}

	f := &ExistingFile{Path: "x"}
	if got, err := f.With("tok"); err != nil || string(got) != "token = \"tok\"\n" {
		t.Errorf("a file that is not there: With = %q, %v, want the single line", got, err)
	}
}

// TestReadExistingRefusesALayoutThatIsNotTheExpectedOne_TS_16_31 verifies TS-16-31 for the
// reading: no file is edited unless it has the top-level one-line token that af replaces.
// Verifies: 16-REQ-5.3
func TestReadExistingRefusesALayoutThatIsNotTheExpectedOne_TS_16_31(t *testing.T) {
	for name, c := range map[string]struct {
		content string
		kind    string
	}{
		"empty":                 {"", CredentialConfig},
		"another key":           {"other = 1\n", CredentialConfig},
		"after a table":         {"[t]\ntoken = \"x\"\n", CredentialConfig},
		"after a table array":   {"[[t]]\nk = 1\ntoken = \"x\"\n", CredentialConfig},
		"a multi-line string":   {"token = \"\"\"x\"\"\"\n", CredentialConfig},
		"a multi-line literal":  {"token = '''x'''\n", CredentialConfig},
		"a dotted key":          {"token.x = \"x\"\n", CredentialConfig},
		"a dotted prefix":       {"a.token = \"x\"\n", CredentialConfig},
		"a quoted key":          {"\"token\" = \"x\"\n", CredentialConfig},
		"an inline table":       {"token = { a = 1 }\n", CredentialConfig},
		"a number":              {"token = 5\n", CredentialConfig},
		"two strings":           {"token = \"a\" \"b\"\n", CredentialConfig},
		"text after the value":  {"token = \"a\" x\n", CredentialConfig},
		"not TOML":              {"token = \"a\n", CredentialConfig},
		"only in a decoy":       {"notes = \"\"\"\ntoken = \"decoy\"\n\"\"\"\n", CredentialConfig},
		"a comment, not a line": {"# token = \"x\"\n", CredentialConfig},
	} {
		_, err := existing(t, c.content)
		var ce *CredentialError
		if !errors.As(err, &ce) || ce.Kind != c.kind {
			t.Errorf("%s: ReadExisting = %v, want a %s error", name, err, c.kind)
			continue
		}
		if !strings.Contains(ce.Error(), CredentialFile) {
			t.Errorf("%s: message %q does not name the file", name, ce.Error())
		}
	}

	// A file that is not there is not an error, and one that is unreadable is.
	home := func() (string, error) { return t.TempDir(), nil }
	if f, err := ReadExisting(home); err != nil || f.Exists || !strings.HasSuffix(f.Path, CredentialFile) {
		t.Errorf("ReadExisting of no file = %+v, %v", f, err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, CredentialFile), 0o700); err != nil {
		t.Fatal(err)
	}
	var ce *CredentialError
	if _, err := ReadExisting(func() (string, error) { return dir, nil }); !errors.As(err, &ce) || ce.Kind != CredentialIO {
		t.Errorf("ReadExisting of a directory = %v, want an io error", err)
	}
}
