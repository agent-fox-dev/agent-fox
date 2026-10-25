package secrets_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/secrets"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func newKey(t *testing.T) secrets.Key {
	t.Helper()
	k, err := secrets.NewKey(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// TestSeal_IsVersionNonceAndAESGCMBoundToTheSlug opens a sealed value by hand, as any reader of the stored format would.
func TestSeal_IsVersionNonceAndAESGCMBoundToTheSlug(t *testing.T) {
	const token = "pat-0123456789abcdef"
	sealed, err := newKey(t).Seal("widgets", token)
	if err != nil {
		t.Fatal(err)
	}
	if sealed[0] != 0x01 || len(sealed) != 1+12+len(token)+16 || bytes.Contains(sealed, []byte(token)) {
		t.Fatalf("sealed = %x, want 0x01, a 12-byte nonce and the ciphertext with its tag", sealed)
	}
	block, err := aes.NewCipher(testutil.Key())
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := gcm.Open(nil, sealed[1:13], sealed[13:], []byte("agent-fox/subscription/widgets")); err != nil || string(plain) != token {
		t.Errorf("AES-GCM with the slug's additional data gave %q, %v; want the token", plain, err)
	}
}

func TestSealThenOpen_RoundTripsAndNoTwoSealsAreEqual(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 21))
	k := newKey(t)
	for i := range 200 {
		b := make([]byte, rng.IntN(300))
		for j := range b {
			b[j] = byte(rng.IntN(256))
		}
		tok, slug := string(b), fmt.Sprintf("ws-%d", i)
		first, err1 := k.Seal(slug, tok)
		second, err2 := k.Seal(slug, tok)
		if err1 != nil || err2 != nil || bytes.Equal(first, second) {
			t.Fatalf("two seals of a %d-byte token: %v, %v, equal %v", len(tok), err1, err2, bytes.Equal(first, second))
		}
		if got, err := k.Open(slug, first); err != nil || got != tok {
			t.Fatalf("Open(Seal(%d-byte token)) = %d bytes, %v", len(tok), len(got), err)
		}
	}
}

func TestOpen_FailsWithErrCannotOpenAndNoPlaintext(t *testing.T) {
	keyA := newKey(t)
	other := testutil.Key()
	other[0] ^= 0xff
	keyB, err := secrets.NewKey(other)
	if err != nil {
		t.Fatal(err)
	}
	const token = "pat-secret-value"
	sealed, err := keyA.Seal("widgets", token)
	if err != nil {
		t.Fatal(err)
	}
	type tc struct {
		key   secrets.Key
		slug  string
		value []byte
	}
	cases := map[string]tc{
		"a wrong key":    {keyB, "widgets", sealed},
		"the zero key":   {secrets.Key{}, "widgets", sealed},
		"another slug":   {keyA, "gadgets", sealed},
		"nil":            {keyA, "widgets", nil},
		"trailing bytes": {keyA, "widgets", append(bytes.Clone(sealed), 0)},
	}
	for _, v := range []byte{0x00, 0x02} {
		altered := bytes.Clone(sealed)
		altered[0] = v
		cases[fmt.Sprintf("version %#x", v)] = tc{keyA, "widgets", altered}
	}
	for i := range sealed {
		altered := bytes.Clone(sealed)
		altered[i] ^= 0x01
		cases[fmt.Sprintf("byte %d flipped", i)] = tc{keyA, "widgets", altered}
		cases[fmt.Sprintf("truncated to %d", i)] = tc{keyA, "widgets", sealed[:i]}
	}
	for name, c := range cases {
		got, err := c.key.Open(c.slug, c.value)
		if got != "" || !errors.Is(err, secrets.ErrCannotOpen) || strings.Contains(fmt.Sprint(err), token) {
			t.Errorf("%s: Open = %q, %v; want ErrCannotOpen and no plaintext", name, got, err)
		}
	}
}

func TestSeal_TheZeroKeySealsNothing(t *testing.T) {
	if sealed, err := (secrets.Key{}).Seal("widgets", "tok"); err == nil || sealed != nil {
		t.Errorf("the zero key sealed: %x, %v", sealed, err)
	}
}
