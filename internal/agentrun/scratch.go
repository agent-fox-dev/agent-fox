package agentrun

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// A writing phase gets a scratch directory: somewhere for a throwaway script,
// a copy made before an edit, a mutation-testing harness. The file tools are
// confined to the repository, so without one the model wrote such files into
// the change, and a later step had to find and remove them.
//
// It lives inside the workspace, the only place the file tools reach, under
// .agent-fox/scratch/, which a .gitignore of its own (`*`, itself included)
// hides from git without touching the repository's ignore files: no commit,
// untracked-file listing, scope check or structural scan sees it. It lasts
// one phase — created before it, removed when it returns, however it returns
// — so it never exists while the project's own checks run, some of which
// (`gofmt -l .`) do walk dot-directories. Each phase has its own random
// subdirectory, so two runs in one checkout do not share or remove each
// other's.

// scratchRoot is the scratch area's directory, relative to the workspace.
const scratchRoot = ".agent-fox/scratch"

// newScratch creates the phase's scratch directory under root and returns it
// relative to root, and the function that removes it. The function removes
// the area and .agent-fox too when nothing else is left in them.
func newScratch(root string) (string, func(), error) {
	area := filepath.Join(root, filepath.FromSlash(scratchRoot))
	if err := os.MkdirAll(area, 0o755); err != nil {
		return "", nil, err
	}
	ignore := filepath.Join(area, ".gitignore")
	if _, err := os.Stat(ignore); err != nil {
		if err := os.WriteFile(ignore, []byte("*\n"), 0o644); err != nil {
			return "", nil, err
		}
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	name := hex.EncodeToString(b[:])
	dir := filepath.Join(area, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.RemoveAll(dir)
		entries, err := os.ReadDir(area)
		if err != nil {
			return
		}
		if len(entries) == 1 && entries[0].Name() == ".gitignore" {
			_ = os.Remove(ignore)
			_ = os.Remove(area)
			_ = os.Remove(filepath.Dir(area)) // .agent-fox, when it is empty
		}
	}
	return scratchRoot + "/" + name, cleanup, nil
}

// writes reports whether a phase's tools can write: the file tools or a
// shell.
func writes(builtin []string) bool {
	for _, n := range builtin {
		switch n {
		case "write_file", "edit_file", "execute", "run_command", "powershell":
			return true
		}
	}
	return false
}
