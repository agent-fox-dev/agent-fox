package optin

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// recorder is a test that remembers how many times it was skipped, and why.
type recorder struct {
	skips  int
	reason string
}

func (r *recorder) Helper() {}

func (r *recorder) Skipf(format string, args ...any) {
	r.skips++
	r.reason = fmt.Sprintf(format, args...)
}

func TestRequirePodman_SkipsOnceNamingWhatIsMissing(t *testing.T) {
	up := func() error { return nil }
	down := func() error { return errors.New("engine-down") }
	present := func(string) error { return nil }
	gone := func(string) error { return errors.New("image-gone") }
	for _, c := range []struct {
		name    string
		env     string
		host    host
		want    string // part of the reason; "" for a test that runs
		notWant string
	}{
		{"the variable unset", "", host{down, gone}, "AF_TEST_PODMAN is not 1: set it to run the tests that need a real podman box", "engine-down"},
		{"the variable not 1", "yes", host{up, present}, "AF_TEST_PODMAN is not 1", ""},
		{"no engine", "1", host{down, gone}, "AF_TEST_PODMAN=1 is set, but podman info fails: engine-down", "image-gone"},
		{"no image", "1", host{up, gone}, "the image img:1 is not there (image-gone): build it with make build-tools-container, or name another with AF_SANDBOX_IMAGE", ""},
		{"everything", "1", host{up, present}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			old := probe
			probe = c.host
			t.Cleanup(func() { probe = old })
			t.Setenv(PodmanVar, c.env)
			var r recorder

			RequirePodman(&r, "img:1")

			if c.want == "" {
				if r.skips != 0 {
					t.Errorf("a host with everything skipped the test: %s", r.reason)
				}
				return
			}
			if r.skips != 1 || !strings.Contains(r.reason, c.want) || (c.notWant != "" && strings.Contains(r.reason, c.notWant)) {
				t.Errorf("skipped %d times, last with %q; want once, naming %q and not %q", r.skips, r.reason, c.want, c.notWant)
			}
		})
	}
}

func TestRequirePodman_SkipsARealTestAndDoesNotFailIt(t *testing.T) {
	t.Setenv(PodmanVar, "")
	t.Run("gated", func(t *testing.T) {
		RequirePodman(t, "img:1")
		t.Error("the gate did not skip")
	})
}
