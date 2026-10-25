package box

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

func TestFS_AnswersWhatIsInTheBox(t *testing.T) {
	dir := hostRepo(t, map[string]string{"README.md": "# readme\n", "src/main.go": "package main\n"})
	fsys := openLocal(t, dir, &ledger{}).FS(context.Background())

	for _, name := range []string{"missing", "src/missing", "src/missing/deeper", "README.md/under"} {
		_, serr := fs.Stat(fsys, name)
		_, rerr := fs.ReadFile(fsys, name)
		if !errors.Is(serr, fs.ErrNotExist) || !errors.Is(rerr, fs.ErrNotExist) {
			t.Errorf("%s: Stat %v, ReadFile %v, want fs.ErrNotExist", name, serr, rerr)
		}
	}
	for _, name := range []string{"../x", "/abs", "a//b", "src/../README.md", ""} {
		_, serr := fs.Stat(fsys, name)
		_, derr := fs.ReadDir(fsys, name)
		if !errors.Is(serr, fs.ErrInvalid) || !errors.Is(derr, fs.ErrInvalid) {
			t.Errorf("%q: Stat %v, ReadDir %v, want fs.ErrInvalid", name, serr, derr)
		}
	}
	if info, err := fs.Stat(fsys, "src"); err != nil || !info.IsDir() || info.Mode() != fs.ModeDir|0o555 {
		t.Errorf("Stat(src) = %v, %v, want a directory", info, err)
	}
	if info, err := fs.Stat(fsys, "README.md"); err != nil || info.IsDir() || info.Name() != "README.md" || info.Size() != 9 || info.Mode() != 0o444 {
		t.Errorf("Stat(README.md) = %v, %v, want a read-only file of 9 bytes", info, err)
	}
	if _, err := fs.ReadFile(fsys, "src"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadFile(src) = %v, want the error of a directory", err)
	}
	var names []string
	entries, err := fs.ReadDir(fsys, ".")
	for _, e := range entries {
		names = append(names, e.Name()+map[bool]string{true: "/"}[e.IsDir()])
	}
	if err != nil || !slices.Equal(names, []string{".git/", "README.md", "src/"}) {
		t.Errorf("ReadDir(.) = %v, %v, want the entries sorted, directories as such", names, err)
	}
}

func TestFS_ReadsTheExactBytesOfAFile(t *testing.T) {
	var many strings.Builder
	for i := range 5000 {
		fmt.Fprintf(&many, "line %d\n", i)
	}
	files := map[string]string{
		"no-newline":    "one\ntwo",
		"empty":         "",
		"crlf.txt":      "one\r\ntwo\r\n",
		"latin1.txt":    "caf\xe9\n",
		"binary.bin":    "\x00\x01\x02\xff\xfe\n\x00",
		"one-long-line": strings.Repeat("x", 120_000),
		"many-lines":    many.String(),
		"ünï/файл.txt":  "unicode name\n",
	}
	fsys := openLocal(t, hostRepo(t, files), &ledger{}).FS(context.Background())

	for name, want := range files {
		got, err := fs.ReadFile(fsys, name)
		info, serr := fs.Stat(fsys, name)
		if err != nil || string(got) != want || serr != nil || info.Size() != int64(len(want)) {
			t.Errorf("%s: %d bytes %.20q, %v; size %v, %v; want %d bytes %.20q", name, len(got), got, err, info, serr, len(want), want)
		}
	}
}

func TestFS_NeverFollowsALink(t *testing.T) {
	secret := testutil.WriteFile(t, t.TempDir(), "secret.txt", "the host's secret\n")
	s := openLocal(t, hostRepo(t, nil), &ledger{})
	inBox(t, s, "ln -s '"+secret+"' escape && ln -s file0.txt inside && ln -s ../../.. up")
	fsys := s.FS(context.Background())

	for _, name := range []string{"escape", "inside"} {
		if data, err := fs.ReadFile(fsys, name); len(data) > 0 || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("ReadFile(%s) = %q, %v, want nothing and fs.ErrPermission", name, data, err)
		}
	}
	for _, name := range []string{"escape", "inside", "up"} {
		if _, err := fs.Stat(fsys, name); err != nil {
			t.Errorf("Stat(%s): %v, want the link to be there", name, err)
		}
	}
	if entries, err := fs.ReadDir(fsys, "up"); err == nil || len(entries) > 0 {
		t.Errorf("ReadDir of a link out of the box = %v, %v, want an error", entries, err)
	}
}

func TestFS_ReadsFilesTogether(t *testing.T) {
	files := map[string]string{}
	for i := range 8 {
		files[fmt.Sprintf("file%d.txt", i)] = fmt.Sprintf("content %d\n", i)
	}
	fsys := openLocal(t, hostRepo(t, files), &ledger{}).FS(context.Background())

	var wg sync.WaitGroup
	errs := make(chan error, len(files))
	for name, want := range files {
		wg.Go(func() {
			if data, err := fs.ReadFile(fsys, name); err != nil || string(data) != want {
				errs <- fmt.Errorf("%s = %q, %v", name, data, err)
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestFS_IsAFileSystemAsFstestHasIt(t *testing.T) {
	dir := plainDir(t, map[string]string{"a.txt": "a\n", "sub/b.go": "package sub\n", "sub/deep/c.md": "# c\n", "empty": ""})
	fsys := openHere(t, dir, &ledger{}).FS(context.Background())

	if err := fstest.TestFS(fsys, "a.txt", "sub/b.go", "sub/deep/c.md", "empty"); err != nil {
		t.Error(err)
	}
}

func TestFS_StatsInADirectoryTooLongToList(t *testing.T) {
	files := map[string]string{"big/sub/x.txt": "x\n"}
	for i := range listLimit + 1 {
		files[fmt.Sprintf("big/f%03d.txt", i)] = "f\n"
	}
	fsys := openHere(t, plainDir(t, files), &ledger{}).FS(context.Background())

	if info, err := fs.Stat(fsys, "big/sub"); err != nil || !info.IsDir() {
		t.Errorf("Stat(big/sub) = %v, %v, want a directory", info, err)
	}
	if info, err := fs.Stat(fsys, "big"); err != nil || !info.IsDir() {
		t.Errorf("Stat(big) = %v, %v, want a directory", info, err)
	}
	if info, err := fs.Stat(fsys, "big/f010.txt"); err != nil || info.IsDir() || info.Size() != 2 {
		t.Errorf("Stat(big/f010.txt) = %v, %v, want a file of 2 bytes", info, err)
	}
	if data, err := fs.ReadFile(fsys, "big/sub/x.txt"); err != nil || string(data) != "x\n" {
		t.Errorf("ReadFile(big/sub/x.txt) = %q, %v", data, err)
	}
	for _, name := range []string{"big/missing", "big/sub/missing.txt"} {
		if _, err := fs.Stat(fsys, name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%s) = %v, want fs.ErrNotExist", name, err)
		}
	}
	if _, err := fs.ReadDir(fsys, "big"); err == nil || !strings.Contains(err.Error(), "more than 500 entries") {
		t.Errorf("ReadDir(big) = %v, want the error of a directory longer than the box lists", err)
	}
}
