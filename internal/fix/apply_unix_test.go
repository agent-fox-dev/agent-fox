//go:build unix

package fix

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

// A named pipe is neither a file nor a link, and is not the checkout's to take.
func TestApply_LeavesOutWhatIsNoFileAndNoLink(t *testing.T) {
	r := newRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
	export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "new.txt": file("new\n")})
	if err := syscall.Mkfifo(filepath.Join(export, "pipe"), 0o644); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	p, _ := applying(r, export)

	res, err := Apply(context.Background(), p)

	if err != nil || res != (ApplyResult{Written: 1, Ignored: 1}) {
		t.Errorf("Apply = %+v, %v, want the pipe left out and the file written", res, err)
	}
	if got := snapshotOf(t, r.Dir, "pipe"); len(got) != 0 {
		t.Errorf("the pipe reached the checkout: %v", got)
	}
}
