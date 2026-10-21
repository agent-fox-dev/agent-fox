package box

import (
	"fmt"
	"runtime"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// goos is the operating system the checks of a provider are made for; a test
// replaces it.
var goos = runtime.GOOS

// landlockMinABI is the first Landlock ABI with rules for the network. A box that
// is asked for no network must not leave one open, so a host with less has no
// landlock box: the provider fails closed.
const landlockMinABI = 4

// LandlockABI reads the Landlock ABI of the host, or -1 when it has none; a test
// replaces it.
var LandlockABI = landlockABI

// landlockEntry is the provider that confines a process of the host with Landlock.
func landlockEntry() Entry { return Entry{Name: "landlock", Build: buildLandlock} }

// buildLandlock makes the provider after the checks that decide whether this host can
// confine a box: it must be Linux, and its kernel must have Landlock ABI 4 or newer.
// A host that fails them is not given another provider, and nothing is created.
func buildLandlock(cfg ProviderConfig) (sandbox.Provider, error) {
	if goos != "linux" {
		return nil, landlockRefusal("landlock needs Linux")
	}
	abi := LandlockABI()
	if abi < 1 {
		return nil, landlockRefusal(fmt.Sprintf("landlock needs Landlock ABI %d or newer, and this host has no Landlock: its ABI is absent", landlockMinABI))
	}
	if abi < landlockMinABI {
		return nil, landlockRefusal(fmt.Sprintf("landlock needs Landlock ABI %d or newer, and this host has Landlock ABI %d", landlockMinABI, abi))
	}
	p, err := newLandlockProvider(cfg)
	if err != nil {
		return nil, landlockRefusal(fmt.Sprintf("the landlock provider cannot be made: %v", err))
	}
	return p, nil
}

// landlockRefusal is the failure of a host that cannot give a landlock box: it is
// not retryable, and podman is the provider to ask for instead.
func landlockRefusal(message string) error {
	return &toolio.Failure{
		Category: toolio.CategorySandbox,
		Message:  message,
		FixHint:  &toolio.FixHint{Flag: "--sandbox", Suggest: "podman"},
	}
}
