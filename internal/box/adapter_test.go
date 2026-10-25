package box

import (
	"testing"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
)

func TestNetworkFor_PrefersWhatTheRequestAsksAndTakesWhatTheProviderHas(t *testing.T) {
	both := sandbox.Capabilities{Network: []sandbox.NetworkMode{sandbox.NetworkNone, sandbox.NetworkUnrestricted}}
	openOnly := sandbox.Capabilities{Network: []sandbox.NetworkMode{sandbox.NetworkUnrestricted}}
	for _, tc := range []struct {
		want string
		caps sandbox.Capabilities
		mode sandbox.NetworkMode
	}{
		{NetworkNone, both, sandbox.NetworkNone},
		{NetworkOpen, both, sandbox.NetworkUnrestricted},
		{NetworkHost, both, sandbox.NetworkNone},
		{"", both, sandbox.NetworkNone},
		{"", openOnly, sandbox.NetworkUnrestricted},
		{NetworkNone, openOnly, sandbox.NetworkUnrestricted},
		{NetworkOpen, sandbox.Capabilities{}, sandbox.Network{}.Mode},
	} {
		if got := networkFor(tc.caps, tc.want); got.Mode != tc.mode {
			t.Errorf("networkFor(%v, %q) = %v, want %v", tc.caps.Network, tc.want, got.Mode, tc.mode)
		}
	}
}

func TestNetworkName_IsTheRequestsElseHostForAnUnconfinedProviderElseTheMode(t *testing.T) {
	confined, unconfined := Entry{}, Entry{Unconfined: true}
	for _, tc := range []struct {
		requested string
		entry     Entry
		mode      sandbox.NetworkMode
		want      string
	}{
		{NetworkOpen, unconfined, sandbox.NetworkUnrestricted, NetworkOpen},
		{NetworkNone, confined, sandbox.NetworkUnrestricted, NetworkNone},
		{"", unconfined, sandbox.NetworkUnrestricted, NetworkHost},
		{"", confined, sandbox.NetworkNone, NetworkNone},
		{"", confined, sandbox.NetworkUnrestricted, NetworkOpen},
		{"", confined, sandbox.NetworkAllowlist, sandbox.NetworkAllowlist.String()},
	} {
		if got := networkName(tc.requested, tc.entry, tc.mode); got != tc.want {
			t.Errorf("networkName(%q, unconfined %v, %v) = %q, want %q", tc.requested, tc.entry.Unconfined, tc.mode, got, tc.want)
		}
	}
}
