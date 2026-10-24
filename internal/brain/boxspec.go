package brain

import (
	"os"
	"slices"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/box"
)

// Placement is where a brain runs for one run.
type Placement string

// The placements: the host, over the box's tools (af's process); and a box, as a
// process inside it.
const (
	PlacementHost Placement = "host"
	PlacementBox  Placement = "box"
)

// The settings that choose the image of a podman box.
const (
	sandboxImageVar = "AF_SANDBOX_IMAGE"
	// DefaultImage is the image of a podman box without AF_SANDBOX_IMAGE: the
	// sandbox image with af in it.
	DefaultImage = "quay.io/agentfox/tools:latest"
)

// ttlMargin is the time a box lives beyond the deadline of the command that opened
// it, so that closing it is not racing the end of its lifetime.
const ttlMargin = 10 * time.Minute

// shellTools are the tools of the hands that can run a command, and so reach
// whatever the network offers.
var shellTools = []string{"execute", "run_command"}

// PlacementOf is where a brain runs on a provider. A driver is a process in a box that
// isolates. Every other brain, and a driver under a provider that does not isolate,
// runs on the host.
func PlacementOf(e Entry, provider box.Entry) Placement {
	if e.DriverInBox && provider.Isolates() {
		return PlacementBox
	}
	return PlacementHost
}

// BoxParams is what the request for the box of a run is computed from.
type BoxParams struct {
	// Brain is the entry of the brain, and Provider the provider of the box.
	Brain    Entry
	Provider box.Entry
	// Tools are the tools the brief of the agent allows, by name. Nil is every tool
	// of the hands, a shell among them, as a fix brief allows.
	Tools []string
	// Getenv reads the host's environment; nil is os.LookupEnv.
	Getenv Getenv
	// Deadline is the deadline of the command; the zero time is none.
	Deadline time.Time
	// Defaulted says the provider was chosen by AF_SANDBOX or by the platform, and not
	// with --sandbox (Settings.SandboxSource is not SandboxFlag).
	Defaulted bool

	// The rest is passed to the request as it is: the checkout, the run and the
	// tool that label the box, the ledger and the warnings of the run, and the
	// registry the provider is found in.
	HostDir, RunID, Tool string
	Record               func(kind string, data any) error
	Warn                 func(code, message string)
	Registry             *box.Registry
	// Labels are put on the box beside the run and the tool.
	Labels map[string]string
}

// BoxRequest is the request for the box of a run, computed from the brain, the
// provider and the tools of the agent, in this one place.
//
// Env holds credentials and nothing else: the host values of the names the brain is
// given for its placement that are set, which is none for a brain on the host and
// the key of the model vendor for a process. No other host variable reaches the box
// by any field. The network is none for a brain on the host that has no tool to run
// a command, and open otherwise, and host, which says that nothing is enforced,
// under a provider that does not isolate. The image is AF_SANDBOX_IMAGE, or the
// tools image, and only for podman. The box lives until the deadline of the command
// and ten minutes more.
func BoxRequest(p BoxParams) box.Request {
	placement := PlacementOf(p.Brain, p.Provider)
	req := box.Request{
		Provider:  p.Provider.Name,
		HostDir:   p.HostDir,
		RunID:     p.RunID,
		Tool:      p.Tool,
		Env:       BuildBoxEnv(p.Brain, placement, p.Getenv),
		Network:   networkFor(p, placement),
		Placement: string(placement),
		Defaulted: p.Defaulted,
		Record:    p.Record,
		Warn:      p.Warn,
		Registry:  p.Registry,
		Labels:    p.Labels,
	}
	if p.Provider.Name == "podman" {
		req.Image = DefaultImage
		get := p.Getenv
		if get == nil {
			get = os.LookupEnv
		}
		if v, ok := get(sandboxImageVar); ok && v != "" {
			req.Image = v
		}
	}
	if !p.Deadline.IsZero() {
		req.TTL = max(time.Until(p.Deadline), 0) + ttlMargin
	}
	return req
}

// networkFor is the network of a box: host under a provider that enforces nothing,
// none for a brain on the host that cannot run a command, and open for the rest.
func networkFor(p BoxParams, placement Placement) string {
	switch {
	case !p.Provider.Isolates():
		return box.NetworkHost
	case placement == PlacementHost && !hasShell(p.Tools):
		return box.NetworkNone
	}
	return box.NetworkOpen
}

// hasShell says whether the tools can run a command. No list is every tool.
func hasShell(tools []string) bool {
	if tools == nil {
		return true
	}
	return slices.ContainsFunc(tools, func(name string) bool { return slices.Contains(shellTools, name) })
}
