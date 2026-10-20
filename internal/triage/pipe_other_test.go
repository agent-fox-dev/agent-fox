//go:build !unix

package triage

import "errors"

// makePipe makes none where there are no named pipes.
func makePipe(string) error { return errors.New("no named pipes here") }
