package secrets_test

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// keyFile writes a key file with the mode and returns its path.
func keyFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := testutil.WriteFile(t, t.TempDir(), "key", content)
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func encoded(n int) string { return base64.StdEncoding.EncodeToString(make([]byte, n)) }

func TestLoadKey_RefusesWhatIsNotAKeyNamingTheFileAndNotItsContent(t *testing.T) {
	for name, path := range map[string]string{
		"missing":                    filepath.Join(t.TempDir(), "nowhere", "key"),
		"a directory":                t.TempDir(),
		"not base64":                 keyFile(t, "not base64 !!!secretmarker", 0o600),
		"31 bytes":                   keyFile(t, encoded(31), 0o600),
		"33 bytes":                   keyFile(t, encoded(33), 0o600),
		"empty":                      keyFile(t, "", 0o600),
		"base64 without its padding": keyFile(t, strings.TrimRight(encoded(32), "="), 0o600),
		"too large to be a key":      keyFile(t, strings.Repeat("A", 1<<20), 0o600),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := secrets.LoadKey(path)
			if err == nil {
				t.Fatal("LoadKey accepted the file")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name %s", err, path)
			}
			if strings.Contains(err.Error(), "secretmarker") || strings.Contains(err.Error(), "AAAA") {
				t.Errorf("error = %q holds the file's content", err)
			}
		})
	}
}

func TestLoadKey_RefusesPermissionBitsBeyondTheOwner(t *testing.T) {
	content := base64.StdEncoding.EncodeToString(testutil.Key())
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o610} {
		path := keyFile(t, content, mode)
		if _, err := secrets.LoadKey(path); err == nil || !strings.Contains(err.Error(), fmt.Sprintf("%04o", mode)) || !strings.Contains(err.Error(), path) {
			t.Errorf("mode %04o: error = %v, want a refusal naming the file and the mode", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400, 0o700} {
		if _, err := secrets.LoadKey(keyFile(t, content, mode)); err != nil {
			t.Errorf("mode %04o: %v, want an owner-only mode accepted", mode, err)
		}
	}
}

func TestLoadKey_ReadsTheKeyIgnoringSurroundingWhitespace(t *testing.T) {
	k, err := secrets.LoadKey(keyFile(t, " \n\t"+base64.StdEncoding.EncodeToString(testutil.Key())+" \r\n\n", 0o600))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := k.Seal("widgets", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := newKey(t).Open("widgets", sealed); err != nil || got != "tok" {
		t.Errorf("the key of the file opens with %q, %v; want it to be the key the file holds", got, err)
	}
}

func TestNewKey_TakesExactly32Bytes(t *testing.T) {
	for _, n := range []int{0, 31, 33, 64} {
		if _, err := secrets.NewKey(make([]byte, n)); err == nil {
			t.Errorf("NewKey accepted %d bytes", n)
		}
	}
	if _, err := secrets.NewKey(make([]byte, 32)); err != nil {
		t.Errorf("NewKey refused 32 bytes: %v", err)
	}
}

func TestKey_PrintsAsAPlaceholderUnderEveryVerb(t *testing.T) {
	raw := testutil.Key()
	k := newKey(t)
	needles := []string{base64.StdEncoding.EncodeToString(raw), base64.RawStdEncoding.EncodeToString(raw), fmt.Sprintf("%x", raw), fmt.Sprintf("%v", raw), string(raw)}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d"} {
		for _, shown := range []string{fmt.Sprintf(verb, k), fmt.Sprintf(verb, &k), fmt.Sprintf(verb, []secrets.Key{k}), fmt.Sprintf(verb, struct{ K secrets.Key }{k})} {
			for _, needle := range needles {
				if strings.Contains(shown, needle) {
					t.Errorf("%s printed the key: %q", verb, shown)
				}
			}
		}
	}
	if s, zero := k.String(), (secrets.Key{}).String(); s == "" || zero == "" || s == zero {
		t.Errorf("String() = %q, and %q for the zero key; want two placeholders", s, zero)
	}
}
