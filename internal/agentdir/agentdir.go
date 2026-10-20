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

// WriteFileAtomic writes data to path through a temporary file in the same
// directory and a rename, so that a reader sees the old file or the new one
// and never half of one, and runs that write together leave the last
// writer's complete file. The directory must exist; the file is made 0644.
func WriteFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	committed = true
	return nil
}
