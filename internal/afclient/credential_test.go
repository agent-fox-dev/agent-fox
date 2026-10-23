package afclient

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// homeWith makes a home directory whose credential file holds content, and returns the function
// that gives the home and the path of the file.
func homeWith(t *testing.T, content string) (func() (string, error), string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, CredentialFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return func() (string, error) { return home, nil }, path
}

// TestLoadCredentialNamesTheLineOfATOMLErrorAndNeverTheToken_TS_16_25 verifies TS-16-25 for
// the reading: a file that does not parse is a config error that names the file and the line,
// and what the parser read is not in the message.
// Verifies: 16-REQ-4.4
func TestLoadCredentialNamesTheLineOfATOMLErrorAndNeverTheToken_TS_16_25(t *testing.T) {
	home, path := homeWith(t, "note = 1\ntoken = \"SENTINEL\n")
	_, err := LoadCredential(home)
	var ce *CredentialError
	if !errors.As(err, &ce) || ce.Kind != CredentialConfig {
		t.Fatalf("LoadCredential = %v, want a config error", err)
	}
	if !strings.Contains(ce.Error(), path) || !strings.Contains(ce.Error(), "line 2") || strings.Contains(ce.Error(), "SENTINEL") {
		t.Errorf("message %q, want it to name %s and line 2 and not the token", ce.Error(), path)
	}
}

// TestLoadCredentialTrimsSpacesAroundTheToken_TS_16_22 verifies TS-16-22 for the reading: the
// credential is the top-level token, without the spaces around it.
// Verifies: 16-REQ-4.1
func TestLoadCredentialTrimsSpacesAroundTheToken_TS_16_22(t *testing.T) {
	home, path := homeWith(t, "token = \"  T1 \"\n[t]\ntoken = \"T2\"\n")
	cred, err := LoadCredential(home)
	if err != nil || cred.Token != "T1" || cred.Path != path || cred.Loose {
		t.Errorf("LoadCredential = %+v, %v, want T1 from %s, not loose", cred, err, path)
	}
}
