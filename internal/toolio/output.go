package toolio

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// writeAtomic writes data to path so that a reader polling path sees either
// nothing yet or the complete document, never a partial one: the parent
// directory is created when missing, a temp file is created in that same
// directory (so the rename cannot cross a filesystem), written, synced and
// closed, then renamed over path. The temp file is removed on every failure
// branch. It is the same idiom afspec.Save and codeimpl's saveTasks use.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("creating a temp file in %s: %w", dir, err)
	}
	name := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return fmt.Errorf("writing %s: %w", name, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return fmt.Errorf("syncing %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("closing %s: %w", name, err)
	}
	// CreateTemp makes the file 0600; the document is an ordinary output.
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("setting the mode of %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("renaming %s to %s: %w", name, path, err)
	}
	return nil
}

// EmitWithOutput is Emit with a durable second copy: when outPath is not
// empty, the very bytes about to be written to w are first written
// atomically to outPath, so the file is complete before any byte reaches
// stdout. That includes Emit's own marshal-failure fallback: the same
// minimal envelope goes to both. A failure to write outPath never changes
// what is written to w or the exit code returned; here it is ignored, since
// on the fallback path there is no further envelope left to carry a warning
// about it. It returns the exit code, as Emit does.
func EmitWithOutput(w io.Writer, outPath string, env Envelope) int {
	b, code := marshalEnvelope(env)
	if outPath != "" {
		_ = writeAtomic(outPath, b)
	}
	_, _ = w.Write(b)
	return code
}
