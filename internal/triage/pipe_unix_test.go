//go:build unix

package triage

import "syscall"

// makePipe makes a named pipe, which a read waits on until someone writes to it.
func makePipe(path string) error { return syscall.Mkfifo(path, 0o644) }
