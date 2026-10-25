package afclient

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestReadSecretFile_ReadsAFileOrStdinTrimmed(t *testing.T) {
	path := testutil.WriteFile(t, t.TempDir(), "pat", "\n  ghp_secret \n")
	for name, c := range map[string]struct {
		path  string
		stdin io.Reader
	}{
		"a file": {path, nil},
		"stdin":  {"-", strings.NewReader(" ghp_secret\r\n")},
	} {
		if got, err := ReadSecretFile("--hub-token-file", c.path, c.stdin); err != nil || got != "ghp_secret" {
			t.Errorf("%s: %q, %v, want the secret trimmed", name, got, err)
		}
	}
}

func TestReadSecretFile_RefusesWhatBreaksARuleWithoutEchoingIt(t *testing.T) {
	dir := t.TempDir()
	for name, c := range map[string]struct {
		content string
		says    string
	}{
		"empty":             {" \n\t", "is empty"},
		"not UTF-8":         {"SENTINEL\xff", "is not valid UTF-8"},
		"two lines":         {"SENTINEL\nSENTINEL", "spans more than one line"},
		"a carriage return": {"SENTINEL\rSENTINEL", "spans more than one line"},
		"a control char":    {"SENTINEL\x07", "holds a control character"},
		"over 4096 bytes":   {strings.Repeat("S", MaxSecretBytes+1), "is longer than 4096 bytes"},
		"over the read":     {strings.Repeat("S", 64<<10+1), "is longer than 4096 bytes"},
	} {
		path := testutil.WriteFile(t, dir, strings.ReplaceAll(name, " ", "-"), c.content)

		_, err := ReadSecretFile("--hub-token-file", path, nil)

		var se *SecretError
		want := "--hub-token-file: what was read from " + path + " " + c.says
		if !errors.As(err, &se) || se.Kind != SecretContent || se.Error() != want {
			t.Errorf("%s: %v, want a content error %q", name, err, want)
		}
	}
	if got, err := ReadSecretFile("--f", testutil.WriteFile(t, dir, "max", strings.Repeat("S", MaxSecretBytes)), nil); err != nil || len(got) != MaxSecretBytes {
		t.Errorf("a secret of exactly 4096 bytes: %v", err)
	}
}

func TestReadSecretFile_ASourceThatCannotBeReadIsAnIOError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for name, c := range map[string]struct {
		path, source string
		stdin        io.Reader
	}{
		"a missing file": {missing, missing, nil},
		"no stdin":       {"-", "stdin", nil},
		"a broken stdin": {"-", "stdin", errReader{}},
	} {
		_, err := ReadSecretFile("--hub-token-file", c.path, c.stdin)

		var se *SecretError
		if !errors.As(err, &se) || se.Kind != SecretIO || !strings.HasPrefix(se.Error(), "cannot read "+c.source+", which --hub-token-file names: ") {
			t.Errorf("%s: %v, want an io error that names %s and the flag", name, err, c.source)
		}
	}
}

// errReader is a reader that fails.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("broken pipe") }
