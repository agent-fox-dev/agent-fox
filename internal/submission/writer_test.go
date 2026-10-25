package submission

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// rootWriter is RootWriter over a new directory, and the directory.
func rootWriter(t *testing.T) (func(context.Context, string, []byte) error, string) {
	t.Helper()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return RootWriter(root), dir
}

func TestRootWriter_ReplacesAFileWholeAndLeavesNoTemporaryFile(t *testing.T) {
	write, dir := rootWriter(t)
	path := filepath.Join(dir, "a", "b", "file.json")
	stop, torn := make(chan struct{}), make(chan string, 1)
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if data, err := os.ReadFile(path); err == nil && !json.Valid(data) {
				select {
				case torn <- string(data):
				default:
				}
			}
		}
	})

	for i := range 100 {
		if err := write(context.Background(), "a/b/file.json", fmt.Appendf(nil, `{"n":%d,"pad":%q}`, i, strings.Repeat("p", 20000))); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()

	select {
	case data := <-torn:
		t.Errorf("a reader saw a partial file of %d bytes", len(data))
	default:
	}
	files := testutil.Files(t, dir)
	if len(files) != 1 || !strings.HasPrefix(files["a/b/file.json"], `{"n":99,`) {
		t.Errorf("the directory holds %d files, want the last file.json only", len(files))
	}
}

func TestRootWriter_WritesNothingOutsideTheRoot(t *testing.T) {
	write, dir := rootWriter(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, dir, "taken/x", "")
	for _, rel := range []string{".", "", "../x", "/abs", "link/x", "taken"} {
		if err := write(context.Background(), rel, []byte("{}")); err == nil {
			t.Errorf("writing %q succeeded", rel)
		}
	}
	if files := testutil.Files(t, outside); len(files) != 0 {
		t.Errorf("the link led out to %v", files)
	}
	if files := testutil.Files(t, dir); len(files) != 1 {
		t.Errorf("the root holds %v, want taken/x only", files)
	}
}
