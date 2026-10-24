package box

import (
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"
	"github.com/agent-fox-dev/agentkit-go/sandbox/podman"
)

// This file is the one place that names the sandbox module of agentkit-go by
// anything but its types: the providers it offers, how a host directory is
// synced in, and which network a spec asks for.

// localEntry is the `local` provider: the hands' agent in this process, with
// this process's privileges.
func localEntry() Entry {
	return Entry{Name: "local", Unconfined: true, New: local.Unisolated}
}

// newPodmanProvider is agentkit's podman provider, which runs the tools of a box in a
// container of the engine on PATH. A test replaces it.
var newPodmanProvider = func() (sandbox.Provider, error) { return podman.New(podman.Options{}) }

// syncSource is the source of a sync: the host's work tree with its .git, as
// the hands' ignore rules see it, and what the host no longer has removed.
func syncSource(hostDir string) sandbox.Source {
	return sandbox.LocalDir{Path: hostDir, Delete: true}
}

// networkFor is the network a spec asks for: an unrestricted one for NetworkOpen
// and none for anything else, when the provider can honour that, and otherwise the
// first mode it offers (the local provider offers only an unrestricted one, which is
// part of why it raises box_unconfined).
func networkFor(caps sandbox.Capabilities, want string) sandbox.Network {
	prefer := sandbox.NetworkNone
	if want == NetworkOpen {
		prefer = sandbox.NetworkUnrestricted
	}
	for _, mode := range caps.Network {
		if mode == prefer {
			return sandbox.Network{Mode: prefer}
		}
	}
	if len(caps.Network) > 0 {
		return sandbox.Network{Mode: caps.Network[0]}
	}
	return sandbox.Network{}
}

// networkName is the network of a box as its ledger entry and the result of a run
// say it: the one the request asked for, else host for a provider that enforces
// nothing, else the mode the sandbox was given.
func networkName(requested string, entry Entry, mode sandbox.NetworkMode) string {
	switch {
	case requested != "":
		return requested
	case entry.Unconfined:
		return NetworkHost
	case mode == sandbox.NetworkNone:
		return NetworkNone
	case mode == sandbox.NetworkUnrestricted:
		return NetworkOpen
	}
	return mode.String()
}
