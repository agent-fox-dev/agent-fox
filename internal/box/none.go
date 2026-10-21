package box

import (
	"context"
	"fmt"
	"os"

	"github.com/agent-fox-dev/agentkit-go/sandbox"

	"github.com/agent-fox-dev/agentfox/internal/submission"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// noneEntry is the provider that is no box: the brain works on the checkout itself.
// Open makes its session without a provider, so the entry has none to make.
func noneEntry() Entry { return Entry{Name: "none", Unconfined: true, InPlace: true} }

// noneMessage is what box_unconfined says of a brain on the checkout itself.
const noneMessage = "the brain works on the checkout itself, with no copy of it and no box around it: the tools reach every file that this process can, and the hook is the only guard"

// agentFoxDir is where a session in place writes, and the file in it that is never
// overwritten.
const gitignorePath = agentDir + ".gitignore"

// openInPlace makes the session of the none provider over the checkout: the hands'
// tools work on the checkout, the view of it is read-only, and the run files are
// written to it by the host. Nothing is copied in or out of it, and nothing in it is
// changed by opening it.
func openInPlace(req Request, entry Entry, hostDir string) (*Session, error) {
	set, err := handsOver(hostDir)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(hostDir)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot open the checkout %s: %v", hostDir, err)}
	}
	spec := sandbox.Spec{
		Workdir: hostDir, Env: req.Env, TTL: req.TTL,
		Labels:  map[string]string{"af.run_id": req.RunID, "af.tool": req.Tool},
		Network: sandbox.Network{Mode: sandbox.NetworkUnrestricted},
	}
	s := &Session{
		req: req, sb: &inPlace{dir: hostDir, spec: spec}, tools: set, provider: entry.Name, hostDir: hostDir,
		network: NetworkHost, none: true, root: root,
	}
	s.record("box_open", map[string]any{"provider": entry.Name, "image": "", "network": s.network, "placement": req.Placement})
	if req.Warn != nil {
		req.Warn("box_unconfined", noneMessage)
	}
	return s, nil
}

// writeInPlace writes a run file to the checkout, atomically, through the root so
// that no link in the way leads out of it. A .gitignore that is there is the
// person's or an earlier run's, and is left as it is.
func (s *Session) writeInPlace(ctx context.Context, rel string, data []byte) error {
	if rel == gitignorePath {
		if _, err := s.root.Stat(rel); err == nil {
			return nil
		}
	}
	if err := submission.RootWriter(s.root)(ctx, rel, data); err != nil {
		return &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot write %s in the checkout: %v", rel, err)}
	}
	return nil
}
