// Package agentdir owns the .agent-fox directory af keeps under a checkout.
package agentdir

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Name is the name of the directory under the checkout.
const Name = ".agent-fox"

// GitignoreContent is what <dir>/.agent-fox/.gitignore holds: it ignores
// everything under .agent-fox, itself included, without touching the
// repository's own .gitignore.
const GitignoreContent = "*\n"

// Ensure creates <dir>/.agent-fox and its .gitignore when they are missing
// and returns the directory's path. dir itself must exist: a mistyped
// directory is an error, not a tree of new ones. The .gitignore is opened
// exclusively, so that runs starting together neither fail nor overwrite one
// another, and one that is there already is left as it is.
func Ensure(dir string) (string, error) {
	agent := filepath.Join(dir, Name)
	if err := os.Mkdir(agent, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(agent, ".gitignore"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return agent, nil
	}
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(GitignoreContent)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return "", werr
	}
	return agent, nil
}
