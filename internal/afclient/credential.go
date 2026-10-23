package afclient

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// CredentialFile is the file the credential is read from, relative to the home directory.
var CredentialFile = filepath.Join(".af", "config.toml")

// tokenKey is the top-level key of the credential file that holds the credential.
const tokenKey = "token"

// The kinds of CredentialError.
const (
	// CredentialIO: the home directory cannot be found, or the file exists and cannot be read.
	CredentialIO = "io"
	// CredentialAuth: there is no credential: no file, or no usable token in it.
	CredentialAuth = "auth"
	// CredentialConfig: the file does not parse, or the token holds a line break or a control character.
	CredentialConfig = "config"
)

// CredentialError is a credential that cannot be used. Its message names the file and never the token.
type CredentialError struct {
	// Kind is CredentialIO, CredentialAuth or CredentialConfig.
	Kind string
	// Path is the credential file, or "~/.af/config.toml" when the home directory is unknown.
	Path    string
	Message string
}

func (e *CredentialError) Error() string { return e.Message }

// Credential is the hub credential, as the credential file holds it.
type Credential struct {
	Token string
	// Path is the credential file.
	Path string
	// Loose is true when, on Unix, a group or other permission bit of the file is set.
	Loose bool
}

// LoadCredential reads the credential from the top-level string key token of
// ~/.af/config.toml, where home gives the home directory (os.UserHomeDir for nil). Every
// other key of the file is ignored.
func LoadCredential(home func() (string, error)) (Credential, error) {
	path, err := credentialPath(home)
	if err != nil {
		return Credential{}, err
	}
	data, rerr := os.ReadFile(path)
	switch {
	case errors.Is(rerr, fs.ErrNotExist):
		return Credential{}, noCredential(path, "does not exist")
	case rerr != nil:
		return Credential{}, &CredentialError{Kind: CredentialIO, Path: path, Message: fmt.Sprintf("cannot read the credential file %s: %v", path, rerr)}
	}

	var doc map[string]any
	if _, err := toml.Decode(string(data), &doc); err != nil {
		return Credential{}, parseError(path, err)
	}
	token, ok := doc[tokenKey].(string)
	if !ok {
		return Credential{}, noCredential(path, "has no top-level string key `token`")
	}
	if strings.IndexFunc(token, unicode.IsControl) >= 0 {
		return Credential{}, &CredentialError{Kind: CredentialConfig, Path: path, Message: fmt.Sprintf("the token of the credential file %s holds a line break or a control character", path)}
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return Credential{}, noCredential(path, "has an empty `token`")
	}

	return Credential{Token: token, Path: path, Loose: isLoose(path)}, nil
}

// isLoose says whether, on Unix, the file has a group or other permission bit set.
func isLoose(path string) bool {
	if runtime.GOOS == "windows" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm()&0o077 != 0
}

// noCredential is the error for a credential that is not there: what is wrong with the file, and how to store one.
func noCredential(path, what string) error {
	return &CredentialError{Kind: CredentialAuth, Path: path,
		Message: fmt.Sprintf("the credential file %s %s; run `af login --token-file FILE` or `afc login` to store a credential", path, what)}
}

// credentialPath is the credential file of the home directory that home gives (os.UserHomeDir
// for nil). A home that cannot be found is a CredentialIO error that names ~/.af/config.toml.
func credentialPath(home func() (string, error)) (string, error) {
	if home == nil {
		home = os.UserHomeDir
	}
	dir, err := home()
	if err != nil {
		path := "~/" + filepath.ToSlash(CredentialFile)
		return "", &CredentialError{Kind: CredentialIO, Path: path,
			Message: fmt.Sprintf("the home directory cannot be found, so %s cannot be read: %v", path, err)}
	}
	return filepath.Join(dir, CredentialFile), nil
}

// parseError is the error for a credential file that does not parse as TOML. The message of
// the parser can quote what it read, so only the line is given.
func parseError(path string, err error) error {
	where := ""
	var perr toml.ParseError
	if errors.As(err, &perr) && perr.Position.Line > 0 {
		where = fmt.Sprintf(" (line %d)", perr.Position.Line)
	}
	return &CredentialError{Kind: CredentialConfig, Path: path, Message: fmt.Sprintf("the credential file %s does not parse as TOML%s", path, where)}
}
