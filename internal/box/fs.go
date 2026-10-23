package box

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentkit-go/core"
)

// FS is a read-only view of the box's tree, built on the box's tools: what
// exists is told by the list tool, and what a file holds by exporting that one
// file out of the box. The calls go through ctx, as the host's own hands and
// not as a brain's.
//
// The read tool is not used for content. It shows lines, at most 2000 of them
// and 50 KB, without the terminator of the last; and a file that is not valid
// UTF-8 makes the agent fail to encode its answer, which takes the whole box
// down (every later call is sandbox_unavailable), as the real local box does.
// An export is exact, has no limit, never carries a link that leaves the
// workspace, and is safe for any bytes.
//
// A missing path is fs.ErrNotExist, and so is a path below a file. A symbolic
// link is not followed, which the export does not do either: it stats as a file
// or a directory, and reading it is fs.ErrPermission (a link to a directory in
// the middle of a path is followed by the list tool when it stays in the
// workspace). A directory stats as one. A directory lists at most the 500
// entries the list tool gives, and a longer one is an error, not a part of the
// truth. A file is read whole, up to maxReadBytes. The size of a file is found
// when it is asked for, because it takes a read.
func (s *Session) FS(ctx context.Context) fs.FS {
	if s.none {
		return s.root.FS()
	}
	return &treeFS{s: s, ctx: ctx}
}

type treeFS struct {
	s   *Session
	ctx context.Context
}

const (
	// listLimit is the most entries the list tool gives.
	listLimit = 500
	// maxReadBytes is the most a file may hold to be read through the view.
	maxReadBytes = 32 << 20
)

var (
	errIsDir   = errors.New("is a directory")
	errNotDir  = errors.New("not a directory")
	errTooMany = fmt.Errorf("the directory has more than %d entries, which is as many as the box lists", listLimit)
	errTooBig  = fmt.Errorf("the file is larger than %d bytes, which is as much as the view reads", maxReadBytes)
)

func (t *treeFS) Open(name string) (fs.File, error) {
	info, err := t.Stat(name)
	if err != nil {
		return nil, wrap("open", name, err)
	}
	if info.IsDir() {
		entries, err := t.ReadDir(name)
		if err != nil {
			return nil, wrap("open", name, err)
		}
		return &dirFile{info: info, entries: entries}, nil
	}
	data, err := t.ReadFile(name)
	if err != nil {
		return nil, wrap("open", name, err)
	}
	return &memFile{info: &fileInfo{name: path.Base(name), known: true, size: int64(len(data))}, Reader: bytes.NewReader(data)}, nil
}

func (t *treeFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return &fileInfo{name: ".", dir: true}, nil
	}
	parent, base := path.Split(name)
	entries, err := t.list(strings.TrimSuffix(parent, "/"))
	switch {
	case errors.Is(err, errNotDir):
		err = fs.ErrNotExist // a directory of the path is a file
	case errors.Is(err, errTooMany):
		return t.statInLongDir(name)
	}
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	for _, e := range entries {
		if e.Name() == base {
			info, _ := e.Info()
			fi := *info.(*fileInfo)
			fi.t, fi.path = t, name
			return &fi, nil
		}
	}
	return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
}

// statInLongDir stats a path in a directory that is too long to list. What the
// list tool says of the path itself tells a directory (it lists, or lists too
// long) from a file (it is not a directory), and a file is then exported for its
// size.
func (t *treeFS) statInLongDir(name string) (fs.FileInfo, error) {
	_, err := t.list(name)
	switch {
	case err == nil || errors.Is(err, errTooMany):
		return &fileInfo{name: path.Base(name), dir: true}, nil
	case !errors.Is(err, errNotDir):
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	data, err := t.exportOne(name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: err}
	}
	return &fileInfo{name: path.Base(name), known: true, size: int64(len(data))}, nil
}

func (t *treeFS) ReadFile(name string) ([]byte, error) {
	info, err := t.Stat(name)
	if err != nil {
		return nil, wrap("read", name, err)
	}
	if info.IsDir() {
		return nil, &fs.PathError{Op: "read", Path: name, Err: errIsDir}
	}
	data, err := t.exportOne(name)
	if err != nil {
		return nil, &fs.PathError{Op: "read", Path: name, Err: err}
	}
	return data, nil
}

// exportOne reads a file out of the box by exporting it into a directory of its
// own.
func (t *treeFS) exportOne(name string) ([]byte, error) {
	dir, err := os.MkdirTemp("", "af-read-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := t.s.export(t.ctx, name, dir); err != nil {
		if strings.Contains(err.Error(), "symlink") {
			// The box exports no link, whatever it leads to.
			return nil, fmt.Errorf("%w: a symbolic link is not followed", fs.ErrPermission)
		}
		return nil, t.s.goneIf(isGone(err), fmt.Errorf("exporting it out of the box: %w", err))
	}
	file := filepath.Join(dir, path.Base(name))
	if info, err := os.Lstat(file); err == nil && info.Size() > maxReadBytes {
		return nil, errTooBig
	}
	return os.ReadFile(file)
}

func (t *treeFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	entries, err := t.list(name)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: err}
	}
	return entries, nil
}

// list lists a directory with the list tool.
func (t *treeFS) list(name string) ([]fs.DirEntry, error) {
	if name == "" {
		name = "."
	}
	r, err := t.s.call(t.ctx, toolListFiles, map[string]any{"path": name, "limit": listLimit})
	if err != nil {
		return nil, err
	}
	if !r.OK {
		return nil, toolError(r)
	}
	if truncated, _ := r.Data["truncated"].(bool); truncated {
		return nil, errTooMany
	}
	var names []string
	switch v := r.Data["entries"].(type) {
	case []string:
		names = v
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				names = append(names, s)
			}
		}
	}
	entries := make([]fs.DirEntry, 0, len(names))
	for _, n := range names {
		dir := strings.HasSuffix(n, "/")
		base := strings.TrimSuffix(n, "/")
		entries = append(entries, &dirEntry{info: fileInfo{name: base, dir: dir, t: t, path: path.Join(name, base)}})
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// toolError is what a failed result of a list tool says, as an error that is
// fs.ErrNotExist when the path is not there, errNotDir when a directory of it
// is a file, and fs.ErrPermission when the box refuses it, as a path that
// leads out of the workspace.
func toolError(r core.ToolResult) error {
	detail := strings.ToLower(r.Detail)
	switch {
	case resultGone(r):
		return gone(fmt.Errorf("%s: %s", r.Error, r.Detail))
	case r.Error == "path_not_allowed":
		return fmt.Errorf("%w: %s", fs.ErrPermission, r.Detail)
	case strings.Contains(detail, "no such file or directory"), strings.Contains(detail, "cannot find the"), strings.Contains(detail, "does not exist"):
		return fmt.Errorf("%w: %s", fs.ErrNotExist, r.Detail)
	case strings.Contains(detail, "not a directory"):
		return fmt.Errorf("%w: %s", errNotDir, r.Detail)
	}
	return fmt.Errorf("%s: %s", r.Error, r.Detail)
}

// wrap puts an operation and a name on an error that has none.
func wrap(op, name string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return &fs.PathError{Op: op, Path: name, Err: pe.Err}
	}
	return &fs.PathError{Op: op, Path: name, Err: err}
}

// fileInfo is what the view knows of a file: its name, whether it is a
// directory, and its size, which takes a read and is found when it is asked
// for. The tools report no time or mode.
type fileInfo struct {
	name string
	dir  bool

	t     *treeFS // where the size is read from; nil when it is known
	path  string
	known bool
	size  int64
}

func (i *fileInfo) Name() string { return i.name }

// Size is the size of the file; 0 for a directory, and for a file that cannot
// be read.
func (i *fileInfo) Size() int64 {
	if !i.known && !i.dir && i.t != nil {
		if data, err := i.t.exportOne(i.path); err == nil {
			i.size = int64(len(data))
		}
		i.known = true
	}
	return i.size
}

func (i *fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o555
	}
	return 0o444
}
func (i *fileInfo) ModTime() time.Time { return time.Time{} }
func (i *fileInfo) IsDir() bool        { return i.dir }
func (i *fileInfo) Sys() any           { return nil }

// dirEntry is an entry of a listing.
type dirEntry struct{ info fileInfo }

func (e *dirEntry) Name() string               { return e.info.name }
func (e *dirEntry) IsDir() bool                { return e.info.dir }
func (e *dirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e *dirEntry) Info() (fs.FileInfo, error) { return &e.info, nil }

// memFile is an open file: its content, read whole.
type memFile struct {
	info fs.FileInfo
	*bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f.info, nil }
func (f *memFile) Close() error               { return nil }

// dirFile is an open directory.
type dirFile struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	read    int
}

func (d *dirFile) Stat() (fs.FileInfo, error) { return d.info, nil }
func (d *dirFile) Close() error               { return nil }
func (d *dirFile) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.info.Name(), Err: errIsDir}
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.read:]
	if n <= 0 {
		d.read = len(d.entries)
		return slices.Clone(rest), nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(rest))
	d.read += n
	return slices.Clone(rest[:n]), nil
}
