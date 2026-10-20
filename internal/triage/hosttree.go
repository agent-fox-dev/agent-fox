package triage

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// maxHostRead is the most of a file that is read to count its lines.
const maxHostRead = 32 << 20

// RegularFile says whether the file rel of the checkout dir, which the file view
// lists, is one a diagnosis may cite: a regular file, or a link that leads to one
// inside the checkout. The view lists a link whatever it leads to, and the
// directory of another repository, a submodule or a nested checkout, as a file.
func RegularFile(dir, rel string) bool {
	full := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(full)
	switch {
	case err != nil:
		return false
	case info.Mode().IsRegular():
		return true
	case info.Mode()&fs.ModeSymlink == 0:
		return false
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	if inside, err := filepath.Rel(root, target); err != nil || !filepath.IsLocal(inside) {
		return false
	}
	info, err = os.Stat(target)
	return err == nil && info.Mode().IsRegular()
}

// HostLines counts the lines of the files of the checkout dir as the host has
// them. It reads through a root, so that no link leads out of the checkout, takes
// only a regular file (a pipe or a device would block or never end), and reads at
// most maxHostRead bytes of it.
func HostLines(dir string) func(path string) (int, error) {
	return func(rel string) (int, error) {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return 0, err
		}
		defer func() { _ = root.Close() }()
		info, err := root.Stat(filepath.FromSlash(rel))
		if err != nil {
			return 0, err
		}
		if !info.Mode().IsRegular() {
			return 0, errors.New("it is no regular file")
		}
		if info.Size() > maxHostRead {
			return 0, fmt.Errorf("it is larger than the %d bytes that are read to count lines", maxHostRead)
		}
		f, err := root.Open(filepath.FromSlash(rel))
		if err != nil {
			return 0, err
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, maxHostRead+1))
		if err != nil {
			return 0, err
		}
		if len(data) > maxHostRead {
			return 0, fmt.Errorf("it is larger than the %d bytes that are read to count lines", maxHostRead)
		}
		return countLines(data), nil
	}
}
