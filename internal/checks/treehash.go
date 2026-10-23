package checks

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/agent-fox-dev/agentfox/internal/agentdir"
)

// TreeHash is the SHA-256 of the file view of dir, as 64 lowercase hex
// characters. It is about content only: the same files, contents, executable
// bits and symlink targets hash the same wherever the directory is, whatever
// commit HEAD points at, and whatever ignored files are around, which is what
// lets a gate run inside a box be a cache hit outside it.
//
// An entry holds, for a regular file, its kind, executable bit, path and the
// SHA-256 of its content; for a symlink its target, which is not followed;
// for anything else its type, and the file is not opened. A file that cannot
// be read is an entry with a fixed "unreadable" marker in place of its
// content, and does not fail the call.
//
// The .agent-fox directory and its .gitignore are made first, so that nothing
// af writes there ever shows in git status.
func TreeHash(ctx context.Context, dir string) (string, error) {
	if _, err := agentdir.Ensure(dir); err != nil {
		return "", fmt.Errorf("tree hash: %w", err)
	}
	view, err := FileView(ctx, dir)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	for _, p := range view {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		hashEntry(h, dir, p)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// hashEntry adds the entry for the file p of dir to h. Every field is written
// with its length, so that two trees cannot hash alike by moving a boundary.
func hashEntry(h hash.Hash, dir, p string) {
	abs := filepath.Join(dir, filepath.FromSlash(p))
	info, err := os.Lstat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return // gone since the view was taken
	case err != nil:
		field(h, "unreadable")
		field(h, p)
		return
	}

	mode := info.Mode()
	switch {
	case mode.IsRegular():
		field(h, "file")
		field(h, executable(mode))
		field(h, p)
		if sum, err := contentHash(abs); err != nil {
			field(h, "unreadable")
		} else {
			field(h, string(sum))
		}
	case mode&os.ModeSymlink != 0:
		target, err := os.Readlink(abs)
		if err != nil {
			field(h, "unreadable")
			field(h, p)
			return
		}
		field(h, "symlink")
		field(h, p)
		field(h, target)
	default:
		field(h, fileType(mode))
		field(h, p)
	}
}

// field writes s to h preceded by its length.
func field(h hash.Hash, s string) {
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(s)))
	h.Write(n[:])
	_, _ = io.WriteString(h, s)
}

// executable is the executable bit of mode, as git keeps it: the owner's.
func executable(mode fs.FileMode) string {
	if mode&0o100 != 0 {
		return "x"
	}
	return "-"
}

// contentHash is the SHA-256 of a file's content.
func contentHash(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// fileType names the type of a file that is neither regular nor a symlink.
func fileType(mode fs.FileMode) string {
	switch {
	case mode.IsDir():
		return "dir"
	case mode&os.ModeNamedPipe != 0:
		return "pipe"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeCharDevice != 0:
		return "chardevice"
	case mode&os.ModeDevice != 0:
		return "device"
	}
	return "irregular"
}
