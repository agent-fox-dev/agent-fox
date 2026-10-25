package afclient

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// home is a home directory whose credential file holds content with the mode, unless content
// is nil, and the path of that file.
func home(t *testing.T, content *string, mode os.FileMode) (func() (string, error), string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, CredentialFile)
	if content != nil {
		testutil.WriteFile(t, dir, filepath.ToSlash(CredentialFile), *content)
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	return func() (string, error) { return dir, nil }, path
}

func ptr(s string) *string { return &s }

func TestLoadCredential_ReadsTheTopLevelTokenWithoutTheSpacesAroundIt(t *testing.T) {
	h, path := home(t, ptr("other = 1\ntoken = \"  T1 \"\n[t]\ntoken = \"T2\"\n"), 0o600)

	cred, err := LoadCredential(h)

	if err != nil || cred != (Credential{Token: "T1", Path: path}) {
		t.Errorf("LoadCredential = %+v, %v, want T1 from %s, not loose", cred, err, path)
	}
}

func TestLoadCredential_SaysAFileThatOthersCanReadIsLoose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are a Unix notion")
	}
	for mode, loose := range map[os.FileMode]bool{0o644: true, 0o640: true, 0o604: true, 0o600: false, 0o400: false} {
		h, _ := home(t, ptr(`token = "T"`), mode)
		if cred, err := LoadCredential(h); err != nil || cred.Loose != loose {
			t.Errorf("mode %v: %+v, %v, want loose %v", mode, cred, err, loose)
		}
	}
}

func TestLoadCredential_AFileThatHoldsNoUsableTokenNamesTheFileAndNeverTheToken(t *testing.T) {
	for name, c := range map[string]struct {
		content *string
		kind    string
		says    string
	}{
		"no file":            {nil, CredentialAuth, "does not exist; run `afc login`"},
		"no token key":       {ptr("other = 1\n"), CredentialAuth, "has no top-level string key `token`"},
		"a token in a table": {ptr("[profile]\ntoken = \"SENTINEL\"\n"), CredentialAuth, "has no top-level string key"},
		"not a string":       {ptr("token = 5\n"), CredentialAuth, "has no top-level string key"},
		"empty":              {ptr("token = \"\"\n"), CredentialAuth, "has an empty `token`"},
		"blank":              {ptr("token = \"   \"\n"), CredentialAuth, "has an empty `token`"},
		"not TOML":           {ptr("note = 1\ntoken = \"SENTINEL\n"), CredentialConfig, "does not parse as TOML (line 2)"},
		"a line break":       {ptr(`token = "SENTINEL\nSENTINEL"`), CredentialConfig, "holds a line break or a control character"},
		"a control char":     {ptr(`token = "SENTINEL\u0007"`), CredentialConfig, "holds a line break or a control character"},
		"a trailing return":  {ptr(`token = "SENTINEL\r"`), CredentialConfig, "holds a line break or a control character"},
	} {
		h, path := home(t, c.content, 0o600)

		_, err := LoadCredential(h)

		var ce *CredentialError
		if !errors.As(err, &ce) || ce.Kind != c.kind || ce.Path != path {
			t.Errorf("%s: %v, want a %s error for %s", name, err, c.kind, path)
			continue
		}
		if msg := ce.Error(); !strings.Contains(msg, path) || !strings.Contains(msg, c.says) || strings.Contains(msg, "SENTINEL") {
			t.Errorf("%s: %q, want it to name %s and say %q, and not the token", name, msg, path, c.says)
		}
	}
}

func TestLoadCredential_AFileOrHomeThatCannotBeReadIsAnIOError(t *testing.T) {
	dirHome, path := home(t, nil, 0)
	if err := os.MkdirAll(path, 0o700); err != nil { // the file is a directory
		t.Fatal(err)
	}
	noHome := func() (string, error) { return "", errors.New("no HOME") }
	for name, c := range map[string]struct {
		home func() (string, error)
		path string
	}{
		"the file is a directory":    {dirHome, path},
		"the home cannot be found":   {noHome, "~/.af/config.toml"},
		"the home of the user [nil]": {nil, filepath.Join(t.TempDir(), "home", CredentialFile)},
	} {
		if c.home == nil {
			t.Setenv("HOME", filepath.Dir(filepath.Dir(c.path)))
			if err := os.MkdirAll(c.path, 0o700); err != nil {
				t.Fatal(err)
			}
		}

		_, err := LoadCredential(c.home)

		var ce *CredentialError
		if !errors.As(err, &ce) || ce.Kind != CredentialIO || ce.Path != c.path || !strings.Contains(ce.Error(), c.path) {
			t.Errorf("%s: %v, want an io error that names %s", name, err, c.path)
		}
	}
}
