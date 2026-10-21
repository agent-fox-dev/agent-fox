package box

import (
	"context"
	"fmt"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
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

// landlockEntry, podmanEntry and noneEntry are the providers that confine a
// process on Linux, run a container, and work on the checkout itself. Each is a
// provider that cannot create a box until its scope is wired.
func landlockEntry() Entry { return Entry{Name: "landlock", New: unwired("landlock")} }
func podmanEntry() Entry   { return Entry{Name: "podman", New: unwired("podman")} }
func noneEntry() Entry {
	return Entry{Name: "none", Unconfined: true, InPlace: true, New: unwired("none")}
}

// unwired is the provider of a name that is registered and whose box is not made
// yet: it refuses to create one, as a sandbox failure.
func unwired(name string) func() sandbox.Provider {
	return func() sandbox.Provider { return unwiredProvider{name: name} }
}

type unwiredProvider struct{ name string }

func (p unwiredProvider) Capabilities() sandbox.Capabilities {
	return sandbox.Capabilities{Provider: p.name}
}

func (p unwiredProvider) Create(context.Context, sandbox.Spec) (sandbox.Sandbox, error) {
	return nil, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("the %s provider cannot make a box yet", p.name)}
}

func (unwiredProvider) Reap(context.Context) ([]string, error) { return nil, nil }

// syncSource is the source of a sync: the host's work tree with its .git, as
// the hands' ignore rules see it, and what the host no longer has removed.
func syncSource(hostDir string) sandbox.Source {
	return sandbox.LocalDir{Path: hostDir, Delete: true}
}

// networkFor is the network a spec asks for: none when the provider can honour
// that, and otherwise the first mode it offers (the local provider offers only
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
