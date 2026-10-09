package agentrun

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/outline"
	"github.com/agentfox/agentkit-go/tools"
)

// The two names DetectSymbolBackend reports, which are the Detail of a
// preflight "symbol_backend" check.
const (
	SymbolBackendTreeSitter = "tree-sitter"
	SymbolBackendGoOnly     = "go-only"
)

// symbolProbeTimeout bounds the one outline the probe makes.
const symbolProbeTimeout = 5 * time.Second

// symbolProbeSource is the file the probe outlines: one Python function, a
// language only the tree-sitter backend outlines.
var symbolProbeSource = []byte("def probe():\n    pass\n")

// DetectSymbolBackend reports which backend file_outline and find_symbol will
// use for languages other than Go: SymbolBackendTreeSitter when AgentKit was
// built with cgo and parses them in-process, SymbolBackendGoOnly when it was
// not and only Go files (go/ast) have an outline.
//
// AgentKit exposes no detection function, so the probe builds the tools the
// way registeredTools does, confirms file_outline is among them, and outlines
// a small Python source with AgentKit's own outline package (the one
// file_outline uses), reading the backend it reports, so the decision cannot
// diverge from AgentKit's. An error means the detection itself failed: no
// workspace, or the file tools could not be built.
//
// It is a variable so a test can simulate a failing detection.
var DetectSymbolBackend = detectSymbolBackend

func detectSymbolBackend(ws *tools.Workspace) (string, error) {
	if ws == nil {
		return "", errors.New("no workspace configured")
	}
	built, err := tools.All(tools.Options{Workspace: ws})
	if err != nil {
		return "", fmt.Errorf("building the file tools: %w", err)
	}
	if !slices.ContainsFunc(built, func(t core.Tool) bool { return t.Name == "file_outline" }) {
		return "", errors.New("the file tools do not include file_outline")
	}
	ctx, cancel := context.WithTimeout(context.Background(), symbolProbeTimeout)
	defer cancel()
	f, err := outline.Outline(ctx, filepath.Join(ws.Root, "probe.py"), symbolProbeSource, outline.Options{})
	if err != nil || f.Backend != outline.BackendTreeSitter {
		return SymbolBackendGoOnly, nil
	}
	return SymbolBackendTreeSitter, nil
}
