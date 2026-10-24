// Package optin gates the tests that need what make check does not have: a real
// Landlock kernel or a container engine. A gate skips the test, with a reason that
// names the variable or the prerequisite that is missing, and never fails it, so that
// make check stays offline and free on any machine.
package optin

import (
	"os"
	"os/exec"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// The variables that switch the tests on; each is 1 to say yes.
const (
	LandlockVar = "AF_TEST_LANDLOCK"
	PodmanVar   = "AF_TEST_PODMAN"
)

// Skipper is what the gates need of a test; *testing.T is one.
type Skipper interface {
	Helper()
	Skipf(format string, args ...any)
}

// host is what the gates ask of the machine; a test replaces it.
type host struct {
	// landlock is nil when this host can give a landlock box: Linux with Landlock ABI
	// 4 or newer.
	landlock func() error
	// podman is nil when the engine answers, and image when the image is there.
	podman func() error
	image  func(image string) error
}

var probe = host{
	landlock: func() error {
		entry, err := box.Default.Lookup("landlock")
		if err != nil {
			return err
		}
		_, err = entry.Build(box.ProviderConfig{})
		return err
	},
	podman: func() error { return exec.Command("podman", "info").Run() },
	image:  func(image string) error { return exec.Command("podman", "image", "exists", image).Run() },
}

// enabled says whether the variable is 1; otherwise the test is skipped.
func enabled(t Skipper, name, what string) bool {
	t.Helper()
	if os.Getenv(name) != "1" {
		t.Skipf("%s is not 1: set it to run the tests that need %s", name, what)
		return false
	}
	return true
}

// RequireLandlock skips the test unless AF_TEST_LANDLOCK is 1 and this host can give a
// landlock box: Linux with Landlock ABI 4 or newer.
func RequireLandlock(t Skipper) {
	t.Helper()
	if !enabled(t, LandlockVar, "a real landlock box (Linux, Landlock ABI 4 or newer)") {
		return
	}
	if err := probe.landlock(); err != nil {
		t.Skipf("%s=1 is set, but this host cannot give a landlock box: %v", LandlockVar, err)
	}
}

// RequirePodman skips the test unless AF_TEST_PODMAN is 1, podman info answers and the
// image is there.
func RequirePodman(t Skipper, image string) {
	t.Helper()
	if !enabled(t, PodmanVar, "a real podman box (a reachable engine and the tools image)") {
		return
	}
	if err := probe.podman(); err != nil {
		t.Skipf("%s=1 is set, but podman info fails: %v", PodmanVar, err)
		return
	}
	if err := probe.image(image); err != nil {
		t.Skipf("%s=1 is set, but the image %s is not there (%v): build it with make build-tools-container, or name another with AF_SANDBOX_IMAGE", PodmanVar, image, err)
	}
}
