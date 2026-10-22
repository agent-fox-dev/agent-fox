// Package secrets loads the deployment key of the agent-fox service and seals
// the workspace tokens it keeps with it.
//
// A token is sealed with AES-256-GCM under a fresh 12-byte nonce, with the
// additional data agent-fox/subscription/<slug>, which binds the ciphertext to
// its row. The sealed value is one version byte (0x01), the nonce, and the
// ciphertext with its tag. The key and a plaintext token are never part of an
// error message.
package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
)

// The layout of a sealed value.
const (
	keySize     = 32
	nonceSize   = 12
	tagSize     = 16
	versionByte = 0x01

	// maxKeyFile bounds what is read of a key file: the base64 of 32 bytes is 44
	// characters, so anything near this is not a key.
	maxKeyFile = 4 << 10
)

// ErrCannotOpen is what every failure to open a sealed value wraps: a wrong key,
// another slug, a byte that was altered, a version this binary does not know and
// a value that is too short.
var ErrCannotOpen = errors.New("sealed value cannot be opened")

// Key is the 32-byte key that workspace tokens are sealed with. The zero Key
// holds no key and neither seals nor opens. A Key prints as a placeholder under
// every verb of fmt, so logging one by accident leaks nothing.
type Key struct {
	aead cipher.AEAD
}

// NewKey makes a key from exactly 32 bytes.
func NewKey(b []byte) (Key, error) {
	if len(b) != keySize {
		return Key{}, fmt.Errorf("a key is %d bytes, got %d", keySize, len(b))
	}
	block, err := aes.NewCipher(b)
	if err != nil {
		return Key{}, errors.New("the key was refused by AES")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Key{}, errors.New("AES-GCM could not be set up")
	}
	return Key{aead: aead}, nil
}

// LoadKey reads the key file at path: the standard base64 encoding of exactly
// 32 bytes, with whitespace around it ignored. On Unix a file that has any group
// or other permission bit set is refused, whatever its owner bits are. An error
// names the file and the reason and never holds what the file holds.
func LoadKey(path string) (Key, error) {
	f, err := os.Open(path)
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: cannot be read: %s", path, reason(err))
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: cannot be read: %s", path, reason(err))
	}
	if !info.Mode().IsRegular() {
		return Key{}, fmt.Errorf("key file %s: is not a regular file", path)
	}
	if mode := info.Mode().Perm(); runtime.GOOS != "windows" && mode&0o077 != 0 {
		return Key{}, fmt.Errorf("key file %s: has mode %04o, but group and other must have no access (chmod 600 %s)", path, mode, path)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxKeyFile+1))
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: cannot be read: %s", path, reason(err))
	}
	if len(data) > maxKeyFile {
		return Key{}, fmt.Errorf("key file %s: is too large to hold a key", path)
	}
	raw, err := base64.StdEncoding.DecodeString(string(bytes.TrimSpace(data)))
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: is not standard base64", path)
	}
	if len(raw) != keySize {
		return Key{}, fmt.Errorf("key file %s: decodes to %d bytes, want %d", path, len(raw), keySize)
	}
	k, err := NewKey(raw)
	if err != nil {
		return Key{}, fmt.Errorf("key file %s: %w", path, err)
	}
	return k, nil
}

// reason is the cause of an error of the file system, without the path that
// the error repeats.
func reason(err error) string {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err.Error()
	}
	return err.Error()
}

// additionalData binds a sealed token to the subscription it belongs to.
func additionalData(slug string) []byte {
	return []byte("agent-fox/subscription/" + slug)
}

// Seal seals token for the subscription slug. Two seals of the same token
// differ, for each has a nonce of its own.
func (k Key) Seal(slug, token string) ([]byte, error) {
	if k.aead == nil {
		return nil, errors.New("there is no key to seal with")
	}
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("no nonce could be made")
	}
	sealed := make([]byte, 0, 1+nonceSize+len(token)+tagSize)
	sealed = append(sealed, versionByte)
	sealed = append(sealed, nonce...)
	return k.aead.Seal(sealed, nonce, []byte(token), additionalData(slug)), nil
}

// Open opens a value that Seal made for slug with this key. Every failure wraps
// ErrCannotOpen, and none returns any plaintext.
func (k Key) Open(slug string, sealed []byte) (string, error) {
	if k.aead == nil {
		return "", fmt.Errorf("%w: there is no key to open with", ErrCannotOpen)
	}
	if len(sealed) < 1+nonceSize+tagSize {
		return "", fmt.Errorf("%w: the value is too short", ErrCannotOpen)
	}
	if sealed[0] != versionByte {
		return "", fmt.Errorf("%w: the version %#x is not known", ErrCannotOpen, sealed[0])
	}
	plain, err := k.aead.Open(nil, sealed[1:1+nonceSize], sealed[1+nonceSize:], additionalData(slug))
	if err != nil {
		return "", fmt.Errorf("%w: the key, the slug or the value is not the one it was sealed with", ErrCannotOpen)
	}
	return string(plain), nil
}

// Format prints a key as a placeholder under every verb.
func (k Key) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, k.String())
}

// String is what a key prints as.
func (k Key) String() string {
	if k.aead == nil {
		return "secrets.Key(none)"
	}
	return "secrets.Key(redacted)"
}
