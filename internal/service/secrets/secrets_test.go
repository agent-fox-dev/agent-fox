package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// keyBytes is a key that is the same on every run.
func keyBytes(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

// mustKey makes a key from keyBytes(seed).
func mustKey(t *testing.T, seed byte) Key {
	t.Helper()
	k, err := NewKey(keyBytes(seed))
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// keyFile writes a key file with the mode and returns its path.
func keyFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// encoded is the standard base64 of n bytes.
func encoded(n int) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, n))
}

// TestLoadKeyRefusesAMissingMalformedOrWrongSizedFile_TS_11_18 verifies TS-11-18:
// A missing, non-base64 or wrong-length key file fails naming the file and never
// its contents. The exit code of the binary is checked with the start.
// Verifies: 11-REQ-3.1
func TestLoadKeyRefusesAMissingMalformedOrWrongSizedFile_TS_11_18(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere", "key")
	garbage := keyFile(t, "not base64 !!!secretmarker", 0o600)
	short := keyFile(t, encoded(31), 0o600)
	long := keyFile(t, encoded(33), 0o600)
	empty := keyFile(t, "", 0o600)
	unpadded := keyFile(t, strings.TrimRight(encoded(32), "="), 0o600)
	huge := keyFile(t, strings.Repeat("A", 1<<20), 0o600)
	dir := t.TempDir()

	for name, path := range map[string]string{
		"missing": missing, "not base64": garbage, "31 bytes": short, "33 bytes": long,
		"empty": empty, "base64 without its padding": unpadded, "huge": huge, "a directory": dir,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadKey(path)
			if err == nil {
				t.Fatal("LoadKey accepted the file")
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("error = %q, want it to name %s", err, path)
			}
			if strings.Contains(err.Error(), "secretmarker") || strings.Contains(err.Error(), "AAAA") {
				t.Errorf("error = %q, want none of the file's contents", err)
			}
		})
	}

	t.Run("surrounding whitespace is ignored", func(t *testing.T) {
		valid := keyFile(t, " \n\t"+base64.StdEncoding.EncodeToString(keyBytes(1))+" \r\n\n", 0o600)
		k, err := LoadKey(valid)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := k.Seal("widgets", "tok")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := mustKey(t, 1).Open("widgets", sealed); err != nil || got != "tok" {
			t.Errorf("a key loaded from the file opens with %q, %v; want it to be the key of the file", got, err)
		}
	})
}

// TestLoadKeyRefusesGroupAndOtherPermissionBits_TS_11_19 verifies TS-11-19:
// Key files with group or other permission bits are refused and owner-only modes
// accepted. The exit code of the binary is checked with the start.
// Verifies: 11-REQ-3.2
func TestLoadKeyRefusesGroupAndOtherPermissionBits_TS_11_19(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to check")
	}
	content := base64.StdEncoding.EncodeToString(keyBytes(1))
	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o660, 0o666, 0o610} {
		path := keyFile(t, content, mode)
		_, err := LoadKey(path)
		if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), fmt.Sprintf("%04o", mode)) {
			t.Errorf("mode %04o: error = %v, want a refusal naming the file and the mode", mode, err)
		}
	}
	for _, mode := range []os.FileMode{0o600, 0o400, 0o700} {
		if _, err := LoadKey(keyFile(t, content, mode)); err != nil {
			t.Errorf("mode %04o: %v, want an owner-only mode accepted", mode, err)
		}
	}
}

// TestASealedValueIsTheVersionTheNonceAndTheCiphertext_TS_11_20 verifies TS-11-20:
// A sealed value has version byte 0x01, a 12-byte nonce and ciphertext plus tag.
// Verifies: 11-REQ-3.3
func TestASealedValueIsTheVersionTheNonceAndTheCiphertext_TS_11_20(t *testing.T) {
	raw := keyBytes(9)
	k, err := NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	const token = "pat-0123456789abcdef"
	sealed, err := k.Seal("widgets", token)
	if err != nil {
		t.Fatal(err)
	}
	if sealed[0] != 0x01 || len(sealed) != 1+12+len(token)+16 {
		t.Errorf("first byte %#x, length %d; want 0x01 and %d", sealed[0], len(sealed), 1+12+len(token)+16)
	}
	if bytes.Contains(sealed, []byte(token)) {
		t.Error("the sealed value contains the plaintext")
	}

	// Open it by hand, with AES-GCM and the additional data of the spec.
	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := gcm.Open(nil, sealed[1:13], sealed[13:], []byte("agent-fox/subscription/widgets"))
	if err != nil || string(plain) != token {
		t.Errorf("AES-GCM with the additional data agent-fox/subscription/widgets gave %q, %v; want the token", plain, err)
	}
	if _, err := gcm.Open(nil, sealed[1:13], sealed[13:], []byte("agent-fox/subscription/gadgets")); err == nil {
		t.Error("the value opens under other additional data")
	}
}

// TestSealingThenOpeningRoundTrips_TS_11_21 verifies TS-11-21:
// Sealing then opening round-trips and two seals of the same token differ.
// Verifies: 11-REQ-3.4
func TestSealingThenOpeningRoundTrips_TS_11_21(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 21))
	t.Logf("seed: PCG(11, 21)")
	k := mustKey(t, byte(rng.IntN(256)))

	token := func() string {
		switch rng.IntN(4) {
		case 0: // a token the way hub makes one
			return fmt.Sprintf("afpat_%x", rng.Uint64())
		case 1: // unicode
			runes := make([]rune, rng.IntN(40))
			for i := range runes {
				runes[i] = rune(0x4e00 + rng.IntN(0x200))
			}
			return string(runes)
		case 2: // any bytes at all
			b := make([]byte, rng.IntN(300))
			for i := range b {
				b[i] = byte(rng.IntN(256))
			}
			return string(b)
		}
		return strings.Repeat("x", rng.IntN(70000))
	}
	slug := func() string {
		const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
		b := make([]byte, 1+rng.IntN(100))
		for i := range b {
			b[i] = alphabet[rng.IntN(len(alphabet))]
		}
		b[0] = 'a'
		return string(b)
	}

	for range 300 {
		tok, sl := token(), slug()
		a, err := k.Seal(sl, tok)
		if err != nil {
			t.Fatal(err)
		}
		b, err := k.Seal(sl, tok)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(a, b) {
			t.Fatalf("two seals of the same %d-byte token are equal", len(tok))
		}
		got, err := k.Open(sl, a)
		if err != nil || got != tok {
			t.Fatalf("Open(Seal(%q, %d-byte token)) = %d bytes, %v; want the token back", sl, len(tok), len(got), err)
		}
	}
}

// TestOpeningFailsWithAnErrorNeverAPanic_TS_11_22 verifies TS-11-22:
// Opening fails with an error, never a panic, for a wrong key, altered byte,
// other slug, unknown version or truncation.
// Verifies: 11-REQ-3.5
func TestOpeningFailsWithAnErrorNeverAPanic_TS_11_22(t *testing.T) {
	keyA, keyB := mustKey(t, 1), mustKey(t, 2)
	const token = "pat-secret-value"
	sealed, err := keyA.Seal("widgets", token)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := keyA.Open("widgets", sealed); err != nil || got != token {
		t.Fatalf("the unaltered value does not open: %q, %v", got, err)
	}

	fails := func(name string, k Key, slug string, value []byte) {
		t.Helper()
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("%s: Open panicked: %v", name, r)
			}
		}()
		got, err := k.Open(slug, value)
		if err == nil || got != "" {
			t.Errorf("%s: Open = %q, %v; want an error and no plaintext", name, got, err)
			return
		}
		if !errors.Is(err, ErrCannotOpen) {
			t.Errorf("%s: error %q does not wrap ErrCannotOpen", name, err)
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("%s: error %q contains the token", name, err)
		}
	}

	fails("a wrong key", keyB, "widgets", sealed)
	fails("another slug", keyA, "gadgets", sealed)
	fails("a nil value", keyA, "widgets", nil)
	for i := range sealed {
		altered := bytes.Clone(sealed)
		altered[i] ^= 0x01
		fails(fmt.Sprintf("byte %d flipped", i), keyA, "widgets", altered)
	}
	version2 := bytes.Clone(sealed)
	version2[0] = 0x02
	fails("version byte 0x02", keyA, "widgets", version2)
	version0 := bytes.Clone(sealed)
	version0[0] = 0x00
	fails("version byte 0x00", keyA, "widgets", version0)
	for n := range len(sealed) {
		fails(fmt.Sprintf("truncated to %d", n), keyA, "widgets", sealed[:n])
	}
	fails("trailing bytes", keyA, "widgets", append(bytes.Clone(sealed), 0))

	// An empty token seals to the shortest value there is, and its truncations fail too.
	empty, err := keyA.Seal("widgets", "")
	if err != nil || len(empty) != 1+12+16 {
		t.Fatalf("Seal of an empty token = %d bytes, %v; want %d", len(empty), err, 1+12+16)
	}
	if got, err := keyA.Open("widgets", empty); err != nil || got != "" {
		t.Errorf("Open of the empty token = %q, %v", got, err)
	}
	for n := range len(empty) {
		fails(fmt.Sprintf("empty token truncated to %d", n), keyA, "widgets", empty[:n])
	}
}

// TestNewKeyNeedsExactly32Bytes checks the constructor that the tests of the
// other packages make keys with.
// Verifies: 11-REQ-3.1
func TestNewKeyNeedsExactly32Bytes(t *testing.T) {
	for _, n := range []int{0, 1, 31, 33, 64} {
		if _, err := NewKey(make([]byte, n)); err == nil {
			t.Errorf("NewKey accepted %d bytes", n)
		}
	}
	if _, err := NewKey(make([]byte, 32)); err != nil {
		t.Errorf("NewKey refused 32 bytes: %v", err)
	}
}

// TestTheZeroKeyNeitherSealsNorOpens guards against a key that was never loaded
// sealing under all-zero bytes.
// Verifies: 11-REQ-3.6
func TestTheZeroKeyNeitherSealsNorOpens(t *testing.T) {
	var zero Key
	if sealed, err := zero.Seal("widgets", "tok"); err == nil || sealed != nil {
		t.Errorf("the zero key sealed: %d bytes, %v", len(sealed), err)
	}
	sealed, err := mustKey(t, 1).Seal("widgets", "tok")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := zero.Open("widgets", sealed); err == nil || got != "" {
		t.Errorf("the zero key opened: %q, %v", got, err)
	}
}

// TestAKeyNeverPrintsItsBytes checks that no verb of fmt shows the key, so that
// logging one by accident does not leak it.
// Verifies: 11-REQ-3.6
func TestAKeyNeverPrintsItsBytes(t *testing.T) {
	raw := keyBytes(40)
	k, err := NewKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	needles := []string{
		base64.StdEncoding.EncodeToString(raw), base64.RawStdEncoding.EncodeToString(raw),
		fmt.Sprintf("%x", raw), fmt.Sprintf("%v", raw), string(raw),
	}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%T"} {
		for _, shown := range []string{fmt.Sprintf(verb, k), fmt.Sprintf(verb, &k), fmt.Sprintf(verb, []Key{k}), fmt.Sprintf(verb, struct{ K Key }{k})} {
			for _, needle := range needles {
				if strings.Contains(shown, needle) {
					t.Errorf("%s printed the key: %q", verb, shown)
				}
			}
		}
	}
	if s := k.String(); strings.Contains(s, needles[0]) || s == "" {
		t.Errorf("String() = %q", s)
	}
}
