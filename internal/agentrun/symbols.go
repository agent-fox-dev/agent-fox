package agentrun

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/agentfox/agentkit-go/core"
	"github.com/agentfox/agentkit-go/tools"
)

// The two names DetectSymbolBackend reports, which are the Detail of a
// preflight "symbol_backend" check.
const (
	SymbolBackendCtags      = "ctags"
	SymbolBackendHeuristics = "heuristics"
)

// symbolProbeTimeout bounds the one `ctags --version` run the probe makes.
const symbolProbeTimeout = 5 * time.Second

// DetectSymbolBackend reports which backend file_outline and find_symbol will
// use in a workspace: SymbolBackendCtags when universal-ctags is installed and
// usable, SymbolBackendHeuristics when it is not.
//
// AgentKit exposes no detection function, so the probe builds the tools the
// way registeredTools does, confirms file_outline is among them, and runs
// AgentKit's own ctags runner (the one file_outline uses by default) with
// --version. The runner locates ctags and confirms it is Universal Ctags, and
// answers tools.ErrCtagsUnavailable when it is not, so the decision cannot
// diverge from AgentKit's. A ctags that cannot run counts as unavailable. An
// error means the detection itself failed: no workspace, or the file tools
// could not be built.
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
	_, err = tools.CtagsRunner(nil)(ctx, []string{"--version"})
	if err != nil {
		// Absent, not universal, or failing to run: file_outline cannot use
		// it either, so the heuristics are the backend.
		return SymbolBackendHeuristics, nil
	}
	return SymbolBackendCtags, nil
}
