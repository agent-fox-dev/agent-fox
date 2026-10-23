package afclient

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The kinds of SecretError.
const (
	// SecretIO: the source of the secret cannot be read.
	SecretIO = "io"
	// SecretContent: what was read breaks a rule of a secret.
	SecretContent = "usage"
)

const (
	// MaxSecretBytes is the longest secret, once the whitespace around it is trimmed.
	MaxSecretBytes = 4096

	// secretReadLimit is how much of a source is read: a guard on memory, past which the secret is too long.
	secretReadLimit = 64 << 10
)

// SecretError is a secret that cannot be read or used. Its message names the source and the
// flag, and never what was read.
type SecretError struct {
	// Kind is SecretIO or SecretContent.
	Kind    string
	Message string
}

func (e *SecretError) Error() string { return e.Message }

// ReadSecretFile reads a secret, a credential or a token, from the file path, or from stdin
// when path is "-". The whitespace around it is trimmed, and it must then be non-empty, on one
// line, valid UTF-8, free of control characters and at most MaxSecretBytes long. flag is the
// flag that named the source, which the messages name. A source that cannot be read gives a
// SecretIO error and a secret that breaks a rule a SecretContent one.
func ReadSecretFile(flag, path string, stdin io.Reader) (string, error) {
	source := path
	var data []byte
	var err error
	if path == "-" {
		source = "stdin"
		if stdin == nil {
			err = io.ErrUnexpectedEOF
		} else {
			data, err = io.ReadAll(io.LimitReader(stdin, secretReadLimit+1))
		}
	} else {
		data, err = readLimited(path)
	}
	if err != nil {
		return "", &SecretError{Kind: SecretIO, Message: fmt.Sprintf("cannot read %s, which %s names: %v", source, flag, err)}
	}

	content := func(rule string) error {
		return &SecretError{Kind: SecretContent, Message: fmt.Sprintf("%s: what was read from %s %s", flag, source, rule)}
	}
	if len(data) > secretReadLimit {
		return "", content(fmt.Sprintf("is longer than %d bytes", MaxSecretBytes))
	}
	secret := strings.TrimSpace(string(data))
	switch {
	case secret == "":
		return "", content("is empty")
	case !utf8.ValidString(secret):
		return "", content("is not valid UTF-8")
	case strings.ContainsAny(secret, "\r\n"):
		return "", content("spans more than one line")
	case strings.IndexFunc(secret, unicode.IsControl) >= 0:
		return "", content("holds a control character")
	case len(secret) > MaxSecretBytes:
		return "", content(fmt.Sprintf("is longer than %d bytes", MaxSecretBytes))
	}
	return secret, nil
}

// readLimited reads at most secretReadLimit+1 bytes of the file.
func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, secretReadLimit+1))
}
