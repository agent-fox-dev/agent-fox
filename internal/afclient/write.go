package afclient

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FS is the file system the credential file is written to.
type FS interface {
	MkdirAll(path string, perm fs.FileMode) error
	CreateTemp(dir, pattern string) (File, error)
	Rename(oldpath, newpath string) error
	Remove(name string) error
}

// File is a file that is being written.
type File interface {
	Name() string
	Write(p []byte) (int, error)
	Sync() error
	Chmod(mode fs.FileMode) error
	Close() error
}

// OSFS is the file system of the operating system.
type OSFS struct{}

// MkdirAll is os.MkdirAll.
func (OSFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }

// CreateTemp is os.CreateTemp.
func (OSFS) CreateTemp(dir, pattern string) (File, error) { return os.CreateTemp(dir, pattern) }

// Rename is os.Rename.
func (OSFS) Rename(oldpath, newpath string) error { return os.Rename(oldpath, newpath) }

// Remove is os.Remove.
func (OSFS) Remove(name string) error { return os.Remove(name) }

// StoreCredential writes the credential file f with credential in it (ExistingFile.With). The
// directory is created with mode 0700 when it is missing, the content goes to a temporary file in
// it, with mode 0600, and the temporary file is renamed over the old one, so that the file is
// either as it was or complete. A failure of any step is a CredentialIO error that names the
// file; the temporary file is removed, and an existing file is as it was.
func StoreCredential(fsys FS, f *ExistingFile, credential string) error {
	content, err := f.With(credential)
	if err != nil {
		return err
	}
	fail := func(what string, err error) error {
		return &CredentialError{Kind: CredentialIO, Path: f.Path, Message: fmt.Sprintf("cannot %s to write the credential file %s: %v", what, f.Path, err)}
	}
	dir := filepath.Dir(f.Path)
	if err := fsys.MkdirAll(dir, 0o700); err != nil {
		return fail("create "+dir, err)
	}
	tmp, err := fsys.CreateTemp(dir, filepath.Base(f.Path)+".*")
	if err != nil {
		return fail("create a temporary file in "+dir, err)
	}
	name := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = fsys.Remove(name)
		}
	}()
	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fail("write "+name, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fail("set the mode of "+name, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fail("sync "+name, err)
	}
	if err := tmp.Close(); err != nil {
		return fail("close "+name, err)
	}
	if err := fsys.Rename(name, f.Path); err != nil {
		return fail("replace "+f.Path+" with "+name, err)
	}
	renamed = true
	return nil
}
