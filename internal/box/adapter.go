package box

import (
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"
)

// This file is the one place that names the sandbox module of agentkit-go by
// anything but its types: the providers it offers, how a host directory is
// synced in, and which network a spec asks for.

// localEntry is the `local` provider: the hands' agent in this process, with
// this process's privileges.
func localEntry() Entry {
	return Entry{Name: "local", Unconfined: true, New: local.Unisolated}
}

// syncSource is the source of a sync: the host's work tree with its .git, as
// the hands' ignore rules see it, and what the host no longer has removed.
func syncSource(hostDir string) sandbox.Source {
	return sandbox.LocalDir{Path: hostDir, Delete: true}
}

// networkFor is the network a spec asks for: none when the provider can honour
// that, and otherwise the only mode it offers (the local provider offers only
// an unrestricted one, which is part of why it raises box_unconfined).
func networkFor(caps sandbox.Capabilities) sandbox.Network {
	for _, mode := range caps.Network {
		if mode == sandbox.NetworkNone {
			return sandbox.Network{Mode: sandbox.NetworkNone}
		}
	}
	if len(caps.Network) > 0 {
		return sandbox.Network{Mode: caps.Network[0]}
	}
	return sandbox.Network{}
}
